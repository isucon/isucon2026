"""Check private file transport, exact bytes and access restrictions on existing VMs.

Only creates and removes uniquely named probe files. No published image changes.
"""
from concurrent.futures import ThreadPoolExecutor
import base64
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import uuid

root = Path.cwd()
runtime = json.loads((root / 'webapp-optmized-astra/deploy/runtime.json').read_text())
assert runtime['fileImageStore']['enabled']
lock = open(root / 'var/astra/operation.lock', 'w')
fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
env = dict(os.environ, AWS_CONFIG_FILE=str(root / 'provisioning/aws-sdk.config'), AWS_SDK_LOAD_CONFIG='1')
ssh = str(root / 'webapp-optmized-astra/deploy/ssh.sh')
payload = (root / 'webapp-optmized-astra/rust/tests/fixtures/small.jpg').read_bytes()
digest = hashlib.sha256(payload).hexdigest()
probes = [{**app, 'id': str(uuid.uuid4())} for app in runtime['applications']]


def remote(app, program):
    return subprocess.check_output([ssh, app['instanceId'], 'python3 -c ' + shlex.quote(program)], env=env, text=True)


def create(app):
    path = '/var/lib/astra-images/' + app['id'][:2] + '/' + app['id']
    remote(app, f'''import base64,os
path={path!r}
fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o444)
with os.fdopen(fd,'wb') as output:
 output.write(base64.b64decode({base64.b64encode(payload).decode()!r}))
 output.flush();os.fsync(output.fileno())
fd=os.open(os.path.dirname(path),os.O_RDONLY|os.O_DIRECTORY)
os.fsync(fd);os.close(fd)
''')


def verify(app):
    program = 'probes=' + repr(probes) + '\nlocal=' + repr(app) + '\ndigest=' + repr(digest) + '\n' + '''
import hashlib,subprocess,urllib.request
for peer in probes:
 path='/internal/astra/files/'+peer['id'][:2]+'/'+peer['id']
 url='http://'+peer['host']+':8082'+path
 with urllib.request.urlopen(url,timeout=15) as response:
  assert response.status==200 and response.headers.get_content_type()=='image/jpeg'
  assert hashlib.sha256(response.read()).hexdigest()==digest
path='/internal/astra/files/'+local['id'][:2]+'/'+local['id']
def status(url,*args):
 return subprocess.check_output(['curl','--silent','--show-error','--max-time','10','-o','/dev/null','-w','%{http_code}',*args,url],text=True)
private='http://'+local['host']+':8082'+path
assert status(private,'-X','POST')=='403'
assert status(private,'--interface','127.0.0.1','-H','X-Forwarded-For: '+probes[0]['host'])=='403'
public=subprocess.check_output(['curl','--silent','--show-error','--max-time','10','http://127.0.0.1'+path])
assert hashlib.sha256(public).hexdigest()!=digest
assert status('http://'+local['host']+':8082/internal/astra/files/no-such-file')=='404'
print('all 3 peer JPEG hashes match; public path does not expose image; spoofed peer and POST rejected')
'''
    return app['region'] + ': ' + remote(app, program).strip()


try:
    for app in probes:
        create(app)
    with ThreadPoolExecutor(max_workers=3) as pool:
        for result in pool.map(verify, probes):
            print(result, flush=True)
finally:
    for app in probes:
        path = '/var/lib/astra-images/' + app['id'][:2] + '/' + app['id']
        remote(app, f'from pathlib import Path; Path({path!r}).unlink(missing_ok=True)')
