import { Link, Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom';
import { FeedPage } from '../features/feeds/FeedPage';
import { MyCurrentPostPage } from '../features/posts/MyCurrentPostPage';
import { PostComposer } from '../features/posts/PostComposer';
import { PostDetail } from '../features/posts/PostDetail';
import { RelationshipPage } from '../features/relationships/RelationshipPage';
import { ProfilePage } from '../features/users/ProfilePage';
import { UserSearchPage } from '../features/users/UserSearchPage';
import { LogoutButton } from './LogoutButton';
import { useSession } from './auth';
import { LoginPage } from '../features/auth/LoginPage';
import { RegisterPage } from '../features/auth/RegisterPage';
import { ProfileSettingsPage } from '../features/profile/ProfileSettingsPage';
import { MemoriesPage } from '../features/memories/MemoriesPage';
import { NotificationPage } from '../features/notifications/NotificationPage';

function ProtectedLayout(): React.JSX.Element {
  const session = useSession();
  const location = useLocation();
  if (session.loading) return <main aria-busy="true"><p>セッションを確認しています…</p></main>;
  if (!session.authenticated) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <div className="app-shell"><header><Link to="/" className="brand">ISUReal</Link><nav aria-label="メインナビゲーション">
    <Link to="/">ホーム</Link><Link to="/posts/new">投稿</Link><Link to="/posts/me/current">自分の投稿</Link>
    <Link to="/feeds/friends">友達</Link><Link to="/feeds/friends-of-friends">友達の友達</Link><Link to="/feeds/public">公開</Link><Link to="/feeds/following">フォロー中</Link>
    <Link to="/users">ユーザー検索</Link><Link to="/relationships/friends">関係一覧</Link>
    <Link to="/memories">Memories</Link><Link to="/notifications">通知</Link><Link to="/settings/profile">設定</Link>
  </nav><LogoutButton /></header><main id="main-content" tabIndex={-1}><Outlet /></main></div>;
}

function HomePage(): React.JSX.Element { return <><h1>ホーム</h1><p>今日の一枚を共有しましょう。</p><Link to="/posts/new">投稿画面へ</Link></>; }

export function AppRoutes(): React.JSX.Element {
  return <Routes><Route path="/login" element={<LoginPage />} /><Route path="/register" element={<RegisterPage />} /><Route element={<ProtectedLayout />}>
    <Route index element={<HomePage />} /><Route path="posts/new" element={<PostComposer />} /><Route path="posts/me/current" element={<MyCurrentPostPage />} />
    <Route path="posts/:postId" element={<PostDetail />} />
	<Route path="memories/posts/:postId" element={<PostDetail readOnly />} />
	<Route path="memories" element={<MemoriesPage />} /><Route path="notifications" element={<NotificationPage />} /><Route path="settings/profile" element={<ProfileSettingsPage />} />
    <Route path="feeds/friends" element={<FeedPage feed="friends" />} /><Route path="feeds/friends-of-friends" element={<FeedPage feed="friends-of-friends" />} />
    <Route path="feeds/public" element={<FeedPage feed="public" />} /><Route path="feeds/following" element={<FeedPage feed="following" />} />
    <Route path="users" element={<UserSearchPage />} /><Route path="users/:id" element={<ProfilePage />} />
    {['friends', 'following', 'followers', 'incoming-requests', 'outgoing-requests'].map((kind) => <Route key={kind} path={`relationships/${kind}`} element={<RelationshipPage kind={kind} />} />)}
  </Route><Route path="*" element={<Navigate to="/" replace />} /></Routes>;
}
