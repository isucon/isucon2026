import { apiFetch, APIError } from './client';

export interface NotificationItem {
  id: string;
  postingWindowId: string;
  region: 'america' | 'europe' | 'asia';
  createdAt: string;
}

export async function receiveNotification(signal: AbortSignal): Promise<NotificationItem | null> {
  const response = await apiFetch('/notifications', { headers: { Accept: 'text/event-stream' }, signal });
  if (!response.ok) throw new APIError(response.status, await response.json().catch(() => null));
  // The server closes the stream after delivering one event.
  const body = await response.text();
  const data = body.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n');
  return data ? JSON.parse(data) as NotificationItem : null;
}
