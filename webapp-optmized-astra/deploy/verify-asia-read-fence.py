"""Exercise a paused Asia standby through the real HTTP application.

Run while no benchmark is active, after Asia replica reads are deployed.
Creates one disposable user, pauses only the asynchronous Asia standby, checks
fresh reads and timeout behavior, then resumes replay and deletes the account.
Credentials and session tokens remain in memory on the Asia VM.
"""
from pathlib import Path
import fcntl
import importlib.util
import json
import os
import shlex
import subprocess

root = Path(__file__).resolve().parents[2]
deploy = root / 'webapp-optmized-astra/deploy'
runtime = json.loads((deploy / 'runtime.json').read_text())
assert runtime.get('asiaReplicaEnabled') and runtime.get('asiaReplicaReads')
lock = open(root / 'var/astra/operation.lock', 'w')
fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
env = dict(os.environ, AWS_CONFIG_FILE=str(root / 'provisioning/aws-sdk.config'), AWS_SDK_LOAD_CONFIG='1')
os.environ.update(env)
spec = importlib.util.spec_from_file_location('asset_gc', deploy / 'asset-gc.py')
gc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gc)
gc.ensure_idle()
subprocess.run(['python3', str(deploy / 'verify-runtime.py')], cwd=root, check=True)
asia = next(app for app in runtime['applications'] if app['region'] == 'asia')
program = r'''
from concurrent.futures import ThreadPoolExecutor
import json,os,signal,subprocess,time,urllib.error,urllib.request,uuid

def local_sql(query):
    return subprocess.check_output(['sudo','-u','postgres','psql','-XAt','-v','ON_ERROR_STOP=1','-d','isureal','-c',query],text=True).strip()

assert local_sql('SHOW primary_slot_name')=='astra_asia'
assert local_sql('SELECT pg_is_in_recovery()')=='t'
assert local_sql("SELECT pg_get_wal_replay_pause_state()")=='not paused'
username='astrafence'+uuid.uuid4().hex[:16]
headers={'Content-Type':'application/json','X-Country-Code':'JP','X-Client-IP':'192.0.2.42'}
def request(method,path,body=None):
    data=None if body is None else json.dumps(body).encode()
    req=urllib.request.Request('http://127.0.0.1:8080'+path,data=data,headers=headers,method=method)
    try:
        with urllib.request.urlopen(req,timeout=15) as response:
            raw=response.read()
            return response.status,json.loads(raw) if raw else None
    except urllib.error.HTTPError as error:
        return error.code,json.loads(error.read())

def update(name):
    assert name in ('after-pause','after-timeout')
    env=dict(os.environ,PGPASSWORD=os.environ.get('ISUREAL_DB_PASSWORD','isureal'))
    command=['psql','-XAt','-v','ON_ERROR_STOP=1','-h',os.environ['ISUREAL_DB_HOST'],
             '-p',os.environ.get('ISUREAL_DB_PORT','5432'),'-U',os.environ.get('ISUREAL_DB_USER','isureal'),
             '-d',os.environ.get('ISUREAL_DB_NAME','isureal')]
    query="SET synchronous_commit=on; UPDATE users SET display_name='"+name+"' WHERE username='"+username+"' RETURNING id;"
    result=subprocess.check_output(command,input=query,env=env,text=True)
    assert 'UPDATE 1' in result

def pause():
    local_sql('SELECT pg_wal_replay_pause()')
    deadline=time.monotonic()+5
    while local_sql("SELECT pg_get_wal_replay_pause_state()")!='paused':
        assert time.monotonic()<deadline
        time.sleep(.02)

# A detached watchdog resumes this asynchronous standby even if SSH drops.
watchdog=subprocess.Popen(['sh','-c','sleep 45; sudo -u postgres psql -XAt -d isureal -c "SELECT pg_wal_replay_resume()"'],
    start_new_session=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
token=None
try:
    status,body=request('POST','/api/auth/register',{'username':username,'displayName':'before-pause','region':'asia'})
    assert status==201
    status,login=request('POST','/api/auth/login',{'username':username,'secretKey':body['secretKey']})
    assert status==200
    token=login['token'];headers['Authorization']='Bearer '+token
    status,body=request('GET','/api/me')
    assert status==200 and body['displayName']=='before-pause'
    pause();update('after-pause')
    with ThreadPoolExecutor(1) as pool:
        future=pool.submit(request,'GET','/api/me')
        time.sleep(.3)
        assert not future.done(), 'read returned while its update was not replayed'
        local_sql('SELECT pg_wal_replay_resume()')
        status,body=future.result(timeout=12)
        assert status==200 and body['displayName']=='after-pause'
    pause();update('after-timeout')
    status,body=request('GET','/api/me')
    assert status==503, 'stale read was not rejected'
    local_sql('SELECT pg_wal_replay_resume()')
    status,body=request('GET','/api/me')
    assert status==200 and body['displayName']=='after-timeout'
    print('PASS: paused replay blocks HTTP reads; timeout returns 503; resumed replay exposes committed updates')
finally:
    local_sql('SELECT pg_wal_replay_resume()')
    os.killpg(watchdog.pid,signal.SIGTERM)
    watchdog.wait()
    if token:
        status,_=request('POST','/api/me/account-deletion')
        assert status==204
        print('Disposable verification account deleted')
'''
command='set -a; . /home/isucon/env.sh; set +a; python3 -c '+shlex.quote(program)
subprocess.run([str(deploy / 'ssh.sh'), asia['instanceId'], 'sudo bash -c '+shlex.quote(command)],env=env,check=True)
subprocess.run(['python3', str(deploy / 'verify-runtime.py')],cwd=root,check=True)
