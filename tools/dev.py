"""One local/CI build route for the experimental Go application (stdlib only)."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]

def fingerprints(root):
    sources = [x for x in root.rglob('*.go') if 'spikes' not in x.relative_to(root).parts and '.build' not in x.relative_to(root).parts]
    # The voice helper is embedded in Go and must share its build identity.
    # Add any future non-Go embedded source types here and extend the self-check.
    sources += [x for x in (root/'internal').rglob('*.py') if '.build' not in x.relative_to(root).parts]
    sources += [root/'go.mod',root/'go.sum']
    digest = lambda paths: {str(x.relative_to(root)).replace('\\','/'):hashlib.sha256(x.read_bytes()).hexdigest() for x in sorted(paths) if x.is_file()}
    return digest(sources), digest((root/'tools').glob('*.py'))

def self_test():
    with tempfile.TemporaryDirectory(prefix='ucli-build-identity-') as directory:
        root=Path(directory)
        (root/'tools').mkdir()
        (root/'go.mod').write_text('module fixture\n',encoding='utf8')
        source=root/'fixture.go'
        source.write_text('package fixture\n',encoding='utf8')
        before=fingerprints(root)
        assert before==fingerprints(root)
        source.write_text('package changed\n',encoding='utf8')
        assert before!=fingerprints(root), 'edited source was not detected'
        before=fingerprints(root)
        source.unlink()
        assert before!=fingerprints(root), 'removed source was not detected'
        before=fingerprints(root)
        (root/'tools/check.py').write_text('pass\n',encoding='utf8')
        assert before!=fingerprints(root), 'new verification tool was not detected'
        helper=root/'internal/voice/helper.py'
        helper.parent.mkdir(parents=True)
        helper.write_text('print("original")\n',encoding='utf8')
        before=fingerprints(root)
        helper.write_text('print("changed")\n',encoding='utf8')
        assert before!=fingerprints(root), 'changed embedded voice source was not detected'
    print('build provenance guard self-check passed')
    return 0

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('action', choices=['check', 'build', 'cross', 'all', 'self-test'])
    p.add_argument('--go', default=os.environ.get('UCLI_GO') or shutil.which('go'))
    p.add_argument('--evidence', type=Path, default=ROOT/'.build/verification')
    p.add_argument('--jobs',type=int,choices=(1,2),default=2,help='Bound Go build/test concurrency on busy development hosts.')
    args = p.parse_args()
    if args.action=='self-test': return self_test()
    if not args.go:
        p.error('Go 1.27.2 is required; pass --go PATH or set UCLI_GO.')
    executable = str(Path(args.go).resolve(strict=True))
    evidence = args.evidence.resolve()
    evidence.mkdir(parents=True, exist_ok=True)
    output = ROOT/'.build/bin'
    output.mkdir(parents=True, exist_ok=True)
    env = os.environ.copy()
    env.update(GOTOOLCHAIN='local', CGO_ENABLED='0', GOMAXPROCS='4')
    if os.name == 'nt':
        cache = Path.home()/'.local/share/ucli-dev/cache'
        env.update(GOMODCACHE=str(cache/'gomod'), GOCACHE=str(cache/'gobuild'))
    flags = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0
    records = []
    binaries = []
    revision = 'unknown'
    branch = 'unknown'
    initial_sources,initial_tools=fingerprints(ROOT)

    def run(label, command, overrides=None):
        start = time.monotonic()
        proc = subprocess.run([str(a) for a in command], cwd=ROOT,
            env=env | (overrides or {}), capture_output=True, timeout=600, creationflags=flags)
        (evidence/f'{label}.stdout.txt').write_bytes(proc.stdout)
        (evidence/f'{label}.stderr.txt').write_bytes(proc.stderr)
        records.append({'label': label, 'command': [str(a) for a in command],
            'overrides': overrides or {}, 'exit_code': proc.returncode,
            'elapsed_seconds': round(time.monotonic()-start,3)})
        print(f'{label}: exit {proc.returncode}', flush=True)
        if proc.returncode:
            sys.stderr.buffer.write(proc.stderr)
            raise RuntimeError(f'{label} failed; see {evidence}')
        return proc.stdout

    def build(target_os, target_arch):
        name = f'ucli-{target_os}-{target_arch}'+('.exe' if target_os=='windows' else '')
        binary = output/name
        run('build-'+target_os+'-'+target_arch,
            [executable,'build',f'-p={args.jobs}','-trimpath','-ldflags',
             '-X=ucli.local/ucli/internal/cli.Version=0.1.0-dev+'+revision,'-o',binary,'./cmd/ucli'],
            {'GOOS': target_os,'GOARCH':target_arch})
        binaries.append({'os':target_os,'arch':target_arch,'path':str(binary),
            'sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'bytes':binary.stat().st_size})

    status = 0
    try:
        revision = run('source-revision',['git','rev-parse','--short=12','HEAD']).decode().strip()
        branch = run('source-branch',['git','branch','--show-current']).decode().strip()
        if run('source-status',['git','status','--porcelain']).strip():
            revision += '-dirty'
        version = run('go-version',[executable,'version']).decode().strip()
        if not version.startswith('go version go1.27.2 '):
            raise RuntimeError('This experimental build is pinned to Go 1.27.2.')
        if args.action in ('check','all'):
            run('build-route-self-test',[sys.executable,'tools/dev.py','self-test'])
            run('migration-route-self-test',[sys.executable,'tools/check_migration.py','--self-test'])
            run('modules',[executable,'mod','verify'])
            run('test',[executable,'test',f'-p={args.jobs}','-json','-count=1','-timeout=90s','./...'])
            run('vet',[executable,'vet',f'-p={args.jobs}','./...'])
            run('plan-self-test',[sys.executable,'tools/check_plan.py','--self-test'])
            run('plan',[sys.executable,'tools/check_plan.py'])
            run('overview',[sys.executable,'tools/check_overview.py'])
            suffix='.exe' if os.name=='nt' else ''
            run('app-test-helper',[executable,'test',f'-p={args.jobs}','-c','-trimpath','-o',output/('ucli-app-check'+suffix),'./internal/app'])
        if args.action in ('build','all'):
            target_os = {'Windows':'windows','Darwin':'darwin','Linux':'linux'}[platform.system()]
            arch = 'arm64' if platform.machine().lower() in ('arm64','aarch64') else 'amd64'
            build(target_os,arch)
        if args.action in ('cross','all'):
            for target in [('windows','amd64'),('linux','amd64'),('darwin','arm64')]:
                if not any((b['os'],b['arch'])==target for b in binaries):
                    build(*target)
    except (RuntimeError, subprocess.TimeoutExpired) as error:
        status = 1
        print(str(error), file=sys.stderr)
    finally:
        final_sources,final_tools=fingerprints(ROOT)
        changed=(initial_sources,initial_tools)!=(final_sources,final_tools)
        if changed:
            status=1
            print('Source or verification tooling changed during this run; rebuild before accepting these binaries.',file=sys.stderr)
        receipt = {'scope':'experimental application build; host execution separate from cross-compilation',
            'platform':platform.platform(),'python':platform.python_version(),
            'revision':revision,'branch':branch,
            'jobs':args.jobs,'go_memory_environment':{key:env[key] for key in ('GOMEMLIMIT','GOGC') if key in env},
            'opt_in_python':{key:env[key] for key in ('UCLI_TEST_VOICE_PYTHON','UCLI_VOICEENV_TEST_PYTHON') if key in env},
            'commands':records,'binaries':binaries,'exit_code':status,
            'source_sha256':initial_sources,'tooling_sha256':initial_tools,
            'source_changed_during_run':changed,
            'final_source_sha256':final_sources if changed else {},
            'final_tooling_sha256':final_tools if changed else {}}
        (evidence/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8')
    return status

if __name__ == '__main__':
    raise SystemExit(main())
