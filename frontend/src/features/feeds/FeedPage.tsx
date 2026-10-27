import { Link, NavLink } from 'react-router-dom';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  type CurrentPostingWindow,
  type FeedKind,
  getCurrentPostingWindow,
  getFeed,
  type Post,
} from '../../api/posts';
import { APIError } from '../../api/client';
import { StateView } from '../../components/UI';
const labels: Record<FeedKind, string> = {
  friends: 'My friends',
  'friends-of-friends': 'Friends of friends',
  public: '公開',
  following: 'フォロー中',
};
export function FeedTabs() {
  return (
    <nav className='tabs feed-tabs' aria-label='フィード'>
      {Object.entries(labels).map(([kind, label]) => (
        <NavLink
          key={kind}
          to={kind === 'friends' ? '/' : `/feeds/${kind}`}
          end
        >
          {label}
        </NavLink>
      ))}
    </nav>
  );
}
export function FeedPage({ feed }: { feed: FeedKind }) {
  const [items, setItems] = useState<Post[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [gate, setGate] = useState<CurrentPostingWindow | null>(null);
  const request = useRef(0);
  const load = useCallback(async (next?: string) => {
    const id = ++request.current;
    setLoading(true);
    setError('');
    try {
      if (!next) {
        const state = await getCurrentPostingWindow();
        if (id !== request.current) return;
        setGate(state);
        if (!state.hasPosted) {
          setItems([]);
          setCursor(null);
          return;
        }
      }
      const page = await getFeed(feed, next);
      if (id !== request.current) return;
      setItems((old) => next ? [...old, ...page.items] : page.items);
      setCursor(page.nextPage);
    } catch (reason) {
      if (id === request.current) {
        setError(
          reason instanceof APIError && reason.message === 'region_mismatch'
            ? '接続先の地域を確認してください。'
            : '写真を読み込めませんでした。',
        );
      }
    } finally {
      if (id === request.current) setLoading(false);
    }
  }, [feed]);
  useEffect(() => {
    setItems([]);
    setGate(null);
    setCursor(null);
    void load();
    return () => {
      request.current++;
    };
  }, [load]);
  return (
    <section>
      <h1 className='sr-only'>{labels[feed]}フィード</h1>
      <FeedTabs />
      {error
        ? (
          <StateView
            title={error}
            detail='接続を確認して、もう一度お試しください。'
          >
            <button onClick={() => void load()}>再読み込み</button>
          </StateView>
        )
        : !loading && gate && !gate.hasPosted
        ? (
          <StateView
            icon='lock'
            title={gate.canPost
              ? 'まずは、あなたの一枚。'
              : '次の一枚を、待っています。'}
            detail={gate.canPost
              ? '投稿すると、友達の写真が見られます。'
              : gate.deletionCount >= 2
              ? 'この投稿期間の削除回数の上限に達しました。'
              : '投稿時間が始まるとお知らせします。'}
          >
            {gate.canPost && (
              <Link className='button' to='/posts/new'>今日の一枚を投稿</Link>
            )}
            <Link className='text-link' to='/notifications'>通知を見る</Link>
          </StateView>
        )
        : (
          <>
            <div className='feed-grid'>
              {items.map((post) => (
                <article className='post-card' key={post.id}>
                  <div className='card-meta'>
                    <Link to={`/users/${post.author.id}`}>
                      {post.author.displayName}
                    </Link>
                    <time dateTime={post.postedAt}>
                      {new Date(post.postedAt).toLocaleTimeString('ja-JP', {
                        hour: '2-digit',
                        minute: '2-digit',
                      })}
                    </time>
                  </div>
                  <Link
                    to={`/posts/${post.id}`}
                    aria-label={`${post.author.displayName}の投稿を見る`}
                  >
                    <img
                      className='post-image'
                      src={post.imageUrl}
                      alt={`${post.author.displayName}の投稿画像`}
                    />
                  </Link>
                  <p>{post.caption}</p>
                  <p className='muted'>
                    {post.onTime ? '時間内' : `${post.lateSeconds}秒遅れ`}
                    {post.location.kind === 'approximate' &&
                      ` · ${post.location.locality}`}
                    {post.location.kind === 'exact' &&
                      ` · ${post.location.latitude}, ${post.location.longitude}`}
                  </p>
                </article>
              ))}
            </div>
            {!loading && items.length === 0 && (
              <StateView
                title='まだ、一枚もありません。'
                detail='友達が投稿すると、ここに写真が届きます。'
              >
                <Link className='button' to='/users'>友達を探す</Link>
              </StateView>
            )}
            {loading && (
              <StateView
                busy
                title='少しだけ、お待ちください。'
                detail='みんなの一枚を読み込んでいます…'
              />
            )}
            {cursor && !loading && (
              <button
                className='secondary more'
                onClick={() => void load(cursor)}
              >
                さらに読み込む
              </button>
            )}
          </>
        )}
    </section>
  );
}
