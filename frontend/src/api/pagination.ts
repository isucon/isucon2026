import { jsonRequest } from './client';

export interface Page<T> { items: T[]; nextPage: string | null }

export async function pageRequest<T>(path: string, next: string | undefined, position: (item: T) => Record<string, string>): Promise<Page<T>> {
  const separator = path.includes('?') ? '&' : '?';
  const page = await jsonRequest<{ items: T[]; hasMore: boolean }>(path + (next ? separator + next : ''));
  const last = page.items.at(-1);
  return { items: page.items, nextPage: page.hasMore && last ? new URLSearchParams(position(last)).toString() : null };
}
