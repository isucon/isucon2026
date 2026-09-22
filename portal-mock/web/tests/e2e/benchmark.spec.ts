import { expect, test } from '@playwright/test';
import { requirePortalServer, resetPortal } from './support';

test.beforeEach(async ({ page }) => {
  await requirePortalServer(page);
  await resetPortal(page);
  await page.goto('/');
  await expect(page.getByRole('button', { name: 'ベンチマークを実行' })).toBeEnabled();
});

test('pending/running、設定ロック、完了履歴とログを表示する', async ({ page }) => {
  await page.getByRole('button', { name: 'ベンチマークを実行' }).click();
  await expect(page.getByRole('button', { name: /実行待ち|実行中/ })).toBeDisabled();
  await expect(page.getByRole('radio', { name: /server1/ })).toBeDisabled();
  await expect(page.getByRole('progressbar')).toBeVisible();

  await expect(page.getByRole('table', { name: '新しい順のベンチマーク実行履歴' })).toBeVisible({
    timeout: 15_000,
  });
  await expect(page.getByRole('cell', { name: '187,642' })).toBeVisible();
  const logButton = page.getByRole('button', { name: 'ログを表示' }).first();
  await expect(logButton).toBeVisible();
  await logButton.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByText(/deterministic sequence 1/)).toBeVisible();
});

test('reset が進行中ベンチをキャンセルする', async ({ page }) => {
  await page.getByRole('button', { name: 'ベンチマークを実行' }).click();
  await expect(page.getByRole('progressbar')).toBeVisible();
  await page.getByRole('button', { name: /全モック状態を初期状態へリセット/ }).click();
  await expect(page.getByRole('button', { name: 'ベンチマークを実行' })).toBeEnabled();
  await expect(page.getByText('まだ実行履歴はありません')).toBeVisible();
  await expect(page.getByText('実行回数').locator('..')).toContainText('0 回');
});
