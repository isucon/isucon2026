import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { APIError } from '../../api/client';
import { deletePost, getMyCurrentPost, updatePost, type Post, type Visibility } from '../../api/posts';

export function MyCurrentPostPage(): React.JSX.Element {
  const [post, setPost] = useState<Post | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [caption, setCaption] = useState('');
  const [visibility, setVisibility] = useState<Visibility>('friends');
  useEffect(() => { void getMyCurrentPost().then((value) => { setPost(value); setCaption(value.caption); setVisibility(value.visibility); }).catch((reason: unknown) => {
    if (!(reason instanceof APIError && reason.status === 404)) setError('現在の投稿を取得できませんでした。');
  }).finally(() => setLoading(false)); }, []);
  if (loading) return <p aria-busy="true">投稿を読み込んでいます…</p>;
  if (!post) return <section><h1>自分の現在の投稿</h1>{error ? <p role="alert">{error}</p> : <p>現在の投稿はありません。削除は期間ごとに2回までです。</p>}<Link to="/posts/new">投稿する</Link></section>;
  async function save(): Promise<void> {
    if (!post) return;
    try { setPost(await updatePost(post.id, { caption, visibility })); setError(''); }
    catch { setError('投稿を更新できませんでした。'); }
  }
  async function remove(): Promise<void> {
    if (!post || !window.confirm('この投稿を削除しますか？')) return;
    try { await deletePost(post.id); setPost(null); }
    catch { setError('投稿を削除できませんでした。'); }
  }
  return <section className="panel"><h1>自分の現在の投稿</h1><img className="post-image" src={post.imageUrl} alt="自分の投稿画像" />
    <p>{post.onTime ? '時間内の投稿' : `${post.lateSeconds}秒遅れ`}</p>
    <label>キャプション<textarea maxLength={1000} value={caption} onChange={(event) => setCaption(event.target.value)} /></label>
    <label>公開範囲<select value={visibility} onChange={(event) => setVisibility(event.target.value as Visibility)}><option value="friends">友達</option><option value="friends_of_friends">友達の友達</option><option value="public">公開</option></select></label>
    {error && <p role="alert">{error}</p>}<div className="actions"><button type="button" onClick={() => { void save(); }}>変更を保存</button><button className="danger" type="button" onClick={() => { void remove(); }}>削除する</button></div>
  </section>;
}
