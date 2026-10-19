"""Explicit native Codex inspection or one short, tool-free ChatGPT-plan turn.

Uses the production Go driver, never reads/copies credentials, and retains only
sanitized metadata. No native stderr/protocol transcript is written to evidence.
"""
from pathlib import Path
import argparse
import json
import os
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--native-home', type=Path, required=True)
    parser.add_argument('--go', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--infer', action='store_true')
    parser.add_argument('--model')
    args = parser.parse_args()
    if args.infer and not args.model:
        parser.error('--infer requires an exact previously inspected --model')
    root = Path(__file__).resolve().parents[1]
    evidence = args.evidence.absolute()
    evidence.mkdir(parents=True, exist_ok=False)
    env = os.environ.copy()
    env.update(GOTOOLCHAIN='local', CGO_ENABLED='0', GOMEMLIMIT='192MiB', GOGC='40', GOMAXPROCS='2')
    env.update(UCLI_CODEX_PROBE_EXE=str(args.binary.resolve(strict=True)),
               UCLI_CODEX_PROBE_HOME=str(args.native_home.resolve(strict=True)),
               UCLI_CODEX_PROBE_RECEIPT=str(evidence/'receipt.json'),
               UCLI_CODEX_PROBE_INFER='1' if args.infer else '0',
               UCLI_CODEX_PROBE_MODEL=args.model or '')
    program = evidence/('codex-probe.exe' if os.name == 'nt' else 'codex-probe')
    commands = [[str(args.go), 'test', '-p=1', '-c', '-o', str(program), './internal/adapters/codex'],
                [str(program), '-test.run=^TestCodexNativeProbe$', '-test.v', '-test.timeout=210s']]
    records = []
    for index, command in enumerate(commands):
        result = subprocess.run(command, cwd=root, env=env, capture_output=True,
                                creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0, timeout=240)
        (evidence/f'{index}.stdout.txt').write_bytes(result.stdout)
        (evidence/f'{index}.stderr.txt').write_bytes(result.stderr)
        records.append({'command': command, 'exit_code': result.returncode})
        if result.returncode:
            break
    (evidence/'commands.json').write_text(json.dumps(records, indent=2)+'\n', encoding='utf8')
    receipt = evidence/'receipt.json'
    if receipt.exists():
        print(receipt.read_text(encoding='utf8'))
    else:
        print('Native probe did not run; inspect retained compilation output.', file=sys.stderr)
    return records[-1]['exit_code']


if __name__ == '__main__':
    raise SystemExit(main())
