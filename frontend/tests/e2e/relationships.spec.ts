import { expect, test } from '@playwright/test';

test('検索から関係操作をキーボードで実行できる', async ({ page }) => {
  await page.route('**://*/api/**', async (route) => {
    const url = new URL(route.request().url());
    const user = { id: '10000000-0000-0000-0000-000000000001', username: 'bob', displayName: 'Bob', profileImageUrl: null, bio: 'public', accountType: 'certified_public' };
    if (url.pathname === '/api/users') { await route.fulfill({ json: { items: [user], hasMore: false } }); return; }
    if (url.pathname.includes('/users/')) { await route.fulfill({ json: user }); return; }
    await route.fulfill({ status: 204 });
  });
  await page.goto('/');
  await page.evaluate(async () => {
    const request = indexedDB.open('isureal-session', 1);
    await new Promise<void>((resolve, reject) => {
      request.onupgradeneeded = () => request.result.createObjectStore('credentials');
      request.onsuccess = () => { const transaction = request.result.transaction('credentials', 'readwrite'); transaction.objectStore('credentials').put('token', 'user-token'); transaction.oncomplete = () => resolve(); transaction.onerror = () => reject(transaction.error); };
      request.onerror = () => reject(request.error);
    });
  });
  await page.goto('/users');
  await page.getByLabel('ユーザー名または表示名').fill('bob');
  await page.getByRole('button', { name: '検索' }).press('Enter');
  await page.getByRole('link', { name: /Bob/ }).press('Enter');
  await expect(page.getByRole('heading', { name: 'Bob' })).toBeVisible();
  await page.getByRole('button', { name: '友達申請' }).press('Enter');
  await page.getByRole('button', { name: 'フォロー', exact: true }).press('Enter');
});
