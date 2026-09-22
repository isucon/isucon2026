import { expect, test } from '@playwright/test';
import { requirePortalServer, resetPortal } from './support';

test.beforeEach(async ({ page }) => {
  await requirePortalServer(page);
  await resetPortal(page);
  await page.goto('/');
  await expect(page.getByText('ISUCON2026', { exact: true })).toBeVisible();
});

test('設定変更が desired へ反映され applied へ収束する', async ({ page }) => {
  await expect(page.getByRole('radio')).toHaveCount(0);
  await page.getByLabel('望ましい設定').selectOption('DE');
  await expect(page.getByRole('status').filter({ hasText: /反映待ち|反映済み/ })).toBeVisible();
  await expect(page.getByLabel('望ましい設定')).toHaveValue('DE');
  await expect(page.getByRole('status').filter({ hasText: '反映済み' })).toBeVisible({
    timeout: 10_000,
  });
});

test('同値更新、10か国表示、reset の既知初期値', async ({ page }) => {
  await page.getByRole('button', { name: /対応国/ }).click();
  await expect(page.getByRole('table', { name: '対応国とリージョンの一覧' })).toBeVisible();
  await expect(page.getByRole('table', { name: '対応国とリージョンの一覧' }).getByRole('row')).toHaveCount(11);

  await page.getByLabel('望ましい設定').selectOption('JP');
  await expect(page.getByText('設定値は変更されていません。')).toBeVisible();

  await page.getByLabel('望ましい設定').selectOption('US');
  await expect(page.getByLabel('望ましい設定')).toHaveValue('US');
  await page.getByRole('button', { name: /全モック状態を初期状態へリセット/ }).click();
  await expect(page.getByLabel('望ましい設定')).toHaveValue('JP');
  await expect(page.getByLabel('server1 の固定リージョン')).toHaveText('AP');
  await expect(page.getByLabel('server2 の固定リージョン')).toHaveText('EU');
  await expect(page.getByLabel('server3 の固定リージョン')).toHaveText('US');
  await expect(page.getByLabel(/desired リージョン/)).toHaveCount(0);
});
