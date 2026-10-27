#!/bin/bash
set -euo pipefail
archive=$1
read_host=${2:-}
read_fence=${3:-0}
case "$read_fence" in 0|1) ;; *) exit 2 ;; esac
case "$read_host" in ''|127.0.0.1) ;; *) exit 2 ;; esac
destination=/home/isucon/webapp-optmized-astra
mkdir -p "$destination"
tar -xzf "$archive" -C "$destination"
chown -R isucon:isucon "$destination"
cat >/etc/systemd/system/webapp-optmized-astra.service <<'UNIT'
[Unit]
Description=webapp-optmized-astra
After=network-online.target
Wants=network-online.target

[Service]
User=isucon
Group=isucon
WorkingDirectory=/home/isucon/webapp-optmized-astra/rust
EnvironmentFile=/home/isucon/env.sh
ExecStart=/home/isucon/webapp-optmized-astra/rust/isureal
Restart=on-failure
RestartSec=1
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
mkdir -p /etc/systemd/system/webapp-optmized-astra.service.d
printf '[Service]\nEnvironment=ISUREAL_DB_READ_HOST=%s\nEnvironment=ISUREAL_READ_FENCE=%s\n' "$read_host" "$read_fence" >/etc/systemd/system/webapp-optmized-astra.service.d/reader.conf
systemctl stop isureal-go isureal-rust
systemctl disable isureal-go isureal-rust
systemctl daemon-reload
systemctl enable webapp-optmized-astra
systemctl restart webapp-optmized-astra
sleep 2
systemctl is-active webapp-optmized-astra
