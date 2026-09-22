import type { Timestamp } from '@bufbuild/protobuf/wkt';
import { Region, ServerId } from '@isucon/portal-proto/portal/v1/portal_pb';
import {
  ApplyState,
  BenchmarkResult,
  BenchmarkStatus,
  RouterMode,
  StandInProfile,
} from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';

export const regionLabel = (region: Region): string =>
  ({
    [Region.US]: 'US',
    [Region.AP]: 'AP',
    [Region.EU]: 'EU',
    [Region.UNSPECIFIED]: '不明',
  })[region];

export const serverLabel = (server: ServerId): string =>
  ({
    [ServerId.SERVER1]: 'server1',
    [ServerId.SERVER2]: 'server2',
    [ServerId.SERVER3]: 'server3',
    [ServerId.UNSPECIFIED]: '不明',
  })[server];

export const applyStateLabel = (state: ApplyState): string =>
  ({
    [ApplyState.CONVERGED]: '反映済み',
    [ApplyState.PENDING]: '反映待ち',
    [ApplyState.DELAYED]: '反映遅延',
    [ApplyState.FAILED_RETRYABLE]: '反映失敗・再試行中',
    [ApplyState.UNSYNCED]: '未同期',
    [ApplyState.INVALID_REPORT]: '不正な反映報告',
    [ApplyState.UNSPECIFIED]: '状態不明',
  })[state];

export const benchmarkStatusLabel = (status: BenchmarkStatus): string =>
  ({
    [BenchmarkStatus.IDLE]: '待機中',
    [BenchmarkStatus.PENDING]: '実行待ち',
    [BenchmarkStatus.RUNNING]: '実行中',
    [BenchmarkStatus.UNSPECIFIED]: '状態不明',
  })[status];

export const benchmarkResultLabel = (result: BenchmarkResult): string =>
  ({
    [BenchmarkResult.PASS]: 'PASS',
    [BenchmarkResult.FAIL]: 'FAIL',
    [BenchmarkResult.UNSPECIFIED]: '不明',
  })[result];

export const routerModeLabel = (mode: RouterMode): string =>
  ({
    [RouterMode.STAND_IN]: 'stand-in',
    [RouterMode.EXTERNAL_ROUTER]: 'external-router',
    [RouterMode.UNSPECIFIED]: '不明',
  })[mode];

export const standInProfileLabel = (profile: StandInProfile): string =>
  ({
    [StandInProfile.SUCCESS]: 'success',
    [StandInProfile.FAIL_ONCE_THEN_SUCCESS]: 'fail-once-then-success',
    [StandInProfile.NO_REPORT]: 'no-report',
    [StandInProfile.UNSPECIFIED]: '—',
  })[profile];

export function timestampToDate(timestamp?: Timestamp): Date | undefined {
  if (!timestamp) return undefined;
  return new Date(Number(timestamp.seconds) * 1_000 + timestamp.nanos / 1_000_000);
}

export function formatDateTime(timestamp?: Timestamp): string {
  const date = timestampToDate(timestamp);
  if (!date || Number.isNaN(date.getTime())) return '—';
  return new Intl.DateTimeFormat('ja-JP', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(date);
}

export function formatLongDateTime(timestamp?: Timestamp): string {
  const date = timestampToDate(timestamp);
  if (!date || Number.isNaN(date.getTime())) return '—';
  return new Intl.DateTimeFormat('ja-JP', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date);
}

export function formatScore(score: bigint): string {
  return new Intl.NumberFormat('ja-JP').format(score);
}

export function shortSnapshot(snapshotId: string): string {
  if (!snapshotId) return '—';
  return snapshotId.length <= 12
    ? snapshotId
    : `${snapshotId.slice(0, 8)}…${snapshotId.slice(-4)}`;
}
