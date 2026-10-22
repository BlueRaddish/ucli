# Continue from the verified experimental application

Read README, docs/development-slice.md and the relevant existing plan packet.
Use an isolated contributor branch and the public [verification route](verification.md).
G0 remains open for provider/platform admission. Start with offline protocol
fixtures; historical verification results do not authorize access to another
contributor's accounts or computer. Native sign-in, credential refresh and live
inference require that user's explicit authorization and a bounded usage budget.
Preserve existing native sessions, never copy credentials into UCLI, and do not
enable paid overages.

## What works now

The Go CLI has one shared foreground application service, durable SQLite intent,
task revisions/drafts/history, replay protection, workspace fences, bounded owned
process streams, independent model/account/effort/permission choices, a deterministic
snapshot resolver, explicit demo and native Muse echo, and offline workflow graphs.
Windows ConPTY, interruption/restart/recovery and packaged scripts have actual
receipts. Workflow graph readiness is not execution or completion evidence.

Run the shared route in docs/verification.md with your Go 1.27.2 toolchain.
Full integrity scans belong to explicit doctor, not every command. Keep original
slow/failing evidence alongside corrections and benchmark the actual packaged CLI.

## Continue optional runtime and voice admission

The source now adds explicit Claude byte preparation and English `whisper/tiny.en`
voice preparation. Keep ordinary startup/catalog/task browsing free of optional
setup. `voice setup` remembers interpreter/site/model/language/duration so later
voice commands stay short; flags/environment override them and `--offline` remains
per invocation. Interactive speech is reviewable literal draft input. Preserve
composition on cancellation; only an explicit `Enter` or `/send` submits the reviewed text.

Source `1264b70beb69` passed the full-tree build, packaged voice checks, hidden
ConPTY route and receipt-owned local installation. Follow the optional route in
docs/verification.md and retain the original failed receipts. Synthetic backend
checks do not validate real package/model downloads, microphone hardware, native
backend compatibility or transcription accuracy/latency. Those are separate next
checks with an isolated cache and explicit voice request; no automatic setup is
allowed to make them pass. A prepared Claude binary is still unadmitted for managed
AI execution. An existing supported Python is required; interpreter installation
and a transitive wheel hash lock remain outside the implemented scope.

The local Murmur source audit informed capture/stop and transcript-only interaction
concepts. No source was copied because no tracked license/header was found. Keep
global hotkeys, clipboard/automatic typing and startup downloads out of this slice.

## First managed AI adapter: P02/P12/P15/P16

Experimental pinned Windows Codex/Claude drivers are now registered through explicit
native connection references. Their exact executable, account/configuration/model
and effective permission envelope are checked before dispatch. Read-only is
text-only without tools; sandbox explicitly permits full local computer access.
See [native sign-in](native-signin.md) and the adapter guides for the current
boundary. The Muse implementation still admits only echo. Catalog advertisement,
login status and an empty thread are not proof of entitlement or tool enforcement.

The 2026-10-18 native verification used source `24ae3fdf9f6b`.
Account reuse/unlink/reconnect passed, but all five Codex live attempts remained
unknown and the one Claude attempt failed;
Claude was signed out on the verification machine when checked on 2026-10-18.
This observation does not describe another contributor's account state. The last
Codex attempt passed a native warning and
then reached an unsupported execution notification. Its generic diagnostic did not
retain the method, so do not guess its identity. Before further inference, add fixed
literal diagnostics for reviewed notification names, inspect the exact installed
schemas and tagged emitters, and drive actual-child fixtures. `turn/plan/updated`
and `turn/moderationMetadata` are known unsupported methods, not established causes
of this failure. Preserve permission/identity checks and unknown-outcome guards.

The bounded offline packet is implemented in
[reviewed notifications](../internal/adapters/codex/notifications.md). Only eight
reviewed method literals enter diagnostics; unknown names and all payloads stay
withheld. Plan/moderation metadata requires matching thread/turn identities and
bounded schemas. Terminal-interaction/patch-update details require explicit
sandbox permission and a matching active command/file item. The driver discards
their contents; they neither execute reported input nor settle a turn. Proposed
plan deltas, MCP and automatic approval-review items remain outside the admitted
lifecycle. Existing warning/configuration/reroute boundaries are unchanged.

Actual-child fixtures cover completion, failed terminal, EOF, malformed/foreign
metadata, bounds, active-tool constraints and warning ordering. Separate public
Driver-to-App tests preserve fixed diagnostics, terminal classifications and
unknown-work guards through restart and same-command replay. This layered proof
does not identify the old unrecorded notification or establish live inference.
Preserve earlier unknown receipts. Any later live check remains a bounded opt-in
after the current source and packaged checks pass.

Exact-tag references: [moderation emission](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server/src/bespoke_event_handling.rs#L422),
[checklist emission](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server/src/bespoke_event_handling.rs#L1291),
[checklist default-off resolution](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/core/src/config/mod.rs#L2730),
and [event mappings](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server-protocol/src/protocol/event_mapping.rs#L422).
UCLI currently neither pins nor verifies inherited `tools.update_plan.enabled`;
absence resolves to false in that tag. Neither this inventory nor reachability
identifies the actual unrecorded notification in the failed fifth request.

After offline fixtures pass, obtain explicit authorization for any live check
on the contributor's own test connection; do not copy an active credential store.
Verify identity, harmless bounded inference, real effective model,
read-only/tool denial, cancellation terminal and
cross-account isolation. A second real account and actual macOS host are separate
evidence. Never turn a local process timeout/death into proof of remote stop.
The GitHub CLI identity bridge is local and optional. Hosted GitHub identity and
sync remain separate from provider authorization; no cloud client ID or sync
service exists. Antigravity has discovery but no admitted status/login/AI route.

Native latency is an open product gate. Explicit Codex status has taken about
20 seconds on this host, and execution currently starts two native processes to
discover and then verify the disabled-MCP envelope. Cached lists stay offline.
Measure setup separately from model time before claiming native parity. A possible
next optimization is a cached disabled-server-name set keyed to the exact binding
and cwd, with a fresh full effective-config check before every turn and a safe
zero-turn refresh on mismatch. That is a proposal, not an implemented guarantee;
do not remove admission checks to improve timing.

## Approved conversation and dynamic catalogs

Use [the conversation contract](conversation-ui.md) for transcript changes and
[the verification route](verification.md) for consuming checks. Submitted prompts,
assistant replies and UCLI messages have distinct display roles, independent of
their text. Keep the frame, draft, cursor and independent pins stable. Native task
history still does not provide provider memory: current submissions use fresh
contexts. Typed display history must not imply a persistent native conversation.

Claude model discovery uses initialization metadata without a user prompt, with
exact advertised resolution/capability checks before dispatch. Keep fixed-effort,
unknown-effort and native-disabled models visible with precise limitations. Do not
reintroduce fallback model-name lists or alias-prefix acceptance. Discovery,
authentication, execution compatibility, entitlement and capacity are separate.
On executable changes, retain account identity, explicit pins and last-good data;
metadata from a new file does not admit its execution. Extended connection records
use format 2 on explicit writes, with format-1 reads retained. Older clients refuse
these extended records; this does not migrate the task SQLite schema.

The next session-ownership slice must separate workers from the foreground UI.
Keep one active turn per provider/account/workspace/permission-bound native context,
persist sequenced output before delivery, and support UI reattachment without
replaying work. Launch lazily and close idle processes only where verified native
resume preserves context. UI detachment or a slow reader must not cancel a worker.
Explicit cancellation still requires native terminal proof; unknown outcomes
remain fenced. Do not add a second task store, infer resume from saved prose or
start every installed CLI at application startup.

## Durable capacity and workflow execution: P08/P17/P21/P22

Explicit schema1-to-schema2 migration now exists, holding a writer barrier through
quiescence, verified pre-migration backup, metadata backfill/audit and commit.
Snapshot certificates and migration outcomes are separate. Actual retained older
binaries refuse schema2 reads and writes. Existing/new homes stay schema1 until
the explicit command. Use this tested seam for future tables, retaining original
JSON/unknown fields and fencing already-open older writers. Public restore,
downgrade compatibility and physical power-loss certification remain future work.

The resolver is pure snapshot selection, not quota reservation. Native acceptance
has a conservative atomic per-runtime running/unknown guard per UCLI home; it does
not cover other UCLI homes or native clients. Add provider-scope
quota observations/reservations and create the accepted attempt in one transaction,
rechecking on contention. Preserve unknown capacity, shared pools/units, stale reset
observations and uncertain in-flight work. No reset timer, expired lease or account
cycling grants capacity or authorization. Auto cannot enable paid overages.

`internal/workflow` validates v1 graphs and explicit snapshots. `workflow view FILE`
and interactive `/workflow` now inspect readiness, dependencies, requested settings
and prompts with filtering, list/detail navigation and responsive terminal layouts.
They start no tasks and preserve interactive composition. A future executor
must persist graph revision, node generation, accepted command/attempt links and
ownership, then resume from durable receipts without replaying completed or unknown
steps. Reuse App.Run/Store.Accept rather than a second execution or JSON state path.
Implement artifact identity/validation and required check evidence before declaring
a step complete; agent prose and exit0 alone cannot establish that evidence.

## Platform and product limits to retain

- Windows native echo is pinned to one exact Muse binary. Experimental pinned
  Codex/Claude routes exist; full provider admission, shared real quota feed,
  account cycling, decision agent, supervisor and native escape remain unfinished.
- Darwin external-process launch deliberately fails before spawning. Implement and
  host-test a safe lifecycle before admission; CLI/state/demo cross-build is separate.
  Linux WNOWAIT ownership is source-reviewed/cross-compiled, not host-executed here;
  WSL returned E_UNEXPECTED and was not restarted.
- Interactive input has a grapheme-aware inline editor, searchable selectors,
  cancellable archive task finder, read-only `/sessions` monitor and offline workflow
  view. Nonempty `Enter` sends the reviewed composition; `/send` remains available.
  Rich mode requires two `Ctrl+C` presses within two seconds to interrupt or exit.
  New behavior requires the focused and packaged checks in the verification route;
  earlier Windows ConPTY paths and ANSI-emulated
  layout review have evidence; owner usability/accessibility and Linux/macOS
  terminal behavior remain unexecuted. Keep drafts,
  selection, task identity and displayed CAS revision through history/navigation.
- Task summaries page/search the archive without loading all drafts. Schema1 search
  remains O(task-body bytes); its previous no-match case measured 719 ms p95 locally.
  Explicit schema2 migration now enables trigger-maintained typed summaries and an
  O(metadata rows) scan. It is not an indexed substring search. Keep the same fixture
  and binary when comparing schemas; latest samples belong in application evidence.
  Legacy mixed
  fractional/offset timestamp spellings follow their text index rather than a new
  chronological normalization. Attempt history remains bounded and adaptive.
- GitHub sync, CLI extensions, updates and remote CI are unimplemented/unexecuted.
  The local installer now supports receipt-owned replacement with retained backup,
  crash recovery and conditional rollback. A remote updater/release installer and
  physical power-loss/noncooperating-filesystem certification remain future work.
