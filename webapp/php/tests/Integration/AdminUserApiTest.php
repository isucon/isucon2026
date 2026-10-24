<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Repository\AdminSessionRepository;

final class AdminUserApiTest extends IntegrationTestCase
{
    public function testCertifyUser(): void
    {
        $this->db->execute("INSERT INTO administrators (id, username, password_hash) VALUES ('admin', 'admin', 'hash')");
        $sessions = new AdminSessionRepository($this->db);
        $admin = $sessions->create('admin');
        foreach (['target', 'viewer', 'deleted'] as $id) {
            $this->insertUser($id, id: $id);
        }
        $this->db->execute("UPDATE users SET status = 'deleted' WHERE id = 'deleted'");
        $viewer = $this->login('viewer');

        $path = '/api/admin/users/target/certification';
        foreach (['', $viewer] as $token) {
            $headers = ['Authorization' => "Bearer {$token}"];
            self::assertStatus(401, $this->request('PUT', $path, headers: $headers));
            self::assertStatus(401, $this->request('DELETE', $path, headers: $headers));
        }
        // 認定されていないユーザーはフォローできない
        self::assertStatus(409, $this->request('PUT', '/api/follows/target', $viewer));
        foreach (['missing', 'deleted'] as $id) {
            self::assertStatus(409, $this->request('PUT', "/api/admin/users/{$id}/certification", $admin->token));
        }

        $results = self::concurrently(array_fill(0, 2, ['PUT', $path, $admin->token, null]));
        $statuses = array_column($results, 0);
        sort($statuses);
        self::assertSame([204, 409], $statuses, implode(',', $statuses));

        self::assertStatus(409, $this->request('PUT', $path, $admin->token));
        self::assertStatus(204, $this->request('PUT', '/api/follows/target', $viewer));
        self::assertStatus(409, $this->request('DELETE', $path, $admin->token));
        self::assertSame('certified_public', $this->db->fetchValue("SELECT account_type FROM users WHERE id = 'target'"));

        $sessions->invalidate($admin->administratorId, $admin->tokenHash);
        self::assertStatus(401, $this->request('PUT', $path, $admin->token));
    }
}
