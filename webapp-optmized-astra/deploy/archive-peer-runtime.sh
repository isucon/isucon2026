#!/bin/bash
set -euo pipefail
python3 - "$(dirname "$0")/runtime.json" <<'PY'
import json,os,pwd,sys
from pathlib import Path
r=json.loads(Path(sys.argv[1]).read_text());p=r.get('archivePeer',{'enabled':False,'readsEnabled':False})
assert not p['readsEnabled'] or p['enabled']
text='# Archive peer disabled by the Pulumi runtime.\n'
if p['enabled']:
 assert p['host']==r['database']['host']=='192.168.180.10' and p['instanceId']==r['database']['instanceId']
 assert p['root']=='/var/lib/astra-images/packs' and p['port']==8082
 owner=pwd.getpwnam('isucon')
 for path in [Path('/var/lib/astra-images'),Path(p['root'])]:
  path.mkdir(exist_ok=True);os.chown(path,owner.pw_uid,owner.pw_gid);path.chmod(0o755)
  fd=os.open(path,os.O_RDONLY|os.O_DIRECTORY)
  try:os.fsync(fd)
  finally:os.close(fd)
 fd=os.open('/var/lib',os.O_RDONLY|os.O_DIRECTORY)
 try:os.fsync(fd)
 finally:os.close(fd)
 aio='threads' if p.get('aioThreads',False) else 'off'
 allow='\n'.join('    allow '+a['host']+';' for a in r['applications']+[r['database']])
 text=f'''# Managed by provisioning/aws/astra. Existing db1 immutable archive peer.
server {{
    listen {p['host']}:{p['port']};
    server_name _;
    sendfile on;
    aio {aio};
    tcp_nopush on;
    access_log off;
    gzip off;
{allow}
    deny all;
    location ~ "^/internal/astra/packs/([0-9a-f]{{32}})$" {{
        alias {p['root']}/$1;
        default_type application/octet-stream;
        max_ranges 1;
        open_file_cache max=64 inactive=60s;
        open_file_cache_valid 60s;
        open_file_cache_errors off;
        limit_except GET {{ deny all; }}
    }}
    location / {{ return 404; }}
}}
'''
Path('/etc/nginx/conf.d/astra-archive-peer.conf.new').write_text(text)
PY
configuration=/etc/nginx/conf.d/astra-archive-peer.conf
if cmp -s "$configuration" "$configuration.new"; then
    rm "$configuration.new"
    nginx -t
    systemctl enable --now nginx
    exit 0
fi
existed=false
if [ -f "$configuration" ]; then
    cp -a "$configuration" "$configuration.previous"
    existed=true
fi
mv "$configuration.new" "$configuration"
if ! nginx -t; then
    if [ "$existed" = true ]; then mv "$configuration.previous" "$configuration"; else rm "$configuration"; fi
    exit 1
fi
systemctl enable --now nginx
systemctl reload nginx
rm -f "$configuration.previous"
