#!/bin/bash
set -euo pipefail
archive=$1
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
systemctl stop isureal-go isureal-rust
systemctl disable isureal-go isureal-rust
systemctl daemon-reload
systemctl enable webapp-optmized-astra
systemctl restart webapp-optmized-astra
sleep 2
systemctl is-active webapp-optmized-astra
