# Native protocol profiles: 2026-10-22

This review adds exact installed Windows amd64 identities to the native execution
admission table. It does not change the download catalog, account authorization,
permission mapping, model selection, or terminal-outcome rules. An admitted file
still needs fresh account, capability and effective-configuration checks.

| Runtime | Version | SHA-256 |
| --- | --- | --- |
| Codex | 0.163.0 | `2692c8c7bb2cf8b0aefe638741fc90236f4b824c59578ce84994f9de58200993` |
| Claude Code | 2.1.304 | `019e5174d5d954585edb1a946fe280e44a973050b4897034adf0e16306f85fdd` |

Previously admitted Codex 0.162.1 and Claude 2.1.299 identities remain available.
The Claude downloadable catalog remains 2.1.299. A newly installed file does not
inherit another build's review. Discovery evidence does not replace an execution
pin; explicit connection relinking remains a separate application operation.

## Codex evidence and bounded change

The installed 0.163.0 executable generated 452 experimental JSON schema files
without a user turn, using an isolated empty native home. The prior 0.162.1
schema had 447. Structural comparison excluded descriptions and schema titles.
Account, capacity, model-list, effective-config, thread-start and turn-response
shapes used by this adapter are unchanged. `TurnStartParams` only adds optional
`retain` to nested `TurnToolOutput`; UCLI's text input does not use that variant.
The reviewed [0.163.0 protocol schema](https://github.com/openai/codex/tree/rust-v0.163.0/codex-rs/app-server-protocol/schema/json/v2)
is the compatibility reference, rather than a claim that all new features work.

The [feature definitions](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/features/src/lib.rs)
add `async_compaction`, disabled by default. UCLI explicitly disables it and
requires literal false in effective configuration for the new profile, preventing
an inherited setting from starting background model compaction. The old profile
receives no unknown feature flag. The reviewed provider implementation is byte
identical across the two tags: SHA-256
`93ce6cd0a65477dff89545b8b48853455da0654c4bcc5a1092596d88d9dfe25c`.
The pinned ChatGPT backend and API-key refusal are unchanged.

The new `thread/readState/changed` notification concerns durable read receipts.
Its [publisher](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/app-server/src/request_processors/thread_read_state.rs)
returns before publication when `config.ephemeral` is true; UCLI requests an
ephemeral thread and never calls read-state update. No new execution-message
exception was added. Source SHA-256:
`21084850835d150596ef4652c267161d75bc2ed1d825714f19393a9d060f7a2e`.

## Claude evidence and bounded change

The official [2.1.304 manifest](https://downloads.claude.ai/claude-code-releases/2.1.304/manifest.json)
was verified with Anthropic's [documented signing key](https://code.claude.com/docs/en/setup),
fingerprint `31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE`, in an isolated keyring.
Its Windows x64 identity matches the installed 293,227,168-byte binary; Windows
Authenticode was valid. No native binary was downloaded.

Hidden isolated `--safe-mode --help` checks on both versions confirm the managed
flags remain present. Installed program inspection found the same initialization
identity/model/permission envelope and the reviewed `system/ui_invalidate`
schema. Execution now validates that known render-cache notification with the
same bounded, session-bound helper used by metadata discovery. It grants no tool
authority, emits no private payload and cannot establish completion. Inspection
reports the version matched by the exact file profile, including older files.
No proprietary implementation is copied into this repository.

## Verification limits

Focused offline package tests and vet cover retained/current profile matching,
wrong hash/version/platform refusal, current-only configuration enforcement and
owned child fixtures with valid, mismatched, malformed and EOF-only Claude UI
metadata. These are synthetic protocol checks, not successful model requests.
Current-device native inference and packaged release verification are separate
release checks. Non-Windows execution has not been admitted by this update.
