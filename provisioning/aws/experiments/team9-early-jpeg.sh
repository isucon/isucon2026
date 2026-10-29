#!/bin/sh
set -eu

marker=/var/lib/isureal/team9-early-jpeg-v2
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
\t\t\tc.Set("abuse", "invalid_image")
\t\t\treturn NewAPIError(http.StatusBadRequest, "invalid_profile_image")
\t\t}
\t\tdata, err := io.ReadAll(io.MultiReader(bytes.NewReader(prefix[:]), c.Request().Body))
'''
legacy = new.replace('\t\t\tc.Set("abuse", "invalid_image")\n', '')
if new not in text:
    if legacy in text:
        source.write_text(text.replace(legacy, new, 1))
    elif text.count(old) == 1:
        source.write_text(text.replace(old, new, 1))
    else:
        raise SystemExit('unexpected profile image handler')

action_log = Path('/home/isucon/webapp/go/action_log.go')
text = action_log.read_text()
fallback = '''\t\t\t// The Team 9 streaming JPEG check may reject before jpeg.Decode sets the marker.
\t\t\tif abuse == "" && action == "put_profile_image" && status == http.StatusBadRequest && c.Request().ContentLength >= 1<<20 {
\t\t\t\tabuse = "invalid_image"
\t\t\t}
'''
if fallback in text:
    action_log.write_text(text.replace(fallback, '', 1))

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
if systemctl is-active --quiet isureal-go; then
  systemctl restart isureal-go
fi
if systemctl is-active --quiet nginx; then
  systemctl reload nginx
fi
install -d /var/lib/isureal
touch "$marker"
