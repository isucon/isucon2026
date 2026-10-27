<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Ids;
use Isureal\Repository\AdminSessionRepository;

final class AdminSessionTest extends IntegrationTestCase
{
    public function testAdminSession(): void
    {
        // 同時に作るのは HTTP のログインで行うので、admin1 にはパスワードを付ける
        $this->db->execute(
            "INSERT INTO administrators (id, username, password_hash) VALUES ('admin1', 'admin', :hash), ('admin2', 'other', 'hash')",
            ['hash' => Ids::sha256('password')],
        );
        $sessions = new AdminSessionRepository($this->db);
        $first = $sessions->create('admin1');
        self::assertNotSame('', $first->token);
        self::assertSame(Ids::sha256($first->token), $first->tokenHash);
        self::assertSame('admin1', $first->administratorId);
        self::assertGreaterThan(0, $first->createdAt->getTimestamp());

        // 同時に作っても、他のセッションは無効にならない
        $request = ['POST', '/api/admin/auth/login', null, ['username' => 'admin', 'password' => 'password']];
        self::assertSame(array_fill(0, 8, 200), array_column(self::concurrently(array_fill(0, 8, $request)), 0));
        self::assertSame(9, $this->db->fetchValue('SELECT count(*) FROM administrator_sessions WHERE active'));

        try {
            $sessions->create('missing');
            self::fail('missing administrator accepted');
        } catch (\PDOException) {
            // 失敗する
        }

        // 管理者のセッションが通ると、存在しないユーザーの認定で 409 になる
        $check = function (string $header, int $status): void {
            $response = $this->request('PUT', '/api/admin/users/missing/certification', headers: $header === '' ? [] : ['Authorization' => $header]);
            self::assertStatus($status, $response);
        };
        $check("Bearer {$first->token}", 409);
        $check('', 401);
        $check('Basic invalid', 401);
        $check('Bearer unknown', 401);
        // 管理者のトークンはユーザーのセッションとしては使えない
        self::assertStatus(401, $this->request('GET', '/api/me', headers: ['Authorization' => "Bearer {$first->token}"]));

        $userSession = $this->login($this->insertUser('alice', id: 'user1'));
        $check("Bearer {$userSession}", 401);

        // 別の管理者として無効にしても、無効にならない
        $sessions->invalidate('admin2', $first->tokenHash);
        $check("Bearer {$first->token}", 409);
        for ($i = 0; $i < 2; $i++) {
            $sessions->invalidate('admin1', $first->tokenHash);
        }
        $check("Bearer {$first->token}", 401);
        self::assertSame(8, $this->db->fetchValue('SELECT count(*) FROM administrator_sessions WHERE active'), 'logout invalidated other sessions');
        self::assertTrue($this->db->fetchValue('SELECT invalidated_at IS NOT NULL FROM administrator_sessions WHERE token_hash = :hash', ['hash' => $first->tokenHash]), 'missing invalidated_at');
    }
}
