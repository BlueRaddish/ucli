# P01 disposable platform spike

This is a synthetic experiment, not the UCLI product or its public adapter API.
It makes no provider calls, uses no credentials, and creates only temporary fixture
workspaces. Go remains provisional; this spike does not complete G0.

## Repeatable route

From the repository root on this Windows host:

```powershell
python spikes/platform/build.py --go USER_HOME/.local/share/ucli-dev/toolchains/go1.27.2/go/bin/go.exe --evidence evidence/spikes/platform/local-build
python spikes/platform/verify.py --binary spikes/platform/_build/p01-windows-amd64.exe --evidence evidence/spikes/platform/local-windows
python spikes/platform/run_wsl.py --evidence evidence/spikes/platform/local-linux
```

The build script runs module checksum verification, four Go tests, `go vet`, and
cross-compiles Windows x64, Linux x64 and macOS ARM64 with `CGO_ENABLED=0`. It records
commands, exits, source hashes, binary hashes and runtime inventory. It sets Go cache
locations for its own subprocesses and does not change global PATH. `_build/` is
ignored, including Unix binaries without file extensions.

The verifier needs Python 3.9+ and Git. Its default workload is 30 paired cold trials
and 100 paired warm trials, in deterministic randomized order. `--cold N --warm N`
can shorten a diagnostic run; reduced sample counts do not meet the P01 timing gate.
Each script has `--help`. All Windows children use hidden creation flags and pipes.

`run_wsl.py` requires an existing Ubuntu WSL distribution and Python/Git inside it.
It copies the Linux executable to native WSL `/tmp`, drives the same verifier, and
removes that package copy afterward. Evidence stays at the requested repository
path. It does not install or shut down a distribution. A Linux shell can instead
run `python3 spikes/platform/verify.py --binary PATH_TO_LINUX_BINARY --evidence PATH`.
No macOS execution route was available on this machine.

The installed Go 1.27.2 archive came from the official live release metadata and was
checked against its published SHA-256 before extraction. See the
[toolchain receipt](../../docs/verification.md).
This local installation is outside the product and is not an installer for UCLI.

## What actually runs

`run` and `host` call the same `Service.Execute`. `run` is a new foreground process;
`host` is a persistent, sequential, pipe-driven fixture with no terminal dependency.
Each request still starts a fresh synthetic provider child. This demonstrates a
service/host boundary; it does not implement authenticated supervisor IPC, detach,
cross-host adoption, concurrency or a daemon lifecycle.

The service reads a strictly named fixture configuration, opens a one-connection
SQLite database in WAL/FULL mode, commits accepted intent before launching the child,
streams bounded JSONL frames, and persists the final fixture status. Actual bundled
SQLite is queried at runtime and rejected if it does not meet the documented WAL fix
gate. This build uses modernc.org/sqlite v1.60.1 and reports SQLite 3.53.4. The state
and event schemas are intentionally smaller than `planning/contract-baseline.json`;
they must not be promoted into production contracts.

Windows uses documented `CreateProcess` with `CREATE_NO_WINDOW | CREATE_SUSPENDED`,
an explicit inherited pipe-handle list, job assignment, and primary-thread resume.
The job has kill-on-close semantics. Linux/macOS use a distinct process group and
group termination. Neither implementation searches or kills by executable name.
Timeout tests exercise cooperative stop followed by forced cleanup; they do not
establish actual terminal Ctrl-C or terminal-loss behavior.

Workspace probes resolve the enclosing checkout and a filesystem identity. Tests
use a real temporary Git repository, subdirectory, Windows junction/Linux symlink,
and linked worktree. Aliases share a lease key; linked worktrees have distinct keys;
the execution directory remains distinct from the checkout root. A minimal SQLite
transaction demonstrates cross-process shared-read/exclusive-write contention.

## Checks and failure controls

The packaged verifier exercises configuration/SQLite integrity, split UTF-8 frames,
4 MiB stderr drainage, malformed/oversized/unterminated frames, provider failure,
crash after accepted intent, cooperative and forced descendant cleanup, input-wait
cancellation, workspace identity and contention, and foreground/host timings.

Its negative controls deliberately inject 200 ms delay, terminate only a parent
while a recorded fixture descendant remains alive, and leave a direct child's stdout
open past a 200 ms deadline. The leak detector must reject the parent-only cleanup
before a fixture stop file cleans that descendant. No unrelated PID is touched.
The stdout deadline is enforced while consuming, rather than only after EOF.

Raw timing samples include both absolute first-content times and their paired
difference. Cold means a new process, not flushed OS caches. Warm means an already
open fixture host, not a reusable provider session. Negative differences are retained
as measurement variability; they do not imply useful negative work. The local
150/75 ms overhead checks omit authentication, interactive rendering and real-model
latency, so they are narrower than the product SLOs.

## Known ceilings to carry into P08/P09

- The Windows drain deadline cannot unblock a downstream writer that never returns.
  A slow/non-reading terminal has not been tested; production needs a bounded output
  queue with lifecycle cancellation independent of rendering. The code flags this
  ceiling at the wait site. This spike does not certify bounded backpressure.
- Native termination error paths are not fault-injected; `TerminateProcess`,
  `TerminateJobObject` and group-kill error handling need production receipts. An
  abrupt parent death on POSIX is not proven to kill descendants. Windows job-close
  behavior is configured but abrupt owner death was not separately tested.
- The Windows launcher supports the same executable with inherited cwd/environment.
  Explicit provider environments, actual wrappers, ConPTY/PTY behavior and shell
  quoting matrices are future platform work.
- The lease fixture has no stale-owner fencing/recovery or migration maintenance
  barrier. It proves live contention only. The store is not the production task,
  receipt, event, reservation or transcript store.
- Mac execution, real secret-store access, native terminal resize/keyboard flows,
  real-provider parity, 100 MiB bounded streaming, 1/4/8 concurrency, and the full
  product latency/memory matrix remain unexecuted.

See [measurement receipt](../../docs/verification.md) for
the failed first implementation, measured correction, host/storage uncertainty and
final platform results. The ADR stays provisional until the missing gates are met.

## Primary implementation references

- [Official Go release metadata](https://go.dev/dl/?mode=json) and
  [Go build configuration](https://pkg.go.dev/cmd/go).
- [SQLite WAL documentation and fix versions](https://www.sqlite.org/wal.html).
- [Microsoft CreateProcess](https://learn.microsoft.com/en-us/windows/win32/procthread/creating-processes),
  [returned process/thread handles](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/ns-processthreadsapi-process_information),
  and [explicit handle inheritance](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute).
