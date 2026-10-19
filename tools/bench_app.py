"""Measure local packaged CLI startup; no provider launches, network or accounts.

Seed an isolated, nonexistent home using the app test executable:
  python tools/bench_app.py --seed-helper PATH_TO_APP_TEST --home NEW_ABSOLUTE_HOME --seed-size large --evidence DIR
Then compare normal empty-home and seeded-history runs with the same binary:
  python tools/bench_app.py --binary PATH --trials 30 --evidence SMALL_DIR
  python tools/bench_app.py --binary PATH --home SEEDED_HOME --trials 30 --evidence LARGE_DIR
  python tools/bench_app.py --binary PATH --home SEEDED_HOME --search TEXT --trials 30 --evidence SEARCH_DIR
The seeded home is retained. Only a complete synthetic fixture is accepted by
--home; this tool never accepts an ordinary user state home.
Search preflight validates the first page and one advertised continuation before
timing repeated first-page queries. Evidence retains counts/cursors, not bodies.
"""
import argparse
from contextlib import nullcontext
import hashlib
import json
import os
from pathlib import Path
import platform
import random
import shutil
import statistics
import subprocess
import tempfile
import time

ROOT=Path(__file__).resolve().parents[1]
FLAGS=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0
MARKER='.ucli-timing-fixture.json'
MAX_FIXTURE_BYTES=500*1024*1024
MIN_FREE_BYTES=1024*1024*1024
SEARCH_LIMIT=20

def search_text(value):
    try:
        valid='\x00' not in value and len(value.encode('utf-8'))<=256
    except UnicodeEncodeError:
        valid=False
    if not valid: raise argparse.ArgumentTypeError('--search needs literal UTF-8 text without NUL, at most 256 bytes.')
    return value

def search_page(output, query, cursor=''):
    """Validate the public summary envelope without retaining task/draft bodies."""
    try:
        replies=[json.loads(line) for line in output.decode('utf-8').splitlines()]
    except (UnicodeError,ValueError):
        raise ValueError('task_search returned malformed JSONL.') from None
    if len(replies)!=1 or not isinstance(replies[0],dict): raise ValueError('task_search needs one reply.')
    reply=replies[0]
    if reply.get('version')!=1 or reply.get('type')!='tasks': raise ValueError('task_search reply type/version differs.')
    tasks,pagination=reply.get('data'),reply.get('pagination')
    if not isinstance(tasks,list) or len(tasks)>SEARCH_LIMIT: raise ValueError('task_search needs a bounded summary array.')
    if (not isinstance(pagination,dict) or type(pagination.get('limit')) is not int or
            pagination.get('limit')!=SEARCH_LIMIT or pagination.get('search')!=query or
            pagination.get('cursor')!=cursor or not isinstance(pagination.get('next_cursor'),str)):
        raise ValueError('task_search pagination differs from the request.')
    next_cursor=pagination['next_cursor']
    if len(next_cursor)>2048 or (next_cursor and (len(tasks)!=SEARCH_LIMIT or next_cursor==cursor)):
        raise ValueError('task_search continuation is inconsistent with its page.')
    fold=lambda value: value.translate(str.maketrans('ABCDEFGHIJKLMNOPQRSTUVWXYZ','abcdefghijklmnopqrstuvwxyz'))
    ids=[]
    for task in tasks:
        if (not isinstance(task,dict) or set(task)!={'id','title','revision','workspace','status','has_draft','updated_at'} or
                not all(isinstance(task.get(key),str) and task[key] for key in ('id','title','status','updated_at')) or
                type(task.get('revision')) is not int or task['revision']<1 or type(task.get('has_draft')) is not bool or
                not isinstance(task.get('workspace'),dict) or
                not all(isinstance(task['workspace'].get(key),str) and task['workspace'][key] for key in ('id','root','directory'))):
            raise ValueError('task_search returned an invalid summary or unexpected body fields.')
        if fold(query) not in fold(task['title']) and fold(query) not in fold(task['id']):
            raise ValueError('task_search returned a nonmatching literal title/ID.')
        ids.append(task['id'])
    if len(set(ids))!=len(ids): raise ValueError('task_search returned duplicate task IDs.')
    return {'returned_count':len(tasks),'next_cursor':next_cursor},set(ids)

def check_search_continuation(first_ids, second_ids):
    if not second_ids or first_ids & second_ids:
        raise ValueError('task_search continuation is empty or repeats the first page.')

def sha(path):
    h=hashlib.sha256()
    with path.open('rb') as f:
        while chunk:=f.read(1024*1024): h.update(chunk)
    return h.hexdigest()

def fixture_metadata(home):
    """Validate completion/identity before a benchmark can open a supplied DB."""
    if not home.is_absolute() or home.is_symlink(): raise ValueError('Fixture home must be an absolute unlinked path.')
    home=home.resolve(strict=True)
    marker=home/MARKER
    if marker.is_symlink() or not marker.is_file() or marker.stat().st_size>65536:
        raise ValueError('Only a completed synthetic timing fixture may be used with --home.')
    m=json.loads(marker.read_text(encoding='utf-8'))
    if (m.get('version'),m.get('kind'),m.get('status'))!=(1,'ucli-synthetic-timing','complete'):
        raise ValueError('Partial or unrecognized fixture; retained for inspection, not benchmarked.')
    if Path(m.get('home','')).resolve()!=home: raise ValueError('Fixture marker belongs to another home.')
    expected={'small':(10,2,4096),'large':(10000,40,4*1024*1024)}.get(m.get('size'))
    if expected is None or (m.get('tasks'),m.get('history_attempts'),m.get('output_bytes_per_attempt'))!=expected:
        raise ValueError('Unrecognized synthetic fixture shape.')
    if m.get('draft_bytes')!=256 or m.get('draft_tasks')!=expected[0]-1 or m.get('total_output_bytes')!=expected[1]*expected[2]:
        raise ValueError('Fixture counts disagree.')
    if m.get('fixture_max_bytes')!=MAX_FIXTURE_BYTES or m.get('observed_free_bytes_before_seed',0)<MIN_FREE_BYTES:
        raise ValueError('Fixture lacks bounded size/free-space evidence.')
    total=0
    for path in home.rglob('*'):
        if path.is_symlink(): raise ValueError('Linked entry in synthetic fixture.')
        if path.is_file(): total+=path.stat().st_size
    if total>MAX_FIXTURE_BYTES: raise ValueError('Fixture exceeds 500 MiB.')
    db=home/'state/state.db'
    if not db.is_file() or db.stat().st_size!=m.get('database_bytes') or sha(db)!=m.get('database_sha256'):
        raise ValueError('Fixture database differs from its completed seed receipt.')
    return m | {'observed_fixture_files_bytes':total}

def seed_fixture(args, evidence):
    if not args.home or not args.home.is_absolute(): raise ValueError('Seeding needs --home with a new absolute path.')
    home=args.home
    if home.exists() or home.is_symlink(): raise ValueError('Seeding refuses every existing home, including partial/empty homes.')
    if not home.parent.is_dir(): raise ValueError('Create the fixture parent directory first; the home itself must not exist.')
    helper=args.seed_helper.resolve(strict=True)
    free=shutil.disk_usage(home.parent).free
    if free<MIN_FREE_BYTES: raise ValueError('Seeding needs at least 1 GiB free disk space for the 500 MiB ceiling.')
    env=os.environ.copy(); env.pop('UCLI_HOME',None)
    env.update(UCLI_TIMING_SEED=args.seed_size,UCLI_TIMING_HOME=str(home),UCLI_TIMING_FREE_BYTES=str(free))
    command=[str(helper),'-test.run=^TestTimingFixtureProcess$','-test.v','-test.timeout=10m']
    receipt={'scope':'synthetic startup fixture only; no runtime or provider execution','command':command,
        'helper_sha256':sha(helper),'home':str(home),'size':args.seed_size,'free_bytes_before_seed':free,
        'fixture_ceiling_bytes':MAX_FIXTURE_BYTES,'failure':None,'exit_code':1,
        'cleanup':'seeded or partial home retained; never deleted or reused automatically'}
    try:
        proc=subprocess.run(command,stdin=subprocess.DEVNULL,capture_output=True,cwd=ROOT,
            env=env,creationflags=FLAGS,timeout=610)
        (evidence/'seed.stdout.txt').write_bytes(proc.stdout)
        (evidence/'seed.stderr.txt').write_bytes(proc.stderr)
        if proc.returncode: raise RuntimeError(f'Seed helper exit {proc.returncode}; partial home retained.')
        receipt['fixture']=fixture_metadata(home)
        receipt['exit_code']=0
    except Exception as error:
        receipt['failure']=repr(error)
    (evidence/'seed-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8')
    print(json.dumps(receipt,indent=2))
    return receipt['exit_code']

def self_test():
    """Guard negatives need no CLI/helper; the Go test proves public-API seeding."""
    with tempfile.TemporaryDirectory(prefix='ucli-timing-guards-') as scratch:
        home=Path(scratch)
        for marker in (None,{'version':1,'kind':'ucli-synthetic-timing','status':'seeding'}):
            if marker is not None: (home/MARKER).write_text(json.dumps(marker),encoding='utf-8')
            try: fixture_metadata(home)
            except ValueError: pass
            else: raise AssertionError('ordinary/partial home was accepted')
        args=argparse.Namespace(home=home)
        try: seed_fixture(args,home)
        except ValueError: pass
        else: raise AssertionError('existing home was accepted for seeding')
        assert list(home.iterdir())==[home/MARKER], 'negative guards changed existing state'
    for value in ('', 'literal %_\\', '\u00e9'*128): assert search_text(value)==value
    for value in ('x\x00y','x'*257,'\u00e9'*129,'\ud800'):
        try: search_text(value)
        except argparse.ArgumentTypeError: pass
        else: raise AssertionError('invalid search accepted')
    def reply(count=0, next_cursor='', cursor='', query='needle'):
        return {'version':1,'type':'tasks','data':[{'id':f'task-{i}','title':'Needle title','revision':1,
            'workspace':{'id':'workspace','root':'synthetic','directory':'synthetic'},
            'status':'draft','has_draft':True,'updated_at':'2026-10-18T00:00:00Z'} for i in range(count)],
            'pagination':{'limit':SEARCH_LIMIT,'search':query,'cursor':cursor,'next_cursor':next_cursor}}
    encode=lambda value: (json.dumps(value)+'\n').encode()
    assert search_page(encode(reply()),'needle')[0]=={'returned_count':0,'next_cursor':''}
    assert search_page(encode(reply(1,query='')),'')[0]['returned_count']==1
    meta,ids=search_page(encode(reply(20,'opaque-next')),'needle')
    assert meta=={'returned_count':20,'next_cursor':'opaque-next'} and len(ids)==20
    assert search_page(encode(reply(1,cursor='opaque-next')),'needle','opaque-next')[0]['returned_count']==1
    negatives=[reply(21),reply(0,'opaque-next'),reply(1,'opaque-next'),reply(20,'same','same'),reply(query='other')]
    for key,value in (('limit',19),('limit',True),('cursor','unexpected'),('next_cursor',None)):
        bad=reply(); bad['pagination'][key]=value; negatives.append(bad)
    bad=reply(); bad['data']={}; negatives.append(bad)
    bad=reply(1); bad['data'][0]['draft']='private body'; negatives.append(bad)
    bad=reply(1); bad['data'][0]['title']='not a match'; negatives.append(bad)
    bad=reply(2); bad['data'][1]['id']=bad['data'][0]['id']; negatives.append(bad)
    for bad in negatives:
        try: search_page(encode(bad),'needle',bad['pagination']['cursor'] if bad['pagination']['cursor']=='same' else '')
        except ValueError: pass
        else: raise AssertionError('invalid search result accepted')
    for malformed in (b'not json',encode(reply())+encode(reply()),b'\xff'):
        try: search_page(malformed,'needle')
        except ValueError: pass
        else: raise AssertionError('malformed/multiple replies accepted')
    check_search_continuation({'first'},{'second'})
    for second in (set(),{'first'}):
        try: check_search_continuation({'first'},second)
        except ValueError: pass
        else: raise AssertionError('invalid continuation accepted')
    print('timing fixture/search guard self-test: passed')
    return 0

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary',type=Path)
    p.add_argument('--evidence',type=Path,default=ROOT/'.build/app-timing')
    p.add_argument('--trials',type=int,default=100)
    p.add_argument('--home',type=Path,help='Completed synthetic fixture only; never a user state home.')
    p.add_argument('--seed-helper',type=Path,help='Seed only via the compiled app test executable, without timing.')
    p.add_argument('--seed-size',choices=('small','large'),default='large')
    p.add_argument('--search',type=search_text,help='Add a literal task title/ID search case (empty text lists all tasks).')
    p.add_argument('--self-test',action='store_true')
    args=p.parse_args()
    if args.self_test: return self_test()
    if bool(args.binary)==bool(args.seed_helper): p.error('Choose --binary to benchmark or --seed-helper to seed only.')
    if args.search is not None and args.seed_helper: p.error('--search applies only to --binary benchmarking.')
    if not 30<=args.trials<=1000: p.error('--trials must be 30..1000')
    evidence=args.evidence.resolve()
    if args.home and (evidence==args.home.resolve() or args.home.resolve() in evidence.parents):
        p.error('Keep evidence outside the fixture home so it survives independent fixture cleanup.')
    evidence.mkdir(parents=True,exist_ok=True)
    if args.seed_helper:
        try: return seed_fixture(args,evidence)
        except (ValueError,OSError) as error: p.error(str(error))
    binary=args.binary.resolve(strict=True)
    samples=[]; failure=None
    fixture=None; search_preflight=None; cases={}
    env=os.environ.copy(); env.pop('UCLI_HOME',None)
    try:
        if args.home: fixture=fixture_metadata(args.home)
        with (nullcontext(None) if args.home else tempfile.TemporaryDirectory(prefix='ucli-timing-')) as scratch:
            home=args.home.resolve() if args.home else Path(scratch)/'home'
            cases={'help':['--help'],'version':['--version'],
                   'models':['--home',str(home),'models','--json'],
                   'tasks':['--home',str(home),'tasks','--json']}
            if args.search is not None:
                cases['task_search']=['--home',str(home),'tasks','list','--search',args.search,'--limit',str(SEARCH_LIMIT),'--json']
            # Each trial starts a new OS process. These are primed filesystem/DB
            # measurements, not a claim of cleared OS cache or native parity.
            for name,argv in cases.items():
                prime=subprocess.run([str(binary),*argv],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE if name=='task_search' else subprocess.DEVNULL,
                    stderr=subprocess.PIPE,env=env,cwd=ROOT,creationflags=FLAGS,timeout=5,check=True)
                if name=='task_search':
                    search_preflight,first_ids=search_page(prime.stdout,args.search)
                    if search_preflight['next_cursor']:
                        cursor=search_preflight['next_cursor']
                        following=subprocess.run([str(binary),*argv,'--cursor',cursor],stdin=subprocess.DEVNULL,capture_output=True,
                            env=env,cwd=ROOT,creationflags=FLAGS,timeout=5,check=True)
                        next_meta,next_ids=search_page(following.stdout,args.search,cursor)
                        check_search_continuation(first_ids,next_ids)
                        search_preflight['continuation']=next_meta
            order=list(cases)*args.trials
            random.Random(20261018).shuffle(order)
            for name in order:
                started=time.perf_counter_ns()
                proc=subprocess.run([str(binary),*cases[name]],stdin=subprocess.DEVNULL,capture_output=True,
                    env=env,cwd=ROOT,creationflags=FLAGS,timeout=5)
                elapsed=(time.perf_counter_ns()-started)/1e6
                samples.append({'case':name,'milliseconds':round(elapsed,4),'exit_code':proc.returncode})
                if proc.returncode: raise RuntimeError(f'{name} exit {proc.returncode}')
                if name=='task_search':
                    meta,_=search_page(proc.stdout,args.search)
                    samples[-1].update(meta)
                    if any(meta[key]!=search_preflight[key] for key in meta):
                        raise ValueError('task_search page count/cursor changed on the fixed fixture.')
                elif name in ('models','tasks'):
                    rows=[json.loads(line) for line in proc.stdout.decode('utf-8').splitlines()]
                    assert len(rows)==1 and rows[0]['version']==1 and rows[0]['type']==name
                else:
                    assert proc.stdout.strip(),f'{name} empty output'
            if args.home:
                after=fixture_metadata(home)
                assert after['database_sha256']==fixture['database_sha256'],'benchmark changed fixture database'
    except Exception as error:
        failure=repr(error)
    results={}
    for name in sorted({s['case'] for s in samples}):
        times=sorted(s['milliseconds'] for s in samples if s['case']==name)
        results[name]={'n':len(times),'p50_ms':statistics.median(times),'p95_ms':times[(95*len(times)+99)//100-1],'max_ms':max(times)}
    receipt={'scope':'actual packaged local CLI; primed local filesystem, new process per sample; small real catalog; '+('seeded synthetic task/receipt history' if args.home else 'empty task list'),
        'binary':str(binary),'binary_sha256':sha(binary),'benchmark_sha256':sha(Path(__file__)),
        'fixture':fixture,'trials_per_case':args.trials,'shuffle_seed':20261018,
        'platform':platform.platform(),'processor':platform.processor(),'python':platform.python_version(),
        'results':results,'failure':failure,'exit_code':int(failure is not None),'samples':samples,
        'limits':['not the 500-model reference catalog','synthetic task history only; no realistic delta event volume','no native runtime parity comparison','no cleared OS cache','power/antivirus/background load uncontrolled','not a release performance gate'],
        'cleanup':'seeded fixture home retained; samples retained' if args.home else 'only temporary UCLI home removed; samples retained'}
    if args.search is not None:
        receipt['task_search']={'search':args.search,'case':cases.get('task_search'),'preflight':search_preflight,
            'validation':'literal matches and summary/pagination shape; first advertised continuation consumed; no full-archive completeness oracle'}
    (evidence/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8')
    print(json.dumps({'results':results,'failure':failure},indent=2))
    return receipt['exit_code']

if __name__=='__main__': raise SystemExit(main())
