# Provider, runtime, account, and quota integration plan

Status: proposed implementation design, researched 2026-10-17. UCLI is a separate project; these adapter plans do not authorize changes to cly or existing native profiles. Documented support, local observations, and unverified behavior are deliberately distinct. The [source ledger](research/source-ledger.md) records the evidence boundary.

## 1. The product promise

Users should learn one way to choose a model, adjust effort, add an account, resume work, and recover from exhausted usage. Adapters absorb differences in flags, credential locations, events, and failures. The interface must explain the selected account and effective behavior without requiring users to understand adapter internals.

The common interface expresses intent; it cannot promise identical agent behavior. A requested setting must be applied faithfully, rejected with a useful alternative, or explicitly changed under an already selected automatic policy. Silently ignoring effort, permissions, or account choice would undermine the entire product.

Use independent identities:

| Entity | Meaning | Important relationships |
| --- | --- | --- |
| Provider | Service or inference endpoint | Owns entitlement, billing and usage rules |
| Runtime | Agent harness and its installed build | Owns tools, permission implementation and native session format |
| Model | Provider-specific model identity | May be reachable through several runtimes |
| Connection | One authorized account/workspace/authentication method | References a credential handle and eligibility policy |
| Route | Model + runtime + connection + effective effort | Immutable for one running attempt |
| Preset | User's named preferences and constraints | Resolves to a route when work starts |

The same email does not prove a shared connection or quota pool. The same model name does not prove equivalent tools, privacy behavior, or access through another runtime. Retain native IDs and display-friendly names separately.

## 2. Integration strategy and support levels

Implement versioned runtime adapters around structured protocols. Prefer a supported bidirectional protocol for conversations and approvals; use structured headless subprocesses for suitable jobs. Keep native terminal launch as an explicit escape hatch. Direct inference APIs are a distinct integration class: they supply model responses but need a tool loop, session policy, and execution environment. Do not accidentally expand the initial scope into rebuilding every agent harness.

Every adapter publishes capability values with evidence, runtime version bounds, OS restrictions, and a last-checked date. The [core contract](contracts.md) owns the support enum: `native`, `mapped`, `conditional`, `unsupported`, and `unverified`. Evidence maturity is separate: documented, locally observed, or unverified; experimental maturity also carries version scope. A documented feature is not automatically admitted as tested UCLI support. Support levels are cumulative only after their checks pass:

1. **Discover:** identify executable, version, models/catalog, and supported launch modes.
2. **Run:** normalized streamed output, cancellation, error classification, and effective settings.
3. **Converse:** approval round trips, continue/resume, interruptions, and session identity.
4. **Manage:** isolated accounts, quota telemetry, workflow checkpoints, and recovery.

A provider can ship at Run while another reaches Manage. Advertise the capability difference where it affects the user's action; do not make the user memorize a separate command language.

### Initial evidence matrix

`D` = official documentation fetched; `L` = observed on this Windows machine; `U` = not established. Documentation is not an executed UCLI integration test.

| Runtime | Structured path | Model/effort evidence | Account and quota evidence | First adapter target |
| --- | --- | --- | --- | --- |
| Codex | D: app-server requests/events | D: `model/list`, supported reasoning options | D: auth and rate-limit methods; U: two-account isolation | Converse, then Manage |
| Claude Code | D: print JSON/streaming and Agent SDK | D: CLI model/effort flags; U: full account-entitled catalog | D: auth status; U: stable subscription quota endpoint and isolated concurrent accounts | Run; API-backed Converse |
| Gemini CLI | D: headless JSON/JSONL; ACP needs a separate spike | D: model flag; U: portable effort mapping | D: cached auth and `/stats model`; U: stable external remaining-quota feed | Run |
| Antigravity `agy` | D: print JSON/JSONL, resume | D: model listing and effort; L: Argon variants listed | D: interactive usage; L: print `/usage` yields structured buckets; U: isolation | Run with experimental quota capability |
| OpenCode | D: HTTP/OpenAPI/SSE server | D: provider/model surfaces; U: account entitlement assurance | D: auth interfaces; U: isolated credentials and generalized quota | Converse spike |
| Meta Muse Code | D: versioned `serve`/`schema` protocol; L: launcher present | U: installed model/effort schema | U: isolated accounts and machine quota | Discover, protocol spike |
| Kimi Code | D: ACP and print stream JSON | D: configured model selection; U: effort and quota | D: native device login; U: isolation | Discover, ACP spike |
| Qwen Code | D: REST/ACP/headless integration paths | U: version-specific model/effort behavior | U: isolation and quota | Discover, ACP spike |
| Local backends | D: Ollama compatible API subset | D: compatibility varies by model | U: resource scheduling; no assumption of cloud subscription quota | Separate inference adapter |

For all rows, UCLI account isolation and cross-platform operation remain untested. Local PATH lookup resolved Codex, Claude, agy, and Muse; other names were unresolved in this shell, which does not establish absence from the computer. Sources and observation details are in the ledger.

### Choosing the second initial adapter

For the owner's local-first milestone, conditionally pair Codex with installed Antigravity: model/effort controls, structured print, resume and locally observed quota make it relevant to daily use. Admit only the Run tier until the spike proves permission enforcement, terminal outcomes, cancellation/resume and account binding. A useful local adapter does not establish authorization to offer provider subscription login inside a distributed product.

If Antigravity cannot support the required managed session or approval behavior, evaluate Muse's documented `serve`/`schema` interface next. For a publicly distributed API-authorized path, Claude Agent SDK with supported API authentication is a strong second-adapter candidate; integrated claude.ai login remains separately gated. This is a conditional development recommendation, not a claim that any of these adapters has passed admission. The per-runtime sources below establish the candidate interfaces.

## 3. Per-runtime implementation briefs

### Codex

Use app-server over child-process stdio initially. Generate schemas from the installed executable, negotiate initialization, normalize streamed events, and retain native thread IDs. Discover reasoning choices dynamically. Rate-limit snapshots can contain several buckets and reset timestamps; consume updates rather than polling continuously. [App-server documentation](https://learn.chatgpt.com/docs/app-server)

Separate native Codex authentication from the newer Sign in with ChatGPT open-source-client route. The latter documents per-account registration, host identity, PKCE and token validation, plus a distinct app-server provider configuration. It is a useful future UCLI login route, subject to its preview constraints. Its catalog can include models without proving entitlement; only a successful turn establishes access for that request. [Registration](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [app-server integration](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server), [preview limits](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)

Native credentials may live under `CODEX_HOME` or an OS credential store. A different directory alone is insufficient proof of isolation. Test the selected storage strategy, refresh ownership, and native client coexistence. [Authentication](https://learn.chatgpt.com/docs/auth)

### Claude Code

Begin with a subprocess adapter; add the Agent SDK when its richer interaction contract removes concrete limitations. The SDK provides the Claude Code loop, while the ordinary client SDK supplies API access. CLI flags include model/effort selection and structured auth status. [Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview), [CLI reference](https://code.claude.com/docs/en/cli-reference)

There is an important scope distinction: the Agent SDK overview requires prior approval for third-party products offering claude.ai login or rate limits. An official support article updated October 7, 2026 also says SDK, `claude -p`, and third-party usage can still draw subscription limits. Neither statement establishes approval for UCLI to distribute its own subscription-login experience. Keep local native invocation research separate from a public integrated login offer; ship the supported API connection path while resolving that gate. [SDK policy statement](https://code.claude.com/docs/en/agent-sdk/overview), [subscription usage update](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan)

Do not infer remaining subscription allowance from session token counts. Inspect configuration-directory support, keychain naming, daemon sharing, and auth precedence before concurrent account certification.

### Gemini CLI and Antigravity

Treat these as independent runtimes even when the same Google identity can authorize both. Gemini headless mode exposes JSON/JSONL, usage statistics, and terminal results; its authentication method affects quota and billing. `/stats model` exposes quota information in the CLI, but that does not establish a stable external telemetry API. Its own fallback can also change the effective model, including utility calls. [Headless](https://geminicli.com/docs/cli/headless/), [authentication](https://geminicli.com/docs/get-started/authentication/), [commands](https://geminicli.com/docs/reference/commands/), [fallback behavior](https://geminicli.com/docs/core/)

Antigravity documents model discovery, effort, structured print output, and conversation resumption. Its documented usage panel refreshes backend quota. Locally, the planning session also observed `/usage` through print mode returning structured quota groups with weekly/five-hour windows, fractions remaining, and reset times, without an inference turn. This is promising evidence, not a published stability guarantee. Gate the parser by tested versions and retain sanitized fixtures. [Headless](https://antigravity.google/docs/cli/headless/), [usage](https://antigravity.google/docs/cli/commands/usage)

Antigravity exposes an overage-credit preference. UCLI's budget policy must detect and enforce the chosen billing behavior before launch; automatic account fallback must not silently enable credits. Validate behavior in a UCLI-owned configuration or report that it cannot be enforced. [Credit controls](https://antigravity.google/docs/cli/credits)

### OpenCode and expansion adapters

OpenCode's server provides an OpenAPI specification, sessions, provider APIs and SSE events; its CLI documents credential-file and environment loading. Use a UCLI-owned authenticated loopback instance, never attach blindly to an arbitrary user's server. Test per-instance credential isolation because changing shared provider credentials can affect other work. Its broad provider list does not certify every provider's login or subscription eligibility. [Server](https://opencode.ai/docs/server/), [CLI](https://opencode.ai/docs/cli/)

Muse's documented versioned session protocol makes it a credible independent adapter candidate. Workflow rollout and some platform capabilities vary; Windows session messaging is documented as unavailable. UCLI workflows should own portable coordination instead of depending on Muse's internal workflow engine. [Muse Code](https://dev.meta.ai/docs/muse-code)

Kimi requires product-generation detection: current Kimi Code documentation offers ACP and stream JSON, while earlier kimi-cli documentation describes Wire mode. Do not mix schemas. The current print mode's automatic permission behavior means ACP is the first candidate for interactive approval parity. [Current command reference](https://www.kimi.com/code/docs/en/kimi-code-cli/reference/kimi-command.html), [earlier Wire protocol](https://moonshotai.github.io/kimi-cli/en/customization/wire-mode.html)

Qwen's official REST guide distinguishes serve, headless and ACP paths. Investigate a reusable ACP transport, but keep auth, quota, effort and tool-policy semantics inside the runtime adapter. MCP is a tool integration mechanism, not a substitute for the agent-session contract. [Qwen integration guide](https://qwenlm.github.io/qwen-code-docs/en/developers/rest-api-integration/)

For Ollama and later local servers, probe actual tools, context, cancellation and thinking controls. API compatibility is a subset, not a guarantee of complete behavioral compatibility. Schedule memory/concurrency separately from cloud usage pools; identify whether a nominally local server forwards to cloud. [Ollama compatibility](https://docs.ollama.com/api/openai-compatibility)

## 4. Adapter contract and ownership

Freeze the logical contract before parallel adapter implementation; transport details remain local. Proposed operations are `probe`, `catalog`, `inspectConnection`, `capabilities`, `prepare`, `start`, `send`, `respondApproval`, `cancel`, `resume`, `usage`, and `close`. Unsupported optional operations return a typed result. No provider-specific branch belongs in the scheduler merely to parse output.

`prepare` takes a resolved route and produces the effective execution plan without sending the task. Include executable identity, exact settings, billing mode, credential reference, workspace, account-isolation class, and permission policy. It must reject conflicting environment credentials and unsupported explicit settings. Secrets never appear in command arguments or the displayed plan.

Events need attempt/session IDs, sequence, timestamp, event kind, normalized payload, optional redacted native details, and schema version. Cover text, tool lifecycle, approvals, user questions, usage, effective-model changes, checkpoint, and terminal outcome. A child exiting zero is insufficient if the protocol reports waiting, failure, or incomplete work. Unknown event types remain inspectable; malformed required state fails visibly.

Separate requested model/effort from effective model/effort and native utility/subagent usage. A route that cannot prevent fallback of the pinned primary model is ineligible for that strict request; show a compatible alternative rather than weakening the pin after a notice. Disclose utility/subagent model use separately: a primary-model pin does not implicitly constrain every internal call. If the user requires all inference to stay on a model, runtime or provider, require that broader enforceable capability. Provider-specific options live in a namespaced escape hatch and are validated by their adapter; they do not expand the common flag set by default.

## 5. Accounts that coexist reliably

A connection records an immutable internal ID, provider subject/workspace when available, auth mechanism, credential handle, runtime attachment, policy, and verification timestamp. Labels such as personal/work are editable. Authentication state is `unknown`, `ready`, `refreshing`, `reauth_required`, `revoked`, or `disabled`; do not conflate authentication failure with exhausted usage.

Classify isolation as `process_isolated`, `serialized`, `native_only`, or `unverified`. Certification must examine config, data, cache, OS credential-store keys, refresh tokens, sockets/daemons, inherited environment, and background children. Setting a fake HOME can change unrelated tool behavior; use explicit supported runtime overrides before considering broader environment virtualization. UCLI must never rotate global native login files while other agents run.

An explicit account pin requires evidence that the launch is bound to that isolated connection, including conflicting credential-precedence checks. An ambient native login does not satisfy it. Where a runtime cannot verify identity or establish a trustworthy isolated credential binding, mark that account capability unverified/native-only and exclude it from automatic multi-account routing. Do not infer account identity from the display label or the fact that a prompt succeeded.

Keep existing sessions pinned to their connection. Switching the active account changes future runs; moving live work creates a checkpoint and new attempt. Serialize refresh by credential identity with a cross-process lease and atomic replacement. A stale refresher must not overwrite a newer token. Isolate records by workspace/issued client where required, and ensure logout semantics are explicit: removing a UCLI link differs from revoking provider credentials.

Use OS-protected credential storage through a small platform abstraction. Plan Windows, macOS, Linux desktop and headless Linux separately; absence of a Linux secret service must produce an explained option, not silent plaintext fallback. Spawn only allowlisted inherited environment plus connection-specific values, and redact logs/diagnostic bundles. Validate account identity after login before replacing any active record.

Account cycling is scheduler selection among user-enabled, eligible connections within their allowed scopes. Respect project restrictions and provider account rules. A work connection cannot receive personal work merely because it has more allowance. All-account exhaustion yields a queue-until-reset choice, eligible alternative, or clear stop. No purchase, reset-credit consumption, paid overage, or account creation is an implicit recovery action.

## 6. Usage telemetry and scheduling behavior

Store observations separately from reservations. A quota observation contains provider/connection, bucket ID, affected models or unknown mapping, window, units, reported remaining/used values, reset timestamp, observed timestamp, source, confidence, and freshness deadline. Sources are official structured, version-observed structured, estimated, manual, and unknown. Never invent a percentage for missing telemetry.

Pools may be shared by models, runtimes, workspaces or accounts. Explicit shared-pool IDs prevent double counting. If mapping is unknown, routing must state uncertainty rather than assume every model receives an independent balance. Token counts, dollars, percentage allowances and request caps are different units; do not sum them. Remaining work is an estimate with uncertainty, not a promise derived from a percentage.

Maintain reservations for pending/running work to reduce oversubscription, then reconcile with observations. Reservations are local scheduling estimates, not provider guarantees; native clients and other devices can consume usage concurrently. Cancellation, a crashed child, or an expired lease does not prove external work stopped. Retain unresolved capacity as uncertain until runtime/provider state is reconciled; release only work known not to have started or confirmed terminal, with idempotent accounting. If upstream attribution remains unknown, preserve conservative uncertainty rather than refunding estimated capacity automatically. Multi-device reservations wait until authenticated sync and conflict handling exist.

Refresh in the background after connection changes, relevant completions and rate-limit signals; coalesce requests and use bounded concurrency. Status screens show cached data immediately with its age. Route decisions use an allowed staleness policy; a stale snapshot never freezes the CLI while every provider is polled. Use wall-clock UTC for displayed resets and monotonic durations for backoff; handle clock changes and provider/server skew.

On 429, distinguish short-window throttling, exhausted allowance, capacity pressure and unknown failure when evidence permits. Honor server retry delay, cap retries, add jitter, and open a temporary circuit per affected pool. An unknown 429 is not proof that an account is exhausted. Avoid competing retry loops: the adapter reports when the native runtime is already retrying.

Do not replay a whole agent turn after an ambiguous disconnection: tools may already have changed files or external systems. Reconnect/resume when supported; otherwise inspect durable checkpoints, classify the attempt as interrupted/uncertain, and offer a context handoff. Preserve the task, draft and outputs through account recovery.

## 7. GitHub identity and optional sync

GitHub sign-in identifies the UCLI user. Individual provider connections retain their own authorization, workspace and billing identities. Local operation should work before GitHub login; sync adds device registrations, aliases, presets and workflow definitions. GitHub documents a device authorization flow suitable for terminal sign-in. [GitHub OAuth](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps)

Use minimal identity permissions and immutable GitHub user ID for association. Issue UCLI device sessions rather than sending the GitHub token to AI providers. Handle device-flow denial, expiration and polling delay, plus account unlink and device revocation. Keep synced metadata separate from local credentials. A newly linked device shows provider connections as needing local authorization until their actual access is verified. Syncing provider tokens, repository content or transcripts is a later explicit feature with separate authorization and recovery design.

## 8. Parallel implementation spikes and acceptance

| Spike / independent owner | Deliverable | Acceptance evidence |
| --- | --- | --- |
| AD-01 contract | Schemas, capability registry and fake adapter | Unknown capabilities, malformed event, duplicate event and terminal mismatch fixtures fail or recover as specified |
| AD-02 Codex | Versioned stdio adapter | Initialize/catalog; approved turn; denial; cancel; restart/resume; no global auth mutation |
| AD-03 agy | JSONL adapter and experimental usage reader | Real event fixture, non-inference usage response, schema drift fixture, interrupted print result, explicit overage policy |
| AD-04 Claude | Structured subprocess, API connection path | Effort honored/rejected, permission round trip or honest limitation, auth identity, failure outcome; product-login gate retained |
| AD-05 account isolation | Credential broker and per-runtime certification | Two test connections active concurrently; ambient credential conflict rejected; forced refresh collision; logout one leaves other intact; native client unchanged on each target OS |
| AD-06 quota | Observation normalization and reservations | Shared buckets; unknown mapping; stale/absent reset; clock jump; crashed/canceled external work retains uncertain reservation; native external consumption; all exhausted; retry storm bounded |
| AD-07 expansion | OpenCode + one ACP transport candidate | Third adapter integrates without scheduler provider branches; version/capability mismatch is understandable |
| AD-08 identity | Optional GitHub/device metadata sync | Denial/expiry; offline local launch; device revocation; conflicting settings; zero provider secrets in sync payload |

Run protocol fixtures offline in routine CI; run live smoke tests only with dedicated, authorized test connections and explicit usage budgets. A fake-provider pass proves the contract, not account isolation or provider compatibility. Test the actual consumer path on Windows, macOS and Linux, including child process cleanup and terminal interruption. Keep sanitized receipts with exact runtime version, OS, adapter version, effective configuration, exit/result state and observed side effects.

For performance, measure direct native launch versus UCLI on the same machine, runtime and fixture. Record cold/warm process start, first visible feedback, first event, streaming overhead, cancellation latency, and idle memory. Compare incremental UCLI overhead separately from provider inference latency. Avoid launching every runtime on startup; lazily start the selected adapter, cache discovery, and recycle only instances whose credential/session isolation is proven.
