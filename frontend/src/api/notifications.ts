import { jsonRequest } from './client';
import { randomUUID } from './ids';

export interface NotificationItem {
  id: string;
  postingWindowId: string;
  region: 'america' | 'europe' | 'asia';
  createdAt: string;
  read: boolean;
}

export interface NotificationPage {
  items: NotificationItem[];
  nextCursor: string | null;
}

const idempotencyHeaders = (): HeadersInit => ({ 'Idempotency-Key': randomUUID() });

export const listNotifications = (cursor?: string) =>
  jsonRequest<NotificationPage>(`/notifications${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`);

export const openNotification = (id: string) =>
  jsonRequest<{ destination: 'create-post' | 'my-current-post' | 'memory'; path: string }>(
    `/notifications/${id}/open`,
    { method: 'POST', headers: idempotencyHeaders() },
  );

export const replacePushSubscription = (subscription: PushSubscriptionJSON) =>
  jsonRequest<void>('/me/push-subscription', {
    method: 'PUT',
    headers: { ...idempotencyHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(subscription),
  });

export const removePushSubscription = () =>
  jsonRequest<void>('/me/push-subscription', { method: 'DELETE', headers: idempotencyHeaders() });
