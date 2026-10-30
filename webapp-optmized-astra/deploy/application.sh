#!/bin/bash
set -euo pipefail
archive=$1
read_host=${2:-}
read_fence=${3:-0}
write_fence_hosts=${4:-}
write_fence_mode=${5:-poll}
region=${6:?region required}
read_scope=${7:-all}
case "$read_scope" in all|feed_search) ;; *) exit 2 ;; esac
case "$write_fence_mode" in poll|native) ;; *) exit 2 ;; esac
case "$write_fence_hosts" in ''|192.168.178.10,192.168.179.10) ;; *) exit 2 ;; esac
case "$read_fence" in 0|1) ;; *) exit 2 ;; esac
case "$read_host" in ''|127.0.0.1) ;; *) exit 2 ;; esac
destination=/home/isucon/webapp-optmized-astra
mkdir -p "$destination"
bash "$(dirname "$0")/extract-release.sh" "$archive" "$destination"
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
printf '[Service]\nEnvironment=ISUREAL_DB_READ_HOST=%s\nEnvironment=ISUREAL_READ_FENCE=%s\nEnvironment=ISUREAL_DB_READ_SCOPE=%s\n' "$read_host" "$read_fence" "$read_scope" >/etc/systemd/system/webapp-optmized-astra.service.d/reader.conf
printf '[Service]\nEnvironment=ISUREAL_WRITE_FENCE_HOSTS=%s\nEnvironment=ISUREAL_WRITE_FENCE_MODE=%s\n' "$write_fence_hosts" "$write_fence_mode" >/etc/systemd/system/webapp-optmized-astra.service.d/writer.conf
systemctl stop isureal-go isureal-rust
systemctl disable isureal-go isureal-rust
bash "$(dirname "$0")/tokio-runtime.sh" webapp-optmized-astra
bash "$(dirname "$0")/logging-runtime.sh" webapp-optmized-astra
bash "$(dirname "$0")/malloc-runtime.sh" webapp-optmized-astra
bash "$(dirname "$0")/image-store-runtime.sh" webapp-optmized-astra
bash "$(dirname "$0")/file-store-runtime.sh" webapp-optmized-astra "$region"
bash "$(dirname "$0")/upload-proxy-runtime.sh" webapp-optmized-astra "$region"
systemctl daemon-reload
systemctl enable webapp-optmized-astra
systemctl restart webapp-optmized-astra
sleep 2
systemctl is-active webapp-optmized-astra
bash "$(dirname "$0")/nginx-runtime.sh"
bash "$(dirname "$0")/guard-runtime.sh"
