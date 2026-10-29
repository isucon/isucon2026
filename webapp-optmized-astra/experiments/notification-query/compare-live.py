import json,shlex,subprocess
from pathlib import Path
folder=Path(__file__).resolve().parent
queries={'old':(folder/'baseline.sql').read_text(),'new':(folder.parents[1]/'rust/src/notification_query.sql').read_text()}
program='queries='+repr(queries)+'\n'+r'''
import json,subprocess,hashlib

def sql(query):
 return subprocess.check_output(['sudo','-u','postgres','psql','-XqAt','-v','ON_ERROR_STOP=1','-d','isureal'],input=query,text=True)
def arr(items,kind='text'):
 return 'ARRAY['+','.join("'"+str(x).replace("'","''")+"'" for x in items)+']::'+kind+'[]'
rows=json.loads(sql("""BEGIN READ ONLY; SET LOCAL statement_timeout='5s';
 SELECT json_agg(row_to_json(q)) FROM (
 SELECT u.id AS user_id,u.region,n.notification_id,b.posting_window_id,b.created_at::text,
 (b.created_at-interval '1 second')::text AS connected_before,
 (b.created_at+interval '1 second')::text AS connected_after
 FROM astra_notification_batches b JOIN posting_windows w ON w.id=b.posting_window_id AND w.ended_at IS NULL AND w.region='asia'
 CROSS JOIN LATERAL unnest(b.user_ids,b.notification_ids) AS n(user_id,notification_id)
 JOIN users u ON u.id=n.user_id AND u.status='active' AND u.region=w.region
 ORDER BY u.id LIMIT 512) q; COMMIT;"""))
assert rows and len(rows)==512
keep={'Node Type','Join Type','Relation Name','Index Name','Actual Startup Time','Actual Total Time','Actual Rows','Actual Loops','Shared Hit Blocks','Shared Read Blocks','Rows Removed by Filter','Rows Removed by Join Filter','Plans'}
def compact(node):
 return {key:([compact(p) for p in value] if key=='Plans' else value) for key,value in node.items() if key in keep}
results=[]
for candidate in (False,True):
 args=[arr([r['user_id'] for r in rows]),arr([r['region'] for r in rows]),arr([r['connected_before' if candidate else 'connected_after'] for r in rows],'timestamptz'),arr(range(1,513) if candidate else [],'bigint'),arr([r['notification_id'] for r in rows] if candidate else []),arr([r['posting_window_id'] for r in rows] if candidate else []),arr([r['created_at'] for r in rows] if candidate else [],'timestamptz')]
 digests=[]
 for mode in ('force_custom_plan','force_generic_plan'):
  for name,query in queries.items():
   prefix="BEGIN READ ONLY; SET LOCAL statement_timeout='5s'; SET LOCAL plan_cache_mode="+mode+'; PREPARE trial(text[],text[],timestamptz[],bigint[],text[],text[],timestamptz[]) AS '+query+'; '
   execution='EXECUTE trial('+','.join(args)+')'
   result=sql(prefix+execution+'; COMMIT;')
   digests.append(hashlib.sha256('\n'.join(sorted(result.splitlines())).encode()).hexdigest())
   for iteration in range(3):
    plan=json.loads(sql(prefix+'EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) '+execution+'; COMMIT;'))[0]
    results.append({'candidate':candidate,'mode':mode,'query':name,'iteration':iteration,'planning_ms':plan['Planning Time'],'execution_ms':plan['Execution Time'],'plan':compact(plan['Plan'])})
 assert len(set(digests))==1
print(json.dumps({'recipients':len(rows),'all_results_match':True,'runs':results}))
'''
output=subprocess.check_output(['webapp-optmized-astra/deploy/ssh.sh','i-0ecc0569db071b251','python3 -c '+shlex.quote(program)],text=True)
print(json.dumps(json.loads(output),indent=2))
