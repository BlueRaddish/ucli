# Experimental Muse echo driver

This internal driver supports one explicit `muse/echo` demo turn per invocation.
It is local echo, not an AI model, and does not grant Muse or an operating system
production admission. The accepted route is runtime `muse`, provider/mode `echo`,
account `none`, effective effort `none`, permission `read-only`. The application
accepts requested effort `auto` only and resolves it to no reasoning for echo.
The application must require deliberate
demo selection; this driver must not become an automatic model fallback.

`Driver{}` implements `adapters.Driver`. It creates a unique operational home and
empty workspace below `StateHome/runtimes/muse-echo`, with an explicit environment
allowlist and absent auth file. The task's actual checkout is never supplied to
the native host. Native arguments always force `serve --provider echo
--no-session-log --disable-write --disable-shell`. The session additionally
requests `denyUnmatched` approval mode, and the response must confirm that mode,
echo routing and the same physical workspace. Every server request is refused.

The configured executable is a trusted local installation boundary. The driver
requires the observed stable schema v1 fingerprint
`sha256:6de32db08bbbe0deb478d2bfcaba001e9fb490986bc80db3aceb4d996abf7df9`
and an explicitly ephemeral handshake. Other schemas fail before a turn is sent.
Schema compatibility is not a substitute for executable provenance or provider
admission. `NativeSession` is opaque attempt metadata; memory-only sessions do not
offer persistent resume.
When supplied, `Request.OnContext` receives the confirmed namespaced session
provenance before any turn dispatch. Its failure prevents `turn/start`; the
application uses this boundary to persist the native session receipt.

## Protocol and lifecycle

- Native mutation commands get fresh cryptographic UUIDv7 IDs. RPC request IDs
  are separate, connection-local integers. This driver never retries a dispatch.
- Frames are UTF-8 JSONL bounded to 1 MiB. Duplicate/case-ambiguous JSON members,
  invalid envelopes, response IDs, nesting over 64 levels and incomplete EOF are
  rejected. The raw process queue is 4 MiB. Transcript limits are 16 MiB and 2048
  frames; these deliberate echo ceilings need a spool before general model use.
- Deltas identify an item, not a turn. Full item events establish item ownership
  by the acknowledged session and turn. Streamed agent text must agree with the
  authoritative final item. Completion requires a matching `turn/completed`, all
  agent items settled, and exactly `echo: ` plus the submitted nonempty input.
  User-message echoes and unrelated notifications cannot establish completion.
- Request waits are bounded to 8 seconds; an echo turn to 30 seconds. Caller
  cancellation gets a native interrupt and up to 2 seconds for a matching terminal.
  Only native `cancelled` proves interruption. A completed race remains complete;
  lost acknowledgments, invalid events and unconfirmed stops remain unknown.
- Output delivery must return promptly. The application owns its delivery gate;
  the driver cannot forcibly interrupt arbitrary callback code. A separate
  cancellation watcher closes the owned child after 3 seconds even if the callback
  stalls. `process.Child` bounds cleanup independently of output consumption.
- Stderr is counted in memory, never persisted as native diagnostic text. Clean
  owned-process shutdown removes the disposable operational directory. Cleanup
  errors remain observable, retain that directory, and produce an unknown outcome.
  Killing the local host never substitutes for a native terminal record.

These flags and the echo tests do not establish hostile-tool containment,
authenticated account binding, suppression of every administrator configuration
source, quota handling, persistent resume, or real model inference. The observed
Windows native handshake reported sandbox unavailable. No credentials or user
prompts were used. Linux host execution remains unverified. macOS native execution
is explicitly disabled in `process.Start` until its owned-child lifecycle has a
verified non-reaping observer. Child-dependent adapter fixtures skip on Darwin;
pure route/envelope checks remain enabled.

## Repeatable checks

Use the project's pinned Go 1.27.2 toolchain and cache setup from `tools/dev.py`.
The normal project check includes this package. For the narrow consuming route:

```console
go test -count=1 -timeout=45s -v ./internal/adapters/muse
go vet ./internal/adapters/muse
```

Tests launch the test executable as a real hidden protocol child. They exercise
isolated environment/cwd, approval refusal, split Unicode frames, genuine output,
malformed/oversized data, wrong session/turn, orphan deltas, bad final text, unknown
terminal states, lost acknowledgments, cancellation/completion races, stalled
callbacks, and cleanup. The default route skips the live native test explicitly.

To run the no-inference native test, set `UCLI_TEST_MUSE_EXE` in the test parent's
environment to the installed native Muse executable, then run:

```console
go test -count=1 -timeout=30s -run TestNativeEchoOptIn -v ./internal/adapters/muse
```

That test rejects any executable whose SHA-256 differs from the observed
Muse 1.4.7 build:
`2d1ac7af0a9ea77643cb8690eee70f52f0f8476c46b194f143c831f481aea59d`.
The environment marker is not forwarded to the native host. Only synthetic echo
text is sent. It checks genuine output and a cancellation terminal, reporting a
completion race honestly if one occurs. Initial development checks exposed that
wire approval mode uses `denyUnmatched` and the returned approval state is an
object, rather than the CLI vocabulary/string assumption; the implementation and
fixture now enforce the native shape.

Retained machine output and command/source identity are under `evidence/` beside
this file. Cross-compilation only proves package compilation, not host execution.
