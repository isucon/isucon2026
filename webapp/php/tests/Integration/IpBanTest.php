<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Slim\Psr7\Factory\ServerRequestFactory;
use Slim\Psr7\Factory\StreamFactory;

final class IpBanTest extends IntegrationTestCase
{
    private const string PROXY = '127.0.0.1';

    public function testIpBan(): void
    {
        $this->ban('203.0.113.7', '2 minutes');
        $this->ban('203.0.113.8', '-1 second');
        $this->send('POST', '/api/auth/login', self::PROXY, '203.0.113.7', 403);
        $this->send('GET', '/api/images/missing', self::PROXY, '203.0.113.7', 403);
        // ルートがなくても BAN を優先する
        $this->send('GET', '/api/unknown', self::PROXY, '203.0.113.7', 403);
        $this->send('DELETE', '/api/auth/login', self::PROXY, '203.0.113.7', 403);
        $this->send('POST', '/api/auth/login', self::PROXY, '203.0.113.8', 401);
        $this->send('POST', '/api/auth/login', self::PROXY, '198.51.100.1', 401);
        $this->send('POST', '/api/auth/login', '203.0.113.7', '', 403);
        $this->send('POST', '/api/auth/login', '203.0.113.7', '198.51.100.1', 401);

        // 追加した BAN はすぐに反映され、期限を過ぎたら解ける
        $this->ban('198.51.100.1', '600 milliseconds');
        $this->send('POST', '/api/auth/login', self::PROXY, '198.51.100.1', 403);
        usleep(650_000);
        $this->send('POST', '/api/auth/login', self::PROXY, '198.51.100.1', 401);

        $logs = $this->actionLogs();
        self::assertSame(['action' => 'login', 'ip' => '203.0.113.7', 'level' => 'info', 'status' => 403, 'user_id' => null, 'username' => null], $logs[0]);
        self::assertSame(['action' => 'get_image', 'ip' => '203.0.113.7', 'level' => 'debug', 'status' => 403, 'user_id' => null], $logs[1]);
        self::assertSame(['action' => null, 'ip' => '203.0.113.7', 'level' => 'debug', 'status' => 403, 'user_id' => null], $logs[2]);
        self::assertSame(['action' => 'login', 'ip' => '203.0.113.8', 'level' => 'info', 'status' => 401, 'user_id' => null, 'username' => 'nobody'], $logs[4]);
    }

    public function testNotifications(): void
    {
        $this->ban('203.0.113.7', '2 minutes');
        $url = self::serverUrl() . '/api/notifications';
        foreach ([['GET', 403, ['error' => 'ip_banned']], ['POST', 403, ['error' => 'ip_banned']]] as [$method, $status, $body]) {
            $handle = curl_init($url) ?: throw new \RuntimeException('curl_init failed');
            curl_setopt_array($handle, [
                CURLOPT_CUSTOMREQUEST => $method,
                CURLOPT_HTTPHEADER => ['X-Client-IP: 203.0.113.7'],
                CURLOPT_RETURNTRANSFER => true,
                CURLOPT_TIMEOUT => 10,
            ]);
            $response = (string) curl_exec($handle);
            self::assertSame($status, curl_getinfo($handle, CURLINFO_RESPONSE_CODE), $response);
            self::assertSame($body, json_decode($response, true));
        }
        [[$status, $response]] = self::concurrently([['GET', '/api/notifications', null, null]]);
        self::assertSame(401, $status, $response);
    }

    public function testLoginLogsUsername(): void
    {
        $this->insertUser('alice');
        $request = fn (string $path, array $json) => $this->request('POST', $path, json: $json, headers: ['X-Client-IP' => '192.0.2.1']);
        self::assertStatus(201, $request('/api/auth/register', ['username' => 'bob', 'displayName' => 'Bob', 'region' => 'asia']));
        self::assertStatus(401, $request('/api/auth/login', ['username' => 'alice', 'secretKey' => 'wrong']));
        $login = $request('/api/auth/login', ['username' => 'alice', 'secretKey' => self::SECRET_KEY]);
        self::assertStatus(200, $login);
        $token = self::json($login)['token'];
        self::assertIsString($token);
        self::assertStatus(200, $this->request('GET', '/api/me', $token, headers: ['X-Client-IP' => '192.0.2.1']));
        $userId = self::json($login)['user']['id'];
        self::assertSame([
            ['action' => 'register', 'ip' => '192.0.2.1', 'level' => 'info', 'status' => 201, 'user_id' => null, 'username' => 'bob'],
            ['action' => 'login', 'ip' => '192.0.2.1', 'level' => 'info', 'status' => 401, 'user_id' => null, 'username' => 'alice'],
            ['action' => 'login', 'ip' => '192.0.2.1', 'level' => 'info', 'status' => 200, 'user_id' => null, 'username' => 'alice'],
            ['action' => 'get_me', 'ip' => '192.0.2.1', 'level' => 'debug', 'status' => 200, 'user_id' => $userId],
        ], $this->actionLogs());
    }

    private function ban(string $ip, string $until): void
    {
        $this->db->execute('INSERT INTO ip_bans (ip, banned_until) VALUES (:ip, clock_timestamp() + CAST(:until AS interval))', ['ip' => $ip, 'until' => $until]);
    }

    private function send(string $method, string $path, string $remoteAddr, string $clientIp, int $status): void
    {
        $body = '{"username":"nobody","secretKey":"wrong"}';
        $request = new ServerRequestFactory()->createServerRequest($method, $path, ['REMOTE_ADDR' => $remoteAddr])
            ->withHeader('Content-Type', 'application/json')
            ->withHeader('Content-Length', (string) strlen($body))
            ->withBody(new StreamFactory()->createStream($body));
        if ($clientIp !== '') {
            $request = $request->withHeader('X-Client-IP', $clientIp);
        }
        $response = $this->app()->handle($request);
        self::assertStatus($status, $response, "{$method} {$path} from {$remoteAddr} (X-Client-IP {$clientIp})");
        if ($status === 403) {
            self::assertSame(['error' => 'ip_banned'], self::json($response));
        }
    }
}
