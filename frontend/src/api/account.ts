import { APIError, apiFetch, jsonRequest } from './client';
import { pageRequest } from './pagination';
import { replaceSecretKey, replaceToken } from '../app/auth';
import type { Post } from './posts';
import type { Comment, RealMoji } from './interactions';

export interface SessionUser {
  id: string;
  username: string;
  displayName: string;
  bio: string;
  region: 'america' | 'europe' | 'asia';
  accountType: string;
  status: string;
}
export interface Me extends SessionUser { profileImageUrl: string | null }
export interface MemoryPost extends Post { realmojis: RealMoji[]; comments: Comment[] }

export function cachedMe(): Me | null {
  const value = sessionStorage.getItem('isureal-me');
  return value ? JSON.parse(value) as Me : null;
}
function remember(user: Me): Me {
  sessionStorage.setItem('isureal-me', JSON.stringify(user));
  return user;
}
export async function login(username: string, secretKey: string) {
  const result = await jsonRequest<{ token: string; user: SessionUser }>('/auth/login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username, secretKey }),
  });
  await replaceSecretKey(secretKey);
  remember({ ...result.user, profileImageUrl: null });
  await replaceToken(result.token);
  return result.user;
}
export async function register(input: { username: string; displayName: string; region: string }) {
  const result = await jsonRequest<{ secretKey: string; user: SessionUser }>('/auth/register', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  });
  await replaceSecretKey(result.secretKey);
  return result;
}
export const getMe = async () => remember(await jsonRequest<Me>('/me'));
export const updateMe = async (input: Partial<Pick<Me, 'displayName' | 'bio' | 'region'>>) => remember(await jsonRequest<Me>('/me', {
  method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
}));
export const putProfileImage = (file: File) => jsonRequest<void>('/me/profile-image', { method: 'PUT', headers: { 'Content-Type': 'image/jpeg' }, body: file });
export const deleteProfileImage = () => jsonRequest<void>('/me/profile-image', { method: 'DELETE' });
export const requestAccountDeletion = () => jsonRequest<void>('/me/account-deletion', { method: 'POST' });
const position = (post: MemoryPost) => ({ beforePostedAt: post.postedAt, beforeId: post.id });
export const getMemories = (month: string, next?: string) => pageRequest<MemoryPost>(`/memories?month=${encodeURIComponent(month)}`, next, position);
export const getMemoriesDate = (date: string, next?: string) => pageRequest<MemoryPost>(`/memories/${date}`, next, position);

export async function exportPosts(): Promise<Blob> {
  const response = await apiFetch('/me/export', { headers: { Accept: 'application/zip' } });
  if (!response.ok) throw new APIError(response.status, await response.json().catch(() => null));
  return response.blob();
}
