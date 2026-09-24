import { afterEach, describe, expect, it, vi } from 'vitest';
import { getFeed, createPost } from './posts';
import { getMemories } from './account';
import { listComments, listRealMojis } from './interactions';
import { listRelationships, searchUsers } from './relationships';
import { receiveNotification } from './notifications';
import { login, register } from './account';
import { replaceToken } from '../app/auth';
import { APIError, jsonRequest } from './client';

vi.mock('../app/auth', () => ({
  getToken: vi.fn(async () => 'session'), clearToken: vi.fn(), replaceToken: vi.fn(), replaceSecretKey: vi.fn(),
}));
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); sessionStorage.clear(); });

describe('Go API contract', () => {
  it('registers without a token and logs in separately', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ secretKey: 'secret', user: { username: 'alice' } })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: 'new-session', user: { username: 'alice' } })));
    vi.stubGlobal('fetch', fetcher);
    const result = await register({ username: 'alice', displayName: 'Alice', region: 'asia' });
    expect(result.secretKey).toBe('secret');
    expect(replaceToken).not.toHaveBeenCalled();
    await login('alice', result.secretKey);
    expect(replaceToken).toHaveBeenCalledWith('new-session');
    expect(fetcher.mock.calls.map(call => call[0])).toEqual(['/api/auth/register', '/api/auth/login']);
    expect(new Headers(fetcher.mock.calls[0][1].headers).has('Idempotency-Key')).toBe(false);
  });

  it.each([
    [() => getFeed('friends'), (next: string) => getFeed('friends', next), { id: 'p', postedAt: '2026-09-24T01:00:00Z' }, 'beforePostedAt', 'beforeId', 'p'],
    [() => getMemories('2026-09'), (next: string) => getMemories('2026-09', next), { id: 'm', postedAt: '2026-09-24T01:00:00Z' }, 'beforePostedAt', 'beforeId', 'm'],
    [() => listComments('p'), (next: string) => listComments('p', next), { id: 'c', createdAt: '2026-09-24T01:00:00Z' }, 'afterCreatedAt', 'afterId', 'c'],
    [() => listRealMojis('p'), (next: string) => listRealMojis('p', next), { id: 'r', createdAt: '2026-09-24T01:00:00Z' }, 'beforeCreatedAt', 'beforeId', 'r'],
    [() => listRelationships('friends'), (next: string) => listRelationships('friends', next), { relationId: 'f', updatedAt: '2026-09-24T01:00:00Z' }, 'beforeUpdatedAt', 'beforeId', 'f'],
    [() => searchUsers('a'), (next: string) => searchUsers('a', next), { id: 'u', username: 'alice' }, 'beforeUsername', 'beforeId', 'u'],
  ] as const)('paginates using the last item (%#)', async (first, more, item, key, idKey, id) => {
    const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ items: [item], hasMore: true })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [], hasMore: false })));
    vi.stubGlobal('fetch', fetcher);
    const page = await first();
    const last = await more(page.nextPage!);
    const query = new URL(fetcher.mock.calls[1][0], 'http://localhost').searchParams;
    expect(query.has(key)).toBe(true);
    expect(query.get(idKey)).toBe(id);
    expect(query.has('cursor')).toBe(false);
    expect(last.nextPage).toBeNull();
  });

  it('sends location as multipart fields and preserves image bytes', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response('{}'));
    vi.stubGlobal('fetch', fetcher);
    const image = new File(['original'], 'image.jpg', { type: 'image/jpeg' });
    await createPost({ image, caption: 'test', visibility: 'friends', location: { kind: 'exact', latitude: 35, longitude: 139 } });
    const body = fetcher.mock.calls[0][1].body as FormData;
    expect(body.get('image')).toBe(image);
    expect(body.get('location_kind')).toBe('exact');
    expect(body.get('latitude')).toBe('35');
    expect(body.get('longitude')).toBe('139');
    expect(body.has('location')).toBe(false);
  });

  it('receives a notification split across stream chunks with bearer auth', async () => {
    const stream = new ReadableStream({ start(controller) {
      controller.enqueue(new TextEncoder().encode('data: {"id":'));
      controller.enqueue(new TextEncoder().encode('"n1"}\n\n'));
      controller.close();
    } });
    const fetcher = vi.fn().mockResolvedValue(new Response(stream));
    vi.stubGlobal('fetch', fetcher);
    expect(await receiveNotification(new AbortController().signal)).toEqual({ id: 'n1' });
    const headers = new Headers(fetcher.mock.calls[0][1].headers);
    expect(headers.get('Accept')).toBe('text/event-stream');
    expect(headers.get('Authorization')).toBe('Bearer session');
  });

  it('reports region mismatch using the simplified error body', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"error":"region_mismatch"}', { status: 403 })));
    const changed = vi.fn();
    window.addEventListener('isureal:region-mismatch', changed);
    await expect(jsonRequest('/me')).rejects.toEqual(new APIError(403, { error: 'region_mismatch' }));
    expect(changed).toHaveBeenCalledOnce();
    window.removeEventListener('isureal:region-mismatch', changed);
  });
});
