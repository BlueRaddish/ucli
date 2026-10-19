# Deterministic route proposal (experimental)

`Resolve(Request) (Decision, error)` selects among immutable candidate snapshots.
It does not read files, clocks, environment variables, credentials or task text,
call a provider, change an account, accept an attempt, or reserve capacity. Its
only inputs are typed evidence and policy supplied by the application. There
are no dependencies beyond the standard library and `internal/core`.

Run this only at a new-task or explicit checkpoint boundary. `ContinuityID` is a
soft preference, not authority to switch the runtime/account of an active turn.
The caller must preserve the existing route while a turn is active. Replaying a
command uses its existing receipt, not a new resolution. A 401/403 or uncertain
external outcome does not authorize the caller to reroute and retry.

## API and evidence ownership

`Request.Requested` is `core.Selection`; empty fields take core defaults. Pins
are independent, exact identifiers. Aliases must be resolved by the caller.
Every connection, including a no-auth service, must be explicitly present in
`AllowedAccounts`. This is an allowlist, not a discovery result.

A candidate describes one runtime/provider/model/connection tuple. `Admitted`
means the caller has accepted evidence for this exact runtime identity, platform,
connection/auth generation, model, native configuration and resolved workspace.
`Workspace` must match the request's ID, root and cwd exactly. The resolver does
not canonicalize paths or verify files; the application does that before building
the snapshot. Invalidate evidence when these inputs change. An experimental
local echo fixture's admission is not a real provider admission.

- `AuthMode="none"` requires no fake credential binding. Route trust/admission,
  workspace eligibility and permission enforcement still apply. Such a route may
  use `Entitlement="not-required"` if access entitlement does not apply.
- Other auth modes require both `Authenticated` and `BindingVerified`. A working
  ambient login is insufficient, even for one explicit account pin.
- Explicit model pins require `PrimaryModelFixed`; otherwise the route is
  excluded. Auto mode can use such a runtime but emits an uncertainty reason;
  the adapter must separately record actual primary/utility model use.
- Capabilities use the contract labels `native`, `mapped`, `conditional`,
  `unsupported`, `unverified`. Only the first two, or conditional evidence with
  `ConditionsMet=true`, satisfy a requirement. Admission is the caller's proof
  boundary; this package does not independently validate native guarantees.
- `Permissions["filesystem"][Requested.Permission]` must be supported. The
  caller also supplies every independent shell/network/external-mutation/approval
  constraint through `RequiredPermissions`; a filesystem envelope never implies
  those guarantees. Missing evidence fails closed. A conflicting filesystem
  requirement is rejected rather than merged or broadened.
- `QualityClass` is an ordinal on the caller's shared evaluation scale, not a
  provider's marketing model name. Zero quality/context/latency means unknown.
  Unknown quality/context cannot satisfy a positive task requirement.

`Efforts` is an ascending list of admitted semantic/native mappings, with a
declared `DefaultEffort`. `auto` uses that default; this slice does not infer task
complexity or call a decision model. `max` selects the highest entry actually
admitted for the route; it grants no extra authority. A fixed missing effort is
excluded, never downgraded or simulated by prompt text. Empty mappings support
only automatic native default effort, reported as `default` with no native value.

## Catalog, entitlement and spend

Entitlement is separate from advertised catalog membership and capacity. A known
denial excludes the route. First use needs `AllowFirstUse`, `BoundedAttempt`, an
authenticated/bound connection when applicable, and a fresh advertised entry.
An unknown, unlisted route is eligible only when the model is explicitly pinned
or the connection intentionally configured that model. First use refuses any
applicable pool already carrying in-flight work. It is one ordinary bounded task
attempt, not a paid access probe; the caller records its resulting entitlement.

Fresh catalog evidence defaults to 24 hours. A stale catalog does not permit
automatic exploration. Previously verified access alone also does not discover
new auto routes: a fresh entry or intentional configured connection is required.

`Billing="included"` or `"unmetered"` explicitly describes existing authority.
Unknown billing excludes. Metered routes require `AllowMetered`, `MaxCost`, and
an enforceable candidate `CostUpperBound` within that same-currency ceiling.
These fields cannot convert subscriptions into API credits or enable overages.
The caller must establish/enforce that upper bound; an optimistic prediction is
insufficient. This is not a task spend ledger or a currency conversion service.

## Capacity snapshots

Each candidate lists every applicable `PoolUse`. A pool key is the provider's
quota scope plus bucket/window, shared by every applicable model alias and
account, including organization-wide allowances. The caller must supply the
complete association; the resolver cannot discover hidden provider sharing.
An unknown account pool still needs a snapshot containing its local in-flight
count. Missing pools or mismatched exact units exclude rather than imply zero
usage. An explicitly admitted unmetered route uses an `Unmetered` pool.

`Remaining=nil` and `Estimate=nil` are unknown, distinct from a known zero.
Known headroom is `Remaining - Reserve - LocalReserved`; each outstanding local
claim is supplied once. All applicable windows must pass. Observations with
coarse attribution must use conservative outstanding claims; the resolver never
assumes a provider snapshot already included local usage. Values are finite and
nonnegative; different units are never added or converted.

Quota freshness defaults to two minutes. Future observation timestamps, stale
observations, missing estimates, or a passed reset not followed by an observation
make headroom unknown. Known exhaustion and explicit `Blocked` remain blocking
until the caller supplies updated evidence. A reset timer never grants capacity.
Clock shifts/suspend require the caller to refresh/invalidate snapshots.

Unknown capacity permits one bounded attempt only when `Inflight==0`, or is
excluded under `RequireKnownCapacity`. Include running and uncertain external
work in that count; an expired lease, cancel request or process crash is not proof
of stopped work. Known capacity subtracts the supplied reservations; unknown
capacity applies the same pool count across aliases and accounts.

**This function does not atomically reserve anything.** Two concurrent callers
given the same snapshot can propose the same capacity. The caller's future P04
ledger transaction must recheck evidence, outstanding reservations and in-flight
counts, then reserve/create the attempt atomically, re-resolving on contention.
Until integrated, do not use this package to claim multi-process/provider quota
safety. `snapshot_only` is always included in a successful decision.

## Stable ranking and explanations

All constraints filter first. Continuity is then preferred for every profile.
The remaining lexicographic priorities are visible in `Decision.Ranking`:

| Preference | Priorities after continuity |
|---|---|
| balanced | Verified entitlement, known capacity, known latency, marginal cost |
| faster | Known latency, verified entitlement, known capacity, marginal cost |
| conserve | Marginal cost, verified entitlement, known capacity, quota pressure, known latency |
| quality | Evaluated quality class, verified entitlement, known capacity, known latency, marginal cost |

Stable candidate ID breaks ties; candidate input order does not. Unknown latency
sorts behind known latency. All eligible paid costs share the request currency;
included/unmetered routes have zero marginal cost. Quota pressure is the largest
estimated share of usable headroom among the route's pools, never a sum of token,
request, money or percentage values. This is a transparent conservation heuristic,
not a claim that different allowances have equal value. Unknown pressure is not
invented. The policy is intentionally simple and unbenchmarked.

`Decision` contains requested/effective selections, native effort, structured
reasons, each excluded candidate's reasons and ranked metrics. Errors retain the
decision for inspection. Malformed snapshots return a typed usage error (exit 2);
no eligible route uses exit 4 when an otherwise eligible route is capacity-blocked,
or exit 5 for policy/evidence conflicts. No private native diagnostics are read.

## Verification

Use the project's pinned Go toolchain and cache environment from `tools/dev.py`:

```console
go test -p=2 -count=1 -timeout=60s ./internal/routing
go vet -p=2 ./internal/routing
```

Tests exercise the actual resolver with synthetic snapshots: independent pins,
native effort mappings, no-auth admission, workspace/cwd scope, capability and
permission guards, denied/unknown/advertised entitlement, first-use gating,
shared allowance windows, unknown concurrency, reservations, unit mismatch,
stale/reset/clock cases, spend authority, deterministic profile ordering and
concurrent immutable reads. Malformed and newer-schema enum cases fail closed.
These tests establish selection behavior, not provider telemetry quality, native
binding/enforcement, ledger atomicity, inferred task quality or live performance.
