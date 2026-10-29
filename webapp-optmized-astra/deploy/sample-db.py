"""Sample active application queries without changing PostgreSQL configuration.

Run from the repository root: python3 .../sample-db.py OUTPUT.json SECONDS.
Only SQL statement shapes and wait categories are saved; quoted literals are
redacted. This read-only diagnostic uses the existing Team 12 database VM.
"""
import collections,json,subprocess,shlex,sys
seconds=int(sys.argv[2])
assert 1 <= seconds <= 300
runtime=json.load(open('webapp-optmized-astra/deploy/runtime.json'))
query="""SELECT coalesce(json_agg(row_to_json(s)),'[]'::json) FROM (
SELECT left(regexp_replace(query,$q$'[^']*'$q$,'?','g'),1200) AS query,
coalesce(wait_event_type,'CPU') AS wait,count(*) AS n,
max(extract(epoch FROM clock_timestamp()-query_start)) AS age
FROM pg_stat_activity WHERE datname=current_database() AND usename='isureal'
AND state='active' AND pid<>pg_backend_pid()
GROUP BY 1,2) s"""
command='printf %s '+shlex.quote(query+';\n\\watch 0.5\n')+' | timeout '+str(seconds)+' sudo -u postgres psql -XAt -d isureal'
result=subprocess.run(['webapp-optmized-astra/deploy/ssh.sh',runtime['database']['instanceId'],command],text=True,capture_output=True)
assert result.returncode in (0,124),result.stderr
counts=collections.Counter();max_age={};samples=0
for line in result.stdout.splitlines():
 if not line.startswith('['):continue
 rows=json.loads(line);samples+=1
 for row in rows:
  key=row['wait'],row['query'];counts[key]+=row['n'];max_age[key]=max(max_age.get(key,0),row['age'])
output={'samples':samples,'interval_seconds':.5,'queries':[{'wait':key[0],'query':key[1],'active_connection_samples':count,'max_observed_age_seconds':max_age[key]} for key,count in counts.most_common()]}
open(sys.argv[1],'w').write(json.dumps(output,ensure_ascii=False,indent=2)+'\n')
print('Recorded',samples,'samples;',len(counts),'query/wait combinations')
