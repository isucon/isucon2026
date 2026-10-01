import React from "react";
import { create } from "@bufbuild/protobuf";
import {
  RegistrationRecordInputSchema,
  type RegistrationRecord,
} from "../../proto/isuxportal/services/registration/records_pb";
import type { ApiClient } from "./ApiClient";
import { ErrorMessage } from "./ErrorMessage";

export function RegistrationRecordPanel({ client }: { client: ApiClient }) {
  const [record, setRecord] = React.useState<RegistrationRecord | null>(null);
  const [error, setError] = React.useState<Error | null>(null);
  const [busy, setBusy] = React.useState(false);
  React.useEffect(() => {
    client.getRegistrationRecord().then(setRecord).catch(setError);
  }, [client]);
  async function setSponsor(agreed: boolean) {
    setBusy(true);
    try {
      setRecord(await client.updateRegistrationRecord(create(RegistrationRecordInputSchema, { agreeSponsor: agreed })));
      setError(null);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="mt-6">
      <h3 className="title is-3">同意記録</h3>
      {error && <ErrorMessage error={error} />}
      {record && (
        <>
          <p>参加規約：{record.termsAgreedAt ?? "記録なし"}</p>
          <p>個人情報の取扱い：{record.privacyAgreedAt ?? "記録なし"}</p>
          <p>
            法定代理人の同意：
            {record.minorParentalConsent === undefined ? "記録なし" : record.minorParentalConsent ? "あり" : "なし"}
          </p>
          <p>
            個人メールアドレス：{record.email || "未登録"} / Tシャツサイズ：{record.tshirtSize || "未登録"}
          </p>
          <p>
            スポンサーへの情報提供：
            {record.sponsorProvideAgreedAt
              ? `同意済み（${record.sponsorProvideAgreedAt}）`
              : record.sponsorProvideWithdrawnAt
                ? `撤回済み（${record.sponsorProvideWithdrawnAt}）`
                : "同意の記録なし"}
          </p>
          <p>
            <a href="/privacy.html" target="_blank" rel="noreferrer">
              個人情報の取扱い
            </a>
            を確認してください。スポンサーへの情報提供は任意です。
          </p>
          <button
            className="button"
            type="button"
            disabled={busy}
            onClick={() => setSponsor(!record.sponsorProvideAgreedAt)}
          >
            {record.sponsorProvideAgreedAt
              ? "スポンサーへの情報提供の同意を撤回する"
              : "スポンサーへの情報提供に同意する"}
          </button>
        </>
      )}
    </section>
  );
}
