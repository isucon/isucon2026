import React from "react";
import type { RegistrationRecordInput } from "../../proto/isuxportal/services/registration/records_pb";

export function RegistrationConsentFields({
  value,
  onChange,
}: {
  value: Partial<RegistrationRecordInput>;
  onChange: (value: Partial<RegistrationRecordInput>) => void;
}) {
  return (
    <fieldset className="my-5">
      <legend className="label">同意事項</legend>
      <p>
        <label>
          <input
            type="checkbox"
            name="agreeTerms"
            required
            checked={value.agreeTerms ?? false}
            onChange={(e) => onChange({ ...value, agreeTerms: e.target.checked })}
          />{" "}
          <a href="/terms" target="_blank" rel="noreferrer">
            参加規約
          </a>
          に同意する
        </label>
      </p>
      <p>
        <label>
          <input
            type="checkbox"
            name="agreePrivacy"
            required
            checked={value.agreePrivacy ?? false}
            onChange={(e) => onChange({ ...value, agreePrivacy: e.target.checked })}
          />{" "}
          <a href="/privacy.html" target="_blank" rel="noreferrer">
            個人情報の取扱い
          </a>
          に同意する
        </label>
      </p>
      <p>
        <label>
          <input
            type="checkbox"
            name="agreeSponsor"
            checked={value.agreeSponsor ?? false}
            onChange={(e) => onChange({ ...value, agreeSponsor: e.target.checked })}
          />{" "}
          スポンサー企業への個人情報提供に同意する（任意）
        </label>
      </p>
      <p className="help">
        同意しなくても参加できます。同意は登録後に撤回できます。提供の目的は「個人情報の取扱い」を確認してください。
      </p>
      <p>
        <label>
          <input
            type="checkbox"
            name="minorParentalConsent"
            checked={value.minorParentalConsent ?? false}
            onChange={(e) => onChange({ ...value, minorParentalConsent: e.target.checked })}
          />{" "}
          未成年者のみ：法定代理人の同意を得ている
        </label>
      </p>
      <p>
        <label>
          個人のメールアドレス{" "}
          <input
            className="input"
            type="email"
            value={value.email ?? ""}
            onChange={(e) => onChange({ ...value, email: e.target.value })}
          />
        </label>
      </p>
      <p>
        <label>
          Tシャツサイズ{" "}
          <select value={value.tshirtSize ?? ""} onChange={(e) => onChange({ ...value, tshirtSize: e.target.value })}>
            {["", "S", "M", "L", "XL", "2XL", "3XL"].map((size) => (
              <option key={size} value={size}>
                {size || "未選択"}
              </option>
            ))}
          </select>
        </label>
      </p>
    </fieldset>
  );
}
