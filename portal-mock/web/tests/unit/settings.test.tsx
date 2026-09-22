import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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
    expect(screen.getByRole('heading', { name: '国別自動ルーティング' })).toBeInTheDocument();
    expect(screen.getByLabelText('server1 の固定リージョン')).toHaveTextContent('AP');
    expect(screen.getByLabelText('server2 の固定リージョン')).toHaveTextContent('EU');
    expect(screen.getByLabelText('server3 の固定リージョン')).toHaveTextContent('US');
    expect(screen.queryByLabelText(/desired リージョン/)).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /対応国/ }));
    expect(screen.getByText('アメリカ合衆国')).toBeInTheDocument();
    expect(screen.getByText('フランス')).toBeInTheDocument();
  });

  it('反映待ちとRouter失敗理由を表示する', () => {
    renderPortal(makeController(makeState({ desiredCountry: 'DE', converged: false, reason: 'router apply failed' })));
    expect(screen.getAllByText('反映待ち').length).toBeGreaterThan(0);
    expect(screen.getByText(/router apply failed/)).toBeInTheDocument();
  });
});
