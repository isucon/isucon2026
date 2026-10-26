import { useEffect, useRef, useState } from "react";
import { Link, NavLink } from "react-router-dom";
import {
  listRelationships,
  type RelationshipItem,
  transitionFriendRequest,
} from "../../api/relationships";
import { RelationshipActions } from "./RelationshipActions";
import { Avatar, StateView } from "../../components/UI";
const labels: Record<string, string> = {
  friends: "友達",
  following: "フォロー中",
  followers: "フォロワー",
  "incoming-requests": "受信申請",
  "outgoing-requests": "送信申請",
};
export function RelationshipPage({ kind }: { kind: string }) {
  const [error, setError] = useState("");
  const [items, setItems] = useState<RelationshipItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [version, setVersion] = useState(0);
  const [busy, setBusy] = useState(false);
  const request = useRef(0);
  async function load(next?: string) {
    const id = ++request.current;
    setBusy(true);
    setError("");
    try {
      const page = await listRelationships(kind, next);
      if (id !== request.current) return;
      setItems((old) => next ? [...old, ...page.items] : page.items);
      setCursor(page.nextPage);
    } catch {
      if (id === request.current) setError("関係一覧を取得できませんでした。");
    } finally {
      if (id === request.current) setBusy(false);
    }
  }
  useEffect(() => {
    setItems([]);
    setCursor(null);
    void load();
    return () => {
      request.current++;
    };
  }, [kind, version]);
  async function decide(id: string, action: "accept" | "reject" | "cancel") {
    setBusy(true);
    try {
      await transitionFriendRequest(id, action);
      setVersion((value) => value + 1);
    } catch {
      setError("申請を更新できませんでした。");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h1>{labels[kind]}</h1>
      <nav className="tabs wrap-tabs" aria-label="関係一覧">
        {Object.entries(labels).map(([key, label]) => (
          <NavLink key={key} to={`/relationships/${key}`}>{label}</NavLink>
        ))}
      </nav>
      <Link className="text-link" to="/users">友達を探す</Link>
      {error && (
        <div role="alert">
          <p>{error}</p>
          <button onClick={() => void load()}>再読み込み</button>
        </div>
      )}
      <ul className="user-list">
        {items.map((item) => (
          <li key={item.relationId}>
            <Avatar
              name={item.user.displayName}
              src={item.user.profileImageUrl}
            />
            <Link to={`/users/${item.user.id}`}>
              {item.user.displayName}
              <small>@{item.user.username}</small>
            </Link>
            <div className="row-actions">
              {kind === "incoming-requests" && (
                <>
                  <button
                    disabled={busy}
                    onClick={() => void decide(item.relationId, "accept")}
                  >
                    承認
                  </button>
                  <button
                    disabled={busy}
                    className="secondary"
                    onClick={() => void decide(item.relationId, "reject")}
                  >
                    拒否
                  </button>
                </>
              )}
              {kind === "outgoing-requests" && (
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={() => void decide(item.relationId, "cancel")}
                >
                  取消
                </button>
              )}
              {kind === "friends"
                ? (
                  <RelationshipActions
                    onlyUnfriend
                    userId={item.user.id}
                    certified={item.user.accountType === "certified_public"}
                    onChanged={() => setVersion((value) => value + 1)}
                  />
                )
                : !kind.endsWith("requests") && (
                  <Link
                    className="button secondary compact"
                    to={`/users/${item.user.id}`}
                  >
                    開く
                  </Link>
                )}
            </div>
          </li>
        ))}
      </ul>
      {busy && <p role="status">読み込み中…</p>}
      {!busy && !error && items.length === 0 && (
        <StateView icon="people" title={`${labels[kind]}はまだありません。`} />
      )}
      {cursor && (
        <button
          disabled={busy}
          className="secondary more"
          onClick={() => void load(cursor)}
        >
          さらに読み込む
        </button>
      )}
    </section>
  );
}
