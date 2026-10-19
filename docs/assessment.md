# Assessment and product decisions

Date: 2026-10-17. Status: design assessment. The owner selected a new UCLI project
and requested an implementation/development plan before implementation.

## Product thesis

The product earns its place by reducing the work required to start, steer and
finish AI tasks. Universal flags are a means to this outcome. The user should not
need to know which provider names a setting "thinking", "reasoning" or "effort",
which resume command it accepts, or where its quota panel lives.

The default encounter should be a useful prompt with a small number of recognizable
destinations. Expert controls and native escape routes remain available. The
interface must explain effective behavior without exposing internal adapters,
protocol schemas or database concepts during normal work.

This is a strong product direction if we can make one useful task easier than in
native clients while preserving their tools, interruption behavior and output.
The largest risks are semantic differences, weak account isolation, unsafe retry,
and visible orchestration latency. More provider logos do not resolve those risks.

## Confirmed requirements

| ID | Requirement | Design consequence |
|---|---|---|
| R01 | Comfort and minimal memorization | Discoverable interactive entry, shared vocabulary, good defaults, contextual help |
| R02 | Unified syntax | One command registry and consistent semantic settings, explicit native passthrough |
| R03 | Choose integrated models and effort | Dynamic catalogs, namespaced exact IDs, aliases and effective-setting display |
| R04 | Multiple accounts and account cycling | Isolated credential contexts, verified identity, eligible account pools and sticky attempts |
| R05 | Fluid work with multiple agents | Durable tasks, dependencies, steering, handoffs, checks and interruption recovery |
| R06 | Automatic model/effort decisions | Policy-constrained routing with optional bounded decision agent |
| R07 | Usage and reset-aware behavior | Timestamped quota observations, shared pools, reservations, waiting/fallback policy |
| R08 | Windows/macOS/Linux expansion | Platform boundaries, native shell/terminal tests, distribution parity |
| R09 | Future runtimes and capabilities | Versioned adapter contracts and capability negotiation |
| R10 | Competitive responsiveness | Local-first fast paths, streaming backpressure and measured native-relative overhead |
| R11 | Robustness and recovery | Durable state, explicit unknown outcome, conservative replay and corruption recovery |
| R12 | GitHub identity and linked connections | Optional cly-independent UCLI identity, device management and provider consent boundaries |
| R13 | Separate project | Separate source, binaries, state, credentials namespace and release process from cly |
| R14 | Privacy and control | Local default storage, scoped account/model routing, permission preservation, no automatic paid overages |
| R15 | Access to native features | Explicit native route with honest reduced integration capabilities |

"Universal" means a stable user-facing contract plus discoverable differences.
It does not imply that every runtime can run every model or that all providers
support the same permissions, resumability, billing or authorization.

## Recommended shape

Build a local CLI and terminal experience above existing supported agent runtimes.
UCLI owns tasks, account selection, effective settings, durable handoff and route
explanations. Providers own inference and their native tool loop in the first
release. A direct-API tool loop is a later execution mode with explicit ownership.

Prefer built-in, typed adapters initially. Give third-party adapter processes a
versioned stdio contract after two real adapters expose the shared shape. Avoid a
public plugin marketplace, distributed scheduler or generalized workflow language
before the core task loop works. ACP is an integration route where supported;
account, quota, persistence and policy contracts still belong to UCLI.

The architecture recommendation is Go, with a packaged cross-platform spike to
validate process control, terminal behavior, SQLite driver and latency before the
choice becomes difficult to change. It is a recommendation about shipping and
maintaining a local product; no measurement here proves Go faster than alternatives.

## Scope boundary

The first useful release should launch a task through a structured adapter,
stream its output, expose effective settings, preserve its context, and recover
from a provider interruption. It should work locally without UCLI cloud login.
Support depth outranks provider count: a working adapter must include failure,
permissions, identity and termination behavior, not just a successful prompt.

The next increments add verified account isolation, quota observations and bounded
deterministic routing, then multi-agent workflows and optional decision assistance.
GitHub identity/settings sync can follow the local account contract. Subscriptions
remain with the providers; a GitHub account does not transfer their entitlements.

Defer: collaborative cloud execution, unrestricted credential synchronization,
automatic global import from native stores, third-party plugin auto-install,
continuous autonomous optimization, universal native-session conversion, mobile
clients and a replacement agent loop for every provider. These remain compatible
with the boundaries; they are not prerequisites for comfortable local work.

## Assessment of the earlier cly proposal

Retain its useful distinctions: model/runtime/account/preset, explicit capability
reporting, context packets and quota provenance. Change its implementation premise:
UCLI has no obligation to inherit Bash entrypoints, Python internals or cly grammar.
Cly can remain a working launcher while UCLI develops independently.

Any future import is read-only, previewable, versioned and reversible. No automatic
profile, credential or session migration happens on startup. Code reuse, if chosen,
requires an identified unit, license preservation and UCLI-specific tests. Prior
cly success on a platform is not UCLI verification.

## Evidence and uncertainties

Primary-source integration claims and local observations are recorded in the
[source ledger](research/source-ledger.md). The plan distinguishes documentation,
local inspection and proposed tests. An installed CLI or advertised model does not
establish model entitlement or simultaneous account isolation.

On this machine `agy models` and a zero-turn `/usage` print command expose model
choices and structured quota groups. This makes a telemetry spike promising; it
does not establish a stable supported contract across versions or devices.

Provider authorization rules differ. In particular, native local invocation,
third-party hosted login and raw token reuse are different integration questions.
Use supported product-specific flows and keep those scopes separate in the provider
matrix. Do not assume API and subscription capacity are interchangeable.

Ease of use and latency remain hypotheses until measured. The first prototype must
be reviewed by the owner doing actual tasks; a mock screen or agent review cannot
establish comfort. Proposed targets in the experience/performance docs are decision
criteria, not results.

## First evidence that could change the design

1. Structured adapters cannot preserve required tool approvals or interruption.
   Response: reduce the managed capability tier or choose a supported integration.
2. A provider cannot isolate two accounts on a supported OS.
   Response: mark concurrent accounts unsupported for that adapter; do not overwrite
   shared credentials. Continue other adapters and expose a supported single account.
3. The proposed Go packaging/terminal path misses responsiveness or maintenance
   goals. Response: compare the bounded Rust/TypeScript/Python spike alternatives.
4. Routing consumes more time or quota than it saves. Response: retain fixed
   selection and deterministic routing; keep the decision agent optional.
5. First-time users cannot begin and resume without instruction. Response: revise
   navigation/defaults before adding providers or more settings.

## Readiness verdict

Ready to begin bounded feasibility work and contract refinement. Not ready to
promise universal account portability, measured performance, or seamless conversion
of native sessions. The [implementation plan](implementation-plan.md) turns those
uncertainties into early gates and separately assignable work.
