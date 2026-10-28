#!/bin/bash
set -euo pipefail
role=${1:?backend or frontend required}
config=/home/isucon/astra-deploy/runtime.json
mapfile -t values < <(python3 - "$config" <<'PY'
import json, sys
runtime = json.load(open(sys.argv[1]))
apps = runtime['applications']
primary = next(app for app in apps if app['region'] == 'asia')
print('true' if runtime.get('writeProxyEnabled', False) else 'false')
print(primary['host'])
print(json.dumps({app['host']: app['region'] for app in apps if app['region'] != 'asia'}, separators=(',', ':')))
print(','.join(app['host'] for app in apps if 'replicaName' in app) if runtime.get('writeFenceEnabled', False) else '')
print(runtime.get('writeFenceMode', 'poll'))
print('true' if runtime.get('regionalUploadCacheEnabled',False) else 'false')
print('true' if runtime.get('regionalReactionCacheEnabled',True) else 'false')
print('true' if runtime.get('asiaUpstreamKeepaliveEnabled',False) else 'false')
print('true' if runtime['nginxJsonGzipEnabled'] else 'false')
PY
)
enabled=${values[0]}
backend_host=${values[1]}
ingress_json=${values[2]}
write_fence_hosts=${values[3]}
write_fence_mode=${values[4]}
upload_cache=${values[5]}
reaction_cache=${values[6]}
asia_keepalive=${values[7]}
json_gzip=${values[8]}
if [ "$role" = backend ]; then
    if [ "$enabled" = true ]; then
        # Public topology only; the existing env.sh provides DB credentials.
        printf 'ISUREAL_LISTEN_ADDRESS=%s:8081\nISUREAL_DB_READ_HOST=\nISUREAL_DB_READ_SCOPE=all\nISUREAL_WRITE_INGRESS_JSON=\047%s\047\n' "$backend_host" "$ingress_json" > /etc/astra-write-worker.env
        chmod 644 /etc/astra-write-worker.env
        cat > /etc/systemd/system/webapp-optmized-astra-writer.service <<'UNIT'
[Unit]
Description=webapp-optmized-astra private write worker
After=network-online.target
Wants=network-online.target

[Service]
User=isucon
Group=isucon
WorkingDirectory=/home/isucon/webapp-optmized-astra/rust
EnvironmentFile=/home/isucon/env.sh
EnvironmentFile=/etc/astra-write-worker.env
ExecStart=/home/isucon/webapp-optmized-astra/rust/isureal
Restart=on-failure
RestartSec=1
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
        mkdir -p /etc/systemd/system/webapp-optmized-astra-writer.service.d
        printf '[Service]\nEnvironment=ISUREAL_WRITE_FENCE_HOSTS=%s\nEnvironment=ISUREAL_WRITE_FENCE_MODE=%s\n' "$write_fence_hosts" "$write_fence_mode" >/etc/systemd/system/webapp-optmized-astra-writer.service.d/writer.conf
        bash "$(dirname "$0")/tokio-runtime.sh" webapp-optmized-astra-writer
        bash "$(dirname "$0")/logging-runtime.sh" webapp-optmized-astra-writer
        bash "$(dirname "$0")/malloc-runtime.sh" webapp-optmized-astra-writer
        bash "$(dirname "$0")/image-store-runtime.sh" webapp-optmized-astra-writer
        bash "$(dirname "$0")/file-store-runtime.sh" webapp-optmized-astra-writer asia
        bash "$(dirname "$0")/upload-proxy-runtime.sh" webapp-optmized-astra-writer asia
        systemctl daemon-reload
        systemctl enable webapp-optmized-astra-writer
        systemctl restart webapp-optmized-astra-writer
        sleep 2
        systemctl is-active webapp-optmized-astra-writer
    elif systemctl list-unit-files webapp-optmized-astra-writer.service --no-legend | grep -q webapp-optmized-astra-writer; then
        systemctl disable --now webapp-optmized-astra-writer
    fi
elif [ "$role" = frontend ]; then
    region=${2:?frontend region required}
    case "$region" in asia|europe|america) ;; *) exit 2 ;; esac
    [ "$region" != asia ] || enabled=false
    # The frontend remains in its original region. Only write requests cross
    # the existing, shaped data-plane route to the private worker.
    python3 - "$enabled" "$backend_host" "$upload_cache" "$reaction_cache" "$region" "$asia_keepalive" "$json_gzip" > /etc/nginx/sites-available/isureal.conf.astra-new <<'PY'
import sys
enabled, host, upload_cache, reaction_cache, region, asia_keepalive, json_gzip = sys.argv[1:]
keepalive=region!='asia' or asia_keepalive=='true'
http_version='1.1' if keepalive else '1.0'
connection='""' if keepalive else 'close'
print('''upstream astra_local {
  server 127.0.0.1:8080;
  keepalive 64;
}''')
if enabled == 'true':
    print('upstream astra_writer {\n  server '+host+':8081;\n  keepalive 64;\n}')
    print('map "$request_method:$uri" $astra_upstream {')
    print('  ~^(GET|HEAD): astra_local;')
    if upload_cache == 'true':
        print('  "POST:/api/posts" astra_local;')
        if reaction_cache == 'true':
            print('  ~^POST:/api/posts/[^/]+/realmojis$ astra_local;')
    print('  default astra_writer;\n}')
upstream = '$astra_upstream' if enabled == 'true' else 'astra_local'
print('''server {
  listen 80 default_server;
  server_name _;
  client_max_body_size 32m;
  root /home/isucon/webapp/public;

  location /api/ {
    gzip '''+('on' if json_gzip=='true' else 'off')+''';
    gzip_types application/json;
    gzip_min_length 1024;
    gzip_comp_level 1;
    gzip_proxied any;
    gzip_vary on;
    proxy_http_version '''+http_version+''';
    proxy_set_header Connection '''+connection+''';
    proxy_set_header Host $host;
    proxy_next_upstream off;
    proxy_pass http://'''+upstream+''';
  }
  location / {
    try_files $uri /index.html;
  }
}''')
PY
    cp -a /etc/nginx/sites-available/isureal.conf /etc/nginx/sites-available/isureal.conf.astra-previous
    mv /etc/nginx/sites-available/isureal.conf.astra-new /etc/nginx/sites-available/isureal.conf
    if ! nginx -t; then
        mv /etc/nginx/sites-available/isureal.conf.astra-previous /etc/nginx/sites-available/isureal.conf
        exit 1
    fi
    systemctl reload nginx
else
    echo 'Expected backend or frontend' >&2
    exit 2
fi
