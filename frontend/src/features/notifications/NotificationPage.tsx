import { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { listNotifications, openNotification, type NotificationItem } from '../../api/notifications';
import { disablePush, enablePush } from '../../app/serviceWorker';

export function NotificationPage(): React.JSX.Element {
  const [items, setItems] = useState<NotificationItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [message, setMessage] = useState('');
  const [params] = useSearchParams();
  const navigate = useNavigate();

  async function load(next?: string): Promise<void> {
    const page = await listNotifications(next);
    setItems((current) => next ? [...current, ...page.items] : page.items);
    setCursor(page.nextCursor);
  }
  async function open(id: string): Promise<void> {
    const destination = await openNotification(id);
    setItems((current) => current.map((item) => item.id === id ? { ...item, read: true } : item));
    navigate(destination.path);
  }
  useEffect(() => { void load().catch(() => setMessage('通知を読み込めませんでした')); }, []);
  useEffect(() => {
    const id = params.get('open');
    if (id) void open(id).catch(() => setMessage('通知を開けませんでした'));
  }, [params]);

  return <section><h1>通知</h1><div className="actions">
    <button onClick={() => void enablePush().then(() => setMessage('Push 通知を有効にしました')).catch((error: unknown) => setMessage(error instanceof Error ? error.message : 'Push 通知を有効にできませんでした'))}>Push 通知を有効にする</button>
    <button onClick={() => void disablePush().then(() => setMessage('Push 通知を無効にしました')).catch(() => setMessage('Push 通知を無効にできませんでした'))}>Push 通知を無効にする</button>
  </div>{message && <p role="status">{message}</p>}<ul className="card-list">{items.map((item) => <li key={item.id}>
    <button className={item.read ? '' : 'unread'} onClick={() => void open(item.id)}>{item.region} の投稿時間が始まりました <time dateTime={item.createdAt}>{new Date(item.createdAt).toLocaleString()}</time>{!item.read && '（未読）'}</button>
  </li>)}</ul>{cursor && <button onClick={() => void load(cursor)}>さらに読み込む</button>}</section>;
}
