"""Consume packaged migration commands using synthetic homes only; no providers.

Optionally derive a schema-2 timing fixture from bench_app's verified schema-1
fixture. The original is never modified. Databases remain local and are not proof
archive inputs; receipts contain identities, counts and synthetic command output.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import tempfile

import bench_app

ROOT=Path(__file__).resolve().parents[1]
FLAGS=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0
OLD_SHA='62c12cb183566f7ec8a4f82ea665c1b4b1d01c5eec6fc334f824309eadc817c1'
OLD_VERSION='ucli 0.1.0-dev+222bccafb19e'

def sha(path): return bench_app.sha(path)

def data_fingerprint(path):
    """Read only a verified, quiescent synthetic DB; immutable forbids sidecars."""
    digest=hashlib.sha256()
    with sqlite3.connect(path.as_uri()+'?mode=ro&immutable=1',uri=True) as database:
        for table,order in [('tasks','id'),('attempts','id'),('events','attempt_id,sequence'),('leases','attempt_id')]:
            digest.update(table.encode())
            for row in database.execute(f'SELECT * FROM {table} ORDER BY {order}'):
                digest.update(json.dumps(row,ensure_ascii=True,separators=(',',':')).encode())
                digest.update(b'\n')
    return digest.hexdigest()
def reply(raw, kind):
    rows=[json.loads(line) for line in raw.decode('utf8').splitlines()]
    found=[row['data'] for row in rows if row.get('version')==1 and row.get('type')==kind]
    if len(found)!=1: raise ValueError(f'Expected one {kind} envelope.')
    return found[0]

def check_seeded_page(metadata, remaining):
    if metadata['returned_count'] != min(bench_app.SEARCH_LIMIT, remaining):
        raise ValueError('Migrated search omitted known synthetic tasks.')
    if bool(metadata['next_cursor']) != (remaining > bench_app.SEARCH_LIMIT):
        raise ValueError('Migrated search continuation disagrees with the known fixture.')

def self_test():
    check_seeded_page({'returned_count':10,'next_cursor':''},10)
    check_seeded_page({'returned_count':20,'next_cursor':'next'},10000)
    for metadata in ({'returned_count':0,'next_cursor':''},
                     {'returned_count':19,'next_cursor':'next'},
                     {'returned_count':20,'next_cursor':''}):
        try: check_seeded_page(metadata,10000)
        except ValueError: pass
        else: raise AssertionError('Broken migrated search was accepted.')
    print('migration known-fixture search negative controls passed')
    return 0

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary',type=Path)
    parser.add_argument('--old-binary',type=Path)
    parser.add_argument('--evidence',type=Path)
    parser.add_argument('--derive-from',type=Path)
    parser.add_argument('--derive-home',type=Path)
    parser.add_argument('--self-test',action='store_true')
    args=parser.parse_args()
    if args.self_test: return self_test()
    if not args.binary or not args.evidence: parser.error('Use --binary and --evidence.')
    binary=args.binary.resolve(strict=True)
    evidence=args.evidence.resolve()
    evidence.mkdir(parents=True,exist_ok=False)
    commands=[]; checks=[]
    receipt={'scope':'actual packaged commands; synthetic local state; no credentials, provider or AI use',
        'binary_sha256':sha(binary),'old_binary_sha256':None,'commands':commands,'checks':checks,'exit_code':1}
    env=os.environ.copy();env.pop('UCLI_HOME',None)
    def run(label,arguments,expected=0,exe=binary):
        command=[str(exe),*[str(a) for a in arguments]]
        proc=subprocess.run(command,cwd=ROOT,env=env,stdin=subprocess.DEVNULL,
            capture_output=True,timeout=120,creationflags=FLAGS)
        (evidence/(label+'.stdout.txt')).write_bytes(proc.stdout)
        (evidence/(label+'.stderr.txt')).write_bytes(proc.stderr)
        commands.append({'label':label,'command':command,'exit_code':proc.returncode})
        if (expected is None and proc.returncode==0) or (expected is not None and proc.returncode!=expected):
            raise ValueError(f'{label}: unexpected exit {proc.returncode}')
        if proc.stderr: raise ValueError(f'{label}: unexpected stderr; inspect retained output')
        return proc.stdout
    def doctor(label,home,schema):
        data=reply(run(label,['--home',home,'doctor','--json']),'doctor')
        assert data['integrity']=='ok' and data['state_schema']==schema
    try:
        old=args.old_binary.resolve(strict=True) if args.old_binary else None
        if old:
            assert sha(old)==OLD_SHA,'Historical executable differs from the retained checkpoint.'
            assert run('old-version',['--version'],exe=old).decode().strip()==OLD_VERSION
            receipt['old_binary_sha256']=sha(old)
        with tempfile.TemporaryDirectory(prefix='ucli-packaged-migration-') as temporary:
            root=Path(temporary);home=root/'home';workspace=root/'workspace';workspace.mkdir()
            common=['--home',home,'--json']
            complete=reply(run('seed-complete',[*common,'run','--demo','--cwd',workspace,'--command-id','migration-complete','Completed synthetic migration task']),'attempt')
            failed=reply(run('seed-unsent',[*common,'run','--demo','--cwd',workspace,'--model','missing/unadmitted','Unsent synthetic migration draft'],expected=None),'error')
            draft_id=failed['task_id']
            before={}
            for label,id in [('complete',complete['task_id']),('draft',draft_id)]:
                before[id]=reply(run('before-'+label,[*common,'tasks','show',id]),'task')
            assert before[draft_id]['task']['draft']=='Unsent synthetic migration draft'
            doctor('before-doctor',home,1)
            backup=root/'before.sqlite'
            migration=reply(run('migrate',[*common,'migrate',backup]),'migration')
            assert migration['status']=='applied' and migration['from_schema']==1 and migration['to_schema']==2
            snapshot=migration['backup']
            assert snapshot['schema']==1 and snapshot['tasks']==2 and snapshot['attempts']==1 and snapshot['sha256']==sha(backup)
            assert snapshot['bytes']==backup.stat().st_size
            certificate=Path(migration['receipt_path'])
            cert=json.loads(certificate.read_text(encoding='utf8'))
            assert cert['backup']==snapshot and 'status' not in cert
            doctor('after-doctor',home,2)
            for label,id in [('complete',complete['task_id']),('draft',draft_id)]:
                assert reply(run('after-'+label,[*common,'tasks','show',id]),'task')==before[id]
            checks.append('Explicit upgrade preserved the complete task/attempt and unsent draft byte-for-byte in public JSON.')
            db=home/'state/state.db'
            before_hash=sha(db)
            if old:
                for label,arguments in [('read',['tasks','list']),('write',['run','--demo','--cwd',workspace,'Old binary must not create work'])]:
                    error=reply(run('old-refusal-'+label,[*common,*arguments],expected=9,exe=old),'error')
                    assert error['code']=='schema_newer'
                    assert sha(db)==before_hash,'Historical binary changed the schema2 database.'
                checks.append('Exact historical executable refused schema2 reads and writes without changing database bytes.')
            unchanged=reply(run('repeat',[*common,'migrate',backup]),'migration')
            assert unchanged['status']=='unchanged' and unchanged['backup']['path']=='' and sha(backup)==snapshot['sha256']
            occupied=root/'occupied.sqlite';occupied.write_bytes(b'preserve foreign file')
            untouched=root/'unupgraded'
            error=run('nonclobber',['--home',untouched,'migrate',occupied,'--json'],expected=None)
            assert reply(error,'migration')['status']=='not_applied'
            reply(error,'error')
            assert occupied.read_bytes()==b'preserve foreign file'
            doctor('unupgraded-doctor',untouched,1)
            checks.append('Repeat was unchanged; occupied destination stayed intact and its home remained schema1.')
            clone=root/'snapshot-home';(clone/'state').mkdir(parents=True)
            shutil.copyfile(backup,clone/'state/state.db')
            doctor('snapshot-doctor',clone,1)
            for label,id in [('complete',complete['task_id']),('draft',draft_id)]:
                assert reply(run('snapshot-'+label,['--home',clone,'tasks','show',id,'--json']),'task')==before[id]
            checks.append('Published pre-migration snapshot reopened independently with original results/drafts.')
            receipt['migration']=migration
        if bool(args.derive_from)!=bool(args.derive_home): raise ValueError('Use --derive-from and --derive-home together.')
        if args.derive_from:
            source=args.derive_from.resolve(strict=True)
            parent=bench_app.fixture_metadata(source)
            target=args.derive_home
            if not target.is_absolute() or target.exists() or target.is_symlink(): raise ValueError('Derived home must be a new absolute path.')
            target=target.resolve()
            if not target.parent.is_dir() or source in target.parents or target in source.parents: raise ValueError('Derived home must have an existing independent parent.')
            if shutil.disk_usage(target.parent).free<1024**3: raise ValueError('Derivation needs at least 1 GiB free space.')
            source_db=source/'state/state.db'
            for suffix in ('-wal','-journal'):
                side=Path(str(source_db)+suffix)
                if side.exists() and side.stat().st_size: raise ValueError('Source fixture has uncheckpointed state; it was left untouched.')
            source_hash=sha(source_db)
            logical_hash=data_fingerprint(source_db)
            target.mkdir();(target/'state').mkdir()
            marker=target/bench_app.MARKER
            derived={**parent,'home':str(target),'status':'deriving'}
            derived.pop('observed_fixture_files_bytes',None)
            marker.write_text(json.dumps(derived,indent=2)+'\n',encoding='utf8')
            shutil.copyfile(source_db,target/'state/state.db')
            doctor('derive-before-doctor',target,1)
            backup=evidence/'large-before.sqlite'
            migration=reply(run('derive-migrate',['--home',target,'migrate',backup,'--json']),'migration')
            assert migration['status']=='applied' and migration['backup']['tasks']==parent['tasks']
            assert sha(backup)==migration['backup']['sha256']
            assert data_fingerprint(backup)==logical_hash,'Backup changed original synthetic records.'
            doctor('derive-after-doctor',target,2)
            assert data_fingerprint(target/'state/state.db')==logical_hash,'Migration changed original synthetic records.'
            first=run('derive-search',['--home',target,'tasks','list','--search','Synthetic timing task','--json'])
            metadata, first_ids=bench_app.search_page(first,'Synthetic timing task')
            check_seeded_page(metadata,parent['tasks'])
            if metadata['next_cursor']:
                second=run('derive-search-next',['--home',target,'tasks','list','--search','Synthetic timing task','--cursor',metadata['next_cursor'],'--json'])
                next_meta, next_ids=bench_app.search_page(second,'Synthetic timing task',metadata['next_cursor'])
                check_seeded_page(next_meta,parent['tasks']-bench_app.SEARCH_LIMIT)
                bench_app.check_search_continuation(first_ids,next_ids)
            db=target/'state/state.db'
            derived.update(status='complete',database_bytes=db.stat().st_size,database_sha256=sha(db),
                migration={'from_schema':1,'to_schema':2,'source_home':str(source),'source_database_sha256':source_hash,
                    'binary_sha256':sha(binary),'backup_sha256':sha(backup),'receipt':migration})
            derived['migration']['logical_records_sha256']=logical_hash
            marker.write_text(json.dumps(derived,indent=2)+'\n',encoding='utf8')
            bench_app.fixture_metadata(target)
            assert sha(source_db)==source_hash and bench_app.fixture_metadata(source)['database_sha256']==source_hash
            receipt['derived_fixture']=derived
            checks.append('Derived schema2 timing home through actual migrate; original verified fixture unchanged.')
        receipt['exit_code']=0
    except Exception as error:
        receipt['failure']=repr(error)
    (evidence/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf8')
    print(json.dumps({'exit_code':receipt['exit_code'],'checks':checks,'failure':receipt.get('failure'),'evidence':str(evidence)},indent=2))
    return receipt['exit_code']

if __name__=='__main__': raise SystemExit(main())
