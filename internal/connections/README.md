# Native connection references

This package records selected native executable/home references, not credentials.
It serves explicit account actions and bounded stale-catalog refresh on submission.
`New`, `List` and
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

GitHub discovery checks `gh auth status` and reads the numeric user ID from `gh
api user`, treating the result as a local native connection rather than a hosted
UCLI identity or sync service. `gh` owns credential storage and may fall back to
plaintext when an OS store is unavailable; UCLI never requests insecure storage.
For `agy`, no reviewed noninteractive auth or login path exists, so authentication
remains unknown and requires the native UI. Codex account and catalog inspection
remains pinned to reviewed builds, whereas Claude relies on native status and
account metadata paired with a zero-turn catalog probe.

An explicit status check may refresh Claude or `agy` discovery from a changed
executable while preserving the original execution fingerprint and marking
execution compatibility as unknown. `CatalogSHA256` records the discovery source
separately so that discovery refreshes cannot promote execution compatibility or
imply successful inference. Native tools remain responsible for credential
refresh. Signed-out state is tracked distinctly from API billing and failed
metadata refreshes, and a failed refresh retains the last-good catalog
observation.

Explicit account reuse in `/login` can recheck a reviewed native CLI update without removing the connection. When the current executable and authentication are reviewed, stable, and match the bound account, the connection ID, label, and native profile remain fixed. A signed-out, genuinely unbound record can acquire its first verified account through this explicit action, while unknown builds, identity changes, or failed checks leave the existing binding untouched. Routine status and catalog refreshes never silently replace the execution fingerprint.

`IdentityFingerprint(provider,nativeID)` is SHA-256 of the exact provider name,
a NUL separator, and the stable native ID. Adapters use the same formula. This is
a pseudonymous equality check, not an authorization token or anonymization claim.
An identity change preserves the old fingerprint, clears the model catalog,
persists `identity-changed`, and refuses rebinding. Sign-out also preserves the
pin. Removing the local reference and reconnecting creates a fresh connection ID.
Cached authentication/models have an observation time and are not fresh admission
evidence; execution must independently revalidate identity/configuration/permissions.
`Isolation` remains `unverified`, and `ManagedAI` remains false.

Canonical checksummed immutable metadata writes format 2 to record execution
compatibility state and the discovery-source SHA, along with optional resolved
IDs, effort state, and unavailable state. The reader still accepts canonical
format 1 records without rewriting them during browsing, and legacy zero-valued
fields preserve their canonical bytes. Older readers refuse format 2 records
rather than attempting to execute extended records they do not understand.

This format update requires no SQLite schema migration. Routine status and
catalog refresh keep the original execution fingerprint while tracking the
discovery source independently. Explicit account reuse follows the reviewed
update checks described above.
Cached metadata never serves as fresh admission proof or proof of inference;
execution independently validates identity, configuration, and permissions, and
not all fresh checks run prior to durable acceptance.

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
