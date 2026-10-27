#!/bin/bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
app="$root/webapp-optmized-astra"
(cd "$app/rust" && cargo build --release)
mkdir -p "$root/var/astra/package/rust" "$root/var/astra/package/sql"
install -m755 "$app/rust/target/release/isureal" "$root/var/astra/package/rust/isureal"
cp "$app"/sql/* "$root/var/astra/package/sql/"
tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -czf "$root/var/astra/release.tar.gz" -C "$root/var/astra/package" rust sql
