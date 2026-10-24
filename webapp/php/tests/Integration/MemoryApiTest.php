<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Psr\Http\Message\ResponseInterface;

final class MemoryApiTest extends IntegrationTestCase
{
    /**
     * @var array<string, string>
     */
    private array $tokens = [];

    public function testMemoryInteractions(): void
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
            $this->db->execute(
                "INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES (:id, :owner, convert_to('photo', 'UTF8'), 'image/jpeg')",
                ['id' => "{$id}-image", 'owner' => $id],
            );
            $this->db->execute(
                "INSERT INTO posts (id, author_id, posting_window_id, image_id, visibility, location_kind, posted_at)
                 VALUES (:id, :author, 'window', :image, 'public', 'none', clock_timestamp())",
                ['id' => "{$id}-post", 'author' => $id, 'image' => "{$id}-image"],
            );
        }
        $this->db->execute('UPDATE posting_windows SET ended_at = clock_timestamp()');

        // RealMoji とコメントがなくても空の配列を返す
        foreach (['/api/memories?month=2026-09', '/api/memories/2026-09-24'] as $path) {
            $out = $this->page('author', $path);
            self::assertCount(1, $out['items'], $path);
            self::assertSame([], $out['items'][0]['realmojis'] ?? null, $path);
            self::assertSame([], $out['items'][0]['comments'] ?? null, $path);
        }

        for ($i = 0; $i < 55; $i++) {
            $id = sprintf('r-%02d', $i);
            $this->insertUser($id, id: $id);
            $this->db->execute(
                "INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES (:id, :owner, convert_to('original bytes', 'UTF8'), 'image/jpeg')",
                ['id' => "{$id}-image", 'owner' => $id],
            );
            $this->db->execute(
                "INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at)
                 VALUES (:id, 'author-post', :reactor, :image, 'like', '2026-09-24T01:00:00Z')",
                ['id' => $id, 'reactor' => $id, 'image' => "{$id}-image"],
            );
            $this->db->execute(
                "INSERT INTO comments (id, post_id, author_id, body, created_at) VALUES (:id, 'author-post', :author, 'comment', '2026-09-24T01:00:00Z')",
                ['id' => $id, 'author' => $id],
            );
        }
        $this->db->execute("UPDATE realmojis SET replaced_at = clock_timestamp() WHERE id = 'r-54'");
        $this->db->execute("UPDATE realmojis SET deleted_at = clock_timestamp() WHERE id = 'r-53'");
        $this->db->execute("UPDATE comments SET deleted_at = clock_timestamp() WHERE id = 'r-00'");
        $this->db->execute("UPDATE users SET status = 'deleted', deleted_at = clock_timestamp() WHERE id IN ('r-01', 'r-52')");

        foreach (['/api/memories?month=2026-09', '/api/memories/2026-09-24'] as $path) {
            $out = $this->page('author', $path);
            self::assertCount(1, $out['items'], $path);
            self::assertFalse($out['hasMore'], $path);
            $item = $out['items'][0];
            self::assertCount(50, $item['realmojis'], $path);
            self::assertCount(50, $item['comments'], $path);
            self::assertSame('r-51', $item['realmojis'][0]['id'], 'reaction ordering or filtering');
            self::assertSame('r-02', $item['realmojis'][49]['id'], 'reaction ordering or filtering');
            self::assertSame('r-02', $item['comments'][0]['id'], 'comment ordering or filtering');
            self::assertSame('r-51', $item['comments'][49]['id'], 'comment ordering or filtering');
            $image = $this->get('author', $item['realmojis'][0]['imageUrl'], 200);
            self::assertSame('original bytes', (string) $image->getBody(), 'image bytes changed');
            self::assertStatus(200, $this->request('GET', $item['realmojis'][0]['imageUrl']));
        }
        // 終わった投稿期間の投稿のコメントは見られない
        $this->get('author', '/api/posts/author-post/comments', 403);
        $other = $this->page('reactor', '/api/memories?month=2026-09');
        self::assertCount(1, $other['items']);
        self::assertSame([], $other['items'][0]['comments'], "another user's interactions leaked");
        self::assertSame([], $other['items'][0]['realmojis'], "another user's interactions leaked");
        $this->db->execute("UPDATE posts SET deleted_at = clock_timestamp() WHERE id = 'author-post'");
        self::assertSame([], $this->page('author', '/api/memories?month=2026-09')['items'], 'deleted post listed');
    }

    private function get(string $actor, string $path, int $status): ResponseInterface
    {
        $response = $this->request('GET', $path, $this->tokens[$actor]);
        self::assertStatus($status, $response, $path);
        return $response;
    }

    /**
     * @return array{items: list<array<string, mixed>>, hasMore: bool}
     */
    private function page(string $actor, string $path): array
    {
        /** @var array{items: list<array<string, mixed>>, hasMore: bool} */
        return self::json($this->get($actor, $path, 200));
    }
}
