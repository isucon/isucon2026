// `crypto.randomUUID()` is only exposed in secure contexts, so it is undefined
// when the app is served over plain HTTP through the Router domain (for example
// http://app.team1-local.127.0.0.1.nip.io:18080/).  `crypto.getRandomValues()`
// is available in insecure contexts, so fall back to a v4 UUID built from it.
type UUIDCrypto = Pick<Crypto, 'getRandomValues'> & {
  randomUUID?: Crypto['randomUUID'];
};

export function randomUUID(source: UUIDCrypto = globalThis.crypto): string {
  if (typeof source.randomUUID === 'function') {
    return source.randomUUID();
  }
  const bytes = new Uint8Array(16);
  source.getRandomValues(bytes);
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0'));
  return (
    hex.slice(0, 4).join('') +
    '-' +
    hex.slice(4, 6).join('') +
    '-' +
    hex.slice(6, 8).join('') +
    '-' +
    hex.slice(8, 10).join('') +
    '-' +
    hex.slice(10, 16).join('')
  );
}
