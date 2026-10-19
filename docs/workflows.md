# UCLI workflow and task contract

Status: proposed development specification, 2026-10-17. Examples illustrate
future behavior. The workflow system extends the same task experience described
in [experience.md](experience.md), rather than introducing another agent language.

## The useful unit of work

A **task** states a goal, authorized scope, inputs, outputs, and completion
condition. An **attempt** records one execution of that task with a resolved
runtime, model, effort, account, permissions, and budget. A **workflow** connects
tasks by dependencies and artifact references. A **role** supplies a reusable
instruction and capability preference, such as implementer or reviewer; it does
not require a permanently running agent or a fixed provider.

Keep the first implementation small: persisted acyclic task graphs, explicit
inputs and outputs, bounded retries, validation gates, and resumable execution.
Branches can be ordinary conditional eligibility and explicit graph revisions.
Unbounded self-modifying loops and a separate workflow programming language are
unnecessary for an initial product. Allow future graph extensions through a
versioned schema, with unknown execution semantics rejected clearly.

A user may type a goal, choose a template, or load a workflow file. Natural
language planning produces the same inspectable structure, with a short plan
summary and editable tasks. The plan is a proposal until accepted for execution;
routine single tasks do not need this extra step. A user's direct instruction
to run an established trusted workflow can constitute acceptance within its
existing scope. Changed permissions or external publication remain separate
authorization boundaries.

## End-to-end example

```text
$ ucli workflow run feature --input "Add offline forecast caching"

Feature plan  4 tasks  Workspace C:\work\weather-app
1  Inspect cache boundary       auto       reads source
2  Implement offline fallback   auto       edits isolated worktree
3  Run cache and offline checks local      depends on implementation
4  Review behavior and diff     auto       reads pinned candidate

Completion  checks pass and review findings are resolved
Actions     Run | Edit plan | Settings | Back
```

The plan preview shows work, dependencies, permission scope, concurrency,
validation, and estimated usage uncertainty. It should not make the user study
internal scheduler metadata. A developer can inspect/export the full versioned
plan. Edits through the UI and files produce the same validation errors.

After starting, show a compact task list with dependency and blocked reasons.
Selecting a task reveals its latest useful output, effective route, evidence,
and available actions. The next useful step remains visible when a task fails;
users should not inspect logs to discover that authentication expired.

`ucli workflow run` accepts a durable run and remains attached by default; its
final exit status reflects the workflow outcome. Explicit `--detach` returns
after persistence and supervisor acceptance: exit `0` then means accepted, not
completed. The receipt supplies the workflow/task IDs and the exact inspect and
attach commands. If no supervisor can own continued execution, refuse detach
before starting. Scripts can later inspect the stored terminal outcome without
relying on the original shell process.

Canonical recovery routes are `ucli workflow inspect ID` and
`ucli workflow attach ID` for workflow IDs. Task rows expose task IDs for
`ucli tasks show ID`, `ucli tasks attach ID`, `ucli tasks resume ID`,
`ucli tasks retry ID`, and `ucli tasks cancel ID`. Inspection lists only actions
supported by the task's current state and adapter. The same names appear in
interactive controls and help.

```text
Workflow 8  Offline forecast caching                 needs attention
  31 Inspect cache boundary           complete
  32 Implement offline fallback       complete    candidate 81ac...
  33 Run cache and offline checks     failed      1 offline test
  34 Review behavior and diff         waiting     needs checks

Actions  Open failed check | Continue fix | Edit plan | Cancel
```

“Continue fix” creates a task/attempt with the failed evidence and candidate as
inputs, within the authorized scope. It does not erase the failed check or mark
the workflow complete. A bounded repair loop may automate that transition after
the user has selected a workflow that explicitly includes it.

## Minimum persisted records

| Record | Required content |
| --- | --- |
| Task | stable ID, goal, scope, input artifact refs, output contract, completion conditions |
| Task input/revision | accepted follow-up/steering input, command ID, base/current revision, delivery receipt |
| Attempt | attempt ID, resolved route, start/end, status, failure class, parent checkpoint |
| Dependency | required producer output/version, gate type, current satisfaction |
| Artifact | identifier, kind, location, digest/version, creator attempt, retention policy |
| Evidence | command/check, environment, result, exit code, input revision, log reference |
| Checkpoint | adapter capability, replayable context, completed effects, unresolved effects |
| Decision | selection reason, quota freshness, policy version, override/source |
| Event | sequence, task/attempt IDs, timestamp, type, payload schema version |
| Authorization | allowed operations, workspace/account scope, limits, expiry where applicable |

User-facing state is `planned`, `queued`, `running`, `waiting`, `paused`,
`complete`, `failed`, `canceled`, or `needs attention`. Machine-readable reasons
distinguish dependencies, quota, credentials, approval, contention, and unknown
external outcomes. A task is complete only after its required outputs exist and
its completion conditions have evidence. An agent's “done” message is output,
not validation evidence.

Emit accepted events only after the persistence transaction commits. Each
attempt has one owning executor lease; an expired lease requires reconciliation
before another executor may continue. The lease fences stale UCLI executors,
but cannot magically cancel an already sent provider or external operation.
Duplicate dispatch and late events must not overwrite a newer attempt's state.

## Precise continuation semantics

| Operation | Meaning and required guard |
| --- | --- |
| Attach | observe/control an existing task; never create execution |
| Follow up | new accepted input/revision in the open task; new attempt and completion checks; retain prior evidence |
| New task | fresh task identity and context unless explicit inputs/handoff are selected |
| Detach | remove the UI only after existing supervisor ownership is acknowledged; foreground attempts do not transfer live |
| Resume | continue paused/interrupted work from a proven checkpoint; preserve task identity |
| Retry | create a new attempt for a failed step using pinned inputs; previous evidence remains |
| Rerun | intentionally execute completed work again as a new task/run; repeat effects are explicit |
| Restart | begin a new attempt from initial inputs; disclose lost execution context |
| Handoff | create a continuation with exported context and recorded limitations |
| Pause | prevent new dispatch and stop at the next supported safe boundary |
| Cancel | request stop and prevent queued dispatch for the captured revision; preserve effects; a deliberate later follow-up is new work after reconciliation |

Use Resume only when the adapter can resume the native session or a sufficient
checkpoint exists. Otherwise offer “Continue from handoff” and explain what
context survives. A terminal attachment ending does not constitute a successful
checkpoint. An unsupported pause is shown as unavailable with Cancel/Detach
alternatives; do not relabel process suspension as durable pause.

Retries need a classified cause. A failed read or a request proven not accepted
may be retried with a bounded backoff. A provider timeout after dispatch is an
unknown outcome until a run ID/status query, idempotency key, or artifact check
reconciles it. Never automatically replay publication, purchases, database
mutations, emails, merges, or other external effects merely because the tool
returned no result. The router does not decide that repetition is harmless.

Record an effect intent before dispatch where UCLI controls the boundary, then
record the provider request ID and receipt. If the provider/runtime owns tools
internally, advertise that observability limit and disallow automatic retry of
an indeterminate attempt. Exactly-once external execution is not promised.
Cancellation may leave changes behind; Undo requires a separately implemented
compensation with evidence, and is not another name for Cancel.

## Editing and routing running work

Plans have revisions. Additions or changed goals apply to pending tasks. A
changed input invalidates affected downstream validation; completed evidence
continues to refer to its original artifact revision. Changes to running work
become queued steering or checkpoint transitions. Display what applies now,
what applies next, and why a task must be recomputed. Never silently replace
input beneath an active reviewer.

An unsaved plan edit or unsent steering message is a draft, not an accepted graph
revision or instruction. Preserve it across local navigation and submission
failure, associate it with the task and base revision, and clear it only after
durable acceptance or explicit discard. Restore retained drafts after restart
without executing them. If the underlying plan changed, show the conflict and
preserve both versions rather than applying the stale draft automatically.
Follow the draft retention/private-session policy in the experience contract.

Route selection happens at task/attempt boundaries. A model/account route change
creates a new attempt at a supported checkpoint; retaining a native session
requires separately verified adapter support. Automatic effort may vary between
tasks; it never widens authority.
Account selection considers active local reservations and supported shared
quota buckets. Unknown capacity, expired telemetry, and reset races are reasons
to reconcile or wait, not proof that another account should be hammered.

The controller has bounded planning/routing/repair budgets and a route that does
not depend recursively on itself. Task budgets include subtasks and retries.
If a hard bound is reached, preserve work and show the cheapest valid next
actions. A failed routing model should leave deterministic eligible defaults or
an understandable selection screen, not make every model unusable.

## Safe parallel work and handoffs

For repository writes, give parallel implementers separate worktrees/branches
and declared ownership scopes. Shared read-only tasks may use a pinned revision.
Overlapping write scopes serialize or require explicit coordination. Repository
ownership is a scheduler policy, not a filesystem sandbox; use independent
permission enforcement for actual access boundaries.

A single integration task merges candidates, resolves conflicts, and runs the
required checks against the integrated tree. Parallel task success alone never
establishes integrated correctness. Do not auto-commit user changes, reset the
original working tree, or clean an unfamiliar directory. Detect dirty state,
record the starting revision, and retain worktrees until output and evidence
have been reconciled and retained according to policy.

Non-Git projects use declared output directories or serialized writes. Future
remote workers use the same artifact/lease interfaces; do not assume a local
absolute path is portable. Artifact references carry resolvable locators and
content identity, with permissions checked at the consuming boundary.

A handoff includes the goal, accepted decisions, input/output revisions,
relevant files, executed checks, unresolved questions, authorized scope, and
next action. Separate user instructions from quoted repository/provider output.
Never transfer credentials, hidden reasoning, or unrelated history. The receiving
attempt records the handoff source and any lost capability/context. Handoff
summaries are aids; artifacts and receipts remain the evidence of execution.

## Implementation slices and checks

1. **Durable single task:** accept, emit events, show status, attach, cancel.
   Check process death before/after persistence, duplicate submission, cancellation
   during a tool call, and late events. No accepted task disappears.
2. **Checkpoint and recovery:** add pause where supported, retry classification,
   unknown-outcome reconciliation, and handoff continuation. Inject a timeout
   after a simulated effect commits; no automatic duplicate effect may occur.
3. **Small workflow graph:** validate graph cycles, missing artifacts, schema
   versions, and permission scope; schedule dependencies and local checks.
   Restart the supervisor between every transition and compare durable state.
4. **Parallel implementation:** isolate two worktrees, serialize overlap, integrate,
   and validate the combined result. Retain failed candidates and logs through
   cleanup. Test disk-full and abandoned-worker recovery.
5. **Adaptive routing:** record eligible alternatives, effective settings,
   quota freshness, and budget decisions; reroute only at valid boundaries.
   Inject stale quotas, account revocation, reset races, and router failure.
6. **Editable natural-language plans:** use the same graph validator and scheduler.
   Test steering while output streams, changing a pending input, invalidating
   downstream evidence, and declining an expanded permission request.

Each slice needs a small runnable invariant check, real consuming CLI tests,
and at least one live supported-provider path before being described as
implemented. Mock-provider success establishes scheduler behavior only. Use
contract suites for future adapters so the same interruption, evidence, output,
and capability semantics remain consistent as providers expand.

Human review must cover finding a failed step, understanding why a workflow is
waiting, changing the next task without disturbing active work, and resuming
after terminal loss. These checks are planned; no runtime, cross-platform,
provider, or human usability test has executed for this specification.
