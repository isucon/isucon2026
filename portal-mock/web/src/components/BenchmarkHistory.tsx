import {
  BenchmarkResult,
  type BenchmarkRun,
} from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { useId, useState } from 'react';
import {
  benchmarkResultLabel,
  formatDateTime,
  formatScore,
  regionLabel,
  serverLabel,
} from '../app/format';

export interface BenchmarkHistoryProps {
  history: BenchmarkRun[];
}

export function BenchmarkHistory({ history }: BenchmarkHistoryProps) {
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const prefix = useId();

  return (
    <section className="card history-card" aria-labelledby="history-heading">
      <h2 id="history-heading">実行履歴</h2>
      {history.length === 0 ? (
        <div className="empty-state">
          <strong>まだ実行履歴はありません</strong>
          <span>ベンチマーク完了後、最新の結果が先頭に追加されます。</span>
        </div>
      ) : (
        <div className="history-table-wrap">
          <table className="history-table">
            <caption className="visually-hidden">新しい順のベンチマーク実行履歴</caption>
            <thead>
              <tr>
                <th scope="col">#</th>
                <th scope="col">日時</th>
                <th scope="col">対象</th>
                <th scope="col">スコア</th>
                <th scope="col">結果</th>
                <th scope="col"><span className="visually-hidden">ログ操作</span></th>
              </tr>
            </thead>
            {history.map((run) => {
              const expanded = expandedId === run.id;
              const panelId = `${prefix}-${run.id}-logs`;
              const failed = run.result === BenchmarkResult.FAIL;
              return (
                <tbody key={run.id}>
                  <tr>
                    <td className="mono">{run.sequence.toString()}</td>
                    <td className="mono">{formatDateTime(run.completedAt)}</td>
                    <td>
                      <span className="mono">{serverLabel(run.mainServer)}</span>
                      <small>{regionLabel(run.mainServerRegion)}</small>
                    </td>
                    <td className="mono">{failed ? '—' : formatScore(run.score)}</td>
                    <td>
                      <span className={`result-badge result-${benchmarkResultLabel(run.result).toLowerCase()}`}>
                        {benchmarkResultLabel(run.result)}
                      </span>
                    </td>
                    <td>
                      <button
                        type="button"
                        className="expand-button"
                        aria-expanded={expanded}
                        aria-controls={panelId}
                        onClick={() => setExpandedId(expanded ? null : run.id)}
                      >
                        {expanded ? 'ログを閉じる' : 'ログを表示'}
                        <span aria-hidden="true">{expanded ? '▾' : '▸'}</span>
                      </button>
                    </td>
                  </tr>
                  {expanded && (
                    <tr className="log-row">
                      <td colSpan={6}>
                        <div id={panelId} className="run-details">
                          <dl>
                            <div>
                              <dt>実行ID</dt>
                              <dd className="mono">{run.id}</dd>
                            </div>
                            <div>
                              <dt>接続先</dt>
                              <dd className="mono">{run.targetHostname || '—'}</dd>
                            </div>
                          </dl>
                          <pre aria-label={`実行 ${run.sequence.toString()} のログ`}>
                            {run.logs.length > 0 ? run.logs.join('\n') : 'ログはありません'}
                          </pre>
                        </div>
                      </td>
                    </tr>
                  )}
                </tbody>
              );
            })}
          </table>
        </div>
      )}
    </section>
  );
}
