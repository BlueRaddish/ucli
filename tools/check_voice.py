"""Consume packaged voice with canonical fake libraries; no mic/network/weights."""
import argparse
import json
import os
from pathlib import Path
import platform
import subprocess

from voice_fixture import make_fixture,check_fixture,sha

ROOT=Path(__file__).resolve().parents[1]
FLAGS=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary',type=Path)
    p.add_argument('--python',type=Path,required=True)
    p.add_argument('--evidence',type=Path,required=True)
    p.add_argument('--prepare-only',action='store_true',help='Create only the explicitly fake native-UI fixture.')
    args=p.parse_args()
    if not args.prepare_only and not args.binary:p.error('Use --binary for consuming checks.')
    binary=args.binary.resolve(strict=True) if args.binary else None
    folder=args.evidence.resolve();folder.mkdir(parents=True,exist_ok=False)
    fixture_dir=folder/'fixture'
    fixture=make_fixture(fixture_dir,args.python)
    if args.prepare_only:
        print(str(fixture_dir/'fixture.json'));return 0
    receipt={'scope':'actual packaged CLI with explicitly fake ASR/capture modules and synthetic PCM; no microphone, inference, downloads or credentials',
        'binary_sha256':sha(binary),'python_sha256':sha(args.python),'fixture':fixture,'platform':platform.platform(),
        'commands':[],'checks':[],'failure':None,'exit_code':1}
    env=os.environ.copy()
    for key in ('UCLI_HOME','UCLI_VOICE_PYTHON','UCLI_VOICE_SITE','UCLI_VOICE_MODEL_DIR'):env.pop(key,None)
    home=folder/'voice-home'
    common=['--home',str(home),'--json']
    options=['--python',fixture['python'],'--python-site',fixture['site'],'--model-dir',fixture['model'],'--offline']
    def run(label,arguments,expected=0):
        command=[str(binary),*arguments]
        proc=subprocess.run(command,cwd=ROOT,env=env,stdin=subprocess.DEVNULL,capture_output=True,timeout=35,creationflags=FLAGS)
        (folder/(label+'.stdout.txt')).write_bytes(proc.stdout);(folder/(label+'.stderr.txt')).write_bytes(proc.stderr)
        receipt['commands'].append({'label':label,'arguments':arguments,'exit_code':proc.returncode})
        if proc.returncode!=expected:raise ValueError(label+': unexpected exit '+str(proc.returncode))
        if proc.stderr:raise ValueError(label+': unexpected diagnostics; inspect proof')
        return [json.loads(line) for line in proc.stdout.splitlines()]
    def data(rows,kind):
        matches=[r['data'] for r in rows if r.get('version')==1 and r.get('type')==kind]
        if len(matches)!=1:raise ValueError('Expected exactly one '+kind)
        return matches[0]
    try:
        models=data(run('models',['--home',str(folder/'unopened-home'),'voice','models','--json']),'voice_models')
        assert models[0]['id']=='whisper/tiny.en' and not (folder/'unopened-home').exists()
        receipt['checks'].append('Model catalog has no state/runtime/download side effect.')
        ready=data(run('setup',[*common,'voice','setup',*options]),'voice_ready')
        assert Path(ready['python'])==Path(fixture['python']) and ready['site_dir']==fixture['site'] and ready['model']=='local'
        assert ready['runtime_source']=='explicit-site' and ready['preferences_saved']
        receipt['checks'].append('Offline setup remembers explicit interpreter/site/model without opening microphone.')
        generations=set()
        for label,arguments,duration in [('file',['voice','transcribe',fixture['audio']],.1),('record',['voice','record','--duration','1','--no-input'],.1)]:
            # Only the setup command supplies paths. Subsequent commands must
            # consume remembered choices, preserving the ordinary short path.
            rows=run(label,[*common,*arguments,'--offline'])
            result=data(rows,'voice')
            assert result['text']==fixture['expected_text'] and result['site_dir']==fixture['site'] and result['duration_seconds']==duration
            assert result['versions']['faster-whisper']=='1.2.1' and len(result['generation'])==32
            assert result['generation'] not in generations
            generations.add(result['generation'])
            assert data(rows,'voice_prepared')['model']=='local'
            receipt['checks'].append(label+' reuses remembered paths and returns a fresh bounded transcript/provenance, never a task.')
        assert not (home/'state').exists() and not (home/'cache').exists() and not (home/'voice-env-v1').exists()
        check_fixture(fixture_dir,fixture)
        receipt['checks'].append('No task database, downloads, package mutations or fixture changes; .pth and bytecode checks hold.')
        receipt['exit_code']=0
    except Exception as error:receipt['failure']=repr(error)
    (folder/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf8')
    print(json.dumps({'checks':receipt['checks'],'failure':receipt['failure'],'exit_code':receipt['exit_code']},indent=2))
    return receipt['exit_code']

if __name__=='__main__':raise SystemExit(main())
