import { createHash, createHmac } from "node:crypto";
import { HTTPException } from "hono/http-exception";
import type { ContentfulStatusCode } from "hono/utils/http-status";
import { caseFold } from "unicode-case-folding";

// The error code becomes the `{ "error": ... }` body in server.ts's onError.
export function fail(status: ContentfulStatusCode, code: string): never {
  throw new HTTPException(status, { message: code });
}
export const hash = (value: string) =>
  createHash("sha256").update(value).digest("hex");
export const usernamePattern = /^[a-z0-9_.-]{1,64}$/;
export const validRegion = (region: string) =>
  ["america", "europe", "asia"].includes(region);
export function validUserText(value: string, min: number, max: number) {
  const length = [...value].length;
  return value.isWellFormed() && length >= min && length <= max;
}
export const normalizeSearchText = (value: string) =>
  caseFold(value.normalize("NFC")).normalize("NFC");
export interface Location {
  kind: string;
  latitude?: number | null;
  longitude?: number | null;
  locality?: string | null;
}
export function validLocation(l: Location) {
  switch (l.kind) {
    case "none":
      return l.latitude == null && l.longitude == null && l.locality == null;
    case "exact":
      return (
        l.latitude != null &&
        l.longitude != null &&
        l.locality == null &&
        l.latitude >= -90 &&
        l.latitude <= 90 &&
        l.longitude >= -180 &&
        l.longitude <= 180
      );
    case "approximate":
      return (
        l.latitude == null &&
        l.longitude == null &&
        l.locality != null &&
        validUserText(l.locality, 1, 100)
      );
    default:
      return false;
  }
}
export function imageSigningKey(secret: string) {
  if (!/^[0-9a-fA-F]{64}$/.test(secret))
    throw new Error("invalid image signing key");
  return Buffer.from(secret, "hex");
}
export function imageSignature(key: Buffer, id: string, exp: bigint) {
  let value: Buffer = Buffer.from(`${exp}:${id}`);
  for (let i = 0; i < 1000; i++)
    value = createHmac("sha256", key).update(value).digest();
  return value;
}
export function signedImageURL(user: User, id: string) {
  const exp = BigInt(Math.floor(Date.now() / 1000) + 86400);
  return `/api/images/${encodeURIComponent(id)}?exp=${exp}&sig=${imageSignature(user.imageKey!, id, exp).toString("base64url")}`;
}
// pg's default Date parser drops microseconds, which breaks tuple pagination.
export function timestamp(value: string) {
  const normalized = value.replace(" ", "T").replace(/([+-]\d\d)$/, "$1:00");
  const fraction = normalized.match(/\.(\d+)/)?.[1] ?? "";
  const seconds = new Date(normalized).toISOString().slice(0, 19);
  const trimmed = fraction.replace(/0+$/, "");
  return seconds + (trimmed ? "." + trimmed : "") + "Z";
}
export function micros(value: string) {
  const fraction = value.match(/\.(\d+)/)?.[1] ?? "";
  return (
    BigInt(Math.floor(new Date(value).getTime() / 1000)) * 1000000n +
    BigInt(fraction.padEnd(6, "0").slice(0, 6))
  );
}
export function addSeconds(value: string, seconds: number) {
  const result = micros(value) + BigInt(seconds) * 1000000n;
  const whole =
    result >= 0n ? result / 1000000n : (result - 999999n) / 1000000n;
  const frac = (result - whole * 1000000n)
    .toString()
    .padStart(6, "0")
    .replace(/0+$/, "");
  return (
    new Date(Number(whole) * 1000).toISOString().slice(0, 19) +
    (frac ? "." + frac : "") +
    "Z"
  );
}
export function validDate(value: string, month = false) {
  if (!(month ? /^\d{4}-\d{2}$/ : /^\d{4}-\d{2}-\d{2}$/).test(value))
    return false;
  const date = month ? value + "-01" : value;
  const parsed = new Date(date + "T00:00:00Z");
  return (
    !Number.isNaN(parsed.getTime()) &&
    parsed.toISOString().slice(0, 10) === date
  );
}
export interface User {
  id: string;
  username: string;
  secret_key_hash: string;
  display_name: string;
  bio: string;
  profile_image_id: string | null;
  region: string;
  account_type: string;
  status: string;
  imageKey?: Buffer;
}
export interface Post {
  id: string;
  author_id: string;
  posting_window_id: string;
  image_id: string;
  caption: string;
  visibility: string;
  location_kind: string;
  latitude: number | null;
  longitude: number | null;
  locality: string | null;
  posted_at: string;
  on_time_deadline: string;
  window_ended_at: string | null;
  author: User;
}
export interface Viewer {
  id: string;
  hasPostedToday: boolean;
  friends: Set<string>;
  friendsOfFriends: Set<string>;
  blocked: Set<string>;
}
export function canView(
  v: Viewer,
  p: Pick<Post, "author_id" | "window_ended_at" | "visibility">,
) {
  if (p.author_id === v.id) return true;
  if (
    p.window_ended_at != null ||
    !v.hasPostedToday ||
    v.blocked.has(p.author_id)
  )
    return false;
  switch (p.visibility) {
    case "public":
      return true;
    case "friends":
      return v.friends.has(p.author_id);
    case "friends_of_friends":
      return v.friends.has(p.author_id) || v.friendsOfFriends.has(p.author_id);
    default:
      return false;
  }
}
export const sessionUserResponse = (u: User) => ({
  id: u.id,
  username: u.username,
  displayName: u.display_name,
  bio: u.bio,
  region: u.region,
  accountType: u.account_type,
  status: u.status,
});
export const getMeResponse = (u: User) => ({
  ...sessionUserResponse(u),
  profileImageUrl:
    u.profile_image_id == null ? null : signedImageURL(u, u.profile_image_id),
});
export const relationshipUserResponse = (u: User) => ({
  id: u.id,
  username: u.username,
  displayName: u.display_name,
  profileImageUrl: getMeResponse(u).profileImageUrl,
  bio: u.bio,
  accountType: u.account_type,
});
export function postResponse(p: Post, u: User) {
  const location: Location = { kind: p.location_kind };
  if (p.latitude != null) location.latitude = p.latitude;
  if (p.longitude != null) location.longitude = p.longitude;
  if (p.locality != null) location.locality = p.locality;
  const late = micros(p.posted_at) - micros(p.on_time_deadline);
  return {
    id: p.id,
    author: relationshipUserResponse(u),
    postingWindowId: p.posting_window_id,
    imageUrl: signedImageURL(u, p.image_id),
    caption: p.caption,
    visibility: p.visibility,
    location,
    postedAt: p.posted_at,
    onTime: late <= 0n,
    lateSeconds: late > 0n ? Number(late / 1000000n) : 0,
  };
}
export const page = <T>(items: T[]) => ({
  items: items.slice(0, 50),
  hasMore: items.length > 50,
});
