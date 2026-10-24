<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Ids;
use Psr\Http\Message\ResponseInterface;

final class PostListsApiTest extends IntegrationTestCase
{
    private string $token = '';

    public function testPostLists(): void
    {
        foreach (['viewer', 'a', 'b', 'c', 'd'] as $id) {
            $this->insertUser($id, id: $id);
        }
        $this->token = $this->login('viewer');
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at) VALUES
             ('w', 'asia', '2026-09-23', '2026-09-23T00:00:00Z', '2026-09-23T00:02:00Z', NULL),
             ('old', 'europe', '2026-09-22', '2026-09-22T00:00:00Z', '2026-09-22T00:02:00Z', clock_timestamp()),
             ('august', 'asia', '2026-08-31', '2026-08-31T00:00:00Z', '2026-08-31T00:02:00Z', clock_timestamp())",
        );
        $this->addPost('a', 'a', 'w', 'friends');
        $this->addPost('b', 'b', 'w', 'friends_of_friends');
        $this->addPost('c', 'c', 'w', 'public');
        $this->addPost('d', 'd', 'w', 'public');
        $this->db->execute(
            "INSERT INTO friendships (id, user_low_id, user_high_id) VALUES (:id1, 'a', 'viewer'), (:id2, 'a', 'b')",
            ['id1' => Ids::generate(), 'id2' => Ids::generate()],
        );
        $this->db->execute(
            "INSERT INTO follows (id, follower_id, followee_id) VALUES (:id1, 'viewer', 'c'), (:id2, 'viewer', 'b')",
            ['id1' => Ids::generate(), 'id2' => Ids::generate()],
        );
        $this->db->execute("INSERT INTO block_relations (id, blocker_id, blocked_id) VALUES (:id, 'd', 'viewer')", ['id' => Ids::generate()]);

        // 今日まだ投稿していないので何も見えない
        $this->assertIds('/api/feeds/public');
        $this->get('/api/posts/c', 404);
        $this->addPost('own', 'viewer', 'w', 'public');
        $this->assertIds('/api/feeds/friends', 'a');
        $this->assertIds('/api/feeds/friends-of-friends', 'b');
        $this->assertIds('/api/feeds/following', 'c', 'b');
        $this->assertIds('/api/feeds/public', 'c');
        foreach (['own', 'a', 'b', 'c'] as $id) {
            $this->get("/api/posts/{$id}", 200);
        }
        $this->get('/api/posts/d', 404);
        $this->get('/api/posts/missing', 404);
        $this->get('/api/feeds/unknown', 400);
        $this->get('/api/feeds/public?beforePostedAt=invalid&beforeId=x', 400);

        $this->addPost('old-own', 'viewer', 'old', 'friends');
        $this->addPost('august-own', 'viewer', 'august', 'public');
        $this->addPost('old-other', 'a', 'old', 'public');
        $this->get('/api/posts/old-own', 200);
        $this->get('/api/posts/old-other', 404);
        $this->assertIds('/api/memories?month=2026-09', 'own', 'old-own');
        $this->assertIds('/api/memories/2026-09-22', 'old-own');
        $this->assertIds('/api/memories?month=2026-08', 'august-own');
        $this->assertIds('/api/memories/2026-09-21');
        $this->get('/api/memories?month=invalid', 400);
        $this->get('/api/memories/2026-02-30', 400);
        $this->db->execute("UPDATE posts SET deleted_at = clock_timestamp() WHERE id = 'old-own'");
        $this->assertIds('/api/memories/2026-09-22');
        $this->get('/api/posts/old-own', 404);

        for ($i = 0; $i < 55; $i++) {
            $id = sprintf('public-%02d', $i);
            $this->insertUser($id, id: $id);
            $this->addPost($id, $id, 'w', 'public');
        }
        // 新しくて見えない投稿がページの枠を使ってはいけない
        for ($i = 0; $i < 55; $i++) {
            $id = sprintf('private-%02d', $i);
            $this->insertUser($id, id: $id);
            $this->addPost($id, $id, 'w', 'friends');
            $this->db->execute("UPDATE posts SET posted_at = '2026-09-23T00:01:30Z' WHERE id = :id", ['id' => $id]);
        }
        $first = $this->page('/api/feeds/public');
        self::assertCount(50, $first['items']);
        self::assertTrue($first['hasMore']);
        $last = $first['items'][49];
        $second = $this->page('/api/feeds/public?' . http_build_query(['beforePostedAt' => $last['postedAt'], 'beforeId' => $last['id']]));
        self::assertCount(6, $second['items']);
        self::assertFalse($second['hasMore']);
        $ids = array_column([...$first['items'], ...$second['items']], 'id');
        self::assertSame(array_unique($ids), $ids, 'duplicate post');
        foreach (['c', 'public-00', 'public-54'] as $id) {
            self::assertContains($id, $ids);
        }

        $this->get('/api/feeds/public?beforeId=x', 400);
        $this->get('/api/feeds/public?beforePostedAt=2026-09-23T00:00:00Z', 400);
        $this->get('/api/memories?month=2026-09&beforeId=x', 400);
        $this->assertIds('/api/memories?month=2026-09&beforePostedAt=2026-09-23T00:01:00Z&beforeId=own');
        $this->assertIds('/api/memories/2026-09-23?beforePostedAt=2026-09-23T00:01:00Z&beforeId=own');
    }

    private function addPost(string $id, string $author, string $window, string $visibility): void
    {
        $this->db->execute(
            "INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES (:id, :owner, convert_to('image', 'UTF8'), 'image/jpeg')",
            ['id' => "{$id}-image", 'owner' => $author],
        );
        $this->db->execute(
            "INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at)
             VALUES (:id, :author, :window, :image, 'caption', :visibility, 'none', '2026-09-23T00:01:00Z')",
            ['id' => $id, 'author' => $author, 'window' => $window, 'image' => "{$id}-image", 'visibility' => $visibility],
        );
    }

    private function get(string $path, int $status): ResponseInterface
    {
        $response = $this->request('GET', $path, $this->token);
        self::assertStatus($status, $response, $path);
        return $response;
    }

    /**
     * @return array{items: list<array<string, mixed>>, hasMore: bool}
     */
    private function page(string $path): array
    {
        $response = self::json($this->get($path, 200));
        self::assertIsList($response['items'] ?? null, 'items must be an array');
        self::assertIsBool($response['hasMore']);
        /** @var array{items: list<array<string, mixed>>, hasMore: bool} $response */
        return $response;
    }

    private function assertIds(string $path, string ...$expected): void
    {
        self::assertSame($expected, array_column($this->page($path)['items'], 'id'), $path);
    }
}
