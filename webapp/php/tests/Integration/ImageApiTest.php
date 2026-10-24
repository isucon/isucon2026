<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\ImageSignature;
use PHPUnit\Framework\Attributes\DataProvider;
use Psr\Http\Message\ResponseInterface;
use Slim\Psr7\Factory\ServerRequestFactory;

final class ImageApiTest extends IntegrationTestCase
{
    private const array REGIONS = ['asia', 'europe', 'america'];

    public function testSignedImages(): void
    {
        $registered = $this->request('POST', '/api/auth/register', json: ['username' => 'owner', 'displayName' => 'Owner', 'region' => 'asia']);
        self::assertStatus(201, $registered);
        $registration = self::json($registered);
        $ownerId = $registration['user']['id'];
        $key = hash('sha256', $registration['secretKey'], true);
        $login = $this->request('POST', '/api/auth/login', json: ['username' => 'owner', 'secretKey' => $registration['secretKey']]);
        self::assertStatus(200, $login);
        $token = self::json($login)['token'];

        $reactorKey = hash('sha256', 'reactor-secret', true);
        $this->db->execute(
            "INSERT INTO users (id, username, display_name, secret_key_hash, region) VALUES ('reactor', 'reactor', 'Reactor', :hash, 'europe')",
            ['hash' => bin2hex($reactorKey)],
        );
        $this->db->execute(
            "INSERT INTO image_objects (id, owner_user_id, data, media_type)
             VALUES ('profile', :owner, 'profile', 'image/jpeg'), ('photo', :owner, 'photo', 'image/jpeg'), ('reaction', 'reactor', 'reaction', 'image/jpeg')",
            ['owner' => $ownerId],
        );
        $this->db->execute("UPDATE users SET profile_image_id = 'profile' WHERE id = :id", ['id' => $ownerId]);
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline)
             VALUES ('w', 'asia', CURRENT_DATE, clock_timestamp(), clock_timestamp() + interval '1 day')",
        );
        $this->db->execute(
            "INSERT INTO posts (id, author_id, posting_window_id, image_id, visibility, location_kind, posted_at)
             VALUES ('post', :owner, 'w', 'photo', 'friends', 'none', clock_timestamp())",
            ['owner' => $ownerId],
        );
        $this->db->execute(
            "INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at)
             VALUES ('r', 'post', 'reactor', 'reaction', 'like', clock_timestamp())",
        );

        $before = time();
        $me = $this->request('GET', '/api/me', $token);
        self::assertStatus(200, $me);
        $profileUrl = self::json($me)['profileImageUrl'];
        self::assertIsString($profileUrl);
        $post = $this->request('GET', '/api/posts/post', $token);
        self::assertStatus(200, $post);
        $reactions = $this->request('GET', '/api/posts/post/realmojis', $token);
        self::assertStatus(200, $reactions);
        $items = self::json($reactions)['items'];
        self::assertCount(1, $items);

        // URL、画像 ID、署名の鍵（画像の持ち主のもの）
        $links = [
            [$profileUrl, 'profile', $key],
            [self::json($post)['imageUrl'], 'photo', $key],
            [$items[0]['imageUrl'], 'reaction', $reactorKey],
        ];
        foreach ($links as [$url, $imageId, $signingKey]) {
            $path = (string) parse_url($url, PHP_URL_PATH);
            $rawQuery = (string) parse_url($url, PHP_URL_QUERY);
            parse_str($rawQuery, $query);
            self::assertIsString($query['exp']);
            $exp = (int) $query['exp'];
            self::assertGreaterThanOrEqual($before + 86400, $exp, $url);
            self::assertLessThanOrEqual(time() + 86400, $exp, $url);
            self::assertSame(ImageSignature::base64UrlEncode(ImageSignature::sign($signingKey, $imageId, $exp)), $query['sig'], "not signed by image owner: {$url}");
            // どのリージョンのサーバーからでも取得できる
            foreach (self::REGIONS as $region) {
                $image = $this->request('GET', $url, region: $region);
                self::assertStatus(200, $image, $region);
                self::assertSame($imageId, (string) $image->getBody());
                self::assertSame('image/jpeg', $image->getHeaderLine('Content-Type'));
                self::assertSame('no-store', $image->getHeaderLine('Cache-Control'));
            }
            self::assertStatus(403, $this->request('GET', "/api/images/{$imageId}", $token));
            self::assertStatus(403, $this->request('GET', $path . '?' . http_build_query(['exp' => $exp + 1, 'sig' => $query['sig']])));
            self::assertStatus(403, $this->request('GET', $path . '?' . http_build_query(['exp' => $exp, 'sig' => ImageSignature::base64UrlEncode(str_repeat("\0", 32))])));
            self::assertStatus(403, $this->request('GET', "/api/images/missing?{$rawQuery}"));
        }
        // 別の画像の署名は使えない
        self::assertStatus(403, $this->request('GET', '/api/images/photo?' . parse_url($profileUrl, PHP_URL_QUERY)));

        // 持ち主がリージョンを変えてログアウトしても、発行済みの URL は使える
        self::assertStatus(200, $this->request('PATCH', '/api/me', $token, ['region' => 'europe']));
        self::assertStatus(204, $this->request('POST', '/api/auth/logout', $token));
        foreach ($links as [$url]) {
            self::assertStatus(200, $this->request('GET', $url));
            self::assertStatus(200, $this->request('GET', $url, 'invalid-session'));
        }

        // 削除・退会・差し替えの直後から、全地域で取得できなくなる
        $transitions = [
            'reactor retirement' => ["UPDATE users SET status = 'deleted' WHERE id = 'reactor'", "UPDATE users SET status = 'active' WHERE id = 'reactor'", [2]],
            'reaction replacement' => ["UPDATE realmojis SET replaced_at = clock_timestamp() WHERE id = 'r'", "UPDATE realmojis SET replaced_at = NULL WHERE id = 'r'", [2]],
            'reaction deletion' => ["UPDATE realmojis SET deleted_at = clock_timestamp() WHERE id = 'r'", "UPDATE realmojis SET deleted_at = NULL WHERE id = 'r'", [2]],
            'post deletion' => ["UPDATE posts SET deleted_at = clock_timestamp() WHERE id = 'post'", "UPDATE posts SET deleted_at = NULL WHERE id = 'post'", [1, 2]],
            'author retirement' => ["UPDATE users SET status = 'deleted' WHERE username = 'owner'", "UPDATE users SET status = 'active' WHERE username = 'owner'", [0, 1, 2]],
        ];
        foreach ($transitions as $name => [$update, $reset, $denied]) {
            $this->db->execute($update);
            try {
                foreach (self::REGIONS as $region) {
                    foreach ($denied as $index) {
                        self::assertStatus(403, $this->request('GET', $links[$index][0], region: $region), "{$name} {$region} {$links[$index][1]}");
                    }
                }
            } finally {
                $this->db->execute($reset);
            }
        }
    }

    /**
     * 不正な URL や期限切れの URL は拒否する
     */
    #[DataProvider('malformedQueries')]
    public function testRejectsMalformedAndExpiredUrl(string $query): void
    {
        $response = $this->rawImageRequest('/api/images/image', $query);
        self::assertStatus(403, $response);
        self::assertSame('no-store', $response->getHeaderLine('Cache-Control'));
    }

    /**
     * @return iterable<string, array{string}>
     */
    public static function malformedQueries(): iterable
    {
        $now = time();
        $key = hash('sha256', 'test-secret', true);
        $sign = static fn (int $exp): string => ImageSignature::base64UrlEncode(ImageSignature::sign($key, 'image', $exp));
        $future = (string) ($now + 86400);
        yield 'missing' => [''];
        yield 'missing signature' => ["exp={$future}"];
        yield 'missing expiry' => ['sig=' . $sign($now + 86400)];
        yield 'invalid expiry' => ['exp=tomorrow&sig=x'];
        yield 'overflow' => ['exp=999999999999999999999999&sig=x'];
        yield 'leading zero' => ["exp=0{$future}&sig=x"];
        yield 'invalid signature' => ["exp={$future}&sig=***"];
        yield 'short signature' => ["exp={$future}&sig=YQ"];
        yield 'newline in signature' => ["exp={$future}&sig=" . $sign($now + 86400) . '%0A'];
        yield 'duplicate expiry' => ["exp={$future}&exp={$future}&sig=x"];
        yield 'duplicate signature' => ["exp={$future}&sig=x&sig=y"];
        yield 'malformed query' => ["exp={$future}&sig=%zz"];
        yield 'expired' => ['exp=' . ($now - 1) . '&sig=' . $sign($now - 1)];
        yield 'expiry boundary' => ["exp={$now}&sig=" . $sign($now)];
    }

    /**
     * 正しく署名した画像でも、不正な形のクエリなら拒否する（DB の画像の有無で 403 になっているのではないことを確かめる）
     */
    #[DataProvider('malformedValidSignatureQueries')]
    public function testRejectsMalformedQueryOfExistingImage(string $suffix): void
    {
        $ownerId = $this->insertUser('owner');
        $this->db->execute(
            "INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES ('image', :owner, 'data', 'image/jpeg')",
            ['owner' => $ownerId],
        );
        $this->db->execute("UPDATE users SET profile_image_id = 'image' WHERE id = :id", ['id' => $ownerId]);
        $exp = time() + 86400;
        $valid = "exp={$exp}&sig=" . ImageSignature::base64UrlEncode(ImageSignature::sign(hash('sha256', self::SECRET_KEY, true), 'image', $exp));
        self::assertStatus(200, $this->rawImageRequest('/api/images/image', $valid));
        self::assertStatus(403, $this->rawImageRequest('/api/images/image', $valid . $suffix));
    }

    /**
     * @return iterable<string, array{string}>
     */
    public static function malformedValidSignatureQueries(): iterable
    {
        yield 'malformed extra pair' => ['&x=%zz'];
        yield 'semicolon' => ['&x=1;y=2'];
        yield 'duplicate expiry' => ['&exp=1'];
        yield 'newline in signature' => ['%0A'];
    }

    /**
     * QUERY_STRING をそのまま渡す（PSR-7 の URI は不正な % を直してしまうため）
     */
    private function rawImageRequest(string $path, string $query): ResponseInterface
    {
        $uri = $query === '' ? $path : "{$path}?{$query}";
        $request = new ServerRequestFactory()->createServerRequest('GET', $uri, ['QUERY_STRING' => $query]);
        return $this->app()->handle($request);
    }
}
