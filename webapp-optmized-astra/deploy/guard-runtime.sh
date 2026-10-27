#!/bin/bash
set -euo pipefail
# Preserve the installed guard and its detection thresholds. Only update its
# journal source to the current application and private write worker units.
unit=$(python3 - "$(dirname "$0")/runtime.json" <<'PY_CONFIG'
import json,sys
unit=json.load(open(sys.argv[1]))['guardJournalUnit']
assert unit=='webapp-optmized-astra*.service'
print(unit)
PY_CONFIG
)
[ "$(systemctl show isureal-guard.service -p LoadState --value)" = loaded ]
mkdir -p /etc/systemd/system/isureal-guard.service.d
printf '[Service]\nEnvironment=GUARD_JOURNAL_UNIT=%s\n' "$unit" > /etc/systemd/system/isureal-guard.service.d/astra.conf
systemctl daemon-reload
systemctl restart isureal-guard.service
systemctl is-active isureal-guard.service
