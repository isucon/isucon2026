"""Exercise a paused Asia standby through the real HTTP application.

Run while no benchmark is active, after Asia replica reads are deployed.
Creates two disposable users, pauses only the asynchronous Asia standby, checks
fresh reads and timeout behavior, then resumes replay and deletes the accounts.
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
program = 'selective='+repr(runtime.get('asiaReplicaReadScope','all')=='feed_search')+'\n'+r'''
from concurrent.futures import ThreadPoolExecutor
import json,os,signal,subprocess,time,urllib.error,urllib.request,uuid

def local_sql(query):
    return subprocess.check_output(['sudo','-u','postgres','psql','-XAt','-v','ON_ERROR_STOP=1','-d','isureal','-c',query],text=True).strip()

assert local_sql('SHOW primary_slot_name')=='astra_asia'
assert local_sql('SELECT pg_is_in_recovery()')=='t'
assert local_sql("SELECT pg_get_wal_replay_pause_state()")=='not paused'
username='astrafence'+uuid.uuid4().hex[:16]
target_name=username+'x'
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
    query="SET synchronous_commit=on; UPDATE users SET display_name='"+name+"' WHERE username='"+target_name+"' RETURNING id;"
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
accounts=[]
def read():
    status,body=request('GET','/api/users?q='+target_name)
    if status!=200:return status,None
    assert len(body['items'])==1
    return status,body['items'][0]['displayName']
try:
    for name in (username,target_name):
        status,body=request('POST','/api/auth/register',{'username':name,'displayName':'before-pause','region':'asia'})
        assert status==201
        if name==target_name:target_id=body['user']['id']
        status,login=request('POST','/api/auth/login',{'username':name,'secretKey':body['secretKey']})
        assert status==200
        accounts.append(login['token'])
    headers['Authorization']='Bearer '+accounts[0]
    assert read()==(200,'before-pause')
    pause();update('after-pause')
    if selective:
        status,body=request('GET','/api/users/'+target_id)
        assert status==200 and body['displayName']=='after-pause'
        print('Primary-backed user lookup remains fresh while the local standby is paused')
    with ThreadPoolExecutor(1) as pool:
        future=pool.submit(read)
        time.sleep(.3)
        assert not future.done(), 'read returned while its update was not replayed'
        local_sql('SELECT pg_wal_replay_resume()')
        assert future.result(timeout=12)==(200,'after-pause')
    pause();update('after-timeout')
    assert read()[0]==503, 'stale read was not rejected'
    local_sql('SELECT pg_wal_replay_resume()')
    assert read()==(200,'after-timeout')
    print('PASS: paused replay blocks HTTP reads; timeout returns 503; resumed replay exposes committed updates')
finally:
    local_sql('SELECT pg_wal_replay_resume()')
    try:os.killpg(watchdog.pid,signal.SIGTERM)
    except ProcessLookupError:pass
    watchdog.wait()
    for token in accounts:
        headers['Authorization']='Bearer '+token
        status,_=request('POST','/api/me/account-deletion')
        assert status==204
        print('Disposable verification account deleted')
'''
command='set -a; . /home/isucon/env.sh; set +a; python3 -c '+shlex.quote(program)
subprocess.run([str(deploy / 'ssh.sh'), asia['instanceId'], 'sudo bash -c '+shlex.quote(command)],env=env,check=True)
subprocess.run(['python3', str(deploy / 'verify-runtime.py')],cwd=root,check=True)
