<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Ids;
use Psr\Http\Message\ResponseInterface;

final class PostReadApiTest extends IntegrationTestCase
{
    /**
     * @var array<string, string>
     */
    private array $tokens = [];

    /**
     * ユーザーごとの今のリージョン。リクエストはそのリージョンのサーバーに送る
     *
     * @var array<string, string>
     */
    private array $regions = [];

    public function testPostRead(): void
    {
        foreach (['a', 'b', 'c'] as $id) {
            $this->insertUser($id, id: $id);
            $this->tokens[$id] = $this->login($id);
            $this->regions[$id] = 'asia';
        }

        self::assertStatus(401, $this->request('GET', '/api/posting-window/current'));
        self::assertStatus(401, $this->request('GET', '/api/posts/me/current'));
        self::assertStatus(403, $this->request('GET', '/api/images/missing'));
        $this->checkWindow(false, false, false, 0);
        $this->get('a', '/api/posts/me/current', 404);
        $this->openWindow(id: 'w');
        $this->checkWindow(true, false, true, 0);

        $this->addPost('pa', 'a');
        $this->checkWindow(true, true, false, 0);
        $post = self::json($this->get('a', '/api/posts/me/current', 200));
        self::assertSame('pa', $post['id']);
        self::assertSame('a', $post['author']['id']);
        self::assertStringStartsWith('/api/images/pa-image?', $post['imageUrl']);
        self::assertTrue($post['onTime']);
        $image = $this->request('GET', $post['imageUrl']);
        self::assertStatus(200, $image);
        self::assertSame('image data', (string) $image->getBody());
        self::assertSame('image/jpeg', $image->getHeaderLine('Content-Type'));

        // b は今日まだ投稿していない
        $this->get('b', '/api/posts/pa', 404);
        self::assertStatus(403, $this->request('GET', '/api/images/missing', $this->tokens['a']));
        $this->addPost('pb', 'b');
        $this->get('b', '/api/posts/pa', 200);
        $this->exec("UPDATE posts SET visibility = 'friends' WHERE id = 'pa'");
        $this->get('b', '/api/posts/pa', 404);
        $this->exec("INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ('" . Ids::generate() . "', 'a', 'b')");
        $this->get('b', '/api/posts/pa', 200);
        // どちらの向きのブロックでも見えない
        $this->exec("INSERT INTO block_relations (id, blocker_id, blocked_id) VALUES ('" . Ids::generate() . "', 'a', 'b')");
        $this->get('b', '/api/posts/pa', 404);
        $this->exec("UPDATE block_relations SET blocker_id = 'b', blocked_id = 'a'");
        $this->get('b', '/api/posts/pa', 404);
        $this->exec('DELETE FROM block_relations');
        $this->exec('DELETE FROM friendships');
        $this->exec("INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ('" . Ids::generate() . "', 'a', 'c'), ('" . Ids::generate() . "', 'b', 'c')");
        $this->get('b', '/api/posts/pa', 404);
        $this->exec("UPDATE posts SET visibility = 'friends_of_friends' WHERE id = 'pa'");
        $this->get('b', '/api/posts/pa', 200);

        // europe に移った b は europe の投稿期間がなければ今日投稿していないことになる
        $this->moveTo('europe', 'b');
        $this->get('b', '/api/posts/pa', 404);
        $this->exec("INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ('europe', 'europe', '2026-09-23', clock_timestamp() - interval '1 minute', clock_timestamp())");
        $this->get('b', '/api/posts/pa', 200);
        $this->moveTo('europe', 'a');
        $this->checkWindow(true, true, false, 0);
        $this->get('a', '/api/posts/me/current', 200);
        $this->exec("UPDATE posting_windows SET ended_at = clock_timestamp() WHERE id = 'w'");
        $this->checkWindow(true, true, false, 0);
        $this->get('a', '/api/posts/me/current', 200);
        $this->exec("UPDATE posting_windows SET local_date = '2026-09-24' WHERE id = 'europe'");
        $this->get('a', '/api/posts/me/current', 404);
        $this->moveTo('asia', 'a', 'b');
        $this->exec("UPDATE posting_windows SET ended_at = NULL WHERE id = 'w'");

        $this->exec("UPDATE posting_windows SET ended_at = clock_timestamp() WHERE id = 'w'");
        $this->checkWindow(false, false, false, 0);
        $this->get('a', '/api/posts/me/current', 404);
        $this->get('b', '/api/posts/pa', 404);
        self::assertStatus(200, $this->request('GET', $post['imageUrl'], $this->tokens['a']));
        $this->exec("UPDATE posting_windows SET ended_at = NULL WHERE id = 'w'");
        $this->exec("UPDATE posts SET deleted_at = clock_timestamp() WHERE id = 'pa'");
        $this->exec("UPDATE posting_windows SET local_date = '2026-09-23' WHERE id = 'europe'");
        $this->moveTo('europe', 'a');
        $this->checkWindow(true, false, true, 1);
        $this->get('a', '/api/posts/me/current', 404);
        self::assertStatus(403, $this->request('GET', $post['imageUrl']));
        $this->addPost('pa2', 'a');
        $this->exec("UPDATE posts SET deleted_at = clock_timestamp() WHERE id = 'pa2'");
        $this->checkWindow(true, false, false, 2);
    }

    private function exec(string $sql): void
    {
        $this->db->execute($sql);
    }

    private function moveTo(string $region, string ...$ids): void
    {
        foreach ($ids as $id) {
            $this->db->execute('UPDATE users SET region = :region WHERE id = :id', ['region' => $region, 'id' => $id]);
            $this->regions[$id] = $region;
        }
    }

    private function get(string $actor, string $path, int $status): ResponseInterface
    {
        $response = $this->request('GET', $path, $this->tokens[$actor], region: $this->regions[$actor]);
        self::assertStatus($status, $response, $path);
        return $response;
    }

    private function addPost(string $id, string $user): void
    {
        $this->db->execute(
            "INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES (:id, :owner, convert_to('image data', 'UTF8'), 'image/jpeg')",
            ['id' => "{$id}-image", 'owner' => $user],
        );
        $this->db->execute(
            "INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at)
             VALUES (:id, :author, 'w', :image, 'caption', 'public', 'none', clock_timestamp())",
            ['id' => $id, 'author' => $user, 'image' => "{$id}-image"],
        );
    }

    private function checkWindow(bool $hasWindow, bool $hasPosted, bool $canPost, int $deletions): void
    {
        $response = self::json($this->get('a', '/api/posting-window/current', 200));
        $message = json_encode($response, JSON_THROW_ON_ERROR);
        self::assertSame($hasWindow, $response['window'] !== null, $message);
        self::assertSame($hasPosted, $response['hasPosted'], $message);
        self::assertSame($canPost, $response['canPost'], $message);
        self::assertSame($deletions, $response['deletionCount'], $message);
        if ($response['window'] !== null) {
            self::assertSame('2026-09-23', $response['window']['localDate'], $message);
        }
    }
}
