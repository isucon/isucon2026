import { useEffect, useState } from 'react';
import { getMemories, type MemoryPost } from '../../api/account';
import { deletePost } from '../../api/posts';

export function MemoriesPage(): React.JSX.Element {
  const [month, setMonth] = useState(new Date().toISOString().slice(0, 7));
  const [items, setItems] = useState<MemoryPost[]>([]);
  const [nextPage, setNextPage] = useState<string | null>(null);
  const [error, setError] = useState('');
  async function load(next?: string) {
    try {
      const page = await getMemories(month, next);
      setItems(old => next ? [...old, ...page.items] : page.items);
      setNextPage(page.nextPage); setError('');
    } catch { setError('Memories を取得できませんでした。'); }
  }
  useEffect(() => { setItems([]); setNextPage(null); void load(); }, [month]);
  async function remove(id: string) {
    if (!window.confirm('この過去投稿を削除しますか？')) return;
    try { await deletePost(id); await load(); }
    catch { setError('投稿を削除できませんでした。'); }
  }
  return <section><h1>Memories</h1><label>月<input type="month" value={month} onChange={event => setMonth(event.target.value)} /></label>
    {error && <p role="alert">{error}</p>}
    <div className="feed-grid">{items.map(post => <article className="post-card" key={post.id}>
      <img src={post.imageUrl} alt="過去の投稿画像" /><p>{post.caption}</p>
      <details><summary>RealMoji とコメント</summary>
        <ul>{post.realmojis.map(item => <li key={item.id}><img src={item.imageUrl} alt={`${item.reactor.displayName}のRealMoji`} />{item.reactionType}</li>)}</ul>
        <ol>{post.comments.map(item => <li key={item.id}>{item.author.displayName}: {item.body}</li>)}</ol>
      </details>
      <button className="danger" onClick={() => void remove(post.id)}>削除</button>
    </article>)}</div>
    {nextPage && <button onClick={() => void load(nextPage)}>さらに読み込む</button>}
  </section>;
}
