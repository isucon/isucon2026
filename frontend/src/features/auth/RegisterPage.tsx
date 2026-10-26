import { Brand } from "../../components/UI";
import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { login, register } from "../../api/account";
import { SecretKeyPanel } from "./SecretKeyPanel";

export function RegisterPage(): React.JSX.Element {
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [region, setRegion] = useState("asia");
  const [error, setError] = useState("");
  const [secretKey, setSecretKey] = useState("");
  const navigate = useNavigate();
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    try {
      const result = await register({ username, displayName, region });
      setSecretKey(result.secretKey);
    } catch {
      setError("登録できませんでした。入力内容を確認してください。");
    }
  }
  if (secretKey) {
    return (
      <main className="auth-page">
        <Brand />
        <SecretKeyPanel secretKey={secretKey} />
        {error && <p role="alert">{error}</p>}
        <button
          type="button"
          onClick={() =>
            void login(username, secretKey).then(() => navigate("/")).catch(
              () => setError("ログインできませんでした。再度お試しください。"),
            )}
        >
          はじめる
        </button>
      </main>
    );
  }
  return (
    <main className="auth-page">
      <Brand />
      <form onSubmit={(event) => void submit(event)}>
        <h1>新規登録</h1>
        <label>
          ユーザー名<input
            required
            maxLength={64}
            pattern="[a-z0-9_.\-]+"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
          />
        </label>
        <label>
          表示名<input
            required
            maxLength={100}
            value={displayName}
            onChange={(event) => setDisplayName(event.target.value)}
          />
        </label>
        <label>
          地域<select
            value={region}
            onChange={(event) => setRegion(event.target.value)}
          >
            <option value="america">アメリカ</option>
            <option value="europe">ヨーロッパ</option>
            <option value="asia">アジア</option>
          </select>
        </label>
        {error && <p role="alert">{error}</p>}
        <button>登録</button>
        <Link to="/login">ログインへ</Link>
      </form>
    </main>
  );
}
