import json,re,shlex,subprocess,statistics
from pathlib import Path
root=Path.cwd();source=(root/'webapp-optmized-astra/rust/src/user_read_api.rs').read_text()
query=next(q for q in re.findall(r'r#"(WITH candidate_ids.*?)"#',source,re.S) if '$5' in q)
statements=['BEGIN READ ONLY; SET LOCAL statement_timeout=1000;', "SELECT '%'||replace(replace(replace(normalized_username,'!','!!'),'%','!%'),'_','!_')||'%' AS pattern FROM astra_user_search ORDER BY user_id LIMIT 1;\\gset", 'PREPARE trial(text,text,text,text,bigint) AS '+query+';']
labels=[]
for mode in ['force_custom_plan','force_generic_plan']:
 statements.append('SET LOCAL plan_cache_mode='+mode+';')
 for label,pattern in [('one_user',":'pattern'"),('broad',"'%a%'"),('absent',"'%zznonexistent123456789zz%'")]:
  for iteration in range(5):
   statements.append("EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) EXECUTE trial('probe-viewer','','',"+pattern+",64);")
   labels.append((mode,label,iteration))
statements.append('ROLLBACK;')
# gset must execute the SELECT itself, without first printing its result.
sql='\n'.join(statements).replace("LIMIT 1;\\gset", "LIMIT 1\\gset")
command='printf %s '+shlex.quote(sql)+' | timeout 20s sudo -u postgres psql -XqAt -v ON_ERROR_STOP=1 -d isureal'
output=subprocess.check_output([str(root/'webapp-optmized-astra/deploy/ssh.sh'),'i-000e45b5dad0a1b39',command],text=True)
decoder=json.JSONDecoder();plans=[]
while output.strip():
 output=output.lstrip();p,n=decoder.raw_decode(output);plans.append(p[0]);output=output[n:]
assert len(plans)==len(labels)
rows=[]
for (mode,label,iteration),p in zip(labels,plans):
 nodes=[]
 def visit(n):
  nodes.append({'node':n['Node Type'],'rows':n['Actual Rows'],'estimated':n['Plan Rows'],'loops':n['Actual Loops'],'relation':n.get('Relation Name')})
  for child in n.get('Plans',[]):visit(child)
 visit(p['Plan']);rows.append(dict(mode=mode,pattern_kind=label,iteration=iteration,planning_ms=p['Planning Time'],execution_ms=p['Execution Time'],hit_blocks=p['Plan'].get('Shared Hit Blocks',0),read_blocks=p['Plan'].get('Shared Read Blocks',0),nodes=nodes))
path=root/'var/astra/search-plan-probe.json';path.write_text(json.dumps(rows,indent=2)+'\n')
for mode in ['force_custom_plan','force_generic_plan']:
 for label in ['one_user','broad','absent']:
  values=[r for r in rows if r['mode']==mode and r['pattern_kind']==label]
  print(json.dumps(dict(mode=mode,pattern=label,planning_ms=statistics.median(r['planning_ms'] for r in values),execution_ms=statistics.median(r['execution_ms'] for r in values),read_blocks=sum(r['read_blocks'] for r in values))))
