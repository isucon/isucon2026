import { useEffect, useRef, useState } from 'react';
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from 'react-router-dom';
import { getMemories, type MemoryPost } from '../../api/account';
import { deletePost } from '../../api/posts';
import { ConfirmButton, StateView } from '../../components/UI';
export function MemoriesPage() {
  const [params, setParams] = useSearchParams();
  const month = params.get('month') || new Date().toISOString().slice(0, 7);
  const [items, setItems] = useState<MemoryPost[]>([]);
  const [nextPage, setNextPage] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const version = useRef(0);
  async function load(next?: string) {
    const id = ++version.current;
    setLoading(true);
    setError('');
    try {
      const page = await getMemories(month, next);
      if (id !== version.current) return;
      setItems((old) => next ? [...old, ...page.items] : page.items);
      setNextPage(page.nextPage);
    } catch {
      if (id === version.current) setError('Memories を取得できませんでした。');
    } finally {
      if (id === version.current) setLoading(false);
    }
  }
  useEffect(() => {
    setItems([]);
    setNextPage(null);
    void load();
    return () => {
      version.current++;
    };
  }, [month]);
  return (
    <section>
      <h1>Memories</h1>
      <label>
        月<input
          type='month'
          required
          value={month}
          onChange={(event) => {
            if (event.target.value) setParams({ month: event.target.value });
          }}
        />
      </label>
      {error && (
        <div role='alert'>
          <p>{error}</p>
          <button onClick={() => void load()}>再読み込み</button>
        </div>
      )}
      <div className='feed-grid memories-grid'>
        {items.map((post) => (
          <article className='post-card' key={post.id}>
            <time dateTime={post.postedAt}>
              {new Date(post.postedAt).toLocaleDateString('ja-JP', {
                month: 'long',
                day: 'numeric',
              })}
            </time>
            <Link
              to={`/memories/posts/${post.id}?month=${month}`}
              aria-label={`${post.caption || '写真'}の過去投稿を見る`}
            >
              <img
                className='post-image'
                src={post.imageUrl}
                alt='過去の投稿画像'
              />
            </Link>
            <p>{post.caption}</p>
            <ConfirmButton
              className='secondary'
              title='投稿の削除'
              detail='この過去投稿を削除しますか？'
              onConfirm={async () => {
                await deletePost(post.id);
                await load();
              }}
            >
              削除
            </ConfirmButton>
          </article>
        ))}
      </div>
      {loading && <StateView busy title='写真を読み込んでいます…' />}
      {!loading && !error && items.length === 0 && (
        <StateView
          title='この月の写真は、まだ。'
          detail='投稿した一枚が、ここに残っていきます。'
        />
      )}
      {nextPage && (
        <button
          className='secondary more'
          disabled={loading}
          onClick={() => void load(nextPage)}
        >
          さらに読み込む
        </button>
      )}
    </section>
  );
}
export function MemoryDetailPage() {
  const { postId = '' } = useParams();
  const [params] = useSearchParams();
  const month = params.get('month') || new Date().toISOString().slice(0, 7);
  const [post, setPost] = useState<MemoryPost | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [retry, setRetry] = useState(0);
  const navigate = useNavigate();
  useEffect(() => {
    let active = true;
    setLoading(true);
    setError('');
    setPost(null);
    void (async () => {
      try {
        let cursor: string | undefined;
        do {
          const page = await getMemories(month, cursor);
          if (!active) return;
          const found = page.items.find((item) => item.id === postId);
          if (found) {
            setPost(found);
            return;
          }
          cursor = page.nextPage || undefined;
        } while (cursor);
        setError(
          'この月には投稿が見つかりませんでした。Memoriesから月を選び直してください。',
        );
      } catch {
        if (active) setError('過去投稿を取得できませんでした。');
      } finally {
        if (active) setLoading(false);
      }
    })();
    return () => {
      active = false;
    };
  }, [postId, month, retry]);
  return (
    <section>
      <Link className='back-link' to={`/memories?month=${month}`}>
        ‹ Memories
      </Link>
      <h1>過去投稿・交流を表示</h1>
      {loading
        ? <StateView busy title='写真を読み込んでいます…' />
        : error
        ? (
          <StateView title={error}>
            <button onClick={() => setRetry((value) => value + 1)}>
              再読み込み
            </button>
          </StateView>
        )
        : post && (
          <>
            <img
              className='post-image'
              src={post.imageUrl}
              alt='過去の投稿画像'
            />
            <p>{post.caption}</p>
            <time dateTime={post.postedAt}>
              {new Date(post.postedAt).toLocaleString('ja-JP')}
            </time>
            <h2>RealMoji</h2>
            <ul className='reaction-list'>
              {post.realmojis.map((item) => (
                <li key={item.id}>
                  <img
                    src={item.imageUrl}
                    alt={`${item.reactor.displayName}のRealMoji`}
                  />
                  {item.reactionType}
                </li>
              ))}
            </ul>
            <h2>コメント</h2>
            <ol className='comment-list'>
              {post.comments.map((item) => (
                <li key={item.id}>
                  <strong>{item.author.displayName}</strong>
                  <p>{item.body}</p>
                </li>
              ))}
            </ol>
            <p className='muted'>
              過去の交流は閲覧のみ。各50件まで表示します。
            </p>
            <ConfirmButton
              title='投稿の削除'
              detail='この過去投稿を削除しますか？'
              onConfirm={async () => {
                await deletePost(post.id);
                navigate(`/memories?month=${month}`);
              }}
            >
              削除
            </ConfirmButton>
          </>
        )}
    </section>
  );
}
