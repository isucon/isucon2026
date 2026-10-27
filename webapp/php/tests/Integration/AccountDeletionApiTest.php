<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\ImageSignature;
use Isureal\Repository\UserRepository;

final class AccountDeletionApiTest extends IntegrationTestCase
{
    public function testAccountDeletion(): void
    {
        $registration = ['username' => 'retiring', 'displayName' => 'Retiring', 'region' => 'asia'];
        $registered = $this->request('POST', '/api/auth/register', json: $registration);
        self::assertStatus(201, $registered);
        $result = self::json($registered);
        $id = $result['user']['id'];
        $credentials = ['username' => 'retiring', 'secretKey' => $result['secretKey']];
        $login = function () use ($credentials): string {
            $response = $this->request('POST', '/api/auth/login', json: $credentials);
            self::assertStatus(200, $response);
            return self::json($response)['token'];
        };
        $old = $login();
        $current = $login();
        $viewer = $this->login($this->insertUser('viewer', id: 'viewer'));
        $this->db->execute(
            "INSERT INTO image_objects (id, owner_user_id, data, media_type)
             VALUES ('profile', :id, 'profile', 'image/jpeg'), ('post-image', :id, 'post', 'image/jpeg'), ('viewer-image', 'viewer', 'post', 'image/jpeg')",
            ['id' => $id],
        );
        $this->db->execute("UPDATE users SET profile_image_id = 'profile' WHERE id = :id", ['id' => $id]);
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline)
             VALUES ('window', 'asia', '2026-09-24', clock_timestamp(), clock_timestamp() + interval '1 day')",
        );
        $this->db->execute(
            "INSERT INTO posts (id, author_id, posting_window_id, image_id, visibility, location_kind, posted_at)
             VALUES ('post', :id, 'window', 'post-image', 'public', 'none', clock_timestamp()),
                    ('viewer-post', 'viewer', 'window', 'viewer-image', 'public', 'none', clock_timestamp())",
            ['id' => $id],
        );
        $this->db->execute(
            "INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ('00000000-0000-0000-0000-000000000001', :low, :high)",
            ['low' => min($id, 'viewer'), 'high' => max($id, 'viewer')],
        );
        $owner = new UserRepository($this->db)->mustFind($id);
        $profileUrl = ImageSignature::url($owner, 'profile');
        $postUrl = ImageSignature::url($owner, 'post-image');

        $path = '/api/me/account-deletion';
        self::assertStatus(401, $this->request('POST', $path, headers: ['Authorization' => 'Bearer ']));
        self::assertStatus(401, $this->request('POST', $path, $old));
        self::assertStatus(200, $this->request('GET', '/api/posts/post', $viewer));
        self::assertStatus(200, $this->request('GET', $profileUrl));

        // 失敗したら退会しない
        $this->db->executeScript(<<<'SQL'
            CREATE FUNCTION reject_retirement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.reason='account_deletion' THEN RAISE EXCEPTION 'reject retirement'; END IF; RETURN NEW; END $$;
            CREATE TRIGGER reject_retirement BEFORE UPDATE ON login_sessions FOR EACH ROW EXECUTE FUNCTION reject_retirement();
            SQL);
        try {
            self::assertStatus(500, $this->request('POST', $path, $current));
            self::assertSame(['status' => 'active', 'deleted_at' => null], $this->db->fetchOne('SELECT status, deleted_at FROM users WHERE id = :id', ['id' => $id]), 'rollback state');
            self::assertStatus(200, $this->request('GET', '/api/me', $current));
        } finally {
            $this->db->executeScript('DROP FUNCTION reject_retirement() CASCADE');
        }

        $deleted = $this->request('POST', $path, $current);
        self::assertStatus(204, $deleted);
        self::assertSame('', (string) $deleted->getBody(), 'unexpected response body');
        $state = $this->db->fetchOne('SELECT status, deleted_at FROM users WHERE id = :id', ['id' => $id]);
        self::assertSame('deleted', $state['status'] ?? null);
        self::assertNotNull($state['deleted_at'] ?? null);
        self::assertSame(0, $this->db->fetchValue('SELECT count(*) FROM login_sessions WHERE user_id = :id AND active', ['id' => $id]));
        self::assertSame('account_deletion', $this->db->fetchValue('SELECT reason FROM login_sessions WHERE user_id = :id ORDER BY created_at DESC LIMIT 1', ['id' => $id]));

        self::assertStatus(401, $this->request('GET', '/api/me', $current));
        self::assertStatus(401, $this->request('POST', $path, $current));
        self::assertStatus(401, $this->request('POST', '/api/auth/login', json: $credentials));
        self::assertStatus(409, $this->request('POST', '/api/auth/register', json: $registration));
        self::assertStatus(404, $this->request('GET', "/api/users/{$id}", $viewer));
        self::assertStatus(404, $this->request('GET', '/api/posts/post', $viewer));
        self::assertStatus(403, $this->request('GET', $postUrl));
        self::assertStatus(403, $this->request('GET', $profileUrl));
        $search = $this->request('GET', '/api/users?q=retiring', $viewer);
        self::assertStatus(200, $search);
        self::assertSame([], self::json($search)['items'], 'retired user searchable');
        $friends = $this->request('GET', '/api/relationships/friends', $viewer);
        self::assertStatus(200, $friends);
        self::assertSame([], self::json($friends)['items'], 'retired friend visible');
        self::assertSame(1, $this->db->fetchValue('SELECT count(*) FROM posts WHERE author_id = :id', ['id' => $id]), 'post removed');
        self::assertStatus(200, $this->request('GET', '/api/me', $viewer));
    }
}
