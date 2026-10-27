import { fail, validTimestamp } from "./core.js";
import type { C } from "./types/hono.js";
type FieldType = "string" | "number";
type JSONBody<S extends Record<string, FieldType>> = {
  [K in keyof S]?: S[K] extends "string" ? string : number;
};
export async function readJSON<S extends Record<string, FieldType>>(
  c: C,
  fields: S,
): Promise<JSONBody<S>> {
  const text = await c.req.text();
  if (!text) return {};
  if (!c.req.header("Content-Type")?.startsWith("application/json"))
    fail(415, "Unsupported Media Type");
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    fail(400, "invalid JSON");
  }
  if (value === null) return {};
  if (typeof value !== "object" || Array.isArray(value))
    fail(400, "invalid JSON");
  const data = value as Record<string, unknown>,
    body: Record<string, unknown> = {};
  for (const [key, type] of Object.entries(fields)) {
    if (data[key] == null) continue;
    if (
      typeof data[key] !== type ||
      (type === "number" && !Number.isSafeInteger(data[key]))
    )
      fail(400, "invalid JSON field: " + key);
    body[key] = data[key];
  }
  return body as JSONBody<S>;
}
export function cursor(c: C, timeKey: string, idKey: string) {
  const at = c.req.query(timeKey) ?? null,
    id = c.req.query(idKey) ?? "";
  if (at !== null && !validTimestamp(at)) fail(400, "invalid timestamp");
  if ((at === null) !== (id === "")) fail(400, "invalid_pagination");
  return { at, id };
}
