from pathlib import Path
import json,re,subprocess,uuid,statistics
root=Path(__file__).resolve().parents[3];schema='astra_search_reset_'+uuid.uuid4().hex
query=re.search(r'r#"(WITH candidate_ids.*?)"#',(root/'webapp-optmized-astra/rust/src/user_read_api.rs').read_text(),re.S)[1].replace('$5','64')
def sql(q):
 return subprocess.check_output(['docker','exec','-i','astra-local-assets','psql','-XqAt','-U','postgres','-v','ON_ERROR_STOP=1'],input='SET search_path='+schema+',public;\n'+q,text=True,stderr=None)
setup=f'CREATE SCHEMA {schema}; CREATE TABLE users(id text PRIMARY KEY,username text,display_name text,status text); CREATE TABLE block_relations(blocker_id text,blocked_id text); CREATE INDEX user_names ON users(username,id);'
setup+=(root/'webapp-optmized-astra/sql/014-normalized-user-search.sql').read_text()+(root/'webapp-optmized-astra/sql/015-search-candidates.sql').read_text()
setup+="ALTER TABLE astra_search_dirty SET (autovacuum_enabled=false); INSERT INTO users SELECT i::text,'person_'||md5(i::text),'Display name '||i,'active' FROM generate_series(1,6000)i; INSERT INTO astra_user_search SELECT id,username,display_name,lower(username),lower(display_name) FROM users; ANALYZE; SET session_replication_role=replica; TRUNCATE astra_search_dirty,astra_user_search; INSERT INTO astra_search_dirty SELECT id FROM users; SET session_replication_role=origin; INSERT INTO astra_user_search SELECT id,username,display_name,lower(username),lower(display_name) FROM users; ANALYZE astra_user_search;"
results=[]
try:
 sql(setup)
 for stage in ['after_reset','after_analyze']:
  if stage=='after_analyze':sql('ANALYZE astra_search_dirty')
  stat=sql("SELECT json_build_object('estimated_rows',reltuples,'pages',relpages,'actual_rows',(SELECT count(*) FROM astra_search_dirty)) FROM pg_class WHERE oid='astra_search_dirty'::regclass")
  for i in range(3):
   p=json.loads(sql('PREPARE trial(text,text,text,text) AS '+query+"; EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) EXECUTE trial('viewer','','','%person!_c4ca4238%');"))[0]
   results.append(dict(stage=stage,iteration=i,statistics=json.loads(stat),plan=p))
finally:sql('DROP SCHEMA '+schema+' CASCADE')
Path(__file__).with_name('measurements.json').write_text(json.dumps(results,indent=2)+'\n')
for stage in ['after_reset','after_analyze']:
 rows=[r for r in results if r['stage']==stage]
 print(stage,rows[0]['statistics'],'median planning/execution',statistics.median(r['plan']['Planning Time'] for r in rows),statistics.median(r['plan']['Execution Time'] for r in rows))
