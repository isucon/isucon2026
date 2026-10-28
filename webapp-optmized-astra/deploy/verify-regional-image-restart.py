"""Verify published images from every durable store across process restarts.

Run capture with the existing private env.sh sourced; verify needs no DB
credentials. Signed URLs stay in a root-only local snapshot, never in output.
"""
import hashlib
import hmac
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.parse
import urllib.request

snapshot = Path('/var/lib/astra-maint/regional-image-restart.json')


def fetch(uri):
    assert uri.startswith('/api/images/') and '\r' not in uri and '\n' not in uri
    with urllib.request.urlopen('http://127.0.0.1:8080'+uri, timeout=15) as response:
        assert response.status == 200
        assert response.headers.get_content_type() == 'image/jpeg'
        data=response.read()
    return {'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()}


if sys.argv[1:] == ['capture']:
    import base64
    env=dict(os.environ,PGPASSWORD=os.environ.get('ISUREAL_DB_PASSWORD','isureal'))
    query="""SELECT coalesce(json_agg(row_to_json(sample)), '[]'::json) FROM (
      SELECT region, image_id, secret_key_hash FROM
      (VALUES ('asia'),('europe'),('america')) stores(region)
      CROSS JOIN LATERAL (
        SELECT i.id AS image_id,u.secret_key_hash FROM posts p
        JOIN image_objects i ON i.id=p.image_id AND i.created_at=p.posted_at
        JOIN users u ON u.id=p.author_id AND u.status='active'
        WHERE p.deleted_at IS NULL AND p.posted_at>=current_date
          AND CASE WHEN stores.region='asia' THEN i.asset_key NOT LIKE '%:%'
                   ELSE i.asset_key LIKE stores.region||':%' END
        ORDER BY p.posted_at DESC,p.id DESC LIMIT 2
      ) images
    ) sample"""
    command=['psql','-XAt','-v','ON_ERROR_STOP=1',
             '-h',os.environ.get('ISUREAL_DB_HOST','192.168.148.10'),
             '-p',os.environ.get('ISUREAL_DB_PORT','5432'),
             '-U',os.environ.get('ISUREAL_DB_USER','isureal'),
             '-d',os.environ.get('ISUREAL_DB_NAME','isureal'),'-c',query]
    rows=json.loads(subprocess.check_output(command,env=env,text=True))
    assert len(rows)==6 and {r['region'] for r in rows}=={'asia','europe','america'}
    samples=[]
    exp=int(time.time())+3600
    for row in rows:
        key=bytes.fromhex(row['secret_key_hash'])
        value=f"{exp}:{row['image_id']}".encode()
        for _ in range(1000):value=hmac.digest(key,value,'sha256')
        signature=base64.urlsafe_b64encode(value).decode().rstrip('=')
        uri='/api/images/'+urllib.parse.quote(row['image_id'],safe='')+f'?exp={exp}&sig={signature}'
        samples.append({'region':row['region'],'uri':uri,**fetch(uri)})
    snapshot.parent.mkdir(mode=0o711,parents=True,exist_ok=True)
    fd=os.open(snapshot,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
    with os.fdopen(fd,'w') as output:json.dump(samples,output)
    print('Captured 6 published images: 2 each from Asia, Europe and America stores')
elif sys.argv[1:] == ['verify']:
    samples=json.loads(snapshot.read_text())
    for sample in samples:
        assert fetch(sample['uri'])=={k:sample[k] for k in ('bytes','sha256')}
    snapshot.unlink()
    print('Verified 6 images after restart: exact bytes and JPEG types match for all three stores')
else:
    raise SystemExit('Expected capture or verify')
