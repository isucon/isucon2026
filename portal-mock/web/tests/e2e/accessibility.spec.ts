import { expect, test } from '@playwright/test';
import { requirePortalServer, resetPortal } from './support';

test.beforeEach(async ({ page }) => {
  await requirePortalServer(page);
  await resetPortal(page);
});

test('日本語ラベルと desired/applied の意味構造を持つ', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('html')).toHaveAttribute('lang', 'ja');
  await expect(page.getByRole('heading', { name: '設定反映状況' })).toBeVisible();
  await expect(page.getByRole('table', { name: 'desired と applied の設定比較' })).toBeVisible();
  await expect(page.getByRole('complementary', { name: '地理分散設定' })).toBeVisible();
});

test('キーボードだけで国一覧・設定・reset を操作でき、フォーカスが可視', async ({ page }) => {
  await page.goto('/');
  const help = page.getByRole('button', { name: /対応国/ });
  await help.focus();
  await expect(help).toBeFocused();
  await expect(help).toHaveCSS('outline-style', 'solid');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('table', { name: '対応国とリージョンの一覧' })).toBeVisible();

  const country = page.getByLabel('望ましい設定');
  await country.focus();
  await page.keyboard.press('u');
  await page.keyboard.press('Enter');
  await country.selectOption('US');
  await expect(country).toHaveValue('US');

  const reset = page.getByRole('button', { name: /全モック状態を初期状態へリセット/ });
  await reset.focus();
  await page.keyboard.press('Enter');
  await expect(country).toHaveValue('JP');
});

test('狭幅でも主要カードが横にはみ出さず縦配置される', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  );
  expect(overflow).toBe(false);
  await expect(page.getByRole('heading', { name: 'ベンチマーク実行' })).toBeVisible();
  await expect(page.getByRole('heading', { name: '国別自動ルーティング' })).toBeVisible();
});
