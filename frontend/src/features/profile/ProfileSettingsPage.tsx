import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { cachedMe, deleteProfileImage, exportPosts, getMe, putProfileImage, requestAccountDeletion, updateMe, type Me } from '../../api/account';
import { APIError } from '../../api/client';
import { clearToken, getSecretKey } from '../../app/auth';
import { SecretKeyPanel } from '../auth/SecretKeyPanel';
import { RegionSettings } from './RegionSettings';

export function ProfileSettingsPage(): React.JSX.Element {
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState('');
  const [exportStatus, setExportStatus] = useState('');
  const [error, setError] = useState('');
  const [me, setMe] = useState<Me | null>(null);
  const [display, setDisplay] = useState('');
  const [bio, setBio] = useState('');
  const [image, setImage] = useState<File | null>(null);
  const [secretKey, setSecretKey] = useState('');
  const navigate = useNavigate();
  async function load() {
    let value: Me;
    try { value = await getMe(); }
    catch (reason) {
      const cached = cachedMe();
      if (!(reason instanceof APIError && reason.status === 403 && cached)) throw reason;
      value = cached;
    }
    setMe(value);
    setDisplay(value.displayName);
    setBio(value.bio);
  }
  useEffect(() => {
    void load().catch(() => setError('プロフィールを取得できませんでした。もう一度ログインしてください。'));
    void getSecretKey().then((value) => { if (value) setSecretKey(value); });
  }, []);
  if (!me) return <p role="status">{error || 'プロフィールを読み込んでいます…'}</p>;
  async function save(event: React.FormEvent) {
    event.preventDefault();
    try {
      setMe(await updateMe({ displayName: display, bio }));
      if (image) { await putProfileImage(image); setImage(null); }
      await load();
      setError('');
    } catch { setError('保存できませんでした。'); }
  }
  async function downloadPosts() {
    if (exporting) return;
    setExporting(true);
    setExportError('');
    setExportStatus('');
    try {
      const blob = await exportPosts();
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = 'isureal-posts.zip';
      document.body.appendChild(link);
      try { link.click(); }
      finally {
        link.remove();
        window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
      }
      setExportStatus('ダウンロードを開始しました。');
    } catch (reason) {
      setExportError(reason instanceof APIError && reason.status === 403
        ? '接続先の地域を確認してください。'
        : 'エクスポートできませんでした。もう一度お試しください。');
    } finally { setExporting(false); }
  }
  async function removeAccount() {
    if (!window.confirm('退会しますか？このアカウントは復元できず、同じユーザー名で再登録できません。')) return;
    try { await requestAccountDeletion(); await clearToken(); navigate('/login'); }
    catch { setError('退会できませんでした。接続先の地域を確認してください。'); }
  }
  return (
    <section>
      <h1>プロフィール設定</h1>{error && <p role="alert">{error}</p>}
      {secretKey && <SecretKeyPanel secretKey={secretKey} />}
      <form onSubmit={(event) => void save(event)}>
        <label>表示名<input required maxLength={100} value={display} onChange={(event) => setDisplay(event.target.value)} /></label>
        <label>自己紹介<textarea maxLength={500} value={bio} onChange={(event) => setBio(event.target.value)} /></label>
        <label>プロフィール画像<input type="file" accept="image/jpeg" onChange={(event) => setImage(event.target.files?.[0] ?? null)} /></label>
        <button>保存</button>
      </form>
      {me.profileImageUrl && <button onClick={() => void deleteProfileImage().then(load).catch(() => setError('画像を削除できませんでした。'))}>画像を削除</button>}
      <RegionSettings me={me} onChange={setMe} />
      <section aria-labelledby="export-heading">
        <h2 id="export-heading">投稿のエクスポート</h2>
        <p>全投稿の画像と投稿情報（JSON）を ZIP ファイルでダウンロードできます。削除済みの投稿は含まれません。</p>
        <button disabled={exporting} onClick={() => void downloadPosts()}>
          {exporting ? 'エクスポート中…' : '全投稿をダウンロード'}
        </button>
        <p role="status">{exporting ? 'ZIP ファイルを準備しています。投稿数によって時間がかかります。' : exportStatus}</p>
        {exportError && <p role="alert">{exportError}</p>}
      </section>
      <button className="danger" onClick={() => void removeAccount()}>退会する</button>
    </section>
  );
}
