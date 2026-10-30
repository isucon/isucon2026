"""Build identical optional layouts on existing Team 12 application VMs."""
import concurrent.futures,fcntl,importlib.util,json,os,shlex,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2];DEPLOY=Path(__file__).resolve().parent
runtime=json.loads((DEPLOY/'runtime.json').read_text());config=runtime['ownerArchive']
assert config['root']=='/var/lib/astra-owner-images'
assert {a['instanceId'] for a in runtime['applications']}=={'i-055f64431d3006a5f','i-0a062905e0bb9ebb7','i-03c92149bdc1fcfe1'}
assert runtime['database']['instanceId']=='i-000e45b5dad0a1b39'
work=ROOT/'var/astra';work.mkdir(parents=True,exist_ok=True)
lock=(work/'operation.lock').open('w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
spec=importlib.util.spec_from_file_location('asset_gc',DEPLOY/'asset-gc.py');gc=importlib.util.module_from_spec(spec);spec.loader.exec_module(gc);gc.ensure_idle()
ssh=str(DEPLOY/'ssh.sh')
source=work/'owner-archive-source.tsv'
query="COPY (SELECT DISTINCT i.owner_user_id,i.asset_key FROM image_objects i JOIN posts p ON p.image_id=i.id WHERE p.deleted_at IS NULL AND p.created_at<=(SELECT created_at FROM initial_data_boundary) AND i.created_at<=(SELECT created_at FROM initial_data_boundary) AND i.asset_key LIKE 'pack:%' ORDER BY 1,2) TO STDOUT"
fd=os.open(source,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
with os.fdopen(fd,'w') as out:subprocess.run([ssh,runtime['database']['instanceId'],'sudo -u postgres psql -XqAt -v ON_ERROR_STOP=1 -d isureal -c '+shlex.quote(query)],stdout=out,check=True)
def prepare(app):
 instance=app['instanceId']
 for local,remote in [(DEPLOY/'build-owner-archive.py','/home/isucon/build-owner-archive.py'),(source,'/home/isucon/.astra-owner-source.tsv')]:
  subprocess.run([ssh,instance,'--copy',str(local),remote],check=True,stdout=subprocess.DEVNULL)
 command='sudo install -d -o isucon -g isucon -m 0755 /var/lib/astra-owner-images && chmod 600 /home/isucon/.astra-owner-source.tsv && python3 /home/isucon/build-owner-archive.py /home/isucon/.astra-owner-source.tsv /var/lib/astra-images/packs /var/lib/astra-owner-images'
 report=json.loads(subprocess.check_output([ssh,instance,command],text=True))
 (work/('owner-archive-'+app['region']+'.json')).write_text(json.dumps(report,indent=2)+'\n')
 print(json.dumps({'region':app['region'],'images':report['images'],'bytes':report['bytes'],'index_sha256':report['index_sha256']}),flush=True)
 return report
with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:reports=list(pool.map(prepare,runtime['applications']))
assert all(report==reports[0] for report in reports),'layout outputs differ between VMs'
(work/'owner-archive-report.json').write_text(json.dumps(reports[0],indent=2)+'\n')
print('Verified identical owner archives on all three application VMs',flush=True)
