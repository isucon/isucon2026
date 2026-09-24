import { Link } from 'react-router-dom';
import { useNotifications } from './NotificationProvider';

export function NotificationPage(): React.JSX.Element {
  const { items, error } = useNotifications();
  return <section><h1>通知</h1><p>接続中に届いた投稿開始のお知らせを表示します。</p>
    {error && <p role="alert">{error}</p>}
    {!items.length && <p>新しい通知を待っています。</p>}
    <ul>{items.map(item => <li key={item.id}><Link to="/posts/new">{item.region} の投稿時間が始まりました</Link> <time dateTime={item.createdAt}>{new Date(item.createdAt).toLocaleString()}</time></li>)}</ul>
  </section>;
}
