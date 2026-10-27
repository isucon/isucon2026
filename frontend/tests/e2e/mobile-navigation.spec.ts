import { expect, type Page, test } from '@playwright/test';
const me = {
  id: 'me',
  username: 'alice',
  displayName: 'Alice',
  region: 'asia',
  accountType: 'regular',
  bio: 'Hello',
  profileImageUrl: null,
};
const post = {
  id: 'post1',
  author: me,
  imageUrl: '/isureal-logo.svg',
  caption: '今日の椅子',
  postedAt: '2026-10-26T12:00:00Z',
  postingWindowId: 'w',
  visibility: 'friends',
  location: { kind: 'none' },
  onTime: true,
  lateSeconds: 0,
  comments: [],
  realmojis: [],
};
async function setup(page: Page) {
  await page.route('**://*/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/auth/login') {
      return route.fulfill({ json: { token: 'test', user: me } });
    }
    if (path === '/api/posts/post1') return route.fulfill({ json: post });
    if (path === '/api/me') return route.fulfill({ json: me });
    if (path === '/api/posting-window/current') {
      return route.fulfill({
        json: {
          window: { id: 'w' },
          hasPosted: true,
          canPost: false,
          deletionCount: 0,
        },
      });
    }
    if (path.startsWith('/api/feeds/') || path.startsWith('/api/memories')) {
      return route.fulfill({ json: { items: [post], hasMore: false } });
    }
    if (path.startsWith('/api/relationships/')) {
      return route.fulfill({ json: { items: [], hasMore: false } });
    }
    if (path === '/api/notifications') {
      return route.fulfill({ contentType: 'text/event-stream', body: '' });
    }
    return route.fulfill({ json: { items: [], hasMore: false } });
  });
  await page.goto('/login');
  await page.getByLabel('ユーザー名').fill('alice');
  await page.getByLabel('SecretKey', { exact: true }).fill('secret');
  await page.getByRole('button', { name: 'ログイン', exact: true }).click();
  await expect(page).toHaveURL('/');
}
test('mobile home, relationship tabs and separated settings have working destinations', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await setup(page);
  await expect(page.getByRole('heading', { name: 'My friendsフィード' }))
    .toBeVisible();
  await expect(page.getByText('今日の椅子')).toBeVisible();
  await page.screenshot({ path: '/tmp/isureal-ui-home.png', fullPage: true });
  await page.getByRole('link', { name: 'つながり', exact: true }).click();
  for (
    const label of ['フォロー中', 'フォロワー', '受信申請', '送信申請', '友達']
  ) {
    await page.getByRole('navigation', { name: '関係一覧' }).getByRole('link', {
      name: label,
      exact: true,
    }).click();
    await expect(page.getByRole('heading', { name: label, exact: true }))
      .toBeVisible();
  }
  await page.getByRole('link', { name: 'プロフィールメニュー', exact: true })
    .click();
  await page.getByRole('link', { name: 'アカウント・データ' }).click();
  await page.getByRole('link', { name: 'SecretKey' }).click();
  await expect(page.getByLabel('保存した鍵')).toHaveText('secret');
  await page.goto('/settings/region');
  await expect(page.getByText('現在の地域: アジア')).toBeVisible();
  expect(
    await page.evaluate(() =>
      document.documentElement.scrollWidth <= innerWidth
    ),
  ).toBe(true);
});
test('memory detail survives a direct visit and exposes read-only interactions', async ({ page }) => {
  await setup(page);
  await page.goto('/memories');
  await page.getByRole('link', { name: /過去投稿を見る/ }).click();
  await expect(page).toHaveURL(/\/memories\/posts\/post1/);
  await page.reload();
  await expect(page.getByText('今日の椅子')).toBeVisible();
  await expect(page.getByRole('heading', { name: '過去投稿・交流を表示' }))
    .toBeVisible();
  await expect(page.getByRole('button', { name: 'カメラを開始' })).toHaveCount(
    0,
  );
});

test('RealMoji preview requires explicit send and retains the image after an error', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await setup(page);
  await page.route(
    '**://*/api/posts/post1',
    (route) =>
      route.fulfill({ json: { ...post, author: { ...me, id: 'other' } } }),
  );
  let sends = 0;
  await page.route('**://*/api/posts/post1/realmojis', async (route) => {
    if (route.request().method() === 'POST') {
      sends++;
      return route.fulfill(
        sends === 1
          ? { status: 500, json: { error: 'internal_error' } }
          : { status: 201, json: {} },
      );
    }
    return route.fulfill({ json: { items: [], hasMore: false } });
  });
  await page.goto('/posts/post1/realmoji');
  await page.getByLabel('RealMoji画像').setInputFiles(
    'tests/fixtures/isureal-logo.jpg',
  );
  await expect(page.getByAltText('RealMojiのプレビュー')).toBeVisible();
  expect(sends).toBe(0);
  await page.getByRole('button', { name: '送信', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('送信できませんでした');
  await expect(page.getByAltText('RealMojiのプレビュー')).toBeVisible();
  await page.screenshot({
    path: '/tmp/isureal-ui-realmoji.png',
    fullPage: true,
  });
  await page.getByRole('button', { name: '送信', exact: true }).click();
  await expect(page.getByAltText('RealMojiのプレビュー')).toHaveCount(0);
  expect(sends).toBe(2);
});
test('post settings retain the draft through location and retake navigation, retry and completion work', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() =>
    Object.defineProperty(navigator, 'mediaDevices', {
      value: {
        getUserMedia: async () => {
          throw new DOMException('denied', 'NotAllowedError');
        },
      },
    })
  );
  await setup(page);
  let attempts = 0;
  await page.route('**://*/api/posts', async (route) => {
    attempts++;
    const data = route.request().postDataBuffer()!.toString();
    expect(data).toContain('大切な椅子');
    expect(data).toContain('東京都');
    return route.fulfill(
      attempts === 1
        ? { status: 500, json: { error: 'internal_error' } }
        : { status: 201, json: post },
    );
  });
  await page.goto('/posts/new');
  await page.getByLabel('画像ファイル').setInputFiles(
    'tests/fixtures/isureal-logo.jpg',
  );
  await page.getByLabel('キャプション').fill('大切な椅子');
  await page.getByRole('button', { name: /位置情報:/ }).click();
  await page.getByRole('radio', { name: 'おおよその場所' }).check();
  await page.getByLabel('地域名').fill('東京都');
  await page.getByRole('button', { name: '設定を適用' }).click();
  await expect(page.getByLabel('キャプション')).toHaveValue('大切な椅子');
  await page.getByRole('button', { name: '撮り直す' }).click();
  await page.getByRole('button', { name: 'この写真で続ける' }).click();
  await expect(page.getByLabel('キャプション')).toHaveValue('大切な椅子');
  await page.getByRole('button', { name: '投稿する', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('投稿できませんでした');
  await expect(page.getByLabel('キャプション')).toHaveValue('大切な椅子');
  await page.screenshot({
    path: '/tmp/isureal-ui-compose.png',
    fullPage: true,
  });
  await page.getByRole('button', { name: '投稿する', exact: true }).click();
  await expect(page).toHaveURL('/posts/complete');
  await page.getByRole('link', { name: '友達の写真を見る' }).click();
  await expect(page).toHaveURL('/');
});

test('restored sessions populate the header and own posts only allow RealMoji moderation', async ({ page }) => {
  await setup(page);
  await page.evaluate(() => sessionStorage.clear());
  await page.reload();
  await expect(
    page.getByRole('link', { name: 'プロフィールメニュー', exact: true }),
  )
    .toHaveText('A');
  await page.goto('/posts/post1/realmoji');
  await expect(page.getByRole('heading', { name: 'RealMoji', exact: true }))
    .toBeVisible();
  await expect(page.getByLabel('RealMoji画像')).toHaveCount(0);
  let savedMe = me;
  await page.route('**://*/api/me', (route) => {
    if (route.request().method() === 'PATCH') {
      savedMe = { ...me, ...route.request().postDataJSON() };
    }
    return route.fulfill({ json: savedMe });
  });
  await page.goto('/settings/profile');
  await page.getByLabel('表示名', { exact: true }).fill('Updated Alice');
  await page.getByRole('button', { name: '保存', exact: true }).click();
  await expect(
    page.getByRole('link', { name: 'プロフィールメニュー', exact: true }),
  )
    .toHaveText('U');
});
