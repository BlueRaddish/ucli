"""Verification-only fake ASR packages and PCM; never use real audio or models."""
import hashlib
import json
from pathlib import Path
import re
import wave

ROOT=Path(__file__).resolve().parents[1]

def sha(path):
    digest=hashlib.sha256()
    with path.open('rb') as source:
        while chunk:=source.read(1024*1024): digest.update(chunk)
    return digest.hexdigest()

def make_fixture(destination,python):
    destination=destination.resolve()
    if destination.exists(): raise ValueError('Fake fixture destination must be new.')
    python=python.resolve(strict=True)
    if not python.is_file(): raise ValueError('Select an existing regular Python executable.')
    destination.mkdir(parents=True)
    site=destination/'site';site.mkdir()
    model=destination/'model';model.mkdir()
    source=ROOT/'internal/voice/helper_test.go'
    text=source.read_text(encoding='utf8')
    # Reuse the voice package's canonical test-library fakes; do not duplicate a
    # second mock engine that can drift away from its helper protocol fixture.
    for constant,name in [('fakeNumpy','numpy.py'),('fakeWhisper','faster_whisper.py'),('fakeSoundDevice','sounddevice.py')]:
        matches=re.findall(r'const '+constant+r' = `([\s\S]*?)`',text)
        if len(matches)!=1: raise ValueError('Canonical fake module is unavailable: '+constant)
        (site/name).write_text(matches[0],encoding='utf8')
    for name in ('ctranslate2','tokenizers','huggingface_hub'):
        (site/(name+'.py')).write_text('# Verification metadata stub; no native backend.\n',encoding='utf8')
    versions={'numpy':'2.4.5','faster-whisper':'1.2.1','ctranslate2':'4.8.1','tokenizers':'0.23.1','huggingface-hub':'1.28.0','sounddevice':'0.5.6'}
    for name,version in versions.items():
        info=site/(name.replace('-','_')+'-'+version+'.dist-info');info.mkdir()
        (info/'METADATA').write_text('Metadata-Version: 2.1\nName: '+name+'\nVersion: '+version+'\n',encoding='utf8')
    marker=destination/'pth-executed'
    (site/'never.pth').write_text('import pathlib; pathlib.Path('+repr(str(marker))+').write_text("unexpected")\n',encoding='utf8')
    (site/'sitecustomize.py').write_text('raise RuntimeError("explicit site startup must not run")\n',encoding='utf8')
    for name in ('config.json','model.bin','tokenizer.json','vocabulary.txt'):
        (model/name).write_bytes(b'Synthetic fixture only; no model weights.')
    audio=destination/'synthetic.wav'
    with wave.open(str(audio),'wb') as output:
        output.setnchannels(1);output.setsampwidth(2);output.setframerate(16000)
        output.writeframes(b'\x00\x00'*1600)
    receipt={'version':1,'kind':'ucli-fake-voice-no-hardware','python':str(python),'site':str(site),'model':str(model),
        'audio':str(audio),'expected_text':'/send remains literal','source_sha256':sha(source),
        'files':{path.relative_to(destination).as_posix():sha(path) for path in destination.rglob('*') if path.is_file()}}
    (destination/'fixture.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf8')
    return receipt

def check_fixture(destination,receipt):
    for relative,digest in receipt['files'].items():
        if sha(destination/relative)!=digest: raise ValueError('Fake source/model/audio changed: '+relative)
    if (destination/'pth-executed').exists(): raise ValueError('Explicit site .pth executed.')
    if list((destination/'site').rglob('*.pyc')): raise ValueError('External fake site was mutated with bytecode.')
