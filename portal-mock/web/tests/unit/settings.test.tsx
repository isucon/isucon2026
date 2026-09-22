import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Region } from '@isucon/portal-proto/portal/v1/portal_pb';
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

describe('地理分散設定', () => {
  it('10か国とdesired/applied設定を表示する', async () => {
    const user = userEvent.setup();
    renderPortal();
    expect(screen.getByRole('heading', { name: 'メイン接続先' })).toBeInTheDocument();
    expect(screen.getByLabelText('server2 の desired リージョン')).toHaveValue(String(Region.AP));
    await user.click(screen.getByRole('button', { name: /対応国/ }));
    expect(screen.getByText('アメリカ合衆国')).toBeInTheDocument();
    expect(screen.getByText('フランス')).toBeInTheDocument();
  });

  it('反映待ちとRouter失敗理由を表示する', () => {
    renderPortal(makeController(makeState({ desiredServer2: Region.EU, converged: false, reason: 'router apply failed' })));
    expect(screen.getAllByText('反映待ち').length).toBeGreaterThan(0);
    expect(screen.getByText(/router apply failed/)).toBeInTheDocument();
  });
});
