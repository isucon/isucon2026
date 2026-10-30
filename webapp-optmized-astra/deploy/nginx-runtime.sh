#!/bin/bash
set -euo pipefail
configuration=/etc/nginx/nginx.conf
environment=/etc/systemd/system/nginx.service.d/astra-memory.conf
mkdir -p "$(dirname "$environment")"
python3 - "$(dirname "$0")/runtime.json" "$configuration" "$environment" <<'PY'
import json
from pathlib import Path
import re
import sys

runtime = json.loads(Path(sys.argv[1]).read_text())
connections = runtime["nginxWorkerConnections"]
descriptors = runtime["nginxWorkerRlimitNofile"]
assert isinstance(connections, int) and 768 <= connections <= 65536
assert isinstance(descriptors, int) and connections <= descriptors <= 524288
configuration = Path(sys.argv[2])
content = configuration.read_text()
content, count = re.subn(r"(?m)^(\s*worker_connections\s+)\d+\s*;", lambda m: f"{m[1]}{connections};", content)
assert count == 1, "Expected one nginx worker_connections directive"
content, count = re.subn(r"(?m)^\s*worker_rlimit_nofile\s+\d+\s*;", f"worker_rlimit_nofile {descriptors};", content)
assert count <= 1, "Multiple nginx worker_rlimit_nofile directives"
if count == 0:
    content = f"worker_rlimit_nofile {descriptors};\n" + content
buffering = runtime['nginxRequestBuffering']
assert isinstance(buffering, bool)
content = re.sub(r"(?m)^    proxy_request_buffering (?:on|off); # astra-managed\n", "", content)
content, count = re.subn(r"(?m)^http[ \t]*\{[ \t]*$", "http {\n    proxy_request_buffering " + ('on' if buffering else 'off') + "; # astra-managed", content)
assert count == 1, "Expected one global nginx http block"
response_buffering = runtime.get('nginxResponseBuffering', True)
assert isinstance(response_buffering, bool)
content = re.sub(r"(?m)^    proxy_buffering (?:on|off); # astra-managed\n", "", content)
content, count = re.subn(r"(?m)^http[ \t]*\{[ \t]*$", "http {\n    proxy_buffering " + ('on' if response_buffering else 'off') + "; # astra-managed", content)
assert count == 1, "Expected one global nginx http block"
body_buffer = runtime.get('nginxClientBodyBufferKiB', 8)
assert type(body_buffer) is int and 8 <= body_buffer <= 1024
content = re.sub(r"(?m)^    client_body_buffer_size \d+k; # astra-managed\n", "", content)
content, count = re.subn(r"(?m)^http[ \t]*\{[ \t]*$", "http {\n    client_body_buffer_size " + str(body_buffer) + "k; # astra-managed", content)
assert count == 1, "Expected one global nginx http block"
log_bytes = runtime['nginxAccessLogBufferBytes']
flush_seconds = runtime['nginxAccessLogFlushSeconds']
assert log_bytes in (0, 16384, 32768, 65536) and isinstance(flush_seconds, int) and 1 <= flush_seconds <= 5
logging = runtime['nginxAccessLogEnabled']
assert isinstance(logging, bool)
log_options = f" combined buffer={log_bytes} flush={flush_seconds}s" if log_bytes else ""
log_target = '/var/log/nginx/access.log' + log_options if logging else 'off'
content, count = re.subn(r"(?m)^([ \t]*)access_log[ \t]+(?:/var/log/nginx/access\.log(?:[ \t]+combined)?(?:[ \t]+buffer=\d+[kKmM]?)?(?:[ \t]+flush=\d+[sm]?)?|off)[ \t]*;(?: # astra-managed)?$", lambda m: m[1] + 'access_log ' + log_target + "; # astra-managed", content)
assert count == 1, "Expected one standard global Nginx access log"
threshold = runtime.get('nginxMallocThresholdKiB', 0)
assert type(threshold) is int and threshold in (0, 64, 128, 256, 512)
keys = ['MALLOC_MMAP_THRESHOLD_', 'MALLOC_TRIM_THRESHOLD_']
for key in keys:
    content = re.sub(r"(?m)^env " + key + r"; # astra-managed\n", "", content)
if threshold:
    content = ''.join('env ' + key + '; # astra-managed\n' for key in keys) + content
    memory = '[Service]\n' + ''.join('Environment=' + key + '=' + str(threshold * 1024) + '\n' for key in keys)
else:
    memory = '[Service]\nUnsetEnvironment=' + ' '.join(keys) + '\n'
Path(sys.argv[3] + '.new').write_text(memory)
configuration.with_suffix(".conf.astra-new").write_text(content)
PY
configuration_changed=false
if cmp -s "$configuration" "$configuration.astra-new"; then
    rm "$configuration.astra-new"
else
    cp -a "$configuration" "$configuration.astra-previous"
    mv "$configuration.astra-new" "$configuration"
    if ! nginx -t; then
        mv "$configuration.astra-previous" "$configuration"
        rm -f "$environment.new"
        exit 1
    fi
    configuration_changed=true
fi
if ! cmp -s "$environment" "$environment.new"; then
    mv "$environment.new" "$environment"
    systemctl daemon-reload
    # A new master must start with the allocator environment; reload is insufficient.
    systemctl restart nginx
else
    rm "$environment.new"
    if [ "$configuration_changed" = true ]; then
        systemctl reload nginx
    fi
fi
