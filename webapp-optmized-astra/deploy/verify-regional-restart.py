"""Verify durable regional images by restarting the three apps and image DBs.

Run from the repository root while no benchmark is active. The shared operation
lock prevents concurrent local benchmark/deployment commands. Applications are
started again in a finally block; snapshots and signed URLs stay on the VMs.
"""
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import fcntl,json,os,shlex,subprocess
root=Path.cwd();runtime=json.loads(Path('webapp-optmized-astra/deploy/runtime.json').read_text())
lock=open(root/'var/astra/operation.lock','w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
env=dict(os.environ,AWS_CONFIG_FILE=str(root/'provisioning/aws-sdk.config'),AWS_SDK_LOAD_CONFIG='1')
ssh=str(root/'webapp-optmized-astra/deploy/ssh.sh');helper=root/'webapp-optmized-astra/deploy/verify-regional-image-restart.py'
apps=runtime['applications']
def remote(app,command):return subprocess.check_output([ssh,app['instanceId'],command],env=env,text=True)
def parallel(function):
 with ThreadPoolExecutor(max_workers=3) as pool:
  for line in pool.map(function,apps):print(line,flush=True)
def capture(app):
 subprocess.run([ssh,app['instanceId'],'--copy',str(helper),'/home/isucon/astra-verify-regional-image-restart.py'],env=env,check=True)
 command='set -a; . /home/isucon/env.sh; set +a; python3 /home/isucon/astra-verify-regional-image-restart.py capture'
 return app['region']+' '+remote(app,'sudo bash -c '+shlex.quote(command)).strip()
def units(app):return 'webapp-optmized-astra webapp-optmized-astra-writer' if app['region']=='asia' else 'webapp-optmized-astra'
def stop(app):remote(app,'sudo systemctl stop '+units(app));return app['region']+' applications stopped'
def storage(app):remote(app,'sudo systemctl restart postgresql@16-astraassets nginx && systemctl is-active --quiet postgresql@16-astraassets nginx');return app['region']+' durable image DB restarted'
def start(app):
 remote(app,'sudo systemctl start '+units(app))
 endpoints=[('127.0.0.1',8080)]+([('192.168.145.10',8081)] if app['region']=='asia' else [])
 program='endpoints='+repr(endpoints)+'\n'+'''import socket,time
for endpoint in endpoints:
 deadline=time.monotonic()+40
 while True:
  try:
   with socket.create_connection(endpoint,timeout=1):break
  except OSError:
   if time.monotonic()>deadline:raise
   time.sleep(.2)
'''
 remote(app,'python3 -c '+shlex.quote(program));return app['region']+' applications ready'
def verify(app):return app['region']+' '+remote(app,'sudo python3 /home/isucon/astra-verify-regional-image-restart.py verify').strip()
parallel(capture)
try:
 parallel(stop)
 parallel(storage)
finally:
 parallel(start)
parallel(verify)
subprocess.run(['python3','webapp-optmized-astra/deploy/verify-runtime.py'],env=env,check=True)
