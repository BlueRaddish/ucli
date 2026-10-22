# Reviewed nonterminal notifications

This boundary is pinned to OpenAI's `rust-v0.162.1` protocol source. It does
not remove the executable/schema/account/configuration admission checks.
Notification payloads are validated and discarded: they are never assistant
output, a permission grant, a tool instruction or proof that a turn finished.

| Method | Handling during an active native turn |
| --- | --- |
| `turn/plan/updated` | Require exact thread/turn IDs; validate bounded step text, optional explanation and `pending`/`inProgress`/`completed` statuses. |
| `turn/moderationMetadata` | Require exact thread/turn IDs and a present JSON value; retain none of its contents. |
| `item/commandExecution/terminalInteraction` | Require sandbox permission, exact thread/turn, a matching active command item and bounded process/input fields. Never send the reported input to any process. |
| `item/fileChange/patchUpdated` | Require sandbox permission, exact thread/turn, a matching active file-change item and bounded patch fields. Never open the reported paths or apply the diff. |
| `item/plan/delta` | Refuse; proposed-plan item streaming is outside the admitted item lifecycle. |
| `item/mcpToolCall/progress` | Refuse; MCP tools remain disabled. |
| `item/autoApprovalReview/started`, `item/autoApprovalReview/completed` | Refuse; no Guardian/automatic approval authority is added. |

Only these eight reviewed literal method names can enter rejection diagnostics.
Unrecognized execution method names and all native payloads remain withheld.
Safe additive fields are ignored within the existing frame, transcript, event,
JSON-depth and duplicate-key bounds. Setup does not admit execution metadata.
An EOF, timeout or local process exit after dispatch remains an unknown outcome
unless a valid matching terminal event was observed. A completed plan step is
not a completed turn.

## Primary source basis

Reviewed on 2026-10-21:

- [Turn plan notification and step enum](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server-protocol/src/protocol/v2/turn.rs)
- [Moderation notification (`serde_json::Value`)](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server-protocol/src/protocol/v2/model.rs)
- [Terminal interaction, patch update and proposed-plan item schemas](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server-protocol/src/protocol/v2/item.rs)
- [Generated patch notification JSON schema, including nullable optional `move_path`](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server-protocol/schema/json/v2/FileChangePatchUpdatedNotification.json)
- [Wire notification method registration](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server-protocol/src/protocol/common.rs)
- [Native event-to-notification identity mapping](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server-protocol/src/protocol/event_mapping.rs)
- [Plan/moderation emitters](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server/src/bespoke_event_handling.rs)

## Offline consuming checks

Run `go test -p=1 ./internal/adapters/codex` and
`go test -p=1 ./internal/app -run TestCodexNotificationOutcomeSurvivesRestartAndReplay`.

The adapter checks launch the test executable as an owned child and use the real
JSONL transport, handshake, native-style turn acceptance and cleanup. They cover
metadata followed by completion/failure/EOF, malformed/foreign/missing identity,
limits, warning ordering, active-tool constraints and safe diagnostics. Darwin's
existing unavailable-process fixture skip still applies; compilation alone is
not host execution evidence.

The App check separately supplies public `Driver` outcomes and verifies durable
receipt/events, restart, same-command replay and retained unknown-work leases.
This is layered coverage, not one App-to-native end-to-end test. No native
provider, real account, model inference or remote cancellation is exercised.
