# Architecture readiness review — October 18, 2026

Verdict: ready for the existing bounded feasibility spikes. Production foundations
remain behind G0. The review found missing semantics at shared boundaries, not a
reason to add another architecture phase. The decisions below are now the design
baseline for those spikes; provider/platform claims still require execution proof.

The local core, application/domain/adapter/platform separation, SQLite plus artifact
files, explicit account/permission scope, conservative quota handling and future
language-neutral adapter protocol remain suitable. No language rewrite, new service,
workflow framework or universal tool runtime is justified by this review.

## Expensive-to-change boundaries now specified

| Boundary | Minimum decision recorded | Existing work that proves it |
|---|---|---|
| Everyday continuity | Task is the visible thread; follow-up creates input/revision and a new attempt; old evidence stays attached to its original revision. Native sessions have exclusive active-turn ownership. | P00, P02/P03, P05, P07/P08, P14 |
| Workspace identity and concurrent work | Project and physical workspace are separate. Explicit binding governs trust/settings; aliases share a workspace, distinct worktrees do not. Phase1 reader/writer leases apply across UCLI processes. | P01, P07/P08, P14; P21 reuses these services |
| Native context and authority | Runtime owns its skills/tools/hooks. Record context sources and avoid duplicate injection. Common filesystem envelopes have explicit meanings; shell/network/external effects remain separately bounded. | P00, P02/P03, P05/P07 |
| Execution and durable changes | Choose foreground/supervisor ownership at launch. Mutating commands have IDs, revision guards and receipts. Migrations exclude active incompatible writers; no hot migration. | P01, P07/P08, P14/P20 |
| Future service/runtime routes | Task adapters own a complete agent loop. Executable and service identities are supported; authentication may explicitly be absent. Direct inference remains behind a runtime. | P07, P25; implementation of a UCLI-owned loop deferred |
| Route evidence | Static capabilities and account/model/endpoint/config-specific observations have distinct cache scopes; identity or scope changes invalidate the latter. | P02/P03, P16 |

Authoritative semantics are in [contracts](contracts.md), [architecture](architecture.md),
[experience](experience.md) and [workflows](workflows.md). The backlog acceptance
criteria were tightened in place. All 35 package IDs, phases and dependency edges
remain unchanged; no extra implementation workstream was created.

## Scenario check before the v0 freeze

P00 supplies concise request/state/result examples; the named spikes exercise the
real boundary. A scenario is not passed merely because its prose is consistent.

| Scenario | Required distinction |
|---|---|
| Complete a task, then ask “also handle empty responses” | Same visible task; fresh revision/attempt/checks; previous result remains inspectable |
| Switch runtime for that follow-up | Bounded explicit context and required tools carry forward; no implicit native memory portability |
| Two terminals continue the same native session or edit one checkout | Native turn/workspace conflicts serialize; duplicate commands cannot duplicate effects |
| Open via a subdirectory/alias, then a different worktree | Correct workspace identity and deliberately scoped trust/defaults; no URL/name-based merging |
| Untrusted repo contains a native hook/helper/endpoint override | No startup execution or credential redirection before authorization; actual native loading behavior is checked |
| Read-only task launches a shell; runtime refreshes credentials | Task effects remain constrained; isolated operational state is separately declared |
| Lose terminal, request detach, then update with an old worker active | Owner/lifetime is truthful; no live reparenting assumption or incompatible concurrent DB writes |
| Change account or endpoint with the same executable | Old entitlement/config evidence is ineligible; no-auth service identity remains explicit |

## What still needs evidence, and what can wait

The unanswered questions are already assigned: packaged Go/process/terminal/DB
behavior on the target OSes (P01), supported lifecycle/trust/permission semantics
of two real adapters (P02/P03), account isolation and quota observations (P04), and
the owner's first-use/follow-up/recovery experience (P05). G0 can accept an explicit
capability limitation; it cannot relabel an unexecuted test as a pass. Unavailable
OS/provider checks stay visible and constrain the advertised support scope.

Defer exact public plugin schemas until two real adapters establish the common
shape; defer a shared skill installer/tool loop, cloud execution, credential sync,
distributed leases, live process adoption and hot DB migration. GitHub registration
and sync conflict details belong before P28, not before the first local task.
No further speculative design is needed to start the listed spikes.

## Keep future development context small

- Give each worker one package, the accepted contract revision, exact relevant
  sections, owned paths, fixtures and a failing acceptance scenario. Do not paste
  the complete plan into every assignment.
- Keep canonical behavior in the existing domain documents and task assignments
  in backlog.json. Generate work-packages.md; the presentation is an orientation
  companion, not another implementation specification.
- Require a proposed boundary change to name affected consumers, persisted/wire
  compatibility, acceptance changes and migration/recovery consequences. Integrate
  the contract and consumers together under the coordinator.
- End a spike with a bounded decision and retained evidence. If the boundary holds,
  proceed; if it fails, narrow the capability or revise that boundary before more
  implementation depends on it. Avoid rebuilding every language/provider to answer
  a question that one targeted experiment resolves.

This is a design audit with three independent specialist reviews, followed by
cross-document and static plan validation. No UCLI runtime, native trust fixture,
concurrency test, benchmark or human usability test ran during this audit.
