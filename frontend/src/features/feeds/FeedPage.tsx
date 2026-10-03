import { Link } from 'react-router-dom';
import { useEffect, useState } from 'react';
import { getFeed, type FeedKind, type Post } from '../../api/posts';

const labels: Record<FeedKind, string> = { friends: '友達', 'friends-of-friends': '友達の友達', public: '公開', following: 'フォロー中' };

export function FeedPage({ feed }: { feed: FeedKind }): React.JSX.Element {
  const [items, setItems] = useState<Post[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  async function load(next?: string): Promise<void> {
    setLoading(true);
    try { const page = await getFeed(feed, next); setItems((old) => next ? [...old, ...page.items] : page.items); setCursor(page.nextPage); setError(''); }
    catch { setError('フィードを取得できませんでした。投稿後にもう一度お試しください。'); }
    finally { setLoading(false); }
  }
  useEffect(() => { setItems([]); setCursor(null); void load(); }, [feed]);
  return <section><h1>{labels[feed]}フィード</h1>{error && <p role="alert">{error}</p>}
    <div className="feed-grid">{items.map((post) => <article className="post-card" key={post.id}><h2><Link to={`/posts/${post.id}`}>{post.author.displayName}</Link></h2><img src={post.imageUrl} alt={`${post.author.displayName}の投稿画像`} /><p>{post.caption}</p><p>{post.onTime ? '時間内' : `${post.lateSeconds}秒遅れ`}</p><LocationText post={post} /></article>)}</div>
    {!loading && items.length === 0 && !error && <p>表示できる投稿はありません。</p>}
    {loading && <p aria-busy="true">読み込み中…</p>}{cursor && !loading && <button type="button" onClick={() => { void load(cursor); }}>さらに読み込む</button>}
  </section>;
}

function LocationText({ post }: { post: Post }): React.JSX.Element | null {
  if (post.location.kind === 'exact') return <p>位置: {post.location.latitude}, {post.location.longitude}</p>;
  if (post.location.kind === 'approximate') return <p>場所: {post.location.locality}</p>;
  return null;
}
