import { type ReactNode, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";

export function Brand() {
  return (
    <Link to="/" className="brand" aria-label="ISUReal ホーム">
      <img src="/isureal-logo.svg" alt="ISUReal" />
    </Link>
  );
}
export function Icon({ name }: { name: string }) {
  const paths: Record<string, ReactNode> = {
    home: (
      <>
        <path d="m3 10 9-7 9 7v11h-6v-7H9v7H3z" />
      </>
    ),
    people: (
      <>
        <circle cx="9" cy="7" r="3" />
        <path d="M3 21v-4a6 6 0 0 1 12 0v4M16 4a3 3 0 0 1 0 6m2 3a5 5 0 0 1 3 4v4" />
      </>
    ),
    camera: (
      <>
        <path d="M3 6h4l2-3h6l2 3h4v15H3z" />
        <circle cx="12" cy="13" r="4" />
      </>
    ),
    calendar: (
      <>
        <rect x="3" y="5" width="18" height="16" rx="2" />
        <path d="M7 2v6m10-6v6M3 11h18m-13 4h2m4 0h2" />
      </>
    ),
    bell: (
      <>
        <path d="M5 17h14l-2-3V9a5 5 0 0 0-10 0v5zm5 4h4" />
      </>
    ),
    lock: (
      <>
        <rect x="5" y="10" width="14" height="11" rx="2" />
        <path d="M8 10V6a4 4 0 0 1 8 0v4m-4 5v2" />
      </>
    ),
    check: (
      <>
        <circle cx="12" cy="12" r="9" />
        <path d="m7 12 3 3 7-7" />
      </>
    ),
    search: (
      <>
        <circle cx="10" cy="10" r="6" />
        <path d="m15 15 6 6" />
      </>
    ),
  };
  return (
    <svg
      width="24"
      height="24"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name] ?? paths.camera}
    </svg>
  );
}
export function StateView(
  { title, detail, icon = "camera", children, busy = false }: {
    title: string;
    detail?: string;
    icon?: string;
    children?: ReactNode;
    busy?: boolean;
  },
) {
  return (
    <div className="state-view" aria-busy={busy}>
      <span className={busy ? "spinner" : "state-icon"}>
        {!busy && <Icon name={icon} />}
      </span>
      <h2>{title}</h2>
      {detail && <p>{detail}</p>}
      <div className="state-actions">{children}</div>
    </div>
  );
}
export function Avatar({ name, src }: { name: string; src?: string | null }) {
  return src
    ? <img className="avatar" src={src} alt={`${name}のプロフィール画像`} />
    : <span className="avatar" aria-hidden="true">{name.slice(0, 1)}</span>;
}
export function ConfirmButton(
  { children, title, detail, onConfirm, className = "" }: {
    children: ReactNode;
    title: string;
    detail: string;
    onConfirm: () => Promise<unknown>;
    className?: string;
  },
) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function confirm() {
    setBusy(true);
    setError("");
    try {
      await onConfirm();
      dialog.current?.close();
    } catch {
      setError("操作できませんでした。もう一度お試しください。");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <button
        type="button"
        className={className}
        onClick={() => {
          setError("");
          dialog.current?.showModal();
        }}
      >
        {children}
      </button>
      <dialog
        ref={dialog}
        className="confirm-dialog"
        aria-label={title}
        onCancel={(event) => {
          if (busy) event.preventDefault();
        }}
      >
        <h2>{title}</h2>
        <p>{detail}</p>
        {error && <p role="alert">{error}</p>}
        <div className="stack">
          <button type="button" disabled={busy} onClick={() => void confirm()}>
            {busy ? "処理中…" : children}
          </button>
          <button
            type="button"
            className="secondary"
            disabled={busy}
            onClick={() => dialog.current?.close()}
          >
            キャンセル
          </button>
        </div>
      </dialog>
    </>
  );
}
export function useObjectURL(file: Blob | null) {
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    if (!file) {
      setUrl(null);
      return;
    }
    const next = URL.createObjectURL(file);
    setUrl(next);
    return () => URL.revokeObjectURL(next);
  }, [file]);
  return url;
}
