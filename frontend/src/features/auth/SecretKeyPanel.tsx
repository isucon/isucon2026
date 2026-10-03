export function SecretKeyPanel({ secretKey }: { secretKey: string }): React.JSX.Element {
  return (
    <section className="secret-key-panel">
      <h2>SecretKey</h2>
      <p>ログインするときに、このSecretKeyを送信します。</p>
      <output className="secret-key" aria-label="保存した鍵">{secretKey}</output>
    </section>
  );
}
