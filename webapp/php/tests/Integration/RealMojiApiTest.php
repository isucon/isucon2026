<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Psr\Http\Message\ResponseInterface;

final class RealMojiApiTest extends IntegrationTestCase
{
    private const string PATH = '/api/posts/author-post/realmojis';

    /**
     * 画像として扱われるが JPEG ではないバイト列。そのまま保存されることを確かめる
     */
    private const string PAYLOAD = "unchanged image bytes\x00\xff";

    /**
     * @var array<string, string>
     */
    private array $tokens = [];

    public function testRealMojis(): void
    {
        foreach (['author', 'reactor', 'stranger'] as $id) {
            $this->insertUser($id, id: $id);
            $this->tokens[$id] = $this->login($id);
        }
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline)
             VALUES ('window', 'asia', '2026-09-24', clock_timestamp(), clock_timestamp() + interval '1 day')",
        );
        foreach (['author', 'reactor'] as $id) {
            $this->db->execute("INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES (:id, :owner, 'photo', 'image/jpeg')", ['id' => "{$id}-image", 'owner' => $id]);
            $this->db->execute(
                "INSERT INTO posts (id, author_id, posting_window_id, image_id, visibility, location_kind, posted_at)
                 VALUES (:id, :author, 'window', :image, 'public', 'none', clock_timestamp())",
                ['id' => "{$id}-post", 'author' => $id, 'image' => "{$id}-image"],
            );
        }

        self::assertStatus(401, $this->create(null));
        // 自分の投稿にはリアクションできない
        self::assertStatus(403, $this->create('author'));
        self::assertStatus(403, $this->create('stranger'));
        self::assertStatus(400, $this->requestMultipart('POST', self::PATH, $this->tokens['reactor'], ['reactionType' => 'bad'], ['image' => self::PAYLOAD]));
        self::assertStatus(400, $this->requestMultipart('POST', self::PATH, $this->tokens['reactor'], ['reactionType' => 'like'], []));
        self::assertSame([], $this->list()['items']);

        $first = $this->created($this->create('reactor'));
        $this->assertImage('author', $first, 200);
        $this->assertImage('reactor', $first, 200);
        $this->assertImage('stranger', $first, 200);
        $second = $this->created($this->create('reactor'));
        // 差し替えた古い画像は取得できない
        $this->assertImage('author', $first, 403);
        $items = $this->list()['items'];
        self::assertCount(1, $items, 'replacement list');
        self::assertSame($second['id'], $items[0]['id']);

        $this->db->executeScript(<<<'SQL'
            CREATE OR REPLACE FUNCTION reject_realmoji() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject realmoji'; END $$;
            CREATE TRIGGER reject_realmoji BEFORE INSERT ON realmojis FOR EACH ROW EXECUTE FUNCTION reject_realmoji();
            SQL);
        try {
            self::assertStatus(500, $this->create('reactor'));
            // 失敗したら差し替えも画像の保存もロールバックする
            $this->assertImage('author', $second, 200);
            self::assertSame(3, $this->db->fetchValue("SELECT count(*) FROM image_objects WHERE owner_user_id = 'reactor'"), 'image rollback count');
        } finally {
            // スキーマを作り直しても関数は残るので消す
            $this->db->executeScript('DROP FUNCTION reject_realmoji() CASCADE');
        }

        // 同時に差し替えても、有効なリアクションは 1 つだけになる
        $statuses = $this->createConcurrently('reactor', 4);
        self::assertSame([201, 201, 201, 201], $statuses, implode(',', $statuses));
        $items = $this->list()['items'];
        self::assertCount(1, $items, 'concurrent replacements');
        $active = $items[0];

        self::assertStatus(403, $this->delete('stranger', $active['id']));
        // 投稿者は他人のリアクションを消せる
        self::assertStatus(204, $this->delete('author', $active['id']));
        $this->assertImage('author', $active, 403);
        self::assertStatus(404, $this->delete('author', $active['id']));
        $third = $this->created($this->create('reactor'));
        self::assertStatus(204, $this->delete('reactor', $third['id']));

        foreach ([['author', 'reactor'], ['reactor', 'author']] as [$blocker, $blocked]) {
            $this->db->execute(
                "INSERT INTO block_relations (id, blocker_id, blocked_id) VALUES ('00000000-0000-0000-0000-000000000001', :blocker, :blocked)",
                ['blocker' => $blocker, 'blocked' => $blocked],
            );
            self::assertStatus(403, $this->create('reactor'));
            $this->db->execute('DELETE FROM block_relations');
        }
        $this->db->execute("UPDATE posts SET visibility = 'friends' WHERE id = 'author-post'");
        self::assertStatus(403, $this->create('reactor'));
        $this->db->execute("UPDATE posts SET visibility = 'public' WHERE id = 'author-post'");
        $current = $this->created($this->create('reactor'));

        // 投稿期間が終わるとリアクションも一覧もできないが、画像は取得できる
        $this->db->execute('UPDATE posting_windows SET ended_at = clock_timestamp()');
        self::assertStatus(403, $this->create('reactor'));
        self::assertStatus(403, $this->request('GET', self::PATH, $this->tokens['reactor']));
        $this->assertImage('reactor', $current, 200);
        $this->assertImage('author', $current, 200);
        $this->db->execute('UPDATE posting_windows SET ended_at = NULL');

        // 投稿を消すとリアクションの画像も取得できない
        $this->db->execute("UPDATE posts SET deleted_at = clock_timestamp() WHERE id = 'author-post'");
        self::assertStatus(403, $this->create('reactor'));
        $this->assertImage('author', $current, 403);
        $this->db->execute("UPDATE posts SET deleted_at = NULL WHERE id = 'author-post'");

        $this->db->execute('UPDATE realmojis SET deleted_at = clock_timestamp()');
        for ($i = 0; $i < 52; $i++) {
            $id = sprintf('page-%02d', $i);
            $this->insertUser($id, id: $id);
            $this->db->execute(
                "INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at)
                 VALUES (:id, 'author-post', :id, 'author-image', 'like', '2026-09-24T00:00:00Z')",
                ['id' => $id],
            );
        }
        $page = $this->list();
        self::assertCount(50, $page['items']);
        self::assertTrue($page['hasMore']);
        $last = $page['items'][49];
        $tail = $this->list('?' . http_build_query(['beforeCreatedAt' => $last['createdAt'], 'beforeId' => $last['id']]));
        self::assertCount(2, $tail['items']);
        self::assertFalse($tail['hasMore']);
        self::assertLessThan($last['id'], $tail['items'][0]['id']);
        self::assertStatus(400, $this->request('GET', self::PATH . '?beforeId=x', $this->tokens['author']));
    }

    private function create(?string $actor): ResponseInterface
    {
        $token = $actor === null ? null : $this->tokens[$actor];
        return $this->requestMultipart('POST', self::PATH, $token, ['reactionType' => 'heart'], ['image' => self::PAYLOAD]);
    }

    /**
     * @return array<mixed>
     */
    private function created(ResponseInterface $response): array
    {
        self::assertStatus(201, $response);
        return self::json($response);
    }

    /**
     * 投稿者が一覧を取得する
     *
     * @return array<mixed>
     */
    private function list(string $suffix = ''): array
    {
        $response = $this->request('GET', self::PATH . $suffix, $this->tokens['author']);
        self::assertStatus(200, $response);
        return self::json($response);
    }

    private function delete(string $actor, string $realMojiId): ResponseInterface
    {
        return $this->request('DELETE', "/api/realmojis/{$realMojiId}", $this->tokens[$actor]);
    }

    /**
     * @param array<mixed> $reaction
     */
    private function assertImage(string $actor, array $reaction, int $status): void
    {
        $response = $this->request('GET', $reaction['imageUrl'], $this->tokens[$actor]);
        self::assertStatus($status, $response, "image of {$reaction['id']}");
        if ($status === 200) {
            self::assertSame(self::PAYLOAD, (string) $response->getBody(), 'image bytes changed');
        }
    }

    /**
     * 実際の HTTP サーバーに multipart/form-data のリアクションを同時に送り、ステータスを返す
     *
     * @return list<int>
     */
    private function createConcurrently(string $actor, int $count): array
    {
        $url = self::serverUrl() . self::PATH;
        $multi = curl_multi_init();
        $handles = [];
        for ($i = 0; $i < $count; $i++) {
            $handle = curl_init($url) ?: throw new \RuntimeException('curl_init failed');
            curl_setopt_array($handle, [
                CURLOPT_POST => true,
                CURLOPT_HTTPHEADER => ["Authorization: Bearer {$this->tokens[$actor]}"],
                CURLOPT_POSTFIELDS => ['reactionType' => 'heart', 'image' => new \CURLStringFile(self::PAYLOAD, 'image.jpg', 'image/jpeg')],
                CURLOPT_RETURNTRANSFER => true,
                CURLOPT_TIMEOUT => 30,
            ]);
            curl_multi_add_handle($multi, $handle);
            $handles[] = $handle;
        }
        do {
            curl_multi_exec($multi, $running);
            curl_multi_select($multi);
        } while ($running > 0);
        $statuses = [];
        foreach ($handles as $handle) {
            $statuses[] = (int) curl_getinfo($handle, CURLINFO_RESPONSE_CODE);
            curl_multi_remove_handle($multi, $handle);
        }
        curl_multi_close($multi);
        return $statuses;
    }
}
