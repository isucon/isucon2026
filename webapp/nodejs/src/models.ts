import { type DB, rows, first, one, scalar } from "./db.js";
import { type User, type Post, imageSigningKey, canView } from "./core.js";
export const userColumns = [
  "id",
  "username",
  "secret_key_hash",
  "display_name",
  "bio",
  "profile_image_id",
  "region",
  "account_type",
  "status",
];
export const userColumnsAs = (alias: string, prefix: string) =>
  userColumns.map((c) => `${alias}.${c} AS "${prefix}.${c}"`).join(", ");
export function joinedUser(row: Record<string, unknown>, prefix: string): User {
  const user = Object.fromEntries(
    userColumns.map((c) => [c, row[`${prefix}.${c}`]]),
  ) as unknown as User;
  user.imageKey = imageSigningKey(user.secret_key_hash);
  return user;
}
export async function getUser(db: DB, id: string) {
  const user = await first<User>(
    db,
    `SELECT ${userColumns.join(", ")} FROM users WHERE id = $1 AND status = 'active'`,
    id,
  );
  if (user) user.imageKey = imageSigningKey(user.secret_key_hash);
  return user;
}
export const postColumns = `p.id, p.author_id, p.posting_window_id, p.image_id, p.caption, p.visibility,
 p.location_kind, p.latitude, p.longitude, p.locality, p.posted_at,
 w.on_time_deadline, w.ended_at AS window_ended_at`;
export const getPost = (db: DB, id: string) =>
  first<Post>(
    db,
    `SELECT ${postColumns} FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id WHERE p.id = $1 AND p.deleted_at IS NULL`,
    id,
  );
export async function selectPostsWithAuthor(
  db: DB,
  conditions: string,
  ...args: unknown[]
) {
  const posts = await rows<Post>(
    db,
    `SELECT ${postColumns}, ${userColumnsAs("u", "author")} FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id JOIN users u ON u.id = p.author_id AND u.status = 'active' WHERE p.deleted_at IS NULL ${conditions}`,
    ...args,
  );
  for (const p of posts)
    p.author = joinedUser(p as unknown as Record<string, unknown>, "author");
  return posts;
}
export async function getPostWithAuthor(db: DB, id: string) {
  const posts = await selectPostsWithAuthor(db, "AND p.id = $1", id);
  return posts[0] as Post | undefined;
}
export async function getFriendIDs(db: DB, id: string) {
  const friendships = await rows<{ user_low_id: string; user_high_id: string }>(
    db,
    "SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id = $1 OR user_high_id = $1",
    id,
  );
  return friendships.map((f) =>
    f.user_low_id === id ? f.user_high_id : f.user_low_id,
  );
}
export async function getBlockedUserIDs(db: DB, id: string) {
  const records = await rows(
    db,
    "SELECT CASE WHEN blocker_id = $1 THEN blocked_id ELSE blocker_id END FROM block_relations WHERE blocker_id = $1 OR blocked_id = $1",
    id,
  );
  return new Set(records.map((r) => Object.values(r)[0] as string));
}
export const placeholders = (ids: unknown[], offset = 0) =>
  ids.map((_, i) => `$${i + offset + 1}`).join(", ");
export async function loadViewerContext(db: DB, id: string) {
  const user = await getUser(db, id);
  if (!user) return undefined;
  const hasPostedToday = await scalar<boolean>(
    db,
    `SELECT EXISTS(SELECT 1 FROM posts p JOIN posting_windows posted ON posted.id = p.posting_window_id JOIN posting_windows current ON current.region = $2 AND current.ended_at IS NULL AND current.local_date = posted.local_date WHERE p.author_id = $1 AND p.deleted_at IS NULL)`,
    user.id,
    user.region,
  );
  const blocked = await getBlockedUserIDs(db, user.id);
  const ids = await getFriendIDs(db, user.id);
  const friends = new Set(ids),
    friendsOfFriends = new Set<string>();
  if (ids.length) {
    const friendships = await rows<{
      user_low_id: string;
      user_high_id: string;
    }>(
      db,
      `SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id IN (${placeholders(ids)}) OR user_high_id IN (${placeholders(ids, ids.length)})`,
      ...ids,
      ...ids,
    );
    for (const f of friendships) {
      if (friends.has(f.user_low_id)) friendsOfFriends.add(f.user_high_id);
      if (friends.has(f.user_high_id)) friendsOfFriends.add(f.user_low_id);
    }
  }
  return { ...user, hasPostedToday, blocked, friends, friendsOfFriends };
}
export async function canViewPost(db: DB, viewerID: string, postID: string) {
  const post = await getPostWithAuthor(db, postID);
  if (!post) return false;
  const viewer = await loadViewerContext(db, viewerID);
  return viewer !== undefined && canView(viewer, post);
}
export async function canComment(db: DB, viewerID: string, postID: string) {
  const post = await getPostWithAuthor(db, postID);
  if (!post) return false;
  const viewer = await loadViewerContext(db, viewerID);
  if (!viewer || !canView(viewer, post) || post.window_ended_at != null)
    return false;
  if (viewer.id === post.author_id) return viewer.hasPostedToday;
  if (post.visibility === "public") return true;
  return viewer.friends.has(post.author_id);
}
export interface PostingWindow {
  id: string;
  region: string;
  local_date: string;
  starts_at: string;
  on_time_deadline: string;
  ended_at: string | null;
}
export const getPostingWindow = (db: DB, id: string) =>
  one<PostingWindow>(
    db,
    "SELECT id, region, local_date, starts_at, on_time_deadline, ended_at FROM posting_windows WHERE id = $1",
    id,
  );
export const windowResponse = (w: PostingWindow) => ({
  id: w.id,
  region: w.region,
  localDate: w.local_date,
  startsAt: w.starts_at,
  onTimeDeadline: w.on_time_deadline,
});
