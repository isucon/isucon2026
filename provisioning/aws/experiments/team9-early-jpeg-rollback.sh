#!/bin/sh
set -eu

python3 - <<'PY'
from pathlib import Path

source = Path('/home/isucon/webapp/go/profile_image_api.go')
text = source.read_text()
new = '''\t\tvar prefix [2]byte
\t\tif _, err := io.ReadFull(c.Request().Body, prefix[:]); err != nil || prefix != [2]byte{0xff, 0xd8} {
\t\t\treturn NewAPIError(http.StatusBadRequest, "invalid_profile_image")
\t\t}
\t\tdata, err := io.ReadAll(io.MultiReader(bytes.NewReader(prefix[:]), c.Request().Body))
'''
old = '\t\tdata, err := io.ReadAll(c.Request().Body)\n'
if new not in text:
    raise SystemExit('early JPEG handler not installed')
source.write_text(text.replace(new, old, 1))

site = Path('/etc/nginx/sites-available/isureal.conf')
text = site.read_text()
new = '''  location = /api/me/profile-image {
    proxy_request_buffering off;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_pass http://127.0.0.1:8080;
  }

'''
if new not in text:
    raise SystemExit('streaming nginx location not installed')
site.write_text(text.replace(new, '', 1))
PY

nginx -t
cd /home/isucon/webapp/go
runuser -u isucon -- /home/isucon/local/go/bin/go build -o isureal.late-jpeg cmd/isureal/main.go
mv -f isureal.late-jpeg isureal
systemctl restart isureal-go
systemctl reload nginx
rm -f /var/lib/isureal/team9-early-jpeg-v1
