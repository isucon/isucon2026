"""Read-only verification of the configured Team 10 runtime and release."""
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import tarfile

root=Path.cwd()
runtime=json.loads((root/'webapp-optmized-astra/deploy/runtime.json').read_text())
archive=root/runtime['archivePath']
with tarfile.open(archive) as package:
    expected_binary=hashlib.file_digest(package.extractfile('rust/isureal'),'sha256').hexdigest()
expected_artifact=hashlib.file_digest(open(archive,'rb'),'sha256').hexdigest()
env=dict(os.environ,AWS_CONFIG_FILE=str(root/'provisioning/aws-sdk.config'),AWS_SDK_LOAD_CONFIG='1',PULUMI_BACKEND_URL='s3://isucon2026-pulumi-state')
ssh=str(root/'webapp-optmized-astra/deploy/ssh.sh')
def remote(instance,command):
    return subprocess.check_output([ssh,instance,command],env=env,text=True)

def inspect(app):
    services=['webapp-optmized-astra']
    if app['region']=='asia':services.append('webapp-optmized-astra-writer')
    expected={}
    for service in services:
        regional=service=='webapp-optmized-astra' and app['region']!='asia'
        expected[service]={
            'ISUREAL_LISTEN_BACKLOG':str(runtime['listenBacklog']),
            'TOKIO_WORKER_THREADS':str(runtime['tokioWorkerThreads']),
            'ISUREAL_FILE_IMAGE_ROOT':runtime.get('fileImageStore',{}).get('root','') if runtime.get('fileImageStore',{}).get('enabled') else '',
            'ISUREAL_FILE_IMAGE_ARCHIVES':str(int(runtime.get('fileImageStore',{}).get('archiveReadsEnabled',False))),
            'ISUREAL_FILE_IMAGE_WRITES':str(int(runtime.get('fileImageStore',{}).get('writesEnabled',False))),
            'ISUREAL_IMAGE_STORE_HOST':runtime['imageStore']['host'],
            'ISUREAL_IMAGE_STORE_WRITES':str(int(runtime['imageStore']['writesEnabled'])),
            'ISUREAL_REGIONAL_IMAGE_WRITES':str(int(runtime.get('regionalImageWritesEnabled',False))),
            'ISUREAL_LOG_LEVEL':runtime.get('applicationLogLevel','debug'),
            'ISUREAL_WRITE_FENCE_MODE':runtime['writeFenceMode'],
            'ISUREAL_DB_READ_HOST':'127.0.0.1' if regional and runtime['replicaReads'] else '',
            'ISUREAL_UPLOAD_PROXY_TARGET':'192.168.145.10:8081' if regional and runtime['regionalUploadCacheEnabled'] else '',
        }
    regions={a['region']:a['host'] for a in runtime['applications'] if a['region']!='asia'} if runtime.get('regionalImageStoresEnabled') else {}
    program='expected='+repr(expected)+'\nbinary='+repr(expected_binary)+'\nregions='+repr(regions)+'\n'+'''
import hashlib,json,os,subprocess
result={}
for service,values in expected.items():
    assert subprocess.check_output(['systemctl','is-active',service],text=True).strip()=='active'
    assert subprocess.check_output(['systemctl','is-enabled',service],text=True).strip()=='enabled'
    pid=subprocess.check_output(['systemctl','show',service,'-p','MainPID','--value'],text=True).strip()
    assert hashlib.file_digest(open('/proc/'+pid+'/exe','rb'),'sha256').hexdigest()==binary
    entries=open('/proc/'+pid+'/environ','rb').read().split(b'\\0')
    actual={k.decode():v.decode() for entry in entries if b'=' in entry for k,v in [entry.split(b'=',1)]}
    for key,value in values.items():assert actual.get(key,'')==value,service+' runtime mismatch: '+key
    assert json.loads(actual.get('ISUREAL_REGIONAL_IMAGE_HOSTS_JSON','{}'))==regions
    if values['ISUREAL_REGIONAL_IMAGE_WRITES']=='1':
        assert len(bytes.fromhex(actual['ISUREAL_UPLOAD_HANDOFF_KEY']))==32
        assert os.stat('/home/isucon/.astra-upload.env').st_mode & 0o777==0o600
    result[service]='active, enabled, release and runtime match'
for service in ['nginx','isureal-guard']:
    assert subprocess.check_output(['systemctl','is-active',service],text=True).strip()=='active'
    assert subprocess.check_output(['systemctl','is-enabled',service],text=True).strip()=='enabled'
    result[service]='active, enabled'
print(json.dumps(result))
'''
    result=json.loads(remote(app['instanceId'],'sudo python3 -c '+shlex.quote(program)))
    if app['region']!='asia':
        assert remote(app['instanceId'],'sudo -u postgres psql -XAtc "SELECT pg_is_in_recovery()"').strip()=='t'
        result['metadata']='standby'
    if app['region']=='asia' or runtime.get('regionalImageStoresEnabled',False):
        state=remote(app['instanceId'],"sudo -u postgres psql -X -p 5433 -d isureal_assets -Atc \"SELECT pg_is_in_recovery(),current_setting('fsync'),current_setting('full_page_writes'),current_setting('synchronous_commit'),pg_database_size(current_database())\"").strip()
        assert state.startswith('f|on|on|on|')
        result['images']='durable independent primary; bytes='+state.rsplit('|',1)[1]
        start=remote(app['instanceId'],"cat /etc/postgresql/16/astraassets/start.conf")
        assert [line.strip() for line in start.splitlines() if line.strip() and not line.startswith('#')]==['auto']
        available=int(remote(app['instanceId'],"df -B1 --output=avail /var/lib/postgresql | tail -n1").strip())
        assert available>1024**3
        result['available_disk_gib']=round(available/1024**3,1)
    listeners=[line.split() for line in remote(app['instanceId'],'ss -lntH').splitlines()]
    for port in ([8080,8081] if app['region']=='asia' else [8080]):
        sockets=[entry for entry in listeners if entry[3].rsplit(':',1)[-1]==str(port)]
        assert sockets and all(int(entry[2])==runtime['listenBacklog'] for entry in sockets)
    result['listen_backlog']=runtime['listenBacklog']
    keepalive=app['region']!='asia' or runtime.get('asiaUpstreamKeepaliveEnabled',False)
    site=remote(app['instanceId'],'cat /etc/nginx/sites-available/isureal.conf')
    assert ('proxy_http_version 1.1;' in site)==keepalive
    if 'nginxRequestBuffering' in runtime:
        nginx=remote(app['instanceId'],'cat /etc/nginx/nginx.conf')
        assert 'proxy_request_buffering '+('on' if runtime['nginxRequestBuffering'] else 'off')+'; # astra-managed' in nginx
        log_bytes=runtime.get('nginxAccessLogBufferBytes',0)
        if not runtime.get('nginxAccessLogEnabled',True):
            assert 'access_log off; # astra-managed' in nginx
        elif log_bytes:
            assert 'access_log /var/log/nginx/access.log combined buffer='+str(log_bytes)+' flush='+str(runtime['nginxAccessLogFlushSeconds'])+'s; # astra-managed' in nginx
    return app['region'],result

with ThreadPoolExecutor(max_workers=3) as pool:
    for region,result in pool.map(inspect,runtime['applications']):print(region,json.dumps(result),flush=True)
query="SELECT application_name,state,sync_state,pg_wal_lsn_diff(pg_current_wal_lsn(),replay_lsn)::bigint FROM pg_stat_replication ORDER BY application_name"
rows=remote(runtime['database']['instanceId'],'sudo -u postgres psql -X -d isureal -Atc '+shlex.quote(query)).strip().splitlines()
assert len(rows)==2
for row in rows:
    name,state,sync,lag=row.split('|')
    assert state=='streaming' and sync=='sync' and 0<=int(lag)<=1024*1024
    print(name,'streaming; replay backlog bytes='+lag)
outputs=json.loads(subprocess.check_output(['pulumi','stack','output','-C',str(root/'provisioning/aws/astra'),'--stack','dev','--json'],env=env,text=True))
assert outputs['runtime']==runtime and outputs['artifactSha256']==expected_artifact
print('Pulumi runtime, artifact, live processes and durable storage verified')
