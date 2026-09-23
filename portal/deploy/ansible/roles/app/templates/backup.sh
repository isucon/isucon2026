#!/bin/bash
set -euo pipefail
umask 077
backup_dir=/var/backups/portal
exec 9> /run/lock/portal-backup.lock
flock -n 9 || exit 0
container=$(docker ps -q --filter name={{ app.stack }}_postgres --filter status=running)
[[ -n "$container" && "$container" != *$'\n'* ]]
tmp=$(mktemp "$backup_dir/.dump.XXXXXX")
trap 'rm -f "$tmp" "$tmp.gz"' EXIT
docker exec "$container" pg_dump -U {{ postgres.user }} {{ postgres.database }} > "$tmp"
gzip "$tmp"
mv "$tmp.gz" "$backup_dir/isuxportal-$(date -u +%F-%H%M%S).sql.gz"
find "$backup_dir" -name 'isuxportal-*.sql.gz' -mtime +7 -delete
