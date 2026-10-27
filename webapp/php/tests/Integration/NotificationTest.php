<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Isureal\Ids;

final class NotificationTest extends IntegrationTestCase
{
    public function testNotifications(): void
    {
        [$userId, $otherId] = [Ids::generate(), Ids::generate()];
        $this->insertUser('alice', id: $userId);
        $this->insertUser('bob', id: $otherId);
        $token = $this->login($userId);
        [$windowId, $endedId] = [Ids::generate(), Ids::generate()];
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at)
             VALUES (:window, 'asia', '2026-09-23', clock_timestamp(), clock_timestamp(), NULL),
                    (:ended, 'asia', '2026-09-22', clock_timestamp(), clock_timestamp(), clock_timestamp())",
            ['window' => $windowId, 'ended' => $endedId],
        );
        $this->insertNotification($userId, $windowId, "clock_timestamp() - interval '1 minute'");

        $stream = $this->connect($token);
        // 古い通知、終わった投稿期間の通知、他のユーザーの通知は届かない
        $this->insertNotification($userId, $endedId);
        $this->insertNotification($otherId, $windowId);
        self::assertNull(self::readUntilClosed($stream, 0.7), 'delivered an old, ended or other user\'s notification');

        $this->db->execute('UPDATE posting_windows SET ended_at = clock_timestamp() WHERE id = :id', ['id' => $windowId]);
        $newWindowId = Ids::generate();
        $this->db->execute(
            "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline)
             VALUES (:id, 'asia', '2026-09-24', clock_timestamp(), clock_timestamp())",
            ['id' => $newWindowId],
        );
        $notificationId = $this->insertNotification($userId, $newWindowId);
        $data = self::readUntilClosed($stream, 5);
        self::assertNotNull($data, 'notification was not delivered');
        $notification = json_decode(trim((string) preg_replace('/\Adata: /', '', trim($data))), true, flags: JSON_THROW_ON_ERROR);
        self::assertSame($notificationId, $notification['id']);
        self::assertSame($newWindowId, $notification['postingWindowId']);

        // 届いた通知は、接続し直しても届かない
        $stream = $this->connect($token);
        self::assertNull(self::readUntilClosed($stream, 0.7), 'redelivered notification');
        fclose($stream);

        // リージョンが変わったら、何も送らずに閉じる
        $stream = $this->connect($token);
        $this->db->execute("UPDATE users SET region = 'europe' WHERE id = :id", ['id' => $userId]);
        self::assertSame('', self::readUntilClosed($stream, 5), 'notification delivered after region change');
    }

    private function insertNotification(string $userId, string $windowId, string $createdAt = 'clock_timestamp()'): string
    {
        $id = Ids::generate();
        $this->db->execute(
            "INSERT INTO notifications (id, user_id, posting_window_id, created_at) VALUES (:id, :user_id, :window_id, {$createdAt})",
            ['id' => $id, 'user_id' => $userId, 'window_id' => $windowId],
        );
        return $id;
    }

    /**
     * 通知の接続を開き、ヘッダーを確かめて、ボディを読める状態のソケットを返す
     *
     * @return resource
     */
    private function connect(string $token)
    {
        $address = substr(self::serverUrl(), strlen('http://'));
        $stream = stream_socket_client("tcp://{$address}", $errno, $error, 5)
            ?: throw new \RuntimeException("failed to connect: {$error}");
        // HTTP/1.0 にしてチャンク転送を避ける
        fwrite($stream, "GET /api/notifications HTTP/1.0\r\nHost: {$address}\r\nAuthorization: Bearer {$token}\r\n\r\n");
        $head = '';
        $deadline = microtime(true) + 5;
        while (!str_contains($head, "\r\n\r\n") && microtime(true) < $deadline) {
            stream_set_timeout($stream, 1);
            $chunk = fread($stream, 1);
            if ($chunk === false || ($chunk === '' && feof($stream))) {
                break;
            }
            $head .= $chunk;
        }
        [$statusLine] = explode("\r\n", $head, 2);
        self::assertMatchesRegularExpression('#\AHTTP/1\.[01] 200 #', $statusLine, $head);
        self::assertMatchesRegularExpression('#\r\nContent-Type: text/event-stream\r\n#i', $head, $head);
        return $stream;
    }

    /**
     * 接続が閉じるまでボディを読む。$timeout 秒以内に閉じなければ null。
     * 待つ間に送られるコメント行（: ping）は除く
     *
     * @param resource $stream
     */
    private static function readUntilClosed($stream, float $timeout): ?string
    {
        $data = '';
        $deadline = microtime(true) + $timeout;
        while (($remaining = $deadline - microtime(true)) > 0) {
            $read = [$stream];
            $write = $except = null;
            $seconds = (int) $remaining;
            if (stream_select($read, $write, $except, $seconds, (int) (($remaining - $seconds) * 1_000_000)) === 0) {
                break;
            }
            $chunk = fread($stream, 8192);
            if ($chunk === false || ($chunk === '' && feof($stream))) {
                fclose($stream);
                return (string) preg_replace('/^:[^\n]*\n\n/m', '', $data);
            }
            $data .= $chunk;
        }
        return null;
    }
}
