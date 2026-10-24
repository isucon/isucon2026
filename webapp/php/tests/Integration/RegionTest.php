<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Repository\AdminSessionRepository;

final class RegionTest extends IntegrationTestCase
{
    private const array REGION_ROUTES = [
        ['GET', '/api/me'], ['GET', '/api/users?q=x'], ['GET', '/api/users/other'],
        ['POST', '/api/me/account-deletion'], ['POST', '/api/posts'], ['GET', '/api/posting-window/current'], ['GET', '/api/posts/me/current'],
        ['GET', '/api/posts/post'], ['PATCH', '/api/posts/post'], ['DELETE', '/api/posts/post'], ['GET', '/api/feeds/public'],
        ['GET', '/api/memories?month=2026-09'], ['GET', '/api/memories/2026-09-24'],
        ['POST', '/api/posts/post/comments'], ['GET', '/api/posts/post/comments'], ['DELETE', '/api/comments/comment'],
        ['POST', '/api/posts/post/realmojis'], ['GET', '/api/posts/post/realmojis'], ['DELETE', '/api/realmojis/reaction'],
        ['POST', '/api/friend-requests'],
        ['POST', '/api/friend-requests/request/accept'], ['GET', '/api/relationships/friends'], ['DELETE', '/api/friendships/other'],
        ['PUT', '/api/follows/other'], ['DELETE', '/api/follows/other'], ['PUT', '/api/blocks/other'], ['DELETE', '/api/blocks/other'],
    ];

    public function testRegion(): void
    {
        $registered = $this->request('POST', '/api/auth/register', json: ['username' => 'traveler', 'displayName' => 'Traveler', 'region' => 'europe']);
        self::assertStatus(201, $registered);
        $registration = self::json($registered);
        $login = function () use ($registration): string {
            $response = $this->request('POST', '/api/auth/login', json: ['username' => 'traveler', 'secretKey' => $registration['secretKey']]);
            self::assertStatus(200, $response);
            return self::json($response)['token'];
        };
        $token = $login();

        // asia のサーバーでは europe のユーザーは使えない
        foreach (self::REGION_ROUTES as [$method, $path]) {
            self::assertError(403, 'region_mismatch', $this->request($method, $path, $token, headers: ['Content-Type' => 'application/json']));
        }
        // 通知は別の入口（notifications.php）なので実際のサーバーで確かめる
        [[$status, $body]] = self::concurrently([['GET', '/api/notifications', $token, null]]);
        self::assertSame(403, $status, $body);
        self::assertSame(['error' => 'region_mismatch'], json_decode($body, true));

        self::assertStatus(401, $this->request('GET', '/api/me', headers: ['Authorization' => 'Bearer ']));
        // リージョンによらず使える
        self::assertStatus(200, $this->request('PATCH', '/api/me', $token, ['displayName' => 'Changed', 'bio' => 'bio']));
        self::assertStatus(204, $this->requestRaw('PUT', '/api/me/profile-image', $token, 'image bytes', ['Content-Type' => 'image/jpeg']));
        self::assertStatus(204, $this->request('DELETE', '/api/me/profile-image', $token));
        self::assertStatus(200, $this->request('PATCH', '/api/me', $token, ['region' => 'asia']));
        self::assertStatus(200, $this->request('GET', '/api/me', $token));
        self::assertStatus(200, $this->request('GET', '/api/memories?month=2026-09', $token));
        self::assertStatus(200, $this->request('PATCH', '/api/me', $token, ['region' => 'america']));
        self::assertStatus(403, $this->request('GET', '/api/me', $token));
        self::assertStatus(204, $this->request('POST', '/api/auth/logout', $token));
        self::assertStatus(401, $this->request('GET', '/api/me', $token));

        $token = $login();
        self::assertStatus(200, $this->request('GET', '/api/me', $token, region: 'america'));
        self::assertStatus(403, $this->request('GET', '/api/me', $token, region: 'europe'));

        // 管理者の API はリージョンによらない
        $this->db->execute("INSERT INTO administrators (id, username, password_hash) VALUES ('admin', 'admin', 'hash')");
        $admin = new AdminSessionRepository($this->db)->create('admin');
        self::assertStatus(204, $this->request('PUT', "/api/admin/users/{$registration['user']['id']}/certification", $admin->token, region: 'europe'));
    }
}
