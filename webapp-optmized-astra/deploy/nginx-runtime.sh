#!/bin/bash
set -euo pipefail
configuration=/etc/nginx/nginx.conf
python3 - "$(dirname "$0")/runtime.json" "$configuration" <<'PY'
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
log_bytes = runtime['nginxAccessLogBufferBytes']
flush_seconds = runtime['nginxAccessLogFlushSeconds']
assert log_bytes in (0, 16384, 32768, 65536) and isinstance(flush_seconds, int) and 1 <= flush_seconds <= 5
logging = runtime['nginxAccessLogEnabled']
assert isinstance(logging, bool)
log_options = f" combined buffer={log_bytes} flush={flush_seconds}s" if log_bytes else ""
log_target = '/var/log/nginx/access.log' + log_options if logging else 'off'
content, count = re.subn(r"(?m)^([ \t]*)access_log[ \t]+(?:/var/log/nginx/access\.log(?:[ \t]+combined)?(?:[ \t]+buffer=\d+[kKmM]?)?(?:[ \t]+flush=\d+[sm]?)?|off)[ \t]*;(?: # astra-managed)?$", lambda m: m[1] + 'access_log ' + log_target + "; # astra-managed", content)
assert count == 1, "Expected one standard global Nginx access log"
configuration.with_suffix(".conf.astra-new").write_text(content)
PY
if cmp -s "$configuration" "$configuration.astra-new"; then
    rm "$configuration.astra-new"
    exit 0
fi
cp -a "$configuration" "$configuration.astra-previous"
mv "$configuration.astra-new" "$configuration"
if ! nginx -t; then
    mv "$configuration.astra-previous" "$configuration"
    exit 1
fi
systemctl reload nginx
