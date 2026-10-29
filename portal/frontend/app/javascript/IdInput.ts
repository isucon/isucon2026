// Resource IDs are positive signed int64 values in the API.
export function parseResourceId(value: string | null | undefined): bigint | null {
  if (!value || !/^[0-9]+$/.test(value)) return null;
  const id = BigInt(value);
  return id > 0n && id <= 9223372036854775807n ? id : null;
}

export function requireResourceId(value: string | null | undefined): bigint {
  const id = parseResourceId(value);
  if (id === null) throw new Error("ID は正の整数（int64 の範囲）で指定してください。");
  return id;
}

export function parseTeamIds(value: string): bigint[] | null {
  if (value.trim() === "") return [];
  const ids = value.split(",").map((id) => parseResourceId(id.trim()));
  return ids.every((id): id is bigint => id !== null) ? ids : null;
}
