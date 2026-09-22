import type { Config } from '@isucon/portal-proto/portal/v1/portal_pb';
import type { SupportedCountry } from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { regionLabel } from '../app/format';
import { CountryHelp } from './CountryHelp';

export interface CountrySelectorProps {
  desired?: Config;
  applied?: Config;
  countries: SupportedCountry[];
  locked: boolean;
  busy: boolean;
  lockReason?: string;
  onChange: (countryCode: string) => void;
}

export function CountrySelector({
  desired,
  applied,
  countries,
  locked,
  busy,
  lockReason,
  onChange,
}: CountrySelectorProps) {
  const desiredCountry = desired?.defaultCountry ?? '';
  const appliedCountry = applied?.defaultCountry;
  const pending = appliedCountry !== desiredCountry;
  const selected = countries.find((country) => country.code === desiredCountry);

  return (
    <section className="card settings-card" aria-labelledby="country-heading">
      <div className="settings-card__heading">
        <div>
          <h2 id="country-heading">デフォルト国 (X-Country-Code)</h2>
          <p>X-Country-Code ヘッダーが無い場合に採用されるクライアントの国</p>
        </div>
        {pending && <span className="pending-label">反映待ち</span>}
      </div>
      <label className="field-label" htmlFor="default-country">
        望ましい設定
      </label>
      <select
        id="default-country"
        className={pending ? 'control control--pending' : 'control'}
        value={desiredCountry}
        disabled={locked || busy || countries.length === 0}
        aria-describedby="country-status"
        onChange={(event) => onChange(event.target.value)}
      >
        {countries.length === 0 && <option value="">国一覧を取得中</option>}
        {countries.map((country) => (
          <option key={country.code} value={country.code}>
            {country.code} — {country.nameJa} ({regionLabel(country.region)})
          </option>
        ))}
      </select>
      <div id="country-status" className="value-comparison">
        <span>
          desired <strong>{desiredCountry || '—'}</strong>
          {selected && <small>{selected.nameJa} / {regionLabel(selected.region)}</small>}
        </span>
        <span>
          applied <strong>{appliedCountry || '不明'}</strong>
        </span>
      </div>
      {locked && lockReason && <p className="lock-note">{lockReason}</p>}
      <CountryHelp countries={countries} />
    </section>
  );
}
