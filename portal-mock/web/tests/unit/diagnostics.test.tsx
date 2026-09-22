import { describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  ApplyState,
  BenchmarkStatus,
} from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { PortalStateProvider } from '../../src/app/PortalStateContext';
import { PortalPage } from '../../src/app/PortalPage';
import { makeController, makeState } from './fixtures';

function renderPortal(controller = makeController()) {
  return render(
    <PortalStateProvider controller={controller}>
      <PortalPage />
    </PortalStateProvider>,
  );
}

describe('反映診断の全状態表示', () => {
  it.each([
    [ApplyState.PENDING, '反映待ち'],
    [ApplyState.DELAYED, '反映遅延'],
    [ApplyState.FAILED_RETRYABLE, '反映失敗・再試行中'],
    [ApplyState.UNSYNCED, '未同期'],
    [ApplyState.INVALID_REPORT, '不正な反映報告'],
  ])('診断状態 %s を日本語ラベルで表示する', (applyState, label) => {
    renderPortal(
      makeController(
        makeState({
          applied: applyState !== ApplyState.UNSYNCED,
          converged: false,
          applyState,
          reason:
            applyState === ApplyState.INVALID_REPORT ? 'default_country が不正です' : '',
        }),
      ),
    );
    expect(screen.getAllByText(label).length).toBeGreaterThan(0);
  });

  it('INVALID_REPORT の妥当性エラーは alert として提示する', () => {
    renderPortal(
      makeController(
        makeState({
          converged: false,
          applyState: ApplyState.INVALID_REPORT,
          reason: 'unsupported country code: ZZ',
        }),
      ),
    );
    const alerts = screen.getAllByRole('alert');
    expect(
      alerts.some((el) => el.textContent?.includes('unsupported country code: ZZ')),
    ).toBe(true);
  });

  it('収束時は desired と applied 一致メッセージを表示する', () => {
    renderPortal(makeController(makeState({ converged: true })));
    expect(
      screen.getByText('desired と applied は一致しています。'),
    ).toBeInTheDocument();
  });

  it('desired と applied の比較表に snapshot と項目差分を含む', () => {
    renderPortal(
      makeController(makeState({ desiredCountry: 'DE', converged: false })),
    );
    // snapshot ID は短縮表示され、フル値は title 属性に保持される
    expect(
      document.querySelector('[title="desired-snapshot-00000001"]'),
    ).not.toBeNull();
    const table = screen.getByRole('table', {
      name: 'desired と applied の設定比較',
    });
    // 固定配置は desired/applied で一致する
    const rows = within(table).getAllByRole('row');
    const server2Row = rows.find((r) => r.textContent?.startsWith('server2'));
    expect(server2Row).toBeDefined();
    expect(server2Row).toHaveTextContent('EU');
    expect(server2Row).not.toHaveTextContent('AP');
  });
});

describe('設定パッチ送信形式', () => {
  it('国のみを生成パッチ形式で送る', async () => {
    const user = userEvent.setup();
    const updateDesired = vi.fn(async () => true);
    renderPortal(makeController(makeState(), { updateDesired }));

    expect(screen.queryByLabelText(/desired リージョン/)).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText('望ましい設定'), 'GB');

    expect(updateDesired).toHaveBeenCalledTimes(1);
    expect(updateDesired).toHaveBeenNthCalledWith(1, { defaultCountry: 'GB' });
  });

  it('ベンチ実行中は全設定コントロールをロックする', () => {
    renderPortal(
      makeController(
        makeState({
          benchmarkStatus: BenchmarkStatus.RUNNING,
          locked: true,
          lockReason: 'ベンチマーク実行中は設定を変更できません',
        }),
      ),
    );
    expect(screen.queryByLabelText(/desired リージョン/)).not.toBeInTheDocument();
    expect(screen.getByLabelText('望ましい設定')).toBeDisabled();
    expect(screen.queryByRole('radio')).not.toBeInTheDocument();
  });
});

describe('国一覧の補助UI', () => {
  it('セレクターに10か国を列挙し、対応表を展開して全件表示する', async () => {
    const user = userEvent.setup();
    renderPortal();
    const selector = screen.getByLabelText('望ましい設定');
    expect(within(selector).getAllByRole('option')).toHaveLength(10);

    await user.click(screen.getByRole('button', { name: /対応国/ }));
    const table = screen.getByRole('table', { name: '対応国とリージョンの一覧' });
    // header + 10 rows
    expect(within(table).getAllByRole('row')).toHaveLength(11);
    expect(within(table).getByText('日本')).toBeInTheDocument();
    expect(within(table).getByText('ドイツ')).toBeInTheDocument();
  });
});

describe('接続状態と reset', () => {
  it('再接続中の接続状態を提示し reset を呼び出せる', async () => {
    const user = userEvent.setup();
    const reset = vi.fn(async () => undefined);
    renderPortal(
      makeController(makeState(), { connectionStatus: 'reconnecting', reset }),
    );
    expect(
      screen.getByRole('button', { name: /全モック状態を初期状態へリセット/ }),
    ).toBeEnabled();
    await user.click(
      screen.getByRole('button', { name: /全モック状態を初期状態へリセット/ }),
    );
    expect(reset).toHaveBeenCalledOnce();
  });

  it('reset 処理中はリセットボタンを無効化する', () => {
    renderPortal(makeController(makeState(), { mutating: true }));
    expect(
      screen.getByRole('button', { name: /全モック状態を初期状態へリセット/ }),
    ).toBeDisabled();
  });

  it('エラー・通知フィードバックを表示し閉じられる', async () => {
    const user = userEvent.setup();
    const clearFeedback = vi.fn();
    renderPortal(
      makeController(makeState(), {
        error: '状態ストリームを再接続しています',
        clearFeedback,
      }),
    );
    const alert = screen
      .getAllByRole('alert')
      .find((el) => el.textContent?.includes('状態ストリームを再接続しています'));
    expect(alert).toBeDefined();
    await user.click(within(alert as HTMLElement).getByRole('button', { name: '閉じる' }));
    expect(clearFeedback).toHaveBeenCalledOnce();
  });
});

describe('ベンチマーク前提条件とスコア', () => {
  it('未収束ではベンチ開始を拒否し理由を表示する', () => {
    renderPortal(
      makeController(makeState({ converged: false, applyState: ApplyState.PENDING })),
    );
    expect(
      screen.getByRole('button', { name: 'ベンチマークを実行' }),
    ).toBeDisabled();
    expect(screen.getByText(/設定の反映を待っています/)).toBeInTheDocument();
  });

  it('収束時はベンチ開始でき startBenchmark を呼ぶ', async () => {
    const user = userEvent.setup();
    const startBenchmark = vi.fn(async () => undefined);
    renderPortal(makeController(makeState({ converged: true }), { startBenchmark }));
    const button = screen.getByRole('button', { name: 'ベンチマークを実行' });
    expect(button).toBeEnabled();
    await user.click(button);
    expect(startBenchmark).toHaveBeenCalledOnce();
  });

  it('スコア未設定時は — と 0 回を表示する', () => {
    renderPortal(makeController(makeState({ converged: true })));
    const scoreSection = screen.getByRole('region', { name: 'ベンチマークスコア概要' });
    expect(within(scoreSection).getByText('最高スコア').parentElement).toHaveTextContent('—');
    expect(within(scoreSection).getByText('実行回数').parentElement).toHaveTextContent('0 回');
  });

  it('履歴ログをキーボードで展開・格納できる', async () => {
    const user = userEvent.setup();
    renderPortal(makeController(makeState({ history: true, converged: true })));
    const logButtons = screen.getAllByRole('button', { name: /ログを表示/ });
    logButtons[0].focus();
    await user.keyboard('{Enter}');
    expect(logButtons[0]).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText(/integrity check failed/)).toBeInTheDocument();
    await user.keyboard('{Enter}');
    expect(screen.queryByText(/integrity check failed/)).not.toBeInTheDocument();
  });
});
