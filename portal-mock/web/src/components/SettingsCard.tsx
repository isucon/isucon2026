import { Region, ServerId, type Config } from '@isucon/portal-proto/portal/v1/portal_pb';
import type { CalculatedConfig } from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { useState } from 'react';
import { regionLabel, serverLabel } from '../app/format';

const SERVERS = [
  { id: ServerId.SERVER1, key: 'server1' as const },
  { id: ServerId.SERVER2, key: 'server2' as const },
  { id: ServerId.SERVER3, key: 'server3' as const },
];

export interface SettingsCardProps {
  desired?: Config;
  applied?: Config;
  desiredCalculated?: CalculatedConfig;
  appliedCalculated?: CalculatedConfig;
}

function getRegion(config: Config | undefined, key: 'server1' | 'server2' | 'server3') {
  return config?.serverRegions?.[key] ?? Region.UNSPECIFIED;
}

function hostnameFor(calculated: CalculatedConfig | undefined, key: 'server1' | 'server2' | 'server3') {
  return calculated?.hostnames?.servers?.[key] ?? '—';
}

export function SettingsCard({
  desired,
  applied,
  desiredCalculated,
  appliedCalculated,
}: SettingsCardProps) {
  const [copied, setCopied] = useState<string | null>(null);

  const copy = async (key: string, value: string) => {
    if (!value || value === '—') return;
    try {
      await navigator.clipboard.writeText(value);
      setCopied(key);
      window.setTimeout(() => setCopied((current) => (current === key ? null : current)), 1_500);
    } catch {
      setCopied(null);
    }
  };

  return (
    <>
      <section className="card settings-card" aria-labelledby="routing-heading">
        <div className="settings-card__heading">
          <div>
            <h2 id="routing-heading">国別自動ルーティング</h2>
            <p>ユーザーの国に対応するリージョンのサーバーへ接続します</p>
          </div>
        </div>
        <div className="hostname-row">
          <span className="mono">{appliedCalculated?.hostnames?.app || '接続先は未同期です'}</span>
          <button type="button" className="copy-button"
            disabled={!appliedCalculated?.hostnames?.app}
            onClick={() => void copy('app', appliedCalculated?.hostnames?.app ?? '')}>
            {copied === 'app' ? 'コピー済' : 'コピー'}
          </button>
        </div>
      </section>

      <section className="card settings-card settings-card--servers" aria-labelledby="server-regions-heading">
        <div className="settings-card__heading">
          <div>
            <h2 id="server-regions-heading">サーバー配置</h2>
            <p>server1=AP、server2=EU、server3=US に固定されています</p>
          </div>
        </div>
        <div className="server-settings">
          {SERVERS.map(({ id, key }) => {
            const desiredRegion = getRegion(desired, key);
            const appliedRegion = getRegion(applied, key);
            const pending = desiredRegion !== appliedRegion;
            const hostname = hostnameFor(desiredCalculated, key);
            return (
              <div className="server-setting" key={key}>
                <div className="server-setting__title">
                  <div>
                    <span className="mono server-name">{serverLabel(id)}</span>
                    <span className={`region-badge region-${regionLabel(desiredRegion).toLowerCase()}`}>
                      {regionLabel(desiredRegion)}
                    </span>
                    {pending && <span className="pending-label">反映待ち</span>}
                  </div>
                  <button
                    type="button"
                    className="copy-button"
                    disabled={hostname === '—'}
                    onClick={() => void copy(key, hostname)}
                  >
                    {copied === key ? 'コピー済' : 'コピー'}
                  </button>
                </div>
                <div className="hostname mono" title={hostname}>
                  {hostname}
                </div>
                <div className="server-setting__controls">
                  <span aria-label={`${key} の固定リージョン`}>{regionLabel(desiredRegion)}</span>
                  <span className="applied-value">
                    applied: <strong>{regionLabel(appliedRegion)}</strong>
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      </section>
    </>
  );
}
