#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."
compose=(docker compose -f deploy/local-debug/compose.yaml)
fixture_args=(-start recent -id-offset 1000000)
case "${1:-}" in
  --scores-only)
    fixture_start=$("${compose[@]}" exec -T portal-db psql -X -At -v ON_ERROR_STOP=1 -U isuxportal -d isuxportal <<'SQL'
SELECT to_char((created_at AT TIME ZONE 'UTC') + interval '1 hour', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
FROM teams WHERE id=1000001 AND name='load-team-1000001';
SQL
)
    if [[ -z "$fixture_start" ]]; then
      printf '%s\n' 'No debug load fixture found; run make debug-loadfixture first.' >&2
      exit 1
    fi
    fixture_args=(-start "$fixture_start" -id-offset 1000000 -scores-only)
    ;;
  "") ;;
  *) printf '%s\n' 'Usage: loadfixture.sh [--scores-only]' >&2; exit 1 ;;
esac
# Generate completely before opening a database transaction.
fixture_sql=$(mktemp "${TMPDIR:-/tmp}/portal-debug-fixture.XXXXXX")
trap 'rm -f "$fixture_sql"' EXIT
(cd portal && go run ./cmd/loadfixture "${fixture_args[@]}") > "$fixture_sql"
"${compose[@]}" exec -T portal-db psql -X -q -v ON_ERROR_STOP=1 -U isuxportal -d isuxportal < "$fixture_sql"
# The default debug contest is unfrozen until its end. Match its best-result visibility
# and ask the running worker to refresh the Redis dashboards for every team.
"${compose[@]}" exec -T portal-db psql -X -q -v ON_ERROR_STOP=1 -U isuxportal -d isuxportal <<'SQL'
BEGIN;
UPDATE teams SET best_benchmark_result_for_audience_id = best_benchmark_result_id
WHERE id BETWEEN 1000001 AND 1001000;
INSERT INTO jobs (name, args) VALUES ('update_contestant_dashboard', '{"team_id":0,"frozen":false}');
COMMIT;
SQL
printf '%s\n' 'Fixture data applied. Dashboard refresh is queued.' \
  'Portal: http://localhost:'"${DEBUG_PORTAL_PORT:-18081}"'/' \
  'Impersonate contestant 1000001 to view the first generated team.'
