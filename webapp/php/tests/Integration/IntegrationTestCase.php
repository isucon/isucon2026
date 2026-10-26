<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Application;
use Isureal\Config;
use Isureal\Database;
use Isureal\Ids;
use Isureal\Repository\SessionRepository;
use Isureal\Repository\UserRepository;
use PHPUnit\Framework\TestCase;
use Psr\Http\Message\ResponseInterface;
use Slim\App;
use Slim\Psr7\Factory\ServerRequestFactory;
use Slim\Psr7\Factory\StreamFactory;
use Slim\Psr7\UploadedFile;

/**
 * DB を使うテスト。ISUREAL_TEST_DATABASE=1 のときだけ実行する。
 * ISUREAL_DB_* の DB のテーブルを作り直すので、使い捨ての DB を指定すること
 */
abstract class IntegrationTestCase extends TestCase
{
    /**
     * テスト用ユーザーのシークレットキー
     */
    protected const string SECRET_KEY = 'secret';

    protected Database $db;

    /**
     * @var array<string, App<\DI\Container>>
     */
    private array $apps = [];

    /**
     * リージョンごとに起動した php -S のプロセスと URL
     *
     * @var array<string, array{resource, string}>
     */
    private static array $servers = [];

    protected function setUp(): void
    {
        if (getenv('ISUREAL_TEST_DATABASE') !== '1') {
            $this->markTestSkipped('ISUREAL_TEST_DATABASE is unset');
        }
        $this->db = Database::connect(self::config());
        $this->db->execute('SET client_min_messages = warning');
        $schema = (string) file_get_contents(__DIR__ . '/../../../sql/001-schema.sql');
        $this->db->executeScript($schema);
        $this->apps = [];
    }

    /**
     * テストのサーバーの設定。リージョンは asia
     */
    protected static function config(string $region = 'asia'): Config
    {
        $env = Config::fromEnv();
        return new Config($region, $env->dbHost, $env->dbPort, $env->dbUser, $env->dbPassword, $env->dbName);
    }

    /**
     * @return App<\DI\Container>
     */
    protected function app(string $region = 'asia'): App
    {
        return $this->apps[$region] ??= Application::create([Config::class => self::config($region)]);
    }

    protected function insertUser(string $username, string $region = 'asia', string $accountType = 'regular', ?string $id = null): string
    {
        $id ??= Ids::generate();
        $this->db->execute(
            'INSERT INTO users (id, username, secret_key_hash, display_name, region, account_type)
             VALUES (:id, :username, :secret_key_hash, :display_name, :region, :account_type)',
            [
                'id' => $id,
                'username' => $username,
                'secret_key_hash' => Ids::sha256(self::SECRET_KEY),
                'display_name' => $username,
                'region' => $region,
                'account_type' => $accountType,
            ],
        );
        return $id;
    }

    protected function login(string $userId): string
    {
        return new SessionRepository($this->db, new UserRepository($this->db))->createInTransaction($userId)->token;
    }

    /**
     * 開いている投稿期間を作る
     */
    protected function openWindow(string $region = 'asia', string $localDate = '2026-09-23', ?string $id = null): string
    {
        $id ??= Ids::generate();
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline)
             VALUES (:id, :region, :local_date, clock_timestamp() - interval '1 minute', clock_timestamp() + interval '1 minute')",
            ['id' => $id, 'region' => $region, 'local_date' => $localDate],
        );
        return $id;
    }

    /**
     * @param array<string, mixed>|null $json
     * @param array<string, string> $headers
     */
    protected function request(string $method, string $path, ?string $token = null, ?array $json = null, array $headers = [], string $region = 'asia'): ResponseInterface
    {
        $request = new ServerRequestFactory()->createServerRequest($method, $path);
        if ($token !== null) {
            $request = $request->withHeader('Authorization', "Bearer {$token}");
        }
        if ($json !== null) {
            $body = json_encode($json, JSON_THROW_ON_ERROR);
            $request = $request
                ->withHeader('Content-Type', 'application/json')
                ->withHeader('Content-Length', (string) strlen($body))
                ->withBody(new StreamFactory()->createStream($body));
        }
        foreach ($headers as $name => $value) {
            $request = $request->withHeader($name, $value);
        }
        parse_str($request->getUri()->getQuery(), $query);
        return $this->app($region)->handle($request->withQueryParams($query));
    }

    /**
     * 生のボディを送る
     *
     * @param array<string, string> $headers
     */
    protected function requestRaw(string $method, string $path, ?string $token, string $body, array $headers): ResponseInterface
    {
        $request = new ServerRequestFactory()->createServerRequest($method, $path)
            ->withHeader('Content-Length', (string) strlen($body))
            ->withBody(new StreamFactory()->createStream($body));
        if ($token !== null) {
            $request = $request->withHeader('Authorization', "Bearer {$token}");
        }
        foreach ($headers as $name => $value) {
            $request = $request->withHeader($name, $value);
        }
        return $this->app()->handle($request);
    }

    /**
     * multipart/form-data を送る（php-fpm が解釈した後の形にする）
     *
     * @param array<string, string> $fields
     * @param array<string, string> $files 名前とファイルの中身
     */
    protected function requestMultipart(string $method, string $path, ?string $token, array $fields, array $files): ResponseInterface
    {
        $uploaded = [];
        foreach ($files as $name => $data) {
            $uploaded[$name] = new UploadedFile(new StreamFactory()->createStream($data), "{$name}.jpg", 'image/jpeg', strlen($data));
        }
        $request = new ServerRequestFactory()->createServerRequest($method, $path)
            ->withHeader('Content-Type', 'multipart/form-data; boundary=test')
            ->withHeader('Content-Length', '1')
            ->withParsedBody($fields)
            ->withUploadedFiles($uploaded);
        if ($token !== null) {
            $request = $request->withHeader('Authorization', "Bearer {$token}");
        }
        return $this->app()->handle($request);
    }

    /**
     * 実際の HTTP サーバー（php -S）の URL を返す。同時に送るリクエストや通知のテストに使う
     */
    protected static function serverUrl(string $region = 'asia'): string
    {
        if (isset(self::$servers[$region])) {
            return self::$servers[$region][1];
        }
        $socket = stream_socket_server('tcp://127.0.0.1:0') ?: throw new \RuntimeException('failed to find a free port');
        $address = (string) stream_socket_get_name($socket, false);
        fclose($socket);
        $config = self::config($region);
        $env = [
            ...getenv(),
            'PHP_CLI_SERVER_WORKERS' => '16',
            'ISUREAL_REGION' => $region,
            'ISUREAL_DB_HOST' => $config->dbHost,
            'ISUREAL_DB_PORT' => $config->dbPort,
            'ISUREAL_DB_USER' => $config->dbUser,
            'ISUREAL_DB_PASSWORD' => $config->dbPassword,
            'ISUREAL_DB_NAME' => $config->dbName,
        ];
        $command = [PHP_BINARY, '-d', 'error_log=/dev/null', '-S', $address, __DIR__ . '/router.php'];
        $process = proc_open($command, [0 => ['file', '/dev/null', 'r'], 1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']], $pipes, null, $env)
            ?: throw new \RuntimeException('failed to start php -S');
        $url = "http://{$address}";
        // 待ち受けを始めるまで待つ
        for ($i = 0; $i < 100 && ($connection = @stream_socket_client("tcp://{$address}")) === false; $i++) {
            usleep(50_000);
        }
        if (isset($connection) && $connection !== false) {
            fclose($connection);
        }
        self::$servers[$region] = [$process, $url];
        register_shutdown_function(static function () use ($process): void {
            proc_terminate($process);
            proc_close($process);
        });
        return $url;
    }

    /**
     * 実際の HTTP サーバーにリクエストを同時に送り、ステータスと応答のボディを返す
     *
     * @param list<array{non-empty-string, string, ?string, ?array<string, mixed>}> $requests メソッド、パス、トークン、JSON
     * @return list<array{int, string}>
     */
    protected static function concurrently(array $requests, string $region = 'asia'): array
    {
        $url = self::serverUrl($region);
        $multi = curl_multi_init();
        $handles = [];
        foreach ($requests as [$method, $path, $token, $json]) {
            $handle = curl_init($url . $path) ?: throw new \RuntimeException('curl_init failed');
            $headers = $token === null ? [] : ["Authorization: Bearer {$token}"];
            if ($json !== null) {
                $headers[] = 'Content-Type: application/json';
                curl_setopt($handle, CURLOPT_POSTFIELDS, json_encode($json, JSON_THROW_ON_ERROR));
            }
            curl_setopt_array($handle, [
                CURLOPT_CUSTOMREQUEST => $method,
                CURLOPT_HTTPHEADER => $headers,
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

    /**
     * @return array<mixed>
     */
    protected static function json(ResponseInterface $response): array
    {
        return json_decode((string) $response->getBody(), true, flags: JSON_THROW_ON_ERROR);
    }

    protected static function assertStatus(int $expected, ResponseInterface $response, string $message = ''): void
    {
        self::assertSame($expected, $response->getStatusCode(), trim("{$message} " . $response->getBody()));
    }

    protected static function assertError(int $status, string $error, ResponseInterface $response): void
    {
        self::assertStatus($status, $response);
        self::assertSame(['error' => $error], self::json($response));
    }

    /**
     * テスト用の JPEG
     *
     * @param int<1, max> $width
     * @param int<1, max> $height
     */
    protected static function jpeg(int $width = 2, int $height = 2): string
    {
        $image = imagecreatetruecolor($width, $height) ?: throw new \RuntimeException('imagecreatetruecolor failed');
        ob_start();
        imagejpeg($image);
        return (string) ob_get_clean();
    }
}
