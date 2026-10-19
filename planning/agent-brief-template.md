# Implementation agent brief template

Copy and fill this for one work package. A vague request to "build accounts" is
insufficient when several agents share a project.

```text
Project: UCLI (separate from cly)
Package: Pxx — exact title from planning/backlog.json
Goal: one concrete user or contract outcome
Base commit / contract revision:
Worktree / branch:
Exclusive writable paths:
Read-only dependencies and relevant docs:
Exact contract sections and boundary scenarios (avoid the entire plan):
Prerequisite evidence:
Available fixtures / authorized provider access and usage ceiling:

Implement:
  - exact deliverables from the package
  - minimum change that satisfies the consuming path

Must preserve:
  - cly and native account/session state
  - explicit model/account/permission choices
  - durable accepted work and honest unknown outcomes
  - the documented common command language and effective-setting receipts

Acceptance:
  - package's normal case
  - relevant failure/interruption case
  - stated platform/provider scope

Verification route:
  - commands and consuming path, not just unit tests
  - evidence location outside disposable state
  - unavailable tests must be reported as unverified

Coordination:
  - request shared-contract edits from the coordinator
  - report discovered conflicts before overlapping another owner's files
  - no account changes, paid overages, public publication or broad migration
    beyond the assigned/user-authorized scope

Return:
  1. changed behavior and files
  2. actual commands/results and evidence
  3. failed/skipped checks and known limits
  4. contract/dependency changes needed
     - affected consumers, persisted/wire compatibility and recovery consequences
  5. integration and rollback notes
```

Review is an independent assignment with read-only scope, a precise diff and the
contract it should satisfy. Ask for reproducible failures and corrections; a
second model's approval is not a substitute for executing the consuming path.
