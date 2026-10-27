# Upstream provenance

- Repository: https://github.com/isucon/isucon14-portal
- Revision: `4c2b99a69b03dc0af56642dcf7ed9ef2015935e4`
- Source directory: `supervisor/`
- Imported: `src/`, `gen/`, `Cargo.toml`, `Cargo.lock`, `rustfmt.toml`, `.gitignore`.
- License: MIT; the repository's original license is retained in [LICENSE](LICENSE).

The Rust implementation and generated protobuf bindings are vendored, so normal
builds do not fetch upstream source. Cargo dependencies are pinned by Cargo.lock;
builds use `--locked`. Docker pins Rust 1.90.0 and the base image digest.
The generated bindings are the upstream snapshot, not regenerated at build time.
If the portal protocol changes, update these bindings and run the cross-process
integration test against the portal before deploying.

Local changes:

- SurveyResponse includes agent (field 2) and harness (field 3), matching the portal protocol.

- Default hard timeout: 70 → 150 seconds for the benchmark's 120-second budget.
- HTTP requests have a 15-second timeout, including progress reports.
- Nonzero/signal/missing exit status and supervisor errors force fail and zero score.
- Config Debug output and request logs redact the shared token and job handle.
- Tests cover a real child process, inherited report FD, protobuf HTTP reporting,
  final-result draining after child exit, failure classification and log redaction.
- Container builds include the repository's ISUReal benchmark, TLS CA certificates,
  OpenSSL runtime libraries and an unprivileged execution user.

The upstream Reporter mentioned in docs/research/bench.md belongs to the benchmark
library, not this supervisor. ISUReal uses its own Go Reporter in bench/internal/benchrun.
