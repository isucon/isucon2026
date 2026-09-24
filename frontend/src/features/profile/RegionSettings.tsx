import { useState } from 'react';
import { updateMe, type Me } from '../../api/account';

const labels = { america: 'アメリカ', europe: 'ヨーロッパ', asia: 'アジア' } as const;
export function RegionSettings({ me, onChange }: { me: Me; onChange: (me: Me) => void }): React.JSX.Element {
  const [region, setRegion] = useState(me.region);
  const [message, setMessage] = useState('');
  async function save() {
    try { onChange(await updateMe({ region })); window.dispatchEvent(new Event('isureal:region-changed')); setMessage('地域を変更しました。選択した地域のサーバーからアクセスしてください。'); }
    catch { setMessage('地域を変更できませんでした。'); }
  }
  return <section><h2>地域設定</h2><p>現在の地域: {labels[me.region]}</p>
    <label>地域<select value={region} onChange={event => setRegion(event.target.value as Me['region'])}>{Object.entries(labels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
    <button type="button" onClick={() => void save()}>地域を変更</button>
    {message && <p role="status">{message}</p>}
  </section>;
}
