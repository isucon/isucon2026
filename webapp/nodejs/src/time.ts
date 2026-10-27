export class Timestamp {
  readonly micros: number;

  constructor(micros: number) {
    this.micros = micros;
  }

  static now(): Timestamp {
    return new Timestamp(Date.now() * 1000);
  }

  unix(): number {
    return Math.floor(this.micros / 1_000_000);
  }

  addSeconds(seconds: number): Timestamp {
    return new Timestamp(this.micros + seconds * 1_000_000);
  }

  before(other: Timestamp): boolean {
    return this.micros < other.micros;
  }

  after(other: Timestamp): boolean {
    return this.micros > other.micros;
  }

  toString(): string {
    const seconds = Math.floor(this.micros / 1_000_000);
    const fraction = this.micros - seconds * 1_000_000;
    let text = new Date(seconds * 1000).toISOString().slice(0, 19);
    if (fraction !== 0) {
      text += "." + String(fraction).padStart(6, "0").replace(/0+$/, "");
    }
    return text + "Z";
  }

  toJSON(): string {
    return this.toString();
  }

  toPostgres(): string {
    return this.toString();
  }
}

const postgresTimestampPattern = /^(\d{4})-(\d\d)-(\d\d) (\d\d):(\d\d):(\d\d)(?:\.(\d{1,6}))?([+-])(\d\d)(?::(\d\d))?(?::(\d\d))?$/;

export function parsePostgresTimestamp(text: string): Timestamp {
  const match = postgresTimestampPattern.exec(text);
  if (match === null) {
    throw new Error(`unsupported timestamptz value: ${text}`);
  }
  const [, year, month, day, hour, minute, second, fraction, sign, offsetHour, offsetMinute, offsetSecond] = match;
  const offset = (Number(offsetHour) * 60 + Number(offsetMinute ?? 0)) * 60 + Number(offsetSecond ?? 0);
  const millis = Date.UTC(Number(year), Number(month) - 1, Number(day), Number(hour), Number(minute), Number(second));
  const micros = (millis / 1000 - (sign === "-" ? -offset : offset)) * 1_000_000;
  return new Timestamp(micros + (fraction === undefined ? 0 : Number(fraction.padEnd(6, "0"))));
}

const rfc3339Pattern = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:[.,](\d+))?(?:(Z)|([+-])(\d{2}):(\d{2}))$/;

export function parseRFC3339(value: string): Timestamp {
  const match = rfc3339Pattern.exec(value);
  if (match === null) {
    throw new Error(`parsing time ${JSON.stringify(value)} as "2006-01-02T15:04:05Z07:00"`);
  }
  const [, year, month, day, hour, minute, second, fraction, zulu, sign, offsetHour, offsetMinute] = match;
  if (!validDate(Number(year), Number(month), Number(day)) || Number(hour) > 23 || Number(minute) > 59 || Number(second) > 59) {
    throw new Error(`parsing time ${JSON.stringify(value)}: value out of range`);
  }
  let offset = 0;
  if (zulu === undefined) {
    if (Number(offsetHour) > 23 || Number(offsetMinute) > 59) {
      throw new Error(`parsing time ${JSON.stringify(value)}: time zone offset out of range`);
    }
    offset = (Number(offsetHour) * 60 + Number(offsetMinute)) * 60 * (sign === "-" ? -1 : 1);
  }
  const date = new Date(0);
  date.setUTCFullYear(Number(year), Number(month) - 1, Number(day));
  date.setUTCHours(Number(hour), Number(minute), Number(second), 0);
  const micros = (date.getTime() / 1000 - offset) * 1_000_000;
  return new Timestamp(micros + (fraction === undefined ? 0 : Number(fraction.slice(0, 6).padEnd(6, "0"))));
}

export function validDate(year: number, month: number, day: number): boolean {
  if (month < 1 || month > 12 || day < 1) {
    return false;
  }
  const date = new Date(0);
  date.setUTCFullYear(year, month, 0);
  return day <= date.getUTCDate();
}

export function formatDate(year: number, month: number, day: number): string {
  const date = new Date(0);
  date.setUTCFullYear(year, month - 1, day);
  return [
    String(date.getUTCFullYear()).padStart(4, "0"),
    String(date.getUTCMonth() + 1).padStart(2, "0"),
    String(date.getUTCDate()).padStart(2, "0"),
  ].join("-");
}
