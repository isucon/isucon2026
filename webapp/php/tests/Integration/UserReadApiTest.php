<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Ids;
use Psr\Http\Message\ResponseInterface;

final class UserReadApiTest extends IntegrationTestCase
{
    private string $token;

    public function testSearchAndProfile(): void
    {
        foreach (['viewer', 'target'] as $id) {
            $this->db->execute(
                "INSERT INTO users (id, username, secret_key_hash, display_name, bio, region) VALUES (:id, :id, :hash, 'Café Straße', 'private', 'asia')",
                ['id' => $id, 'hash' => Ids::sha256(self::SECRET_KEY)],
            );
        }
        $this->token = $this->login('viewer');

        self::assertStatus(401, $this->request('GET', '/api/users?q=x'));
        self::assertStatus(401, $this->request('GET', '/api/users/target'));
        self::assertStatus(400, $this->get('/api/users'));
        self::assertStatus(400, $this->get('/api/users?q=x&beforeId=x'));
        self::assertStatus(404, $this->get('/api/users/missing'));

        // 正規化と大文字小文字の畳み込みをしてから部分一致で探す
        foreach (["CAFE\u{0301}", 'STRASSE', 'TARGET'] as $q) {
            $items = $this->search($q)['items'];
            self::assertCount(1, $items, $q);
            self::assertSame('target', $items[0]['id'], $q);
            self::assertSame('', $items[0]['bio'], $q);
        }
        // LIKE の特殊文字はそのままの文字として扱う
        foreach (['%', '_', '\\'] as $q) {
            self::assertSame([], $this->search($q)['items'], $q);
        }

        // 友達でなければ bio は見えない
        self::assertSame('', $this->profile()['bio'], 'nonfriend bio exposed');
        $this->db->execute("INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ('00000000-0000-0000-0000-000000000001', 'target', 'viewer')");
        self::assertSame('private', $this->profile()['bio'], 'friend bio hidden');
        self::assertSame('', $this->search('target')['items'][0]['bio'], 'search disclosed regular bio');
        $this->db->execute('DELETE FROM friendships');
        // 認証済みアカウントの bio は誰にでも見える
        $this->db->execute("UPDATE users SET account_type = 'certified_public' WHERE id = 'target'");
        self::assertSame('private', $this->profile()['bio'], 'certified bio hidden');
        self::assertSame('private', $this->search('target')['items'][0]['bio'], 'certified bio hidden');

        foreach ([['viewer', 'target'], ['target', 'viewer']] as [$blocker, $blocked]) {
            $this->db->execute(
                "INSERT INTO block_relations (id, blocker_id, blocked_id) VALUES ('00000000-0000-0000-0000-000000000002', :blocker, :blocked)",
                ['blocker' => $blocker, 'blocked' => $blocked],
            );
            self::assertSame([], $this->search('target')['items'], 'blocked user searchable');
            self::assertStatus(404, $this->get('/api/users/target'));
            $this->db->execute('DELETE FROM block_relations');
        }

        $this->db->execute("UPDATE users SET status = 'deleted' WHERE id = 'target'");
        self::assertStatus(404, $this->get('/api/users/target'));
        self::assertSame([], $this->search('target')['items'], 'deleted user searchable');

        for ($i = 0; $i < 52; $i++) {
            $this->db->execute(
                "INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES (:id, 'page', :hash, 'Page', 'asia')",
                ['id' => sprintf('page-%02d', $i), 'hash' => Ids::sha256(self::SECRET_KEY)],
            );
        }
        $first = $this->search('page');
        self::assertCount(50, $first['items']);
        self::assertTrue($first['hasMore']);
        $last = $first['items'][49];
        $response = $this->get('/api/users?' . http_build_query(['q' => 'page', 'beforeUsername' => $last['username'], 'beforeId' => $last['id']]));
        self::assertStatus(200, $response);
        $second = self::json($response);
        self::assertCount(2, $second['items']);
        self::assertFalse($second['hasMore']);
        self::assertGreaterThan($last['id'], $second['items'][0]['id']);
    }

    private function get(string $path): ResponseInterface
    {
        return $this->request('GET', $path, $this->token);
    }

    /**
     * @return array<mixed>
     */
    private function search(string $q): array
    {
        $response = $this->get('/api/users?q=' . rawurlencode($q));
        self::assertStatus(200, $response, $q);
        return self::json($response);
    }

    /**
     * @return array<mixed>
     */
    private function profile(): array
    {
        $response = $this->get('/api/users/target');
        self::assertStatus(200, $response);
        return self::json($response);
    }
}
