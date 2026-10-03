import type { Config } from '@isucon/portal-proto/portal/v1/portal_pb';
import {
  ApplyState,
  type ApplyDiagnostic,
  type AppliedConfig,
  type ConfigSnapshot,
} from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import {
  applyStateLabel,
  formatLongDateTime,
  regionLabel,
  shortSnapshot,
} from '../app/format';

export interface ApplyStatusProps {
  desired?: ConfigSnapshot;
  applied?: AppliedConfig;
  diagnostic?: ApplyDiagnostic;
  converged: boolean;
}

function configRows(desired?: Config, applied?: Config) {
  return [
    {
      label: 'app1',
      desired: desired?.serverRegions ? regionLabel(desired.serverRegions.app1) : '—',
      applied: applied?.serverRegions ? regionLabel(applied.serverRegions.app1) : '不明',
    },
    {
      label: 'app2',
      desired: desired?.serverRegions ? regionLabel(desired.serverRegions.app2) : '—',
      applied: applied?.serverRegions ? regionLabel(applied.serverRegions.app2) : '不明',
    },
    {
      label: 'app3',
      desired: desired?.serverRegions ? regionLabel(desired.serverRegions.app3) : '—',
      applied: applied?.serverRegions ? regionLabel(applied.serverRegions.app3) : '不明',
    },
    {
      label: 'db1',
      desired: desired?.serverRegions ? regionLabel(desired.serverRegions.db1) : '—',
      applied: applied?.serverRegions ? regionLabel(applied.serverRegions.db1) : '不明',
    },
    {
      label: 'デフォルト国',
      desired: desired?.defaultCountry || '—',
      applied: applied?.defaultCountry || '不明',
    },
  ];
}

export function ApplyStatus({
  desired,
  applied,
  diagnostic,
  converged,
}: ApplyStatusProps) {
  const state = diagnostic?.state ?? ApplyState.UNSPECIFIED;
  const rows = configRows(desired?.config, applied?.config);
  const tone =
    state === ApplyState.CONVERGED
      ? 'success'
      : state === ApplyState.PENDING
        ? 'warning'
        : state === ApplyState.DELAYED || state === ApplyState.FAILED_RETRYABLE
          ? 'danger'
          : state === ApplyState.INVALID_REPORT
            ? 'invalid'
            : 'neutral';

  return (
    <section className={`card apply-status apply-status--${tone}`} aria-labelledby="apply-status-heading">
      <div className="apply-status__heading">
        <div>
          <span className="eyebrow">ROUTER APPLY STATUS</span>
          <h2 id="apply-status-heading">設定反映状況</h2>
        </div>
        <span className={`status-pill status-pill--${tone}`} role="status">
          <span aria-hidden="true" />
          {applyStateLabel(state)}
        </span>
      </div>

      <p className="apply-status__summary">
        {converged
          ? 'desired と applied は一致しています。'
          : state === ApplyState.UNSYNCED
            ? 'Router から applied がまだ報告されていません。'
            : 'desired は保持されたままです。Router の applied 報告を待っています。'}
      </p>

      {diagnostic?.reason && (
        <div
          className="diagnostic-message"
          role={state === ApplyState.INVALID_REPORT ? 'alert' : 'status'}
        >
          <strong>
            {state === ApplyState.INVALID_REPORT ? '報告の妥当性エラー' : 'Router からの診断'}
          </strong>
          <span>{diagnostic.reason}</span>
        </div>
      )}

      <dl className="snapshot-grid">
        <div>
          <dt>desired snapshot</dt>
          <dd className="mono" title={desired?.snapshotId}>
            {shortSnapshot(desired?.snapshotId ?? '')}
          </dd>
          <small>{formatLongDateTime(desired?.issuedAt)}</small>
        </div>
        <div>
          <dt>applied snapshot</dt>
          <dd className="mono" title={applied?.snapshotId}>
            {shortSnapshot(applied?.snapshotId ?? '')}
          </dd>
          <small>{formatLongDateTime(applied?.reportedAt)}</small>
        </div>
      </dl>

      <div className="comparison-table-wrap">
        <table className="comparison-table">
          <caption className="visually-hidden">desired と applied の設定比較</caption>
          <thead>
            <tr>
              <th scope="col">項目</th>
              <th scope="col">desired</th>
              <th scope="col">applied</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.label} className={row.desired !== row.applied ? 'has-diff' : ''}>
                <th scope="row">{row.label}</th>
                <td className="mono">{row.desired}</td>
                <td className="mono">{row.applied}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
