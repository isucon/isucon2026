import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { create } from '@bufbuild/protobuf';
import {
  ApplyState,
  BenchmarkStatus,
  StreamStateResponseSchema,
} from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { usePortalState } from '../../src/app/usePortalState';
import type { PortalMockClient } from '../../src/rpc/client';
import { makeState } from './fixtures';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('RPC とストリームの応答順序', () => {
  it('遅れた設定更新応答で反映済みの状態を反映待ちへ戻さない', async () => {
    const pending = makeState({
      converged: false,
      applyState: ApplyState.PENDING,
    });
    const converged = makeState();
    const mutation = deferred<{
      desired: NonNullable<typeof pending.portal>['desired'];
      apply: NonNullable<typeof pending.portal>['apply'];
      changed: boolean;
    }>();
    const next = deferred<ReturnType<typeof makeState>>();
    const client = {
      getState: vi.fn(async () => ({ state: makeState() })),
      listSupportedCountries: vi.fn(async () => ({ countries: [] })),
      updateDesired: vi.fn(() => mutation.promise),
      async *streamState() {
        yield create(StreamStateResponseSchema, { state: await next.promise });
        await new Promise(() => {});
      },
    } as unknown as PortalMockClient;
    const { result } = renderHook(() => usePortalState({ client }));
    await waitFor(() => expect(result.current.loading).toBe(false));
    let request!: Promise<boolean>;
    act(() => {
      request = result.current.updateDesired({ defaultCountry: 'US' });
    });
    await act(async () => {
      next.resolve(converged);
    });
    await waitFor(() =>
      expect(result.current.connectionStatus).toBe('connected'),
    );
    await act(async () => {
      mutation.resolve({
        desired: pending.portal!.desired,
        apply: pending.portal!.apply,
        changed: true,
      });
      await request;
    });
    expect(result.current.state?.portal?.converged).toBe(true);
    expect(result.current.state?.portal?.apply?.state).toBe(
      ApplyState.CONVERGED,
    );
  });

  it('遅れた開始応答で完了済みベンチを pending に戻さない', async () => {
    const mutation = deferred<{
      benchmark: ReturnType<typeof makeState>['benchmark'];
    }>();
    const next = deferred<ReturnType<typeof makeState>>();
    const client = {
      getState: vi.fn(async () => ({ state: makeState() })),
      listSupportedCountries: vi.fn(async () => ({ countries: [] })),
      startBenchmark: vi.fn(() => mutation.promise),
      async *streamState() {
        yield create(StreamStateResponseSchema, { state: await next.promise });
        await new Promise(() => {});
      },
    } as unknown as PortalMockClient;
    const { result } = renderHook(() => usePortalState({ client }));
    await waitFor(() => expect(result.current.loading).toBe(false));
    let request!: Promise<void>;
    act(() => {
      request = result.current.startBenchmark();
    });
    await act(async () => {
      next.resolve(makeState({ history: true }));
    });
    await waitFor(() =>
      expect(result.current.connectionStatus).toBe('connected'),
    );
    await act(async () => {
      mutation.resolve({
        benchmark: makeState({ benchmarkStatus: BenchmarkStatus.PENDING })
          .benchmark,
      });
      await request;
    });
    expect(result.current.state?.benchmark?.status).toBe(BenchmarkStatus.IDLE);
  });

  it('遅れた初期取得でストリームの状態を上書きしない', async () => {
    const initial = deferred<{ state: ReturnType<typeof makeState> }>();
    const latest = makeState({ history: true });
    const client = {
      getState: vi.fn(() => initial.promise),
      listSupportedCountries: vi.fn(async () => ({ countries: [] })),
      async *streamState() {
        yield create(StreamStateResponseSchema, { state: latest });
        await new Promise(() => {});
      },
    } as unknown as PortalMockClient;
    const { result } = renderHook(() => usePortalState({ client }));
    await waitFor(() =>
      expect(result.current.connectionStatus).toBe('connected'),
    );
    await act(async () => {
      initial.resolve({ state: makeState() });
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.state).toEqual(latest);
  });
});
