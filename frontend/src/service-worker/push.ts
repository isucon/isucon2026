/// <reference lib="webworker" />

declare const self: ServiceWorkerGlobalScope;

export interface PushMessage {
  notificationId: string;
}

export function notificationOptions(message: PushMessage): NotificationOptions {
  return {
    body: '今日の投稿時間が始まりました。',
    tag: message.notificationId,
    data: { notificationId: message.notificationId },
  };
}

function validMessage(value: unknown): value is PushMessage {
  return typeof value === 'object' && value !== null &&
    typeof (value as { notificationId?: unknown }).notificationId === 'string';
}

function hasLocalSession(): Promise<boolean> {
  return new Promise((resolve) => {
    const request = indexedDB.open('isureal-session', 1);
    request.onerror = () => resolve(false);
    request.onupgradeneeded = () => request.transaction?.abort();
    request.onsuccess = () => {
      const database = request.result;
      if (!database.objectStoreNames.contains('credentials')) { database.close(); resolve(false); return; }
      const transaction = database.transaction('credentials', 'readonly');
      const token = transaction.objectStore('credentials').get('user-token');
      token.onerror = () => resolve(false);
      token.onsuccess = () => resolve(typeof token.result === 'string' && token.result.length > 0);
      transaction.oncomplete = () => database.close();
    };
  });
}

self.addEventListener('push', (event: PushEvent) => {
  let message: unknown;
  try { message = event.data?.json(); } catch { return; }
  if (!validMessage(message)) return;
  event.waitUntil((async () => {
    if (!await hasLocalSession()) return;
    await self.registration.showNotification('ISUReal', notificationOptions(message));
  })());
});

self.addEventListener('notificationclick', (event: NotificationEvent) => {
  event.notification.close();
  const notificationId = (event.notification.data as { notificationId?: unknown } | undefined)?.notificationId;
  if (typeof notificationId !== 'string') return;
  const target = `/notifications?open=${encodeURIComponent(notificationId)}`;
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    const visible = windows.find((client) => 'focus' in client) as WindowClient | undefined;
    if (visible) {
      await visible.navigate(target);
      await visible.focus();
      return;
    }
    await self.clients.openWindow(target);
  })());
});
