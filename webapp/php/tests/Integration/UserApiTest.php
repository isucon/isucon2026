<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Ids;

final class UserApiTest extends IntegrationTestCase
{
    public function testRegisterLoginAndProfile(): void
    {
        $registered = $this->request('POST', '/api/auth/register', json: ['username' => 'alice', 'displayName' => 'アリス', 'region' => 'asia']);
        self::assertStatus(201, $registered);
        $result = self::json($registered);
        $secret = $result['secretKey'];
        self::assertNotSame('', $secret);
        self::assertArrayNotHasKey('token', $result);
        self::assertSame(['username' => 'alice', 'displayName' => 'アリス'], array_intersect_key($result['user'], ['username' => 0, 'displayName' => 0]));
        self::assertSame(0, $this->db->fetchValue('SELECT count(*) FROM login_sessions'), 'registration created a session');

        $firstLogin = $this->request('POST', '/api/auth/login', json: ['username' => 'alice', 'secretKey' => $secret]);
        self::assertStatus(200, $firstLogin);
        $token = self::json($firstLogin)['token'];

        $me = $this->request('GET', '/api/me', $token);
        self::assertStatus(200, $me);
        $meBody = self::json($me);
        self::assertArrayHasKey('profileImageUrl', $meBody);
        self::assertNull($meBody['profileImageUrl']);
        self::assertStringNotContainsString('secret', (string) $me->getBody());

        self::assertStatus(409, $this->request('POST', '/api/auth/register', json: ['username' => 'alice', 'displayName' => 'Other', 'region' => 'asia']));
        self::assertStatus(400, $this->request('POST', '/api/auth/register', json: ['username' => 'ALICE', 'displayName' => 'Other', 'region' => 'asia']));
        self::assertStatus(401, $this->request('POST', '/api/auth/login', json: ['username' => 'ALICE', 'secretKey' => $secret]));

        $unauthorized = $this->request('GET', '/api/me');
        self::assertError(401, 'invalid_session', $unauthorized);
        self::assertStringStartsWith('application/json', $unauthorized->getHeaderLine('Content-Type'));

        self::assertStatus(401, $this->request('POST', '/api/auth/login', json: ['username' => 'alice', 'secretKey' => 'wrong']));
        self::assertStatus(401, $this->request('POST', '/api/auth/login', json: ['username' => 'missing', 'secretKey' => 'wrong']));

        $login = $this->request('POST', '/api/auth/login', json: ['username' => 'alice', 'secretKey' => $secret]);
        self::assertStatus(200, $login);
        self::assertArrayNotHasKey('secretKey', self::json($login));
        $newToken = self::json($login)['token'];
        // ログインし直すと前のセッションは無効になる
        self::assertStatus(401, $this->request('GET', '/api/me', $token));
        self::assertStatus(200, $this->request('GET', '/api/me', $newToken));

        $patched = $this->request('PATCH', '/api/me', $newToken, ['displayName' => '新しい名前', 'bio' => 'こんにちは', 'region' => 'europe']);
        self::assertStatus(200, $patched);
        self::assertSame(['bio' => 'こんにちは', 'region' => 'europe'], array_intersect_key(self::json($patched), ['bio' => 0, 'region' => 0]));
        self::assertStatus(200, $this->request('PATCH', '/api/me', $newToken, ['bio' => '']));
        // リージョンを変えたので、europe のサーバーで確かめる
        self::assertError(403, 'region_mismatch', $this->request('GET', '/api/me', $newToken));
        $meBody = self::json($this->request('GET', '/api/me', $newToken, region: 'europe'));
        self::assertSame(['displayName' => '新しい名前', 'bio' => '', 'region' => 'europe'], array_intersect_key($meBody, ['displayName' => 0, 'bio' => 0, 'region' => 0]));

        foreach (['{}', '{"displayName":""}', '{"region":"invalid"}', '{"username":"changed"}', 'null', '[]', '{"bio":1}', '{'] as $body) {
            self::assertStatus(400, $this->requestRaw('PATCH', '/api/me', $newToken, $body, ['Content-Type' => 'application/json']), $body);
        }
        self::assertStatus(415, $this->requestRaw('PATCH', '/api/me', $newToken, '{"bio":""}', ['Content-Type' => 'text/plain']));
        self::assertStatus(200, $this->request('PATCH', '/api/me', $newToken, ['displayName' => str_repeat('あ', 100)]));
        self::assertStatus(400, $this->request('PATCH', '/api/me', $newToken, ['displayName' => str_repeat('あ', 101)]));

        self::assertStatus(204, $this->request('POST', '/api/auth/logout', $newToken));
        self::assertStatus(401, $this->request('GET', '/api/me', $newToken, region: 'europe'));
        self::assertStatus(401, $this->request('POST', '/api/auth/logout', $newToken));
        self::assertError(404, 'Not Found', $this->request('GET', '/api/v1/me', $newToken));
        self::assertError(405, 'Method Not Allowed', $this->request('PUT', '/api/me', $newToken));
    }

    public function testFailedLoginLeavesNoSession(): void
    {
        $this->db->executeScript(<<<'SQL'
            CREATE OR REPLACE FUNCTION reject_user_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'insertion rejected'; END $$;
            CREATE TRIGGER reject_user_session BEFORE INSERT ON login_sessions FOR EACH ROW EXECUTE FUNCTION reject_user_session();
            SQL);
        try {
            $registered = $this->request('POST', '/api/auth/register', json: ['username' => 'independent', 'displayName' => 'Independent', 'region' => 'asia']);
            self::assertStatus(201, $registered);
            $login = $this->request('POST', '/api/auth/login', json: ['username' => 'independent', 'secretKey' => self::json($registered)['secretKey']]);
            self::assertStatus(500, $login);
            self::assertSame(0, $this->db->fetchValue("SELECT count(*) FROM login_sessions s JOIN users u ON u.id = s.user_id WHERE u.username = 'independent'"));
        } finally {
            // スキーマを作り直しても関数は残るので、次回のテストのために消す
            $this->db->executeScript('DROP FUNCTION reject_user_session() CASCADE');
        }
    }

    public function testInvalidAuthorizationHeader(): void
    {
        $userId = $this->insertUser('alice');
        $token = $this->login($userId);
        self::assertError(401, 'invalid_session', $this->request('GET', '/api/me', headers: ['Authorization' => "Token {$token}"]));
        self::assertSame(Ids::sha256($token), $this->db->fetchValue('SELECT token_hash FROM login_sessions WHERE active'));
    }

    public function testConcurrentRegistration(): void
    {
        $request = ['POST', '/api/auth/register', null, ['username' => 'concurrent', 'displayName' => '同時登録', 'region' => 'asia']];
        $statuses = array_column(self::concurrently(array_fill(0, 8, $request)), 0);
        self::assertSame(1, count(array_keys($statuses, 201, true)), implode(',', $statuses));
        self::assertSame(7, count(array_keys($statuses, 409, true)), implode(',', $statuses));
    }
}
