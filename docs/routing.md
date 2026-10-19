# Model, effort, account and capacity routing

Status: proposed behavior; no router is implemented or evaluated.

## Goals and authority

Choose an eligible execution route that can complete the task with appropriate
quality and acceptable latency/cost. Preserve explicit user choices, project data
boundaries, permission policy and active work. The router selects within authority;
it cannot grant itself tools, accounts, paid overages, new providers or broader
access. Task text and agent output are inputs, not policy instructions.

Treat model, effort and account auto-selection independently. Pinning a model keeps
that model; `--effort auto` chooses only a supported effort for it. An account pool
is an explicit eligible set, not every credential discovered on the machine.
Default work/personal boundary changes require an intentional configuration choice.

## Selection pipeline

1. Resolve effective configuration and explicit pins. Snapshot policy revision.
2. Obtain cached model/capability catalog with provenance and freshness.
3. Filter by runtime availability, entitlement evidence, required tools/modalities,
   context bound, workspace access, permissions, account policy and spend ceiling.
4. Estimate task demands using explicit metadata and inexpensive local rules.
5. Read quota observations; account for local reservations once. When provider
   snapshots cannot attribute usage to attempts, use conservative bounds rather
   than claiming exact deduction of already reflected usage.
6. Rank candidates using a transparent policy; if ambiguous and permitted, request
   a bounded decision-model classification. Revalidate its structured recommendation.
7. Atomically reserve the selected account/buckets and create an attempt lease.
   If another task wins capacity, re-rank rather than overcommit silently.
8. Show the effective model/runtime/account/effort and a short explanation, then
   dispatch. Record exact native identifiers and policy/catalog revisions.
9. Reconcile actual outcome/usage, release reservations, and update evidence. Use
   fresh provider observations after meaningful capacity changes.

Refresh and decision requests are bounded and cancellable. Cached local commands
never wait for the network. A stale observation remains visibly stale during refresh.

## Start with deterministic policies

Default to balanced. Expose a small set of understandable preferences: balanced, faster, conserve usage,
and highest available quality. These are preferences within constraints, not opaque
provider bundles. The owner can inspect and edit the concrete rules. Prefer a stable
known route for routine tasks instead of exploring a different model every turn.

A first ranking can use lexicographic priorities: satisfy required capabilities and
minimum quality class, respect reserves/budget, preserve session continuity, then
choose the lowest predicted completion time or marginal cost for the selected
preference. Record the rank reasons. Avoid arbitrary hidden numeric weights until
evaluation supplies a reason for them.

Task features may include requested role, scope, code/diff size, context estimate,
language/tool requirements, tests available, uncertainty, deadline and consequence
of an error. A task's text can exaggerate its own importance; local policy remains
authoritative. Do not infer business data permission from model confidence.

## Decision agent

Use an optional, small classifier at task creation or a meaningful checkpoint.
Give it a compact task description, available capability summaries, coarse capacity
state and permitted preferences. Do not expose secrets or unrelated histories.
The decision route itself must be eligible for that project's data.

Output a schema-bound recommendation: task class, minimum capabilities, candidate
class, effort suggestion, confidence, short rationale and escalation trigger. It
does not execute tools or modify policy. Reject unknown model IDs, unsupported effort,
out-of-scope accounts and invalid output. Fall back to deterministic routing on
timeout, quota failure, malformed output or low confidence.

Initial proposed ceiling: one classification per new task, one reassessment per
meaningful failed check, and at most two model/effort escalations per task unless
the user changes the budget. Cache only by relevant task/context/policy/catalog
revision; a changed data boundary invalidates the recommendation. No idle model
polling. The classifier's own tokens, latency and failed calls count in totals.

## Quota model

Track an observation per provider quota scope and bucket, not per model label.
Fields include scope identity, account reference, applicable routes/models, window
kind, unit, used/remaining/limit when known, reset timestamp when known, observed
timestamp, source, confidence, refresh method and response revision. Values can be
unknown. Percentages from unrelated buckets cannot be added or treated as tokens.

An account may have weekly and short-window limits simultaneously; no applicable
bucket may be known exhausted. Unknown telemetry follows the bounded policy below.
Several models may share one bucket, and separate
accounts may share an organization/project limit. Catalog aliases never create new
capacity. API requests/minute, tokens/minute, subscriptions and money are distinct.

Use wall-clock UTC for display/reset contracts and monotonic time for local retry
delays and leases. Detect sleep/resume and clock shifts; refresh before assuming a
window has reset. A reset timestamp passing is an expected opportunity, not proof
that capacity was granted. Provider updates may move resets or change plans.

Entitlement evidence has separate states: advertised, verified-by-successful-turn,
denied, and unknown. A catalog entry is not a proof of access. For first use, an
authenticated, correctly bound account may make one normal bounded task attempt
on an advertised route under the existing budget; do not require an extra paid
probe. Known denial excludes the route. An unknown route absent from the account's
catalog requires an explicit model selection or connection configuration rather
than speculative auto-routing. Record the resulting evidence without pretending
access was verified beforehand.

Default unknown-capacity policy: allow one in-flight attempt per affected account
pool when it is otherwise authorized and no bucket is known exhausted, preserve
task/time/spend limits, label capacity unknown and refresh on relevant events.
Do not pause everyday work solely because a provider has no percentage API. A user
can choose a stricter observed-headroom policy; shared organizational constraints
still apply.

Reservations are estimated local claims, not provider guarantees. Track reservation
ID, attempt, applicable buckets, estimated units/range, lease expiry, heartbeat,
and reconciliation state. The local ledger must never reserve the same attempt
twice. Use source revisions and attribution when supplied; coarse upstream percent
snapshots may not reveal which attempt is included, so use conservative ranges
instead of claiming exact reconciliation. External/native usage can
invalidate estimates; use conservative headroom and refresh after 429 or mismatch.
Cross-device sync is advisory initially; exact global quota reservations are a later
server coordination feature, not a promise of the local scheduler. A crash or
cancel request does not release uncertain in-flight capacity until work is confirmed
unsent/stopped/completed or fresh observations support a conservative reconciliation.

## Account behavior

An attempt stays on its account. Concurrent account refresh is serialized per
credential owner; revocation disables new starts and transitions affected attempts
according to adapter semantics. Do not cycle accounts after every token/request.
Selection at a task boundary preserves caches, provenance and permission context.

Prefer an eligible account with sufficient headroom and continuity. A near reset can
make a currently usable pool attractive; it never justifies burning quota on low
value work. Maintain user-set reserves for expensive work. If all eligible accounts
are unavailable, show wait-until, choose another authorized route, or stop. Distinguish
capacity exhaustion from invalid credentials and forbidden access; the latter are
not generic signals to rotate identities. Respect provider and organization account
rules; account pools are not a bypass of access controls.

## Failure decisions

| Observation | Safe default | Recovery |
|---|---|---|
| 429 with retry/reset hints | Stop dispatch to affected bucket, refresh quota, bounded jittered backoff | Wait, choose eligible route at checkpoint, or surface capacity state |
| 401 | One supported refresh under credential lock | Reconnect account; preserve task draft |
| 403/access denied | Mark route ineligible with source reason | User chooses an authorized route; do not blind-rotate |
| Network failure before confirmed request acceptance | Record acceptance uncertainty | Query status if supported; retry only under replay contract |
| Timeout after tool execution may have started | Outcome unknown, keep evidence | Reconcile artifacts/native run before another attempt |
| Provider unavailable | Circuit-breaker cooldown scoped to route/provider | Eligible alternative at a safe boundary |
| Check failure | Preserve diff/results and classify failure | Repair task or bounded escalation; no hidden infinite loop |
| Decision-agent failure | Deterministic selection | Explain fallback only when relevant |
| Unknown quota | Apply explicit unknown-capacity policy | Limited concurrency with honest status, or require observed headroom |

Retry policy includes per-attempt and task-level ceilings, elapsed deadline and
remaining spend. Retry-After overrides optimistic client timing where valid. Cancel
propagates through routing, refresh and backoff; it does not wait for a retry timer.

## Effort and handoff

Effort levels are semantic preferences with adapter mappings and explicit effective
values. Do not silently emulate unsupported reasoning effort with a longer prompt.
Expose supported native levels as details. A fixed request fails if unsupported;
automatic selection can choose a compatible route and record the mapping.

Keep routing stable within a provider turn. At a checkpoint, preserve the task goal,
constraints, verified outputs, artifact hashes, workspace revision and remaining
checks. A cross-runtime handoff starts a new attempt with explicit context; it is
not a native resume. Budget context transfer and repeated cache warming.

## Evaluation and release gate

Use a versioned set of realistic small fixes, larger refactors, research, writing,
review and long-context tasks with measurable outcomes. Compare fixed user-selected
model, deterministic routing, and decision-assisted routing on the same cases.
Use multiple trials where stochastic variance matters and report sample sizes.

Measure task success, check pass rate, human corrections, wall time, first useful
output, all tokens/requests including routing, money where known, quota consumption
where observed, escalations, context transfer and incorrect eligibility decisions.
Quality cannot be reduced to the routing model's self-score.

Decision assistance ships only if it preserves acceptance quality and improves a
stated latency/cost/capacity objective compared with the deterministic baseline.
If evidence is inconclusive, keep it opt-in and retain deterministic defaults.
No benchmark or successful live routing run has been executed for this plan.
