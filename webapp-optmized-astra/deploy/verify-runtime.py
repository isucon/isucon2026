"""Read-only verification of the configured Team 12 runtime and release."""
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import tarfile
from runtime_topology import metadata_replicas, read_settings, read_scope, synchronous_standby_names, asia_synchronous

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
    if runtime['signatureFileCacheEnabled']:
        local_hash=hashlib.file_digest(open(root/runtime['signatureFileCachePath'],'rb'),'sha256').hexdigest()
        assert remote(app['instanceId'], 'sha256sum /home/isucon/.astra-signatures.bin').split()[0]==local_hash
        assert remote(app['instanceId'], "stat -c %a /home/isucon/.astra-signatures.bin").strip()=='600'
    expected={}
    for service in services:
        regional=service=='webapp-optmized-astra' and app['region']!='asia'
        read_host, fenced=read_settings(runtime,app,writer=service.endswith('-writer'))
        expected[service]={
            'ISUREAL_READ_TIMING':str(int(runtime.get('readTimingEnabled',False))),
            'ISUREAL_POST_TIMING':str(int(runtime.get('postTimingEnabled',False))),
            'ISUREAL_AUTH_TIMING':str(int(runtime.get('authTimingEnabled',False))),
            'MALLOC_MMAP_THRESHOLD_':str(runtime.get('applicationMallocThresholdKiB',0)*1024) if runtime.get('applicationMallocThresholdKiB',0) else '',
            'MALLOC_TRIM_THRESHOLD_':str(runtime.get('applicationMallocThresholdKiB',0)*1024) if runtime.get('applicationMallocThresholdKiB',0) else '',
            'ISUREAL_LISTEN_BACKLOG':str(runtime['listenBacklog']),
            'TOKIO_WORKER_THREADS':str(runtime['tokioWorkerThreads']),
            'ISUREAL_IMAGE_CACHE_MIB':str(runtime['imageCacheMiB']),
            'ISUREAL_EXPORT_CONCURRENCY':str(runtime.get('exportConcurrency',2)),
            'ISUREAL_METADATA_POOL_CONNECTIONS':str(runtime['metadataPoolConnections']),
            'ISUREAL_NOTIFICATION_BATCH_WRITES':str(int(runtime['notificationBatchWritesEnabled'])),
            'ISUREAL_FILE_IMAGE_ROOT':runtime.get('fileImageStore',{}).get('root','') if runtime.get('fileImageStore',{}).get('enabled') else '',
            'ISUREAL_ARCHIVE_PEER':'192.168.180.10:8082' if runtime.get('archivePeer',{}).get('readsEnabled',False) and app['region']=='asia' and service=='webapp-optmized-astra' else '',
            'ISUREAL_FILE_IMAGE_ARCHIVES':str(int(runtime.get('fileImageStore',{}).get('archiveReadsEnabled',False))),
            'ISUREAL_FILE_IMAGE_WRITES':str(int(runtime.get('fileImageStore',{}).get('writesEnabled',False))),
            'ISUREAL_IMAGE_STORE_HOST':runtime['imageStore']['host'],
            'ISUREAL_IMAGE_STORE_WRITES':str(int(runtime['imageStore']['writesEnabled'])),
            'ISUREAL_REGIONAL_IMAGE_WRITES':str(int(runtime.get('regionalImageWritesEnabled',False))),
            'ISUREAL_LOG_LEVEL':runtime.get('applicationLogLevel','debug'),
            'ISUREAL_SIGNATURE_COALESCE':str(int(runtime['signatureCacheCoalescing'])),
            'ISUREAL_SIGNATURE_METRICS':str(int(runtime['signatureCacheMetrics'])),
            'ISUREAL_ISSUED_URL_CACHE':str(int(runtime['issuedImageUrlCacheEnabled'])),
            'ISUREAL_ISSUED_URL_CACHE_ENTRIES':str(runtime['issuedImageUrlCacheEntries']),
            'ISUREAL_SIGNATURE_FILE':'/home/isucon/.astra-signatures.bin' if runtime['signatureFileCacheEnabled'] and not service.endswith('-writer') else '',
            'ISUREAL_WRITE_FENCE_MODE':runtime['writeFenceMode'],
            'ISUREAL_WRITE_FENCE_HOSTS':','.join(a['host'] for a in runtime['applications'] if 'replicaName' in a) if runtime['writeFenceEnabled'] else '',
            'ISUREAL_DB_READ_HOST':read_host,
            'ISUREAL_DB_READ_SCOPE':read_scope(runtime,app,writer=service.endswith('-writer')),
            'ISUREAL_READ_FENCE':str(int(fenced)) if not service.endswith('-writer') else '',
            'ISUREAL_UPLOAD_PROXY_TARGET':'192.168.177.10:8081' if regional and runtime['regionalUploadCacheEnabled'] else '',
        }
    regions={a['region']:a['host'] for a in runtime['applications'] if a['region']!='asia'} if runtime.get('regionalImageStoresEnabled') else {}
    program='expected='+repr(expected)+'\nbinary='+repr(expected_binary)+'\nregions='+repr(regions)+'\nnginx_malloc='+repr(runtime.get('nginxMallocThresholdKiB',0)*1024)+'\n'+'''
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
import shlex
allocator_environment=dict(entry.split('=',1) for entry in shlex.split(subprocess.check_output(['systemctl','show','nginx','-p','Environment','--value'],text=True)) if '=' in entry)
for key in ['MALLOC_MMAP_THRESHOLD_','MALLOC_TRIM_THRESHOLD_']:
    assert allocator_environment.get(key,'')==(str(nginx_malloc) if nginx_malloc else ''), 'Nginx allocator setting mismatch: '+key
result['nginx_malloc_threshold_kib']=nginx_malloc//1024
print(json.dumps(result))
'''
    result=json.loads(remote(app['instanceId'],'sudo python3 -c '+shlex.quote(program)))
    if app['region']!='asia' or runtime.get('asiaReplicaEnabled',False):
        assert remote(app['instanceId'],'sudo -u postgres psql -XAtc "SELECT pg_is_in_recovery()"').strip()=='t'
        result['metadata']='standby'
    else:
        disabled=remote(app['instanceId'], 'if sudo test -f /var/lib/postgresql/16/astra/.astra-basebackup-complete; then systemctl is-active postgresql@16-main || true; cat /etc/postgresql/16/main/start.conf; fi').strip()
        if disabled:
            assert disabled=='inactive\nmanual', 'Disabled Asia standby must stay stopped across reboot'
            result['metadata']='derived standby stopped; primary reads'
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
    assert 'gzip '+('on' if runtime['nginxJsonGzipEnabled'] else 'off')+';' in site
    assert 'gzip_types application/json;' in site and 'gzip_comp_level 1;' in site
    auth_ingress = runtime.get('authTimingEnabled', False)
    assert ('log_format astra_auth escape=json' in site) == auth_ingress
    assert ('access_log /var/log/nginx/astra-auth-timing.log astra_auth buffer=32k flush=1s if=$astra_auth_action;' in site) == auth_ingress
    if 'nginxRequestBuffering' in runtime:
        nginx=remote(app['instanceId'],'cat /etc/nginx/nginx.conf')
        assert 'proxy_request_buffering '+('on' if runtime['nginxRequestBuffering'] else 'off')+'; # astra-managed' in nginx
        assert 'client_body_buffer_size '+str(runtime.get('nginxClientBodyBufferKiB',8))+'k; # astra-managed' in nginx
        for key in ['MALLOC_MMAP_THRESHOLD_','MALLOC_TRIM_THRESHOLD_']:
            assert ('env '+key+'; # astra-managed' in nginx)==bool(runtime.get('nginxMallocThresholdKiB',0))
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
actual_standbys=remote(runtime['database']['instanceId'], 'sudo -u postgres psql -XAtc "SHOW synchronous_standby_names"').strip()
assert actual_standbys==synchronous_standby_names(runtime), 'Primary synchronous acknowledgement set mismatch'
print('primary synchronous standbys='+actual_standbys)
primary_cache=int(remote(runtime['database']['instanceId'], "sudo -u postgres psql -XAtc \"SELECT pg_size_bytes(current_setting('shared_buffers'))\"").strip())
assert primary_cache==runtime['primarySharedBuffersMiB']*1024**2
print('primary shared buffers MiB='+str(primary_cache//1024**2))
peer=runtime.get('archivePeer',{'enabled':False,'readsEnabled':False})
peer_config=remote(runtime['database']['instanceId'], 'sudo cat /etc/nginx/conf.d/astra-archive-peer.conf 2>/dev/null || true')
if peer['enabled']:
    assert 'listen 192.168.180.10:8082;' in peer_config and 'deny all;' in peer_config and 'gzip off;' in peer_config
    assert 'aio '+('threads' if peer.get('aioThreads',False) else 'off')+';' in peer_config
    for app in runtime['applications']:
        assert 'allow '+app['host']+';' in peer_config
        app_config=remote(app['instanceId'], 'sudo cat /etc/nginx/conf.d/astra-files.conf')
        assert '/internal/astra/packs/' in app_config and 'max_ranges 1;' in app_config
    remote(runtime['database']['instanceId'], 'systemctl is-active --quiet nginx && systemctl is-enabled --quiet nginx')
    if peer['readsEnabled']:
        subprocess.run(['python3',str(root/'webapp-optmized-astra/deploy/archive-peer.py'),'verify'],env=env,check=True)
    print('db1 archive peer enabled; Asia peer reads='+str(peer['readsEnabled']))
else:
    assert 'listen 192.168.180.10:8082;' not in peer_config
expected_replicas={app["replicaName"] for app in metadata_replicas(runtime)}
assert len(rows)==len(expected_replicas) and {row.split("|")[0] for row in rows}==expected_replicas
for row in rows:
    name,state,sync,lag=row.split('|')
    expected_sync='sync' if synchronous_standby_names(runtime) and (name!='astra_asia' or asia_synchronous(runtime)) else 'async'
    assert state=='streaming' and sync==expected_sync and 0<=int(lag)<=1024*1024
    print(name,'streaming; replay backlog bytes='+lag)
outputs=json.loads(subprocess.check_output(['pulumi','stack','output','-C',str(root/'provisioning/aws/astra'),'--stack','dev','--json'],env=env,text=True))
assert outputs['runtime']==runtime and outputs['artifactSha256']==expected_artifact
if peer['enabled']:
    assert outputs['archivePeerManifestSha256']==hashlib.file_digest(open(root/'webapp-optmized-astra/deploy/archive-peer-manifest.json','rb'),'sha256').hexdigest()
if runtime['signatureFileCacheEnabled']:
    assert outputs['signatureFileSha256']==hashlib.file_digest(open(root/runtime['signatureFileCachePath'],'rb'),'sha256').hexdigest()
print('Pulumi runtime, artifact, live processes and durable storage verified')
