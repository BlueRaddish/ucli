# First development wave

The owner authorized implementation on October 18. This is the assignment recipe
used for the first wave. See [active work](active-work.md) and
[the implementation receipt](../docs/verification.md) for actual
results; this recipe alone does not establish a passed gate.

## Coordinator setup

1. Read the current repository and all applicable instructions. Preserve cly and
   active user sessions. Confirm no work package has an active overlapping owner.
2. Complete P00: record the provisional command/settings/event baseline and the
   [readiness scenarios](../docs/readiness-review.md),
   choose synthetic fixtures and record exact open spike questions in decisions.md.
   Do not turn every provisional recommendation into a settled fact.
   G0 accepts v0 only after the adapter/platform/experience findings; P00 does not
   freeze provider-specific API shapes before those experiments.
3. Create one isolated worktree/branch per assigned implementation package. Keep a
   small active ownership register (package, agent, branch, paths, base commit,
   contract revision and current status). The coordinator owns shared contract edits.
4. Dispatch up to three workers alongside the coordinator. Start P01, P02 and P05.
   Assign P03 when a slot is free. P04 starts after P02 and P03 have established the
   actual authentication paths. Do not parallelize real-account refresh experiments
   against the same credential store without the isolation contract.
5. Review evidence, resolve discrepancies and update ADR/decision records. G0
   requires the applicable host/runtime proof; unavailable targets remain unverified.

## Ready-to-adapt first assignments

| Worker | Package | Concrete output | Main failure to expose |
|---|---|---|---|
| Platform | P01 | Packaged command/DB/process/stream spike and native-relative timing receipts | Windows descendant cleanup or packaging/latency assumption fails |
| Codex | P02 | Structured protocol lifecycle driver with redacted fixtures | Permission, interrupt or continuation semantics differ from assumptions |
| Experience | P05 | Reviewable terminal task walkthrough and observed owner feedback | User cannot start/recover without knowing hidden commands |
| Second runtime, next free slot | P03 | agy lifecycle/binding spike; Muse/Claude API alternative if needed | Print-only path cannot provide required management/approval behavior |
| Account isolation, after both runtimes | P04 | Provider/OS account isolation and quota scope evidence | Shared keychain/daemon/refresh state defeats isolation |

The owner need not review implementation plumbing for every task. Bring back a
concrete product tradeoff when evidence changes the intended experience, scope,
authority or cost. Routine reversible choices within the assigned contract proceed.

## Integration sequence after G0

P06 establishes the chosen build. P07 supplies shared contracts, then state (P08),
process execution (P09), and CLI parsing/help (P10) can proceed under exclusive
ownership. P11 uses the accepted command registry. P12/P13 use the accepted process
and domain contracts. P14 integrates the real foreground loop and early native
escape. Do not mark G1 complete while only mocks or isolated modules work.

## Review the first useful slice

Ask the owner to open UCLI, describe an ordinary task, inspect/change a model, and
recover from one controlled interruption. Observe first, teach afterward. Compare
the same task in the native client. Record friction, lost state, unnecessary steps,
time to useful feedback and recovery effort. Use the result to choose the next
increment; more features do not compensate for a weak core loop.

The complete task graph and acceptance cases live in
[work-packages.md](work-packages.md). Use [the brief template](agent-brief-template.md)
for every assignment and [verification](../docs/verification.md) for evidence.
