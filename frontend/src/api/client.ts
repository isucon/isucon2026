import type { paths } from './schema';
import { clearToken, getToken } from '../app/auth';

export class APIError extends Error {
  constructor(public readonly status: number, public readonly problem: unknown) {
    super(`API request failed with status ${status}`);
  }
}

export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const token = await getToken();
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const response = await fetch(`/api/v1${path}`, { ...init, headers, credentials: 'omit' });
  if (response.status === 401 && token) {
    await clearToken();
    window.dispatchEvent(new Event('isureal:session-invalidated'));
  }
  return response;
}

export async function jsonRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await apiFetch(path, init);
  if (!response.ok) {
    const problem = await response.json().catch(() => null);
    throw new APIError(response.status, problem);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

export type APIPaths = paths;
