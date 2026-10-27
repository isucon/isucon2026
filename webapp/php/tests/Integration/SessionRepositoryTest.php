<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Ids;
use Isureal\Repository\SessionRepository;
use Isureal\Repository\UserRepository;

final class SessionRepositoryTest extends IntegrationTestCase
{
    public function testCreateLoginSession(): void
    {
        $this->insertUser('alice', id: 'user1');
        $sessions = new SessionRepository($this->db, new UserRepository($this->db));
        $first = $sessions->createInTransaction('user1');
        self::assertNotSame('', $first->token);
        self::assertSame(Ids::sha256($first->token), $first->tokenHash);
        self::assertSame('user1', $first->userId);
        self::assertGreaterThan(0, $first->createdAt->getTimestamp());

        // 同時にログインしても、有効なセッションは 1 つだけになる
        $request = ['POST', '/api/auth/login', null, ['username' => 'alice', 'secretKey' => self::SECRET_KEY]];
        $statuses = array_column(self::concurrently(array_fill(0, 8, $request)), 0);
        self::assertSame(array_fill(0, 8, 200), $statuses);
        self::assertSame(1, $this->db->fetchValue('SELECT count(*) FROM login_sessions WHERE active'));
        self::assertSame(9, $this->db->fetchValue('SELECT count(*) FROM login_sessions'));
        // fetchValue は false を null にするので fetchOne で読む
        self::assertFalse($this->db->fetchOne('SELECT active FROM login_sessions WHERE token_hash = :hash', ['hash' => $first->tokenHash])['active'] ?? null, 'old session is still active');

        try {
            $sessions->createInTransaction('missing');
            self::fail('missing user accepted');
        } catch (\Throwable $e) {
            self::assertNotInstanceOf(\PHPUnit\Framework\AssertionFailedError::class, $e);
        }

        $currentHash = $this->db->fetchValue('SELECT token_hash FROM login_sessions WHERE active');
        $this->db->executeScript(<<<'SQL'
            CREATE OR REPLACE FUNCTION reject_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'insertion rejected'; END $$;
            CREATE TRIGGER reject_session BEFORE INSERT ON login_sessions FOR EACH ROW EXECUTE FUNCTION reject_session();
            SQL);
        try {
            try {
                $sessions->createInTransaction('user1');
                self::fail('expected insertion failure');
            } catch (\PDOException) {
                // 失敗する
            }
            self::assertSame($currentHash, $this->db->fetchValue('SELECT token_hash FROM login_sessions WHERE active'), 'failed creation changed active session');
        } finally {
            // スキーマを作り直しても関数は残るので、次回のテストのために消す
            $this->db->executeScript('DROP FUNCTION reject_session() CASCADE');
        }
    }
}
