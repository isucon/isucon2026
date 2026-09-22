import { create } from '@bufbuild/protobuf';
import { TimestampSchema } from '@bufbuild/protobuf/wkt';
import {
  ConfigSchema,
  Region,
  ServerRegionsSchema,
  TeamKeySchema,
} from '@isucon/portal-proto/portal/v1/portal_pb';
import {
  AppliedConfigSchema,
  ApplyDiagnosticSchema,
  ApplyState,
  BenchmarkResult,
  BenchmarkRunSchema,
  BenchmarkRuntimeSchema,
  BenchmarkStatus,
  CalculatedConfigSchema,
  ConfigSnapshotSchema,
  HostnamesSchema,
  MockPortalStateSchema,
  MockRuntimeSchema,
  PortalStateSchema,
  RouterMode,
  ScoreSummarySchema,
  ServerHostnamesSchema,
  StandInProfile,
  SupportedCountrySchema,
  type ApplyState as ApplyStateType,
  type BenchmarkStatus as BenchmarkStatusType,
  type MockPortalState,
  type SupportedCountry,
} from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import type { PortalStateController } from '../../src/app/usePortalState';

const now = create(TimestampSchema, { seconds: 1_785_800_000n });

const countryRows = [
  ['US', 'アメリカ合衆国', Region.US],
  ['CA', 'カナダ', Region.US],
  ['MX', 'メキシコ', Region.US],
  ['JP', '日本', Region.AP],
  ['KR', '韓国', Region.AP],
  ['CN', '中国', Region.AP],
  ['AU', 'オーストラリア', Region.AP],
  ['GB', 'イギリス', Region.EU],
  ['DE', 'ドイツ', Region.EU],
  ['FR', 'フランス', Region.EU],
] as const;

export const countries: SupportedCountry[] = countryRows.map(([code, nameJa, region]) =>
  create(SupportedCountrySchema, { code, nameJa, region }),
);

function calculated(countryRegion: Region) {
  const hostnames = create(HostnamesSchema, {
    app: 'app.team1-a1b2c3.isucon2026.example',
    target: 'app.team1-a1b2c3.isucon2026.example',
    servers: create(ServerHostnamesSchema, {
      server1: 'server1.team1-a1b2c3.isucon2026.example',
      server2: 'server2.team1-a1b2c3.isucon2026.example',
      server3: 'server3.team1-a1b2c3.isucon2026.example',
    }),
  });
  return create(CalculatedConfigSchema, { hostnames, defaultCountryRegion: countryRegion });
}

export interface StateOverrides {
  applyState?: ApplyStateType;
  reason?: string;
  converged?: boolean;
  applied?: boolean;
  desiredCountry?: string;
  locked?: boolean;
  lockReason?: string;
  benchmarkStatus?: BenchmarkStatusType;
  progress?: number;
  history?: boolean;
}

export function makeState(overrides: StateOverrides = {}): MockPortalState {
  const desiredCountry = overrides.desiredCountry ?? 'JP';
  const appliedExists = overrides.applied ?? true;
  const converged = overrides.converged ?? (appliedExists && desiredCountry === 'JP');
  const applyState =
    overrides.applyState ??
    (appliedExists ? (converged ? ApplyState.CONVERGED : ApplyState.PENDING) : ApplyState.UNSYNCED);
  const desiredConfig = create(ConfigSchema, {
    serverRegions: create(ServerRegionsSchema, {
      server1: Region.AP,
      server2: Region.EU,
      server3: Region.US,
    }),
    defaultCountry: desiredCountry,
  });
  const appliedConfig = create(ConfigSchema, {
    serverRegions: create(ServerRegionsSchema, {
      server1: Region.AP,
      server2: Region.EU,
      server3: Region.US,
    }),
    defaultCountry: 'JP',
  });
  const desired = create(ConfigSnapshotSchema, {
    snapshotId: 'desired-snapshot-00000001',
    config: desiredConfig,
    calculated: calculated(countries.find((country) => country.code === desiredCountry)?.region ?? Region.AP),
    issuedAt: now,
  });
  const applied = appliedExists
    ? create(AppliedConfigSchema, {
        snapshotId: 'applied-snapshot-00000001',
        config: appliedConfig,
        calculated: calculated(Region.AP),
        reportedAt: now,
      })
    : undefined;
  const portal = create(PortalStateSchema, {
    team: create(TeamKeySchema, { teamNumber: 1 }),
    desired,
    applied,
    apply: create(ApplyDiagnosticSchema, {
      state: applyState,
      desiredSnapshotId: desired.snapshotId,
      appliedSnapshotId: applied?.snapshotId ?? '',
      reason: overrides.reason ?? '',
      pendingSince: converged ? undefined : now,
      delayed: applyState === ApplyState.DELAYED,
    }),
    converged,
    locked: overrides.locked ?? false,
    lockReason: overrides.lockReason ?? '',
    supportedCountries: countries,
  });
  const benchmarkStatus = overrides.benchmarkStatus ?? BenchmarkStatus.IDLE;
  const history = overrides.history
    ? [
        create(BenchmarkRunSchema, {
          id: 'run-2',
          sequence: 2n,
          startedAt: now,
          completedAt: now,
          targetHostname: 'app.team1-a1b2c3.isucon2026.example',
          score: 0n,
          result: BenchmarkResult.FAIL,
          logs: ['[ERROR] integrity check failed'],
        }),
        create(BenchmarkRunSchema, {
          id: 'run-1',
          sequence: 1n,
          startedAt: now,
          completedAt: now,
          targetHostname: 'app.team1-a1b2c3.isucon2026.example',
          score: 12_345n,
          result: BenchmarkResult.PASS,
          logs: ['[INFO] final score: 12345'],
        }),
      ]
    : [];
  return create(MockPortalStateSchema, {
    runtime: create(MockRuntimeSchema, {
      routerMode: RouterMode.STAND_IN,
      standInProfile: StandInProfile.SUCCESS,
      processStartedAt: now,
    }),
    portal,
    benchmark: create(BenchmarkRuntimeSchema, {
      status: benchmarkStatus,
      progress: overrides.progress ?? 0,
      activeRunId: benchmarkStatus === BenchmarkStatus.IDLE ? '' : 'run-active',
      startedAt: benchmarkStatus === BenchmarkStatus.IDLE ? undefined : now,
      lockReason: overrides.lockReason ?? '',
    }),
    scores: create(ScoreSummarySchema, {
      hasScores: history.length > 0,
      bestScore: history.length > 0 ? 12_345n : 0n,
      latestScore: history.length > 0 ? 0n : 0n,
      runsCount: history.length,
    }),
    history,
  });
}

export function makeController(
  state = makeState(),
  overrides: Partial<PortalStateController> = {},
): PortalStateController {
  return {
    state,
    countries,
    connectionStatus: 'connected',
    loading: false,
    mutating: false,
    error: null,
    notice: null,
    updateDesired: async () => true,
    startBenchmark: async () => undefined,
    reset: async () => undefined,
    clearFeedback: () => undefined,
    ...overrides,
  };
}
