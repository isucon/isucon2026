"""Destructive integration checks; requires an isolated DB named astra_post_test."""
import concurrent.futures, json, os, subprocess, time
from pathlib import Path
from urllib.parse import urlparse
DSN=os.environ['ASTRA_POST_TEST_DSN']
assert urlparse(DSN).path == '/astra_post_test', 'Use the isolated astra_post_test database'
ROOT=Path(__file__).resolve().parents[1]
def query(sql, ok=True):
    p=subprocess.run(['psql',DSN,'-X','-v','ON_ERROR_STOP=1','-At'],input=sql,text=True,capture_output=True)
    if ok: assert p.returncode==0,p.stderr
    return p
query("DROP FUNCTION IF EXISTS astra_create_post(text,text,text,bytea,text,text,text,numeric,numeric,text);")
query((ROOT / 'sql/001-schema.sql').read_text())
query((ROOT / 'sql/005-create-post.sql').read_text())
query("INSERT INTO users(id,username,secret_key_hash,display_name,region) SELECT 'u'||n,'u'||n,repeat('0',64),'u'||n,'asia' FROM generate_series(1,20)n; INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES('w1','asia',current_date,now()-interval '1 minute',now()+interval '1 hour');")
def post(user, ident, caption=''):
    return query(f"SELECT (post_row).id || ':' || (window_row).id FROM astra_create_post('{user}','{ident}','im-{ident}',decode('ffd8ffd9','hex'),'{caption}','friends','none',NULL,NULL,NULL);",False)
with concurrent.futures.ThreadPoolExecutor(8) as pool:
    result=list(pool.map(lambda n:post('u'+str(n),'p'+str(n)),range(1,9)))
assert all(p.returncode==0 for p in result),[p.stderr for p in result]
with concurrent.futures.ThreadPoolExecutor(2) as pool:
    result=list(pool.map(lambda n:post('u9','race'+str(n)),range(2)))
assert sorted(p.returncode for p in result)==[0,3]
assert 'post_slot_unavailable' in next(p.stderr for p in result if p.returncode)
assert query("SELECT count(*) FROM image_objects WHERE owner_user_id='u9';").stdout.strip()=='1'
query("CREATE FUNCTION astra_injected_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.caption='fail' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$; CREATE TRIGGER astra_fail BEFORE INSERT ON posts FOR EACH ROW EXECUTE FUNCTION astra_injected_failure();")
assert post('u10','rollback','fail').returncode==3
assert query("SELECT count(*) FROM image_objects WHERE owner_user_id='u10';").stdout.strip()=='0'
query("DROP TRIGGER astra_fail ON posts;")
locker=subprocess.Popen(['psql',DSN+'?application_name=astra-post-lock-test','-X','-v','ON_ERROR_STOP=1','-At'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
locker.stdin.write("BEGIN; SELECT id FROM posting_windows WHERE id='w1' FOR UPDATE; SELECT pg_sleep(2); UPDATE posting_windows SET ended_at=clock_timestamp() WHERE id='w1'; INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES('w2','asia',current_date+1,clock_timestamp(),now()+interval '1 hour'); COMMIT;\n")
locker.stdin.close()
for _ in range(100):
    if query("SELECT count(*) FROM pg_stat_activity WHERE application_name='astra-post-lock-test' AND wait_event='PgSleep';").stdout.strip()=='1': break
    time.sleep(.01)
else: raise AssertionError('locker did not acquire lock')
result=post('u11','window-race')
assert result.returncode==0,result.stderr
assert result.stdout.strip()=='window-race:w2',result.stdout
assert locker.wait()==0
print(json.dumps({'parallel_posts':8,'same_user_duplicate':'one commit, one rejection','rollback':'no orphan image','concurrent_window_replacement':'new window selected','status':'PASS'}))
