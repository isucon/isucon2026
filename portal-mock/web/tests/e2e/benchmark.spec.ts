import { expect, test } from '@playwright/test';
import { requirePortalServer, resetPortal } from './support';

test.beforeEach(async ({ page }) => {
  await requirePortalServer(page);
  await resetPortal(page);
  await page.goto('/');
  await expect(
    page.getByRole('button', { name: 'ベンチマークを実行' }),
  ).toBeEnabled();
});

test('pending/running、設定ロック、完了履歴とログを表示する', async ({
  page,
}) => {
  // Observe the short-lived running UI in one browser-side snapshot. Separate
  // assertions can straddle completion when the CI simulator advances every 50 ms.
  const active = page.waitForFunction(() => {
    const button = Array.from(document.querySelectorAll('button')).find(
      (element) => /実行待ち|実行中/.test(element.textContent ?? ''),
    );
    const region = document.querySelector<HTMLSelectElement>(
      '#default-country',
    );
    const progress = document.querySelector('[role="progressbar"]');
    return (
      button?.disabled &&
      region?.disabled &&
      (progress?.getClientRects().length ?? 0) > 0
    );
  });
  await page.getByRole('button', { name: 'ベンチマークを実行' }).click();
  await active;

  await expect(
    page.getByRole('table', { name: '新しい順のベンチマーク実行履歴' }),
  ).toBeVisible({
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
  await page
    .getByRole('button', { name: /全モック状態を初期状態へリセット/ })
    .click();
  await expect(
    page.getByRole('button', { name: 'ベンチマークを実行' }),
  ).toBeEnabled();
  await expect(page.getByText('まだ実行履歴はありません')).toBeVisible();
  await expect(page.getByText('実行回数').locator('..')).toContainText('0 回');
});
