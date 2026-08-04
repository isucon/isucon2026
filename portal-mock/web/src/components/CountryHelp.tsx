import type { SupportedCountry } from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';
import { regionLabel } from '../app/format';

export interface CountryHelpProps {
  countries: SupportedCountry[];
}

export function CountryHelp({ countries }: CountryHelpProps) {
  return (
    <details className="country-help">
      <summary role="button">対応国とリージョンを確認</summary>
      <div className="country-help__content">
        <p>
          X-Country-Code が無い場合に、選択した国と対応リージョンを利用します。
        </p>
        <div className="country-table-wrap">
          <table className="country-table">
            <caption className="visually-hidden">対応国とリージョンの一覧</caption>
            <thead>
              <tr>
                <th scope="col">国コード</th>
                <th scope="col">国名</th>
                <th scope="col">リージョン</th>
              </tr>
            </thead>
            <tbody>
              {countries.map((country) => (
                <tr key={country.code}>
                  <td className="mono">{country.code}</td>
                  <td>{country.nameJa}</td>
                  <td>
                    <span className={`region-badge region-${regionLabel(country.region).toLowerCase()}`}>
                      {regionLabel(country.region)}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </details>
  );
}
