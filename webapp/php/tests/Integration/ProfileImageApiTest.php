<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\ImageSignature;
use Isureal\Repository\UserRepository;
use Psr\Http\Message\ResponseInterface;

final class ProfileImageApiTest extends IntegrationTestCase
{
    private const string PATH = '/api/me/profile-image';

    public function testProfileImage(): void
    {
        $tokens = [];
        foreach (['owner', 'viewer'] as $id) {
            $this->insertUser($id, id: $id);
            $tokens[$id] = $this->login($id);
        }
        $data = self::jpeg();

        self::assertStatus(401, $this->put(null, 'image/jpeg', $data));
        self::assertStatus(404, $this->request('DELETE', self::PATH, $tokens['owner']));
        self::assertStatus(415, $this->put($tokens['owner'], 'image/png', $data));
        $this->assertImageCount(0);

        self::assertStatus(204, $this->put($tokens['owner'], 'image/jpeg', $data));
        $old = $this->currentImageId();
        $oldUrl = $this->signedUrl('owner', $old);
        $this->assertImageCount(1);
        foreach (['owner', 'viewer'] as $actor) {
            $image = $this->request('GET', $oldUrl, $tokens[$actor]);
            self::assertStatus(200, $image);
            self::assertSame('image/jpeg', $image->getHeaderLine('Content-Type'));
            // アップロードしたものと同じバイト列が返る
            self::assertSame($data, (string) $image->getBody());
        }
        self::assertStatus(200, $this->request('GET', $oldUrl));
        // 署名のない URL では取得できない
        self::assertStatus(403, $this->request('GET', "/api/images/{$old}", $tokens['owner']));

        // ブロックしていてもプロフィール画像は取得できる
        foreach ([['owner', 'viewer'], ['viewer', 'owner']] as [$blocker, $blocked]) {
            $this->db->execute(
                "INSERT INTO block_relations (id, blocker_id, blocked_id) VALUES ('00000000-0000-0000-0000-000000000001', :blocker, :blocked)",
                ['blocker' => $blocker, 'blocked' => $blocked],
            );
            self::assertStatus(200, $this->request('GET', $oldUrl, $tokens['viewer']));
            self::assertStatus(200, $this->request('GET', $oldUrl, $tokens['owner']));
            $this->db->execute('DELETE FROM block_relations');
        }

        // 古い画像の削除に失敗したら差し替えも削除もロールバックする
        $this->db->executeScript(<<<'SQL'
            CREATE OR REPLACE FUNCTION reject_image_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject image deletion'; END $$;
            CREATE TRIGGER reject_image_delete BEFORE DELETE ON image_objects FOR EACH ROW EXECUTE FUNCTION reject_image_delete();
            SQL);
        try {
            self::assertStatus(500, $this->put($tokens['owner'], 'image/jpeg', $data));
            self::assertSame($old, $this->currentImageId(), 'replacement failed to roll back');
            $this->assertImageCount(1);
            self::assertStatus(500, $this->request('DELETE', self::PATH, $tokens['owner']));
            self::assertSame($old, $this->currentImageId(), 'deletion failed to roll back');
            $this->assertImageCount(1);
        } finally {
            // スキーマを作り直しても関数は残るので消す
            $this->db->executeScript('DROP FUNCTION reject_image_delete() CASCADE');
        }

        self::assertStatus(204, $this->put($tokens['owner'], 'image/jpeg', $data));
        $fresh = $this->currentImageId();
        $freshUrl = $this->signedUrl('owner', $fresh);
        self::assertNotSame($old, $fresh, 'image ID unchanged');
        $this->assertImageCount(1);
        self::assertStatus(403, $this->request('GET', $oldUrl, $tokens['viewer']));
        self::assertStatus(200, $this->request('GET', $freshUrl, $tokens['viewer']));

        $this->db->execute("UPDATE users SET status = 'deleted' WHERE id = 'owner'");
        self::assertStatus(403, $this->request('GET', $freshUrl, $tokens['viewer']));
        $this->db->execute("UPDATE users SET status = 'active' WHERE id = 'owner'");

        self::assertStatus(204, $this->request('DELETE', self::PATH, $tokens['owner']));
        $this->assertImageCount(0);
        self::assertNull($this->db->fetchValue("SELECT profile_image_id FROM users WHERE id = 'owner'"), 'profile reference retained');
        self::assertStatus(403, $this->request('GET', $freshUrl, $tokens['viewer']));
        self::assertStatus(404, $this->request('DELETE', self::PATH, $tokens['owner']));
    }

    private function put(?string $token, string $mediaType, string $data): ResponseInterface
    {
        return $this->requestRaw('PUT', self::PATH, $token, $data, ['Content-Type' => $mediaType]);
    }

    private function currentImageId(): string
    {
        $id = $this->db->fetchValue("SELECT profile_image_id FROM users WHERE id = 'owner'");
        self::assertIsString($id);
        return $id;
    }

    private function assertImageCount(int $expected): void
    {
        self::assertSame($expected, $this->db->fetchValue('SELECT count(*) FROM image_objects'), 'image_objects count');
    }

    private function signedUrl(string $userId, string $imageId): string
    {
        $user = new UserRepository($this->db)->find($userId);
        self::assertNotNull($user);
        return ImageSignature::url($user, $imageId);
    }
}
