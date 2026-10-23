# Verification

Run checks that exercise the behavior being changed. A build proves compilation;
it does not prove a provider account, permission boundary, remote cancellation or
human workflow. Keep failed and skipped checks visible in a pull request.

## Fixed terminal shell — October 19

The shared rich shell has a static brand/version row, a permission/effort/account
context row and a continuous rule. A bounded transcript sits above the framed
composer. Its status is immediately above the upper rule; requested model and a
tail-preserved location sit below the lower rule, followed by key hints. At 80x24,
status/editor/footer/hints occupy zero-based rows 18/20/22/23. At 60x20 they occupy
14/16/18/19. Multiline input grows upward without moving the final editor row or
footer. Menus retain visible composition and replace the editing hint with an
explicit draft-kept hint. Standalone panels show their own title/location and
controls, without a fake model or editable prompt. Small heights drop decoration
before status and input.

`--no-color` and `NO_COLOR` retain rich navigation. `--no-motion` makes the embedded
catalog static; `--ascii` changes only cat artwork. `--plain` selects the static
line interface. The notice cat follows actual operation hooks and yields to
errors, warnings and long status text. Catalog loading is local and lazy; no
startup asset download is involved.

Run the existing `tools/dev.py all` route below after freezing Go and tooling
sources. Its normal unit checks cover shell geometry, semantic colors, Unicode,
scroll routing, hostile output, bounded shutdown and mascot lifecycle/stale-tick
behavior. Packaged Windows ConPTY
checks require the exact just-built executable; set `UCLI_CONPTY_BINARY` and
`UCLI_CONPTY_EVIDENCE` before the existing `TestPackagedConPTY` route. Set
`UCLI_TERMINAL_QA_DEPS` to an isolated existing pyte 0.8.2 installation for
`TestPackagedConPTYFixedShell` screen-coordinate assertions; these check actual
header/rules/status/editor/footer/hints, multiline expansion, overlay return,
resize, monochrome navigation and a static ASCII welcome. Negative controls
reject moved or missing semantic rows and incorrect colors. Optionally select
Python with `UCLI_TERMINAL_QA_PYTHON`. Check the QA helper first with
`python tools/check_terminal_frame.py --deps PATH --self-test`. These checks use
an isolated virtual console and synthetic/demo data, with no desktop input or
provider inference. Geometry captures are ANSI-emulated evidence, not physical
screenshots or human usability measurements. macOS and Linux cross-builds do
not establish host execution or terminal behavior on those platforms.

## Conversation transcript and native discovery - October 21

The [conversation contract](conversation-ui.md) keeps the existing frame and
editor while giving submitted prompts a structural band and assistant replies
their own typed records. Deltas append to one reply; saved history takes roles
from attempt input/output. Provider prose cannot create UCLI role headings or
controls. Code fences and indentation remain literal; this slice adds no Markdown
renderer or inferred tool summaries. Informational execution status uses the
notice instead of repeating inside the reply. Warnings/errors remain retained.

`Ctrl+Up` reveals the latest retained prompt. `Ctrl+End` returns to the output
tail; `PageUp` pauses following. Incoming fragments and resize retain a source
anchor rather than a tail-relative row count. Pruned source or wrapped rows must
disclose omission and remove unavailable prompt targets. Verify the independent
64 KiB source, 400-record and 600-wrapped-row bounds, including newline-heavy
output and a single oversized record. Native terminal mouse reporting remains
off. Visible transcript history does not imply native provider memory; the
current native-context limitation must remain visible.

Focused checks use the existing Go and hidden-console routes:

```console
go test -p=1 -count=1 ./internal/cli -run 'TestTranscript|TestTerminalShell|TestFramed|TestTerminalCompletion|TestCompletion|TestModel|TestConnectionChooser|TestConnectionExplanation'
go test -p=1 -count=1 ./internal/adapters/claude ./internal/connections ./internal/app
```

After freezing sources and building, use the same `UCLI_CONPTY_BINARY`,
`UCLI_CONPTY_EVIDENCE` and `UCLI_TERMINAL_QA_DEPS` setup described above:

```console
go test -p=1 -count=1 ./internal/cli -run '^TestPackagedConPTY(Transcript(Navigation|Bands)|ModelProviderDetails)$'
```

The packaged paths exercise prompt/tail targets, fixed editor/footer positions,
unsent composition, resize, return from actions, model-detail inspection and
monochrome. Check prompt and reply cell styles through the captured frame's
foreground/background/reverse spans. The observed Windows route retains an
unknown background despite attempted OSC 11 replies, so its packaged band check
verifies paired default-color inversion across the full prompt width. Separate
terminal rendering tests verify exact explicit light/dark RGB pairs. Delivery of
a known theme in this packaged Windows route remains unverified. Retain the raw
bytes and exact observation/frame offsets; use `tools/render_terminal.py --frame PATH` to bind
inspection to the asserted frame, including resize boundaries and cursor. These
are owned virtual-console
and ANSI-emulation checks, not desktop screenshots or evidence from other hosts.
Run the complete `TestPackagedConPTY` family before local delivery.

Claude catalogs now require initialization metadata without a user message.
Exercise new/removed names, exact alias resolution, disabled models, supported,
fixed and unknown effort, malformed/unsolicited control frames, EOF and timeout.
Catalog refresh and authentication are separate from execution admission. On a
changed native executable, preserve the account ID, binding pin and last-good
catalog; execution compatibility stays unknown. AGY refresh remains catalog-only.
Ordinary model browsing must launch no native processes. Distinguish fixture
checks, an actual initialization-only native probe and actual model inference.

Connection metadata extensions use format 2 on explicit writes. Verify old
format-1 reads, checksum/canonical-byte validation, malformed extension rejection,
and refusal by a legacy reader instead of silent field loss. Browsing must not
rewrite old history or migrate the task SQLite database. Do not test this against
a personal home or claim a rollback to an older binary can read new metadata.

Codex's [reviewed notifications](../internal/adapters/codex/notifications.md)
have actual-child fixtures plus separate App persistence/restart/replay checks.
They do not establish a successful live native response. No notification,
checklist step, timeout or process exit may replace matching turn terminal proof.

## Inline slash completion - October 20

The rich composer uses the same supported-command registry as `/help`. Typing `/`
opens descriptions in a bounded part of the transcript body; `/mo` narrows the
matches. `Up`/`Down` wrap, `Tab` completes the selection, and `Enter` completes a
partial command without dispatch. Fully typed commands retain normal `Enter`
behavior; an inserted command has a trailing space and runs only on a subsequent
submission. `Escape` preserves the input and keeps completion dismissed until an
edit. Arguments, multiline input, ordinary slash-containing text and bracketed
paste do not activate completion. No-match feedback leaves input editable. The
popup must preserve composition, cursor, notice and model/path footer positions.

Run `go test -count=1 ./internal/cli -run 'TestTerminalCompletion|TestCompletion'`
for the pure completion, rendering and boundary tests. On Windows, set the same
`UCLI_CONPTY_BINARY`, `UCLI_CONPTY_EVIDENCE` and `UCLI_TERMINAL_QA_DEPS` variables
as the fixed-shell route above, then run:

```console
go test -count=1 ./internal/cli -run '^TestPackagedConPTYSlashCompletion$'
```

The packaged test drives all supported commands through the bounded popup,
wrapping and prefix filtering, completion before action, Escape/edit reopening,
no-match recovery, 60x20 resizing, literal `/send` paste and interrupted input.
It checks unchanged saved selections and zero attempts while browsing/completing,
and verifies retained literal text without provider dispatch. The frame negative
control rejects a detached cursor or lost input. This is a hidden virtual-console
route with synthetic state; no native account, model download or real inference
is required. A skipped opt-in or a test definition is not a packaged pass.

## Direct prompts, model setup and session monitoring - October 20

A nonempty Enter sends one reviewed composition; empty Enter starts nothing.
Newline keys, bracketed paste, voice insertion, editable draft restoration and
menu return must preserve literal text. Missing prerequisites keep unsent input,
and a CAS conflict keeps a separate recovery draft instead of retrying. In rich
mode, first Ctrl+C shows a two-second confirmation; the second confirms. Typing
or Escape disarms it, stale timers cannot clear newer notices, and noninteractive
SIGINT remains immediate. Unknown native outcomes retain their existing fence.

The model picker must show unavailable cached native models and AGY models as
inspection/setup actions, never executable selections. Verify unique row focus
identities, keyboard navigation, independent pins and cancellation. Local path
presence, cached sign-in, version compatibility and execution support are distinct.
AGY metadata refresh is explicit, bounded and display-only; ordinary listing must
not launch a native process. Test strict malformed catalog rejection and offline
cache reload. The default interactive account action checks/reuses existing
sign-in; fresh browser/device sign-in is a separate explicit action.

The read-only `/sessions` overview refreshes saved status and opens bounded
activity/output detail. Back and refresh preserve query/focus plus the active
composition, pins and concurrency revision. Running is a recorded state, not a
liveness proof. Check unknown labels, malformed event identity/sequence, bounded
UTF-8 output, failed reads, cancellation and worker cleanup. The monitor must not
dispatch, select an active task, cancel an executor or resume uncertain work.

Focused checks from the repository root:

```console
go test -count=1 ./internal/cli -run 'TestEnter|TestInterruptConfirmation|TestSessionMonitor|TestModel|TestConnection'
go test -count=1 ./internal/app -run 'TestModelCatalog|TestCatalogDoesNotAdmit'
go test -count=1 ./internal/connections -run 'TestAGYCatalog'
```

After freezing source and building, reuse the Windows ConPTY environment above
for the real packaged input path:

```console
go test -count=1 ./internal/cli -run '^TestPackagedConPTY(EnterSendAndInterruptGuard|RichComposer|ConnectionDiscoveryAndSandbox|FixedShell|SlashCompletion|RichTaskFinder|VoiceDraftAndCancellation)$'
```

Use the current session-monitor packaged route when present, and run the complete
`TestPackagedConPTY` family for delivery. Keep proof from the exact binary, inspect
80x24 and 60x20 light/dark captures, and check notice/editor/footer/cursor placement.
All these recipes use synthetic homes/demo or pure fixtures. No provider login,
real inference, downloads or microphone are needed. Definitions, skipped opt-ins
and earlier-version receipts are not a pass for the new behavior.

## Session event catch-up - October 22

The read-only Sessions worker drains saved 100-event pages without an idle delay
while more remain. Explicit refresh continues the same task/attempt cursor; task,
view and attempt changes reset it. Idle reads and failures retain normal polling.
Keep output bounded, sequence/identity checks, cancellation between pages and
rejection of results from superseded requests.

Run `go test -p=1 -count=1 ./internal/cli -run '^TestSessionMonitor'` for actual
App/Store catch-up plus cursor, failure and interruption checks. After building,
reuse the ConPTY environment above and run
`go test -p=1 -count=1 ./internal/cli -run '^TestPackagedConPTYSessionMonitorBurstAndRefresh$'`.
It drives saved synthetic progress, manual refresh and return to an unchanged
draft through the packaged console. Its raw-output timing excludes physical
paint latency and is not native inference or cross-machine messaging evidence.

## Build and ordinary checks

Use Go 1.27.2 and Python 3.13. The Python tooling uses the standard library.
From the repository root:

```console
python tools/dev.py all --go PATH_TO_GO --jobs 1 --evidence .build/verification
python tools/check_app.py --evidence .build/acceptance
```

The build route verifies module checksums, runs Go tests and vet, checks the
planning documents and overview, builds the crash-test helper, and cross-builds
Windows amd64, Linux amd64 and Darwin arm64. It retains exact source/tool hashes
and refuses a result whose inputs changed during the run. A cross-build is not
execution on that host. The packaged checker uses disposable state and synthetic
responses for task, draft, result, replay and recovery paths.

The pinned GitHub Actions workflow uses the same route with read-only repository
permissions. Its presence is not a claim that remote CI has run. Native provider
calls, interactive sign-in and real microphones are separate opt-ins.

For a focused code change, run the affected packages first:

```console
go test -count=1 ./internal/state ./internal/app
go vet ./internal/state ./internal/app
```

Keep timing runs separate from tests and builds. On busy hosts, `--jobs 1` bounds
build concurrency; Go memory limits may also be set for the development process.
They do not change the installed application's defaults.

## Documentation and offline experiments

```console
python tools/check_plan.py --self-test
python tools/check_plan.py
python tools/check_overview.py
python tools/check_cat_animations.py --self-test
python tools/check_cat_animations.py
python spikes/experience/check.py --receipt .build/checks/experience
python spikes/accounts/check.py --receipt .build/checks/accounts
python spikes/codex/check.py --receipt .build/checks/codex.json
python -m unittest discover -s spikes/second-runtime -p test_probe.py -v
```

Planning checks validate the package graph, generated index, Markdown links and
overview data. They do not validate the architecture or render the interface.
After changing `planning/backlog.json`, regenerate its readable index with
`python tools/check_plan.py --render`. Each spike README describes its boundary.

The cat catalog checks validate the design frames, timing bounds, declared cell
budget, static poses and ASCII fallback data, including malformed-input controls.
They do not render glyphs or establish terminal font, text-direction or animation
behavior. The embedded catalog and rich-terminal lifecycle tests are separate
from these asset checks. See [the mascot runtime scope](cat-mascot.md); neither
unit coverage nor a cross-build establishes physical terminal compatibility.

## Packaged interaction and storage

Windows interaction checks use hidden ConPTY instances without taking desktop
focus. Build first, then follow the environment and invocation instructions in
[the console test guide](../internal/cli/conpty.md). Verify discovery, composition,
confirm/cancel, resize, Unicode, interruption and return to saved work. Screenshots
alone are not evidence of these state transitions or accessibility.

The state suite tests concurrent writers, replay, revision conflicts, unknown
outcomes, explicit snapshots, migration barriers and damaged records. Ordinary
open does not scan every saved record: `ucli doctor` explicitly audits integrity,
including schema-2 metadata consistency. Do not run migration, restore, installer
or crash tests against a personal UCLI home. The migration checker requires an
explicit retained older binary and synthetic homes; see its `--help`.

The local installer checker uses temporary destinations and exact build/acceptance
receipts. It does not publish releases or update other programs. Explicit snapshot
readback in a test is not a public restore feature.

## Optional voice and native accounts

`python tools/check_voice.py --binary PATH --python EXISTING_PYTHON --evidence NEW_DIR`
uses fake backend modules and synthetic PCM in an isolated home. It does not fetch
model weights or open a microphone. Real dependency downloads, backend ABI,
capture and recognition need their own authorized checks.

`python tools/check_connections.py --self-test` is offline. Additional explicit
provider arguments inspect existing native account metadata; status can perform
the native client's own credential refresh. Read [native sign-in](native-signin.md)
before running them. No ordinary contributor check should need a personal account,
live inference, paid quota or a credential copy.

Live opt-ins are bounded real requests, not retries of a fixture. An unknown
receipt does not prove remote failure or permit replay. The October 18 native checkpoint did not confirm a successful end-to-end AI
response; that historical outcome does not describe later device checks. Current
reviewed profiles are listed in [native sign-in](native-signin.md), and live
results must be reported separately from fixture passes. Native unknown
reconciliation is unavailable. The next protocol work is specified in
[the development priorities](next-development.md); use offline event fixtures
before another real request.

## Evidence and publication

Store local receipts in ignored `.build` directories. Review command output before
sharing it: tokens, identities, device codes, local paths and private prompts must
not enter an issue, PR or public artifact. Publish the smallest synthetic or
redacted proof that reproduces the behavior, including its limitations.

This public checkout begins with a clean history. Private development transcripts,
machine-local receipts, authoring sessions, native stores and installed-program
source inspections are intentionally absent. Historical design documents may
describe local checks; their presence is not current public release certification.
The runnable recipes and synthetic fixtures are available so contributors can
establish evidence on their own hosts.
