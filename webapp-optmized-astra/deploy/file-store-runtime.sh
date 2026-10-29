#!/bin/bash
set -euo pipefail
service=${1:?service required}
region=${2:?region required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
case "$region" in asia|europe|america) ;; *) exit 2 ;; esac
mkdir -p "/etc/systemd/system/$service.service.d"
python3 - "$(dirname "$0")/runtime.json" "$region" "$service" <<'PY'
import json,os
from pathlib import Path
import sys
runtime=json.loads(Path(sys.argv[1]).read_text());region=sys.argv[2];service=sys.argv[3]
config=runtime.get('fileImageStore',{'enabled':False,'writesEnabled':False})
enabled=config['enabled'];writes=config['writesEnabled'];archives=config.get('archiveReadsEnabled',False);assert not (writes or archives) or enabled
root=config.get('root','/var/lib/astra-images');port=config.get('port',8082)
assert root=='/var/lib/astra-images' and port==8082
apps=runtime['applications'];host=next(a['host'] for a in apps if a['region']==region)
peers={a['region']:a['host']+':8082' for a in apps} if enabled else {}
peer=runtime.get('archivePeer',{})
archive_peer='192.168.148.10:8082' if peer.get('readsEnabled',False) and region=='asia' and service=='webapp-optmized-astra' else ''
lines=['[Service]',f'Environment=ISUREAL_ARCHIVE_PEER={archive_peer}',f'Environment=ISUREAL_FILE_IMAGE_ROOT={root if enabled else ""}',f'Environment=ISUREAL_FILE_IMAGE_WRITES={int(writes)}',f'Environment=ISUREAL_FILE_IMAGE_ARCHIVES={int(archives)}',"Environment='ISUREAL_FILE_IMAGE_HOSTS_JSON="+json.dumps(peers,separators=(',',':'))+"'"]
Path(f'/etc/systemd/system/{service}.service.d/file-store.conf').write_text('\n'.join(lines)+'\n')
text='# File image readers disabled by the Pulumi runtime.\n'
if enabled:
 import pwd
 owner=pwd.getpwnam('isucon')
 for folder in [Path(root),Path(root)/'packs']+[Path(root)/f'{n:02x}' for n in range(256)]:
  folder.mkdir(exist_ok=True);os.chown(folder,owner.pw_uid,owner.pw_gid);folder.chmod(0o755)
 for folder in (Path(root),Path(root).parent):
  fd=os.open(folder,os.O_RDONLY|os.O_DIRECTORY)
  try:os.fsync(fd)
  finally:os.close(fd)
 allow='\n'.join('    allow '+a['host']+';' for a in apps+[runtime['database']])
 pack_location=''
 if peer.get('enabled',False):
  pack_location=f'''    location ~ "^/internal/astra/packs/([0-9a-f]{{32}})$" {{
        alias {root}/packs/$1;
        default_type application/octet-stream;
        gzip off;
        max_ranges 1;
        open_file_cache max=64 inactive=60s;
        open_file_cache_errors off;
        limit_except GET {{ deny all; }}
    }}
'''
 text=f'''# Managed by provisioning/aws/astra. Private VM-to-VM immutable image reads.
server {{
    listen {host}:{port};
    server_name _;
    sendfile on;
    tcp_nopush on;
    access_log off;
{allow}
    deny all;
    location ~ "^/internal/astra/files/([0-9a-f]{{2}})/([0-9a-f]{{8}}-[0-9a-f]{{4}}-[0-9a-f]{{4}}-[0-9a-f]{{4}}-[0-9a-f]{{12}})$" {{
        alias {root}/$1/$2;
        default_type image/jpeg;
        add_header Cache-Control no-store;
        open_file_cache max=10000 inactive=60s;
        open_file_cache_valid 60s;
        open_file_cache_min_uses 2;
        open_file_cache_errors off;
        limit_except GET {{ deny all; }}
    }}
{pack_location}    location / {{ return 404; }}
}}
'''
Path('/etc/nginx/conf.d/astra-files.conf.new').write_text(text)
PY
configuration=/etc/nginx/conf.d/astra-files.conf
if cmp -s "$configuration" "$configuration.new"; then
    rm "$configuration.new"
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
systemctl reload nginx
rm -f "$configuration.previous"
