# Decisions, assumptions and scope

## Confirmed by the owner

- Product name UCLI, Universal CLI; separate from cly so cly remains usable.
- Ease of use and comfort are central; reduce memorization and unnecessary choices.
- Common model/effort/account experience, multiple accounts and future agent CLIs.
- Native workflow-style multi-agent work, quota/reset-aware automatic decisions.
- Windows, macOS and Linux expansion, robustness and competitive responsiveness.
- Detailed assessment, architecture and implementation plan before development.
- GitHub identity is preferred; provider connections should be manageable in UCLI.

## Recommended, not yet proven

Implementation began October 18 against `planning/contract-baseline.json` revision
`spike-v0-2026-10-18`. P00 is recorded; platform/provider/UX/account experiments
are implemented independently and integrated with retained evidence. G0 remains
open until its actual admission checks and owner walkthrough are reconciled.
Muse's installed MSP echo backend now has a real no-auth lifecycle/interrupt
receipt; this improves its second-runtime candidacy without establishing Meta
inference, account isolation or Windows sandbox enforcement. Antigravity stays
discovery/native-only until binding/trust guarantees are established.

The [October 18 readiness review](readiness-review.md) closes the shared-boundary
planning gaps for the spike baseline: task follow-ups, concrete workspace/native
session ownership, native context and permission dimensions, command arbitration,
executor lifetime/migration barriers, service/no-auth routes and scoped caches.
These are proposed implementation contracts, not evidence of provider/platform
support or owner usability approval. P00 records examples; G0 accepts v0 after
P01-P05 findings. No new phase or package is required.

| Decision | Recommendation | Validation/revisit point |
|---|---|---|
| Core language | Go | P01 packaged terminal/process/DB benchmark, ADR 0001 |
| Execution | Existing supported agent runtimes behind adapters | P02/P03 lifecycle/permission fidelity |
| First surface | CLI plus useful terminal home/prompt | P05 owner task walkthrough |
| State | Local transactional DB plus artifact files | P08 crash/integrity/recovery proof |
| Background work | Optional local supervisor after foreground loop | P20 detach/reconnect/fencing tests |
| Accounts | Local isolated connections, GitHub metadata identity later | P04/P15 provider+OS isolation |
| Automatic routing | Deterministic baseline, optional bounded decision agent | P18/P24 quality and overhead evidence |
| Plugin boundary | Typed built-ins first, external stdio protocol after two adapters | P25 independent implementation |
| Initial providers | Codex plus a separately verified second runtime | Choose after P02/P03 legal/technical scope and owner-use evidence |
| Cly relationship | Independent product; optional read-only import preview later | P27 preview/rollback/no-native-write checks |

## Open questions with an owner and deadline

| ID | Question | Owner | Resolve by |
|---|---|---|---|
| D01 | Which second runtime gives the best supported daily workflow? | Provider lead + owner | G0 |
| D02 | What packaged stack meets native terminal behavior on all target OSes? | Platform lead | G0 |
| D03 | Which providers isolate two accounts under refresh/concurrency on each OS? | Account lead | G2 capability claims |
| D04 | Which permission envelopes can each managed adapter actually enforce? | Runtime lead | Adapter admission |
| D05 | Which observed quota channels are supported/stable enough for default routing? | Routing lead | G2 |
| D06 | Which interactive home and status details minimize repeated friction? | UX lead + owner | G1, revise on user evidence |
| D07 | Does decision assistance improve measured outcomes? | Evaluation lead | G3 opt-in/default decision |
| D08 | Which GitHub App/OAuth registration and server scope is needed? | Identity lead | P28 before public auth |
| D09 | Which ARM64/Linux/headless environments are release-supported? | Release lead | G0 target matrix, G4 release |
| D10 | Public product naming/domain/package availability? | Maintainer | Before remote publication |
| D11 | Provider authorization/terms scope for hosted vs local login? | Maintainer/provider lead | Before shipping each auth mode |
| D12 | Future cloud sync retention/encryption/team policy scope? | Identity lead + owner | Before syncing private work content |

No public name/domain availability, provider approval, live UCLI entitlement,
multi-account isolation, performance or human usability is established by this plan.

## Implementation wave on October 18

The owner authorized building the separate project. P00 recorded the provisional
experiment baseline; P01-P05 now have executable experiments. Review found and
corrected unbounded provider pipe writes, premature release of uncertain native
session ownership, and incomplete descendant cleanup. The Python transport helper
is shared between the two protocol spikes; it is disposable and has a documented
Windows process-start containment gap. The Go platform spike starts its fixture
child suspended, assigns its job, then resumes it.

Keep evidence tiers separate: native Codex metadata is not authenticated inference;
Muse echo is not Meta account admission; synthetic refresh/quota checks are not
real credential isolation. The terminal prototype has automated recovery coverage,
while owner usability observations remain pending. G0 is open; no production
contracts, release capability tiers or core language decision have been accepted.
See [the implementation receipt](verification.md).

## Scope of the original planning task

Create local project docs, development contracts, task backlog, presentation and
verification of those artifacts. Source/research checks support the design. Product
implementation, native account mutation, cloud deployment, buying credits, registering
OAuth apps and public publication are not completed by this task.
