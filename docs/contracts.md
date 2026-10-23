# Core contracts — draft v0

These contracts coordinate implementation agents. They are proposed, not a public
compatibility promise. The contract owner freezes v0 after the first two adapter
spikes. Any change then updates schema/examples, consumers and compatibility tests
in one integration unit. The workflow state machine is authoritative in
[workflows](workflows.md); command behavior is authoritative in [experience](experience.md).

## Identity and ownership

Use opaque generated IDs for UCLI records. Keep native IDs separate and namespaced
by adapter, native store and account reference. Never use an email, model display
name, directory label or PID alone as a stable primary key.

| Record | Owns | Does not imply |
|---|---|---|
| Provider | Service identity and supported authorization/billing routes | A runtime or a subscription |
| Runtime | Installed agent executable/service, adapter and observed version | Model entitlement |
| ModelRef | Provider/runtime route plus exact native model ID and catalog revision | Access merely because catalog lists it |
| AccountRef | Opaque connection identity, provider, scope, credential handle | A copied native token or independent quota |
| Preset | Reusable semantic settings and eligible account references | A separately authenticated identity |
| Project | Workspace roots, trust/data policy and local device mappings | Permission to trust repository instructions blindly |
| Workspace | Concrete local root identity, project binding and location revision | The same checkout as another worktree or a portable absolute path |
| Task | User intent, acceptance checks and durable output references | One native conversation |
| Attempt | One routed execution with effective settings and native run references | Safe replay after failure |
| Workflow | Versioned graph of tasks and dependency/output bindings | An unrestricted planner loop |
| ArtifactRef | Content hash, location, type, producer and workspace revision | Truth of a model's claim about the content |
| QuotaBucket | Provider scope, units/window and applicability | One independent allowance per model |

Display labels can change without changing identities. Deleting a connection must
not erase the provenance of historical attempts. Secrets live behind credential
handles; serialization of public records must exclude secret values.

### Workspaces and continuity

Project identity is logical; Workspace identity names a concrete checkout/root.
Discover the enclosing Git worktree root, or an explicitly registered containing
root for non-Git work; otherwise offer the current directory as a new root.
Subdirectories of a bound root share its workspace. Discovery does not change the
execution directory: preserve the launch directory or explicit `--cwd`, and show
it separately when it differs from the workspace root. Resolve filesystem aliases
using platform evidence rather than string labels or unconditional case-folding.
Linked worktrees have separate workspace IDs. Sharing approved project settings
requires an explicit project binding; remote URLs and folder names never merge
identity or grant trust. A moved/missing root needs explicit rebind and validation.
Trust is scoped to the concrete workspace/configuration sources, separately from
task authority; a new binding does not silently inherit credential/helper trust.

A task is the visible ongoing work thread; no separate universal conversation
engine is required. Its accepted inputs and scope revisions are durable. From an
open task, **Follow up** keeps the task ID and records a new input/revision, preserving
earlier results and checks against their original revision. New work cannot reuse
the old revision's completion verdict. **New task** and standalone `run` create
fresh work unless continuation is explicit. **Resume** continues interrupted work
under the recovery contract, rather than silently submitting a new goal.
Follow-ups inherit the task's accepted settings/policy unless intentionally changed;
later global defaults do not replace them. Dependencies pin producer revisions and
artifact identities. A follow-up cannot silently retarget historical consumers or
reopen a completed workflow; changing a workflow requires an explicit graph revision.

An attempt is one provider turn/dispatch unit, including its native tool loop and
input/approval exchanges until that turn ends. A subsequent follow-up starts a new
attempt. Native process lifetime may span several attempts. A native continuation
reference is scoped by runtime/service, native store, connection and workspace;
it has one active-turn owner and is initially reusable only within its owning task
and compatible authority/context. Other tasks receive an explicit handoff or a
verified native fork. Cross-runtime continuation carries selected task inputs,
artifacts and a bounded handoff; private native state is never assumed portable.

Phase 1 uses a shared-reader/exclusive-writer lease on the concrete workspace,
across UCLI processes, before dispatch. Read tasks can share a stable workspace;
writers serialize or use separate workspaces. P21 reuses this rule and may later
add narrower proven scopes. Locks coordinate UCLI work, not unrelated editors or
native clients; record external changes and invalidate affected check evidence.
Native-session ownership is a separate lease even when attempts/tasks differ.
An expired owner does not free uncertain workspace/native-session activity;
reconcile the effects before granting conflicting ownership.

## Effective configuration

Authoritative precedence: explicit invocation or intentional task-specific change;
selected named preset; trusted project configuration; user defaults; product defaults. Organization and
machine policy impose bounds that lower layers cannot broaden. An explicit pin
that conflicts with bounds produces an actionable error, not a silent substitution.

Only a documented allowlist of project configuration participates before workspace
trust. Treat untrusted repo-provided model endpoints, plugins, commands, hooks,
credential helpers and instructions as data until authorized. No repository can
change user credentials or global account pools by being opened.

An intentional change to a running task becomes a new effective revision at a
declared safe checkpoint; it does not mutate an in-flight provider request. A
future default or session-local next-task choice cannot alter another terminal's
task. Persist the source and revision for each change.

The resolver returns each effective field with source, requested value, native
mapping where applicable and whether it was inferred. Keep a stable human summary
and a machine-readable representation. Defaults are recorded per attempt so future
changes do not alter historical meaning.

Public effort intent: `auto|low|medium|high|max`. `max` means the highest supported
effort for the selected model within policy; it does not increase permissions or
spending authority. Model aliases resolve to exact IDs before dispatch; retain both.

Permission envelope: `read-only|workspace-write|sandbox|unrestricted`, with actual enforcement
and provider approval behavior reported separately. The adapter must demonstrate
the guarantee it claims. A prompt saying "do not write" cannot implement a write
restriction. Choose a compatible route or reject an unsupported guarantee. Native
passthrough has its own explicitly scoped capability contract.

| Envelope | Task/tool filesystem effects | Separate policy dimensions |
|---|---|---|
| read-only | No task/tool writes to workspace or outside roots | Shell, network, external mutations and approvals remain independently bounded |
| workspace-write | Writes only to the explicit allowed workspace/output roots | Same independent bounds; no implied credential or remote-service authority |
| unrestricted | No extra UCLI filesystem restriction within OS authority | Does not create account, network, remote mutation or spending authority |
| sandbox | Full local computer access, explicitly requested for a machine the user treats as a sandbox | Native broad-access mode; no additional OS isolation is implied. Task instructions still determine authorized remote actions and spending. |

Runtime operational writes (isolated caches, logs, credential refresh and UCLI
state) are declared separately; they do not authorize task code to write there.
Shell availability never substitutes for filesystem enforcement. An adapter must
report each dimension's enforcement and limits before claiming the common envelope.
Presets may remember explicitly approved settings; workspace trust only permits
the approved configuration sources to participate, not unlimited task authority.

Native instructions, skills, tools/MCP configuration and hooks remain runtime-owned
in v0. The adapter reports loaded/suppressed configuration sources, revisions and
unknown inventory; UCLI records its own injected context separately and avoids
duplicating native instruction text. Verified required tools/skills constrain
routing. Unknown availability cannot satisfy an explicit requirement. A route
change exposes material context/tool differences. Do not build a shared skill
installer or duplicate tool loop to normalize these sources in the first release.

## Adapter interface

Internal interfaces and external transports share the same semantic request/event
types. Built-in adapters are typed implementations; plugins may use stdio JSON-RPC.
No network listener or dynamic plugin install is required for the first release.

The task adapter represents a complete agent runtime owning its tool loop. A raw
inference backend, including a local model endpoint, sits behind such a runtime;
it is not advertised as a coding agent by implementing text completion alone.
Runtime identity may be an executable build or an explicitly configured service
endpoint/build. Connections declare authentication mode, including `none`; an
unauthenticated route has no credential handle. Do not infer endpoint trust from
absence of authentication. A UCLI-owned API tool loop remains a later decision.

### Capability handshake

Return adapter ID/version, protocol major/minor, runtime executable or service identity/version,
tested version ranges, platform, integration mode, methods and capability metadata.
Capabilities are structured, not a boolean "supported":

```json
{
  "schema_version": "0.1",
  "adapter_id": "example-agent",
  "adapter_version": "0.1.0",
  "runtime_version": "observed-version",
  "integration_mode": "structured-cli",
  "capabilities": {
    "streaming": {"support": "native"},
    "resume": {"support": "conditional", "conditions": ["same native store"]},
    "account_isolation": {"support": "unverified"},
    "quota_read": {"support": "unsupported"}
  }
}
```

Supported labels: native, mapped, conditional, unsupported, unverified. Availability
is a separate runtime observation: ready, missing, incompatible, auth-required,
unavailable. Every guarantee carries evidence/version scope in the admission record.

Static runtime capability caches and route-specific entitlement/configuration
observations have separate scopes. The architecture's cache key includes connection,
endpoint, auth generation, model and relevant workspace/configuration context;
reconnect or scope changes invalidate affected evidence. Reuse never crosses an
account boundary merely because the executable/version is unchanged.

Required initial operations: discover runtime, read capabilities, list catalog,
resolve settings, start, stream, interrupt, collect result and diagnose. Optional
operations: account connect/status/disconnect, quota read, native resume, steer,
status query, checkpoint, fork and context import. Missing optional capabilities
are explicit and do not prevent a useful lower tier.

### Start request

Include request/attempt/task IDs, protocol revision, exact route, connection auth
mode and credential handle when required, effective settings, workspace mapping,
task input, artifact refs, required
capabilities, timeout/deadline and authority snapshot. Send prompt payloads over
stdin/protocol rather than constructing shell command strings. Environment variables
are passed as a deliberate allowlist plus documented runtime requirements.

Resolve executables to explicit paths. Native command args are arrays with platform
launch handling, not concatenated shell snippets. Never log raw environment or auth
headers. The account manager provides credentials at the narrowest supported boundary.

### Streaming envelope

Events contain schema version, task/attempt IDs, monotonic sequence within a run,
UTC observation time, event type and payload. Retain native IDs in a namespaced
extension where useful. Types include accepted, message delta, tool activity,
approval/input request, checkpoint, usage observation, warning, diagnostic and
terminal result. Text deltas are not durable task completion evidence.

The stream distinguishes runtime acceptance, first provider output, first useful
content, terminal result and persisted result. Duplicate event IDs are idempotently
ingested. Gaps trigger reconciliation or explicit incomplete status. Late output
from a superseded/canceled attempt cannot advance the current task silently.

Use bounded queues and bounded event sizes. Coalesce render deltas without dropping
semantic events. Apply backpressure or spill bounded raw streams to local disk;
report truncation rather than pretending a complete transcript. Reject malformed
messages with a diagnosable adapter error and preserve last valid state.

### Completion and errors

Terminal result includes status, output/artifact refs, native continuation ref,
observed usage, requested/effective settings, execution timestamps and known side
effects. Success of the provider call and success of task acceptance checks are
distinct. A result without required output or failed acceptance is not task success.

Structured errors include stable code, origin, human message, retryability class,
acceptance certainty, side-effect certainty, optional retry/reset hint and recovery
actions. Retryability: safe, reconcile-first, manual, or forbidden. Only the workflow
engine decides another attempt within the user's policy and budget.

## Task and process persistence

Mutating task/workflow commands carry a command ID and expected task/graph revision
(creation expects absence).
One transactional application path validates the current state, applies a command
and stores its receipt. Replayed IDs return that receipt; changed payloads using
the same ID are rejected. Stale edits preserve submitted text and return current
state. Approval responses consume a stable request at most once. Cancel is a
monotonic stop request for the captured attempt/revision and its queued work;
a stale edit revision must not prevent stopping that named target, nor retarget
the cancel onto newer work. A deliberate later follow-up may create a new revision
after uncertain effects are reconciled; it never clears the canceled attempt.
All clients use this same path.

Choose the executor host at launch and keep it for the attempt's lifetime. Runs
that support detach start supervisor-owned; detaching removes a UI subscription.
Foreground execution does not promise live reparenting of children, pipes or
terminals. A supported checkpoint can start a new managed attempt. The application
service is independent of terminal lifetime and shared by both host types. Reusing
a live native process across attempts retains its owning host; changing host needs
shutdown/relaunch or separately verified reconnection, not implicit adoption.

Use transactional local durable state for accepted tasks, graph edits, attempt
creation, leases, recovery state and event/result references. The architecture owns
the DB implementation. Files hold potentially large content-addressed artifacts.
Do not store the active database on cloud mounts/network shares. No secret content
is needed in the scheduling database.

Persist an intent before dispatch, then provider acceptance/native identity, then
the terminal result and acceptance checks. A crash between these steps produces a
specific reconciliation case. No exactly-once claim spans arbitrary external tools.
An unknown external outcome requires inspection/status lookup before replay.

The experimental [in-process execution host](owned-execution.md) now separates
durable worker ownership from UI observation. Acceptance and native admission
remain on the shared application path; `Follow`/`Wait` cancellation only detaches
a reader. Explicit `Cancel` targets a captured local handle. `App.Close` stops
admission, requests stop and joins workers before closing SQLite; an unresponsive
worker leaves state open until it returns. This implements observation separation,
not terminal-close survival or persistent native conversation resumption. Existing
state formats, workspace leases and unknown-outcome guards are unchanged.

The experimental implementation stores a validated `FailureClass`
atomically with a confirmed failed attempt: safe code, origin, exit category,
acceptance and side-effect state. Its sanitized message remains in the receipt.
Live return and command replay consume the same record. Historical failures
without a class remain generic provider failures; malformed saved classes are
internal/store errors. Unknown and interrupted outcomes retain their distinct
semantics rather than being recast as confirmed failures.

Native explicit account labels resolve to connection IDs. Accepted task settings
retain that ID for subsequent drafts and follow-ups; the attempt's requested
selection preserves the original label for intent/replay evidence. Removing a
connection and reusing its label cannot silently retarget existing accepted work.
An explicit later account change is required. `auto` remains an automatic choice
within the linked eligible set and does not become an implicit account pin.

Lease owners include process creation identity, instance ID and heartbeat. PID reuse
does not prove ownership. On startup reconcile orphan attempts without killing
unrelated native clients or shared provider daemons. A supervisor owns only children
it created or explicitly adopted under a verified contract.

Incompatible schema migration requires a maintenance barrier excluding active
executors/writers on the older schema. Refuse or wait with a useful explanation;
do not silently kill tasks or perform hot migration. Writers participate in the
barrier/schema epoch protocol, not just a one-time startup version check.
Schema migrations have backups, forward compatibility limits and startup failure
behavior. A newer unsupported schema must not be overwritten by an older binary.
Integrity checks and export/recovery should remain available if normal task loading
fails. The product reports persistence failure before claiming acceptance/saved state.

## Quota and credential contracts

Quota observations and reservations follow [routing](routing.md). A credential
reference resolves through a per-OS store; absence of a keychain in headless Linux
is an explicit mode with a documented secure alternative, never silent plaintext.
Provider adapters state whether they use native auth storage, UCLI-managed auth,
an API key or an authorized external broker.

Refresh uses a lock per credential owner and atomically replaces credentials. A
run must verify the actual provider identity/scope where supported, matching the
selected connection. Identity mismatch fails before task dispatch. Account isolation
tests include refresh races, global OS keychain service names and shared servers.

An explicit account pin requires evidence of binding the launched runtime to that
connection, even when the provider has no identity-read endpoint. An unchecked
ambient native login is not sufficient. Such a route stays native-only or unverified
and is ineligible for automatic multi-account selection. Phase 1 must already have
a minimal pinned local AccountRef, credential-context binding and capability read;
Phase 2 expands enrollment, rotation, pools and catalog/account UX.

Connect/disconnect and account switching never change permissions on existing
attempts. Revocation is distinct from removing a local label. A synced account
record can say "reconnect on this device" without shipping its credential.

## Extension compatibility

Major version mismatch fails with a clear remedy. Minor additions must be optional;
unknown required capability/method fields fail rather than being ignored. Define
limits on frames, nesting, artifact size, timeouts and path access at the boundary.

Adapters use opaque extension namespaces for provider-specific metadata; common
fields cannot be overwritten. Every public capability has an admission fixture and
at least one supported runtime/OS evidence record. A new provider ships reduced
coverage honestly rather than declaring every feature implemented.

External adapters run with the user's OS privileges unless OS isolation is explicitly
configured. UCLI's permission named `sandbox` means full local access, not isolation.
Plugin signatures/hashes identify provenance, not safety. No downloaded
plugin code executes during mere model listing or project discovery.

## CLI process contract

Human status/errors go to stderr for scripted `run`; requested text or JSON output
goes to stdout. Machine output is versioned and free from spinners/control codes.
Noninteractive commands cannot wait for hidden UI input. The native passthrough
command preserves native output/exit semantics and labels that exception in help.

| Exit | Meaning for managed commands |
|---|---|
| 0 | Command's documented success; detached submission means accepted, not finished |
| 2 | Usage error |
| 3 | Missing prerequisite or authentication |
| 4 | Capacity/quota unavailable |
| 5 | Policy or user input required |
| 6 | Task/acceptance validation failed |
| 7 | External outcome unknown; reconciliation needed |
| 8 | Provider/runtime failure |
| 9 | UCLI/internal/store failure |
| 124 | Timeout |
| 130 | Interrupted |

SIGINT/platform equivalent requests interruption and preserves partial work.
Interruption, detachment, cancellation and destructive rollback are distinct actions;
see the experience/workflow specifications. Native processes may not expose every
operation; capability negotiation must constrain the UI and command availability.
