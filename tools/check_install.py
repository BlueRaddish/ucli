"""Consume fresh/owned local installation with disposable destinations only.

The supplied real binary/build/acceptance proof exercises fresh installation.
Update fault fixtures append a harmless trailing marker to a disposable binary
copy (or use invalid executable bytes), with explicitly synthetic checksums. Those
fixtures test installer mechanics; they are not build, provider or release proof.
Only this test script can inject pause/failure hooks; production flags cannot.
"""
import argparse
from contextlib import contextmanager
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import queue
import shutil
import subprocess
import sys
import tempfile
import threading
import time

ROOT=Path(__file__).resolve().parents[1]
FLAGS=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0

def sha(path):
    h=hashlib.sha256()
    with path.open('rb') as f:
        while chunk:=f.read(1024*1024): h.update(chunk)
    return h.hexdigest()

def load_installer():
    spec=importlib.util.spec_from_file_location('ucli_install_fixture',ROOT/'tools/install_local.py')
    mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
    return mod

def worker(mode,argv):
    """Subprocess fault hooks: exercise the real installer, without product flags."""
    mod=load_installer()
    destination=Path(argv[argv.index('--destination')+1]).resolve()
    old=json.loads(Path(argv[argv.index('--replace-owned')+1]).read_text(encoding='utf-8'))
    fired=False
    def gate():
        nonlocal fired
        if fired: return
        fired=True
        print(json.dumps({'fixture_gate':mode}),flush=True)
        if sys.stdin.buffer.readline()!=b'continue\n': raise RuntimeError('Fixture gate interrupted.')
    if mode=='before-swap':
        original=mod.require_hash
        def guarded(path,digest):
            if path==destination and digest==old['sha256']: gate()
            return original(path,digest)
        mod.require_hash=guarded
    elif mode=='after-swap':
        original=mod.os.replace
        def guarded(source,target):
            result=original(source,target)
            if Path(target)==destination: gate()
            return result
        mod.os.replace=guarded
    elif mode=='swap-denied':
        original=mod.os.replace
        def guarded(source,target):
            if Path(target)==destination: raise PermissionError('Synthetic replacement sharing denial.')
            return original(source,target)
        mod.os.replace=guarded
    else: raise ValueError('Unknown fixture mode.')
    return mod.main(argv)

def main():
    if len(sys.argv)>2 and sys.argv[1]=='--fixture-worker':
        return worker(sys.argv[2],sys.argv[3:])
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary',type=Path)
    p.add_argument('--build-receipt',type=Path,required=True)
    p.add_argument('--acceptance-receipt',type=Path,required=True)
    p.add_argument('--evidence',type=Path,required=True)
    args=p.parse_args()
    host={'Windows':'windows','Linux':'linux','Darwin':'darwin'}[platform.system()]
    arch='arm64' if platform.machine().lower() in ('arm64','aarch64') else 'amd64'
    source=(args.binary or ROOT/f'.build/bin/ucli-{host}-{arch}{".exe" if host=="windows" else ""}').resolve(strict=True)
    build=args.build_receipt.resolve(strict=True); acceptance=args.acceptance_receipt.resolve(strict=True)
    before={str(f):sha(f) for f in (source,build,acceptance)}
    evidence=args.evidence.resolve(); evidence.mkdir(parents=True,exist_ok=True)
    commands=[]; failure=None; checks=[]; skipped=[]
    children=[]
    def stop_children():
        for child in children:
            if child.poll() is None:
                child.kill(); child.communicate(timeout=10)
    @contextmanager
    def scratch_fixture():
        with tempfile.TemporaryDirectory(prefix='ucli-install-check-') as scratch:
            try: yield scratch
            finally: stop_children()  # Release Windows image/lock handles before removing fixture files.
    def record(label,command,code,stdout=b'',stderr=b''):
        commands.append({'label':label,'command':command,'exit_code':code,
            'stdout':stdout.decode('utf-8','replace'),'stderr':stderr.decode('utf-8','replace')})
    def run(label,argv,expected=0,mode=None):
        command=[sys.executable,str(ROOT/'tools/install_local.py'),*argv]
        if mode: command=[sys.executable,str(Path(__file__).resolve()),'--fixture-worker',mode,*argv]
        proc=subprocess.run(command,stdin=subprocess.DEVNULL,capture_output=True,timeout=20,creationflags=FLAGS)
        record(label,command,proc.returncode,proc.stdout,proc.stderr)
        assert proc.returncode==expected,(label,proc.returncode,proc.stderr)
        return proc
    def pause(label,argv,mode):
        command=[sys.executable,str(Path(__file__).resolve()),'--fixture-worker',mode,*argv]
        proc=subprocess.Popen(command,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,creationflags=FLAGS)
        children.append(proc)
        lines=queue.Queue(maxsize=1)
        threading.Thread(target=lambda:lines.put(proc.stdout.readline()),daemon=True).start()
        line=lines.get(timeout=15)
        assert json.loads(line).get('fixture_gate')==mode,(label,line)
        record(label+'-gate',command,None,line)
        return proc
    def stop(label,proc):
        proc.kill(); output,error=proc.communicate(timeout=10)
        record(label,['terminate-owned-fixture',str(proc.pid)],proc.returncode,output,error)
        assert proc.returncode!=0,'fault worker did not actually terminate'
    try:
        with scratch_fixture() as scratch_name:
            scratch=Path(scratch_name); suffix='.exe' if os.name=='nt' else ''
            fixtures=scratch/'fixtures'; fixtures.mkdir()
            changed=fixtures/('changed'+suffix)
            shutil.copyfile(source,changed)
            with changed.open('ab') as f: f.write(b'\nUCLI_SYNTHETIC_INSTALL_UPDATE_FIXTURE\n')
            changed.chmod(0o755)
            bad=fixtures/('invalid'+suffix); bad.write_bytes(b'not an executable\n'); bad.chmod(0o755)
            def fixture_proof(binary):
                b=binary.with_name(binary.name+'.build.json'); a=binary.with_name(binary.name+'.acceptance.json')
                digest=sha(binary)
                b.write_text(json.dumps({'exit_code':0,'binaries':[{'sha256':digest}],'scope':'synthetic installer fault fixture only'}),encoding='utf-8')
                a.write_text(json.dumps({'failed':0,'passed':1,'binary_sha256':digest,'scope':'synthetic installer fault fixture only'}),encoding='utf-8')
                return b,a
            changed_build,changed_acceptance=fixture_proof(changed)
            bad_build,bad_acceptance=fixture_proof(bad)
            def arguments(binary,destination,b,a,proof,old=None):
                result=['--binary',str(binary),'--destination',str(destination),'--build-receipt',str(b),
                    '--acceptance-receipt',str(a),'--evidence',str(proof)]
                if old: result+=['--replace-owned',str(old)]
                return result
            def fresh(label):
                folder=scratch/label; folder.mkdir()
                destination=folder/('ucli'+suffix); old=folder/'old.json'
                run(label+'-install',arguments(source,destination,build,acceptance,old))
                assert sha(destination)==before[str(source)]
                return folder,destination,old
            def update(folder,destination,old,proof='new.json',binary=changed,b=None,a=None):
                return arguments(binary,destination,b or changed_build,a or changed_acceptance,folder/proof,old)
            folder=scratch/'fresh'; folder.mkdir(); destination=folder/('ucli'+suffix)
            for label,alias in [('destination',destination),('source',source),('build',build),('acceptance',acceptance)]:
                run('receipt-alias-'+label,arguments(source,destination,build,acceptance,alias),2)
                assert not destination.exists(),'invalid proof path mutated destination'
            checks.append('executable/input receipt aliases rejected before installation')
            old=folder/'old.json'; base=arguments(source,destination,build,acceptance,old)
            run('new-install',base)
            assert sha(destination)==before[str(source)] and json.loads(old.read_text())['exit_code']==0
            original=old.read_bytes(); run('existing-proof',base,2)
            assert old.read_bytes()==original
            checks.append('real verified fresh install and existing proof preservation')
            destination.unlink(); destination.write_bytes(b'foreign sentinel')
            run('fresh-foreign',arguments(source,destination,build,acceptance,folder/'foreign.json'),2)
            assert destination.read_bytes()==b'foreign sentinel'
            checks.append('fresh install preserves foreign files')

            folder,destination,old=fresh('update')
            argv=update(folder,destination,old); run('owned-update',argv)
            proof=json.loads((folder/'new.json').read_text())
            backup=Path(proof['backup']); journal=Path(proof['transition'])
            assert sha(destination)==sha(changed) and sha(backup)==sha(source)
            assert json.loads(journal.read_text())['old_receipt_sha256']==sha(old)
            assert proof['old_sha256']==sha(source)
            checks.append('owned replacement retains exact old backup and bound transition')
            for label,alias in [('old',old),('backup',backup),('journal',journal),('lock',folder/('.'+destination.name+'.ucli-install.lock'))]:
                bad_argv=update(folder,destination,old); bad_argv[bad_argv.index('--evidence')+1]=str(alias)
                run('update-proof-alias-'+label,bad_argv,2)
                assert sha(destination)==sha(changed) and sha(backup)==sha(source)
            checks.append('replacement evidence cannot alias old proof, backup or control files')
            run('recover-proof',update(folder,destination,old,'recovered.json'))
            assert json.loads((folder/'recovered.json').read_text())['action']=='recovered owned replacement proof'
            checks.append('exact desired bytes plus retained lineage permit idempotent proof recovery')

            folder,destination,old=fresh('unowned-new')
            shutil.copyfile(changed,destination)
            run('desired-bytes-without-lineage',update(folder,destination,old),2)
            assert sha(destination)==sha(changed) and not (folder/'new.json').exists()
            checks.append('desired bytes alone never establish replacement ownership')

            folder,destination,old=fresh('foreign')
            destination.write_bytes(b'foreign changed after prior installation')
            run('owned-foreign',update(folder,destination,old),2)
            assert destination.read_bytes()==b'foreign changed after prior installation'
            checks.append('old receipt cannot overwrite a changed foreign destination')

            folder,destination,old=fresh('hardlink')
            alias=folder/'linked-copy'; os.link(destination,alias)
            run('owned-hardlink',update(folder,destination,old),2)
            assert sha(destination)==sha(source) and sha(alias)==sha(source)
            checks.append('hard-linked destination refused without damage')
            alias.unlink()

            folder,destination,old=fresh('symlink')
            target=folder/('real'+suffix); destination.rename(target)
            try: destination.symlink_to(target)
            except OSError as error:
                target.rename(destination); skipped.append('symlink creation unavailable: '+str(error))
            else:
                run('owned-symlink',update(folder,destination,old),2)
                assert destination.is_symlink() and sha(target)==sha(source)
                checks.append('symbolic-link destination refused without damage')

            folder,destination,old=fresh('wrong-proof')
            altered=folder/'wrong-acceptance.json'
            altered.write_text(json.dumps({'failed':0,'passed':1,'binary_sha256':'0'*64}),encoding='utf-8')
            run('unaccepted-new-hash',update(folder,destination,old,a=altered),2)
            wrong=folder/'wrong-owner.json'; obj=json.loads(old.read_text()); obj['destination']=str(folder/'other')
            wrong.write_text(json.dumps(obj),encoding='utf-8')
            run('wrong-owner-destination',update(folder,destination,wrong),2)
            assert sha(destination)==sha(source) and not (folder/'new.json').exists()
            checks.append('new acceptance hash and exact old destination are mandatory')

            # JSON booleans compare equal to integers in Python. Neither those
            # nor truthy negative/string counts establish a successful check.
            for kind,key,values in [('build','exit_code',[False,-1,'0']),
                                    ('acceptance','failed',[False,-1,'0']),
                                    ('acceptance','passed',[True,-1,0,'1']),
                                    ('owner','exit_code',[False,-1,'0'])]:
                original_path={'build':changed_build,'acceptance':changed_acceptance,'owner':old}[kind]
                for index,value in enumerate(values):
                    obj=json.loads(original_path.read_text()); obj[key]=value
                    malformed=folder/f'malformed-{kind}-{key}-{index}.json'
                    malformed.write_text(json.dumps(obj),encoding='utf-8')
                    kwargs={kind:malformed}
                    run(f'reject-malformed-{kind}-{key}-{index}',update(folder,destination,kwargs.get('owner',old),
                        b=kwargs.get('build'),a=kwargs.get('acceptance')),2)
                    assert sha(destination)==sha(source) and not (folder/'new.json').exists()
            malformed=folder/'malformed-build-array.json'
            malformed.write_text(json.dumps({'exit_code':0,'binaries':{'sha256':sha(changed)}}),encoding='utf-8')
            run('reject-build-nonarray',update(folder,destination,old,b=malformed),2)
            checks.append('strict integer success counts and a build array required; booleans, negatives and strings refused')

            old_alias=folder/'linked-old-receipt.json'; os.link(old,old_alias)
            run('reject-linked-old-receipt',update(folder,destination,old),2)
            assert sha(destination)==sha(source) and not (folder/'new.json').exists()
            old_alias.unlink()
            checks.append('hard-linked ownership receipt refused without destination changes')

            if os.name=='nt':
                for ending in ('.',' '):
                    missing=folder/('new-destination'+ending)
                    run('reject-new-windows-leaf-'+repr(ending),arguments(source,missing,build,acceptance,folder/'bad-leaf.json'),2)
                    assert not missing.exists() and not (folder/'new-destination').exists() and not (folder/'bad-leaf.json').exists()
                checks.append('missing Windows trailing-dot/space leaves refused before normalized installation')

                import ctypes
                alias_folder=scratch/'short-name'; alias_folder.mkdir()
                long_destination=alias_folder/'ucli-owned-long-executable.exe'; long_old=alias_folder/'old.json'
                # Windows hard-link publication may omit a DOS alias. Exercise
                # the supported identical-existing-file route with a normal copy.
                shutil.copyfile(source,long_destination); long_destination.chmod(0o755)
                run('short-name-fresh',arguments(source,long_destination,build,acceptance,long_old))
                get_short=ctypes.windll.kernel32.GetShortPathNameW
                get_short.argtypes=[ctypes.c_wchar_p,ctypes.c_wchar_p,ctypes.c_ulong]; get_short.restype=ctypes.c_ulong
                buffer=ctypes.create_unicode_buffer(32768)
                length=get_short(str(long_destination),buffer,len(buffer))
                assert 0<length<len(buffer),'GetShortPathNameW failed for owned temporary executable'
                short_destination=Path(buffer.value)
                if short_destination.name.lower()==long_destination.name.lower():
                    skipped.append('Windows filesystem did not supply a distinct 8.3 leaf alias; short-name lock check unexecuted')
                else:
                    mod=load_installer()
                    assert os.path.samefile(long_destination,short_destination)
                    assert mod.canonical(short_destination)==mod.canonical(long_destination)
                    proc=pause('short-name-owner',update(alias_folder,long_destination,long_old),'before-swap')
                    run('short-name-shared-lock',update(alias_folder,short_destination,long_old,'rival.json'),1)
                    assert sha(long_destination)==sha(source)
                    stop('stop-short-name-owner',proc)
                    run('short-name-retry',update(alias_folder,short_destination,long_old))
                    assert sha(long_destination)==sha(changed)
                    checks.append('actual Windows short-name alias uses the same lock and old receipt identity; retry succeeds')

            folder,destination,old=fresh('rollback')
            run('bad-version-rollback',update(folder,destination,old,binary=bad,b=bad_build,a=bad_acceptance),1)
            assert sha(destination)==sha(source) and not (folder/'new.json').exists()
            assert sha(next(folder.glob('*.ucli-backup-*')))==sha(source)
            checks.append('actual invalid executable launch restores verified old bytes without success proof')

            folder,destination,old=fresh('denied')
            run('swap-denial',update(folder,destination,old),1,mode='swap-denied')
            assert sha(destination)==sha(source) and not (folder/'new.json').exists()
            checks.append('replacement failure preserves original destination and backup')

            folder,destination,old=fresh('before-crash')
            proc=pause('before-swap',update(folder,destination,old),'before-swap')
            run('cooperating-lock-contention',update(folder,destination,old,'rival.json'),1)
            assert sha(destination)==sha(source)
            stop('kill-before-swap',proc)
            run('retry-after-lock-owner-death',update(folder,destination,old))
            assert sha(destination)==sha(changed)
            checks.append('OS lock excludes a competing installer and releases on real worker death; pre-swap retry succeeds')

            folder,destination,old=fresh('changed-at-boundary')
            proc=pause('before-final-hash',update(folder,destination,old),'before-swap')
            destination.write_bytes(b'foreign boundary sentinel')
            proc.stdin.write(b'continue\n'); proc.stdin.flush()
            output,error=proc.communicate(timeout=10); record('foreign-before-swap-result',[],proc.returncode,output,error)
            assert proc.returncode==2 and destination.read_bytes()==b'foreign boundary sentinel'
            assert not (folder/'new.json').exists()
            checks.append('last ownership recheck rejects a changed destination before swap')

            folder,destination,old=fresh('foreign-during-rollback')
            proc=pause('after-swap-before-verification',update(folder,destination,old),'after-swap')
            destination.write_bytes(b'foreign post-swap sentinel')
            proc.stdin.write(b'continue\n'); proc.stdin.flush()
            output,error=proc.communicate(timeout=10); record('foreign-rollback-refusal',[],proc.returncode,output,error)
            assert proc.returncode==1 and destination.read_bytes()==b'foreign post-swap sentinel'
            assert sha(next(folder.glob('*.ucli-backup-*')))==sha(source) and not (folder/'new.json').exists()
            checks.append('rollback refuses to overwrite a post-swap foreign change and retains verified backup')

            folder,destination,old=fresh('after-crash')
            proc=pause('after-swap',update(folder,destination,old),'after-swap')
            assert sha(destination)==sha(changed) and not (folder/'new.json').exists()
            stop('kill-after-swap',proc)
            journal=next(folder.glob('*.ucli-transition-*.json')); raw=journal.read_bytes()
            for field in ('destination','old_sha256','new_sha256','old_receipt_sha256','backup'):
                obj=json.loads(raw); obj[field]='tampered fixture identity'; journal.write_text(json.dumps(obj),encoding='utf-8')
                run('reject-tampered-transition-'+field,update(folder,destination,old),2)
                assert sha(destination)==sha(changed) and not (folder/'new.json').exists()
                journal.write_bytes(raw)
            backup=next(folder.glob('*.ucli-backup-*')); saved_backup=backup.read_bytes()
            backup.write_bytes(b'changed backup')
            run('reject-changed-backup',update(folder,destination,old),2)
            assert sha(destination)==sha(changed) and not (folder/'new.json').exists()
            backup.write_bytes(saved_backup)
            run('retry-after-swap-before-proof',update(folder,destination,old))
            assert json.loads((folder/'new.json').read_text())['action']=='recovered owned replacement proof'
            checks.append('real post-swap crash recovers only with exact journal identity and backup bytes')

            if os.name=='nt':
                folder,destination,old=fresh('in-use')
                command=[str(destination),'--home',str(folder/'unused-home'),'run','--demo','-']
                live=subprocess.Popen(command,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,creationflags=FLAGS)
                children.append(live); time.sleep(0.2)
                assert live.poll() is None,'in-use fixture exited before replacement check'
                run('actual-windows-in-use',update(folder,destination,old),1)
                assert sha(destination)==sha(source) and not (folder/'new.json').exists()
                stop('stop-owned-in-use-fixture',live)
                run('retry-after-in-use-closes',update(folder,destination,old))
                checks.append('actual Windows running executable blocks replacement without damage; retry succeeds after owned child closes')
            else:
                skipped.append('Windows in-use image denial is not a POSIX property; running POSIX images may retain old inodes')
        assert before=={str(f):sha(f) for f in (source,build,acceptance)},'input proof or source changed'
    except Exception as error:
        failure=repr(error)
    finally:
        stop_children()
    receipt={'scope':'actual installer consuming checks; disposable destinations and explicitly synthetic update fault fixtures only',
        'binary_sha256':before[str(source)],'platform':platform.platform(),'python':platform.python_version(),
        'source_sha256':{name:sha(ROOT/'tools'/name) for name in ('install_local.py','check_install.py')},
        'passed':len(checks),'checks':checks,'skipped':skipped,'failed':int(failure is not None),'failure':failure,'commands':commands,
        'limits':['process crashes, not power-loss durability','cooperating installers only; arbitrary noncooperating races not prevented',
            'POSIX execution unverified unless this receipt was produced on that host','synthetic update proofs do not admit a release'],
        'api_references':['https://docs.python.org/3/library/os.html#os.replace',
            'https://docs.python.org/3/library/msvcrt.html#msvcrt.locking',
            'https://docs.python.org/3/library/fcntl.html#fcntl.flock'],
        'cleanup':'temporary installed files removed; supplied source and proof hashes checked unchanged'}
    (evidence/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8')
    print(json.dumps({'passed':len(checks),'failure':failure,'skipped':skipped}))
    return receipt['failed']

if __name__=='__main__': raise SystemExit(main())
