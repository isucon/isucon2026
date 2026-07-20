import { useState } from 'react';
import { updateMe, type Me } from '../../api/account';

const labels = { america: 'アメリカ', europe: 'ヨーロッパ', asia: 'アジア' } as const;

export function RegionSettings({ me, onChange }: { me: Me; onChange: (me: Me) => void }): React.JSX.Element {
  const [region, setRegion] = useState<Me['region']>(me.pendingRegion as Me['region'] ?? me.region);
  const [message, setMessage] = useState('');
  async function save(next: Me['region']): Promise<void> {
    const updated = await updateMe({ region: next });
    onChange(updated);
    setRegion(updated.pendingRegion as Me['region'] ?? updated.region);
    setMessage(updated.pendingRegion ? '次の投稿時間から地域を変更します' : '地域を変更しました');
  }
  async function cancel(): Promise<void> {
    await save(me.region);
    setMessage('予約した地域変更を取り消しました');
  }
  return <section aria-labelledby="region-settings"><h2 id="region-settings">地域設定</h2>
    <p>現在の地域: {labels[me.region]}</p>{me.pendingRegion && <p>次回の地域: {labels[me.pendingRegion as Me['region']]}</p>}
    <label>地域<select value={region} onChange={(event) => setRegion(event.target.value as Me['region'])}>{Object.entries(labels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
    <button type="button" onClick={() => void save(region)}>地域を変更</button>{me.pendingRegion && <button type="button" onClick={() => void cancel()}>予約を取り消す</button>}
    {message && <p role="status">{message}</p>}
  </section>;
}
