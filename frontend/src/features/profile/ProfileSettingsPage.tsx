import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { deleteProfileImage, getMe, putProfileImage, requestAccountDeletion, updateMe, type Me } from '../../api/account';
import { clearToken, getSecretKey } from '../../app/auth';
import { SecretKeyPanel } from '../auth/SecretKeyPanel';
import { RegionSettings } from './RegionSettings';

export function ProfileSettingsPage(): React.JSX.Element {
  const [me, setMe] = useState<Me | null>(null);
  const [display, setDisplay] = useState('');
  const [bio, setBio] = useState('');
  const [image, setImage] = useState<File | null>(null);
  const [secretKey, setSecretKey] = useState('');
  const navigate = useNavigate();
  async function load() {
    const value = await getMe();
    setMe(value);
    setDisplay(value.displayName);
    setBio(value.bio);
  }
  useEffect(() => {
    void load();
    void getSecretKey().then((value) => { if (value) setSecretKey(value); });
  }, []);
  if (!me) return <p aria-busy="true">プロフィールを読み込んでいます…</p>;
  async function save(event: React.FormEvent) {
    event.preventDefault();
    await updateMe({ displayName: display, bio });
    if (image) await putProfileImage(image);
    await load();
  }
  async function removeAccount() {
    if (!window.confirm('アカウントを削除予定にしますか？')) return;
    await requestAccountDeletion();
    await clearToken();
    navigate('/login');
  }
  return (
    <section>
      <h1>プロフィール設定</h1>
      {secretKey && <SecretKeyPanel secretKey={secretKey} />}
      <form onSubmit={(event) => void save(event)}>
        <label>表示名<input required maxLength={100} value={display} onChange={(event) => setDisplay(event.target.value)} /></label>
        <label>自己紹介<textarea maxLength={500} value={bio} onChange={(event) => setBio(event.target.value)} /></label>
        <label>プロフィール画像<input type="file" accept="image/jpeg" onChange={(event) => setImage(event.target.files?.[0] ?? null)} /></label>
        <button>保存</button>
      </form>
      {me.profileImageUrl && <button onClick={() => void deleteProfileImage().then(load)}>画像を削除</button>}
      <RegionSettings me={me} onChange={setMe} />
      <button className="danger" onClick={() => void removeAccount()}>アカウント削除を申し込む</button>
    </section>
  );
}
