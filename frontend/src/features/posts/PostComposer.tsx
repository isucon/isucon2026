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
  const [coordinates, setCoordinates] = useState<{ latitude: number; longitude: number } | null>(null);
  const [locationState, setLocationState] = useState<'idle' | 'requesting' | 'ready' | 'unavailable' | 'denied'>('idle');
  const locationRequest = useRef(0);
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

  function chooseLocationKind(kind: Location['kind']): void {
    locationRequest.current += 1;
    setLocationKind(kind);
    setCoordinates(null);
    setLocationState('idle');
    if (kind === 'exact') requestLocation();
  }

  function requestLocation(): void {
    const request = ++locationRequest.current;
    setLocationState('requesting');
    setCoordinates(null);
    setError('');
    if (!navigator.geolocation) { setLocationState('unavailable'); return; }
    navigator.geolocation.getCurrentPosition(
      (position) => {
        if (request !== locationRequest.current) return;
        setCoordinates({ latitude: position.coords.latitude, longitude: position.coords.longitude });
        setLocationState('ready');
      },
      (locationError) => {
        if (request !== locationRequest.current) return;
        setLocationState(locationError.code === locationError.PERMISSION_DENIED ? 'denied' : 'unavailable');
      },
      { enableHighAccuracy: true, maximumAge: 0, timeout: 10_000 },
    );
  }

  function location(): Location {
    if (locationKind === 'exact' && coordinates) return { kind: 'exact', ...coordinates };
    if (locationKind === 'approximate') return { kind: 'approximate', locality };
    return { kind: 'none' };
  }

  async function submit(event: React.FormEvent): Promise<void> {
    event.preventDefault();
    if (!image) { setError('写真を撮影するか画像ファイルを選択してください。'); return; }
    if (locationKind === 'exact' && !coordinates) { setError('GPSから位置情報を取得してください。'); return; }
    setSubmitting(true); setError('');
    try {
      await createPost({ image, caption, visibility, location: location() });
      navigate('/posts/me/current');
    } catch { setError('投稿できませんでした。入力内容と投稿可能期間を確認してください。'); }
    finally { setSubmitting(false); }
  }

  return <section className="panel"><h1>今日の一枚を投稿</h1><form onSubmit={(event) => { void submit(event); }}>
    <fieldset><legend>投稿写真</legend>
      <label>画像ファイル<input type="file" accept="image/jpeg" onChange={event => { cameraRequest.current += 1; stopCamera(); setImage(event.target.files?.[0] ?? null); setCameraState('captured'); }} /></label>
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
      <label><input type="radio" name="location" checked={locationKind === 'none'} onChange={() => chooseLocationKind('none')} />共有しない</label>
      <label><input type="radio" name="location" checked={locationKind === 'exact'} onChange={() => chooseLocationKind('exact')} />GPSで正確な位置を共有</label>
      {locationKind === 'exact' && <div>
        {locationState === 'requesting' && <p aria-live="polite">GPSから位置情報を取得しています…</p>}
        {locationState === 'ready' && coordinates && <p>GPS位置を取得しました（緯度 {coordinates.latitude.toFixed(6)}、経度 {coordinates.longitude.toFixed(6)}）</p>}
        {locationState === 'unavailable' && <p role="alert">GPS位置情報を取得できません。</p>}
        {locationState === 'denied' && <p role="alert">位置情報の利用が拒否されました。</p>}
        {(locationState === 'unavailable' || locationState === 'denied') && <button type="button" onClick={requestLocation}>GPSを再取得</button>}
      </div>}
      <label><input type="radio" name="location" checked={locationKind === 'approximate'} onChange={() => chooseLocationKind('approximate')} />おおよその場所</label>
      {locationKind === 'approximate' && <label>地域名<input required maxLength={100} value={locality} onChange={(event) => setLocality(event.target.value)} /></label>}
    </fieldset>
    {error && <p role="alert">{error}</p>}<button disabled={submitting} type="submit">{submitting ? '投稿中…' : '投稿する'}</button>
  </form></section>;
}
