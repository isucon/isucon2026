import { randomUUID } from "node:crypto";
import type pg from "pg";
import { type C, body, string, cursor } from "./http.js";
import { fail, relationshipUserResponse, page } from "./core.js";
import {
  type DB,
  exec,
  one,
  rows,
  scalar,
  transaction,
  notFound,
  NoRows,
} from "./db.js";
import { getUser, getFriendIDs, userColumnsAs, joinedUser } from "./models.js";
export const isBlocked = (db: DB, a: string, b: string) =>
  scalar<boolean>(
    db,
    "SELECT EXISTS(SELECT 1 FROM block_relations WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1))",
    a,
    b,
  );
export async function relationshipTx<T>(
  db: pg.Pool,
  actor: string,
  target: string,
  fn: (tx: pg.PoolClient) => Promise<T>,
) {
  if (actor === target || target === "") fail(400, "invalid_relationship");
  return transaction(db, async (tx) => {
    for (const id of [actor, target].sort())
      await notFound(
        () =>
          one(
            tx,
            "SELECT id FROM users WHERE id=$1 AND status='active' FOR NO KEY UPDATE",
            id,
          ),
        404,
        "relationship_not_found",
      );
    return fn(tx);
  });
}
interface FriendRequest {
  id: string;
  requester_id: string;
  recipient_id: string;
  status: string;
  created_at: string;
  updated_at: string;
}
const getFriendRequest = (db: DB, id: string) =>
  one<FriendRequest>(
    db,
    "SELECT id, requester_id, recipient_id, status, created_at, updated_at FROM friend_requests WHERE id=$1",
    id,
  );
const insertFriendship = (db: DB, r: FriendRequest) =>
  exec(
    db,
    "INSERT INTO friendships (id, user_low_id, user_high_id, source_request_id) VALUES ($1,$2,$3,$4)",
    randomUUID(),
    ...[r.requester_id, r.recipient_id].sort(),
    r.id,
  );
const response = (r: FriendRequest) => ({
  relationId: r.id,
  status: r.status,
  updatedAt: r.updated_at,
});
export function relationshipHandlers(db: pg.Pool) {
  return {
    createRequest: async (c: C) => {
      const b = await body(c, { recipientId: "string" }),
        actor = c.var.session.user_id,
        target = string(b, "recipientId");
      const result = await relationshipTx(db, actor, target, async (tx) => {
        if (await isBlocked(tx, actor, target))
          fail(409, "relationship_conflict");
        if ((await getFriendIDs(tx, actor)).includes(target))
          fail(409, "relationship_conflict");
        let existing: string | undefined;
        try {
          existing = await scalar(
            tx,
            "SELECT id FROM friend_requests WHERE status='pending' AND ((requester_id=$1 AND recipient_id=$2) OR (requester_id=$2 AND recipient_id=$1))",
            actor,
            target,
          );
        } catch (e) {
          if (!(e instanceof NoRows)) throw e;
        }
        let id: string;
        if (existing) {
          const r = await getFriendRequest(tx, existing);
          if (r.requester_id === actor) fail(409, "relationship_conflict");
          await exec(
            tx,
            "UPDATE friend_requests SET status='accepted', updated_at=clock_timestamp() WHERE id=$1",
            r.id,
          );
          await insertFriendship(tx, r);
          id = r.id;
        } else {
          id = randomUUID();
          await exec(
            tx,
            "INSERT INTO friend_requests (id, requester_id, recipient_id, status, created_at, updated_at) VALUES ($1,$2,$3,'pending',clock_timestamp(),clock_timestamp())",
            id,
            actor,
            target,
          );
        }
        return getFriendRequest(tx, id);
      });
      return c.json(response(result), 201);
    },
    transitionRequest: async (c: C) => {
      const action = c.req.param("action")!,
        status = (
          {
            accept: "accepted",
            reject: "rejected",
            cancel: "cancelled",
          } as Record<string, string>
        )[action];
      if (!status) fail(403, "relationship_action_forbidden");
      const actor = c.var.session.user_id,
        id = c.req.param("requestId")!;
      const initial = await notFound(
        () => getFriendRequest(db, id),
        404,
        "relationship_not_found",
      );
      if (
        (status === "cancelled" && actor !== initial.requester_id) ||
        (status !== "cancelled" && actor !== initial.recipient_id)
      )
        fail(403, "relationship_action_forbidden");
      const result = await relationshipTx(
        db,
        initial.requester_id,
        initial.recipient_id,
        async (tx) => {
          const r = await getFriendRequest(tx, id);
          if (r.status !== "pending")
            fail(403, "relationship_action_forbidden");
          if (status === "accepted") {
            if (await isBlocked(tx, r.requester_id, r.recipient_id))
              fail(409, "relationship_conflict");
            await insertFriendship(tx, r);
          }
          await exec(
            tx,
            "UPDATE friend_requests SET status=$2, updated_at=clock_timestamp() WHERE id=$1",
            r.id,
            status,
          );
          return getFriendRequest(tx, r.id);
        },
      );
      return c.json(response(result));
    },
    change: async (c: C) => {
      const actor = c.var.session.user_id,
        target = c.req.param("userId")!,
        kind = c.req.path.split("/")[2],
        put = c.req.method === "PUT";
      await relationshipTx(db, actor, target, async (tx) => {
        const sorted = [actor, target].sort();
        if (kind === "friendships") {
          if (
            !(await exec(
              tx,
              "DELETE FROM friendships WHERE user_low_id=$1 AND user_high_id=$2",
              ...sorted,
            ))
          )
            fail(409, "relationship_conflict");
        } else if (kind === "follows" && put) {
          const user = await getUser(tx, target);
          if (user.account_type !== "certified_public")
            fail(409, "relationship_conflict");
          if (await isBlocked(tx, actor, user.id))
            fail(409, "relationship_conflict");
          if (
            await scalar(
              tx,
              "SELECT EXISTS(SELECT 1 FROM follows WHERE follower_id=$1 AND followee_id=$2)",
              actor,
              user.id,
            )
          )
            fail(409, "relationship_conflict");
          await exec(
            tx,
            "INSERT INTO follows (id, follower_id, followee_id) VALUES ($1,$2,$3)",
            randomUUID(),
            actor,
            user.id,
          );
        } else if (kind === "follows") {
          if (
            !(await exec(
              tx,
              "DELETE FROM follows WHERE follower_id=$1 AND followee_id=$2",
              actor,
              target,
            ))
          )
            fail(409, "relationship_conflict");
        } else if (put) {
          if (
            await scalar(
              tx,
              "SELECT EXISTS(SELECT 1 FROM block_relations WHERE blocker_id=$1 AND blocked_id=$2)",
              actor,
              target,
            )
          )
            fail(409, "relationship_conflict");
          await exec(
            tx,
            "INSERT INTO block_relations (id, blocker_id, blocked_id) VALUES ($1,$2,$3)",
            randomUUID(),
            actor,
            target,
          );
          await exec(
            tx,
            "DELETE FROM friendships WHERE user_low_id=$1 AND user_high_id=$2",
            ...sorted,
          );
          await exec(
            tx,
            "DELETE FROM follows WHERE (follower_id=$1 AND followee_id=$2) OR (follower_id=$2 AND followee_id=$1)",
            actor,
            target,
          );
          await exec(
            tx,
            "UPDATE friend_requests SET status='cancelled', updated_at=clock_timestamp() WHERE status='pending' AND ((requester_id=$1 AND recipient_id=$2) OR (requester_id=$2 AND recipient_id=$1))",
            actor,
            target,
          );
        } else if (
          !(await exec(
            tx,
            "DELETE FROM block_relations WHERE blocker_id=$1 AND blocked_id=$2",
            actor,
            target,
          ))
        )
          fail(403, "relationship_action_forbidden");
      });
      return c.body(null, 204);
    },
    list: async (c: C) => {
      const [at, id] = cursor(c, "beforeUpdatedAt", "beforeId"),
        kind = c.req.param("kind")!;
      const queries: Record<string, string> = {
        friends:
          "SELECT id, user_low_id AS first_id, user_high_id AS second_id, updated_at FROM friendships WHERE user_low_id=$1 OR user_high_id=$1",
        following:
          "SELECT id, follower_id AS first_id, followee_id AS second_id, updated_at FROM follows WHERE follower_id=$1",
        followers:
          "SELECT id, follower_id AS first_id, followee_id AS second_id, updated_at FROM follows WHERE followee_id=$1",
        "incoming-requests":
          "SELECT id, requester_id AS first_id, recipient_id AS second_id, updated_at FROM friend_requests WHERE recipient_id=$1 AND status='pending'",
        "outgoing-requests":
          "SELECT id, requester_id AS first_id, recipient_id AS second_id, updated_at FROM friend_requests WHERE requester_id=$1 AND status='pending'",
      };
      const query = queries[kind];
      if (!query) fail(400, "invalid_relationship");
      const records = await rows(
        db,
        `SELECT relations.id, relations.updated_at, ${userColumnsAs("u", "user")} FROM (${query}) relations JOIN users u ON u.status = 'active' AND u.id = CASE WHEN relations.first_id = $1 THEN relations.second_id ELSE relations.first_id END WHERE ($2::timestamptz IS NULL OR (relations.updated_at, relations.id) < ($2, $3)) ORDER BY relations.updated_at DESC, relations.id DESC LIMIT 51`,
        c.var.session.user_id,
        at,
        id,
      );
      const items = records.map((r) => {
        const user = joinedUser(r, "user");
        if (user.account_type !== "certified_public" && kind !== "friends")
          user.bio = "";
        return {
          relationId: r.id,
          user: relationshipUserResponse(user),
          status: kind.endsWith("-requests") ? "pending" : kind,
          updatedAt: r.updated_at,
        };
      });
      return c.json(page(items));
    },
  };
}
