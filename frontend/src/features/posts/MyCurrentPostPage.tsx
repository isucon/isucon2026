import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { APIError } from '../../api/client';
import {
  deletePost,
  getMyCurrentPost,
  type Post,
  updatePost,
  type Visibility,
} from '../../api/posts';
import { ConfirmButton, StateView } from '../../components/UI';
export function MyCurrentPostPage() {
  const [post, setPost] = useState<Post | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [caption, setCaption] = useState('');
  const [visibility, setVisibility] = useState<Visibility>('friends');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  async function load() {
    setLoading(true);
    setError('');
    try {
      const value = await getMyCurrentPost();
      setPost(value);
      setCaption(value.caption);
      setVisibility(value.visibility);
    } catch (reason) {
      if (!(reason instanceof APIError && reason.status === 404)) {
        setError('現在の投稿を取得できませんでした。');
      }
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  if (loading) return <StateView busy title='投稿を読み込んでいます…' />;
  if (!post) {
    return (
      <section>
        <h1>自分の現在の投稿</h1>
        <StateView
          title={error || '今日の一枚は、まだ。'}
          detail='削除は期間ごとに2回までです。'
        >
          {error
            ? <button onClick={() => void load()}>再読み込み</button>
            : <Link className='button' to='/posts/new'>投稿する</Link>}
        </StateView>
      </section>
    );
  }
  async function save() {
    if (!post) return;
    setBusy(true);
    setMessage('');
    try {
      setPost(await updatePost(post.id, { caption, visibility }));
      setError('');
      setMessage('保存しました。');
    } catch {
      setError('投稿を更新できませんでした。');
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className='panel'>
      <h1>自分の現在の投稿</h1>
      <img className='post-image' src={post.imageUrl} alt='自分の投稿画像' />
      <p className='muted'>
        {post.onTime ? '時間内の投稿' : `${post.lateSeconds}秒遅れ`}
      </p>
      <div className='stack'>
        <label>
          キャプション<textarea
            maxLength={1000}
            value={caption}
            onChange={(event) => setCaption(event.target.value)}
          />
        </label>
        <label>
          公開範囲<select
            value={visibility}
            onChange={(event) =>
              setVisibility(event.target.value as Visibility)}
          >
            <option value='friends'>友達</option>
            <option value='friends_of_friends'>友達の友達</option>
            <option value='public'>公開</option>
          </select>
        </label>
        {error && <p role='alert'>{error}</p>}
        {message && <p role='status'>{message}</p>}
        <button disabled={busy} onClick={() => void save()}>
          {busy ? '保存中…' : '変更を保存'}
        </button>
        <ConfirmButton
          className='secondary'
          title='投稿の削除'
          detail='この投稿を削除しますか？'
          onConfirm={async () => {
            await deletePost(post.id);
            setPost(null);
          }}
        >
          削除する
        </ConfirmButton>
        <Link className='text-link' to={`/posts/${post.id}`}>
          投稿の交流を見る
        </Link>
      </div>
    </section>
  );
}
