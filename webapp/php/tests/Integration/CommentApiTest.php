<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Psr\Http\Message\ResponseInterface;

final class CommentApiTest extends IntegrationTestCase
{
    private const string PATH = '/api/posts/author-post/comments';

    /**
     * @var array<string, string>
     */
    private array $tokens = [];

    public function testComments(): void
    {
        foreach (['author', 'reactor', 'stranger', 'mutual'] as $id) {
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

        self::assertStatus(401, $this->create(null, 'body'));
        self::assertStatus(401, $this->list(null));
        $this->assertDenied('stranger');
        self::assertStatus(400, $this->create('reactor', ''));
        self::assertStatus(400, $this->create('reactor', str_repeat('あ', 1001)));
        $empty = $this->list('author');
        self::assertStatus(200, $empty);
        self::assertSame([], self::json($empty)['items']);

        $created = $this->create('reactor', str_repeat('あ', 1000));
        self::assertStatus(201, $created);
        $first = self::json($created);
        self::assertSame('reactor', $first['author']['id']);
        self::assertSame(str_repeat('あ', 1000), $first['body']);
        $created = $this->create('author', 'self');
        self::assertStatus(201, $created);
        $second = self::json($created);
        $listed = $this->list('reactor');
        self::assertStatus(200, $listed);
        $items = self::json($listed)['items'];
        self::assertCount(2, $items);
        self::assertSame($first['id'], $items[0]['id']);

        self::assertStatus(403, $this->delete('stranger', $first['id']));
        self::assertStatus(401, $this->delete(null, $first['id']));
        // 投稿者は他人のコメントを消せる
        self::assertStatus(204, $this->delete('author', $first['id']));
        self::assertStatus(404, $this->delete('author', $first['id']));
        $created = $this->create('reactor', 'own');
        self::assertStatus(201, $created);
        self::assertStatus(204, $this->delete('reactor', self::json($created)['id']));
        $items = self::json($this->list('author'))['items'];
        self::assertCount(1, $items, 'deleted comments listed');
        self::assertSame($second['id'], $items[0]['id']);

        // 友達の友達には見えるが、コメントできるのは友達だけ
        $this->db->execute("UPDATE posts SET visibility = 'friends_of_friends' WHERE id = 'author-post'");
        $this->db->execute(
            "INSERT INTO friendships (id, user_low_id, user_high_id)
             VALUES ('00000000-0000-0000-0000-000000000001', 'author', 'mutual'), ('00000000-0000-0000-0000-000000000002', 'mutual', 'reactor')",
        );
        $this->assertDenied('reactor');
        $this->db->execute("INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ('00000000-0000-0000-0000-000000000003', 'author', 'reactor')");
        self::assertStatus(201, $this->create('reactor', 'friend'));
        self::assertStatus(200, $this->list('reactor'));
        $this->db->execute("UPDATE posts SET visibility = 'friends' WHERE id = 'author-post'");
        self::assertStatus(201, $this->create('reactor', 'friend'));
        self::assertStatus(200, $this->list('reactor'));

        foreach ([['author', 'reactor'], ['reactor', 'author']] as [$blocker, $blocked]) {
            $this->db->execute(
                "INSERT INTO block_relations (id, blocker_id, blocked_id) VALUES ('00000000-0000-0000-0000-000000000004', :blocker, :blocked)",
                ['blocker' => $blocker, 'blocked' => $blocked],
            );
            $this->assertDenied('reactor');
            $this->db->execute('DELETE FROM block_relations');
        }

        // 自分の投稿を消すと、他人の投稿にもコメントできない
        $this->db->execute("UPDATE posts SET deleted_at = clock_timestamp() WHERE id = 'reactor-post'");
        $this->assertDenied('reactor');
        $this->db->execute("UPDATE posts SET deleted_at = NULL WHERE id = 'reactor-post'");

        // 投稿期間が終わるとコメントできないが、消すことはできる
        $this->db->execute('UPDATE posting_windows SET ended_at = clock_timestamp()');
        $this->assertDenied('reactor');
        $this->assertDenied('author');
        self::assertStatus(204, $this->delete('author', $second['id']));
        $this->db->execute('UPDATE posting_windows SET ended_at = NULL');

        // 投稿者が別のリージョンに移ったら、そのリージョンの投稿期間（の日付）で判断する
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline)
             VALUES ('europe-window', 'europe', '2026-09-25', clock_timestamp(), clock_timestamp() + interval '1 day')",
        );
        $this->db->execute("UPDATE users SET region = 'europe' WHERE id = 'author'");
        $this->assertDenied('author', 'europe');
        $this->db->execute("UPDATE posting_windows SET local_date = '2026-09-24' WHERE id = 'europe-window'");
        self::assertStatus(201, $this->create('author', 'same date across regions', 'europe'));
        $this->db->execute("UPDATE users SET region = 'asia' WHERE id = 'author'");

        $this->db->execute("UPDATE posts SET deleted_at = clock_timestamp() WHERE id = 'author-post'");
        $this->assertDenied('author');
        $this->assertDenied('reactor');
        $this->db->execute("UPDATE posts SET deleted_at = NULL WHERE id = 'author-post'");

        $this->db->executeScript(<<<'SQL'
            CREATE OR REPLACE FUNCTION reject_comment() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject comment'; END $$;
            CREATE TRIGGER reject_comment BEFORE INSERT ON comments FOR EACH ROW EXECUTE FUNCTION reject_comment();
            SQL);
        try {
            self::assertStatus(500, $this->create('reactor', 'rollback'));
        } finally {
            // スキーマを作り直しても関数は残るので消す
            $this->db->executeScript('DROP FUNCTION reject_comment() CASCADE');
        }

        $this->db->execute('UPDATE comments SET deleted_at = clock_timestamp()');
        for ($i = 0; $i < 52; $i++) {
            $this->db->execute(
                "INSERT INTO comments (id, post_id, author_id, body, created_at) VALUES (:id, 'author-post', 'reactor', 'page', '2026-09-24T00:00:00Z')",
                ['id' => sprintf('page-%02d', $i)],
            );
        }
        $page = self::json($this->list('author'));
        self::assertCount(50, $page['items']);
        self::assertTrue($page['hasMore']);
        $last = $page['items'][49];
        $tail = $this->list('author', '?' . http_build_query(['afterCreatedAt' => $last['createdAt'], 'afterId' => $last['id']]));
        self::assertStatus(200, $tail);
        $tail = self::json($tail);
        self::assertCount(2, $tail['items']);
        self::assertFalse($tail['hasMore']);
        self::assertGreaterThan($last['id'], $tail['items'][0]['id']);
        self::assertStatus(400, $this->list('author', '?afterId=x'));
    }

    private function token(?string $actor): ?string
    {
        return $actor === null ? null : $this->tokens[$actor];
    }

    private function create(?string $actor, string $body, string $region = 'asia'): ResponseInterface
    {
        return $this->request('POST', self::PATH, $this->token($actor), ['body' => $body], region: $region);
    }

    private function list(?string $actor, string $suffix = '', string $region = 'asia'): ResponseInterface
    {
        return $this->request('GET', self::PATH . $suffix, $this->token($actor), region: $region);
    }

    private function delete(?string $actor, string $commentId): ResponseInterface
    {
        return $this->request('DELETE', "/api/comments/{$commentId}", $this->token($actor));
    }

    /**
     * コメントも一覧の取得もできない
     */
    private function assertDenied(string $actor, string $region = 'asia'): void
    {
        self::assertError(403, 'interaction_forbidden', $this->create($actor, 'denied', $region));
        self::assertError(403, 'interaction_forbidden', $this->list($actor, region: $region));
    }
}
