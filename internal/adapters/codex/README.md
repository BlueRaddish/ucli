# Codex native adapter (experimental)

The public driver admits only the reviewed Windows amd64 Codex CLI 0.162.1 binary
with SHA-256 `dd13bdb162a184fa04039488b234a8258ef9b76f84e926c505d5742b19249a6a`.
An explicit native connection supplies the executable, native home, home mode,
ChatGPT authentication mode and opaque account fingerprint. No credential bytes
are copied, parsed by UCLI or persisted in UCLI receipts. Codex owns normal refresh.

`Inspect(ctx, NativeBinding)` is an explicit zero-turn discovery operation. It
confirms the native home, ChatGPT account type, recognized plan, stable account
fingerprint, rate-limit windows and paginated public model catalog. Explicit native
`account: null` is signed out (`AuthMode: none`); malformed/unknown/API-key auth
is an error. An empty expected fingerprint is allowed only for bootstrap discovery.
A nonempty expected fingerprint must match SHA-256 of `codex` + NUL + the exact
native workspace-routing ChatGPT account ID. Neither that original ID nor email
is returned. A catalog is not proof that every listed model is entitled.

Models expose native IDs without the `codex/` display prefix. Public efforts are
low/medium/high/max. Low maps to advertised low, otherwise minimal, otherwise none;
medium/high require those exact advertised values; max maps to the highest
advertised value in ultra, max, xhigh, high, medium, low, minimal, none order.
Unsupported intent is refused. Driver requests must use mode native and the exact
connected account; the driver strips only the `codex/` prefix before native RPC.

## Launch and authority

Existing homes are references to native-managed authentication, not isolated copies.
Separate homes additionally force `cli_auth_credentials_store="file"`, preventing
ambient global-keyring fallback. Environment construction does not inherit API
keys, endpoint overrides or native session variables. The binary is hashed before
launch; same-user hostile file replacement is outside this cooperative boundary.

Read-only disables shell/unified execution, external tools, web search, plugins,
hooks, image/browser/computer tools and agent delegation. Explicit `sandbox` means
**full local computer access**: native dangerFullAccess with command/file tools,
not an OS security sandbox. Both permissions retain approvalPolicy never; unexpected
server approval/tool/auth requests are refused and cannot expand authority.

Native empty TOML tables merge rather than erase inherited configuration. Execution
therefore uses two bounded processes: zero-turn effective config discovery for the
selected cwd, then a new owned app-server with per-MCP-server disabled overrides.
The verified second child is reused for account/catalog admission and the turn.
Only ASCII native server identifiers are currently supported. Effective provider,
endpoints, tool feature gates and disabled MCP entries are checked before creating
any thread. Native config races with another same-user writer are not an adversarial
CAS guarantee. Existing config files are never changed.

A fresh ephemeral thread must confirm cwd/model/provider/approval/sandbox. A durable
context callback with provenance confirmed precedes dispatch. No model fallback is
allowed. Unknown or nearly exhausted capacity (any observed window at least 95%)
refuses dispatch; this snapshot is not a reservation against other native clients.
No API-key fallback, purchase, paid-overage enablement or account change is attempted.

The bounded JSONL engine preserves agent output, binds every execution item to the
thread and turn, ignores private reasoning/tool payloads, and requires matching
terminal receipts. Sandbox command/file events have explicit lifecycle checks.
Limits remain 1 MiB/frame, 16 MiB combined transcript, 4096 envelopes, 128 pending
notifications, 256 native items, and the core output ceiling. Setup can take up to
45 seconds per bounded phase; the turn has a 30-second deadline. Cancellation asks
for native interrupt, then stops the owned process tree; missing remote terminal
proof remains unknown. Settlement and context-only cleanup errors are distinguished
from uncertain teardown. Slow callbacks must honor the adapter callback contract.

## Verification

Run `go test -p=1 -count=1 ./internal/adapters/codex` with the repository pinned Go
and memory settings. Tests use owned child fixtures, never native credentials or
inference by default. Native admission negatives, timestamp parsing, identity and
catalog/capacity checks, explicit signed-out state, permission/config guards,
actual-child sandbox event flow, malformed frames and cancellation are covered.

An explicit native probe is available through `python tools/probe_codex_live.py
--binary ABS --native-home ABS --go ABS --evidence NEW_DIR`. Add `--infer --model
EXACT_ID` only when a real short turn is authorized. It asks solely for
`UCLI_LIVE_OK`, disables tools and saves sanitized metadata, not native protocol
transcripts/stderr. Each evidence directory must be new. The real Windows zero-turn
proof is `.build/codex-live-envelope-keys-fixed/receipt.json`; it verified the native
ChatGPT identity/catalog/capacity and effective disabled-tool envelope. Inspection
was 26.4 seconds and config-only preflight 17.3 seconds in that run: these are observed
native handshakes, not ordinary UCLI startup costs or a performance-parity claim.
One authorized real gpt-6-luna/low marker turn was then dispatched. It reached a
confirmed native context but returned unknown with no output; retained receipt:
`.build/codex-live-one-turn/receipt.json`. No retry was sent. Driver elapsed time
was 33.9 seconds across its two processes, plus 19.3 seconds for the probe's separate
explicit inspection. This does not prove remote termination or inference success.
The initial generic public wrapper lost the local protocol reason; the follow-up
preserves fixed local error reasons and numeric RPC error codes while withholding
native payload diagnostics. Native sandbox tool execution remains unverified.

Primary references: installed 0.162.1 generated JSON schemas retained under
`.build/codex-live-discovery/schema`; [official app-server documentation](https://learn.chatgpt.com/docs/app-server),
[configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference),
and [managed authentication](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server).
The installed schema and consuming binary checks take precedence over documentation
for the exact reviewed notification dialect (including emittedAtMs).

Offline diagnosis also checked the exact public `rust-v0.162.1` source:
[turn processor](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server/src/request_processors/turn_processor.rs)
constructs an inProgress acknowledgment with empty items; the matching
[notification handler](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server/src/bespoke_event_handling.rs)
checks the same started-event shape. These checks remain strict. Source files and
hashes are retained in `.build/codex-live-source`; no unknown-to-complete reclassification
or speculative event relaxation was made after the failed live request.

Both discovery and execution initialize with `explicitGatewayOauth: true`. The
installed v1 InitializeParams schema and the exact tagged initialize processor
confirm that this prevents implicit browser gateway authorization while allowing
normal native credential refresh. Starting a new login remains the explicit
connection-service action. Owned-child fixtures require this capability; native
execution of this final handshake revision remains part of the coordinated check.

The failed-request review found a concrete schema mismatch: native `Turn.error` is
optional as well as nullable. ACK, started, completed and interrupted turns now
accept either absence or JSON null for that field only; malformed non-null values
remain rejected and failed terminals still require a structured error. Actual-child
fixtures cover omission and malformed alternatives. Codex also copies safe protocol
failure reasons into its durable outcome message and reports the exact model
confirmed by thread/start separately from the requested display model. Native
confirmation of these final corrections is pending the coordinated packaged check.

The same schema audit covers optional nullable account, model-pagination cursor
and quota windows. Their absence is schema-valid, rather than observed native
serialization in this check. The required `requiresOpenaiAuth` boolean remains
validated; an omitted account only identifies signed-out discovery and never
satisfies a pinned execution identity. Missing quota windows are allowed only
when at least one other window is valid and all supplied windows are valid.

Included usage additionally requires native `ordinaryUsageAllowed: true`; false,
null, omission or malformed values leave capacity unavailable, even when percentage
usage is low. Inspection can still return authenticated identity/catalog metadata;
execution refuses before thread/turn creation with `codex_capacity_blocked`, exit 4.
This follows the exact tagged [account protocol](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server-protocol/src/protocol/v2/account.rs)
and [model protocol](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server-protocol/src/protocol/v2/model.rs).
No paid-usage fallback or new native request was introduced by this correction.

Native `error` notifications are bound to the active thread and turn and do not
settle an attempt. The reviewed `willRetry` boolean permits native transient retry;
UCLI does not resend the turn. A matching failed terminal produces a durable typed
auth (3), quota (4), request (5), or provider (8) failure. Without a matching terminal,
the attempt remains unknown, including on timeout. Only reviewed error enum tags
and the retry boolean enter diagnostics; native messages/details and unknown enum
payloads never do. Configuration warnings remain strict; valid active-turn
warnings are informational as described below. The exact tagged notification.rs, thread_data.rs, shared.rs
and bespoke_event_handling.rs establish this boundary; fixtures exercise it without
another native inference. This does not retrospectively identify the prior native
notification's payload or establish a successful live turn.

A source-proven routing correction pins the ChatGPT-only inference base to
`https://chatgpt.com/backend-api/codex`. In the exact reviewed
[provider implementation](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/model-provider-info/src/lib.rs),
`to_api_provider` selects that managed-auth default only when `base_url` is absent;
an explicit base URL wins. The matching
[configuration loader](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/core/src/config/mod.rs)
passes nonempty `openai_base_url` into that provider. UCLI's previous explicit
OpenAI API URL therefore overrode ChatGPT inference routing even while separate
account/catalog/quota reads succeeded. Effective configuration now rejects that
old API URL and unreviewed endpoints before thread creation. Prior failed receipts
remain unchanged; this correction's successful native inference is still unverified.

The reviewed `WarningNotification` is nonterminal: it requires a bounded string
message and permits an absent/null thread target. During an active turn, UCLI
accepts only an absent/null/matching thread target, still rejects an explicitly
mismatched turn target, and waits for the matching terminal result. It retains no
warning text. The outcome includes the fixed note "A native warning was observed;
private warning text was withheld." Configuration warnings and setup warnings
remain rejected; model rerouting and effective permission checks remain strict.
Owned-child fixtures cover warning-to-complete, warning-to-failed, warning-to-EOF,
and malformed/mismatched warnings. This final parser revision has not made another
native inference; the prior warning-stopped native receipt remains unknown.

The later coordinated installed build `24ae3fdf9f6b` passed its full fixture and
packaged checks. Its fifth real sandbox request passed a native warning and then
ended unknown on an unsupported execution notification, with no assistant output.
The catch-all did not retain the method; do not infer which event occurred. All
five Codex requests remain unknown, so successful native AI/tool execution is not
confirmed. Further inference stopped. The next packet is safe literal diagnostics
for known schema methods plus offline consuming fixtures before another live call.
See [coordinated evidence](../../../docs/verification.md).
