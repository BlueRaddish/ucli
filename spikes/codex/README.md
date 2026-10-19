# P02: Codex app-server experiment

This is a Python standard-library experiment against **codex-cli 0.162.1** on
Windows x64. It informs the provisional contract; it is not a production adapter
and does not close G0. No provider inference was performed.

## Run the consuming checks

From the repository root, with Python 3.9 or later:

```console
python spikes/codex/check.py --receipt evidence/spikes/codex/fixture-checks.json
```

The 15 checks launch a real synthetic JSONL subprocess and exercise the driver.
They cover initialization, sanitized metadata, split UTF-8 streaming, two turns
with native-style resume, confirmed interrupt, unconfirmed interrupt, crash,
failed terminal result, malformed/oversized/truncated/invalid UTF-8 frames,
unexpected response IDs, bounded event overflow, blocked writes, stderr flood,
approval denial, unknown server requests, account/trust gating, cross-process
session exclusion and owned-descendant termination. These are fixture results,
not evidence of current provider entitlement or actual tool sandbox enforcement.

Run the installed native metadata/config probe in PowerShell:

```powershell
$codexExe = 'USER_HOME/AppData/Roaming/npm/node_modules/@openai/codex/node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/bin/codex.exe'
python spikes/codex/native_probe.py --codex-exe $codexExe --receipt evidence/spikes/codex/native-probe.json
```

Use the native executable, not a PowerShell/npm wrapper. This probe also requires
Git to initialize two disposable synthetic repositories. It runs hidden, captured
children, creates fresh `CODEX_HOME`, home/profile and app-data directories, uses
an allowlisted environment and ephemeral native credential storage, then removes
its temporary state. No operator credential file, native history or ordinary
configuration is copied or read by the probe. The native process reads only the
isolated configuration plus its own platform/managed layers; unexpected nonempty
managed layers block thread creation. Native startup itself is not sandboxed.

The probe initializes the server, calls `account/read` with `refreshToken=false`,
lists models, reads the synthetic config layers, lists hook metadata, and creates
empty native threads with `approvalPolicy=never` and `sandbox=read-only`. It
observes notifications for one second. It never sends `turn/start`, prompts,
login, config mutation or token-refresh requests. Versions other than 0.162.1
are refused before the app-server starts.

## Result and remaining gates

See the [verification receipt](../../docs/verification.md).

| Boundary | Actual evidence | Limit |
|---|---|---|
| Protocol | Real initialize, account/read, model/list, config/read, hooks/list, thread/start | Live turn/start, stream, interrupt and resume are fixture-only |
| Catalog | Eight models listed while isolated account is absent | Catalog cannot establish inference entitlement |
| Account | Fresh ephemeral account/read reports `account=null` | Existing account identity/binding, OS-store namespaces and refresh collisions untested |
| Project trust | Untrusted project layer disabled; trusted project discovers an untrusted SessionStart hook | Hook execution positive control and managed/plugin/helper cases untested |
| Endpoint | Synthetic project provider override absent from effective config in both cases | Version-specific admission behavior; no network trace or credential-helper execution test |
| Permissions | Native reports readOnly/never; fixture approval requests declined | Filesystem, shell, network, connector and operational-write enforcement not proved |
| Continuation | Fixture resumes one thread and completes a second turn without injected base/developer instructions | No live native history continuation or context/tool inventory proof |
| Exclusivity | Independent fixture processes cannot acquire the same scope lease | Cooperative local lock cannot fence other native clients, hosts or a bypassing caller |

**Live inference remains blocked.** The fresh isolated route has no authenticated
connection. Reusing the ambient home would remove the isolation evidence; copying
tokens was not authorized. P04 must establish a supported account binding and
refresh namespace, and the native permission/trust checks must be completed before
the remaining authorized live-turn budget is used. Zero of the two allowed short
turns and zero of the interrupt allowance were used.

## Driver boundary

`driver.py` uses bounded JSONL frames (1 MiB), a bounded input queue (128), bounded
event retention (512), one outstanding RPC, a two-second outbound write deadline,
and request/terminal deadlines. Reaching a bound fails the attempt rather than
silently losing protocol events. Stderr is drained and counted, never retained.
It validates the frame envelope and critical terminal state, not every native
schema field. Malformed/incompatible schema means failure, not permissive coercion.

The native command path has `allow_turns=False`. Only the synthetic harness enables
turns. The flag is an experiment seam, not an authorization implementation. Do not
call the driver with a real runtime and enable turns without adding the unresolved
account/trust checks. It is not exposed as a native CLI override.

The caller holds a lease keyed by runtime/connection/workspace/thread through
resume and execution. A confirmed terminal state permits release; interruption
acknowledgement alone does not. A crash or timeout after dispatch leaves the lease
and external-effect state uncertain; the driver never retries or steals it.
The harness removes uncertain fixture leases only during disposable fixture cleanup.

The two Python provider spikes reuse `spikes/shared/owned_process.py` for ownership;
it is a test utility, not the production Go platform interface.
Windows children belong to an owned Job Object with kill-on-close; POSIX uses a
new process group. There is a Windows race between process creation and job
assignment. The descendant fixture spawns only after assignment, so it proves
owned cleanup after initialization, not containment of hostile startup code.
Killing a local job cannot prove cancellation of accepted remote work. Shutdown
does not signal unrelated terminals, process names, native daemons or sessions.
The library is a single-controller spike, not a multithreaded session manager.

## Schema provenance

[protocol-0.162.1.json](../../testdata/codex-spike/protocol-0.162.1.json) records
SHA-256 and required/top-level fields for 20 schemas emitted by the installed
binary. The fixture validates outgoing request field names and required fields
against that snapshot. Synthetic response bodies cover only fields consumed by
the driver; they are not complete native response recordings. This is shape/drift
evidence, not full JSON Schema validation. Regenerate for comparison:

```powershell
& $codexExe app-server generate-json-schema --out spikes/codex/.scratch/schema-generated
python spikes/codex/schema_manifest.py --schema-dir spikes/codex/.scratch/schema-generated --runtime-version 'codex-cli 0.162.1' --output spikes/codex/.scratch/protocol.json
```

Compare the generated manifest with the committed snapshot before accepting a
runtime upgrade. Do not replace the snapshot to make an incompatible result pass.
The full 3.6 MB generated bundle is deliberately not committed.

The source/claim ledger and exact observations are in the verification receipt.
