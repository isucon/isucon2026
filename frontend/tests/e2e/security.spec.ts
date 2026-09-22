import { expect, test } from '@playwright/test';

test('tokenをIndexedDBだけに保持し失効・削除・logoutで消去する', async ({ page }) => {
  const token = 'SECURITY_TOKEN_MUST_NOT_LEAK';
  const messages: string[] = [];
  page.on('console', (message) => messages.push(message.text()));
  page.on('pageerror', (error) => messages.push(error.message));
  let notificationsUnauthorized = false;
  const me = { id: '10000000-0000-0000-0000-000000000001', username: 'alice', displayName: 'Alice', profileImageUrl: null, bio: '', region: 'asia', pendingRegion: null, accountType: 'regular', status: 'active' };
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === '/api/v1/auth/login') return route.fulfill({ json: { token, user: me } });
    if (path === '/api/v1/auth/logout') return route.fulfill({ status: 204 });
    if (path === '/api/v1/me' && request.method() === 'GET') return route.fulfill({ json: me });
    if (path === '/api/v1/me/account-deletion') return route.fulfill({ status: 202, json: { purgeDueAt: '2026-08-18T00:00:00Z' } });
    if (path === '/api/v1/notifications' && notificationsUnauthorized) return route.fulfill({ status: 401, json: { code: 'invalid_session' } });
    if (path === '/api/v1/notifications') return route.fulfill({ json: { items: [], nextCursor: null } });
    return route.fulfill({ status: 204 });
  });

  const login = async () => {
    await page.goto('/login');
    await page.getByLabel('ユーザー名').fill('alice');
    await page.getByLabel('SecretKey').fill('password-1');
    await page.getByRole('button', { name: 'ログイン' }).click();
    await expect(page).toHaveURL('/');
  };
  const storedToken = () => page.evaluate(async () => {
    const database = await new Promise<IDBDatabase>((resolve, reject) => { const request = indexedDB.open('isureal-session', 1); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error); });
    return new Promise<unknown>((resolve) => { const transaction = database.transaction('credentials', 'readonly'); const request = transaction.objectStore('credentials').get('user-token'); request.onsuccess = () => resolve(request.result); });
  });

  const response = await page.goto('/login');
  expect(response?.headers()['content-security-policy']).toContain("base-uri 'none'");
  expect(response?.headers()['permissions-policy']).toBe('camera=(self)');
  await login();
  expect(await storedToken()).toBe(token);
  expect(await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length, cookie: document.cookie, html: document.documentElement.outerHTML, url: location.href }))).toEqual(expect.objectContaining({ local: 0, session: 0, cookie: '' }));
  expect(await page.locator('html').textContent()).not.toContain(token);
  expect(page.url()).not.toContain(token);
  expect(messages.join('\n')).not.toContain(token);

  notificationsUnauthorized = true;
  await page.goto('/notifications');
  await expect(page).toHaveURL('/login');
  expect(await storedToken()).toBeUndefined();

  notificationsUnauthorized = false;
  await login();
  await page.goto('/settings/profile');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'アカウント削除を申し込む' }).click();
  await expect(page).toHaveURL('/login');
  expect(await storedToken()).toBeUndefined();

  await login();
  await page.getByRole('button', { name: 'ログアウト' }).click();
  await expect(page).toHaveURL('/login');
  expect(await storedToken()).toBeUndefined();
});
