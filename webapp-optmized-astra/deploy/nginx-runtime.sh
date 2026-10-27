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
