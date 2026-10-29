from pathlib import Path
import json,re,subprocess,uuid,statistics
import argparse
parser=argparse.ArgumentParser()
parser.add_argument('--container',default='astra-local-assets')
parser.add_argument('--output',type=Path,default=Path('search-plans.json'))
args=parser.parse_args()
root=Path(__file__).resolve().parents[3]
schema='astra_search_perf_'+uuid.uuid4().hex
container=args.container
def sql(source):
 return subprocess.check_output(['docker','exec','-i',container,'psql','-XqAt','-U','postgres','-v','ON_ERROR_STOP=1'],input='SET search_path='+schema+',public;\n'+source,text=True,stderr=subprocess.DEVNULL)
old=Path(__file__).with_name('baseline.sql').read_text().strip()
new=(root/'webapp-optmized-astra/rust/src/user_read_api.rs').read_text();new=re.search(r'r#"(WITH candidate_ids.*?)"#',new,re.S)[1].replace('$5','64')
setup=f'CREATE SCHEMA {schema}; CREATE TABLE users(id text PRIMARY KEY,username text,display_name text,status text); CREATE TABLE block_relations(blocker_id text,blocked_id text); CREATE INDEX user_names ON users(username,id);'
setup+=(root/'webapp-optmized-astra/sql/014-normalized-user-search.sql').read_text()+(root/'webapp-optmized-astra/sql/015-search-candidates.sql').read_text()
setup+="INSERT INTO users SELECT i::text,'person_'||md5(i::text),'Display name '||i,'active' FROM generate_series(1,20000)i; INSERT INTO astra_user_search SELECT id,username,display_name,lower(username),lower(display_name) FROM users; ANALYZE;"
results=[]
try:
 sql(setup)
 for pattern in ['%person!_c4ca4238%','%person%','%a%','%no-such-name%']:
  for mode in ['force_custom_plan','force_generic_plan']:
   for iteration in range(3):
    for name,query in ([('old',old),('indexed',new)] if iteration%2 else [('indexed',new),('old',old)]):
     plan=json.loads(sql('SET plan_cache_mode='+mode+'; PREPARE trial(text,text,text,text) AS '+query+"; EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) EXECUTE trial('viewer','','','"+pattern+"');"))[0]
     results.append(dict(pattern=pattern,mode=mode,iteration=iteration,implementation=name,plan=plan))
finally:
 sql(f'DROP SCHEMA {schema} CASCADE')
args.output.write_text(json.dumps(results,indent=2)+'\n')
for pattern in ['%person!_c4ca4238%','%person%','%a%','%no-such-name%']:
 for mode in ['force_custom_plan','force_generic_plan']:
  print(pattern,mode, {name:round(statistics.median(x['plan']['Execution Time'] for x in results if x['pattern']==pattern and x['mode']==mode and x['implementation']==name),3) for name in ['old','indexed']})
