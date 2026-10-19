# Native connection references

This package records selected native executable/home references, not credentials.
It is called only from an explicit connection or login action. `New`, `List` and
`Get` read local metadata only; `Discover` inspects known installation locations
and hashes native executables without running them. No startup account polling,
inference, token copying, native logout, or credential deletion is implemented.

```go
service, err := connections.New(ucliHome)
found, err := service.Discover(ctx)
connection, err := service.Connect(ctx, connections.ConnectOptions{
    Provider: "codex", Label: "personal", Executable: absoluteExecutable,
    NativeHome: existingNativeHome, HomeMode: "existing",
})
cached, err := service.List(ctx)
connection, err = service.Status(ctx, connection.ID) // explicit native inspection
connection, err = service.Login(ctx, connection.ID, showEphemeralLoginEvent)
err = service.Remove(ctx, connection.ID) // local tombstone only
```

ID or exact label selects a cached connection. One provider/native-home pair can
have one live record per UCLI home; labels are unique and cannot use connection-ID
syntax. Existing-mode defaults use
the provider's usual configuration location or its explicit native environment
override. Windows npm Codex discovery resolves its packaged native binary rather
than running the PowerShell/cmd/JavaScript updating shim. Explicit executable paths
must be regular native files. The selected SHA-256 is checked again before each
native command. A hash is identity/change detection, not publisher attestation.

`HomeMode="separate"` with no path allocates a name below
`UCLI_HOME/native/PROVIDER/` without creating that directory during discovery or
registration. Only explicit supported login creates it. Separate paths outside
that tree are rejected. Codex separate login selects native file-based auth to
avoid implicitly writing shared keyring entries. Separate Claude/GitHub/agy login
remains unavailable until credential-store isolation is established; a path
override alone does not prove it. Existing native profiles remain owned by their
native CLI and are never copied or deleted, including after UCLI removal.

An authenticated existing connection returns from `Login` without changing its
account. Unknown status refuses login instead of replacing an unidentified
existing account. Signed-out records can start supported native device/browser
login. Missing existing-home directories and failed GitHub credential records
remain unknown because neither proves the absence of OS-stored credentials.
The provider may open its browser; UCLI does not open a terminal, manipulate
the clipboard, accept passwords/tokens, or automate consent. Cancellation bounds
the owned child; native authentication may already have completed or changed its
own cache, so refresh status instead of assuming rollback. Cleanup uncertainty is
exit 9 and retains the prior metadata; it cannot become a successful login.

`LoginEvent` URL and device-code values are temporary user-facing credentials.
Consumers must not put them in task history, logs, JSON diagnostic receipts or
durable metadata. Raw child stdout/stderr is bounded in memory and discarded after
parsing. Stdin is closed; UCLI does not forward credentials or terminal input.
Only allowlisted HTTPS provider URLs and bounded device-code patterns are
emitted; native diagnostics and account emails are never forwarded. Callback code
must return promptly; the process context still bounds child lifetime if it blocks.

GitHub uses `gh auth status` and the numeric ID from `gh api user`; it is a native
local GitHub connection, not a hosted UCLI identity or synchronization service.
Native gh owns credential storage and documents a plaintext fallback if its OS
credential store is unavailable. This package does not request insecure storage
or claim stronger guarantees than gh provides. Agy has no reviewed dedicated
noninteractive auth-status/login interface here: discovery/version metadata can
be recorded, but authentication remains unknown and login requires the native UI.
Codex identity inspection uses its adapter's reviewed app-server initialization,
account and catalog path; Claude uses its reviewed status/account-metadata path.
Both return explicit signed-out results separately from unsupported API billing.
Their pinned build/platform restrictions also apply to connection inspection.
Native tools own any credential refresh performed by their status commands.

`IdentityFingerprint(provider,nativeID)` is SHA-256 of the exact provider name,
a NUL separator, and the stable native ID. Adapters use the same formula. This is
a pseudonymous equality check, not an authorization token or anonymization claim.
An identity change preserves the old fingerprint, clears the model catalog,
persists `identity-changed`, and refuses rebinding. Sign-out also preserves the
pin. Removing the local reference and reconnecting creates a fresh connection ID.
Cached authentication/models have an observation time and are not fresh admission
evidence; execution must independently revalidate identity/configuration/permissions.
`Isolation` remains `unverified`, and `ManagedAI` remains false.

Metadata is canonical JSON with a checksum in immutable revision files. New files
are owner-only where supported, flushed, and published by nonclobber hard link.
Malformed filenames, checksums, ambiguous JSON, duplicate live labels/homes and
foreign entries fail visibly. A persistent OS advisory lock serializes mutation
and login and is released on process death; it must never be unlinked. This is a
cooperative same-user design, not defense against an attacker replacing paths
concurrently. Directory power-loss durability is not certified. The deliberate
scan ceiling is 1,024 directory entries/16 MiB. Publication reserves space for its
temporary link and refuses before exceeding the reader's ceiling, preserving
readable prior history. A later metadata-store migration can replace this limit
if connection churn needs more history. Nothing silently truncates it.

## Verification

Use the pinned toolchain/cache recipe in `docs/verification.md`, with jobs 1,
`GOMEMLIMIT=192MiB`, and `GOGC=40`:

```text
go test -p=1 ./internal/connections -count=1 -timeout=60s
go vet -p=1 ./internal/connections
```

Fixtures consume the actual public service, persistence/restart, immutable
publication, competing service locks, cancellation, changed binary/account pins,
metadata damage, secret sentinels and actual owned fixture child processes.
`UCLI_CONNECTIONS_TEST_PROVIDER=github` opts into an existing native read-only
status check; optional `UCLI_CONNECTIONS_TEST_EXECUTABLE` and
`UCLI_CONNECTIONS_TEST_HOME` select explicit native locations. Only coarse state,
version and identity-present/model-count booleans enter test output. No test
initiates real login, account switching, inference or downloads. Darwin native
process tests skip while the shared process layer refuses owned native launch.

Official command references checked 2026-10-18:
[Codex authentication](https://learn.chatgpt.com/docs/auth),
[Claude CLI authentication commands](https://code.claude.com/docs/en/cli-reference),
[GitHub status](https://cli.github.com/manual/gh_auth_status),
[GitHub login](https://cli.github.com/manual/gh_auth_login), and
[Antigravity authentication](https://antigravity.google/docs/install?tab=cli).
Native command availability is version-dependent; these references do not prove
account isolation, inference entitlement or permission enforcement.
