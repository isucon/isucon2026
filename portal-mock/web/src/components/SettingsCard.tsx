import { Region, ServerId, type Config } from '@isucon/portal-proto/portal/v1/portal_pb';
import type { CalculatedConfig } from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { useState } from 'react';
import { regionLabel, serverLabel } from '../app/format';

const SERVERS = [
  { id: ServerId.SERVER1, key: 'server1' as const },
  { id: ServerId.SERVER2, key: 'server2' as const },
  { id: ServerId.SERVER3, key: 'server3' as const },
];

const REGIONS = [Region.US, Region.AP, Region.EU];

export interface SettingsCardProps {
  desired?: Config;
  applied?: Config;
  desiredCalculated?: CalculatedConfig;
  appliedCalculated?: CalculatedConfig;
  locked: boolean;
  busy: boolean;
  lockReason?: string;
  onMainServerChange: (server: ServerId) => void;
  onRegionChange: (server: 'server1' | 'server2' | 'server3', region: Region) => void;
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
  locked,
  busy,
  lockReason,
  onMainServerChange,
  onRegionChange,
}: SettingsCardProps) {
  const [copied, setCopied] = useState<string | null>(null);
  const disabled = locked || busy || !desired;

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
      <section className="card settings-card" aria-labelledby="main-server-heading">
        <div className="settings-card__heading">
          <div>
            <h2 id="main-server-heading">メイン接続先</h2>
            <p>Router が報告した applied の接続先がベンチ実行対象です</p>
          </div>
          {desired?.mainServer !== applied?.mainServer && (
            <span className="pending-label">反映待ち</span>
          )}
        </div>
        <div className="server-choice" role="radiogroup" aria-label="メイン接続先サーバー">
          {SERVERS.map(({ id }) => {
            const desiredSelected = desired?.mainServer === id;
            const appliedSelected = applied?.mainServer === id;
            return (
              <button
                type="button"
                role="radio"
                aria-checked={desiredSelected}
                key={id}
                disabled={disabled}
                className={[
                  'server-choice__button',
                  desiredSelected ? 'is-desired' : '',
                  appliedSelected ? 'is-applied' : '',
                  desiredSelected && !appliedSelected ? 'is-pending' : '',
                ].join(' ')}
                onClick={() => onMainServerChange(id)}
              >
                <span>{serverLabel(id)}</span>
                <small>{appliedSelected ? 'applied' : desiredSelected ? 'desired' : ' '}</small>
              </button>
            );
          })}
        </div>
        <div className="hostname-row">
          <span className="mono" title={appliedCalculated?.hostnames?.target || undefined}>
            {appliedCalculated?.hostnames?.target || 'applied の接続先は未同期です'}
          </span>
          <button
            type="button"
            className="copy-button"
            disabled={!appliedCalculated?.hostnames?.target}
            onClick={() => void copy('target', appliedCalculated?.hostnames?.target ?? '')}
          >
            {copied === 'target' ? 'コピー済' : 'コピー'}
          </button>
        </div>
        {locked && lockReason && <p className="lock-note">{lockReason}</p>}
      </section>

      <section className="card settings-card settings-card--servers" aria-labelledby="server-regions-heading">
        <div className="settings-card__heading">
          <div>
            <h2 id="server-regions-heading">サーバー個別設定</h2>
            <p>各サーバーを配置するリージョンを選択します</p>
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
                  <label htmlFor={`${key}-region`}>{serverLabel(id)} の desired リージョン</label>
                  <select
                    id={`${key}-region`}
                    className={pending ? 'control control--pending' : 'control'}
                    value={desiredRegion}
                    disabled={disabled}
                    onChange={(event) =>
                      onRegionChange(key, Number(event.target.value) as Region)
                    }
                  >
                    {REGIONS.map((region) => (
                      <option key={region} value={region}>
                        {regionLabel(region)}
                      </option>
                    ))}
                  </select>
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
