import { clearToken, getToken } from '../app/auth';

export class APIError extends Error {
  constructor(public readonly status: number, public readonly problem: unknown) {
    super(typeof problem === 'object' && problem !== null && 'error' in problem ? String(problem.error) : `API request failed with status ${status}`);
  }
}

export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const token = await getToken();
  const headers = new Headers(init.headers);
  if (!headers.has('Accept')) headers.set('Accept', 'application/json');
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const response = await fetch(`${import.meta.env.VITE_API_PREFIX ?? '/api'}${path}`, { ...init, headers, credentials: 'omit' });
  if (response.status === 401 && token) {
    await clearToken();
    window.dispatchEvent(new Event('isureal:session-invalidated'));
  }
  if (response.status === 403) {
    const problem = await response.clone().json().catch(() => null) as { error?: string } | null;
    if (problem?.error === 'region_mismatch') window.dispatchEvent(new Event('isureal:region-mismatch'));
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
