import json,subprocess,shlex
from pathlib import Path
import fcntl,importlib.util,os
root=Path(__file__).resolve().parents[3]
deploy=root/'webapp-optmized-astra/deploy'
os.environ.update(AWS_CONFIG_FILE=str(root/'provisioning/aws-sdk.config'),AWS_SDK_LOAD_CONFIG='1')
lock=open(root/'var/astra/operation.lock','w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
spec=importlib.util.spec_from_file_location('asset_gc',deploy/'asset-gc.py')
gc=importlib.util.module_from_spec(spec);spec.loader.exec_module(gc);gc.ensure_idle()
runtime=json.loads((deploy/'runtime.json').read_text())
queries={name:Path(__file__).with_name(name+'.sql').read_text() for name in ('old','adaptive')}

program='queries='+repr(queries)+'\n'+r'''
import json,subprocess,re

def sql(query):
 return subprocess.check_output(['sudo','-u','postgres','psql','-XqAt','-v','ON_ERROR_STOP=1','-d','isureal'],input=query,text=True)
users=json.loads(sql("""BEGIN READ ONLY; SET LOCAL statement_timeout='5s';
WITH authors AS (SELECT DISTINCT p.author_id AS id FROM posts p JOIN posting_windows w ON w.id=p.posting_window_id WHERE w.ended_at IS NULL AND p.deleted_at IS NULL),
counts AS (SELECT a.id,(SELECT count(*) FROM friendships f WHERE f.user_low_id=a.id)+(SELECT count(*) FROM friendships f WHERE f.user_high_id=a.id) AS n FROM authors a),
ranked AS (SELECT *,row_number() OVER(ORDER BY n DESC,id) AS rn,count(*) OVER() AS total FROM counts)
SELECT json_agg(row_to_json(r)) FROM (SELECT id,n FROM ranked WHERE rn IN (1,total/2,total))r; COMMIT;"""))
results=[]
for user in users:
 assert re.fullmatch('[a-f0-9-]{36}',user['id'])
 for feed,query in queries.items():
  for mode in ('force_custom_plan','force_generic_plan'):
   for iteration in range(2):
    statement="BEGIN READ ONLY; SET LOCAL statement_timeout='5s'; SET LOCAL plan_cache_mode="+mode+"; PREPARE trial(text) AS "+query+"; EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) EXECUTE trial('"+user['id']+"'); COMMIT;"
    plan=json.loads(sql(statement))[0]
    results.append(dict(friends=user['n'],viewer=user['id'],feed=feed,mode=mode,iteration=iteration,plan=plan))
print(json.dumps(results))
'''
result=subprocess.check_output([str(deploy/'ssh.sh'),runtime['database']['instanceId'],'python3 -c '+shlex.quote(program)],text=True)
Path('feed-plans.json').write_text(result)
rows=json.loads(result)
for row in rows:
 if row['iteration']==1: print(row['friends'],row['feed'],row['mode'],row['plan']['Planning Time'],row['plan']['Execution Time'])
