"""Check that signed images remain byte-identical after an application restart.

Run as root on an application VM while no benchmark is running. Capture keeps
up to five recently successful public image URLs in a private temporary file;
verify removes that file only after every response matches.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import time
import urllib.error
import urllib.request

snapshot = Path('/var/lib/astra-maint/image-restart-check.json')

def fetch(uri):
    assert uri.startswith('/api/images/') and not any(c in uri for c in '\r\n')
    with urllib.request.urlopen('http://127.0.0.1:8080' + uri, timeout=10) as response:
        data = response.read()
        assert response.status == 200 and response.headers.get_content_type() == 'image/jpeg'
    return {'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()}

if sys.argv[1:] == ['capture']:
    # Read a bounded tail without copying or printing the access log.
    with open('/var/log/nginx/access.log', 'rb') as log:
        log.seek(0, 2)
        log.seek(max(0, log.tell() - 8 * 1024 * 1024))
        lines = log.read().decode(errors='replace').splitlines()
    seen = set()
    samples = []
    deadline = time.monotonic() + 45
    for line in reversed(lines):
        match = re.search(r'"GET (/api/images/[^ ]+) HTTP/[^\"]+" 200 ', line)
        if not match or match[1].split('?', 1)[0] in seen:
            continue
        uri = match[1]
        seen.add(uri.split('?', 1)[0])
        try:
            samples.append({'uri': uri, **fetch(uri)})
        except urllib.error.HTTPError as error:
            if error.code != 403:
                raise
        if len(samples) >= 5 or time.monotonic() > deadline:
            break
    assert samples, 'No current public image could be sampled'
    snapshot.parent.mkdir(mode=0o711, parents=True, exist_ok=True)
    fd = os.open(snapshot, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(samples, output)
    print(f'Captured {len(samples)} current images ({sum(s["bytes"] for s in samples)} bytes)')
elif sys.argv[1:] == ['verify']:
    samples = json.loads(snapshot.read_text())
    for sample in samples:
        assert fetch(sample['uri']) == {key: sample[key] for key in ('bytes', 'sha256')}, 'Image changed across restart'
    snapshot.unlink()
    print(f'Verified {len(samples)} images after restart; bytes and JPEG content types match')
else:
    raise SystemExit('Expected capture or verify')
