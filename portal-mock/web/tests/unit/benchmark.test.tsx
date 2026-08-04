import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BenchmarkStatus } from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { PortalStateProvider } from '../../src/app/PortalStateContext';
import { PortalPage } from '../../src/app/PortalPage';
import { makeController, makeState } from './fixtures';

describe('ベンチマークUI', () => {
  it('進捗と実行中ロックを表示する', () => {
    render(
      <PortalStateProvider controller={makeController(makeState({ benchmarkStatus: BenchmarkStatus.RUNNING, progress: 42, locked: true, lockReason: '実行中です' }))}>
        <PortalPage />
      </PortalStateProvider>,
    );
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '42');
    expect(screen.getByRole('button', { name: '実行中…' })).toBeDisabled();
    expect(screen.getAllByText('実行中です').length).toBeGreaterThan(0);
  });

  it('履歴とPASS/FAILを表示する', () => {
    render(
      <PortalStateProvider controller={makeController(makeState({ history: true }))}>
        <PortalPage />
      </PortalStateProvider>,
    );
    expect(screen.getByText('PASS')).toBeInTheDocument();
    expect(screen.getByText('FAIL')).toBeInTheDocument();
    expect(screen.getAllByText('12,345').length).toBeGreaterThan(0);
  });
});
