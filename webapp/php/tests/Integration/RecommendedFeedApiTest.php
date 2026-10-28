<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Ids;
use Psr\Http\Message\ResponseInterface;

final class RecommendedFeedApiTest extends IntegrationTestCase
{
    private const string FEED = '/api/feeds/friends-of-friends';

    private string $token = '';

    public function testRecommendedFeed(): void
    {
        foreach (['viewer', 'f1', 'f2', 'a1', 'a2', 'x'] as $id) {
            $this->insertUser($id, id: $id);
        }
        $this->token = $this->login('viewer');
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at)
             VALUES ('w', 'asia', '2026-09-23', '2026-09-23T00:00:00Z', '2026-09-23T00:02:00Z', NULL)",
        );
        foreach (['viewer', 'a1', 'a2', 'x'] as $i => $author) {
            $this->db->execute(
                "INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES (:id, :owner, convert_to('image', 'UTF8'), 'image/jpeg')",
                ['id' => "{$author}-image", 'owner' => $author],
            );
            $this->db->execute(
                "INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at)
                 VALUES (:id, :author, 'w', :image, 'caption', 'friends_of_friends', 'none', :posted_at)",
                ['id' => $author, 'author' => $author, 'image' => "{$author}-image", 'posted_at' => sprintf('2026-09-23T00:01:%02dZ', $i)],
            );
        }
        foreach ([['f1', 'viewer'], ['f2', 'viewer'], ['a1', 'f1'], ['a1', 'f2'], ['a2', 'f1'], ['a1', 'x'], ['a2', 'x'], ['f2', 'x']] as $pair) {
            $this->db->execute(
                'INSERT INTO friendships (id, user_low_id, user_high_id) VALUES (:id, :low, :high)',
                ['id' => Ids::generate(), 'low' => min($pair), 'high' => max($pair)],
            );
        }

        $this->assertRanking(self::FEED, 'a1:7', 'x:6', 'a2:4');
        $this->db->execute(
            "INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES (:id, 'a2', 'f1', 'rm', 'smile', clock_timestamp())",
            ['id' => Ids::generate()],
        );
        $this->db->execute(
            "INSERT INTO comments (id, post_id, author_id, body, created_at) VALUES (:id1, 'a2', 'f1', 'nice', clock_timestamp()), (:id2, 'a2', 'x', 'nice', clock_timestamp())",
            ['id1' => Ids::generate(), 'id2' => Ids::generate()],
        );
        $page = $this->assertRanking(self::FEED, 'a2:14', 'a1:7', 'x:6');
        $this->db->execute(
            "INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES (:id, 'a1', 'f2', 'rm', 'smile', clock_timestamp())",
            ['id' => Ids::generate()],
        );
        $this->db->execute("UPDATE users SET status = 'deleted' WHERE id = 'f2'");
        $this->assertRanking(self::FEED, 'a2:14', 'a1:7', 'x:6');

        $cursor = $page[1];
        $this->assertRanking(self::FEED . '?' . http_build_query(['beforeScore' => $cursor['score'], 'beforePostedAt' => $cursor['postedAt'], 'beforeId' => $cursor['id']]), 'x:6');
        $this->get(self::FEED . '?beforeScore=7', 400);
        $this->get(self::FEED . '?beforePostedAt=2026-09-23T00:01:01Z&beforeId=a1', 400);
        $this->get(self::FEED . '?beforeScore=x&beforePostedAt=2026-09-23T00:01:01Z&beforeId=a1', 400);

        foreach (self::json($this->get('/api/feeds/friends', 200))['items'] as $item) {
            self::assertIsArray($item);
            self::assertArrayNotHasKey('score', $item);
        }
    }

    private function get(string $path, int $status): ResponseInterface
    {
        $response = $this->request('GET', $path, $this->token);
        self::assertStatus($status, $response, $path);
        return $response;
    }

    /**
     * @return list<array<string, mixed>>
     */
    private function assertRanking(string $path, string ...$expected): array
    {
        $items = self::json($this->get($path, 200))['items'];
        self::assertIsList($items);
        $ranking = [];
        foreach ($items as $item) {
            self::assertIsArray($item);
            self::assertIsInt($item['score'] ?? null, "{$path}: post has no score");
            $ranking[] = "{$item['id']}:{$item['score']}";
        }
        self::assertSame($expected, $ranking, $path);
        /** @var list<array<string, mixed>> $items */
        return $items;
    }
}
