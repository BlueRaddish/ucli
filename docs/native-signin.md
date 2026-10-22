# Native sign-in and full local access

This experimental slice connects native accounts through one UCLI vocabulary.
Credentials remain with the native clients. A connection is a pinned executable,
native home and opaque account fingerprint; it is not a copied credential store.
Native execution evidence currently covers Windows x64 with Codex 0.162.1 and
Claude Code 2.1.299. Other native hosts remain unverified; Codex explicitly refuses
other hosts and Darwin owned-process launch is disabled. See the final verification
receipt for live results. The [installed continuation](verification.md)
passed account linking and packaged checks, but has no confirmed successful native
AI response. Its final Codex sandbox read ended unknown on an unsupported execution
notification. On the verification machine, Claude was signed out when checked on
2026-10-18; check your own native status with `ucli accounts status`. Native execution
remains experimental.

## Everyday loop

```console
ucli login
ucli login codex --label personal
ucli login claude
ucli login github
ucli accounts list
ucli models
ucli run --account personal --model codex/gpt-6-luna --effort low "Explain this idea"
ucli run --account personal --permission sandbox "Inspect this project's failing test"
```

Bare `login` opens a provider selector in a terminal. In the prompt, Tab → Connect
account or `/login` opens the same path while preserving composition and settings.
Checking existing sign-in shows progress immediately. An authenticated account is
reused. A supported signed-out account can start its native browser/device flow;
the link and code stay visible together. Escape cancels the connection operation
and returns to the draft. Ctrl+C retains the session's ordinary exit behavior.
Cleanup must finish before another operation. Native authentication may already
have completed when cancellation arrives; `accounts status` checks current truth.

Lists and selectors use cached metadata without launching a provider or polling
authentication. `accounts status [ID]` explicitly refreshes authentication and
model metadata without inference. Status checks can perform native credential
refresh and can take several seconds. Unknown status refuses automatic replacement
of an existing identity. A changed account fingerprint clears its cached models
and blocks the old connection until it is explicitly removed and connected again.

When a submitted prompt encounters an expired catalog, UCLI makes one bounded
metadata-refresh pass and replans before dispatch. It does not retry AI work or
change model, account, effort or permission pins. Browsing and route explanations
stay offline. Ordinary status never replaces an execution fingerprint. After a
reviewed native CLI update, explicitly reuse the account in `/login` or run
`ucli login PROVIDER --no-input`; fresh same-account checks can retain the existing
connection ID while adopting the reviewed build. Unsupported builds and changed
accounts remain blocked without replacing their prior references.

`login PROVIDER --no-input` links an authenticated account and refuses unfinished
interactive sign-in. `accounts add PROVIDER` records and checks the connection
without starting sign-in. `logout ID` and `accounts remove ID` unlink UCLI metadata;
native credentials and saved tasks are retained. Device links and codes are
ephemeral UI output, never task history or diagnostic receipts.

## Multiple accounts and GitHub

Use `--label NAME --native-home PATH` to reference another existing native profile.
`login codex --separate --label work` creates a new scoped native home only during
explicit login, forcing file-based Codex auth to avoid a shared-keyring fallback.
Separate Claude, GitHub and Antigravity credential scopes remain unsupported.
Existing references do not establish cross-account storage isolation.

GitHub currently connects the local GitHub CLI identity through `gh`. Hosted UCLI
identity, GitHub OAuth application registration and synchronization remain future
work. Antigravity discovery is available, but its authentication remains unknown
because no dedicated safe status/login interface has been admitted. It does not
start an inference-capable TUI as a status check.

## Permissions and routing

`--permission sandbox` explicitly authorizes **full local computer access** through
the selected native agent's tools. The permission picker displays that scope.
It maps to the native broad-access mode; it provides no additional OS isolation.
Task instructions still determine authorized remote actions and spending.

Read-only remains the default. Its current native implementation disables tools
and accepts text input only. An agent cannot inspect project files in that mode;
choose sandbox explicitly when local files or commands are needed. Workspace-write
and unrestricted are named contracts but have no admitted native mappings here;
unsupported intent is refused without widening permission.

Explicit account, model and effort pins are preserved. Native account identity,
executable bytes, model and effective configuration are checked again before
dispatch. Requested aliases and native reported model IDs are retained separately.
API-key fallback is disabled. Auto currently chooses a supported default model
and the lowest supported effort; it is not a background decision agent.

Remaining native usage is unknown to the shared resolver. Codex checks its current
native rate-limit snapshot and explicit ordinary-usage permission before dispatch;
missing/false permission blocks with a capacity error. This is not a reservation against
other clients. Acceptance atomically permits only one running or uncertain native
attempt per runtime per UCLI home, across its accounts. Other UCLI homes and native
clients are outside this conservative guard. Unknown work is never automatically
replayed or released because a timer expires. Native unknown-outcome reconciliation
is not implemented; such an attempt retains its local guard for inspection.

Failures retain their safe category in the durable terminal receipt. Reusing the
same command ID returns that receipt without submitting work again. Missing native
terminal proof remains unknown even after the owned local child is stopped.
Follow-ups retain the UCLI task ID, workspace, settings and saved history, but
send only the new prompt to a fresh native session. Prior prompts and results are
not supplied automatically; cross-agent handoff and native session resumption
are not implemented.

## Checks and remaining evidence

The repeatable route is in [verification](verification.md). Fixture checks cover
sign-in reuse, explicit signed-out/unknown states, wrong account refusal, bounded
output, cancellation, URL/code continuity, saved-draft return and error replay.
Packaged native checks distinguish zero-turn account inspection from real
inference and require an explicit one-request opt-in for a synthetic sandbox file
read. Neither fixtures nor a catalog prove live entitlement, two-account isolation,
remote cancellation, hosted identity or production admission.
