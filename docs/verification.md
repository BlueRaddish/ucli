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
receipt does not prove remote failure or permit replay. The current native
integration has no confirmed successful end-to-end AI response. Native unknown
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
