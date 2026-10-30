import json,os,re,shlex,subprocess
from pathlib import Path
root=Path(__file__).resolve().parents[3]; env=dict(os.environ,AWS_CONFIG_FILE=str(root/'provisioning/aws-sdk.config'),AWS_SDK_LOAD_CONFIG='1')
ssh=[str(root/'webapp-optmized-astra/deploy/ssh.sh'),'i-000e45b5dad0a1b39']
import fcntl
operation_lock = (root/'var/astra/operation.lock').open('a')
fcntl.flock(operation_lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
def remote(sql):
 r=subprocess.run(ssh+['sudo -u postgres psql -XAt -v ON_ERROR_STOP=1 -d isureal -c '+shlex.quote(sql)],env=env,text=True,capture_output=True,check=True)
 return r.stdout.strip()
viewer=remote("SELECT id FROM (SELECT user_low_id id,count(*) n FROM friendships GROUP BY 1 UNION ALL SELECT user_high_id,count(*) FROM friendships GROUP BY 1) f GROUP BY id ORDER BY sum(n) DESC LIMIT 1")
assert re.fullmatch('[0-9a-f-]{36}',viewer)
old=subprocess.check_output(['git','show','81a069d3:webapp-optmized-astra/rust/src/feed_api.rs'],text=True,cwd=root)
new=subprocess.check_output(['git','show','b53f0973:webapp-optmized-astra/rust/src/feed_api.rs'],text=True,cwd=root)
def constant(s,name): return re.search(r'const '+name+r': &str = r#"(.*?)"#;',s,re.S)[1]
vr_old=constant(old,'VIEWER_RELATIONS')
vr_new=constant(new,'VIEWER_AND_FRIENDS')+', friends_of_friends AS MATERIALIZED (SELECT path.id,count(*) AS n FROM ('+constant(new,'TWO_HOP_PATHS')+') path GROUP BY path.id)'
# Isolate actual relationship scoring for all new-run post authors in this viewer's two-hop graph.
# This intentionally omits feed visibility and pagination and is not an end-to-end API timing.
candidates="SELECT DISTINCT p.id,p.author_id FROM posts p WHERE p.created_at>(SELECT created_at FROM initial_data_boundary) AND p.author_id IN (SELECT id FROM friends_of_friends)"
for name,s,vr in [('old',old,vr_old),('bounded',new,vr_new)]:
 ranking=constant(s,'RANKING_RELATIONS').split('), engaged AS (')[0]+')'
 q=(vr+ranking.replace('{CANDIDATES}',candidates)+' SELECT count(*),sum(least(30,coalesce(n,0))) FROM connection').replace('$1',"'"+viewer+"'")
 results=[]
 for i in range(3):
  raw=remote('BEGIN READ ONLY; SET LOCAL statement_timeout=\'15s\'; EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) '+q+'; ROLLBACK;')
  start=raw.index('[');end=raw.rindex(']')+1;plan=json.loads(raw[start:end])[0]
  results.append(plan)
 out=json.dumps(results,indent=2)
 out=re.sub(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}','<viewer-id>',out)
 Path('/tmp/team12-feed-'+name+'-plans.json').write_text(out+'\n')
 print(name,[x['Execution Time'] for x in results],flush=True)
