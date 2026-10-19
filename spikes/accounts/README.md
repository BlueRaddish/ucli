# P04 synthetic account-boundary experiment

This is a disposable standard-library experiment, not an account manager or a
provider integration. It uses two fake identities, temporary SQLite data, fake
profile directories, and fake credential values. It does not read native stores,
keyrings, logins, environment credentials, or running agent sessions. Neither
`HOME` nor provider home variables are changed.

Run from the worktree:

```powershell
python spikes/accounts/check.py --receipt evidence/spikes/accounts
```

The checker launches real, owned child processes with piped input/output and
Windows `CREATE_NO_WINDOW`. Each run creates fresh temporary stores and removes
them afterward. Retained JSON receipts contain synthetic identities and credential
digests, never credential values. No network or inference is involved.

## What the experiment establishes

| Boundary | Executed scenario | Result |
| --- | --- | --- |
| Profile versus credential owner | two separate fake HOME-style directories refer to a shared fake global backend | wrong subject/owner rejected before simulated dispatch |
| Explicit account binding | two consuming children use independent opaque owner handles | each dispatch reports the selected synthetic identity |
| Refresh ownership | one worker waits while another requests the same owner and a third refreshes a different owner | same-owner request rejected; other owner progresses |
| Stale refresh result | expired worker returns after a successor has committed | epoch, nonce and generation checks reject the late local write |
| Worker crash | terminate a waiting child, then acquire before and after lease expiry | ownership stays bounded; later synthetic recovery succeeds |
| Shared known quota | different connections concurrently reserve 6 units from one 10-unit pool | exactly one admitted |
| Idempotent reservations | resend the same attempt; resend its ID with a different connection | original receipt reused; changed intent rejected |
| Shared unknown quota | two connections concurrently reserve from one unknown pool | at most one admitted |
| Expiry and cancellation | expire reservations or send cancellation without external proof | reservations remain uncertain and still constrain admission |
| Reconciliation | provide synthetic unsent/stopped proof; known quota needs a fresh remaining observation | capacity reopens only after reconciliation |

Two deliberately unsafe fixture commands validate the checker: `unsafe-probe`
ignores binding and dispatches as the wrong identity; `unsafe-refresh` overwrites
without fencing. Both failures are detected. These are explicit negative controls
inside the synthetic experiment, not proposed UCLI commands or recovery options.
The final snapshot intentionally retains their marked effects so the evidence
does not hide the failure cases.

## Limits that matter for production

Local generation fencing does **not** establish provider-side token validity.
Two overlapping remote refresh requests may invalidate each other's tokens before
a stale local write is rejected. This fixture has no remote credential service.
Production recovery cannot infer that a lease-expired remote refresh has stopped;
it needs provider-specific serialization, reconciliation, and refresh semantics.
An opaque handle isolates the fake store only. Native daemons, OS keychain service
names, caches, ports and shared refresh stores still require real-provider proof.

The quota fixture uses exact synthetic units and explicit synthetic-clock inputs
to make process races repeatable. It does not implement subscription percentages,
provider observation revisions, external-client consumption, cross-device
reservations, or billing. A trusted fixture instruction supplies reconciliation
proof; a real implementation must obtain and validate an actual external receipt.
Known stopped/completed work cannot simply release an old estimate without a fresh
remaining observation. Unknown capacity never becomes invented precision.

The executed environment was Windows 11, Python 3.13.14, SQLite 3.50.4, with actual
journal mode **`delete`** recorded by SQLite. This fresh disposable database uses
the default rollback journal, not WAL. No production WAL safety, performance,
cross-platform locking, or chosen Go-driver behavior is established by this test.

`experiment.py` contains the bounded fake backend and transactional operations;
`check.py` drives its process interface. The [fixture source](../../testdata/account-isolation/fixture.json)
contains only synthetic subjects, opaque handles and pool mappings. Keep these
experiments separate from production account/state code.

## Current evidence and gate

The [acceptance receipt](../../docs/verification.md) records
12 checks passed, zero failed, including two expected-failure negative controls.
[Raw command receipts](../../docs/verification.md) retain
commands, exit codes and responses; [the final snapshot](../../docs/verification.md)
retains the synthetic resulting state. Source hashes identify the executed files.

See [the isolation matrix](../../docs/verification.md)
for the separation between these tests and provider evidence. Real two-account
identity/refresh isolation, provider quota sources, OS keychain boundaries, and
macOS/Linux execution remain unverified. **P04 and G0 are not complete.**
