import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { getPost, type Post } from "../../api/posts";
import { RealMojiPanel } from "../interactions/RealMojiPanel";
import { CommentPanel } from "../interactions/CommentPanel";
import { StateView } from "../../components/UI";
export function PostDetail(
  { mode = "detail" }: { mode?: "detail" | "comments" | "realmoji" },
) {
  const { postId = "" } = useParams();
  const [post, setPost] = useState<Post | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    setPost(null);
    setError("");
    void getPost(postId).then((value) => {
      if (active) setPost(value);
    }).catch(() => {
      if (active) {
        setError(
          "投稿を読み込めませんでした。閲覧できないか、削除された可能性があります。",
        );
      }
    });
    return () => {
      active = false;
    };
  }, [postId, retry]);
  if (error) {
    return (
      <StateView title={error}>
        <button onClick={() => setRetry((value) => value + 1)}>
          再読み込み
        </button>
        <Link to="/">ホームへ</Link>
      </StateView>
    );
  }
  if (!post) return <StateView busy title="投稿を読み込んでいます…" />;
  return (
    <section>
      <Link
        className="back-link"
        to={mode === "detail" ? "/" : `/posts/${postId}`}
      >
        ‹ {mode === "detail" ? "ホーム" : "投稿詳細"}
      </Link>
      {mode !== "realmoji" && (
        <h1>{mode === "detail" ? "投稿詳細" : "コメント"}</h1>
      )}
      {mode !== "realmoji" && (
        <article>
          <Link className="author-name" to={`/users/${post.author.id}`}>
            {post.author.displayName}
          </Link>
          <img className="post-image" src={post.imageUrl} alt="投稿画像" />
          <p>{post.caption}</p>
          <time className="muted" dateTime={post.postedAt}>
            {new Date(post.postedAt).toLocaleString("ja-JP")}
          </time>
        </article>
      )}
      {mode === "detail"
        ? (
          <div className="menu-list">
            <Link to={`/posts/${postId}/realmoji`}>
              RealMoji <span>›</span>
            </Link>
            <Link to={`/posts/${postId}/comments`}>
              コメント <span>›</span>
            </Link>
          </div>
        )
        : mode === "comments"
        ? <CommentPanel postId={post.id} />
        : <RealMojiPanel postId={post.id} />}
    </section>
  );
}
