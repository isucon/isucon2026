import {
  RouterMode,
  type CalculatedConfig,
  type MockRuntime,
} from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import type { ConnectionStatus } from '../app/usePortalState';
import { routerModeLabel, standInProfileLabel } from '../app/format';

export interface PortalHeaderProps {
  teamNumber?: number;
  calculated?: CalculatedConfig;
  runtime?: MockRuntime;
  connectionStatus: ConnectionStatus;
  resetBusy: boolean;
  onReset: () => void;
}

const connectionLabels: Record<ConnectionStatus, string> = {
  connecting: '接続中',
  connected: '接続済み',
  reconnecting: '再接続中',
  offline: '切断',
};

export function PortalHeader({
  teamNumber,
  calculated,
  runtime,
  connectionStatus,
  resetBusy,
  onReset,
}: PortalHeaderProps) {
  const mode = runtime ? routerModeLabel(runtime.routerMode) : '—';
  const profile =
    runtime?.routerMode === RouterMode.STAND_IN
      ? standInProfileLabel(runtime.standInProfile)
      : null;

  return (
    <header className="portal-header">
      <div className="brand">
        <span className="brand__title">ISUCON2026</span>
        <span className="brand__badge">PORTAL MOCK</span>
      </div>
      <div className="header-summary">
        <div className="connection" data-status={connectionStatus} role="status">
          <span aria-hidden="true" />
          {connectionLabels[connectionStatus]}
        </div>
        <div className="runtime-label" title={`Router mode: ${mode}${profile ? ` / ${profile}` : ''}`}>
          <span>{mode}</span>
          {profile && <small>{profile}</small>}
        </div>
        <div className="team-label">Team {teamNumber ?? '—'}</div>
        <button
          type="button"
          className="reset-button"
          disabled={resetBusy}
          onClick={onReset}
          aria-label="全モック状態を初期状態へリセット"
        >
          {resetBusy ? 'リセット中…' : 'リセット'}
        </button>
      </div>
      <div className="header-hostname mono" title={calculated?.hostnames?.app}>
        {calculated?.hostnames?.app || 'ホスト名を取得中'}
      </div>
    </header>
  );
}
