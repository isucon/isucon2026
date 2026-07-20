import { expect, test } from '@playwright/test';

test('通知センター、既読、遷移先、Push の有効化・解除を操作できる', async ({ page }) => {
  const notification = { id: '30000000-0000-0000-0000-000000000001', postingWindowId: '20000000-0000-0000-0000-000000000001', region: 'asia', createdAt: '2026-07-19T12:00:00Z', read: false };
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname === '/api/v1/auth/login') return route.fulfill({ json: { token: 'session-token', user: {} } });
    if (url.pathname === '/api/v1/notifications' && request.method() === 'GET') return route.fulfill({ json: { items: [notification], nextCursor: null } });
    if (url.pathname.endsWith(`/notifications/${notification.id}/open`)) return route.fulfill({ json: { destination: 'create-post', path: '/posts/new' } });
    return route.fulfill({ status: 204 });
  });
  await page.goto('/login');
  await page.getByLabel('ユーザー名').fill('alice');
  await page.getByLabel('パスワード').fill('password-1');
  await page.getByRole('button', { name: 'ログイン' }).click();
  await expect(page).toHaveURL('/');
  await page.goto('/notifications');
  await expect(page.getByText(/asia の投稿時間/)).toContainText('未読');
  await page.getByText(/asia の投稿時間/).click();
  await expect(page).toHaveURL('/posts/new');
  await page.goto(`/notifications?open=${notification.id}`);
  await expect(page).toHaveURL('/posts/new');
  await page.goto('/notifications');
  await page.getByRole('button', { name: 'Push 通知を無効にする' }).click();
  await expect(page.getByRole('status')).toHaveText('Push 通知を無効にしました');
  await page.getByRole('button', { name: 'Push 通知を有効にする' }).click();
  await expect(page.getByRole('status')).toContainText(/Push 通知|通知の許可/);
});
