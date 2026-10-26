import { useState } from "react";
import { Link } from "react-router-dom";
import { searchUsers, type UserSummary } from "../../api/relationships";
import { Avatar, StateView } from "../../components/UI";
export function UserSearchPage() {
  const [q, setQ] = useState("");
  const [searched, setSearched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [items, setItems] = useState<UserSummary[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  async function search(next?: string) {
    setBusy(true);
    setError("");
    const text = next ? query : q;
    try {
      const page = await searchUsers(text, next);
      setQuery(text);
      setItems((old) => next ? [...old, ...page.items] : page.items);
      setCursor(page.nextPage);
      setSearched(true);
    } catch {
      setError("検索できませんでした。もう一度お試しください。");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h1>ユーザー検索</h1>
      <form
        role="search"
        onSubmit={(event) => {
          event.preventDefault();
          void search();
        }}
      >
        <label>
          ユーザー名または表示名<input
            required
            maxLength={100}
            value={q}
            onChange={(event) => setQ(event.target.value)}
          />
        </label>
        <button disabled={busy}>検索</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {busy && <p role="status">検索中…</p>}
      <ul className="user-list">
        {items.map((user) => (
          <li key={user.id}>
            <Avatar src={user.profileImageUrl} name={user.displayName} />
            <Link to={`/users/${user.id}`}>
              {user.displayName}
              <small>@{user.username}</small>
            </Link>
            <Link className="button secondary compact" to={`/users/${user.id}`}>
              開く
            </Link>
          </li>
        ))}
      </ul>
      {searched && !busy && !error && items.length === 0 && (
        <StateView
          icon="search"
          title="見つかりませんでした。"
          detail="別のユーザー名や表示名で検索してください。"
        />
      )}
      {cursor && (
        <button
          disabled={busy}
          className="secondary more"
          onClick={() => void search(cursor)}
        >
          さらに読み込む
        </button>
      )}
    </section>
  );
}
