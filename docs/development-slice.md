# Autonomous development slice - October 18

This document records the experimental development slice begun on 2026-10-18.
Go application development proceeded while G0 and provider/platform admission
remained open. No missing result is a pass. Unverified managed adapters stay
disabled. Contributor work does not inherit the original user's permissions:
native sign-in, credential refresh and live-agent tests need explicit authorization
for the contributor's own test environment. Preserve existing native sessions.
Credentials are never copied into UCLI.

The next complete loop is one packaged Go executable: discover choices, enter a
task, persist accepted intent, stream a clearly labelled local demonstration,
inspect its result, follow up with a new revision, and recover from interruption.
The command and interactive surfaces share one application service. Subsequent
real-runtime echo and structured adapters consume the same service boundary.

Core schema is internal, experimental v0. `internal/core` is coordinator-owned.
P08 owns `internal/state`; P09 owns `internal/process` and `internal/workspace`;
P10/P11 own `internal/cli`. The coordinator owns module/build/application/adapter
integration and shared contracts. Core tests use standard Go testing only.

Input is preserved before dispatch. An explicit model/account/effort/permission
cannot be silently replaced. Unknown provider capacity and external outcomes are
recorded as unknown. A repeated command ID returns its existing receipt and cannot
run the same work twice; changed intent with that ID is rejected. Active attempts
and workspace ownership are transactional. No timeout silently frees uncertain work.

Read-only help/version needs no database or network. Lists are local and disclose
native-only/unverified routes. Unattended checks must not require completing browser
consent. Explicit native status and already-authenticated reuse are separate from
interactive login completion. Fixture/demo success remains labelled.

## Implemented experimental boundary

The root Go application now owns SQLite task/attempt/event state, revision guards,
workspace leases, command replay, bounded process streams and shared execution.
CLI commands and the interactive prompt consume the same application service.
Cached help/version do not open state. Unavailable routes save input as a draft;
stale edits preserve a separate recovery draft rather than replacing newer text.
Windows console input distinguishes Ctrl+C from actual EOF. Test-only owner death
leaves durable accepted intent; safe local demo recovery never replays an attempt.

Task discovery uses metadata-only pages and literal title/ID search, with an
exclusive cursor bound to the exact query. Large drafts do not make ordinary task
lists exceed the full-history memory limit. Recent directory-draft restoration
also scans summaries and loads only the chosen draft. This is a live index: edited
tasks can move between pages; refresh to see them. Schema 1 scans task JSON until
enough matches are found. Explicit `ucli migrate BACKUP_FILE` enables schema 2's
materialized typed metadata, maintained by SQLite triggers. Nonmatching metadata
is still validated; this is an O(metadata rows) scan, not an indexed substring
search. Opening either an existing or new home never upgrades it automatically.

Online backups use a pinned schema-fenced SQLite read transaction, retain unknown
attempts/leases, and verify a private snapshot before atomic non-overwriting
publication. The audit opens the private copy read-write then enables query_only:
the pinned SQLite engine omits CHECK expressions when parsing a read-only database.
The snapshot is made standalone, hashed and synced. Explicit migration holds a
SQLite writer barrier through quiescence checking, backup, certificate publication,
DDL/backfill/audit and commit. Running/unknown attempts or leases block the upgrade.
The snapshot certificate cannot prove migration committed; an ambiguous commit is
reported as unknown. Older schema-1-only builds refuse an upgraded home. There is
no automatic restore, downgrade promise or physical power-loss certification.

The default genuine-terminal interface uses an inline composer and searchable
keyboard selectors. Current choice and focused candidate are distinct; arrows
preview, Enter confirms, and Escape returns unchanged. Tab discovers actions
without discarding the composition. The editor preserves grapheme boundaries,
shows multiline input, pans long lines and reacts to resize. Bracketed paste is
literal text, including leading slash commands; Ctrl+C/EOF preserve processed
partial input without submitting it. Enter still saves a draft line and explicit
`/send` submits. Foreground cancellation exits; live steering and detach remain
unavailable. `--plain`, `--no-color` or `TERM=dumb` select the static line fallback;
`NO_COLOR` retains keyboard navigation without color. Redirected/JSON paths retain
their stable command behavior. See [the local Claude reference audit](reference-claude.md)
for source-derived patterns and the limits of native comparison.

The rich task finder queries the complete archive through one cancellable worker,
debounces typing and ignores stale results. Search/page changes become retained
state only after success; pending results cannot be opened. Escape leaves the
active task, saved revision, workspace and settings untouched. Tiny viewports
disable confirmation until the choices and recovery hints are visible.

`--demo` is a simulation. `--demo --model muse/echo` is an explicit native echo
experiment limited to the pinned Windows x64 binary. Its home, auth path and
workspace are isolated; confirmed session provenance is committed before turn
dispatch. Sessions are ephemeral, so follow-ups retain the UCLI task and start a
new native session. No inference, account binding or OS sandbox is admitted.
The deterministic resolver consumes local unmetered demonstration snapshots only;
live quota observations and atomic capacity reservations are not implemented.

An offline workflow parser/planner validates bounded graphs and explicit state
snapshots. It rejects cycles and malformed documents and never retries unknown
work. It does not route, dispatch, verify completion evidence or store workflows.
`workflow view FILE` and interactive `/workflow` inspect this offline plan without
opening application state in the standalone path. The list shows dependency
readiness, supports filtering, and previews step details at wider sizes. Enter
opens details, Escape returns, and page keys scroll long prompts. The interactive
file prompt preserves an existing composition and resolves paths from the task's
workspace. There are no simulated running states or execution controls.
See `internal/workflow/README.md`, `examples/review.workflow.json` and
[the workflow reference](workflow-interactions.md).

## Native account and permission continuation

The [native sign-in slice](native-signin.md) adds offline connection metadata,
supported native login/status flows, common model/account/effort selection and
experimental pinned Codex/Claude execution. `sandbox` explicitly means full local
computer access; native read-only disables tools. Fresh account/configuration
checks precede dispatch. Unknown work remains fenced and typed native failures
retain their category through restart and command replay. Full quota reservation,
account cycling, hosted GitHub identity/sync, native session resumption and live
provider/platform admission are separate unfinished milestones. Exact executed
results belong in the continuation evidence, including failed/unknown live checks.
Source `24ae3fdf9f6b` is installed after the full build and packaged verification.
Codex/GitHub account reuse passed; Claude is signed-out. The latest real Codex
sandbox read remained unknown on an unsupported notification, so native AI execution
is unconfirmed. See [the final continuation](verification.md).

## Optional runtime and voice integration

The comfort principle extends to optional capabilities: discover them without
installing anything, prepare only what is requested, and retain choices for the
next short command. `runtime list` and `voice models` use shipped metadata.
Ordinary startup/help/task/workflow browsing does not initialize these optional
subsystems or read voice profiles. Optional standalone commands bypass the task
database. Source `1264b70beb69` passed the integrated build, packaged checks and
Windows ConPTY route and was the earlier local installation. Exact identity, measurements and
unverified boundaries are in the [application evidence](verification.md).

`runtime prepare claude` prefers exact reviewed local bytes before the pinned
artifact cache/download. The initial catalog contains Claude Code 2.1.299 direct
binaries from its signed official release manifest. Acquiring bytes does not
execute a provider, change PATH, open an account or pass managed-AI admission.

On an explicit voice/setup request, existing compatible Python packages are reused
or missing optional dependencies are prepared in an owned private site. An existing
supported interpreter is required; UCLI does not install Python. Explicit external
interpreters/sites remain unchanged. Package installation uses reviewed top-level
pins, not a complete transitive hash lock. The default English `whisper/tiny.en`
model pins revision `0d3d19a32d3338f10357c0889762bd8d64bbdeba` and four files totaling
78,090,594 bytes. Another language needs an explicitly selected local multilingual
model. Runtime/model artifacts verify byte bounds and hashes before non-overwriting
publication; complete model reuse verifies every file. Foreign or damaged entries
are preserved and reported. `--offline` forbids acquisition while permitting reuse.

`voice setup` prepares without opening the microphone, then saves Python/site/model,
language and duration in immutable records under `HOME/voice-profiles`. Later voice
requests reuse them; explicit flags/environment override saved choices. Offline
intent is per invocation and is not saved. Tab → Voice preserves composition and
inserts literal editable text; `/voice [FILE]` appends literal text to the saved
draft. Neither dispatches an AI task. Recording is bounded; rich-terminal Enter
stops and Escape cancels. Plain recording uses the duration timer. Canceled or
unconfirmed takes cannot insert a partial transcript, and uncertain cleanup blocks
another take. Standalone commands return text/JSON without opening task state.

Synthetic fixtures and local TLS checks cover control flow and file ownership.
Actual package installation, model download, hardware capture, ASR accuracy and
latency remain unverified. Darwin process launch remains disabled; cross-compilation
does not establish native voice support. This integration does not broaden G0.

The local Murmur project was a read-only interaction reference. No tracked license
or source header was found, so no source was copied or imported. The reused concepts
are bounded 16 kHz mono audio, capture/stop ownership, transcript-only output and
cache reuse. UCLI does not adopt startup downloads, global hotkeys, clipboard access
or automatic typing into another application.

No cloud, credential import, paid decision model, migration of cly, supervisor,
native escape or workflow executor is part of this boundary. GitHub application
registration/provider login, actual macOS execution and owner usability need
external evidence; they do not block independent experimental development.
