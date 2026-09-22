import type { ScoreSummary } from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { formatScore } from '../app/format';

export interface ScoreCardsProps {
  scores?: ScoreSummary;
}

export function ScoreCards({ scores }: ScoreCardsProps) {
  const hasScores = scores?.hasScores ?? false;
  return (
    <section className="score-grid" aria-label="ベンチマークスコア概要">
      <article className="score-card">
        <span>最高スコア</span>
        <strong className="mono">{hasScores ? formatScore(scores?.bestScore ?? 0n) : '—'}</strong>
      </article>
      <article className="score-card">
        <span>直近スコア</span>
        <strong className="mono">{hasScores ? formatScore(scores?.latestScore ?? 0n) : '—'}</strong>
      </article>
      <article className="score-card">
        <span>実行回数</span>
        <strong className="mono">
          {scores?.runsCount ?? 0}
          <small> 回</small>
        </strong>
      </article>
    </section>
  );
}
