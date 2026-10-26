import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { getUser, type UserSummary } from "../../api/relationships";
import { RelationshipActions } from "../relationships/RelationshipActions";
import { Avatar, StateView } from "../../components/UI";
export function ProfilePage() {
  const { id = "" } = useParams();
  const [user, setUser] = useState<UserSummary | null>(null);
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    setError("");
    void getUser(id).then((value) => {
      if (active) setUser(value);
    }).catch(() => {
      if (active) setError("プロフィールを取得できませんでした。");
    });
    return () => {
      active = false;
    };
  }, [id, version]);
  if (error) {
    return (
      <StateView title={error}>
        <button onClick={() => setVersion((value) => value + 1)}>
          再読み込み
        </button>
      </StateView>
    );
  }
  if (!user) return <StateView busy title="プロフィールを読み込んでいます…" />;
  return (
    <section className="panel">
      <Link className="back-link" to="/users">‹ ユーザー検索</Link>
      <h1>{user.displayName}</h1>
      {user.accountType === "certified_public" && (
        <p className="badge">認定公開アカウント</p>
      )}
      <div className="profile-avatar">
        <Avatar name={user.displayName} src={user.profileImageUrl} />
      </div>
      <p>@{user.username}</p>
      {user.bio && <p>{user.bio}</p>}
      <RelationshipActions
        userId={user.id}
        certified={user.accountType === "certified_public"}
        onChanged={() => setVersion((value) => value + 1)}
      />
    </section>
  );
}
