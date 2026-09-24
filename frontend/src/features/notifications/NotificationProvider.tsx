import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { APIError } from '../../api/client';
import { receiveNotification, type NotificationItem } from '../../api/notifications';

const NotificationContext = createContext<{ items: NotificationItem[]; error: string }>({ items: [], error: '' });
export const useNotifications = () => useContext(NotificationContext);

export function NotificationProvider({ children }: { children: ReactNode }): React.JSX.Element {
  const [items, setItems] = useState<NotificationItem[]>([]);
  const [error, setError] = useState('');
  const [version, setVersion] = useState(0);
  useEffect(() => {
    const changed = () => { setItems([]); setVersion(value => value + 1); };
    window.addEventListener('isureal:region-changed', changed);
    return () => window.removeEventListener('isureal:region-changed', changed);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    let retry: ReturnType<typeof setTimeout> | undefined;
    async function connect() {
      try {
        const item = await receiveNotification(controller.signal);
        if (controller.signal.aborted) return;
        setError('');
        if (item) setItems(old => [item, ...old.filter(previous => previous.id !== item.id)]);
        retry = setTimeout(() => void connect(), item ? 0 : 1000);
      } catch (reason) {
        if (controller.signal.aborted) return;
        setError('通知に接続できませんでした。');
        if (reason instanceof APIError && (reason.status === 401 || reason.status === 403)) return;
        retry = setTimeout(() => void connect(), 3000);
      }
    }
    void connect();
    return () => { controller.abort(); clearTimeout(retry); };
  }, [version]);
  return <NotificationContext.Provider value={{ items, error }}>
    {items[0] && <p role="status">投稿時間が始まりました。<Link to="/posts/new">投稿する</Link></p>}
    {children}
  </NotificationContext.Provider>;
}
