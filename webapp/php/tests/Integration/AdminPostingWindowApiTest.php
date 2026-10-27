<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use DateTimeImmutable;
use Isureal\Repository\AdminSessionRepository;
use Psr\Http\Message\ResponseInterface;

final class AdminPostingWindowApiTest extends IntegrationTestCase
{
    public function testOpenPostingWindow(): void
    {
        $this->db->execute("INSERT INTO administrators (id, username, password_hash) VALUES ('admin1', 'admin', 'hash')");
        $this->insertUser('alice', id: 'a');
        $this->insertUser('bob', id: 'b');
        $this->insertUser('carol', 'europe', id: 'c');
        $this->insertUser('dave', id: 'd');
        $this->db->execute("UPDATE users SET status = 'deleted' WHERE id = 'd'");
        $session = new AdminSessionRepository($this->db)->create('admin1')->token;
        $userSession = $this->login('a');

        $body = '{"localDate":"2026-09-23","onTimeSeconds":5}';
        self::assertStatus(401, $this->open('asia', $body, 'unknown'));
        self::assertStatus(401, $this->open('asia', $body, $userSession));
        self::assertStatus(400, $this->open('unknown', $body, $session));
        self::assertStatus(400, $this->open('', $body, $session));
        foreach (['{', '{"localDate":"invalid"}'] as $invalid) {
            self::assertStatus(400, $this->open('asia', $invalid, $session), $invalid);
        }

        // 同時に開いても 1 つだけ作られる
        $request = ['POST', '/api/admin/posting-window', $session, ['region' => 'asia', 'localDate' => '2026-09-23', 'onTimeSeconds' => 5]];
        $results = self::concurrently(array_fill(0, 8, $request));
        $first = null;
        $successes = 0;
        foreach ($results as [$status, $responseBody]) {
            if ($status === 201) {
                $successes++;
                $first = json_decode($responseBody, true, flags: JSON_THROW_ON_ERROR);
            } else {
                self::assertSame(409, $status, $responseBody);
            }
        }
        self::assertSame(1, $successes);
        self::assertIsArray($first);
        self::assertSame('asia', $first['region']);
        self::assertSame(5.0, self::seconds($first['startsAt'], $first['onTimeDeadline']));
        self::assertSame(['a', 'b'], $this->db->fetchColumn('SELECT user_id FROM notifications WHERE posting_window_id = :id ORDER BY user_id', ['id' => $first['id']]));

        self::assertStatus(409, $this->open('asia', '{"localDate":"2026-09-22"}', $session));
        $response = $this->open('europe', '{"localDate":"2026-09-23"}', $session);
        self::assertStatus(201, $response);
        $europe = self::json($response);
        self::assertSame(86400.0, self::seconds($europe['startsAt'], $europe['onTimeDeadline']), 'invalid default deadline');

        $response = $this->open('asia', '{"localDate":"2026-09-24"}', $session);
        self::assertStatus(201, $response);
        $second = self::json($response);
        $endedAt = $this->db->fetchValue('SELECT ended_at FROM posting_windows WHERE id = :id', ['id' => $first['id']]);
        self::assertEquals(new DateTimeImmutable($second['startsAt']), new DateTimeImmutable((string) $endedAt), 'previous window ended at different time');

        $this->db->executeScript(<<<'SQL'
            CREATE FUNCTION reject_notification() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject notification'; END $$;
            CREATE TRIGGER reject_notification BEFORE INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION reject_notification();
            SQL);
        try {
            self::assertStatus(500, $this->open('asia', '{"localDate":"2026-09-25"}', $session));
            self::assertSame(
                [$second['id'], $europe['id']],
                $this->db->fetchColumn('SELECT id FROM posting_windows WHERE ended_at IS NULL ORDER BY region'),
                'active windows changed after failure',
            );
            self::assertSame(3, $this->db->fetchValue('SELECT count(*) FROM posting_windows'), 'failed window persisted');
        } finally {
            $this->db->executeScript('DROP FUNCTION reject_notification() CASCADE');
        }
    }

    /**
     * Go のテストと同じく、ボディの先頭に region を差し込んで送る
     */
    private function open(string $region, string $body, string $token): ResponseInterface
    {
        $body = (string) preg_replace('/\{/', '{"region":' . json_encode($region) . ',', $body, 1);
        return $this->requestRaw('POST', '/api/admin/posting-window', $token, $body, ['Content-Type' => 'application/json']);
    }

    private static function seconds(string $from, string $to): float
    {
        $diff = (float) new DateTimeImmutable($to)->format('U.u') - (float) new DateTimeImmutable($from)->format('U.u');
        return round($diff, 6);
    }
}
