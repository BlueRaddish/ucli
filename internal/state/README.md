# Local state (experimental schemas 1 and 2)

`Open(home)` owns `<home>/state/state.db` and SQLite's WAL/SHM sidecars.
This directory contains task input and results; it is not a credential store.
The caller supplies the resolved workspace identity and selects the data home.
The state layer never inspects native agent state. Explicit backups use SQLite's
online backup API; live database/WAL/SHM files are never copied directly.

The dedicated state directory is protected with POSIX owner modes or a protected
Windows DACL for the current user. Existing state-sidecar permissions are tightened
as well. Explicit network/URI homes, state-directory/file symlinks and Windows
remote drives are refused. POSIX remote mounts cannot be reliably distinguished
here; configure a local disk. This is not protection against a hostile process
already running as the same OS user or against an administrator.

SQLite is pinned by the root module. Open checks its WAL fix version, reads the
database header/schema, refuses foreign/newer schemas, and enables WAL with FULL
sync, foreign-key enforcement and a two-second busy timeout. Each read/write
transaction checks the schema epoch. This binary supports schemas 1 and 2;
unsupported newer epochs are refused. New homes deliberately remain schema 1,
and existing homes remain at their existing epoch until explicitly migrated.
There is no automatic upgrade, downgrade, stale-owner takeover or credential
operation. An already-open older schema-1 writer refuses epoch 2 at its next
transaction; its one-time startup check is not the fence.

Normal startup does **not** certify the whole database. It does not run
`integrity_check` or `quick_check`, whose work grows with stored data. Existing
per-record decoding and size checks still apply, and SQLite may detect page damage
when affected data is accessed. Corruption in unread data can remain undetected
until that access or an explicit audit.

`CheckIntegrity(ctx)` runs the real full `PRAGMA integrity_check` inside a read
transaction with the same schema fence. It is the explicit maintenance/doctor
audit and can take longer as the database grows; the caller controls cancellation.
It accepts only SQLite's successful result and otherwise returns a sanitized
state error without exposing row contents or raw engine diagnostics. It makes no
repairs. This checks SQLite structure, indexes and CHECK/uniqueness constraints.
For schema 2 it additionally validates task metadata and compares every helper
record with its original task-body projection, including missing/orphan rows and
the exact maintenance trigger/view definitions. It does not certify every
attempt/event JSON semantic or check all foreign-key violations.

## Explicit migration and recovery evidence

`SchemaVersion(ctx) (int, error)` inspects the supported, fenced epoch.
`Migrate(ctx, newBackupPath) (MigrationReceipt, error)` advances 1 to 2 only.
The backup path must be a new absolute local file outside the live state
directory, with an existing parent. Its `newBackupPath + ".receipt.json"` sidecar
must also be absent. Neither file is overwritten. Migration is not invoked by
Open or ordinary task commands.

Migration holds `BEGIN IMMEDIATE` from the idle check through backup, schema
creation, backfill, consistency audit and epoch commit. Running/unknown attempts
or any leases refuse it; their age and process liveness never release ownership.
An existing writer can finish before this barrier is acquired, or block migration
with a bounded busy error. Writers arriving during it wait/refuse; once it commits,
an older schema-1 writer's in-transaction epoch check prevents further mutation.
WAL readers may finish their earlier read snapshots without becoming writers.

A separate source read connection takes the pre-migration SQLite snapshot while
the writer barrier is held and before the migration performs any source write.
It does not call Open or start a second writer transaction. The snapshot receives
the existing full SQLite audit, standalone-file normalization, SHA-256 hash, file
sync and no-overwrite publication. The sidecar records its exact pre-migration
schema, hash, counts and path; it **does not certify migration success**. Its file
is synced and the shared backup/certificate parent is synced where supported.
Unsupported/failed directory synchronization produces an explicit warning. This
is not a physical power-loss certification.

`MigrationReceipt` returns `Version`, `FromSchema`, `ToSchema`, `Status`, `Backup`,
`ReceiptPath` and `Warnings`. Status is:

- `applied`: the epoch-2 transaction committed successfully.
- `not_applied`: work stopped before the commit attempt; transactional changes
  roll back. Any already published verified backup/certificate remain available.
- `unknown`: the commit acknowledgment is uncertain. Preserve the receipt and
  inspect `SchemaVersion`; do not infer rollback or automatically retry work.
- `unchanged`: the supported database was already at schema 2. No backup is made.

Consumers must show the returned receipt even when an error accompanies it.
A process can die between publishing the snapshot and certificate; any verified
snapshot remains on disk. A process can also die after commit without returning a
success receipt. Recovery inspects the database epoch and the retained snapshot;
the static pre-migration certificate cannot distinguish those outcomes. Explicit
restore/rollback tooling is outside this API. Restoring an old snapshot would be
a separate maintenance operation, never copying a snapshot over a live database.

## Task metadata and search

Schema 1 retains the legacy body-projection path. Schema 2 adds `task_metadata`,
a covering recent index, a source-projection view and insert/update/delete
triggers on tasks. Migration preserves every original task JSON byte, including
unknown fields, and the exact indexed timestamp spelling. The task table is not
rebuilt. Normal writes maintain metadata in the same transaction, including raw
SQLite task changes; malformed source values remain visibly invalid metadata.

List/search and recent-draft lookup scan typed bounded metadata rather than
parsing large task bodies or nonmatching JSON. A covering-index key check catches
missing/orphan helper rows. All encountered metadata is validated, including
nonmatches. Only selected matches (and a pagination lookahead) re-project the
original task body and compare it with the helper, before returning any page.
RecentDraft then loads and validates its single selected full draft.

This deliberately remains O(number of metadata rows), preserving existing
nonmatching-damage behavior without adding a validity UDF or FTS framework.
A future indexed substring/validity design requires another reviewed migration.
Semantically plausible corruption of unselected helper values can remain
undetected until selection or the explicit full audit; a search miss is not a
database-health certification. Startup verifies schema/trigger definitions but
does not scan task records.

The existing limit 1-1000, 16-MiB metadata page cap, literal title-or-ID substring
search with ASCII case folding, exact-query cursor binding, exclusive raw
`updated_at,id` cursor and live-list behavior are unchanged. Mixed legacy
timestamp spellings retain their lexical ordering; migration does not normalize
them. Windows RecentDraft keeps cleaned-path Unicode EqualFold behavior; Linux
and macOS keep exact path spelling. Existing ListTasks/Attempts APIs remain.

## Application semantics

- `SaveDraft`: create at revision 1 or compare-and-swap against the expected
  revision, then increment. A task's workspace identity stays fixed. A newer draft
  may change cwd/settings during an active attempt without changing its evidence.
- `Accept`: one immediate transaction saves task/revision, immutable accepted
  input and workspace, requested/effective route, owner, lease and seq-1 `accepted`
  event. A running/unknown task blocks another attempt. Read-only workspace leases
  can coexist; any writer excludes both readers and writers.
- `GetCommand`: returns the stored attempt and original intent hash. `Accept`
  checks command replay before new route/owner/revision validation. Same ID/hash
  returns the original receipt with `newlyAccepted=false`; changed intent fails.
- `AppendEvent`: verifies ownership and running state, assigns version/time/sequence,
  and accepts `status`, `message_delta`, `tool_activity`, `input_required`, `warning`
  or `error`. Text is at most 64 KiB of valid UTF-8 without NUL. Lifecycle event
  types are reserved for transactional methods; caller sequence/time are ignored.
- `RecordNativeContext`: requires the running attempt's owner and exact accepted
  runtime/provider/account namespace. It atomically saves bounded session identity
  and durability plus a reserved `runtime_context` event containing that JSON.
  Identical replay returns the original event; replacing context is a conflict.
  Terminal/unknown attempts reject further context writes. `Finish` preserves it.
  Ephemeral/durable context is provenance, never authority to resume. The optional
  JSON field is additive within schema v1; older receipts simply lack context.
- `Finish`: saves terminal receipt, `terminal_result`, `result_persisted`, task
  status and lease release in one commit. Allowed results are complete, failed,
  interrupted and unknown. An exact terminal replay is idempotent. A newer saved
  draft survives completion. Unknown retains its task/workspace fence.
  A successful informational message is saved in `terminal_result.Text` while
  `Attempt.Error` stays empty. Exact replay also checks that saved message.
- `Reconcile`: never dispatches work. A matching expected owner plus explicit
  `confirmedStopped=true` converts running/unknown to interrupted and releases the
  fence. False marks running work unknown and retains ownership. Confirmation is
  evidence supplied by the caller; PID absence, age or timeout is not sufficient.

Attempts are returned oldest to newest. Tasks are newest-updated first. Events
are ordered by sequence after the supplied cursor. Lists permit 1–1000 rows and
preflight the requested page's encoded JSON size in the same read transaction.
More than 16 MiB returns `history_too_large`, with no silent truncation. Full
`Attempts` retains its 1000-row/16-MiB ceiling and oldest-to-newest result.
`ListAttempts(ctx, taskID, beforeRevision, limit)` reads an explicit recent page:
use cursor zero for the latest `limit` attempts, or a positive cursor for strictly
older accepted revisions. Each page is ordered oldest to newest. Its
`NextBeforeRevision` is the oldest returned revision when older records remain;
zero means the end, so stop rather than restarting at the latest page. Revisions
are unique within a task; draft gaps, timestamps and newly appended attempts do
not shift an existing cursor. The same task ID must accompany the cursor.

Pages allow 1-1000 rows with the same 16-MiB bound; reduce the requested limit for
large receipts. An error returns no partial page. Rows and the next-cursor check
share one read transaction. Pagination uses the existing task/revision index and
does not change schema or fetch other attempts. Individual encoded records also
cap at 16 MiB. Core input/output limits apply before persistence. Private
diagnostic payloads from SQLite/filesystem errors are not exposed through public
errors.

These leases coordinate UCLI state users, not independent native clients or
external services. Failure to commit returns no successful acceptance/finish
receipt. Preserve the command ID and inspect it after an ambiguous storage failure.
An owner ID is a local fencing token; this package does not authenticate remote
controllers. Draft/attempt revision numbers need not equal attempt ordinals.

## Verification

From the repository root, using the pinned Go 1.27.2 toolchain and root module:

```console
go test -p=2 -count=1 -timeout=90s -v ./internal/state
go vet -p=2 ./internal/state
```

Set `GOTOOLCHAIN=local`, `CGO_ENABLED=0`, and use the shared toolchain caches outside
the repository, as in `tools/dev.py`. The coordinator's whole-application route is
`python tools/dev.py check --go PATH_TO_GO` after integration.

On Windows AMD64, October 18, 2026, the state test command passed 58 top-level
tests (including child-process entrypoints), with zero failures or test skips;
`go vet` exited 0. The existing symlink-alias backup branch could not run because
this Windows session lacks symlink privileges; this is not an exercised pass.
Raw migration-suite/vet receipts are retained under ignored `internal/state/.build/`.
Tests consume the Store and real SQLite, including two separate
processes racing acceptance and separate connections racing events/draft CAS.
They cover restart/replay, changed command payloads, owner validation, workspace
leases, retained uncertainty, newer schema refusal by existing writers, corrupt
and foreign databases, canceled/busy operations, event limits, explicit history
limits, draft preservation and atomic failure of acceptance/terminal persistence.

The explicit-integrity tests cover empty and populated read-only audits,
cancelled/closed stores and a changed schema fence. A real SQLite CHECK-constraint
violation survives ordinary reopen and is rejected by the full audit without
modifying the damaged row. Existing malformed-header, foreign-schema and
newer-schema Open checks remain part of the suite. Package tests do not establish
startup timing; use the coordinator's identical large-history fixture to compare
before and after this change.

Capacity failure uses SQLite's actual `max_page_count` limit; read-only failure
uses SQLite `query_only`. Those are controlled engine failures, not physical disk
exhaustion or a power-loss durability test. macOS/Linux execution, ACL attacks,
network-mounted state detection on POSIX and physical power-loss behavior remain
unverified. No provider, credential or paid inference
was involved. Temporary databases/processes are owned by Go's test fixtures.

Migration tests exercise real SQLite backup/DDL/backfill, schema-1/2 read/write and
restart, original-byte/unknown-field preservation, cursor/search/draft parity,
trigger maintenance, selected and nonmatching damage, explicit consistency audit,
occupied destinations, real SQLite capacity exhaustion, rollback and uncertain
commit classification. Separate hidden OS processes prove both barrier orderings,
an already-open frozen-v1 writer's refusal after commit, and abrupt termination
before/after commit with retained recovery evidence. This helper freezes the v1
transaction fence; it is not a test of a separately packaged historical binary.
