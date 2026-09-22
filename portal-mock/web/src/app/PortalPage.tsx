import { BenchmarkStatus } from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { ApplyStatus } from '../components/ApplyStatus';
import { BenchmarkHistory } from '../components/BenchmarkHistory';
import { BenchmarkPanel } from '../components/BenchmarkPanel';
import { CountrySelector } from '../components/CountrySelector';
import { PortalFooter } from '../components/PortalFooter';
import { PortalHeader } from '../components/PortalHeader';
import { ScoreCards } from '../components/ScoreCards';
import { SettingsCard } from '../components/SettingsCard';
import { usePortalStateContext } from './PortalStateContext';

export function PortalPage() {
  const controller = usePortalStateContext();
  const {
    state,
    countries,
    connectionStatus,
    loading,
    mutating,
    error,
    notice,
    updateDesired,
    startBenchmark,
    reset,
    clearFeedback,
  } = controller;
  const portal = state?.portal;
  const desired = portal?.desired;
  const applied = portal?.applied;
  const benchmarkActive =
    state?.benchmark?.status === BenchmarkStatus.PENDING ||
    state?.benchmark?.status === BenchmarkStatus.RUNNING;
  const locked = portal?.locked || benchmarkActive || false;
  const lockReason =
    portal?.lockReason ||
    state?.benchmark?.lockReason ||
    (benchmarkActive ? 'ベンチマーク実行中は設定を変更できません。' : '');

  return (
    <div className="portal-app">
      <PortalHeader
        teamNumber={portal?.team?.teamNumber}
        calculated={applied?.calculated ?? desired?.calculated}
        runtime={state?.runtime}
        connectionStatus={connectionStatus}
        resetBusy={mutating}
        onReset={() => void reset()}
      />
      <main className="portal-main">
        {error && (
          <div className="feedback feedback--error" role="alert">
            <span>{error}</span>
            <button type="button" onClick={clearFeedback}>閉じる</button>
          </div>
        )}
        {notice && (
          <div className="feedback feedback--notice" role="status">
            <span>{notice}</span>
            <button type="button" onClick={clearFeedback}>閉じる</button>
          </div>
        )}
        {loading && !state ? (
          <div className="loading-card" role="status">
            <span className="loading-dot" aria-hidden="true" />
            ポータル状態を読み込んでいます…
          </div>
        ) : !state ? (
          <div className="loading-card loading-card--error" role="alert">
            サーバー状態を取得できません。接続を確認してください。
          </div>
        ) : (
          <>
            <ScoreCards scores={state.scores} />
            <div className="dashboard-grid">
              <div className="dashboard-main">
                <BenchmarkPanel
                  benchmark={state.benchmark}
                  portal={portal}
                  busy={mutating}
                  onStart={() => void startBenchmark()}
                />
                <BenchmarkHistory history={state.history} />
                <ApplyStatus
                  desired={desired}
                  applied={applied}
                  diagnostic={portal?.apply}
                  converged={portal?.converged ?? false}
                />
              </div>
              <aside className="dashboard-sidebar" aria-label="地理分散設定">
                <SettingsCard
                  desired={desired?.config}
                  applied={applied?.config}
                  desiredCalculated={desired?.calculated}
                  appliedCalculated={applied?.calculated}
                />
                <CountrySelector
                  desired={desired?.config}
                  applied={applied?.config}
                  countries={countries}
                  locked={locked}
                  busy={mutating}
                  lockReason={lockReason}
                  onChange={(defaultCountry) => void updateDesired({ defaultCountry })}
                />
              </aside>
            </div>
          </>
        )}
        <PortalFooter />
      </main>
    </div>
  );
}
