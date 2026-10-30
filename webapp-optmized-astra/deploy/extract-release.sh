#!/bin/bash
# Stage a complete release before replacing any currently running executable.
set -euo pipefail
archive=${1:?release archive required}
destination=${2:?destination required}
mkdir -p "$destination"
python3 - "$archive" "$destination" <<'PY'
import shutil,sys,tarfile
with tarfile.open(sys.argv[1]) as archive:
    members=archive.getmembers()
    required={'rust/isureal','rust/archive-images','sql/init.sh'}
    assert required <= {m.name for m in members if m.isfile()}, 'Incomplete release archive'
    needed=sum(m.size for m in members if m.isfile())+64*1024**2
available=shutil.disk_usage(sys.argv[2]).free
if available < needed:
    raise SystemExit(f'Insufficient deployment space: {available} bytes available, {needed} required; collect unreferenced assets first')
PY
stage=$(mktemp -d "$destination/.release.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
tar -xzf "$archive" -C "$stage"
test -x "$stage/rust/isureal"
test -x "$stage/rust/archive-images"
test -x "$stage/sql/init.sh"
mkdir -p "$destination/rust" "$destination/sql"
for directory in rust sql; do
    for file in "$stage/$directory/"*; do
        test -f "$file"
        mv -f -- "$file" "$destination/$directory/${file##*/}"
    done
done
