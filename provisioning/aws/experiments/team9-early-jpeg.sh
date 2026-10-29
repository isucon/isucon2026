#!/bin/sh
set -eu

marker=/var/lib/isureal/team9-early-jpeg-v1
if [ -f "$marker" ]; then
  exit 0
fi

python3 - <<'PY'
from pathlib import Path

source = Path('/home/isucon/webapp/go/profile_image_api.go')
text = source.read_text()
old = '\t\tdata, err := io.ReadAll(c.Request().Body)\n'
new = '''\t\tvar prefix [2]byte
\t\tif _, err := io.ReadFull(c.Request().Body, prefix[:]); err != nil || prefix != [2]byte{0xff, 0xd8} {
\t\t\treturn NewAPIError(http.StatusBadRequest, "invalid_profile_image")
\t\t}
\t\tdata, err := io.ReadAll(io.MultiReader(bytes.NewReader(prefix[:]), c.Request().Body))
'''
if new not in text:
    if text.count(old) != 1:
        raise SystemExit('unexpected profile image handler')
    source.write_text(text.replace(old, new, 1))

site = Path('/etc/nginx/sites-available/isureal.conf')
text = site.read_text()
old = '  location /api/ {\n'
new = '''  location = /api/me/profile-image {
    proxy_request_buffering off;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_pass http://127.0.0.1:8080;
  }

  location /api/ {
'''
if new not in text:
    if text.count(old) != 1:
        raise SystemExit('unexpected nginx site')
    site.write_text(text.replace(old, new, 1))
PY

nginx -t
cd /home/isucon/webapp/go
runuser -u isucon -- /home/isucon/local/go/bin/go build -o isureal.early-jpeg cmd/isureal/main.go
mv -f isureal.early-jpeg isureal
systemctl restart isureal-go
systemctl reload nginx
install -d /var/lib/isureal
touch "$marker"
