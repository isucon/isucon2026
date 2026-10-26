import { Link } from 'react-router-dom';
import { useNotifications } from './NotificationProvider';
import { Icon, StateView } from '../../components/UI';
export function NotificationPage() {
  const { items, error } = useNotifications();
  return (
    <section>
      <h1>通知</h1>
      <p className='muted'>接続中に届いた投稿開始のお知らせを表示します。</p>
      {error && <p role='alert'>{error}</p>}
      {!items.length && (
        <StateView
          icon='bell'
          title='次の一枚を、待っています。'
          detail='投稿時間が始まると、ここにお知らせが届きます。'
        />
      )}
      <ul className='user-list'>
        {items.map((item) => (
          <li key={item.id}>
            <Icon name='bell' />
            <div>
              <p>{item.region} の投稿時間が始まりました</p>
              <time dateTime={item.createdAt}>
                {new Date(item.createdAt).toLocaleString('ja-JP')}
              </time>
            </div>
            <Link className='button secondary compact' to='/posts/new'>
              投稿する
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}
