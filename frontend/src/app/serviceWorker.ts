import { removePushSubscription, replacePushSubscription } from '../api/notifications';

function decodeBase64URL(value: string): Uint8Array<ArrayBuffer> {
  const padding = '='.repeat((4 - value.length % 4) % 4);
  const bytes = atob((value + padding).replace(/-/g, '+').replace(/_/g, '/'));
  return Uint8Array.from(bytes, (character) => character.charCodeAt(0));
}

export async function registerServiceWorker(): Promise<ServiceWorkerRegistration> {
  if (!('serviceWorker' in navigator)) throw new Error('このブラウザーは通知に対応していません');
  const script = import.meta.env.DEV ? '/src/service-worker/push.ts' : '/service-worker.js';
  return navigator.serviceWorker.register(script, { type: 'module' });
}

export async function enablePush(): Promise<void> {
  const publicKey = import.meta.env.VITE_VAPID_PUBLIC_KEY as string | undefined;
  if (!publicKey) throw new Error('Push 通知は現在設定されていません');
  const permission = await Notification.requestPermission();
  if (permission !== 'granted') throw new Error('通知の許可が必要です');
  const registration = await registerServiceWorker();
  const previous = await registration.pushManager.getSubscription();
  const subscription = previous ?? await registration.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: decodeBase64URL(publicKey),
  });
  await replacePushSubscription(subscription.toJSON());
}

export async function disablePush(): Promise<void> {
  await removePushSubscription();
  if (!('serviceWorker' in navigator)) return;
  const registration = await navigator.serviceWorker.getRegistration();
  const subscription = await registration?.pushManager.getSubscription();
  await subscription?.unsubscribe();
}
