# Native Claude adapter

This experimental adapter references an existing Claude subscription login. Native
tokens stay with Claude. `Inspect` verifies the reviewed executable digest, runs
bounded `auth status`, and compares native status with the account UUID/email in
native account metadata. Only SHA256(`claude` + NUL + UUID) leaves that comparison.
The native default configuration home is left implicit: setting
`CLAUDE_CONFIG_DIR` even to its default changes Claude's global metadata lookup.
Separate credential isolation is unverified and refused. An identity change
requires explicit relinking; it never silently switches accounts.

`Inspection.Models` contains raw aliases `default`, `sonnet`, `opus`; the app adds
`claude/`. The default alias omits `--model`. This local alias catalog is not a
claim of entitlement, exact model version, remaining quota, or reset timers.
An explicit Sonnet/Opus alias must match the native initialization's model family;
a subsequent reported model change is rejected. `Outcome.NativeModel` carries
the observed identifier separately from the accepted alias. Signed-out status
(`loggedIn:false`, `authMethod:none`, including native exit 1) returns an empty
identity/catalog and explicit `AuthMode:none`; unknown/API-key modes are refused.

The selected executable is trusted native code, not a security sandbox. Read-only
uses an empty temporary working directory and **no tools**. Explicit `sandbox`
means full local computer access: the selected workspace is the working directory,
native default tools are enabled, and Claude uses `bypassPermissions`. It is not an
OS sandbox and does not authorize purchases or messages by itself. The native
tool path is covered with synthetic tool events; a fixture cannot prove live
filesystem/tool permission behavior.

Both paths use `--safe-mode`, empty setting sources, hooks disabled, no MCP
servers/connectors, no slash commands, no Chrome, and no session persistence.
Managed organizational policy still applies. `--bare` is deliberately absent:
it disables OAuth/keychain lookup and requires API-key authentication. The owned
child receives an explicit environment with no API key, auth-token, provider,
endpoint or proxy overrides. Native auth may refresh its own credentials; UCLI
does not copy or edit them, enable extra usage, or change billing settings.

Each request allocates and durably records an ephemeral native session UUID before
launch. That is a proposed session identity, **not a provider acceptance receipt**.
Claude's subsequent initialization must confirm the same UUID and requested
permission/tool envelope. A matching final result and agreeing process exit are
required for success. Once stdin may have been written, killed processes, missing
results and protocol failures remain unknown. Follow-ups start fresh native
sessions; no native history/resume guarantee is made. A native account can still
be changed externally between preflight and dispatch; the current native CLI
protocol does not supply an atomic account lock or account identity in its init.

The driver bounds duration (10 minutes), JSON frames (1 MiB), all native output
(16 MiB), and visible text (4 MiB). Read-only permits one native agent turn;
full-access permits at most 30. Local pipe closure owns the child tree but cannot
prove a remote request stopped. Native diagnostics, private reasoning and raw
tool results are discarded. Assistant text is delivered to the app, whose
consumer must retain the existing bounded delivery contract. No new provider SDK
or UI dependency is introduced.
Marked synthetic/error assistant messages and final native error prose are
classified in memory into safe login, quota, billing, model, request or generic
failure categories. Their raw diagnostic text is not emitted. Unconfirmed
transport outcomes remain unknown even when partial output was received.

## Verification

Run normal checks with the project's pinned Go toolchain:

```console
go test -p=1 ./internal/adapters/claude
go vet -p=1 ./internal/adapters/claude
```

The tests launch the actual test executable through `internal/process`, consume
stdin EOF and structured stdout, and fail on broken admission/provenance,
configuration, stream and interruption boundaries. They use synthetic identities
and cannot spend model quota. macOS process execution remains explicitly skipped
until the shared launcher is verified on that host.

`tools/probe_claude_live.py` defaults to metadata-only inspection. Its explicit
`--live --go PATH` route runs the public adapter once, with tools disabled, effort
low, the native default model, and an exact `UCLI_LIVE_OK` response goal. A durable
exclusive marker prevents accidental repeat at the same evidence path. Its
receipt contains safe codes/counts and hashes, never raw auth output, account
addresses, native IDs, private prompts or diagnostics. A failed or interrupted
live request is not automatically repeated.

Reviewed official references: [CLI reference](https://code.claude.com/docs/en/cli-reference),
[headless operation](https://code.claude.com/docs/en/headless),
[settings](https://code.claude.com/docs/en/settings),
[environment](https://code.claude.com/docs/en/env-vars), and
[subscription billing](https://support.claude.com/en/articles/11145838-use-claude-code-with-your-pro-or-max-plan).
The reviewed native version is 2.1.299. Actual live receipts remain under `.build`;
receipt scope, OS support and fixture coverage must be reported separately.

The first authorized Windows native smoke test reached a matching failed native
result in 3.7 seconds after confirming the no-tools envelope and subscription
identity. It did **not** produce `UCLI_LIVE_OK`. That original receipt predates
specific failure classification, so its cause is unclassified; no inference was
repeated by this package worker. This is failure-path evidence, not successful
inference or full-access tool admission.

## Coordinated continuation result

Installed source `24ae3fdf9f6b` includes safe diagnostic preservation for failed and
unknown outcomes, verified through actual-child regressions. On 2026-10-18, the
verification machine's native status was signed-out. Only the initial failed Claude
turn was requested; no later
inference or browser consent completion was attempted. See the
[coordinated evidence](../../../docs/verification.md).
