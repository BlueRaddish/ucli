"""Drive the packaged CLI through durable task and failure routes, without accounts."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
FLAGS = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0

def sha(path):
    digest=hashlib.sha256()
    with path.open('rb') as source:
        while chunk:=source.read(1024*1024): digest.update(chunk)
    return digest.hexdigest()

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--evidence', type=Path, default=ROOT/'.build/app-acceptance')
    parser.add_argument('--muse-exe', type=Path, help='Opt in to the pinned native Windows echo runtime; no AI or login.')
    parser.add_argument('--claude-exe', type=Path, help='Opt in to read/hash reviewed existing native bytes; never execute or download them.')
    args = parser.parse_args()
    host = {'Windows':'windows','Darwin':'darwin','Linux':'linux'}[platform.system()]
    arch = 'arm64' if platform.machine().lower() in ('arm64','aarch64') else 'amd64'
    binary = (args.binary or ROOT/f'.build/bin/ucli-{host}-{arch}{".exe" if host=="windows" else ""}').resolve(strict=True)
    evidence = args.evidence.resolve()
    evidence.mkdir(parents=True,exist_ok=True)
    env = os.environ.copy()
    env.pop('UCLI_HOME',None)
    env['NO_COLOR']='1'
    records, checks = [], []

    def record(label,command,proc,stdout,stderr,seconds):
        row = {'label':label,'command':[str(x) for x in command],'exit_code':proc.returncode,
               'seconds':round(seconds,3),'stdout_base64':base64.b64encode(stdout).decode(),
               'stderr_base64':base64.b64encode(stderr).decode()}
        records.append(row)
        # JSONL keeps exact bytes, including intentional prompt spaces/control tests.
        with (evidence/'commands.jsonl').open('a',encoding='utf-8') as f:
            f.write(json.dumps(row)+'\n')

    def run(label,argv,input_text=None,expected=0,executable=None,extra_env=None):
        command=[executable or binary,*argv]; start=time.monotonic()
        p=subprocess.run([str(x) for x in command],input=None if input_text is None else input_text.encode('utf-8'),
                         stdin=subprocess.DEVNULL if input_text is None else None,
                         env=env | (extra_env or {}),cwd=ROOT,capture_output=True,timeout=12,creationflags=FLAGS)
        record(label,command,p,p.stdout,p.stderr,time.monotonic()-start)
        assert p.returncode==expected,(label,p.returncode,p.stderr.decode('utf-8','replace'),p.stdout[:300])
        return p

    def rows(proc):
        result=[json.loads(line) for line in proc.stdout.decode('utf-8').splitlines() if line]
        assert result and all(r.get('version')==1 and r.get('type') for r in result),result
        assert b'\x1b' not in proc.stdout,'unescaped terminal controls in machine stream'
        return result

    def data(proc,kind):
        return next(row['data'] for row in rows(proc) if row['type']==kind)

    def passed(name):
        checks.append({'name':name,'status':'pass'})
        print('PASS',name,flush=True)

    # A fresh receipt, not an append to a previous invocation's proof.
    (evidence/'commands.jsonl').write_text('',encoding='utf-8')
    failure=None
    try:
        with tempfile.TemporaryDirectory(prefix='ucli-packaged-') as scratch:
            scratch=Path(scratch); home=scratch/'home'; cwd=scratch/'workspace 東京'; cwd.mkdir()
            broken=scratch/'not-a-directory'; broken.write_text('sentinel',encoding='utf-8')
            h=['--home',str(home)]
            run('help-no-store',['--home',str(broken),'--help'])
            version=run('version-no-store',['--home',str(broken),'--json','--version'])
            assert data(version,'version').startswith('0.1.')
            assert broken.read_text()=='sentinel' and not home.exists()
            passed('help/version work without a writable store')

            native=data(run('runtime-catalog',['--home',str(broken),'runtime','list','--json']),'runtimes')
            models=data(run('voice-catalog',['--home',str(broken),'voice','models','--json']),'voice_models')
            assert len(native)==6 and all(entry['id']=='claude' for entry in native)
            assert models[0]['id']=='whisper/tiny.en' and broken.read_text()=='sentinel' and not home.exists()
            passed('optional runtime and transcription catalogs work without state, Python or downloads')

            if args.claude_exe:
                runtime=args.claude_exe.resolve(strict=True)
                entry=next(x for x in native if x['os']==host and x['arch']==arch)
                before=sha(runtime)
                assert before==entry['sha256']
                prepared=data(run('runtime-existing-offline',['--home',str(home),'runtime','prepare','claude','--offline','--json'],extra_env={'UCLI_CLAUDE_EXE':str(runtime)}),'runtime_prepared')
                assert prepared['source']=='existing' and Path(prepared['path'])==runtime and prepared['sha256']==before
                assert not prepared['executed'] and not prepared['managed_ai'] and not home.exists() and sha(runtime)==before
                passed('offline native preparation reuses reviewed existing bytes without cache, execution or account access')

            run('redirected-no-command',[])
            assert not home.exists()
            passed('redirected startup cannot hang in a hidden prompt')

            offline=data(run('offline-workflow',['--home',str(broken),'workflow','check',str(ROOT/'examples/review.workflow.json'),'--json']),'workflow_plan')
            assert offline['offline'] is True and offline['dispatched'] is False and offline['plan']['ready']==['inspect']
            assert len(offline['plan']['blocked'])==2 and broken.read_text()=='sentinel' and not home.exists()
            passed('offline workflow validation plans dependencies without opening state or dispatching')

            workflow_file=ROOT/'examples/review.workflow.json'
            declared=json.loads(workflow_file.read_text(encoding='utf8'))
            # The public spec materializes omitted selection defaults. Prompts,
            # dependencies and every explicitly declared setting stay identical.
            for step in declared['steps']:
                step['selection']={'model':'auto','account':'auto','effort':'auto','permission':'read-only'} | step.get('selection',{})
            viewed=data(run('offline-workflow-view',['--home',str(broken),'workflow','view',str(workflow_file),'--json']),'workflow_view')
            assert viewed['offline'] is True and viewed['dispatched'] is False and viewed['spec']==declared
            assert viewed['plan']==offline['plan']
            from_stdin=data(run('offline-workflow-view-stdin',['--home',str(broken),'workflow','view','-','--json'],input_text=workflow_file.read_text(encoding='utf8')),'workflow_view')
            assert from_stdin==viewed
            plain=run('offline-workflow-view-plain',['--home',str(broken),'workflow','view',str(workflow_file),'--plain'])
            assert b'not executed' in plain.stdout and b'Dependencies: inspect' in plain.stdout
            assert broken.read_text()=='sentinel' and not home.exists()
            passed('offline workflow view preserves declared prompts/settings through file/stdin/plain paths without state')

            route=data(run('explain',[*h,'explain','--demo','--json']),'route')
            assert route['mode']=='demo' and route['permission']=='read-only'
            assert data(run('no-tasks',[*h,'tasks','--json']),'tasks')==[]
            passed('routing preview does not dispatch work')

            diagnosed=data(run('doctor-full-audit',[*h,'doctor','--json']),'doctor')
            assert diagnosed['local_state']=='opened' and diagnosed['integrity']=='ok'
            passed('doctor performs the explicit full SQLite integrity audit')

            prompt='Review the architecture for 東京 café'
            base=[*h,'run','--demo','--json','--cwd',str(cwd),'--command-id','packaged-original',prompt]
            first=run('new-task',base)
            first_rows=rows(first); attempt=data(first,'attempt')
            task_id=attempt['task_id']; attempt_id=attempt['id']
            assert attempt['state']=='complete' and attempt['effective']['model']=='demo/deep'
            assert attempt['requested']['model']=='auto' and 'no AI model was called' in attempt['output']
            lifecycle=[r['type'] for r in first_rows]
            assert lifecycle.index('accepted')<lifecycle.index('terminal_result')<lifecycle.index('result_persisted')<lifecycle.index('attempt')
            passed('packaged route commits intent and result before reporting completion')

            snapshot=data(run('inspect',[*h,'tasks','show',task_id,'--json']),'task')
            assert snapshot['attempts'][0]['input']==prompt
            assert Path(snapshot['attempts'][0]['workspace']['directory'])==cwd
            assert snapshot['task'].get('draft','')==''
            passed('fresh invocation reads the persisted result and original execution directory')

            replay=data(run('replay',base),'attempt')
            assert replay['id']==attempt_id
            conflict=run('changed-command',[*h,'run','--demo','--json','--cwd',str(cwd),'--command-id','packaged-original','changed input'],expected=5)
            assert data(conflict,'error')['code']=='command_conflict'
            snapshot=data(run('inspect-replay',[*h,'tasks','show',task_id,'--json']),'task')
            assert len(snapshot['attempts'])==1
            passed('command replay cannot dispatch twice or accept changed intent')

            follow=data(run('followup',[*h,'tasks','followup',task_id,'--demo','--json','--model','demo/deep','--effort','high','Continue the same work']),'attempt')
            assert follow['task_id']==task_id and follow['id']!=attempt_id
            inherited=data(run('inherit-pins',[*h,'tasks','followup',task_id,'--demo','--json','Another short followup']),'attempt')
            assert inherited['effective']['model']=='demo/deep' and inherited['effective']['effort']=='high'
            snapshot=data(run('history',[*h,'tasks','show',task_id,'--json']),'task')
            assert len(snapshot['attempts'])==3 and snapshot['attempts'][0]['output']==attempt['output']
            passed('followups preserve task pins and earlier evidence')

            blocked=run('unadmitted-provider',[*h,'run','--json','--cwd',str(cwd),'Do real work later'],expected=3)
            problem=data(blocked,'error'); saved=problem['task_id']
            draft=data(run('blocked-draft',[*h,'tasks','show',saved,'--json']),'task')
            assert draft['task']['draft']=='Do real work later' and draft['attempts']==[]
            passed('missing provider preserves input without account prompts or dispatch')

            first_page=rows(run('summary-first-page',[*h,'tasks','list','--limit','1','--json']))[0]
            assert first_page['type']=='tasks' and len(first_page['data'])==1
            assert first_page['pagination']['next_cursor']
            assert 'draft' not in first_page['data'][0] and 'has_draft' in first_page['data'][0]
            cursor=first_page['pagination']['next_cursor']
            older=rows(run('summary-older-page',[*h,'tasks','list','--limit','1','--cursor',cursor,'--json']))[0]
            assert len(older['data'])==1 and older['data'][0]['id']!=first_page['data'][0]['id']
            searched=rows(run('summary-id-search',[*h,'tasks','list','--search',saved,'--json']))[0]
            assert [t['id'] for t in searched['data']]==[saved] and searched['data'][0]['has_draft']
            wrong_query=run('summary-cursor-query-mismatch',[*h,'tasks','list','--cursor',cursor,'--search','different-query','--json'],expected=2)
            assert data(wrong_query,'error')['code']=='invalid_input'
            passed('summary cursor pages and literal ID search preserve drafts without projecting their text')

            backup=scratch/'saved work.sqlite'
            receipt=data(run('backup',[*h,'backup',str(backup),'--json']),'backup')
            assert receipt['version']==1 and receipt['schema']==1 and Path(receipt['path'])==backup
            assert receipt['sha256']==sha(backup)
            assert receipt['bytes']==backup.stat().st_size and receipt['tasks']==2 and receipt['attempts']==3
            original_hash=receipt['sha256']
            occupied=run('backup-refuses-existing',[*h,'backup',str(backup),'--json'],expected=5)
            assert data(occupied,'error')['code']=='backup_exists'
            assert sha(backup)==original_hash
            clone=scratch/'snapshot-readback'; (clone/'state').mkdir(parents=True)
            shutil.copyfile(backup,clone/'state/state.db')
            clone_h=['--home',str(clone)]
            assert data(run('backup-readback-doctor',[*clone_h,'doctor','--json']),'doctor')['integrity']=='ok'
            restored=data(run('backup-readback-task',[*clone_h,'tasks','show',task_id,'--json']),'task')
            assert len(restored['attempts'])==3 and restored['attempts'][0]['output']==attempt['output']
            assert data(run('backup-readback-draft',[*clone_h,'tasks','show',saved,'--json']),'task')['task']['draft']=='Do real work later'
            assert data(run('backup-readback-replay',[*clone_h,*base[2:]]),'attempt')['id']==attempt_id
            assert len(data(run('backup-readback-no-duplicate',[*clone_h,'tasks','show',task_id,'--json']),'task')['attempts'])==3
            passed('packaged backup preserves task evidence and replay through isolated snapshot readback without overwriting')

            bad_effort=run('unsupported-effort',[*h,'run','--demo','--json','--cwd',str(cwd),'--model','demo/fast','--effort','high','Keep this input'],expected=5)
            assert data(bad_effort,'error')['code']=='effort_unsupported'
            passed('explicit unsupported effort is rejected without silent downgrade')

            multiline='First line\nSecond line 東京\n'
            read=data(run('stdin-task',[*h,'run','--demo','--json','--cwd',str(cwd),'-'],input_text=multiline),'attempt')
            assert read['input']==multiline
            literal=data(run('literal-option',[*h,'run','--demo','--json','--cwd',str(cwd),'--','--version']),'attempt')
            assert literal['input']=='--version'
            passed('redirected multiline and literal option-like inputs are preserved')

            escaped=run('control-output',[*h,'run','--demo','--cwd',str(cwd),'text \x1b[31mred'])
            assert b'\x1b' not in escaped.stdout and b'\x1b' not in escaped.stderr
            passed('human renderer makes terminal escape controls inert')

            boundary=run('input-limit',[*h,'run','--demo','--json','--cwd',str(cwd),'-'],input_text='a'*(1024*1024+1),expected=2)
            assert data(boundary,'error')['code']
            passed('input limit rejects oversized stdin before accepting work')

            large=run('large-input',[*h,'run','--demo','--json','--cwd',str(cwd),'-'],input_text='x'*(128*1024))
            large_attempt=data(large,'attempt')
            command=[binary,*h,'tasks','show',large_attempt['task_id'],'--json']
            start=time.monotonic()
            proc=subprocess.Popen([str(x) for x in command],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,
                                  cwd=ROOT,env=env,creationflags=FLAGS)
            try:
                proc.wait(timeout=4)
                stdout,stderr=proc.communicate(timeout=2)
            finally:
                if proc.poll() is None:
                    proc.kill(); proc.wait(timeout=2)
            record('non-reading-output',command,proc,stdout,stderr,time.monotonic()-start)
            assert proc.returncode==9,proc.returncode
            checked=data(run('after-output-loss',[*h,'tasks','show',large_attempt['task_id'],'--json']),'task')
            assert checked['attempts'][0]['state']=='complete'
            passed('non-reading stdout cannot hang the process or erase saved work')

            helper=ROOT/('.build/bin/ucli-app-check'+('.exe' if os.name=='nt' else ''))
            assert helper.exists(),'Build test fixture with python tools/dev.py check before packaged recovery acceptance.'
            seeded=run('crash-after-acceptance',['-test.run=^TestAppFixtureProcess$'],expected=17,executable=helper,
                       extra_env={'UCLI_APP_FIXTURE':'accepted-orphan','UCLI_APP_FIXTURE_HOME':str(home),'UCLI_APP_FIXTURE_CWD':str(cwd)})
            orphan=json.loads(seeded.stdout)
            unsettled=data(run('orphan-inspect',[*h,'tasks','show',orphan['task_id'],'--json']),'task')
            assert unsettled['attempts'][0]['state']=='running'
            recovered=data(run('recover-stopped-demo',[*h,'tasks','recover',orphan['task_id'],'--json']),'recovery')
            assert recovered['state']=='interrupted'
            recovered_history=data(run('recovered-history',[*h,'tasks','show',orphan['task_id'],'--json']),'task')
            assert len(recovered_history['attempts'])==1,'recovery replayed an attempt'
            passed('actual owner death leaves intent visible and safe demo recovery never replays')

            if args.muse_exe:
                runtime=args.muse_exe.resolve(strict=True)
                assert sha(runtime)=='2d1ac7af0a9ea77643cb8690eee70f52f0f8476c46b194f143c831f481aea59d'
                native_env={'UCLI_MUSE_EXE':str(runtime)}
                sentinel=cwd/'untouched.txt'; sentinel.write_text('user-workspace-canary',encoding='utf-8')
                echo_input='Synthetic UCLI native echo 東京\nsecond line'
                echo_args=[*h,'run','--demo','--model','muse/echo','--json','--cwd',str(cwd),'--command-id','native-echo-acceptance',echo_input]
                echoed=run('native-echo',echo_args,extra_env=native_env)
                echo=data(echoed,'attempt'); echo_rows=rows(echoed)
                assert echo['state']=='complete' and echo['output']=='echo: '+echo_input
                assert echo['effective']['model']=='muse/echo' and echo['effective']['account']=='none' and echo['effective']['effort']=='none'
                native=echo['native_context']
                assert native['runtime']=='muse' and native['provider']=='echo' and native['account']=='none' and native['durability']=='ephemeral'
                kinds=[r['type'] for r in echo_rows]
                assert kinds.index('accepted')<kinds.index('runtime_context')<kinds.index('message_delta')<kinds.index('terminal_result')<kinds.index('result_persisted')
                assert data(run('native-replay',echo_args,extra_env={'UCLI_MUSE_EXE':'invalid-relative'}),'attempt')['id']==echo['id']
                loaded=data(run('native-restart-history',[*h,'tasks','show',echo['task_id'],'--json']),'task')
                assert loaded['attempts'][0]['native_context']==native
                follow=data(run('native-fresh-session',[*h,'tasks','followup',echo['task_id'],'--demo','--json','synthetic followup'],extra_env=native_env),'attempt')
                assert follow['task_id']==echo['task_id'] and follow['native_context']['session_id']!=native['session_id']
                assert follow['effective']['model']=='muse/echo' and follow['output']=='echo: synthetic followup'
                assert sentinel.read_text(encoding='utf-8')=='user-workspace-canary'
                passed('actual isolated native echo persists provenance and fresh-session followups without replay')
    except Exception as error:
        failure=repr(error)
        print('FAIL',failure,flush=True)
    receipt={'scope':'actual packaged CLI; isolated synthetic state; no provider/accounts or human usability proof',
             'platform':platform.platform(),'binary':str(binary),'binary_sha256':sha(binary),
             'checks':checks,'passed':len(checks),'failed':int(failure is not None),'failure':failure,
             'commands':len(records),'cleanup':'temporary synthetic workspace removed; proof retained',
             'unexecuted':['owner usability','physical terminal keys','macOS/Linux unless receipt host says so','authenticated provider accounts']+([] if args.muse_exe else ['native Muse echo (not opted in)'])}
    (evidence/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8')
    return receipt['failed']

if __name__=='__main__':
    raise SystemExit(main())
