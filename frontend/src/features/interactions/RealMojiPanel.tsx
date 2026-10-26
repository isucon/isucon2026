import { useEffect, useRef, useState } from "react";
import {
  deleteRealMoji,
  listRealMojis,
  type ReactionType,
  type RealMoji,
  replaceRealMoji,
} from "../../api/interactions";
import { ConfirmButton, useObjectURL } from "../../components/UI";
export function RealMojiPanel(
  { postId, readOnly = false }: { postId: string; readOnly?: boolean },
) {
  const [error, setError] = useState("");
  const [items, setItems] = useState<RealMoji[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [state, setState] = useState<
    "idle" | "starting" | "ready" | "unavailable" | "denied"
  >("idle");
  const [reaction, setReaction] = useState<ReactionType>("like");
  const [image, setImage] = useState<Blob | null>(null);
  const preview = useObjectURL(image);
  const [busy, setBusy] = useState(false);
  const video = useRef<HTMLVideoElement>(null);
  const stream = useRef<MediaStream | null>(null);
  const request = useRef(0);
  function stop() {
    stream.current?.getTracks().forEach((track) => track.stop());
    stream.current = null;
  }
  async function load(next?: string) {
    try {
      const page = await listRealMojis(postId, next);
      setItems((old) => next ? [...old, ...page.items] : page.items);
      setCursor(page.nextPage);
    } catch {
      setError("RealMojiを取得できませんでした。");
    }
  }
  useEffect(() => {
    void load();
    return () => {
      request.current++;
      stop();
    };
  }, [postId]);
  useEffect(() => {
    if (state === "ready" && video.current) {
      video.current.srcObject = stream.current;
    }
  }, [state]);
  async function camera() {
    const id = ++request.current;
    stop();
    setImage(null);
    setError("");
    if (!navigator.mediaDevices?.getUserMedia) {
      setState("unavailable");
      return;
    }
    setState("starting");
    try {
      const next = await navigator.mediaDevices.getUserMedia({
        video: { facingMode: "user" },
        audio: false,
      });
      if (id !== request.current) {
        next.getTracks().forEach((track) => track.stop());
        return;
      }
      stream.current = next;
      setState("ready");
    } catch (reason) {
      if (id === request.current) {
        setState(
          reason instanceof DOMException && reason.name === "NotAllowedError"
            ? "denied"
            : "unavailable",
        );
      }
    }
  }
  async function capture() {
    const source = video.current;
    if (!source) return;
    const canvas = document.createElement("canvas");
    canvas.width = source.videoWidth || 640;
    canvas.height = source.videoHeight || 480;
    canvas.getContext("2d")?.drawImage(
      source,
      0,
      0,
      canvas.width,
      canvas.height,
    );
    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob(resolve, "image/jpeg", .9)
    );
    if (!blob) {
      setError("撮影できませんでした。");
      return;
    }
    setImage(blob);
    stop();
    setState("idle");
  }
  async function send() {
    if (!image || busy) return;
    setBusy(true);
    setError("");
    try {
      await replaceRealMoji(postId, reaction, image);
      setImage(null);
      await load();
    } catch {
      setError("RealMojiを送信できませんでした。もう一度お試しください。");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="interaction-panel">
      <h1>{preview ? "RealMojiの確認" : "RealMojiを作成"}</h1>
      {error && <p role="alert">{error}</p>}
      {!readOnly && (
        <div className="stack">
          <label>
            リアクション<select
              disabled={busy}
              value={reaction}
              onChange={(event) =>
                setReaction(event.target.value as ReactionType)}
            >
              {["like", "smile", "surprise", "heart", "laugh", "instant"].map(
                (value) => <option key={value}>{value}</option>,
              )}
            </select>
          </label>
          {preview
            ? (
              <>
                <img
                  className="post-preview"
                  src={preview}
                  alt="RealMojiのプレビュー"
                />
                <button disabled={busy} onClick={() => void send()}>
                  {busy ? "送信中…" : "送信"}
                </button>
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={() => {
                    setImage(null);
                    setError("");
                  }}
                >
                  撮り直す
                </button>
              </>
            )
            : (
              <>
                <label>
                  RealMoji画像<input
                    type="file"
                    accept="image/jpeg"
                    onChange={(event) => {
                      const file = event.target.files?.[0];
                      if (file) {
                        request.current++;
                        stop();
                        setState("idle");
                        setImage(file);
                        setError("");
                      }
                      event.target.value = "";
                    }}
                  />
                </label>
                <button
                  onClick={() => void camera()}
                  disabled={state === "starting"}
                >
                  {state === "starting"
                    ? "カメラを起動しています…"
                    : "カメラを開始"}
                </button>
                {state === "ready" && (
                  <>
                    <video
                      className="post-preview"
                      ref={video}
                      autoPlay
                      muted
                      playsInline
                      aria-label="RealMojiカメラ"
                    />
                    <button onClick={() => void capture()}>撮影する</button>
                  </>
                )}
                {state === "unavailable" && (
                  <p role="alert">
                    カメラを利用できません。画像ファイルを選択できます。
                  </p>
                )}
                {state === "denied" && (
                  <p role="alert">
                    カメラ権限が拒否されました。画像ファイルを選択できます。
                  </p>
                )}
              </>
            )}
        </div>
      )}
      <ul className="reaction-list">
        {items.map((item) => (
          <li key={item.id}>
            <img
              src={item.imageUrl}
              alt={`${item.reactor.displayName}のRealMoji`}
            />
            <span>{item.reactionType}</span>
            {!readOnly && (
              <ConfirmButton
                title="RealMojiの削除"
                detail="このRealMojiを削除しますか？"
                onConfirm={async () => {
                  await deleteRealMoji(item.id);
                  await load();
                }}
              >
                削除
              </ConfirmButton>
            )}
          </li>
        ))}
      </ul>
      {cursor && (
        <button
          className="secondary"
          onClick={() => void load(cursor)}
        >
          さらに読み込む
        </button>
      )}
    </section>
  );
}
