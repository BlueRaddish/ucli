"""Bounded native Claude inspection; never print raw authentication output."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--executable', type=Path, default=Path.home()/'.local/bin/claude.exe')
    parser.add_argument('--config-home', type=Path, default=Path.home()/'.claude')
    parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--live', action='store_true', help='Explicitly attempt ONE tiny real no-tools adapter request; never automatically retry.')
    parser.add_argument('--go', type=Path, help='Pinned Go executable, required for --live.')
    args = parser.parse_args()
    executable = args.executable.resolve(strict=True)
    home = args.config_home.resolve(strict=True)
    env = {k: os.environ[k] for k in ('SystemRoot','WINDIR','PATH','PATHEXT','HOME','USERPROFILE','APPDATA','LOCALAPPDATA') if k in os.environ}
    env.update(CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1', DISABLE_AUTOUPDATER='1')
    default_home = Path.home()/'.claude'
    if home != default_home.resolve():
        env['CLAUDE_CONFIG_DIR'] = str(home)
    flags = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0
    args.evidence = args.evidence.resolve()
    args.evidence.parent.mkdir(parents=True,exist_ok=True)
    if args.live:
        if not args.go:
            parser.error('--live requires --go')
        env.update(UCLI_LIVE_CLAUDE='1', UCLI_LIVE_CLAUDE_RECEIPT=str(args.evidence),
                   UCLI_LIVE_CLAUDE_EXECUTABLE=str(executable), UCLI_LAUNCH_CLAUDE_HOME=str(home),
                   GOTOOLCHAIN='local', CGO_ENABLED='0', GOMAXPROCS='2', GOMEMLIMIT='192MiB', GOGC='40')
        cache = Path.home()/'.local/share/ucli-dev/cache'
        env.update(GOMODCACHE=str(cache/'gomod'), GOCACHE=str(cache/'gobuild'))
        proc = subprocess.run([str(args.go.resolve(strict=True)), 'test', '-p=1', './internal/adapters/claude', '-run', '^TestLiveClaude$', '-count=1', '-timeout=110s'],
                              env=env,cwd=Path(__file__).resolve().parent.parent,stdin=subprocess.DEVNULL,
                              stdout=subprocess.PIPE,stderr=subprocess.PIPE,creationflags=flags,timeout=150)
        receipt = json.loads(args.evidence.read_text()) if args.evidence.exists() else {'error':'Live test did not produce a receipt; captured diagnostics discarded.'}
        print(json.dumps(dict(test_exit=proc.returncode, receipt=receipt),indent=2))
        return 0 if proc.returncode == 0 else 1
    with tempfile.TemporaryDirectory(prefix='ucli-claude-inspect-') as cwd:
        proc = subprocess.run([str(executable),'--safe-mode','--setting-sources','','auth','status'],env=env,cwd=cwd,stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,creationflags=flags,timeout=20)
    try:
        native = json.loads(proc.stdout)
        fields = {key:type(value).__name__ for key,value in native.items()}
        allowed = {key:native[key] for key in ('loggedIn','authMethod','apiProvider','subscriptionType') if key in native}
        config = Path.home()/'.claude.json' if home == default_home.resolve() else home/'.claude.json'
        metadata = json.loads(config.read_text(encoding='utf8')).get('oauthAccount', {})
        stable = metadata.get('accountUuid', '')
        identity = hashlib.sha256(('claude\0'+stable).encode()).hexdigest() if isinstance(stable,str) and stable else None
        with executable.open('rb') as source:
            digest = hashlib.file_digest(source,'sha256').hexdigest()
        receipt = dict(kind='claude-auth-inspection-no-inference',executable_sha256=digest,exit_code=proc.returncode,fields=fields,status=allowed,stderr_bytes=len(proc.stderr),config_directory_matches=Path(native.get('configDirectory','')).resolve()==home,identity_available=bool(identity),identity_fingerprint=identity)
    except (OSError,ValueError,TypeError,AttributeError):
        receipt = dict(kind='claude-auth-inspection-no-inference',exit_code=proc.returncode,error='Native status was not the expected JSON object; raw output discarded.',stdout_bytes=len(proc.stdout),stderr_bytes=len(proc.stderr))
    args.evidence.parent.mkdir(parents=True,exist_ok=True)
    args.evidence.write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf8')
    print(json.dumps(receipt,indent=2))
    return 0 if proc.returncode==0 else 1


if __name__=='__main__':
    raise SystemExit(main())
