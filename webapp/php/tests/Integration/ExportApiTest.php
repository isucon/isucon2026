<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Psr\Http\Message\ResponseInterface;
use ZipArchive;

final class ExportApiTest extends IntegrationTestCase
{
    /**
     * @var array<string, string>
     */
    private array $tokens = [];

    public function testExportPosts(): void
    {
        foreach (['owner', 'other', 'empty', 'foreign'] as $id) {
            $this->insertUser($id, id: $id);
            $this->tokens[$id] = $this->login($id);
        }
        $this->db->execute("UPDATE users SET region = 'europe' WHERE id = 'foreign'");
        for ($i = 0; $i < 56; $i++) {
            $id = sprintf('post-%02d', $i);
            $this->db->execute(
                "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at)
                 VALUES (:id, 'asia', '2026-01-01'::date + :i::int, '2026-01-01', '2026-01-01', '2026-01-02')",
                ['id' => $id, 'i' => $i],
            );
            $this->db->execute(
                "INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES (:id, 'owner', convert_to(:data, 'UTF8'), 'image/jpeg')",
                ['id' => $id, 'data' => "image-{$id}"],
            );
            $this->db->execute(
                "INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, latitude, longitude, posted_at)
                 VALUES (:id, 'owner', :id, :id, '日本語の投稿', 'friends', 'exact', 35, 139, '2026-01-01'::timestamptz + :i::int * interval '1 day')",
                ['id' => $id, 'i' => $i],
            );
        }
        $this->db->execute("UPDATE posts SET deleted_at = clock_timestamp() WHERE id = 'post-55'");
        $this->db->execute("INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES ('other', 'other', convert_to('secret', 'UTF8'), 'image/jpeg')");
        $this->db->execute(
            "INSERT INTO posts (id, author_id, posting_window_id, image_id, visibility, location_kind, posted_at)
             VALUES ('other', 'other', 'post-00', 'other', 'public', 'none', clock_timestamp())",
        );

        $this->export(null, 401);
        $this->export('foreign', 403);
        foreach (['owner' => 55, 'empty' => 0] as $actor => $count) {
            $response = $this->export($actor, 200);
            self::assertSame('application/zip', $response->getHeaderLine('Content-Type'));
            self::assertSame('no-store', $response->getHeaderLine('Cache-Control'));
            self::assertSame('attachment; filename="isureal-posts.zip"', $response->getHeaderLine('Content-Disposition'));
            $files = self::unzip((string) $response->getBody());
            $posts = json_decode($files['posts.json'] ?? '', true, flags: JSON_THROW_ON_ERROR);
            self::assertIsList($posts, $actor);
            self::assertCount($count, $posts, $actor);
            self::assertCount($count + 1, $files, $actor);
            foreach ($posts as $i => $post) {
                $id = sprintf('post-%02d', $i);
                self::assertSame($id, $post['id']);
                self::assertSame($actor, $post['author']['id']);
                self::assertSame('日本語の投稿', $post['caption']);
                self::assertSame('friends', $post['visibility']);
                self::assertEquals(35, $post['location']['latitude']);
                self::assertEquals(139, $post['location']['longitude']);
                self::assertSame("image-{$id}", $files[$post['imagePath']] ?? null, "wrong image for {$id}");
            }
        }
    }

    private function export(?string $actor, int $status): ResponseInterface
    {
        $response = $this->request('GET', '/api/me/export?userId=other', $actor === null ? null : $this->tokens[$actor]);
        self::assertStatus($status, $response, (string) $actor);
        return $response;
    }

    /**
     * @return array<string, string> ファイル名と中身
     */
    private static function unzip(string $data): array
    {
        $path = (string) tempnam(sys_get_temp_dir(), 'isureal-export-test-');
        try {
            file_put_contents($path, $data);
            $zip = new ZipArchive();
            self::assertTrue($zip->open($path, ZipArchive::RDONLY) === true, 'invalid zip');
            $files = [];
            for ($i = 0; $i < $zip->numFiles; $i++) {
                $name = (string) $zip->getNameIndex($i);
                $files[$name] = (string) $zip->getFromIndex($i);
            }
            $zip->close();
            return $files;
        } finally {
            @unlink($path);
        }
    }
}
