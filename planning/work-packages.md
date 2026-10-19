# Agent work packages

Generated from [backlog.json](backlog.json). Edit the JSON, then run `python tools/check_plan.py --render`.

Package statuses are recorded below. Sizes are relative, not calendar promises. Ownership is exclusive while active; the coordinator serializes overlapping paths and shared-contract changes.

## Assignment contract

Give the agent its package ID, accepted contract revision, isolated worktree/branch, allowed paths, fixtures/access, acceptance cases and required evidence. The agent returns changed files, commands/exit codes, failed/skipped checks, receipts, residual risks and integration notes. It must not edit cly or shared native credentials.

## Dependency overview

| ID | Package | Phase | Size | Depends on |
|---|---|---:|---|---|
| P00 | Freeze the first contract and product baseline | 0 | M | none |
| P01 | Validate packaged core, process and terminal choices | 0 | L | P00 |
| P02 | Prove the first structured runtime | 0 | M | P00 |
| P03 | Prove and choose a second runtime | 0 | M | P00 |
| P04 | Prove account and quota isolation | 0 | L | P02, P03 |
| P05 | Validate first-use and recovery experience | 0 | M | P00 |
| P06 | Establish build and cross-platform foundation | 1 | M | P01, P04, P05 |
| P07 | Implement domain and effective-setting contracts | 1 | M | P00, P06 |
| P08 | Implement durable state and artifacts | 1 | L | P07 |
| P09 | Implement portable process and stream execution | 1 | L | P07 |
| P10 | Implement unified commands, help and completion | 1 | M | P07 |
| P11 | Implement prompt, home and visible controls | 1 | L | P05, P10 |
| P12 | Implement first production adapter | 1 | L | P02, P07, P09 |
| P13 | Implement second production adapter | 1 | L | P03, P07, P09 |
| P14 | Integrate a complete foreground task | 1 | L | P08, P09, P10, P11, P12, P13 |
| P15 | Implement connection and credential management | 2 | L | P04, P08, P09 |
| P16 | Implement model and capability catalogs | 2 | M | P12, P13, P15 |
| P17 | Implement quota observation and reservations | 2 | L | P15, P16, P08 |
| P18 | Implement deterministic routing and explanation | 2 | L | P07, P16, P17 |
| P19 | Integrate model/account choices into everyday work | 2 | M | P14, P15, P16, P18 |
| P20 | Implement optional supervisor and reconnect | 3 | L | P14, P15 |
| P21 | Implement workflow graph and scheduling | 3 | L | P18, P20 |
| P22 | Implement checkpoints and cross-agent handoff | 3 | M | P08, P13, P21 |
| P23 | Integrate workflow steering and recovery | 3 | L | P21, P22, P19 |
| P24 | Evaluate bounded decision-agent routing | 3 | L | P18, P23 |
| P25 | Publish internal-quality adapter extension kit | 4 | L | P12, P13, P18 |
| P26 | Admit additional providers by capability tier | 4 | L | P25, P17 |
| P27 | Harden native escape and add optional cly import preview | 4 | M | P09, P15, P19 |
| P28 | Add GitHub identity and settings metadata sync | 4 | L | P15, P21 |
| P29 | Harden packaging, updates and diagnostics | 4 | L | P14, P19, P23, P25 |
| P30 | Maintain performance regression evidence | 1 | M | P06, P09 |
| P31 | Maintain failure and privacy boundary coverage | 1 | L | P08, P09, P12 |
| P32 | Run recurring usability and accessibility checks | 1 | M | P11, P14 |
| P33 | Maintain onboarding and capability documentation | 1 | M | P14 |
| P34 | Integrate and sign off milestone evidence | 4 | M | P29, P30, P31, P32, P33 |

## Detailed assignments

### P00 — Freeze the first contract and product baseline

**Owner role:** coordinator · **Phase:** 0 · **Size:** M · **Status:** done

**Prerequisites:** none. **Requirements:** R01, R02, R09, R13.

Resolve command, identity, event and permission vocabulary before shared implementation.

**Exclusive paths while assigned:** `docs/contracts.md`, `docs/decisions.md`, `planning/backlog.json`, `planning/contract-baseline.json`, `planning/active-work.md`

**Deliverables:** Versioned contract examples; requirement map; accepted spike questions.

**Acceptance:**

- Explicit pins, auto scope and native exceptions agree across documents.
- Every future capability has an owner and admission check.
- No task authorizes mutation of cly or native credentials.
- The readiness-review boundary decisions are a provisional spike baseline; v0 is accepted at G0 after evidence, not frozen before provider investigation.
- Task follow-up/revision, workspace/session ownership, native context, permission dimensions, executor ownership and command/migration rules have concrete scenario examples.

**Verification:** Review cross-doc scenarios; run plan integrity checker.

### P01 — Validate packaged core, process and terminal choices

**Owner role:** platform · **Phase:** 0 · **Size:** L · **Status:** active

**Prerequisites:** P00. **Requirements:** R08, R10, R11.

Prove a practical core stack with bounded disposable experiments.

**Exclusive paths while assigned:** `spikes/platform/**`, `docs/adr/0001-core-runtime.md`, `evidence/spikes/platform/**`

**Deliverables:** Packaged Go spike; comparison of viable alternatives; timing samples and lifecycle receipts.

**Acceptance:**

- Windows/macOS/Linux parse, DB, spawn, stream and interrupt paths execute.
- Harness detects injected delay and broken child cleanup.
- Language recommendation records packaging tradeoffs and unreachable OS checks.
- The same minimal execution service runs under a foreground host and a supervisor-like fixture host without terminal-dependent domain logic; no production supervisor or live adoption required.
- Workspace root aliases, subdirectories and distinct worktrees produce the expected platform identity/lease behavior.

**Verification:** Packaged 30 cold/100 warm local trials; compare matching direct-child launch; record p50/p95/errors. Code and scoped Windows/Linux receipts exist; macOS, real credential-store and terminal checks remain open. See evidence/2026-10-18-phase0/verification.md.

### P02 — Prove the first structured runtime

**Owner role:** adapter-codex · **Phase:** 0 · **Size:** M · **Status:** active

**Prerequisites:** P00. **Requirements:** R03, R09, R11, R14.

Validate a Codex managed integration without inferring capability from catalog alone.

**Exclusive paths while assigned:** `spikes/codex/**`, `testdata/codex-spike/**`, `evidence/spikes/codex/**`

**Deliverables:** Versioned protocol fixtures; minimal lifecycle driver; documented auth mode and limits.

**Acceptance:**

- Start, stream, interrupt and native continuation tested on a synthetic task.
- Permission restrictions are enforced or explicitly unsupported.
- Catalog, account identity and inference entitlement are separately recorded.
- An untrusted synthetic repository cannot execute its sentinel hook or redirect endpoint/credential-helper configuration before authorization; loaded/suppressed sources are recorded.
- Sequential continuation works within its supported scope; simultaneous attempts cannot mutate the same native session, and native instructions/tools are not duplicated by UCLI injection.

**Verification:** Official protocol fixture replay plus bounded authorized live smoke; malformed frame and failure paths. Fifteen fixture checks and ten no-inference native metadata observations passed. Authenticated lifecycle, binding and full trust/permission enforcement remain open. See evidence/2026-10-18-phase0/verification.md.

### P03 — Prove and choose a second runtime

**Owner role:** adapter-second · **Phase:** 0 · **Size:** M · **Status:** active

**Prerequisites:** P00. **Requirements:** R03, R09, R11, R14.

Choose a second daily-use runtime by supported lifecycle and UX fit.

**Exclusive paths while assigned:** `spikes/second-runtime/**`, `testdata/second-runtime-spike/**`, `evidence/spikes/second-runtime/**`

**Deliverables:** Candidate comparison; selected integration/auth scope; compatible version fixtures.

**Acceptance:**

- Second runtime completes the same task/interrupt/result contract.
- Native model fallback and permission defaults cannot violate a fixed request.
- Hosted subscription-login scope is not inferred from local invocation.
- An untrusted synthetic repository cannot execute its sentinel hook or redirect endpoint/credential-helper configuration before authorization; loaded/suppressed sources are recorded.
- Sequential continuation works within its supported scope; simultaneous attempts cannot mutate the same native session, and native instructions/tools are not duplicated by UCLI injection.

**Verification:** Replay shared adapter scenarios; live synthetic smoke where authorized; record unverified account/platform paths. Ten consuming fixture checks and native Muse echo continuation/interrupt passed. Muse is a bidirectional candidate; authenticated binding, trust and permission admission remain open. See evidence/2026-10-18-phase0/verification.md.

### P04 — Prove account and quota isolation

**Owner role:** accounts · **Phase:** 0 · **Size:** L · **Status:** active

**Prerequisites:** P02, P03. **Requirements:** R04, R07, R14.

Test the riskiest multi-account assumptions before building account switching.

**Exclusive paths while assigned:** `spikes/accounts/**`, `testdata/account-isolation/**`, `evidence/spikes/accounts/**`

**Deliverables:** Provider/OS isolation matrix; refresh-race observations; quota source fixtures.

**Acceptance:**

- Two test accounts cannot overwrite each other's credentials or run identity.
- Shared keychain/daemon/cache/port scope is inspected, not inferred from HOME.
- Unknown quota and shared pools are represented without invented precision.

**Verification:** Synthetic identity/refresh tests first; authorized real test identities only; no user-session mutation. Twelve synthetic checks passed, including two detected unsafe controls. Real provider/OS isolation and remote refresh remain unverified. See evidence/2026-10-18-phase0/verification.md.

### P05 — Validate first-use and recovery experience

**Owner role:** experience · **Phase:** 0 · **Size:** M · **Status:** active

**Prerequisites:** P00. **Requirements:** R01, R02, R11, R15.

Find friction before committing to the interface framework.

**Exclusive paths while assigned:** `spikes/experience/**`, `docs/experience.md`, `evidence/usability/phase0/**`

**Deliverables:** Task walkthrough/prototype; owner observations; revised experience contract.

**Acceptance:**

- Owner can find model/account/task actions without being taught shortcuts.
- Draft survives a simulated failed login and task survives a recoverable failure.
- Fixed route, auto route and native mode are distinguishable without jargon.
- After a completed answer, a follow-up preserves the visible task and prior evidence while creating fresh work; New task and Resume have distinguishable effects.
- A runtime change shows context/tool differences; unsupported live foreground detach cannot be mistaken for managed background survival.

**Verification:** Natural-goal formative walkthrough; report independent success, assistance, hesitations and revisions. Runnable terminal prototype and fourteen automated subprocess recovery checks exist. Owner feedback, physical terminal interruption and accessibility remain unobserved. See evidence/2026-10-18-phase0/verification.md.

### P06 — Establish build and cross-platform foundation

**Owner role:** foundation · **Phase:** 1 · **Size:** M · **Status:** active

**Prerequisites:** P01, P04, P05. **Requirements:** R08, R13.

Create only the runnable foundation needed by the first vertical slice.

**Exclusive paths while assigned:** `go.mod`, `go.sum`, `cmd/ucli/**`, `.github/workflows/**`, `.gitignore`

**Deliverables:** Pinned toolchain/dependencies; version command; CI OS matrix; local build instructions.

**Acceptance:**

- Packaged executable starts on declared OS/architecture targets.
- Build avoids modifying PATH, cly or native profiles automatically.
- Dependency update and bundled SQLite engine version are inspectable.
- Coordinator records G0 acceptance after both real-adapter spikes, account isolation and experience findings; unresolved gates cannot be bypassed by the dependency scheduler.

**Verification:** Build/test matrix and packaged version smoke; current CLI install remains unchanged.

### P07 — Implement domain and effective-setting contracts

**Owner role:** core · **Phase:** 1 · **Size:** M · **Status:** active

**Prerequisites:** P00, P06. **Requirements:** R02, R03, R09, R14.

Provide typed contracts and one resolver used by every entrypoint.

**Exclusive paths while assigned:** `internal/core/**`, `internal/app/resolve*`, `protocol/core/**`

**Deliverables:** IDs/events/errors; policy snapshot; requested/effective configuration result.

**Acceptance:**

- Explicit pins are never silently overridden.
- Untrusted project config cannot widen account, plugin or permission authority.
- Unsupported required semantics fail with a useful typed error.
- Command IDs and expected revisions give idempotent durable receipts, reject changed replay payloads and stale edits, and consume approval responses at most once; monotonic cancellation remains possible.
- Permission dimensions, workspace/task revision identity, service runtime identities and explicit no-auth connections have typed representations without requiring a shared tool loop.

**Verification:** Table-driven trust-boundary tests and two consuming adapters; version mismatch and malformed input.

### P08 — Implement durable state and artifacts

**Owner role:** state · **Phase:** 1 · **Size:** L · **Status:** active

**Prerequisites:** P07. **Requirements:** R05, R11, R14.

Persist critical intent/results and recover incomplete writes.

**Exclusive paths while assigned:** `internal/state/**`, `internal/artifacts/**`, `testdata/state/**`

**Deliverables:** Local SQLite schema/migrations; artifact manifests; backup/export/recovery path.

**Acceptance:**

- Crash between dispatch intent, acceptance and result yields correct reconciliation state.
- Disk full and migration failure never report accepted/saved work.
- Old binaries refuse incompatible writes; secrets are absent from DB fixtures.
- An incompatible migration refuses/waits while an older executor can write; a maintenance barrier/schema epoch covers already-open writers.
- Separate UCLI processes serialize conflicting workspace writers and native-session turns, including alias paths; uncertain orphan effects require reconciliation before reuse.
- Simultaneous approval responses and replayed mutation commands preserve one accepted outcome and its receipt.

**Verification:** Fault injection at transaction/file boundaries; integrity/backup restore; bundled SQLite version assertion.

### P09 — Implement portable process and stream execution

**Owner role:** platform · **Phase:** 1 · **Size:** L · **Status:** active

**Prerequisites:** P07. **Requirements:** R08, R10, R11.

Own child lifecycles and streams without shell injection or unrelated process impact.

**Exclusive paths while assigned:** `internal/process/**`, `internal/workspace/**`, `testdata/process-child/**`

**Deliverables:** Argument-array launch; wrapper handling; process ownership; bounded framed streams.

**Acceptance:**

- Unicode/spaces/metacharacters reach child as literal data.
- Flooded stderr/partial UTF-8/large output does not deadlock or grow memory without bound.
- Interrupt/escalation only affects owned children; PID reuse is rejected.
- Reusing a live native process across attempts retains its host; changing host requires shutdown/relaunch or separately verified reconnection.

**Verification:** Real packaged fixture on three OSes; Windows Job/nested-job and POSIX descendant cleanup tests.

### P10 — Implement unified commands, help and completion

**Owner role:** cli · **Phase:** 1 · **Size:** M · **Status:** active

**Prerequisites:** P07. **Requirements:** R01, R02, R15.

Make commands discoverable and scripts dependable from one command registry.

**Exclusive paths while assigned:** `internal/cli/**`, `protocol/cli/**`, `testdata/help/**`

**Deliverables:** Grammar, completion, tiered help, stdout/stderr and exit contracts.; Basic native command registration; integrated with the runner in P14

**Acceptance:**

- Help/completion do not call a provider or require writable DB.
- No-input path returns actionable error rather than hanging.
- JSON contains no terminal escapes; error exits agree with contract.

**Verification:** Golden help/parse tests plus shell completion in supported shells; 80/95-column help lint.

### P11 — Implement prompt, home and visible controls

**Owner role:** experience · **Phase:** 1 · **Size:** L · **Status:** active

**Prerequisites:** P05, P10. **Requirements:** R01, R02, R10.

Deliver a small useful interactive home, preserving scrollback and input.

**Exclusive paths while assigned:** `internal/tui/**`, `testdata/tui/**`

**Deliverables:** Prompt, recent tasks, searchable choices, focus/navigation and accessible plain mode.

**Acceptance:**

- Common task starts with prompt and Enter after setup.
- Controls are reachable by visible standard paths; input is not stolen by background updates.
- Resize, long labels, CJK/Unicode and plain mode preserve usable content.

**Verification:** Real terminal main/back/cancel/recovery tests; screen-reader/plain-mode checks and owner walkthrough.

### P12 — Implement first production adapter

**Owner role:** adapter-codex · **Phase:** 1 · **Size:** L · **Status:** proposed

**Prerequisites:** P02, P07, P09. **Requirements:** R03, R09, R11, R14.

Turn the proven Codex route into a bounded production adapter.

**Exclusive paths while assigned:** `internal/adapters/codex/**`, `testdata/adapters/codex/**`

**Deliverables:** Discovery, capability map, settings mapping, lifecycle, diagnostics and fixture suite.

**Acceptance:**

- All claimed capabilities match admitted versions.
- Approval IDs, interrupts and unknown completion map truthfully.
- Actual identity and effective model/effort are recorded without secrets.
- Minimal pinned AccountRef and credential-context binding are present before Phase 2 enrollment features

**Verification:** Adapter conformance suite, supported-version replay and authorized live synthetic task.

### P13 — Implement second production adapter

**Owner role:** adapter-second · **Phase:** 1 · **Size:** L · **Status:** active

**Prerequisites:** P03, P07, P09. **Requirements:** R03, R09, R11, R14.

Prove provider-independent core behavior with the second admitted runtime.

**Exclusive paths while assigned:** `internal/adapters/second/**`, `testdata/adapters/second/**`

**Deliverables:** Second adapter plus documented integration/auth boundaries and native exceptions.

**Acceptance:**

- Common code requires no provider-name conditionals for normal execution.
- A strict primary-model pin is admitted only when native fallback is prevented; otherwise the route is ineligible. Any distinct runtime utility/subagent inference scope is declared separately.
- Unsupported resume/permission features remain clear rather than emulated unsafely.
- Minimal pinned AccountRef and credential-context binding are present before Phase 2 enrollment features

**Verification:** Same conformance cases as first adapter; provider-specific failure fixtures and live smoke.

### P14 — Integrate a complete foreground task

**Owner role:** integration · **Phase:** 1 · **Size:** L · **Status:** active

**Prerequisites:** P08, P09, P10, P11, P12, P13. **Requirements:** R01, R02, R03, R10, R11.

Ship the first useful local task loop across both runtimes.

**Exclusive paths while assigned:** `internal/app/run*`, `internal/tasks/**`, `tests/acceptance/foreground/**`, `internal/app/native*`

**Deliverables:** Start/stream/interrupt/result/inspect path; task history; G1 evidence.; Basic explicit native passthrough with reduced-capability disclosure

**Acceptance:**

- Task produces usable artifact/output and honest acceptance status.
- Interrupted work retains context and a correct resume/reconcile action.
- Both interactive and script paths use the same effective settings and state.
- Unsupported managed capabilities have a reachable basic native launch path with literal arguments
- Follow-up after completion creates a new task revision/attempt with retained earlier evidence and bounded context; New task remains fresh.
- Phase1 foreground runs use the shared workspace/native-session ownership rules before dispatch; host choice cannot change silently during an attempt.

**Verification:** Packaged end-to-end synthetic workspace plus owner real task; failures recorded per runtime/OS.

### P15 — Implement connection and credential management

**Owner role:** accounts · **Phase:** 2 · **Size:** L · **Status:** proposed

**Prerequisites:** P04, P08, P09. **Requirements:** R04, R12, R14.

Make accounts convenient without global login swaps or hidden identity changes.

**Exclusive paths while assigned:** `internal/accounts/**`, `internal/platform/secrets*`, `testdata/accounts/**`

**Deliverables:** Connection registry; credential handles; refresh coordination; revoke/reconnect lifecycle.

**Acceptance:**

- Concurrent refresh cannot overwrite newer credentials.
- Selected account identity is verified at dispatch where supported; mismatch fails.
- Headless keychain absence is explicit and never silently falls back to plaintext.

**Verification:** OS credential-store integration, fake refresh races and authorized isolated real-account test.

### P16 — Implement model and capability catalogs

**Owner role:** catalog · **Phase:** 2 · **Size:** M · **Status:** proposed

**Prerequisites:** P12, P13, P15. **Requirements:** R03, R09, R10.

Expose current model choices without hard-coded entitlement assumptions.

**Exclusive paths while assigned:** `internal/adapters/catalog*`, `internal/app/catalog*`, `testdata/catalog/**`

**Deliverables:** Cached provider-qualified catalog, aliases, capability/version metadata and invalidation.

**Acceptance:**

- Ambiguous aliases present choices and never guess.
- Catalog presence, available login and observed access are distinct.
- Provider upgrade invalidates affected cached capabilities without breaking unrelated adapters.
- Switching connection/auth scope or endpoint without changing the executable cannot reuse another route's entitlement or configuration evidence.

**Verification:** Large/stale catalog tests; alias collisions; runtime-version change and entitlement rejection.

### P17 — Implement quota observation and reservations

**Owner role:** capacity · **Phase:** 2 · **Size:** L · **Status:** proposed

**Prerequisites:** P15, P16, P08. **Requirements:** R04, R07, R11.

Coordinate local tasks against truthful capacity observations.

**Exclusive paths while assigned:** `internal/routing/quota*`, `internal/routing/reservation*`, `testdata/quota/**`

**Deliverables:** Bucket model, provenance/freshness, bounded refresh and transactional estimated reservations.

**Acceptance:**

- Each attempt has one local reservation; shared pools/multiple windows use conservative bounds when upstream attribution is coarse
- External native usage, clock changes and reset shifts trigger conservative reconciliation.
- Reservation races and crashed workers do not manufacture capacity.

**Verification:** Parallel synthetic dispatch; stale/unknown units; reset/429 and observation revision fault tests.

### P18 — Implement deterministic routing and explanation

**Owner role:** routing · **Phase:** 2 · **Size:** L · **Status:** active

**Prerequisites:** P07, P16, P17. **Requirements:** R03, R04, R06, R07, R14.

Choose eligible routes predictably with inspectable reasons.

**Exclusive paths while assigned:** `internal/routing/policy*`, `internal/routing/resolve*`, `internal/app/explain*`, `testdata/routing/**`

**Deliverables:** Eligibility, policy preferences, stable ranking, dry-run/explain and route receipts.

**Acceptance:**

- Explicit model/account pins and data boundaries survive all fallbacks.
- No paid overage, new account or broader permission is selected implicitly.
- Concurrent capacity changes re-rank safely before dispatch.

**Verification:** Known-answer policy fixtures; forbidden candidate injection; route consistency across CLI/TUI.

### P19 — Integrate model/account choices into everyday work

**Owner role:** experience · **Phase:** 2 · **Size:** M · **Status:** active

**Prerequisites:** P14, P15, P16, P18. **Requirements:** R01, R03, R04, R07.

Make automatic and manual choices easy to understand and recover.

**Exclusive paths while assigned:** `internal/tui/settings*`, `internal/cli/accounts*`, `internal/cli/models*`, `tests/acceptance/choices/**`

**Deliverables:** Pickers, account capacity/status, effective route summary and next-task switching.

**Acceptance:**

- Switching default does not mutate an active attempt's account.
- Expired login/stale quota preserves prompt and offers contextual recovery.
- Normal task path does not require visiting an account dashboard.

**Verification:** Owner choice/recovery scenarios; no-input equivalent; long/similar labels and missing telemetry.

### P20 — Implement optional supervisor and reconnect

**Owner role:** supervisor · **Phase:** 3 · **Size:** L · **Status:** proposed

**Prerequisites:** P14, P15. **Requirements:** R05, R08, R10, R11.

Keep accepted managed work alive independently of its UI.

**Exclusive paths while assigned:** `internal/supervisor/**`, `internal/platform/ipc*`, `protocol/supervisor/**`

**Deliverables:** Authenticated local IPC, worker ownership, attach/detach, lease fencing and idle shutdown.

**Acceptance:**

- Detach acknowledges durable ownership transfer before client exits.
- Crash/restart reconciles native effects before replay.
- Unrelated users/processes cannot issue commands through local IPC.
- Detach removes a client from already-supervised work; continuing foreground work in managed mode requires a supported checkpoint and a new attempt rather than live process adoption.
- Reusing a live native process across attempts retains its host; changing host requires shutdown/relaunch or separately verified reconnection.

**Verification:** Kill UI/supervisor, PID reuse and stale lease scenarios; three-OS IPC/lifecycle checks.

### P21 — Implement workflow graph and scheduling

**Owner role:** workflows · **Phase:** 3 · **Size:** L · **Status:** active

**Prerequisites:** P18, P20. **Requirements:** R05, R06, R11, R14.

Run editable dependent tasks with clear ownership and bounded concurrency.

**Exclusive paths while assigned:** `internal/workflows/**`, `protocol/workflows/**`, `testdata/workflows/**`

**Deliverables:** Validated graph, task states, approval/input gates, resource/write leases and templates.

**Acceptance:**

- Cycles/missing inputs/unsupported nodes fail before dispatch.
- Completed steps remain complete when another step fails.
- Parallel edits use separate workspaces or verified exclusive write domains.
- Workflow scheduling reuses the Phase1 workspace/session lease and command arbitration services rather than creating a competing ownership system.

**Verification:** Implement-check-review fixture; cycle/write collision injection; restart and branch revision checks.

### P22 — Implement checkpoints and cross-agent handoff

**Owner role:** context · **Phase:** 3 · **Size:** M · **Status:** proposed

**Prerequisites:** P08, P13, P21. **Requirements:** R05, R09, R11, R14.

Transfer useful context and evidence without pretending native state portability.

**Exclusive paths while assigned:** `internal/artifacts/handoff*`, `internal/tasks/checkpoint*`, `protocol/handoffs/**`, `testdata/handoffs/**`

**Deliverables:** Versioned manifest, artifact hashes, workspace revision, constraints and remaining checks.

**Acceptance:**

- Resume and new-runtime handoff are labeled distinctly.
- Missing/stale/tampered artifacts cannot be accepted as verified input.
- Context respects destination provider data policy and size budget.

**Verification:** Two-adapter task continuation; hash/revision mismatch and secret-filter tests.

### P23 — Integrate workflow steering and recovery

**Owner role:** experience · **Phase:** 3 · **Size:** L · **Status:** proposed

**Prerequisites:** P21, P22, P19. **Requirements:** R01, R05, R11.

Expose fluid task management with precise pause, cancel, retry and resume semantics.

**Exclusive paths while assigned:** `internal/tui/workflow*`, `internal/cli/workflow*`, `tests/acceptance/workflows/**`

**Deliverables:** Task graph/list, queued steering, approval input, acceptance evidence and recovery actions.

**Acceptance:**

- Steering is applied at a declared safe checkpoint and acknowledges scope.
- Unknown external effect never receives a misleading automatic Retry action.
- Attached completion and detached acceptance have distinct receipts.

**Verification:** Running main/interrupted/recovery flows; queued edits, cancel races and repeated input.

### P24 — Evaluate bounded decision-agent routing

**Owner role:** evaluation · **Phase:** 3 · **Size:** L · **Status:** proposed

**Prerequisites:** P18, P23. **Requirements:** R06, R07, R10, R14.

Add model-assisted classification only when it improves real outcomes.

**Exclusive paths while assigned:** `internal/routing/decision*`, `eval/routing/**`, `evidence/eval/routing/**`

**Deliverables:** Schema-bound classifier, overhead budget, deterministic fallback and comparative eval.

**Acceptance:**

- Classifier cannot execute tools or widen candidate/permission scope.
- All routing/retry/context costs count in evaluation.
- Default enablement requires quality retention and demonstrated objective improvement.

**Verification:** Versioned task set; fixed/deterministic/assisted comparisons; malformed/slow/denied classifier cases.

### P25 — Publish internal-quality adapter extension kit

**Owner role:** extensions · **Phase:** 4 · **Size:** L · **Status:** proposed

**Prerequisites:** P12, P13, P18. **Requirements:** R09, R14.

Let independent developers add an adapter without generic core edits.

**Exclusive paths while assigned:** `internal/adapterhost/**`, `protocol/adapters/**`, `sdk/adapter-examples/**`, `tests/acceptance/extensions/**`

**Deliverables:** Versioned external protocol, bounded host, example adapter and conformance suite.

**Acceptance:**

- Independent example implements useful lower capability tier without router changes.
- Major mismatch, malicious frames and untrusted project auto-load are rejected.
- Crash containment is not described as a security sandbox.
- An adapter for a service-based agent runtime can declare explicit no-auth mode; a raw inference endpoint is not advertised as an agent/tool-loop implementation.

**Verification:** External adapter roundtrip; version skew/parser bounds; restart/backpressure and provenance checks.

### P26 — Admit additional providers by capability tier

**Owner role:** adapter-expansion · **Phase:** 4 · **Size:** L · **Status:** proposed

**Prerequisites:** P25, P17. **Requirements:** R03, R04, R07, R09.

Expand one requested provider at a time using the same admission route.

**Exclusive paths while assigned:** `internal/adapters/additional/**`, `testdata/adapters/additional/**`, `docs/adapters/**`

**Deliverables:** Provider-specific source record, adapter, support matrix and fixtures.

**Acceptance:**

- Every advertised capability has version/platform evidence.
- Native-only, managed and partial support remain distinguishable.
- Quota/account unknowns do not prevent honest lower-tier launch support.

**Verification:** Per-provider admission suite and bounded real check; no aggregate all-provider pass from mocks.

### P27 — Harden native escape and add optional cly import preview

**Owner role:** compatibility · **Phase:** 4 · **Size:** M · **Status:** proposed

**Prerequisites:** P09, P15, P19. **Requirements:** R01, R13, R15.

Preserve access to native features and optional familiar settings without takeover.

**Exclusive paths while assigned:** `internal/app/native*`, `internal/cli/native*`, `internal/importers/cly/**`, `testdata/import/**`

**Deliverables:** Native passthrough; explicit capability limits; read-only non-secret import diff.

**Acceptance:**

- Native exit/output contracts remain native and are clearly labeled.
- Import never writes cly config, credentials or native histories.
- User can select/reject proposed settings before UCLI commits an import.

**Verification:** Literal passthrough arguments across shells; snapshot hashes show cly/native inputs unchanged.

### P28 — Add GitHub identity and settings metadata sync

**Owner role:** identity · **Phase:** 4 · **Size:** L · **Status:** proposed

**Prerequisites:** P15, P21. **Requirements:** R12, R14.

Associate devices/settings/connections while preserving local autonomy.

**Exclusive paths while assigned:** `internal/sync/**`, `services/identity/**`, `protocol/sync/**`, `tests/acceptance/sync/**`

**Deliverables:** GitHub flow, device revocation, versioned metadata sync and conflict handling.

**Acceptance:**

- Local runs continue while identity/sync service is down.
- Provider secrets and private work content are excluded by default.
- Concurrent edits, revoked device and offline reconnect have explicit resolution.

**Verification:** Fake identity/server contract tests then registered-app staging flow; token redaction and outage scenarios.

### P29 — Harden packaging, updates and diagnostics

**Owner role:** release · **Phase:** 4 · **Size:** L · **Status:** active

**Prerequisites:** P14, P19, P23, P25. **Requirements:** R08, R10, R11, R13.

Ship reproducibly with safe state compatibility and usable troubleshooting.

**Exclusive paths while assigned:** `packaging/**`, `internal/diagnostics/**`, `internal/update/**`, `docs/release/**`

**Deliverables:** Signed/checksummed packages where supported, update/rollback, redacted doctor bundle.

**Acceptance:**

- Install/update never overwrites cly or unrelated launchers.
- Interrupted update and incompatible schema preserve recoverability.
- Doctor works on broken provider/store paths and redacts secrets.

**Verification:** Clean-machine install/update/remove; rollback/schema matrix; diagnostic fixture with planted secrets.

### P30 — Maintain performance regression evidence

**Owner role:** performance · **Phase:** 1 · **Size:** M · **Status:** active

**Prerequisites:** P06, P09. **Requirements:** R10.

Keep perceived responsiveness and local overhead visible through development.

**Exclusive paths while assigned:** `bench/**`, `tests/performance/**`, `evidence/performance/**`

**Deliverables:** Native-relative harness, cold/warm distributions, stream/concurrency and idle budgets.

**Acceptance:**

- Fixture harness catches deliberate overhead and cleanup regressions.
- Measurements record environment/build/provider and sample counts.
- Provider wait and UCLI overhead remain separately attributed.

**Verification:** Run performance.md matrix at G1-G4; retain raw samples and scoped skipped targets.

### P31 — Maintain failure and privacy boundary coverage

**Owner role:** verification · **Phase:** 1 · **Size:** L · **Status:** active

**Prerequisites:** P08, P09, P12. **Requirements:** R04, R07, R11, R14.

Exercise cross-boundary failures before release rather than after data loss.

**Exclusive paths while assigned:** `tests/failure/**`, `testdata/failure/**`, `evidence/failure/**`

**Deliverables:** Fault catalog, harness and recovery receipts covering state/process/accounts.

**Acceptance:**

- Disk/network/provider/refresh/stream failures preserve honest state.
- No uncertain external action is blindly replayed.
- Injected tokens never appear in fixtures exported to diagnostics or sync.

**Verification:** Repeat applicable verification.md failure matrix at each milestone; negative checker controls.

### P32 — Run recurring usability and accessibility checks

**Owner role:** usability · **Phase:** 1 · **Size:** M · **Status:** proposed

**Prerequisites:** P11, P14. **Requirements:** R01, R02, R10, R11.

Use real user behavior to guide comfort and discovery.

**Exclusive paths while assigned:** `tests/usability/**`, `evidence/usability/**`, `docs/usability-findings.md`

**Deliverables:** Natural-goal scripts, baseline comparison, observations and prioritized fixes.

**Acceptance:**

- First use, repeat work and recovery are attempted without teaching hidden shortcuts.
- Assisted and independent completion are reported separately.
- Real terminals cover narrow/large text/plain/screen-reader input paths.

**Verification:** Owner sessions at G1-G4; task completion/time/help lookups/recovery effort; no universal claim from small sample.

### P33 — Maintain onboarding and capability documentation

**Owner role:** documentation · **Phase:** 1 · **Size:** M · **Status:** active

**Prerequisites:** P14. **Requirements:** R01, R02, R09, R15.

Keep help and documentation aligned with actual supported behavior.

**Exclusive paths while assigned:** `docs/user/**`, `docs/contributing/**`, `docs/adapter-guide.md`

**Deliverables:** Task-oriented quickstart, capability matrix, contributor guide and diagnostic recipes.

**Acceptance:**

- Examples match parser and actual released capabilities.
- First task does not require reading an architecture manual.
- Every unsupported/recovery path points to a useful action.

**Verification:** Executable examples with fixtures; help registry drift check; newcomer route review.

### P34 — Integrate and sign off milestone evidence

**Owner role:** coordinator · **Phase:** 4 · **Size:** M · **Status:** proposed

**Prerequisites:** P29, P30, P31, P32, P33. **Requirements:** R01, R08, R10, R11, R13.

Make readiness explicit per milestone, provider and platform.

**Exclusive paths while assigned:** `docs/release-readiness.md`, `evidence/releases/**`, `planning/backlog.json`

**Deliverables:** Requirement/evidence map, release notes, residual risk list and rollback instructions.

**Acceptance:**

- No mocked/unchecked provider or OS path is labeled live-verified.
- Comfort, latency and recovery gates accompany feature tests.
- Optional cloud/decision-agent work is not silently required for local release.

**Verification:** Run consuming paths on integrated packaged build; review failed/skipped evidence and cly noninterference.

## Requirement coverage

| Requirement | Work packages |
|---|---|
| R01 | P00, P05, P10, P11, P14, P19, P23, P27, P32, P33, P34 |
| R02 | P00, P05, P07, P10, P11, P14, P32, P33 |
| R03 | P02, P03, P07, P12, P13, P14, P16, P18, P19, P26 |
| R04 | P04, P15, P17, P18, P19, P26, P31 |
| R05 | P08, P20, P21, P22, P23 |
| R06 | P18, P21, P24 |
| R07 | P04, P17, P18, P19, P24, P26, P31 |
| R08 | P01, P06, P09, P20, P29, P34 |
| R09 | P00, P02, P03, P07, P12, P13, P16, P22, P25, P26, P33 |
| R10 | P01, P09, P11, P14, P16, P20, P24, P29, P30, P32, P34 |
| R11 | P01, P02, P03, P05, P08, P09, P12, P13, P14, P17, P20, P21, P22, P23, P29, P31, P32, P34 |
| R12 | P15, P28 |
| R13 | P00, P06, P27, P29, P34 |
| R14 | P02, P03, P04, P07, P08, P12, P13, P15, P18, P21, P22, P24, P25, P28, P31 |
| R15 | P05, P10, P27, P33 |
