import { useEffect, useState } from "react";
import {
  Link,
  Navigate,
  NavLink,
  Outlet,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import { NotificationProvider } from "../features/notifications/NotificationProvider";
import { FeedPage } from "../features/feeds/FeedPage";
import { MyCurrentPostPage } from "../features/posts/MyCurrentPostPage";
import { PostComposer } from "../features/posts/PostComposer";
import { PostDetail } from "../features/posts/PostDetail";
import { RelationshipPage } from "../features/relationships/RelationshipPage";
import { ProfilePage } from "../features/users/ProfilePage";
import { UserSearchPage } from "../features/users/UserSearchPage";
import { useSession } from "./auth";
import { cachedMe } from "../api/account";
import { LoginPage } from "../features/auth/LoginPage";
import { RegisterPage } from "../features/auth/RegisterPage";
import {
  ProfileSettingsPage,
  type SettingsView,
} from "../features/profile/ProfileSettingsPage";
import {
  MemoriesPage,
  MemoryDetailPage,
} from "../features/memories/MemoriesPage";
import { NotificationPage } from "../features/notifications/NotificationPage";
import { Brand, Icon, StateView } from "../components/UI";
function ProtectedLayout() {
  const session = useSession();
  const [regionMismatch, setRegionMismatch] = useState(false);
  const location = useLocation();
  useEffect(() => {
    const listener = () => setRegionMismatch(true);
    const changed = () => setRegionMismatch(false);
    window.addEventListener("isureal:region-mismatch", listener);
    window.addEventListener("isureal:region-changed", changed);
    return () => {
      window.removeEventListener("isureal:region-mismatch", listener);
      window.removeEventListener("isureal:region-changed", changed);
    };
  }, []);
  useEffect(() => {
    window.scrollTo(0, 0);
  }, [location.pathname]);
  if (session.loading) {
    return (
      <main>
        <StateView busy title="セッションを確認しています…" />
      </main>
    );
  }
  if (!session.authenticated) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  return (
    <NotificationProvider>
      <div className="app-shell">
        <a className="skip-link" href="#main-content">本文へ移動</a>
        <header className="app-header">
          <Link className="icon-link" to="/users" aria-label="ユーザー検索">
            <Icon name="people" />
          </Link>
          <Brand />
          <Link
            className="profile-link"
            to="/settings"
            aria-label="プロフィールメニュー"
          >
            {cachedMe()?.displayName?.slice(0, 1) || "•"}
          </Link>
        </header>
        <main id="main-content" tabIndex={-1}>
          {regionMismatch && (
            <aside className="region-alert" role="alert">
              <strong>登録した地域と接続先が異なります。</strong>
              <p>
                登録地域のサーバーでログインし直すか、このサーバーに合わせて地域設定を変更してください。
              </p>
              <Link to="/settings/region">地域設定を開く</Link>
              <Link to="/login">ログインへ</Link>
            </aside>
          )}
          <Outlet />
        </main>
        <nav className="bottom-nav" aria-label="メインナビゲーション">
          {[
            ["/", "home", "ホーム"],
            ["/relationships/friends", "people", "つながり"],
            ["/posts/new", "camera", "投稿"],
            ["/memories", "calendar", "Memories"],
            ["/notifications", "bell", "通知"],
          ].map(([to, icon, label]) => (
            <NavLink
              key={to}
              to={to}
              end={to === "/"}
              className={({ isActive }) =>
                [
                  icon === "camera" ? "compose-nav" : "",
                  isActive ||
                    (icon === "home" &&
                      location.pathname.startsWith("/feeds/")) ||
                    (icon === "people" &&
                      location.pathname.startsWith("/relationships/"))
                    ? "active"
                    : "",
                ].join(" ")}
            >
              <Icon name={icon} />
              <span>{label}</span>
            </NavLink>
          ))}
        </nav>
      </div>
    </NotificationProvider>
  );
}
function PostedPage() {
  return (
    <StateView
      icon="check"
      title="投稿しました。"
      detail="友達の一枚が、あなたを待っています。"
    >
      <Link className="button" to="/">友達の写真を見る</Link>
      <Link className="text-link" to="/posts/me/current">自分の投稿を確認</Link>
    </StateView>
  );
}
export function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/register" element={<RegisterPage />} />
      <Route element={<ProtectedLayout />}>
        <Route index element={<FeedPage feed="friends" />} />
        <Route path="posts/new" element={<PostComposer />} />
        <Route path="posts/complete" element={<PostedPage />} />
        <Route path="posts/me/current" element={<MyCurrentPostPage />} />
        <Route path="posts/:postId" element={<PostDetail />} />
        <Route
          path="posts/:postId/comments"
          element={<PostDetail mode="comments" />}
        />
        <Route
          path="posts/:postId/realmoji"
          element={<PostDetail mode="realmoji" />}
        />
        <Route path="memories" element={<MemoriesPage />} />
        <Route path="memories/posts/:postId" element={<MemoryDetailPage />} />
        <Route path="notifications" element={<NotificationPage />} />
        <Route path="settings" element={<ProfileSettingsPage view="menu" />} />
        {([
          "profile",
          "account",
          "region",
          "secret",
          "export",
          "delete",
        ] as SettingsView[]).map((view) => (
          <Route
            key={view}
            path={`settings/${view}`}
            element={<ProfileSettingsPage key={view} view={view} />}
          />
        ))}
        <Route path="feeds/friends" element={<Navigate to="/" replace />} />
        <Route
          path="feeds/friends-of-friends"
          element={<FeedPage feed="friends-of-friends" />}
        />
        <Route path="feeds/public" element={<FeedPage feed="public" />} />
        <Route path="feeds/following" element={<FeedPage feed="following" />} />
        <Route path="users" element={<UserSearchPage />} />
        <Route path="users/:id" element={<ProfilePage />} />
        {[
          "friends",
          "following",
          "followers",
          "incoming-requests",
          "outgoing-requests",
        ].map((kind) => (
          <Route
            key={kind}
            path={`relationships/${kind}`}
            element={<RelationshipPage kind={kind} />}
          />
        ))}
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
