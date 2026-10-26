<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

final class JpegValidationTest extends IntegrationTestCase
{
    public function testProfileImage(): void
    {
        $token = $this->login($this->insertUser('alice'));
        $put = fn (string $data) => $this->requestRaw('PUT', '/api/me/profile-image', $token, $data, ['Content-Type' => 'image/jpeg']);
        self::assertError(400, 'invalid_profile_image', $put('image bytes'));
        self::assertError(400, 'invalid_profile_image', $put(substr(self::jpeg(64, 64), 0, -2)));
        self::assertError(400, 'invalid_profile_image', $put(self::jpeg(513, 1)));
        self::assertError(400, 'invalid_profile_image', $put(self::jpeg(1, 513)));
        self::assertSame(0, $this->db->fetchValue('SELECT count(*) FROM image_objects'));
        self::assertStatus(204, $put(self::jpeg(512, 512)));
    }

    public function testPost(): void
    {
        $token = $this->login($this->insertUser('alice'));
        $fields = ['caption' => '', 'visibility' => 'public', 'location_kind' => 'none'];
        $post = fn (string $image) => $this->requestMultipart('POST', '/api/posts', $token, $fields, ['image' => $image]);
        // 投稿期間より先に画像を確かめる
        self::assertError(400, 'invalid_post', $post('image bytes'));
        $this->openWindow();
        self::assertError(400, 'invalid_post', $post(self::jpeg(4097, 1)));
        self::assertError(400, 'invalid_post', $post(self::jpeg(1, 4097)));
        self::assertSame(0, $this->db->fetchValue('SELECT count(*) FROM posts'));
        self::assertStatus(201, $post(self::jpeg(4096, 1)));
    }

    public function testRealMoji(): void
    {
        $this->insertUser('author', id: 'author');
        $reactor = $this->insertUser('reactor');
        $token = $this->login($reactor);
        $this->openWindow(id: 'window');
        // 自分も投稿していないと他人の投稿は見えない
        foreach (['author' => 'post', $reactor => 'own-post'] as $owner => $postId) {
            $this->db->execute("INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES (:id, :owner, 'photo', 'image/jpeg')", ['id' => "{$postId}-image", 'owner' => $owner]);
            $this->db->execute(
                "INSERT INTO posts (id, author_id, posting_window_id, image_id, visibility, location_kind, posted_at)
                 VALUES (:id, :author, 'window', :image, 'public', 'none', clock_timestamp())",
                ['id' => $postId, 'author' => $owner, 'image' => "{$postId}-image"],
            );
        }
        $react = fn (string $image, string $postId = 'post') => $this->requestMultipart('POST', "/api/posts/{$postId}/realmojis", $token, ['reactionType' => 'heart'], ['image' => $image]);
        // 投稿を探すより先に画像を確かめる
        self::assertError(400, 'invalid_realmoji', $react('image bytes', 'missing'));
        self::assertError(400, 'invalid_realmoji', $react(self::jpeg(513, 1)));
        self::assertSame(0, $this->db->fetchValue('SELECT count(*) FROM realmojis'));
        self::assertStatus(201, $react(self::jpeg(512, 512)));
    }
}
