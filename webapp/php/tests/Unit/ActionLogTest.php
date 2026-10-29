<?php

declare(strict_types=1);

namespace Isureal\Tests\Unit;

use Isureal\Http\ActionLog;
use Isureal\Http\ActionLogEntry;
use Isureal\Http\ApiError;
use Isureal\Http\ErrorHandler;
use Isureal\Http\Input;
use Isureal\Http\Middleware\ActionLogMiddleware;
use PHPUnit\Framework\TestCase;
use Psr\Http\Message\ResponseInterface as Response;
use Psr\Http\Message\ServerRequestInterface as Request;
use Slim\App;
use Slim\Factory\AppFactory;
use Slim\Psr7\Factory\ServerRequestFactory;
use Slim\Psr7\Factory\StreamFactory;

final class ActionLogTest extends TestCase
{
    public function testActionLog(): void
    {
        [$app, $output] = self::app('debug');
        self::send($app, 'POST', '/api/auth/login', '{"username":"alice","secretKey":"wrong"}');
        self::send($app, 'POST', '/api/auth/login', '{"username":"Not Valid","secretKey":"wrong"}');
        self::send($app, 'POST', '/api/posts', '{}');
        self::send($app, 'GET', '/api/feeds/friends', '');
        self::send($app, 'GET', '/api/users/someone', '');
        self::send($app, 'GET', '/api/unknown', '');
        self::send($app, 'DELETE', '/api/feeds/friends', '');
        self::assertSame([
            ['action' => 'login', 'ip' => '203.0.113.7', 'level' => 'info', 'status' => 401, 'user_id' => null, 'username' => 'alice'],
            ['action' => 'login', 'ip' => '203.0.113.7', 'level' => 'info', 'status' => 401, 'user_id' => null, 'username' => null],
            ['action' => 'create_post', 'ip' => '203.0.113.7', 'level' => 'info', 'status' => 201, 'user_id' => 'user-1'],
            ['action' => 'get_feed', 'ip' => '203.0.113.7', 'level' => 'debug', 'status' => 200, 'user_id' => 'user-1'],
            ['action' => 'get_user', 'ip' => '203.0.113.7', 'level' => 'error', 'status' => 500, 'user_id' => null],
            ['action' => null, 'ip' => '203.0.113.7', 'level' => 'debug', 'status' => 404, 'user_id' => null],
            ['action' => null, 'ip' => '203.0.113.7', 'level' => 'info', 'status' => 405, 'user_id' => null],
        ], self::lines($output));

        // info では読み取り（GET）の行を出さない
        [$app, $output] = self::app('info');
        self::send($app, 'GET', '/api/feeds/friends', '');
        self::send($app, 'POST', '/api/posts', '{}');
        self::send($app, 'PUT', '/api/me/profile-image', '');
        self::send($app, 'GET', '/api/images/image-id', '');
        $lines = self::lines($output);
        self::assertCount(3, $lines);
        self::assertSame('create_post', $lines[0]['action']);
        self::assertSame(['action' => 'put_profile_image', 'ip' => '203.0.113.7', 'level' => 'info', 'status' => 400, 'user_id' => null, 'abuse' => 'invalid_image'], $lines[1]);
        self::assertSame(['action' => 'get_image', 'ip' => '203.0.113.7', 'level' => 'info', 'status' => 403, 'user_id' => null, 'abuse' => 'invalid_signature'], $lines[2]);
    }

    public function testKeyOrderAndEncoding(): void
    {
        [$app, $output] = self::app('debug');
        self::send($app, 'POST', '/api/auth/login', '{"username":"alice","secretKey":"wrong"}', "a/b\xff");
        rewind($output);
        self::assertSame(
            '{"action":"login","ip":"a/b' . "\u{fffd}" . '","level":"info","status":401,"user_id":null,"username":"alice"}' . "\n",
            stream_get_contents($output),
        );
    }

    public function testClientIpFallsBackToRemoteAddr(): void
    {
        [$app, $output] = self::app('debug');
        $request = new ServerRequestFactory()->createServerRequest('POST', '/api/posts', ['REMOTE_ADDR' => '198.51.100.1']);
        $app->handle($request);
        $request = new ServerRequestFactory()->createServerRequest('POST', '/api/posts', ['REMOTE_ADDR' => '198.51.100.1'])
            ->withHeader('X-Client-IP', '');
        $app->handle($request);
        self::assertSame(['198.51.100.1', '198.51.100.1'], array_column(self::lines($output), 'ip'));
    }

    /**
     * Application と同じ順にミドルウェアを積んだ、テスト用のハンドラだけのアプリ
     *
     * @return array{App<\Psr\Container\ContainerInterface|null>, resource}
     */
    private static function app(string $minLevel): array
    {
        $output = fopen('php://memory', 'w+') ?: throw new \RuntimeException('failed to open php://memory');
        $app = AppFactory::create();
        $app->post('/api/auth/login', function (Request $request, Response $response): Response {
            ActionLogEntry::of($request)->username = Input::json($request)->string('username');
            throw new ApiError(401, 'invalid_credentials');
        });
        $app->post('/api/posts', function (Request $request, Response $response): Response {
            ActionLogEntry::of($request)->userId = 'user-1';
            return $response->withStatus(201);
        });
        $app->get('/api/feeds/{feed}', function (Request $request, Response $response): Response {
            ActionLogEntry::of($request)->userId = 'user-1';
            return $response;
        });
        $app->get('/api/users/{userId}', function (Request $request, Response $response): Response {
            throw new \RuntimeException('boom');
        });
        $app->put('/api/me/profile-image', function (Request $request, Response $response): Response {
            ActionLogEntry::of($request)->abuse = 'invalid_image';
            throw new ApiError(400, 'invalid_profile_image');
        });
        $app->get('/api/images/{imageId}', function (Request $request, Response $response): Response {
            ActionLogEntry::of($request)->abuse = 'invalid_signature';
            throw new ApiError(403, 'image_forbidden');
        });
        $app->addRoutingMiddleware();
        $app->addErrorMiddleware(false, false, false)
            ->setDefaultErrorHandler(new ErrorHandler($app->getResponseFactory()));
        $app->add(new ActionLogMiddleware($app->getRouteResolver(), new ActionLog($minLevel, $output)));
        return [$app, $output];
    }

    /**
     * @param App<\Psr\Container\ContainerInterface|null> $app
     */
    private static function send(App $app, string $method, string $path, string $body, string $clientIp = '203.0.113.7'): void
    {
        $request = new ServerRequestFactory()->createServerRequest($method, $path, ['REMOTE_ADDR' => '127.0.0.1'])
            ->withHeader('Content-Type', 'application/json')
            ->withHeader('X-Client-IP', $clientIp)
            ->withBody(new StreamFactory()->createStream($body));
        $app->handle($request);
    }

    /**
     * @param resource $output
     * @return list<array<string, mixed>>
     */
    private static function lines(mixed $output): array
    {
        rewind($output);
        $lines = [];
        foreach (explode("\n", trim((string) stream_get_contents($output))) as $line) {
            if ($line !== '') {
                $lines[] = json_decode($line, true, flags: JSON_THROW_ON_ERROR);
            }
        }
        return $lines;
    }
}
