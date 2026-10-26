import { expect, test } from '@playwright/test';
test('登録、セッション復元、プロフィール、Memories、削除要求を操作できる', async ({ page, context }) => {
  let deleted = false;
  const me = {
    id: '10000000-0000-0000-0000-000000000001',
    username: 'alice',
    displayName: 'Alice',
    profileImageUrl: null,
    bio: 'bio',
    region: 'asia',
    accountType: 'regular',
    status: 'active',
  };
  const post = {
    id: '20000000-0000-0000-0000-000000000001',
    author: me,
    postingWindowId: 'w',
    imageUrl: '/i',
    caption: 'memory',
    visibility: 'friends',
    location: { kind: 'none' },
    postedAt: '2026-07-19T12:00:00Z',
    onTime: true,
    lateSeconds: 0,
    comments: [],
    realmojis: [],
  };
  await context.route('**://*/api/**', async (route) => {
    const request = route.request(), url = new URL(request.url());
    if (
      url.pathname === '/api/auth/register' ||
      url.pathname === '/api/auth/login'
    ) {
      await route.fulfill({
        json: { secretKey: 'secret', token: 'session-token', user: me },
      });
      return;
    }
    if (url.pathname === '/api/me' && request.method() === 'GET') {
      await route.fulfill({ json: me });
      return;
    }
    if (url.pathname === '/api/me' && request.method() === 'PATCH') {
      await route.fulfill({ json: me });
      return;
    }
    if (url.pathname === '/api/memories') {
      await route.fulfill({
        json: { items: deleted ? [] : [post], hasMore: false },
      });
      return;
    }
    if (request.method() === 'DELETE' && url.pathname.includes('/posts/')) {
      deleted = true;
      await route.fulfill({ status: 204 });
      return;
    }
    if (url.pathname === '/api/me/account-deletion') {
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fulfill({ status: 204 });
  });
  await page.goto('/register');
  await page.getByLabel('ユーザー名').fill('alice');
  await page.getByLabel('表示名').fill('Alice');
  await page.getByRole('button', { name: '登録' }).click();
  await page.getByRole('button', { name: 'はじめる' }).click();
  await expect(page).toHaveURL('/');
  const reopened = await context.newPage();
  await reopened.goto('/');
  await expect(reopened.getByRole('heading', { name: 'My friendsフィード' }))
    .toBeVisible();
  await reopened.close();
  await page.goto('/settings/profile');
  await expect(page.getByRole('heading', { name: 'プロフィール設定' }))
    .toBeVisible();
  await expect(page.getByText(/パスワード変更|ユーザー名変更/)).toHaveCount(0);
  await page.getByLabel('プロフィール画像').setInputFiles({
    name: 'profile.jpg',
    mimeType: 'image/jpeg',
    buffer: Buffer.from('/9j/4AAQSkZJRgABAQAAAQABAAD/2Q==', 'base64'),
  });
  await page.getByRole('button', { name: '保存' }).click();
  await page.goto('/memories');
  await expect(page.getByText('memory')).toBeVisible();
  await page.getByRole('button', { name: '削除', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', {
    name: '削除',
    exact: true,
  }).click();
  await expect(page.getByText('memory')).toHaveCount(0);
  await page.goto('/settings/delete');
  await page.getByRole('button', { name: '退会する' }).click();
  await page.getByRole('dialog').getByRole('button', { name: '退会する' })
    .click();
  await expect(page).toHaveURL('/login');
});
