#!/usr/bin/env python3
"""Manual evidence generator for CW-20261001-0570, not a test/latency gate."""
import argparse, atexit, os, shutil, sqlite3, pathlib, tempfile, subprocess, re, json, statistics, uuid

parser = argparse.ArgumentParser(description='Disposable SQLite/Postgres list-plan and COUNT audit; never opens Torque operator data.')
parser.add_argument('--output', type=pathlib.Path, required=True)
args = parser.parse_args()
ROOT=pathlib.Path(__file__).resolve().parents[1]
M=ROOT/'internal/persistence/sqlstore/migrations'
TMP=pathlib.Path(tempfile.mkdtemp(prefix='torque0570-'))
atexit.register(shutil.rmtree, TMP)
container = 'torque-list-audit-' + uuid.uuid4().hex[:12]
probe_env = dict(os.environ, HOME=str(TMP))
subprocess.run(['docker','run','-d','--rm','--name',container,'--network','none','--tmpfs','/var/lib/postgresql/data','-e','POSTGRES_PASSWORD=audit','postgres:17-alpine'],check=True,env=probe_env,stdout=subprocess.DEVNULL)
atexit.register(lambda: subprocess.run(['docker','rm','-f',container],env=probe_env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL))
for _ in range(100):
    ready = subprocess.run(['docker','exec',container,'pg_isready','-U','postgres'],env=probe_env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    if ready.returncode == 0: break
    import time
    time.sleep(0.1)
else: raise RuntimeError('disposable PostgreSQL did not start')
db=sqlite3.connect(TMP/'audit.db')
for p in sorted(M.glob('*.sql')):
    if p.name > '034_run_cost_provenance.sql': continue
    db.executescript(p.read_text())
def pg(sql):
    p=subprocess.run(['docker','exec','-i',container,'psql','-X','-qAt','-U','postgres','-v','ON_ERROR_STOP=1'],input=sql,text=True,capture_output=True,env=probe_env)
    if p.returncode: raise RuntimeError(p.stderr[:4000])
    return p.stdout.strip()
N=4330
for table, n in [('projects',43),('epics',200),('sprints',400),('collections',40)]:
    for i in range(n):
        cols=['id','name']; vals=[f'{table}-{i:05}',f'Name {i%31:03}']
        if table in ('epics','sprints'): cols+=['project_id']; vals+= [f'projects-{i%43:05}']
        db.execute(f"INSERT INTO {table} ({','.join(cols)}) VALUES ({','.join('?' for x in cols)})",vals)
for i in range(N):
    dt=f'2026-09-{1+i%28:02} 12:{i%60:02}:00'
    db.execute('INSERT INTO tasks (id,title,description,status,priority,kind,project_id,epic_id,sprint_id,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)',(f't-{i:05}','Example task','Synthetic description '*12,['done','todo','doing','review'][i%4],i%5+1,['agent','issue','plan'][i%3],f'projects-{i%43:05}',f'epics-{i%200:05}',f'sprints-{i%400:05}',dt,dt))
    db.execute('INSERT INTO runs (id,task_id,status,started_at,ended_at,cost) VALUES (?,?,?,?,?,?)',(i+1,f't-{i:05}', ['done','running','failed','cancelled'][i%4],dt,dt[:-2]+f'{i%59+1:02}',i%100/100))
    db.execute('INSERT INTO cost_ledger (task_id,run_id,cost) VALUES (?,?,?)',(f't-{i:05}',i+1,i%100/100))
    db.execute('INSERT INTO artifacts (id,task_id,type,content,created_at) VALUES (?,?,?,?,?)',(i+1,f't-{i%43:05}','log','Synthetic evidence',dt))
    db.execute('INSERT INTO comments (id,entity_type,entity_id,author,content,created_at) VALUES (?,?,?,?,?,?)',(i+1,'task',f't-{i%43:05}',f'author-{i%7}','Synthetic comment',dt))
    db.execute('INSERT INTO sessions (id,state,task_id,project_id,created_at) VALUES (?,?,?,?,?)',(f's-{i:05}',['done','running','failed'][i%3],f't-{i%43:05}',f'projects-{i%43:05}',dt))
    db.execute('INSERT INTO messages (id,kind,from_kind,from_authority,from_id,from_urn,to_kind,to_authority,to_id,to_urn,thread_id,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)',(f'm-{i:05}','notice','agent','torque','sender','msg://agent/torque/sender','agent','torque','recipient','msg://agent/torque/recipient',f'thread-{i%43}',dt))
    db.execute('INSERT INTO checkpoints (task_id,correlation_id,type,payload_json,status,emitted_at,emitter_source_type) VALUES (?,?,?,?,?,?,?)',(f't-{i%43:05}',f'cp-{i}','message','{}',['pending','responded'][i%2],dt,'agent'))
    db.execute('INSERT INTO task_templates (id,version,name,description,kind) VALUES (?,?,?,?,?)',(f'tmpl-{i//3:05}',i%3+1,'Synthetic template','Description','agent'))
db.commit(); db.execute('ANALYZE')
# Equivalent typed PostgreSQL fixture, from the actual v034 SQLite schema.
tables={n:s for n,s in db.execute("SELECT name,sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")}
ddl=[]; made=set()
while tables:
    for name,sql in list(tables.items()):
        refs=set()
        sql=re.sub(r',\s*FOREIGN KEY\s*\([^)]+\)\s*REFERENCES\s+\w+\s*\([^)]+\)(?:\s+ON DELETE (?:SET NULL|CASCADE|RESTRICT|NO ACTION))?', '', sql, flags=re.I)
        sql=re.sub(r'REFERENCES\s+\w+\s*\(\w+\)(?:\s+ON DELETE (?:SET NULL|CASCADE|RESTRICT|NO ACTION))?', '', sql, flags=re.I)
        if refs-made: continue
        sql=re.sub(r'INTEGER PRIMARY KEY AUTOINCREMENT','BIGSERIAL PRIMARY KEY',sql,flags=re.I)
        sql=re.sub(r'\bDATETIME\b','TIMESTAMPTZ',sql,flags=re.I)
        sql=re.sub(r'\bBOOLEAN\b','INTEGER',sql,flags=re.I)
        sql=re.sub(r'\bBLOB\b','BYTEA',sql,flags=re.I)
        ddl.append(sql+';'); made.add(name); del tables[name]
        break
    else: raise RuntimeError('schema cycle')
pg('\n'.join(ddl))
# Transfer synthetic fixture rows, never the user's DB.
insert=[line for line in db.iterdump() if line.startswith('INSERT INTO') and not 'sqlite_' in line]
pg('\n'.join(insert))
existing=[s+';' for s, in db.execute("SELECT sql FROM sqlite_master WHERE type='index' AND sql IS NOT NULL")]
pg('\n'.join(existing)+'\nANALYZE;')

def key(col, postgres=False):
    if postgres or col.rsplit('.',1)[-1] not in ('created_at','updated_at','started_at','ended_at'): return col
    plain=f"replace(replace({col}, ' +0000 UTC', ''), 'Z', '')"
    return f"(replace(substr({plain}, 1, 19), 'T', ' ') || '.' || CASE WHEN substr({plain}, 20, 1) = '.' THEN substr(substr({plain}, 21) || '000000000', 1, 9) ELSE '000000000' END)"
duration=[f"COALESCE(CAST(ROUND((julianday({key('ended_at')})-julianday({key('started_at')}))*86400000) AS INTEGER), -1)","COALESCE(CAST(ROUND(EXTRACT(EPOCH FROM (ended_at-started_at))*1000) AS BIGINT), -1)"]
queries=[]
def add(table,label,exprs,order='ASC',prefix='',where='',indexed=True):
    name=f'idx_{table}_page_{label}'
    if isinstance(exprs,str): exprs=[key(exprs),key(exprs,True)]
    orders=[f'{prefix}{e} {order}, id ASC' for e in exprs]
    qs=[f"SELECT * FROM {table}"+(f' WHERE {where}' if where else '')+f' ORDER BY {o} LIMIT 51' for o in orders]
    queries.append({'label':table+'/'+label,'sql':qs,'index':name if indexed else None})
for table in ['tasks','projects','epics','sprints']:
    for col in (['priority','status','updated_at','created_at'] if table=='tasks' else ['name','status','updated_at','created_at']):
        for direction in ['ASC','DESC']:
            add(table,f'{col}_{direction.lower()}',col,direction,where='' if table=='tasks' else 'archived_at IS NULL')
for col in ['started_at','status','duration','cost']:
    for direction in ['ASC','DESC']:
        exprs=duration if col=='duration' else ['COALESCE(cost, 0)']*2 if col=='cost' else [key(col),key(col,True)]
        add('runs',f'{col}_{direction.lower()}',exprs,direction)
for col in ['status','project_id','epic_id','sprint_id','kind','parent_id']:
    value={'status':'todo','project_id':'projects-00001','epic_id':'epics-00001','sprint_id':'sprints-00001','kind':'issue','parent_id':'t-00001'}[col]
    if col=='parent_id':
        db.execute("UPDATE tasks SET parent_id='t-00001' WHERE id > 't-03000'");db.commit()
        pg("UPDATE tasks SET parent_id='t-00001' WHERE id > 't-03000';ANALYZE tasks;")
    add('tasks',col+'_priority',['priority']*2,prefix=col+', ',where=f"{col}='{value}'")
for table in ['epics','sprints']:
    add(table,'project_updated','updated_at','DESC',prefix='project_id, ',where="project_id='projects-00001' AND archived_at IS NULL")
for col,value in [('status','running'),('task_id','t-00001')]:
    add('runs',col+'_started',[key('started_at'),key('started_at',True)],'DESC',prefix=col+', ',where=f"{col}='{value}'")
for direction in ['ASC','DESC']:
    add('comments','created_'+direction.lower(),'created_at',direction)
    add('comments','entity_created_'+direction.lower(),'created_at',direction,prefix='entity_type, entity_id, ',where="entity_type='task' AND entity_id='t-00001'")
add('artifacts','task_created',['created_at']*2,prefix='task_id, ',where="task_id='t-00001'")
add('sessions','created',['created_at']*2,'DESC')
for col,value in [('state','running'),('project_id','projects-00001'),('task_id','t-00001')]:
    add('sessions',col+'_created',['created_at']*2,'DESC',prefix=col+', ',where=f"{col}='{value}'")
for col,value in [('to_urn','msg://agent/torque/recipient'),('thread_id','thread-1')]:
    add('messages',col+'_created',['created_at']*2,prefix=col+', ',where=f"{col}='{value}'")
# Other persisted list families: audit existing order; add only if uncovered.
add('collections','created',['created_at']*2,'DESC',where='archived_at IS NULL')
add('checkpoints','task_emitted',['emitted_at']*2,'DESC',prefix='task_id, ',where="task_id='t-00001'")
# Match actual id DESC tie-breaker in task checkpoint history.
queries[-1]['sql']=[s.replace('id ASC','id DESC') for s in queries[-1]['sql']]
add('checkpoints','pending_emitted',['emitted_at']*2,prefix='status, ',where="status='pending'")
queries += [{'label':'templates/existing_pk','sql':["SELECT * FROM task_templates WHERE is_archived=0 ORDER BY id ASC, version DESC LIMIT 51"]*2,'index':None}, {'label':'collection_tasks/existing_position','sql':["SELECT * FROM tasks WHERE collection_id='collections-00001' ORDER BY collection_position IS NULL, collection_position ASC, created_at ASC LIMIT 51"]*2,'index':None}, {'label':'tags/name','sql':["SELECT * FROM tags ORDER BY name COLLATE NOCASE ASC, slug ASC LIMIT 51", "SELECT * FROM tags ORDER BY lower(name) ASC, slug ASC LIMIT 51"],'index':None}]
queries.extend([
 {'label':'runs/project_cohort','sql':["SELECT r.* FROM runs r JOIN tasks t ON t.id=r.task_id WHERE t.project_id='projects-00001' ORDER BY "+key('r.started_at')+" DESC,r.id ASC LIMIT 51", "SELECT r.* FROM runs r JOIN tasks t ON t.id=r.task_id WHERE t.project_id='projects-00001' ORDER BY r.started_at DESC,r.id ASC LIMIT 51"],'index':None},
 {'label':'runs/status_time','sql':["SELECT * FROM runs WHERE status IN ('running','failed') AND "+key('started_at')+" >= '2026-09-15 12:00:00.000000000' ORDER BY "+key('started_at')+" DESC,id ASC LIMIT 51", "SELECT * FROM runs WHERE status IN ('running','failed') AND started_at >= '2026-09-15 12:00:00' ORDER BY started_at DESC,id ASC LIMIT 51"],'index':None},
 {'label':'comments/author_time','sql':["SELECT * FROM comments WHERE author='author-1' AND "+key('created_at')+" >= '2026-09-15 12:00:00.000000000' ORDER BY "+key('created_at')+" DESC,id ASC LIMIT 51", "SELECT * FROM comments WHERE author='author-1' AND created_at >= '2026-09-15 12:00:00' ORDER BY created_at DESC,id ASC LIMIT 51"],'index':None},
 {'label':'session_checkpoints/existing_scope','sql':["SELECT * FROM session_checkpoints WHERE session_id='s-00001' ORDER BY created_at DESC LIMIT 51"]*2,'index':None},
 {'label':'project_artifacts/existing_scope','sql':["SELECT * FROM project_artifacts WHERE project_id='projects-00001' ORDER BY file_path ASC,created_at ASC LIMIT 51"]*2,'index':None},
])

def plans(q):
    sqlite='; '.join(r[3] for r in db.execute('EXPLAIN QUERY PLAN '+q['sql'][0]))
    postgres=json.loads(pg('EXPLAIN (FORMAT JSON) '+q['sql'][1]))[0]['Plan']
    q['_postgres_plan'] = postgres
    def flatten(n):
        desc=n['Node Type']+((' '+n['Index Name']) if 'Index Name' in n else '')
        return [desc]+sum([flatten(c) for c in n.get('Plans',[])],[])
    return [sqlite,' → '.join(flatten(postgres))]

# Existing mixed-direction template/collection queries are covered by 035.
for q in queries:
    if q['label']=='templates/existing_pk': q['index']='idx_templates_page_id_version'
    if q['label']=='collection_tasks/existing_position': q['index']='idx_tasks_page_collection_position'
more=[]
for q in queries:
 table=q['label'].split('/')[0]; label=q['label'].split('/')[1]
 if table not in ('tasks','projects','epics','sprints','runs','comments') or q['index'] is None:continue
 # Existing indexed filtered cohorts have fixed leading columns; last sort key before id is the cursor key.
 if label=='existing_position':continue
 qq=[]
 for sql in q['sql']:
  order=sql.split(' ORDER BY ')[1].rsplit(', id ASC',1)[0]
  direction=order.rsplit(' ',1)[1];expr=order.rsplit(' ',1)[0]
  prefix={'status_priority':'status, ','project_id_priority':'project_id, ','epic_id_priority':'epic_id, ','sprint_id_priority':'sprint_id, ','kind_priority':'kind, ','parent_id_priority':'parent_id, ','project_updated':'project_id, ','status_started':'status, ','task_id_started':'task_id, ','entity_created_asc':'entity_type, entity_id, ','entity_created_desc':'entity_type, entity_id, '}.get(label,'')
  if prefix:expr=expr[len(prefix):]
  bound=3 if expr=='priority' else 0.5 if 'cost' in expr else 20000 if 'julianday' in expr or 'EXTRACT' in expr else "'2026-09-15 12:00:00'" if any(c in expr for c in ['created_at','updated_at','started_at']) else "'Name 015'" if expr=='name' else "'todo'"
  if any(c in expr for c in ['created_at','updated_at','started_at']) and 'julianday' not in expr and 'EXTRACT' not in expr and 'replace' in expr:bound="'2026-09-15 12:00:00.000000000'"
  idbound='2100' if table in ('runs','comments') else "'t-02000'" if table=='tasks' else "'"+table+"-00015'"
  predicate=f'({expr} {"<" if direction=="DESC" else ">"} {bound} OR ({expr} = {bound} AND id > {idbound}))'
  beforeorder=sql.split(' ORDER BY ')[0]
  qq.append(beforeorder+(' AND ' if ' WHERE ' in beforeorder else ' WHERE ')+predicate+' ORDER BY '+sql.split(' ORDER BY ')[1])
 cq={'label':q['label']+'/cursor','sql':qq,'index':q['index']};more.append(cq)
queries+=more

# Retain the merged /runs projection (including its extra computed sort value),
# aliases and cohort shape. Values are literals so EXPLAIN needs no bind layer.
run_source=(ROOT/'internal/persistence/sqlstore/run_query.go').read_text()
run_projection=re.search(r'q := `([^`]+)` \+ expr \+ from',run_source).group(1)
for q in queries:
    if not q['label'].startswith('runs/') or q['label']=='runs/facets_cost': continue
    label=q['label'].split('/')[1]
    for dialect,sql in enumerate(q['sql']):
        head,order=sql.split(' ORDER BY ')
        before_limit=order.rsplit(' LIMIT ',1)[0]
        expr_direction=before_limit.rsplit(',',1)[0]
        if label in ('status_started','task_id_started'):
            expr_direction=expr_direction.split(', ',1)[1]
        expr,direction=expr_direction.rsplit(' ',1)
        alias=lambda text: re.sub(r'(?<![\w.])\b(started_at|ended_at|status|cost|task_id|id)\b',r'r.\1',text)
        expr=alias(expr)
        from_where=head.split(' FROM ',1)[1]
        if from_where.startswith('runs r'): from_where=alias(from_where)
        else: from_where=alias(from_where.replace('runs','runs r',1))
        if ' WHERE ' in from_where:
            from_part,where=from_where.split(' WHERE ',1)
            from_where=from_part+' WHERE 1=1 AND '+where
        else: from_where+=' WHERE 1=1'
        from_where=from_where.replace("r.status='running'", "r.status IN ('running')")
        q['sql'][dialect]=run_projection+expr+' FROM '+from_where+' ORDER BY '+expr+' '+direction.lower()+', r.id ASC LIMIT 51'

queries.append({'label':'runs/facets_cost','sql':["SELECT COALESCE(SUM((SELECT SUM(l.cost) FROM cost_ledger l WHERE l.run_id=r.id)),0), COALESCE(SUM(r.prompt_tokens),0), COALESCE(SUM(r.completion_tokens),0) FROM runs r WHERE 1=1"]*2,'index':'idx_cost_run'})

# Use Torque's modernc SQLite driver for EXPLAIN, rather than Python's bundled
# SQLite planner. Python only prepares the synthetic fixture.
go_source = r'''package main
import("database/sql";"encoding/json";"os"; _ "modernc.org/sqlite")
func main(){db,e:=sql.Open("sqlite",os.Args[1]);must(e);defer db.Close();b,e:=os.ReadFile(os.Args[2]);must(e);var qs []string;must(json.Unmarshal(b,&qs));out:=[]string{};for _,q:=range qs{r,e:=db.Query("EXPLAIN QUERY PLAN "+q);must(e);s:="";for r.Next(){var a,b,c int;var d string;must(r.Scan(&a,&b,&c,&d));if s!=""{s+="; "};s+=d};must(r.Err());must(r.Close());out=append(out,s)};var v string;must(db.QueryRow("SELECT sqlite_version()").Scan(&v));must(json.NewEncoder(os.Stdout).Encode(map[string]any{"version":v,"plans":out}))}
func must(e error){if e!=nil{panic(e)}}
'''
(TMP/'plans.go').write_text(go_source)
# Preserve Go's existing caches while isolating probe HOME.
for k in ['GOMODCACHE','GOCACHE']:
    probe_env[k]=subprocess.check_output(['go','env',k],text=True).strip()
subprocess.run(['go','build','-o',str(TMP/'plans'),str(TMP/'plans.go')],cwd=ROOT,env=probe_env,check=True)
(TMP/'queries.json').write_text(json.dumps([q['sql'][0] for q in queries]))
def capture(phase):
    native=json.loads(subprocess.check_output([str(TMP/'plans'),str(TMP/'audit.db'),str(TMP/'queries.json')],text=True,env=probe_env))
    for i,q in enumerate(queries):
        q[phase]=[native['plans'][i],plans(q)[1]]
        q[phase+'_postgres']=q.pop('_postgres_plan')
    return native['version']
version=capture('before')
db.executescript((M/'035_list_sort_indexes.sql').read_text()); db.execute('ANALYZE')
pg((M/'postgres/035_list_sort_indexes.sql').read_text()+'\nANALYZE;')
capture('after')
cohorts=[('tasks/all','SELECT COUNT(*) FROM tasks'),('tasks/status','SELECT COUNT(*) FROM tasks WHERE status=\'todo\''),('tasks/project','SELECT COUNT(*) FROM tasks WHERE project_id=\'projects-00001\''),('tasks/page','SELECT * FROM tasks ORDER BY priority ASC,id ASC LIMIT 51'),('runs/all','SELECT COUNT(*) FROM runs'),('runs/status','SELECT COUNT(*) FROM runs WHERE status=\'running\''),('runs/project','SELECT COUNT(*) FROM runs r JOIN tasks t ON t.id=r.task_id WHERE t.project_id=\'projects-00001\''),('runs/page',run_projection+'r.started_at FROM runs r WHERE 1=1 ORDER BY r.started_at DESC,r.id ASC LIMIT 51'),('projects/all','SELECT COUNT(*) FROM projects'),('epics/all','SELECT COUNT(*) FROM epics'),('sprints/all','SELECT COUNT(*) FROM sprints')]
result=[]
for scale in [1,10]:
 if scale==10:
  for table in ['tasks','runs','projects','epics','sprints']:
   columns=[c[1] for c in db.execute(f'PRAGMA table_info({table})')]
   cols=','.join(columns);n=db.execute(f'SELECT COUNT(*) FROM {table}').fetchone()[0]
   values=','.join((f'id+{n}*g.n' if table=='runs' else "id||'-x'||g.n") if c=='id' else c for c in columns)
   pg(f'INSERT INTO {table} ({cols}) SELECT {values} FROM {table} CROSS JOIN generate_series(1,9) AS g(n);')
 pg('VACUUM ANALYZE;')
 for label,sql in cohorts:
  plans=[json.loads(pg('EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) '+sql))[0] for _ in range(8)]
  times=[p['Execution Time'] for p in plans[1:]];p=plans[-1];row={'scale':scale,'label':label,'sql':sql,'matching':int(pg(sql)) if 'COUNT' in sql else 51,'median_ms':statistics.median(times),'min_ms':min(times),'max_ms':max(times),'plan':p}
  result.append(row)

args.output.write_text(json.dumps({'sqlite_version':version,'postgres_version':pg('SELECT version();'),'fixture':{'tasks':4330,'runs':4330,'projects':43,'epics':200,'sprints':400,'comments':4330,'sessions':4330,'artifacts':4330,'messages':4330,'checkpoints':4330,'templates':4330,'collections':40,'cost_ledger':4330},'plans':queries,'counts':result},indent=2)+'\n')
