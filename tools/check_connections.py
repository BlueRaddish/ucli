"""Check packaged native account linking; live inference requires a separate opt-in."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import secrets
import subprocess
import tempfile
import time

from check_app import sha

ROOT = Path(__file__).resolve().parents[1]
FLAGS = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0


class CheckFailure(ValueError):
    """Only fixed checker labels and validated error codes may enter receipts."""


def checked_rows(label, exit_code, stdout, stderr, allow_signed_out=False):
    try:
        rows = [json.loads(line) for line in stdout.splitlines()]
    except (ValueError, UnicodeError):
        raise CheckFailure(label+': machine output was not valid JSON') from None
    if stderr or not rows or not all(isinstance(row, dict) and row.get('version') == 1 and
                                     isinstance(row.get('type'), str) and row['type'] for row in rows):
        raise CheckFailure(label+': machine output contract failed')
    errors = [row for row in rows if row['type'] == 'error']
    def checking(row):
        value = row.get('data')
        return (row['type'] == 'login_action' and isinstance(value, dict) and
                value.get('type') in ('', 'checking') and not value.get('url') and not value.get('code'))
    if any(row['type'] == 'login_action' and not checking(row) for row in rows):
        raise CheckFailure(label+': noninteractive verification unexpectedly entered a native login flow')
    signed_out = (allow_signed_out and exit_code == 3 and len(errors) == 1 and rows[-1] is errors[0] and
                  all(checking(row) for row in rows[:-1]) and
                  isinstance(errors[0].get('data'), dict) and errors[0]['data'].get('code') == 'sign_in_required')
    if signed_out:
        return rows
    if exit_code != 0 or errors:
        codes = [row.get('data', {}).get('code') if isinstance(row.get('data'), dict) else None for row in errors]
        safe = [code if isinstance(code, str) and re.fullmatch('[a-z][a-z0-9_]{0,63}', code) else 'unrecognized' for code in codes]
        raise CheckFailure(label+': unexpected exit '+str(exit_code)+'; safe error codes '+str(safe))
    return rows


def signed_out_connection(item, provider):
    return (isinstance(item, dict) and item.get('provider') == provider and
            item.get('auth_state') == 'signed-out' and item.get('auth_mode') == 'none' and
            not item.get('identity') and not item.get('models') and item.get('managed_ai') is False)


def save_receipt(folder, receipt):
    # The caller created this fresh directory exclusively. Preserve the prior
    # complete receipt if writing fails; this is process-crash evidence, not a
    # claim of directory durability after physical power loss on every platform.
    pending = folder / 'receipt.pending'
    with pending.open('x', encoding='utf8') as stream:
        stream.write(json.dumps(receipt, indent=2)+'\n')
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(pending, folder/'receipt.json')


def mark_live_attempt(folder, receipt):
    with (folder/'live.attempted').open('x', encoding='utf8') as stream:
        stream.write('One explicitly authorized native request may have started. Do not retry automatically.\n')
        stream.flush()
        os.fsync(stream.fileno())
    receipt['live_requests'] = 1
    save_receipt(folder, receipt)


def require_external_sentinel(sentinel, *roots):
    resolved = Path(sentinel).resolve(strict=True)
    for value in roots:
        if not isinstance(value, (str, Path)) or not Path(value).is_absolute():
            raise CheckFailure('External-read proof requires an absolute accepted workspace root')
        if resolved.is_relative_to(Path(value).resolve(strict=True)):
            raise CheckFailure('Synthetic file is inside a project/workspace root; external-read proof was refused')


def cleanup_external_sentinel(sentinel, directory, expected_hash):
    parent = Path(tempfile.gettempdir()).resolve(strict=True)
    if (directory.is_symlink() or sentinel.is_symlink() or directory.resolve(strict=True) != directory or
            directory.parent != parent or not directory.name.startswith('ucli-external-read-') or
            sentinel.parent != directory or sentinel.name != 'sentinel.txt' or sha(sentinel) != expected_hash):
        raise CheckFailure('External synthetic file cleanup boundary changed; the file was retained')
    sentinel.unlink()
    directory.rmdir()  # Nonrecursive: any unexpected entries are preserved.


def observed_native_model(rows, provider, selected):
    status = [row.get('text') for row in rows if row.get('type') == 'status' and
              isinstance(row.get('text'), str) and row['text'].startswith('Native reported model:')]
    if len(status) != 1:
        raise CheckFailure('Live result requires exactly one native model status event')
    match = re.fullmatch(r'Native reported model: ([A-Za-z0-9._:/-]{1,256}) \| requested selection: ([A-Za-z0-9._:/-]{1,256})', status[0])
    if not match or match[2] != provider+'/'+selected or provider == 'codex' and match[1] != selected:
        raise CheckFailure('Native reported model did not match the validated selection contract')
    return match[1]


def self_test():
    def encoded(rows):
        return b'\n'.join(json.dumps(row).encode() for row in rows)
    denied = {'version': 1, 'type': 'error', 'data': {'code': 'sign_in_required'}}
    success = {'version': 1, 'type': 'connection', 'data': {}}
    checking = {'version': 1, 'type': 'login_action', 'data': {'type': '', 'message': 'Checking native sign-in'}}
    assert checked_rows('fixture', 3, encoded([denied]), b'', True) == [denied]
    assert checked_rows('fixture', 3, encoded([checking, denied]), b'', True) == [checking, denied]
    assert checked_rows('fixture', 0, encoded([success]), b'', True) == [success]
    for code, rows, stderr, allowed in [(3, [denied], b'', False), (0, [denied], b'', True),
                                      (7, [denied], b'', True), (3, [denied, success], b'', True),
                                      (3, [denied], b'PRIVATE-SENTINEL', True),
                                      (3, [{'version': 1, 'type': 'error', 'data': {'code': 'connection_changed'}}], b'', True),
                                      (3, [{'version': 1, 'type': 'error', 'data': {'code': 'sign_in_unknown'}}], b'', True),
                                      (3, [{'version': 1, 'type': 'login_action', 'data': {'type': 'url', 'url': 'PRIVATE-SENTINEL'}}, denied], b'', True),
                                      (3, [{'version': 1, 'type': 'error', 'data': {'code': 'PRIVATE-SENTINEL'}}], b'', True)]:
        try:
            checked_rows('fixture', code, encoded(rows), stderr, allowed)
        except CheckFailure as error:
            assert 'PRIVATE-SENTINEL' not in str(error)
        else:
            raise AssertionError('invalid signed-out exception accepted')
    try:
        checked_rows('fixture', 3, b'PRIVATE-SENTINEL', b'', True)
    except CheckFailure as error:
        assert 'PRIVATE-SENTINEL' not in str(error)
    else:
        raise AssertionError('malformed machine output accepted')
    item = {'provider': 'claude', 'auth_state': 'signed-out', 'auth_mode': 'none', 'managed_ai': False}
    assert signed_out_connection(item, 'claude')
    for change in ({'auth_state': 'unknown'}, {'auth_mode': 'api-key'}, {'identity': 'pinned'},
                   {'models': [{}]}, {'managed_ai': True}, {'provider': 'codex'}):
        assert not signed_out_connection(dict(item, **change), 'claude')
    with tempfile.TemporaryDirectory(prefix='ucli-check-connections-fixture-') as temporary:
        folder = Path(temporary)/'evidence'
        folder.mkdir(exist_ok=False)
        receipt = {'live_requests': 0, 'exit_code': 1}
        mark_live_attempt(folder, receipt)
        stored = (folder/'receipt.json').read_bytes()
        assert json.loads(stored)['live_requests'] == 1 and (folder/'live.attempted').is_file()
        try:
            mark_live_attempt(folder, receipt)
        except FileExistsError:
            pass
        else:
            raise AssertionError('live attempt marker was overwritten')
        try:
            folder.mkdir(exist_ok=False)
        except FileExistsError:
            pass
        else:
            raise AssertionError('evidence directory could be reused')
        assert (folder/'receipt.json').read_bytes() == stored
        (folder/'receipt.pending').write_text('foreign-or-interrupted-stage', encoding='utf8')
        try:
            save_receipt(folder, {'live_requests': 0})
        except FileExistsError:
            pass
        else:
            raise AssertionError('existing receipt stage was overwritten')
        assert (folder/'receipt.json').read_bytes() == stored
    with tempfile.TemporaryDirectory(prefix='ucli-boundary-project-') as project, tempfile.TemporaryDirectory(prefix='ucli-boundary-external-') as external:
        project, external = Path(project).resolve(), Path(external).resolve()
        workspace = project/'workspace'
        workspace.mkdir()
        old_sentinel, sentinel = project/'sibling.txt', external/'sentinel.txt'
        old_sentinel.write_text('synthetic old boundary', encoding='utf8')
        sentinel.write_text('synthetic external boundary', encoding='utf8')
        require_external_sentinel(old_sentinel, workspace)  # The former cwd-only check was insufficient.
        try:
            require_external_sentinel(old_sentinel, workspace, project)
        except CheckFailure:
            pass
        else:
            raise AssertionError('file inside project root accepted as external')
        require_external_sentinel(sentinel, workspace, project)
        try:
            require_external_sentinel(sentinel, '')
        except CheckFailure:
            pass
        else:
            raise AssertionError('missing accepted workspace root accepted')
    owned = Path(tempfile.mkdtemp(prefix='ucli-external-read-')).resolve()
    synthetic = owned/'sentinel.txt'
    synthetic.write_text('synthetic cleanup fixture', encoding='utf8')
    cleanup_external_sentinel(synthetic, owned, sha(synthetic))
    assert not synthetic.exists() and not owned.exists()
    model_event = {'type': 'status', 'text': 'Native reported model: gpt-test | requested selection: codex/gpt-test'}
    assert observed_native_model([model_event], 'codex', 'gpt-test') == 'gpt-test'
    assert observed_native_model([{'type': 'status', 'text': 'Native reported model: claude-test-1 | requested selection: claude/default'}], 'claude', 'default') == 'claude-test-1'
    for rows in ([], [model_event, model_event], [{'type': 'status', 'text': model_event['text'].replace('gpt-test', 'other', 1)}],
                 [{'type': 'status', 'text': model_event['text'].replace('gpt-test', 'invalid\nmodel', 1)}]):
        try:
            observed_native_model(rows, 'codex', 'gpt-test')
        except CheckFailure:
            pass
        else:
            raise AssertionError('missing, duplicate or mismatched native model accepted')
    print('PASS signed-out policy, diagnostic privacy, durable attempt marker, evidence non-reuse and external-root boundary; no process launched.')
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--evidence', type=Path)
    parser.add_argument('--self-test', action='store_true', help='Run offline parser/policy assertions without launching any process.')
    parser.add_argument('--provider', action='append', choices=('codex', 'claude', 'github'), default=[])
    parser.add_argument('--allow-signed-out', action='append', choices=('codex', 'claude', 'github'), default=[],
                        help='Explicitly accept verified signed-out status for this selected provider; authenticated capabilities are marked skipped.')
    parser.add_argument('--live-provider', choices=('codex', 'claude'), help='Explicitly send ONE tiny sandbox file-read task through UCLI.')
    parser.add_argument('--live-model', help='Use this exact native model ID from the linked catalog for the single live check.')
    args = parser.parse_args()
    if args.self_test:
        return self_test()
    if args.binary is None or args.evidence is None:
        parser.error('--binary and --evidence are required unless --self-test is used')
    if len(set(args.provider)) != len(args.provider):
        parser.error('--provider entries must be unique')
    if not set(args.allow_signed_out).issubset(args.provider):
        parser.error('--allow-signed-out providers must also be explicitly included in --provider')
    if args.live_provider in args.allow_signed_out:
        parser.error('--live-provider requires authentication and cannot allow signed-out status')
    if args.live_provider and args.live_provider not in args.provider:
        parser.error('--live-provider must also be explicitly included in --provider')
    if args.live_model and not args.live_provider:
        parser.error('--live-model requires --live-provider')
    binary = args.binary.resolve(strict=True)
    folder = args.evidence.resolve()
    folder.mkdir(parents=True, exist_ok=False)
    home = folder / 'home'
    workspace = folder / 'workspace'
    workspace.mkdir()
    env = os.environ.copy()
    env.pop('UCLI_HOME', None)
    env['NO_COLOR'] = '1'
    receipt = {'scope': 'packaged account metadata/sign-in reuse; existing native accounts; private output is parsed in memory and never archived',
               'binary_sha256': sha(binary), 'platform': platform.platform(), 'commands': [], 'checks': [],
               'live_requests': 0, 'skipped': [], 'failure': None, 'exit_code': 1}

    def run(label, arguments, timeout=60, allow_signed_out=False):
        start = time.monotonic()
        process = subprocess.run([str(binary), '--home', str(home), *arguments, '--json'],
                                 cwd=workspace, env=env, stdin=subprocess.DEVNULL,
                                 capture_output=True, timeout=timeout, creationflags=FLAGS)
        # Native account/identity fields and paths are deliberately absent from
        # the public receipt. Hashes bind private machine output without logging it.
        receipt['commands'].append({'label': label, 'exit_code': process.returncode,
                                    'seconds': round(time.monotonic()-start, 3),
                                    'stdout_sha256': hashlib.sha256(process.stdout).hexdigest(),
                                    'stderr_sha256': hashlib.sha256(process.stderr).hexdigest()})
        return checked_rows(label, process.returncode, process.stdout, process.stderr, allow_signed_out)

    def data(rows, kind):
        selected = [row['data'] for row in rows if row.get('type') == kind]
        if len(selected) != 1:
            raise CheckFailure('Expected exactly one '+kind)
        return selected[0]

    def passed(name):
        receipt['checks'].append(name)
        print('PASS '+name, flush=True)

    try:
        run('help', ['login', '--help'])
        assert not home.exists()
        passed('Login help does not open state, credentials or a provider.')
        discovered = data(run('discover', ['accounts', 'discover']), 'native_providers')
        assert len(discovered) == 4 and not home.exists()
        passed('Explicit discovery finds metadata without executing a provider or creating state.')
        linked, registered = {}, {}
        for provider in args.provider:
            rows = run(provider+'-connect', ['login', provider, '--no-input'], allow_signed_out=provider in args.allow_signed_out)
            if any(row['type'] == 'error' for row in rows):
                cached = data(run(provider+'-signed-out-cached', ['accounts', 'list']), 'connections')
                matches = [item for item in cached if item.get('provider') == provider]
                assert len(matches) == 1 and signed_out_connection(matches[0], provider)
                item = data(run(provider+'-signed-out-status', ['accounts', 'status', matches[0]['id']]), 'connection')
                assert item['id'] == matches[0]['id'] and signed_out_connection(item, provider)
                registered[provider] = item
                receipt['skipped'].append({'provider': provider, 'reason': 'explicitly allowed and freshly verified native signed-out status',
                                           'capabilities': ['authenticated reuse', 'authenticated model catalog', 'native inference', 'unlink preserves authentication']})
                passed(provider+': explicit native status confirms signed-out; no login or inference was initiated.')
                continue
            item = data(rows, 'connection')
            assert item['provider'] == provider and item['auth_state'] == 'authenticated'
            assert len(item['identity']) == 64 and item['managed_ai'] is False
            linked[provider] = item
            registered[provider] = item
            again = data(run(provider+'-reuse', ['login', provider, '--no-input']), 'connection')
            assert again['id'] == item['id'] and again['identity'] == item['identity']
            passed(provider+': native sign-in is reused with stable opaque binding and no interactive login.')
        listed = data(run('cached-list', ['accounts', 'list']), 'connections')
        assert len(listed) == len(registered) and {item['id'] for item in listed} == {item['id'] for item in registered.values()}
        passed('Cached connections are listed without an AI task.')
        if linked:
            models = data(run('cached-models', ['models']), 'models')
            assert any(model['runtime'] in linked for model in models) or set(linked) == {'github'}
            passed('Models consume cached native catalogs.')
        if args.live_provider:
            provider = args.live_provider
            item = linked[provider]
            model = next(model for model in item['models'] if model['id'] == args.live_model) if args.live_model else next(model for model in item['models'] if model['default'])
            # Keep the source outside the repository, not merely outside cwd:
            # UCLI's accepted workspace root can be the enclosing Git checkout.
            # Retain this one harmless synthetic file even on unknown outcome;
            # deleting it could race a native process whose stop is unconfirmed.
            external = Path(tempfile.mkdtemp(prefix='ucli-external-read-')).resolve()
            sentinel = external/'sentinel.txt'
            expected = 'UCLI_SANDBOX_READ_'+secrets.token_hex(12)
            sentinel.write_text(expected+'\n', encoding='utf8')
            require_external_sentinel(sentinel, ROOT.resolve(), workspace.resolve())
            before = sha(sentinel)
            receipt['external_sentinel'] = {'location': 'system temporary directory', 'retained': True,
                                            'sha256': before, 'outside_repository_and_cwd': True,
                                            'outside_accepted_workspace_root': False, 'cleanup_complete': False}
            # Private recovery pointer; never include this descriptor in public
            # proof archives. The public receipt contains only scope and hashes.
            with (folder/'external-sentinel.private.json').open('x', encoding='utf8') as pointer:
                json.dump({'path': str(sentinel), 'sha256': before}, pointer)
                pointer.flush()
                os.fsync(pointer.fileno())
            prompt = ('Use a native local file-reading tool to read exactly this file outside the working directory: '+str(sentinel)+
                      '. Reply only with its one-line contents. Do not modify files, use other data, browse, delegate or run additional tasks.')
            mark_live_attempt(folder, receipt)  # Persist intent before any potentially billable dispatch.
            rows = run('live-sandbox', ['run', '--account', item['id'], '--model', provider+'/'+model['id'],
                                       '--effort', 'low', '--permission', 'sandbox', '--cwd', str(workspace), prompt], timeout=150)
            attempt = data(rows, 'attempt')
            require_external_sentinel(sentinel, attempt.get('workspace', {}).get('root'))
            receipt['external_sentinel']['outside_accepted_workspace_root'] = True
            assert attempt['state'] == 'complete' and attempt['output'].strip() == expected
            assert attempt['effective']['permission'] == 'sandbox' and attempt['effective']['account'] == item['id']
            assert attempt.get('native_context', {}).get('runtime') == provider and sha(sentinel) == before
            native_model = observed_native_model(rows, provider, model['id'])
            # Store only the synthetic result and effective non-account settings.
            receipt['live_result'] = {'provider': provider, 'model': attempt['effective']['model'],
                                      'native_model': native_model,
                                      'permission': 'sandbox', 'state': attempt['state'],
                                      'output': expected, 'sentinel_unchanged': True}
            passed(provider+': ONE real UCLI task reads the synthetic file outside its workspace with sandbox permission.')
            replay = data(run('live-receipt', ['tasks', 'show', attempt['task_id']]), 'task')
            assert replay['task']['id'] == attempt['task_id']
            saved = next(value for value in replay['attempts'] if value['id'] == attempt['id'])
            assert saved['state'] == 'complete' and saved['output'].strip() == expected
            assert saved['native_context'] == attempt['native_context'] and saved['effective'] == attempt['effective']
            assert saved['workspace'] == attempt['workspace']
            passed('Real native task result and provenance survive a new process.')
            if not any(row['type'] == 'warning' for row in rows):
                # A successful CLI exit, completed result and no cleanup warning
                # establish this tested path's settled process before deletion.
                try:
                    cleanup_external_sentinel(sentinel, external, before)
                    receipt['external_sentinel']['cleanup_complete'] = True
                finally:
                    receipt['external_sentinel']['retained'] = sentinel.exists()
                    receipt['external_sentinel']['directory_retained'] = external.exists()
            else:
                receipt['external_sentinel']['retention_reason'] = 'Runtime warning; retain source rather than assume cleanup is settled.'
        for provider, item in linked.items():
            run(provider+'-unlink', ['logout', item['id']])
            reconnected = data(run(provider+'-reconnect', ['login', provider, '--no-input']), 'connection')
            assert reconnected['id'] != item['id'] and reconnected['identity'] == item['identity']
            passed(provider+': unlink preserves native authentication; explicit relinking creates a new connection.')
        assert not (home/'cache').exists() and not (home/'voice-env-v1').exists()
        passed('Sign-in prepares no runtime downloads or voice dependencies.')
        receipt['exit_code'] = 0
    except CheckFailure as error:
        receipt['failure'] = type(error).__name__+': '+str(error)
    except Exception as error:
        # Timeout/OSError exceptions can contain private command arguments or
        # native paths. Preserve their class, never their raw diagnostic text.
        receipt['failure'] = type(error).__name__+': checker did not complete; private diagnostics were discarded.'
    try:
        save_receipt(folder, receipt)
    except OSError:
        print(json.dumps({'exit_code': 1, 'failure': 'Receipt publication failed; prior evidence and attempt marker were retained.'}))
        return 1
    print(json.dumps({'checks': len(receipt['checks']), 'live_requests': receipt['live_requests'],
                      'skipped': receipt['skipped'], 'failure': receipt['failure'], 'exit_code': receipt['exit_code']}, indent=2))
    return receipt['exit_code']


if __name__ == '__main__':
    raise SystemExit(main())
