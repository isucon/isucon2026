import {
  BenchmarkStatus,
  type BenchmarkRuntime,
  type PortalState,
} from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { benchmarkStatusLabel } from '../app/format';

export interface BenchmarkPanelProps {
  benchmark?: BenchmarkRuntime;
  portal?: PortalState;
  busy: boolean;
  onStart: () => void;
}

export function BenchmarkPanel({
  benchmark,
  portal,
  busy,
  onStart,
}: BenchmarkPanelProps) {
  const status = benchmark?.status ?? BenchmarkStatus.UNSPECIFIED;
  const pending = status === BenchmarkStatus.PENDING;
  const running = status === BenchmarkStatus.RUNNING;
  const idle = status === BenchmarkStatus.IDLE;
  const blockReason =
    benchmark?.lockReason ||
    (portal && !portal.converged
      ? '設定の反映を待っています。反映後にベンチマークを実行できます。'
      : portal?.locked
        ? portal.lockReason
        : '');
  const disabled = busy || !idle || !portal?.converged || portal.locked;
  const tone = running ? 'active' : pending ? 'warning' : 'neutral';

  return (
    <section className="card benchmark-panel" aria-labelledby="benchmark-heading">
      <div className="benchmark-panel__heading">
        <h2 id="benchmark-heading">ベンチマーク実行</h2>
        <span className={`status-pill status-pill--${tone}`} role="status">
          <span aria-hidden="true" />
          {benchmarkStatusLabel(status)}
        </span>
      </div>
      <div className="benchmark-target">
        <span>実行対象:</span>
        <strong className="mono">
          全3台（国別自動ルーティング）
        </strong>
        <small className="mono">{portal?.applied?.calculated?.hostnames?.target || '未同期'}</small>
      </div>
      {blockReason && <div className="block-message" role="status">{blockReason}</div>}
      {(pending || running) && (
        <div className="progress-group">
          <div
            className="progress-track"
            role="progressbar"
            aria-label="ベンチマーク進捗"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={benchmark?.progress ?? 0}
          >
            <span style={{ width: `${Math.min(100, benchmark?.progress ?? 0)}%` }} />
          </div>
          <span className="mono">{benchmark?.progress ?? 0}%</span>
        </div>
      )}
      <button
        type="button"
        className="primary-button"
        disabled={disabled}
        onClick={onStart}
      >
        {pending ? '実行待ち…' : running ? '実行中…' : 'ベンチマークを実行'}
      </button>
    </section>
  );
}
