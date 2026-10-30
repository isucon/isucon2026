import hashlib,json,os,subprocess,uuid
from pathlib import Path
folder=Path(__file__).resolve().parent
port=os.environ.get('ASTRA_TEST_PGPORT','26210');assert port.isdigit()
base=['psql','-XqAt','-v','ON_ERROR_STOP=1','-h','127.0.0.1','-p',port,'-U','postgres','-d','postgres']
schema='astra_notice_plan_'+uuid.uuid4().hex
old=(folder/'baseline.sql').read_text()
new=(folder.parents[1]/'rust/src/notification_query.sql').read_text()
def sql(q):return subprocess.check_output(base,input=q,text=True)
def run(q):return sql('SET search_path TO '+schema+';\n'+q)
def arr(items,kind='text'):return 'ARRAY['+','.join("'"+str(x).replace("'","''")+"'" for x in items)+']::'+kind+'[]'
def ident(n):return str(uuid.UUID(hashlib.md5(str(n).encode()).hexdigest()))
ids=[ident(i) for i in range(1,513)]
rows=[]
try:
 sql('CREATE SCHEMA '+schema)
 run('''CREATE TABLE users(id varchar(36) PRIMARY KEY,region text,status text,secret_key_hash text);
 CREATE TABLE posting_windows(id varchar(36) PRIMARY KEY,region text,ended_at timestamptz);
 CREATE UNIQUE INDEX open_region ON posting_windows(region) WHERE ended_at IS NULL;
 CREATE TABLE astra_notification_batches(posting_window_id varchar(36) PRIMARY KEY);
 CREATE TABLE notifications(id varchar(36) PRIMARY KEY,user_id varchar(36),posting_window_id varchar(36),created_at timestamptz,UNIQUE(user_id,posting_window_id));
 INSERT INTO users SELECT md5(i::text)::uuid::text,'asia','active',repeat('ab',32) FROM generate_series(1,30000) i;
 INSERT INTO posting_windows VALUES ('batch','asia',NULL),('legacy','europe',NULL),('ended','america','2026-01-02');
 INSERT INTO astra_notification_batches VALUES ('batch');
 INSERT INTO notifications SELECT md5('n:'||id)::uuid::text,id,'legacy','2026-01-02' FROM users;
 ANALYZE users; ANALYZE posting_windows; ANALYZE astra_notification_batches; ANALYZE notifications;''')
 for scenario in ['mixed','all_batched']:
  if scenario=='all_batched':run("INSERT INTO astra_notification_batches VALUES ('legacy'); ANALYZE astra_notification_batches;")
  for candidate in [False,True]:
   args=[arr(ids),arr(['asia']*512),arr(['2026-01-01' if candidate else '2026-01-03']*512,'timestamptz'),arr(range(1,513) if candidate else [],'bigint'),arr([ident('c:'+x) for x in ids] if candidate else []),arr(['batch']*512 if candidate else []),arr(['2026-01-02']*512 if candidate else [],'timestamptz')]
   actual=[]
   for mode in ['force_custom_plan','force_generic_plan']:
    for label,query in [('old',old),('new',new)]:
     prefix='SET plan_cache_mode='+mode+'; PREPARE trial(text[],text[],timestamptz[],bigint[],text[],text[],timestamptz[]) AS '+query+'; '
     statement='EXECUTE trial('+','.join(args)+')'
     result=run(prefix+statement+';')
     actual.append(sorted(result.splitlines()))
     for iteration in range(3):
      plan=json.loads(run(prefix+'EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) '+statement+';'))[0]
      rows.append({'scenario':scenario,'candidate':candidate,'mode':mode,'query':label,'iteration':iteration,'execution_ms':plan['Execution Time'],'planning_ms':plan['Planning Time'],'shared_hit_blocks':plan['Plan']['Shared Hit Blocks']})
   assert all(r==actual[0] for r in actual)
finally:sql('DROP SCHEMA IF EXISTS '+schema+' CASCADE')
print(json.dumps({'users':30000,'recipients':512,'all_results_match':True,'runs':rows},indent=2))
