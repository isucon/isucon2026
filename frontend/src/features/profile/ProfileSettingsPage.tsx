import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  cachedMe,
  deleteProfileImage,
  exportPosts,
  getMe,
  type Me,
  putProfileImage,
  requestAccountDeletion,
  updateMe,
} from "../../api/account";
import { APIError } from "../../api/client";
import { clearToken, getSecretKey } from "../../app/auth";
import { LogoutButton } from "../../app/LogoutButton";
import { SecretKeyPanel } from "../auth/SecretKeyPanel";
import { RegionSettings } from "./RegionSettings";
import {
  Avatar,
  ConfirmButton,
  StateView,
  useObjectURL,
} from "../../components/UI";
export type SettingsView =
  | "profile"
  | "menu"
  | "account"
  | "region"
  | "secret"
  | "export"
  | "delete";
export function ProfileSettingsPage(
  { view = "profile" }: { view?: SettingsView },
) {
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState("");
  const [exportStatus, setExportStatus] = useState("");
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);
  const [me, setMe] = useState<Me | null>(null);
  const [display, setDisplay] = useState("");
  const [bio, setBio] = useState("");
  const [image, setImage] = useState<File | null>(null);
  const preview = useObjectURL(image);
  const [secretKey, setSecretKey] = useState("");
  const navigate = useNavigate();
  async function load() {
    let value: Me;
    try {
      value = await getMe();
    } catch (reason) {
      const cached = cachedMe();
      if (!(reason instanceof APIError && reason.status === 403 && cached)) {
        throw reason;
      }
      value = cached;
    }
    setMe(value);
    setDisplay(value.displayName);
    setBio(value.bio);
    setError("");
  }
  useEffect(() => {
    void load().catch(() =>
      setError("プロフィールを取得できませんでした。もう一度お試しください。")
    );
    void getSecretKey().then((value) => {
      if (value) setSecretKey(value);
    });
  }, []);
  async function save(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setSaved("");
    try {
      setMe(await updateMe({ displayName: display, bio }));
      if (image) {
        await putProfileImage(image);
        setImage(null);
      }
      await load();
      setSaved("保存しました。");
    } catch {
      setError("保存できませんでした。");
    } finally {
      setBusy(false);
    }
  }
  async function downloadPosts() {
    if (exporting) return;
    setExporting(true);
    setExportError("");
    setExportStatus("");
    try {
      const blob = await exportPosts();
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = "isureal-posts.zip";
      document.body.appendChild(link);
      try {
        link.click();
      } finally {
        link.remove();
        window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
      }
      setExportStatus("ダウンロードを開始しました。");
    } catch (reason) {
      setExportError(
        reason instanceof APIError && reason.status === 403
          ? "接続先の地域を確認してください。"
          : "エクスポートできませんでした。もう一度お試しください。",
      );
    } finally {
      setExporting(false);
    }
  }
  if (!me) {
    return (
      <StateView
        busy={!error}
        title={error || "プロフィールを読み込んでいます…"}
      >
        {error && (
          <button
            onClick={() =>
              void load().catch(() =>
                setError("プロフィールを取得できませんでした。")
              )}
          >
            再読み込み
          </button>
        )}
      </StateView>
    );
  }
  const titles: Record<SettingsView, string> = {
    profile: "プロフィール設定",
    menu: "プロフィールメニュー",
    account: "アカウント・データ",
    region: "地域設定",
    secret: "SecretKeyの確認",
    export: "投稿のエクスポート",
    delete: "退会の確認",
  };
  return (
    <section className="panel">
      <Link className="back-link" to={view === "menu" ? "/" : "/settings"}>
        ‹ {view === "menu" ? "ホーム" : "プロフィールメニュー"}
      </Link>
      <h1>{titles[view]}</h1>
      {error && <p role="alert">{error}</p>}
      {view === "profile" && (
        <>
          <div className="profile-avatar">
            <Avatar
              src={preview || me.profileImageUrl}
              name={display || me.displayName}
            />
          </div>
          <form onSubmit={(event) => void save(event)}>
            <label>
              プロフィール画像<input
                type="file"
                accept="image/jpeg"
                onChange={(event) => setImage(event.target.files?.[0] ?? null)}
              />
            </label>
            <label>
              表示名<input
                required
                maxLength={100}
                value={display}
                onChange={(event) => setDisplay(event.target.value)}
              />
            </label>
            <label>
              自己紹介<textarea
                maxLength={500}
                value={bio}
                onChange={(event) => setBio(event.target.value)}
              />
            </label>
            <button disabled={busy}>{busy ? "保存中…" : "保存"}</button>
          </form>
          {me.profileImageUrl && (
            <button
              className="secondary"
              onClick={() =>
                void deleteProfileImage().then(load).catch(() =>
                  setError("画像を削除できませんでした。")
                )}
            >
              画像を削除
            </button>
          )}
          {saved && <p role="status">{saved}</p>}
        </>
      )}
      {view === "menu" && (
        <>
          <div className="menu-list">
            <Link to="/posts/me/current">
              自分の投稿 <span>›</span>
            </Link>
            <Link to="/settings/profile">
              プロフィール設定 <span>›</span>
            </Link>
            <Link to="/users">
              ユーザー検索 <span>›</span>
            </Link>
            <Link to="/settings/account">
              アカウント・データ <span>›</span>
            </Link>
          </div>
          <LogoutButton />
        </>
      )}
      {view === "account" && (
        <div className="menu-list">
          <Link to="/settings/secret">
            SecretKey <span>›</span>
          </Link>
          <Link to="/settings/region">
            地域設定 <span>›</span>
          </Link>
          <Link to="/settings/export">
            投稿のエクスポート <span>›</span>
          </Link>
          <Link to="/settings/delete">
            退会する <span>›</span>
          </Link>
        </div>
      )}
      {view === "region" && <RegionSettings me={me} onChange={setMe} />}
      {view === "secret" && (secretKey
        ? <SecretKeyPanel secretKey={secretKey} />
        : <p>このブラウザにはSecretKeyが保存されていません。</p>)}
      {view === "export" && (
        <>
          <p>
            全投稿の画像と投稿情報（JSON）を ZIP
            ファイルでダウンロードできます。削除済みの投稿は含まれません。
          </p>
          <button
            disabled={exporting}
            onClick={() => void downloadPosts()}
          >
            {exporting ? "エクスポート中…" : "全投稿をダウンロード"}
          </button>
          <p role="status">
            {exporting
              ? "ZIP ファイルを準備しています。投稿数によって時間がかかります。"
              : exportStatus}
          </p>
          {exportError && <p role="alert">{exportError}</p>}
        </>
      )}
      {view === "delete" && (
        <>
          <p>このアカウントは復元できず、同じユーザー名で再登録できません。</p>
          <ConfirmButton
            title="退会の確認"
            detail="すべてのデータが削除されます。退会しますか？"
            onConfirm={async () => {
              await requestAccountDeletion();
              await clearToken();
              navigate("/login");
            }}
          >
            退会する
          </ConfirmButton>
        </>
      )}
    </section>
  );
}
