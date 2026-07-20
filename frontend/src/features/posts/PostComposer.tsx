import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { createPost, type Location, type Visibility } from '../../api/posts';

export function PostComposer(): React.JSX.Element {
  const navigate = useNavigate();
  const [image, setImage] = useState<File | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [cameraState, setCameraState] = useState<'starting' | 'ready' | 'captured' | 'unavailable' | 'denied'>('starting');
  const video = useRef<HTMLVideoElement>(null);
  const stream = useRef<MediaStream | null>(null);
  const cameraRequest = useRef(0);
  const [caption, setCaption] = useState('');
  const [visibility, setVisibility] = useState<Visibility>('friends');
  const [locationKind, setLocationKind] = useState<Location['kind']>('none');
  const [latitude, setLatitude] = useState('');
  const [longitude, setLongitude] = useState('');
  const [locality, setLocality] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  function stopCamera(): void {
    stream.current?.getTracks().forEach((track) => track.stop());
    stream.current = null;
  }

  async function startCamera(): Promise<void> {
    const request = ++cameraRequest.current;
    stopCamera();
    setCameraState('starting');
    if (!navigator.mediaDevices?.getUserMedia) { setCameraState('unavailable'); return; }
    try {
      const nextStream = await navigator.mediaDevices.getUserMedia({
        video: { facingMode: { ideal: 'environment' } }, audio: false,
      });
      if (request !== cameraRequest.current) {
        nextStream.getTracks().forEach((track) => track.stop());
        return;
      }
      stream.current = nextStream;
      setImage(null);
      setCameraState('ready');
    } catch (cameraError) {
      if (request !== cameraRequest.current) return;
      setCameraState(cameraError instanceof DOMException && cameraError.name === 'NotAllowedError' ? 'denied' : 'unavailable');
    }
  }

  useEffect(() => {
    void startCamera();
    return () => { cameraRequest.current += 1; stopCamera(); };
  }, []);

  useEffect(() => {
    if (cameraState === 'ready' && video.current) video.current.srcObject = stream.current;
  }, [cameraState]);

  useEffect(() => {
    if (!image) { setPreview(null); return; }
    const url = URL.createObjectURL(image);
    setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [image]);

  async function capture(): Promise<void> {
    const source = video.current;
    if (!source) return;
    const canvas = document.createElement('canvas');
    canvas.width = source.videoWidth || 1280;
    canvas.height = source.videoHeight || 720;
    canvas.getContext('2d')?.drawImage(source, 0, 0, canvas.width, canvas.height);
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.9));
    if (!blob) { setError('写真を撮影できませんでした。'); return; }
    setImage(new File([blob], 'camera.jpg', { type: 'image/jpeg' }));
    stopCamera();
    setCameraState('captured');
  }

  function location(): Location {
    if (locationKind === 'exact') return { kind: 'exact', latitude: Number(latitude), longitude: Number(longitude) };
    if (locationKind === 'approximate') return { kind: 'approximate', locality };
    return { kind: 'none' };
  }

  async function submit(event: React.FormEvent): Promise<void> {
    event.preventDefault();
    if (!image) { setError('写真を撮影してください。'); return; }
    setSubmitting(true); setError('');
    try {
      await createPost({ image, caption, visibility, location: location() });
      navigate('/posts/me/current');
    } catch { setError('投稿できませんでした。入力内容と投稿可能期間を確認してください。'); }
    finally { setSubmitting(false); }
  }

  return <section className="panel"><h1>今日の一枚を投稿</h1><form onSubmit={(event) => { void submit(event); }}>
    <fieldset><legend>投稿写真</legend>
      {cameraState === 'starting' && <p aria-live="polite">カメラを起動しています…</p>}
      {cameraState === 'ready' && <><video className="post-preview" ref={video} autoPlay muted playsInline aria-label="投稿用カメラ" /><button type="button" onClick={() => { void capture(); }}>撮影する</button></>}
      {cameraState === 'unavailable' && <p role="alert">カメラを利用できません。</p>}
      {cameraState === 'denied' && <p role="alert">カメラ権限が拒否されました。</p>}
      {(cameraState === 'unavailable' || cameraState === 'denied' || cameraState === 'captured') && <button type="button" onClick={() => { void startCamera(); }}>カメラを再起動</button>}
    </fieldset>
    {preview && <img className="post-preview" src={preview} alt="投稿画像のプレビュー" />}
    <label>キャプション<textarea maxLength={1000} value={caption} onChange={(event) => setCaption(event.target.value)} /></label>
    <fieldset><legend>公開範囲</legend>
      <label><input type="radio" name="visibility" checked={visibility === 'friends'} onChange={() => setVisibility('friends')} />友達</label>
      <label><input type="radio" name="visibility" checked={visibility === 'friends_of_friends'} onChange={() => setVisibility('friends_of_friends')} />友達の友達</label>
      <label><input type="radio" name="visibility" checked={visibility === 'public'} onChange={() => setVisibility('public')} />公開</label>
    </fieldset>
    <fieldset><legend>位置情報</legend>
      <label><input type="radio" name="location" checked={locationKind === 'none'} onChange={() => setLocationKind('none')} />共有しない</label>
      <label><input type="radio" name="location" checked={locationKind === 'exact'} onChange={() => setLocationKind('exact')} />正確な位置</label>
      {locationKind === 'exact' && <div className="inline-fields"><label>緯度<input type="number" min="-90" max="90" step="any" required value={latitude} onChange={(event) => setLatitude(event.target.value)} /></label><label>経度<input type="number" min="-180" max="180" step="any" required value={longitude} onChange={(event) => setLongitude(event.target.value)} /></label></div>}
      <label><input type="radio" name="location" checked={locationKind === 'approximate'} onChange={() => setLocationKind('approximate')} />おおよその場所</label>
      {locationKind === 'approximate' && <label>地域名<input required maxLength={100} value={locality} onChange={(event) => setLocality(event.target.value)} /></label>}
    </fieldset>
    {error && <p role="alert">{error}</p>}<button disabled={submitting} type="submit">{submitting ? '投稿中…' : '投稿する'}</button>
  </form></section>;
}
