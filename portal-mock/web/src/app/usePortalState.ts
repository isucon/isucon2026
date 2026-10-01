import { ConnectError } from '@connectrpc/connect';
import { create } from '@bufbuild/protobuf';
import {
  DesiredPatchSchema,
  GetStateRequestSchema,
  ListSupportedCountriesRequestSchema,
  ResetRequestSchema,
  StartBenchmarkRequestSchema,
  StreamStateRequestSchema,
  UpdateDesiredRequestSchema,
  type DesiredPatch,
  type MockPortalState,
  type SupportedCountry,
} from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { useCallback, useEffect, useRef, useState } from 'react';
import { portalClient, type PortalMockClient } from '../rpc/client';

export type ConnectionStatus =
  'connecting' | 'connected' | 'reconnecting' | 'offline';

export interface PortalStateController {
  state?: MockPortalState;
  countries: SupportedCountry[];
  connectionStatus: ConnectionStatus;
  loading: boolean;
  mutating: boolean;
  error: string | null;
  notice: string | null;
  updateDesired: (patch: Partial<DesiredPatch>) => Promise<boolean>;
  startBenchmark: () => Promise<void>;
  reset: () => Promise<void>;
  clearFeedback: () => void;
}

export interface UsePortalStateOptions {
  client?: PortalMockClient;
  reconnectDelayMs?: number;
}

function errorMessage(error: unknown): string {
  if (error instanceof ConnectError) {
    return error.rawMessage || error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return '予期しないエラーが発生しました';
}

export function usePortalState({
  client = portalClient,
  reconnectDelayMs = 1_000,
}: UsePortalStateOptions = {}): PortalStateController {
  const [state, setState] = useState<MockPortalState>();
  const [countries, setCountries] = useState<SupportedCountry[]>([]);
  const [connectionStatus, setConnectionStatus] =
    useState<ConnectionStatus>('connecting');
  const [loading, setLoading] = useState(true);
  const [mutating, setMutating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const mounted = useRef(true);
  // RPC snapshots must not overwrite a stream update received while awaiting them.
  const streamRevision = useRef(0);

  const clearFeedback = useCallback(() => {
    setError(null);
    setNotice(null);
  }, []);

  useEffect(() => {
    mounted.current = true;
    const abort = new AbortController();
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

    const loadInitial = async () => {
      const revision = streamRevision.current;
      try {
        const [stateResponse, countriesResponse] = await Promise.all([
          client.getState(create(GetStateRequestSchema)),
          client.listSupportedCountries(
            create(ListSupportedCountriesRequestSchema),
          ),
        ]);
        if (abort.signal.aborted || !mounted.current) return;
        if (stateResponse.state) {
          setState((current) =>
            streamRevision.current === revision ? stateResponse.state : current,
          );
        }
        setCountries(countriesResponse.countries);
        setError(null);
      } catch (loadError) {
        if (abort.signal.aborted || !mounted.current) return;
        setError(`初期状態を取得できません: ${errorMessage(loadError)}`);
      } finally {
        if (!abort.signal.aborted && mounted.current) setLoading(false);
      }
    };

    const subscribe = async () => {
      let firstAttempt = true;
      while (!abort.signal.aborted) {
        setConnectionStatus(firstAttempt ? 'connecting' : 'reconnecting');
        try {
          const responses = client.streamState(
            create(StreamStateRequestSchema),
            {
              signal: abort.signal,
            },
          );
          for await (const response of responses) {
            if (abort.signal.aborted || !mounted.current) return;
            firstAttempt = false;
            setConnectionStatus('connected');
            setError(null);
            if (response.state) {
              streamRevision.current += 1;
              setState(response.state);
              const embeddedCountries =
                response.state.portal?.supportedCountries ?? [];
              if (embeddedCountries.length > 0) setCountries(embeddedCountries);
            }
          }
          if (abort.signal.aborted) return;
          throw new Error('状態ストリームが終了しました');
        } catch (streamError) {
          if (abort.signal.aborted || !mounted.current) return;
          setConnectionStatus('offline');
          setError(
            `状態ストリームを再接続しています: ${errorMessage(streamError)}`,
          );
          await new Promise<void>((resolve) => {
            reconnectTimer = setTimeout(resolve, reconnectDelayMs);
          });
        }
      }
    };

    void loadInitial();
    void subscribe();

    return () => {
      mounted.current = false;
      abort.abort();
      if (reconnectTimer) clearTimeout(reconnectTimer);
    };
  }, [client, reconnectDelayMs]);

  const runMutation = useCallback(
    async <T>(operation: () => Promise<T>): Promise<T> => {
      setMutating(true);
      setError(null);
      setNotice(null);
      try {
        return await operation();
      } catch (mutationError) {
        const message = errorMessage(mutationError);
        setError(message);
        throw mutationError;
      } finally {
        setMutating(false);
      }
    },
    [],
  );

  const updateDesired = useCallback(
    async (patch: Partial<DesiredPatch>): Promise<boolean> => {
      const revision = streamRevision.current;
      try {
        const response = await runMutation(() =>
          client.updateDesired(
            create(UpdateDesiredRequestSchema, {
              patch: create(DesiredPatchSchema, patch),
            }),
          ),
        );
        if (response.desired && state?.portal) {
          setState((current) =>
            current?.portal && streamRevision.current === revision
              ? {
                  ...current,
                  portal: {
                    ...current.portal,
                    desired: response.desired,
                    apply: response.apply ?? current.portal.apply,
                    converged: response.apply?.state === 1,
                  },
                }
              : current,
          );
        }
        setNotice(
          response.changed
            ? '望ましい設定を更新しました。Router の反映を待っています。'
            : '設定値は変更されていません。',
        );
        return response.changed;
      } catch {
        return false;
      }
    },
    [client, runMutation, state?.portal],
  );

  const startBenchmark = useCallback(async () => {
    const revision = streamRevision.current;
    try {
      const response = await runMutation(() =>
        client.startBenchmark(create(StartBenchmarkRequestSchema)),
      );
      if (response.benchmark) {
        setState((current) =>
          current && streamRevision.current === revision
            ? { ...current, benchmark: response.benchmark }
            : current,
        );
      }
      setNotice('ベンチマークシミュレーションを開始しました。');
    } catch {
      // Error feedback is handled by runMutation.
    }
  }, [client, runMutation]);

  const reset = useCallback(async () => {
    const revision = streamRevision.current;
    try {
      const response = await runMutation(() =>
        client.reset(create(ResetRequestSchema)),
      );
      if (response.state) {
        setState((current) =>
          streamRevision.current === revision ? response.state : current,
        );
      }
      setNotice('モック状態を既知の初期状態へリセットしました。');
    } catch {
      // Error feedback is handled by runMutation.
    }
  }, [client, runMutation]);

  return {
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
  };
}
