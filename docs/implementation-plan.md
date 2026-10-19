# Implementation and development plan

Status: Phase 0 implementation started, 2026-10-18. The provisional contract and
executable feasibility experiments are in the repository. Production phases remain
unstarted; [the implementation receipt](verification.md)
records the evidence and open G0 checks.

## Deliver by complete user journeys

The first milestone is a comfortable local task from prompt through usable result,
with interruption and recovery. Layer additional providers and automation onto that
journey. Avoid building account pages, plugin infrastructure and a scheduler for
months before a task can run.

Use the [backlog](../planning/backlog.json) as the authoritative dependency graph.
[Work packages](../planning/work-packages.md) render its assignments for humans.
Each package names ownership, inputs, outputs, acceptance and evidence. The proposed
package paths are architectural boundaries, not existing implemented code.

## Phase 0 — retire the risky assumptions

The [readiness review](readiness-review.md) identifies the bounded scenarios to
settle before accepting v0. P00 fixes a provisional vocabulary and test baseline;
G0 freezes the shared contracts after the real adapter/platform/experience findings.

Goal: establish that the first adapters, account model and packaged core can support
the desired experience. Run bounded spikes; keep experiments separate from product
packages until a decision record accepts them.

- P00: consolidate product/command/protocol contracts and requirement traceability.
- P01: benchmark packaged core/terminal/process/SQLite alternatives on real OSes.
- P02: exercise structured Codex lifecycle with synthetic work and permissions.
- P03: exercise the second runtime's supported managed path, including auth scope.
- P04: prove or explicitly limit two-account isolation and quota observation.
- P05: walk the terminal experience with the owner using realistic task scenarios.

Gate G0: choose the core stack; admit two bounded adapter paths; record unsupported
platform/account behavior; freeze v0 contracts and initial UX names. A failed account
isolation experiment does not become an automatic fallback to shared credential swaps.
It can narrow a release's capability tier while the rest proceeds.

## Phase 1 — a useful local CLI

Goal: one task can be entered, streamed, interrupted, persisted and inspected through
the unified interface. No cloud service is required.

- P06: repository/build/CI/toolchain and packaging foundation.
- P07: typed core contracts, common resolver and structured errors.
- P08: transactional state, artifacts and recovery primitives.
- P09: platform process runner and bounded stream pipeline.
- P10: command registry, help, completion, noninteractive contracts and basic explicit native launch.
- P11: interactive shell/home, prompt, task output and discovery.
- P12: production first adapter.
- P13: production second adapter.
- P14: task execution service and real end-to-end vertical slice.

Gate G1: the owner can start a useful task without knowing native flags, inspect the
effective settings, interrupt safely, resume/reconcile appropriately and find the
result. The scripted path emits valid JSON and truthful exit status. All three OSes
pass the applicable local paths; live provider checks are labeled individually.

This phase includes a minimal pinned local AccountRef and verified runtime binding,
capability resolution and exact native settings for each admitted adapter. It does
not postpone identity correctness until the richer Phase 2 account manager. Basic
native access is available as an explicit early escape; P27 adds import and hardening.

## Phase 2 — accounts and capacity

Goal: model/account choice becomes safe and convenient, including known shortages.

- P15: account manager and per-OS credential integration.
- P16: dynamic model/capability catalog, aliases and identity checks.
- P17: quota adapters, observations, reservations and freshness.
- P18: deterministic route resolver and explain output.
- P19: account/model/effort picker integrated with the running task experience.

Gate G2: simultaneous eligible account runs cannot change each other's identity;
model/effort pins are honored; the local reservation ledger does not double-count
attempts and coarse provider telemetry uses conservative bounds; unknown/stale
quota is visible; 429/revocation/refresh failure preserves task context. No automatic
paid overage or move across a configured personal/work boundary occurs.

## Phase 3 — durable multi-agent work

Goal: a task plan can span agents and survive UI/process interruption.

- P20: optional supervisor, local authenticated IPC and reconnect.
- P21: workflow graph, editable task plans, scheduler and workspace ownership.
- P22: checkpoint/context handoff and artifact validation.
- P23: steering/retry/recovery and acceptance evidence in the UI/CLI.
- P24: bounded decision-agent routing and evaluation against deterministic baseline.

Gate G3: a plan with implement/check/review steps survives a killed UI and restart,
retains completed artifacts, and resumes the appropriate unfinished step without
repeating an uncertain external action. Parallel agents respect write ownership.
Decision assistance remains optional until evaluation justifies its default use.

## Phase 4 — extend without destabilizing the core

- P25: external adapter SDK/contract suite with capability tiers/version handling.
- P26: additional adapters selected by proven owner demand and source readiness.
- P27: native passthrough hardening and optional read-only cly import preview.
- P28: GitHub identity/device enrollment and settings metadata sync.
- P29: cross-platform release hardening, updates, diagnostics and rollback.

Gate G4: an independent contributor can add a useful adapter without editing routing
or workflow logic. Local tasks work while cloud login/sync is down. Install/update
does not touch cly and preserves UCLI state. Releases declare tested OS/runtime
versions and capability tiers rather than a universal support checkbox.

## Continuous workstreams

- P30: performance suite and native-relative regression evidence.
- P31: failure injection, security/privacy boundary tests and recovery matrix.
- P32: owner usability sessions and accessibility/terminal parity checks.
- P33: documentation, onboarding and adapter/operator diagnostics.
- P34: integration/release evidence and requirement sign-off.

These packages recur at the milestone where relevant code appears. They are not a
final cleanup phase. Their dependency edges identify initial setup; each milestone
requires fresh evidence for the changed paths.

## Safe parallelization

Start with four lanes: coordinator/contracts, runtime/performance spike,
provider/account spike, and experience prototype. The coordinator reconciles
findings before shared interfaces freeze. After G0, independent implementation
lanes can own state, platform runner, interface, and provider adapters.

Use worktrees and task branches. Assign an agent an explicit backlog package,
acceptance target and exclusive path list. Shared types, dependency manifests,
migrations and release workflows have a named owner; requests go through that
owner. Review changes against the contract before integration.

No work package is "done" based on a summary alone. Require changed files, actual
commands/exit codes, failure/skip counts, useful evidence and known limitations.
The integrator runs the consuming path after merging compatible changes. Don't
have every agent independently install runtimes or mutate shared real credentials.

## Definition of ready

An implementation package is ready when prerequisites are accepted, its contract
revision is known, ownership is exclusive, fixtures are available, required access
is established, and its failing acceptance scenario is concrete. Missing provider
access blocks live verification but can permit clearly labeled fixture work.

## Definition of done

Implementation matches the accepted behavior and real consuming path; relevant
failure/interruption cases pass; targeted tests and required OS checks run; sources
and effective settings are recorded; no unintended native/cly side effects occurred;
documentation and capability reports match actual support; evidence survives cleanup.

For user-facing features, observe the main and recovery paths in a real terminal.
For usability claims, record owner/participant behavior rather than inferring it
from code review. For performance claims, record packaged build, machine, OS,
provider version, cold/warm state, sample count and distributions.

## Effort and sequencing

Backlog sizes are relative planning estimates: S is a bounded contract/fixture change,
M a module or platform slice, and L a cross-boundary feature requiring integration.
They are not calendar commitments. Provider changes and account isolation are the
highest uncertainty. Re-estimate after G0 before setting dates or staffing.

The critical path runs through contracts → packaged/runtime evidence → process/state
foundation → a real adapter → unified task loop → account/capacity → workflows.
GitHub sync, more provider logos and a marketplace must not delay the useful local
loop. If a milestone grows, cut optional breadth and preserve recovery/usability.

## Suggested first agent assignments

1. Coordinator: P00 and integration of spike decisions; own contracts/backlog.
2. Platform agent: P01; own isolated benchmark runner and platform observations.
3. Provider agent: P02, then P04 using a verified connection; own synthetic fixtures.
4. Experience agent: P05, with P03 assigned after a slot becomes free.

Once G0 accepts the shape, schedule P06 and the ready P07/P08/P09/P10 work in
dependency order. Parallelism follows contract readiness and file ownership, not
the number of available agents.
