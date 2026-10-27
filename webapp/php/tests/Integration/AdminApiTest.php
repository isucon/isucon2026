<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Ids;

final class AdminApiTest extends IntegrationTestCase
{
    public function testAdminLoginAndLogout(): void
    {
        $this->db->execute(
            "INSERT INTO administrators (id, username, password_hash) VALUES ('admin1', 'admin', :hash)",
            ['hash' => Ids::sha256('password')],
        );
        $login = function (): array {
            $response = $this->requestRaw('POST', '/api/admin/auth/login', null, '{"username":"admin","password":"password"}', ['Content-Type' => 'application/json']);
            self::assertStatus(200, $response);
            $body = self::json($response);
            self::assertNotSame('', $body['token'] ?? '');
            self::assertSame('admin1', $body['administratorId'] ?? null);
            self::assertStringNotContainsString('password', (string) $response->getBody(), 'password exposed');
            return $body;
        };
        foreach (
            [
                '{"username":"admin","password":"wrong"}',
                '{"username":"missing","password":"password"}',
                '{"username":"Admin","password":"password"}',
            ] as $body
        ) {
            self::assertError(401, 'invalid_credentials', $this->requestRaw('POST', '/api/admin/auth/login', null, $body, ['Content-Type' => 'application/json']));
        }
        self::assertStatus(400, $this->requestRaw('POST', '/api/admin/auth/login', null, '{', ['Content-Type' => 'application/json']));

        [$first, $second] = [$login(), $login()];
        self::assertNotSame($first['token'], $second['token'], 'token reused');
        self::assertSame(2, $this->db->fetchValue('SELECT count(*) FROM administrator_sessions WHERE active'));

        $logout = fn (?string $token): \Psr\Http\Message\ResponseInterface => $this->requestRaw('POST', '/api/admin/auth/logout', $token, '', ['Content-Type' => 'application/json']);
        self::assertStatus(401, $logout(null));
        self::assertStatus(401, $logout('unknown'));
        self::assertStatus(204, $logout($first['token']));
        self::assertStatus(401, $logout($first['token']));
        self::assertStatus(204, $logout($second['token']));

        $this->db->executeScript(<<<'SQL'
            CREATE FUNCTION reject_admin_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject session'; END $$;
            CREATE TRIGGER reject_admin_session BEFORE INSERT ON administrator_sessions FOR EACH ROW EXECUTE FUNCTION reject_admin_session();
            SQL);
        try {
            self::assertStatus(500, $this->requestRaw('POST', '/api/admin/auth/login', null, '{"username":"admin","password":"password"}', ['Content-Type' => 'application/json']));
        } finally {
            $this->db->executeScript('DROP FUNCTION reject_admin_session() CASCADE');
        }
    }
}
