# UCLI provider research ledger

Checked 2026-10-17. These are primary sources fetched during planning, not claims of completed implementation. Provider behavior is versioned and changeable; refresh the relevant entry before its adapter ships. No current pricing amounts are incorporated.

## Official sources and precise scope

| ID | Source | Supports | Does not establish |
| --- | --- | --- | --- |
| OAI-01 | [Codex app-server](https://learn.chatgpt.com/docs/app-server) | Structured agent protocol, model/effort discovery, auth and rate-limit snapshots | UCLI compatibility or concurrent account isolation |
| OAI-02 | [Codex authentication](https://learn.chatgpt.com/docs/auth) | Credential storage modes and shared native login considerations | Distinct keyring namespaces for arbitrary UCLI profiles |
| OAI-03 | [Sign in with ChatGPT registration](https://developers.openai.com/siwc/token-sharing-open-source/sign-in) | Open-source-client flow; issued client/account/host identity and validation | UCLI eligibility tested, or GitHub authenticating a provider |
| OAI-04 | [SIWC app-server](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server) | Provider configuration; catalog is not entitlement; token renewal/resume guidance | Universal access to catalog models |
| OAI-05 | [SIWC preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations) | Distinct API constraints for ChatGPT plan usage | Parity with all direct API features |
| CLA-01 | [Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview) | Agent/runtime distinction, supported embedding, third-party login approval statement | UCLI approval to offer claude.ai login |
| CLA-02 | [Claude CLI reference](https://code.claude.com/docs/en/cli-reference) | Print mode, auth status, model/effort/configuration flags | Installed build supporting every documented feature |
| CLA-03 | [Claude plan usage update](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan) | October 7 update permits continued subscription usage for described SDK/print/third-party cases | Blanket permission to distribute a new provider login product |
| GEM-01 | [Gemini headless](https://geminicli.com/docs/cli/headless/) | JSON/JSONL events, statistics and exit codes | Remaining subscription quota equivalent to token usage |
| GEM-02 | [Gemini authentication](https://geminicli.com/docs/get-started/authentication/) | Cached auth and separate supported authentication methods | Cross-runtime credential portability |
| GEM-03 | [Gemini commands](https://geminicli.com/docs/reference/commands/) | `/stats model` quota display | Stable headless quota endpoint |
| GEM-04 | [Gemini core](https://geminicli.com/docs/core/) | Native model and utility fallback behavior | An explicit model flag preventing every fallback |
| AGY-01 | [Antigravity headless](https://antigravity.google/docs/cli/headless/) | Model/effort selection, structured print, resume and terminal states | Stable schema for every slash command in print mode |
| AGY-02 | [Antigravity usage](https://antigravity.google/docs/cli/commands/usage) | Backend-refreshed interactive quota display | Publicly guaranteed external quota contract |
| AGY-03 | [Antigravity credits](https://antigravity.google/docs/cli/credits) | Overage-credit control | UCLI successfully enforcing that control |
| OPC-01 | [OpenCode server](https://opencode.ai/docs/server/) | OpenAPI, sessions, events, provider interfaces and server auth | Every provider's entitlement or safe shared-server account replacement |
| OPC-02 | [OpenCode CLI](https://opencode.ai/docs/cli/) | Native credential file and environment loading | Concurrent independent accounts without isolation tests |
| MUS-01 | [Meta Muse Code](https://dev.meta.ai/docs/muse-code) | Versioned serve/schema protocol and platform/rollout distinctions | Installed protocol conformance, usage API or Linux/Windows feature parity |
| KIM-01 | [Current Kimi command](https://www.kimi.com/code/docs/en/kimi-code-cli/reference/kimi-command.html) | ACP, stream JSON, login and print permission behavior | Older kimi-cli using identical schemas |
| KIM-02 | [Earlier Kimi Wire mode](https://moonshotai.github.io/kimi-cli/en/customization/wire-mode.html) | Older documented integration family | Best path for current Kimi Code |
| QWE-01 | [Qwen REST integration](https://qwenlm.github.io/qwen-code-docs/en/developers/rest-api-integration/) | Separate serve, ACP and headless integration paths | Installed feature availability, quotas or account isolation |
| LOC-01 | [Ollama compatibility](https://docs.ollama.com/api/openai-compatibility) | Defined API subset and model-dependent support | Complete OpenAI behavior or a built-in coding-agent runtime |
| GIT-01 | [GitHub OAuth authorization](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps) | Device authorization option and OAuth lifecycle | GitHub granting access to independent AI subscriptions |

## Local observations

| Receipt | Observation | Limit |
| --- | --- | --- |
| Planning shell `Get-Command` on 2026-10-17 | Resolved `codex.ps1`, `claude.exe`, `agy.exe`, `muse.cmd`; Gemini/OpenCode/Kimi/Qwen did not resolve | PATH visibility only; no login or run checked |
| Read-only Muse launcher inspection | `muse.cmd` delegates to installed `.muse-launcher.ps1`; launcher identifies Meta endpoints and credential-path override | No credential files read; no launcher run; override is not account-isolation proof |
| [agy availability receipt](../verification.md) | Planning lead observed models including Argon and structured print `/usage` groups/buckets; terminal success with zero inference turns | Locally observed surface, not stable documented quota API; snapshot is not future entitlement |

The agy usage observation should become an adapter fixture only after sanitization and version identification. Never commit credentials or full native home directories as evidence. Runtime verification needs an exact executable version/build or hash; model names and a successful metadata command alone are insufficient.

## Research conclusions that remain hypotheses

- Two concurrent accounts can coexist for each runtime: unverified until config, keyring, daemon and token-refresh tests pass per OS.
- Cross-provider effort labels can represent similar user intent: proposed vocabulary, not equal computational budgets.
- Native launch will be the fastest backend for every task: benchmark required; long-lived structured transports may reduce overhead.
- A local quota snapshot can forecast task completion: only an estimate; shared pools and external usage invalidate certainty.
- UCLI can offer every provider's subscription login inside its own account: not established. Implement each supported authorization path separately.
- Antigravity is the best second local adapter, with Muse as an alternate and Claude API as a public-product candidate: a conditional recommendation pending lifecycle/permission/isolation admission, not an executed comparative result.
- A canceled or crashed local process stops upstream work: not established. Reservation recovery must retain uncertainty until terminal state or non-dispatch is confirmed.

## Planning verification boundary

Project-verification discovery ran against the new UCLI directory and reported no established verification route at that time. This research work checked official pages and inspected local launcher metadata; it did not execute inference, alter credentials, install integrations or prove adapter behavior. The spike acceptance table in the provider plan specifies future checks. The project lead owns the final cross-document checks and reusable planning-artifact verification route.
