import { useState } from "react";
import {
  createFriendRequest,
  relationshipMutation,
} from "../../api/relationships";
import { ConfirmButton } from "../../components/UI";
export function RelationshipActions(
  { userId, certified, onChanged, onlyUnfriend = false }: {
    userId: string;
    certified: boolean;
    onChanged: () => void;
    onlyUnfriend?: boolean;
  },
) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await action();
      setMessage("更新しました。");
      onChanged();
    } catch {
      setError("操作できませんでした。");
    } finally {
      setBusy(false);
    }
  }
  const unfriend = (
    <ConfirmButton
      className="secondary"
      title="友達解除"
      detail="この友達との関係を解除しますか？"
      onConfirm={async () => {
        await relationshipMutation("friendships", userId, "DELETE");
        onChanged();
      }}
    >
      友達解除
    </ConfirmButton>
  );
  if (onlyUnfriend) return unfriend;
  return (
    <div className="stack">
      {certified && (
        <button
          disabled={busy}
          onClick={() =>
            void run(() => relationshipMutation("follows", userId, "PUT"))}
        >
          フォロー
        </button>
      )}
      <button
        disabled={busy}
        onClick={() => void run(() => createFriendRequest(userId))}
      >
        友達申請
      </button>
      {unfriend}
      {certified && (
        <ConfirmButton
          className="secondary"
          title="フォロー解除"
          detail="このアカウントのフォローを解除しますか？"
          onConfirm={async () => {
            await relationshipMutation("follows", userId, "DELETE");
            onChanged();
          }}
        >
          フォロー解除
        </ConfirmButton>
      )}
      <ConfirmButton
        className="secondary"
        title="ブロック"
        detail="関係をすべて解除してブロックしますか？"
        onConfirm={async () => {
          await relationshipMutation("blocks", userId, "PUT");
          onChanged();
        }}
      >
        ブロック
      </ConfirmButton>
      <ConfirmButton
        className="secondary"
        title="ブロック解除"
        detail="ブロックを解除しますか？"
        onConfirm={async () => {
          await relationshipMutation("blocks", userId, "DELETE");
          onChanged();
        }}
      >
        ブロック解除
      </ConfirmButton>
      {error && <p role="alert">{error}</p>}
      {message && <p role="status">{message}</p>}
    </div>
  );
}
