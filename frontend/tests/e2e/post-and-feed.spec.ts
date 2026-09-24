import { expect, test } from '@playwright/test';

test('投稿画面は友達を既定値にし、投稿後に自己投稿と4フィードを閲覧できる', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'mediaDevices', { value: { getUserMedia: async () => new MediaStream() } });
  });
  const post = {
    id: '10000000-0000-0000-0000-000000000001',
    author: { id: '20000000-0000-0000-0000-000000000001', username: 'alice', displayName: 'Alice', accountType: 'regular' },
    postingWindowId: '30000000-0000-0000-0000-000000000001', imageUrl: '/test-image.jpg', caption: '', visibility: 'friends',
    location: { kind: 'none' }, postedAt: '2026-07-19T12:00:00Z', onTime: true, lateSeconds: 0,
  };
  await page.route('**://*/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/posts' && route.request().method() === 'POST') {
      await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify(post) }); return;
    }
    if (path === '/api/posts/me/current') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(post) }); return;
    }
    if (path.startsWith('/api/feeds/')) {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], hasMore: false }) }); return;
    }
    await route.fulfill({ status: 404, contentType: 'application/problem+json', body: '{}' });
  });
  await page.goto('/');
  await page.evaluate(async () => {
    const request = indexedDB.open('isureal-session', 1);
    await new Promise<void>((resolve, reject) => {
      request.onupgradeneeded = () => request.result.createObjectStore('credentials');
      request.onsuccess = () => {
        const transaction = request.result.transaction('credentials', 'readwrite');
        transaction.objectStore('credentials').put('e2e-session-token', 'user-token');
        transaction.oncomplete = () => resolve();
        transaction.onerror = () => reject(transaction.error);
      };
      request.onerror = () => reject(request.error);
    });
  });
  await page.reload();
  await page.goto('/posts/new');
  await expect(page.getByRole('radio', { name: '友達', exact: true })).toBeChecked();
  await expect(page.locator('input[type=file]')).toHaveCount(1);
  await expect(page.getByLabel('投稿用カメラ')).toBeVisible();
  await page.getByRole('button', { name: '撮影する' }).click();
  await expect(page.getByAltText('投稿画像のプレビュー')).toBeVisible();
  await page.getByRole('button', { name: '投稿する' }).click();
  await expect(page).toHaveURL(/\/posts\/me\/current/);
  await expect(page.getByRole('heading', { name: '自分の現在の投稿' })).toBeVisible();
  for (const feed of ['友達', '友達の友達', '公開', 'フォロー中']) {
    await page.getByRole('link', { name: feed, exact: true }).click();
    await expect(page.getByRole('heading', { name: `${feed}フィード` })).toBeVisible();
  }
});
