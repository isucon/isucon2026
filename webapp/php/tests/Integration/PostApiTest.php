<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Bytea;
use Isureal\Ids;
use Psr\Http\Message\ResponseInterface;
use Slim\Psr7\Factory\ServerRequestFactory;
use Slim\Psr7\Factory\StreamFactory;
use Slim\Psr7\UploadedFile;

final class PostApiTest extends IntegrationTestCase
{
    public function testCreatePost(): void
    {
        $userId = $this->insertUser('alice');
        $token = $this->login($userId);
        $image = self::jpeg();
        $fields = ['caption' => 'こんにちは', 'visibility' => 'public', 'location_kind' => 'exact', 'latitude' => '35', 'longitude' => '139'];

        self::assertStatus(401, $this->post(null, $fields, $image));
        // 投稿期間がない
        self::assertStatus(409, $this->post($token, $fields, $image));
        $windowId = $this->openWindow();
        self::assertStatus(400, $this->post($token, ['latitude' => '91'] + $fields, $image));

        // 同時に投稿しても 1 件だけ作られる
        $results = $this->concurrentPosts(array_fill(0, 8, [$token, $fields, $image]));
        $created = null;
        foreach ($results as [$status, $body]) {
            if ($status === 201) {
                self::assertNull($created, 'created twice');
                $created = json_decode($body, true, flags: JSON_THROW_ON_ERROR);
            } else {
                self::assertSame(409, $status, $body);
            }
        }
        self::assertIsArray($created);
        self::assertSame($userId, $created['author']['id']);
        self::assertSame($windowId, $created['postingWindowId']);
        self::assertTrue($created['onTime']);
        self::assertSame(0, $created['lateSeconds']);
        self::assertEquals(35, $created['location']['latitude']);

        $stored = Bytea::read($this->db->fetchValue('SELECT data FROM image_objects WHERE owner_user_id = :id', ['id' => $userId]));
        self::assertSame($image, $stored, 'stored image differs from uploaded JPEG');
        $downloaded = $this->request('GET', $created['imageUrl'], $token);
        self::assertStatus(200, $downloaded);
        self::assertSame($image, (string) $downloaded->getBody(), 'downloaded image differs from uploaded JPEG');

        $path = "/api/posts/{$created['id']}";
        self::assertStatus(401, $this->action('PATCH', $path, null, '{"caption":"edited"}'));
        self::assertStatus(401, $this->action('DELETE', $path, null));
        self::assertStatus(404, $this->action('PATCH', '/api/posts/missing', $token, '{"caption":"edited"}'));
        self::assertStatus(404, $this->action('DELETE', '/api/posts/missing', $token));
        foreach (['{', '{}', '{"visibility":"invalid"}'] as $invalid) {
            self::assertStatus(400, $this->action('PATCH', $path, $token, $invalid), $invalid);
        }
        $other = $this->login($this->insertUser('other', id: 'other'));
        self::assertStatus(403, $this->action('PATCH', $path, $other, '{"caption":"edited"}'));
        self::assertStatus(403, $this->action('DELETE', $path, $other));

        $updated = $this->action('PATCH', $path, $token, '{"caption":"edited"}');
        self::assertStatus(200, $updated);
        $edited = self::json($updated);
        self::assertSame('edited', $edited['caption']);
        self::assertSame($created['visibility'], $edited['visibility']);
        self::assertSame(explode('?', $created['imageUrl'])[0], explode('?', $edited['imageUrl'])[0]);
        self::assertSame(
            (new \DateTimeImmutable($created['postedAt']))->format('U.u'),
            (new \DateTimeImmutable($edited['postedAt']))->format('U.u'),
        );
        self::assertStatus(200, $this->action('PATCH', $path, $token, '{"visibility":"friends"}'));

        // 途中で失敗した更新はロールバックされる
        $this->db->executeScript(<<<'SQL'
            CREATE OR REPLACE FUNCTION reject_visibility() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject visibility'; END $$;
            CREATE TRIGGER reject_visibility BEFORE UPDATE OF visibility ON posts FOR EACH ROW EXECUTE FUNCTION reject_visibility();
            SQL);
        try {
            self::assertStatus(500, $this->action('PATCH', $path, $token, '{"caption":"rolled back","visibility":"public"}'));
        } finally {
            $this->db->executeScript('DROP FUNCTION reject_visibility() CASCADE');
        }
        self::assertSame('edited', $this->db->fetchValue('SELECT caption FROM posts WHERE id = :id', ['id' => $created['id']]), 'partial update was not rolled back');
        self::assertStatus(200, $this->action('PATCH', $path, $token, '{"caption":""}'));

        // europe に移ると、同じ日付の europe の投稿期間には投稿済み
        $europeWindowId = Ids::generate();
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline)
             VALUES (:id, 'europe', '2026-09-23', clock_timestamp() - interval '1 minute', clock_timestamp() - interval '10 seconds')",
            ['id' => $europeWindowId],
        );
        $this->db->execute("UPDATE users SET region = 'europe' WHERE id = :id", ['id' => $userId]);
        self::assertStatus(409, $this->post($token, $fields, $image, 'europe'));

        // 同時に削除しても 1 件だけ成功する
        $statuses = array_column(self::concurrently(array_fill(0, 8, ['DELETE', $path, $token, null]), 'europe'), 0);
        self::assertSame(1, count(array_keys($statuses, 204, true)), implode(',', $statuses));
        self::assertSame(7, count(array_keys($statuses, 404, true)), implode(',', $statuses));
        self::assertStatus(404, $this->action('GET', $path, $token, region: 'europe'));
        self::assertStatus(403, $this->request('GET', $created['imageUrl']));
        self::assertStatus(404, $this->action('PATCH', $path, $token, '{"caption":"edited"}', 'europe'));

        // 締め切りを過ぎた投稿
        $this->db->execute("UPDATE posting_windows SET on_time_deadline = clock_timestamp() - interval '10 seconds' WHERE id = :id", ['id' => $windowId]);
        $late = $this->post($token, $fields, $image, 'europe');
        self::assertStatus(201, $late);
        $created = self::json($late);
        self::assertFalse($created['onTime']);
        self::assertGreaterThanOrEqual(10, $created['lateSeconds']);

        // 終わった投稿期間の投稿は編集できないが削除できる
        $this->db->execute('UPDATE posting_windows SET ended_at = clock_timestamp() WHERE id = :id', ['id' => $europeWindowId]);
        self::assertStatus(403, $this->action('PATCH', "/api/posts/{$created['id']}", $token, '{"caption":"edited"}', 'europe'));
        self::assertStatus(204, $this->action('DELETE', "/api/posts/{$created['id']}", $token, region: 'europe'));

        // 同じ日付で 2 回削除したので投稿できない
        $this->db->execute("UPDATE users SET region = 'asia' WHERE id = :id", ['id' => $userId]);
        self::assertStatus(409, $this->post($token, $fields, $image));
        $this->db->execute('UPDATE posting_windows SET ended_at = clock_timestamp() WHERE id = :id', ['id' => $windowId]);
        self::assertStatus(409, $this->post($token, $fields, $image));

        // 投稿の INSERT に失敗したら画像の INSERT もロールバックされる
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline)
             VALUES (:id, 'asia', '2026-09-24', clock_timestamp(), clock_timestamp() + interval '1 minute')",
            ['id' => Ids::generate()],
        );
        $this->db->executeScript(<<<'SQL'
            CREATE OR REPLACE FUNCTION reject_post() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject post'; END $$;
            CREATE TRIGGER reject_post BEFORE INSERT ON posts FOR EACH ROW EXECUTE FUNCTION reject_post();
            SQL);
        try {
            self::assertStatus(500, $this->post($token, $fields, $image));
        } finally {
            $this->db->executeScript('DROP FUNCTION reject_post() CASCADE');
        }
        self::assertSame(2, $this->db->fetchValue('SELECT count(*) FROM image_objects'), 'image insert was not rolled back');
    }

    /**
     * Go のテストと同じく、常に Content-Type: application/json を付けて送る
     */
    private function action(string $method, string $path, ?string $token, string $body = '', string $region = 'asia'): ResponseInterface
    {
        $request = new ServerRequestFactory()->createServerRequest($method, $path)
            ->withHeader('Content-Type', 'application/json')
            ->withHeader('Content-Length', (string) strlen($body))
            ->withBody(new StreamFactory()->createStream($body));
        if ($token !== null) {
            $request = $request->withHeader('Authorization', "Bearer {$token}");
        }
        return $this->app($region)->handle($request);
    }

    /**
     * POST /api/posts を multipart/form-data で送る
     *
     * @param array<string, string> $fields
     */
    private function post(?string $token, array $fields, string $image, string $region = 'asia'): ResponseInterface
    {
        $file = new UploadedFile(new StreamFactory()->createStream($image), 'image.jpg', 'image/jpeg', strlen($image));
        $request = new ServerRequestFactory()->createServerRequest('POST', '/api/posts')
            ->withHeader('Content-Type', 'multipart/form-data; boundary=test')
            ->withHeader('Content-Length', '1')
            ->withParsedBody($fields)
            ->withUploadedFiles(['image' => $file]);
        if ($token !== null) {
            $request = $request->withHeader('Authorization', "Bearer {$token}");
        }
        return $this->app($region)->handle($request);
    }

    /**
     * 実際の HTTP サーバーに POST /api/posts を同時に送る
     *
     * @param list<array{string, array<string, string>, string}> $requests トークン、フィールド、画像
     * @return list<array{int, string}>
     */
    private function concurrentPosts(array $requests): array
    {
        $url = self::serverUrl() . '/api/posts';
        $multi = curl_multi_init();
        $handles = [];
        foreach ($requests as [$token, $fields, $image]) {
            $handle = curl_init($url) ?: throw new \RuntimeException('curl_init failed');
            curl_setopt_array($handle, [
                CURLOPT_POST => true,
                CURLOPT_POSTFIELDS => [...$fields, 'image' => new \CURLStringFile($image, 'image.jpg', 'image/jpeg')],
                CURLOPT_HTTPHEADER => ["Authorization: Bearer {$token}"],
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
        $results = [];
        foreach ($handles as $handle) {
            $results[] = [(int) curl_getinfo($handle, CURLINFO_RESPONSE_CODE), (string) curl_multi_getcontent($handle)];
            curl_multi_remove_handle($multi, $handle);
        }
        curl_multi_close($multi);
        return $results;
    }
}
