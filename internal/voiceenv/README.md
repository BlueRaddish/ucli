# Lazy voice dependency resolution

`Ensure(ctx, home, Options)` is called only when a user explicitly requests Voice.
It has no startup, help, listing, background prefetch, model download, microphone
or task-dispatch hook. Options are `Python`, `SiteDir`, `Offline`, `AllowInstall`
and `Capture`. The returned `Runtime` records the actual regular Python path,
selected site, source (`interpreter`, `user-site`, `explicit-site` or
`prepared-site`) and the full observed distribution-version map.

An explicit interpreter or external site is a selection, not permission to modify
it or silently substitute another. Otherwise a completed UCLI-owned environment
is preferred before bounded `python`/`python3` PATH discovery. A failed automatic
probe can try the next PATH candidate; this package does not scan application
installations. Windows execution aliases are resolved through `sys.executable`.
That regular path must itself pass an owned probe before it can be returned: a
working Store alias does not prove its reported Program Files image can launch.

Metadata probes run the selected interpreter under `internal/process` with
`-I -S -B` and an embedded stdlib-only script. They use `importlib.metadata` and
top-level `PathFinder.find_spec`, never importing backend packages or loading
native libraries. The exact user-site path is derived by this interpreter's
stdlib. Additional directories are inspected explicitly, never through
`site.addsitedir`, `.pth` activation, cwd or inherited `PYTHONPATH`. Packages are
considered available only with both distribution metadata and a matching module
location. Requirements are faster-whisper 1.x, ctranslate2 4.x, numpy, tokenizers,
huggingface-hub, and sounddevice 0.x only for microphone capture. This is metadata
readiness, not proof of DLL/ABI compatibility, model execution or audio-device use.

Compatible existing global, exact user-site or explicit-site packages are reused
without network or filesystem mutation. Missing dependencies without
`AllowInstall`, in offline mode, or in an explicit incompatible `SiteDir` return
actionable typed `core.Error`s. The user site is never installed into. An absent
global pip is reported; no Python installer, venv, ensurepip, compiler or shell is
run. Context cancellation is preserved through `errors.Is`.

Only an explicitly allowed online install invokes selected Python with
`-I -B -m pip --isolated --no-input --disable-pip-version-check install`,
`--only-binary=:all: --index-url https://pypi.org/simple --target PRIVATE_SITE`,
`--ignore-installed --no-compile --no-cache-dir`, bounded retries, and these pins:

- `faster-whisper==1.2.1`
- `sounddevice==0.5.6` when `Capture` is true

These are top-level pins, **not a transitive lock, publisher signature, hash lock
or platform admission record**. Resulting full distribution versions are recorded.
Transitive wheel versions are resolved by the selected trusted pip. Python/pip
interpreter-global startup remains trusted executable code under `-I`; the
no-`.pth` guarantee applies to metadata probes and manually selected external
sites, not the actual pip or transcription interpreter's global startup.

Pip's ordinary settings and index environment are not inherited. The special
`PIP_CONFIG_FILE=os.devnull` override disables global/site/user configuration,
including extra indexes; this behavior is documented in [pip configuration](https://pip.pypa.io/en/stable/topics/configuration/#pip-config-file)
and implemented outside the isolated-environment option loader in
[pip 25.2 configuration source](https://github.com/pypa/pip/blob/25.2/src/pip/_internal/configuration.py).
Temporary downloads live inside the owned stage via `TMP`, `TEMP` and `TMPDIR`.
The existing Python/pip code and PyPI-resolved wheels remain executable trust
boundaries; this package does not certify their safety.

Prepared sites use `HOME/voice-env-v1`. Only newly created directories/files receive
private modes or Windows ACLs. Install/probe processes have bounded streams and
deadlines (15 seconds per probe, ten minutes per installation). A completed private
sibling receives an ownership manifest; a hardlink publishes the stable manifest
atomically without replacing a preexisting file. Cache selection is bound to
interpreter path, Python version/ABI, capture mode and the top-level install plan.
Reuse rechecks metadata and the link-free, bounded file tree. Changed or foreign
entries fail visibly and are preserved. Concurrent preparations may duplicate
wheel work, but only one completion manifest publishes and the winner is rechecked.

Failed preparations clean only their own private stage after checking its resolved
scope. Unconfirmed process cleanup or an unsafe changed staging tree retains it
for inspection. No published site or externally selected package directory is
removed. Hard process death can leave an inert partial stage. There is no scavenger,
cache repair, overwrite, transitive wheel hash audit, physical power-loss promise
or hostile same-user mutation guarantee. Prepared metadata comparison is strict:
changes in visible distribution versions can require explicit inspection even
when the primary backend versions still match.

## Verification

```console
go test -p=2 ./internal/voiceenv -count=1 -timeout=40s -v
go vet -p=2 ./internal/voiceenv
```

Private fixture seams exercise selection, missing/offline/no-pip modes, preparation,
reuse, foreign manifests, metadata changes and cleanup. Actual owned test children
exercise success, native diagnostic suppression and deadline cleanup. Setting
`UCLI_VOICEENV_TEST_PYTHON` to an explicitly reviewed regular interpreter additionally
runs the real stdlib metadata script against synthetic modules that raise if
imported and a `.pth`/sitecustomize sentinel. This check imports no backend package,
installs no wheels, downloads no model and opens no microphone. Native dependency
imports, real pip installation and real transcription remain unverified. Darwin
owned-process tests skip while that process backend is unavailable.
