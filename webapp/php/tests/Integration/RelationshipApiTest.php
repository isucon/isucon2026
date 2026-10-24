<?php

declare(strict_types=1);

namespace Isureal\Tests\Integration;

use Psr\Http\Message\ResponseInterface;

final class RelationshipApiTest extends IntegrationTestCase
{
    /**
     * @var array<string, string>
     */
    private array $tokens = [];

    public function testRelationships(): void
    {
        foreach (['a', 'b', 'c'] as $id) {
            $this->insertUser($id, id: $id);
            $this->tokens[$id] = $this->login($id);
        }
        $this->db->execute("UPDATE users SET bio = 'bio'");

        $this->create('', 'b', 401);
        $this->create('a', 'a', 400);
        $this->create('a', 'missing', 404);
        $first = $this->create('a', 'b', 201);
        self::assertSame('pending', $first['status']);
        $incoming = $this->list('b', 'incoming-requests');
        self::assertCount(1, $incoming['items']);
        self::assertSame('a', $incoming['items'][0]['user']['id']);
        // 申請の一覧では自己紹介を返さない
        self::assertSame('', $incoming['items'][0]['user']['bio']);
        self::assertCount(1, $this->list('a', 'outgoing-requests')['items'], 'missing outgoing request');
        $this->create('a', 'b', 409);

        $path = "/api/friend-requests/{$first['relationId']}";
        $this->apply('a', 'POST', "{$path}/accept", 403);
        $this->apply('c', 'POST', "{$path}/reject", 403);
        $this->apply('b', 'POST', "{$path}/cancel", 403);
        $this->apply('b', 'POST', "{$path}/reject", 200);
        $this->apply('b', 'POST', "{$path}/accept", 403);
        $first = $this->create('a', 'b', 201);
        $this->apply('a', 'POST', "/api/friend-requests/{$first['relationId']}/cancel", 200);
        $first = $this->create('a', 'b', 201);
        $path = "/api/friend-requests/{$first['relationId']}";

        // 友達を作れなければ申請は消費されない
        $this->db->executeScript(<<<'SQL'
            CREATE FUNCTION reject_friendship() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject friendship'; END $$;
            CREATE TRIGGER reject_friendship BEFORE INSERT ON friendships FOR EACH ROW EXECUTE FUNCTION reject_friendship();
            SQL);
        try {
            $this->apply('b', 'POST', "{$path}/accept", 500);
        } finally {
            $this->db->executeScript('DROP FUNCTION reject_friendship() CASCADE');
        }
        self::assertCount(1, $this->list('b', 'incoming-requests')['items'], 'failed approval consumed request');
        $this->apply('b', 'POST', "{$path}/accept", 200);
        $friends = $this->list('a', 'friends');
        self::assertCount(1, $friends['items']);
        self::assertSame('b', $friends['items'][0]['user']['id']);
        self::assertSame('bio', $friends['items'][0]['user']['bio']);
        self::assertCount(0, $this->list('b', 'incoming-requests')['items'], 'accepted request still pending');
        $this->create('a', 'b', 409);
        $this->apply('a', 'DELETE', '/api/friendships/b', 204);
        $this->apply('a', 'DELETE', '/api/friendships/b', 409);

        // 互いに同時に申請すると、片方が承認になる
        $results = self::concurrently([
            ['POST', '/api/friend-requests', $this->tokens['a'], ['recipientId' => 'b']],
            ['POST', '/api/friend-requests', $this->tokens['b'], ['recipientId' => 'a']],
        ]);
        $statuses = [];
        foreach ($results as [$status, $body]) {
            self::assertSame(201, $status, $body);
            $statuses[] = json_decode($body, true, flags: JSON_THROW_ON_ERROR)['status'];
        }
        sort($statuses);
        self::assertSame(['accepted', 'pending'], $statuses, 'reverse requests');
        self::assertCount(1, $this->list('a', 'friends')['items']);

        // フォローは認定ユーザーだけ
        $this->apply('a', 'PUT', '/api/follows/b', 409);
        $this->db->execute("UPDATE users SET account_type = 'certified_public' WHERE id IN ('a', 'b')");
        $this->apply('a', 'PUT', '/api/follows/b', 204);
        $this->apply('a', 'PUT', '/api/follows/b', 409);
        $this->apply('b', 'PUT', '/api/follows/a', 204);
        self::assertCount(1, $this->list('a', 'following')['items'], 'missing follow');
        self::assertCount(1, $this->list('b', 'followers')['items'], 'missing follow');
        $this->apply('a', 'DELETE', '/api/follows/b', 204);
        $this->apply('a', 'DELETE', '/api/follows/b', 409);
        $this->apply('a', 'PUT', '/api/follows/b', 204);

        // ブロックすると友達とフォローがなくなる
        $this->apply('a', 'PUT', '/api/blocks/b', 204);
        self::assertCount(0, $this->list('a', 'friends')['items'], 'block did not remove relationships');
        self::assertCount(0, $this->list('a', 'following')['items'], 'block did not remove relationships');
        self::assertCount(0, $this->list('b', 'following')['items'], 'block did not remove relationships');
        $this->create('b', 'a', 409);
        $this->apply('b', 'PUT', '/api/follows/a', 409);
        $this->apply('a', 'PUT', '/api/blocks/b', 409);
        $this->apply('b', 'DELETE', '/api/blocks/a', 403);
        $this->apply('a', 'DELETE', '/api/blocks/b', 204);
        self::assertCount(0, $this->list('a', 'friends')['items'], 'unblock restored friendship');

        // ブロックすると申請は取り消される
        $first = $this->create('a', 'b', 201);
        $this->apply('b', 'PUT', '/api/blocks/a', 204);
        self::assertSame('cancelled', $this->db->fetchValue('SELECT status FROM friend_requests WHERE id = :id', ['id' => $first['relationId']]), 'block did not cancel request');
        $this->apply('b', 'POST', "/api/friend-requests/{$first['relationId']}/accept", 403);
        $this->apply('b', 'DELETE', '/api/blocks/a', 204);

        // 承認とブロックが同時でも、友達は残らない
        $first = $this->create('a', 'b', 201);
        $results = self::concurrently([
            ['POST', "/api/friend-requests/{$first['relationId']}/accept", $this->tokens['b'], null],
            ['PUT', '/api/blocks/b', $this->tokens['a'], null],
        ]);
        foreach ($results as [$status, $body]) {
            self::assertContains($status, [200, 204, 403], "race response={$status} {$body}");
        }
        self::assertCount(0, $this->list('a', 'friends')['items'], 'friendship survived block race');

        // ページ送り
        for ($i = 0; $i < 52; $i++) {
            $id = sprintf('friend-%02d', $i);
            $this->insertUser($id, id: $id);
            $this->db->execute(
                "INSERT INTO friendships (id, user_low_id, user_high_id, updated_at) VALUES (gen_random_uuid()::text, 'c', :id, '2026-09-23T00:00:00Z')",
                ['id' => $id],
            );
        }
        $page = $this->list('c', 'friends');
        self::assertCount(50, $page['items'], 'invalid first page');
        self::assertTrue($page['hasMore'], 'invalid first page');
        $last = $page['items'][49];
        $next = $this->list('c', 'friends?' . http_build_query(['beforeUpdatedAt' => $last['updatedAt'], 'beforeId' => $last['relationId']]));
        self::assertCount(2, $next['items'], 'invalid second page');
        self::assertFalse($next['hasMore'], 'invalid second page');
        $relationIds = array_column([...$page['items'], ...$next['items']], 'relationId');
        self::assertSame($relationIds, array_values(array_unique($relationIds)), 'duplicate relation');

        $this->apply('a', 'GET', '/api/relationships/unknown', 400);
        $this->apply('a', 'GET', '/api/relationships/friends?beforeId=x', 400);
    }

    /**
     * @param array<string, mixed>|null $json
     */
    private function send(string $actor, string $method, string $path, ?array $json = null): ResponseInterface
    {
        $token = $this->tokens[$actor] ?? '';
        return $this->request($method, $path, json: $json, headers: ['Content-Type' => 'application/json', 'Authorization' => "Bearer {$token}"]);
    }

    private function apply(string $actor, string $method, string $path, int $status): void
    {
        self::assertStatus($status, $this->send($actor, $method, $path), "{$actor} {$method} {$path}");
    }

    /**
     * @return array<mixed>
     */
    private function create(string $actor, string $target, int $status): array
    {
        $response = $this->send($actor, 'POST', '/api/friend-requests', ['recipientId' => $target]);
        self::assertStatus($status, $response, "{$actor} -> {$target}");
        return $status === 201 ? self::json($response) : [];
    }

    /**
     * @return array<mixed>
     */
    private function list(string $actor, string $kind): array
    {
        $response = $this->send($actor, 'GET', "/api/relationships/{$kind}");
        self::assertStatus(200, $response, $kind);
        $body = self::json($response);
        self::assertIsArray($body['items'] ?? null, 'null items');
        return $body;
    }
}
