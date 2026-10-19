# Offline workflow graph checking

This package implements the read-only graph portion of P21: strict JSON parsing,
graph validation and deterministic dependency readiness. It uses the Go standard
library and the existing universal `core.Selection` type. It does not start a
runtime, open a store, call a model, read credentials or alter a workspace.

## Version 1 document

```json
{
  "version": 1,
  "id": "feature-plan",
  "steps": [
    {
      "id": "implement",
      "prompt": "Implement the requested small change.",
      "selection": {
        "model": "auto",
        "account": "work",
        "effort": "high",
        "permission": "workspace-write"
      }
    },
    {
      "id": "check",
      "prompt": "Run the required checks.",
      "depends_on": ["implement"]
    },
    {
      "id": "review",
      "prompt": "Review the change and its recorded check evidence.",
      "depends_on": ["implement", "check"]
    }
  ]
}
```

`version`, workflow `id`, nonempty `steps`, and each step's `id` and `prompt` are
required. `selection` and its fields are optional and use `core.Defaults()` when
parsed; `depends_on` may be omitted or empty. Universal selection validation
checks syntax only: it does not establish that any model, account or permission
is available or authorized. A later executor inherits its workspace separately.

Workflow and step IDs are case-sensitive ASCII labels, 1-64 bytes: the first
character is a letter or digit; later characters may also be `.`, `_` or `-`.
This avoids Unicode normalization ambiguity. IDs are labels, not filesystem
paths, accepted task IDs or permission to access another resource. Prompts may
contain Unicode; environment-looking text and command text remain literal data.

The document is limited to 2 MiB, 256 steps, 2048 dependency edges and 1 MiB of
combined decoded prompt text. Every prompt must also pass the common input
validation (nonempty, valid UTF-8, no NUL). The parser rejects unknown or
incorrectly cased fields, duplicate object keys, nulls, wrong field types,
unpaired Unicode surrogate escapes, trailing documents and unsupported versions.
It rejects duplicate step IDs, missing dependencies, self-dependencies, duplicate
edges and cycles. No imports, environment interpolation, shell expressions,
retry rules, loops, subworkflows or provider-specific configuration are supported.

## API and state ownership

```go
spec, err := workflow.Parse(reader) // bounded io.Reader; caller owns file/stdin
err = workflow.Validate(spec)      // also validates direct typed callers
plan, err := workflow.PlanReady(spec, snapshot)
```

`snapshot` is `map[string]workflow.State` and must explicitly contain exactly
every step, using `Pending`, `Running`, `Complete`, `Failed`, `Interrupted` or
`Unknown`. Missing entries do not become pending. The caller owns the snapshot
and must ensure it belongs to the same accepted graph revision and that completed
steps have their required evidence. The planner does not verify evidence or infer
state transitions. It preserves completed steps even if another step fails.

`Plan` returns non-nil arrays:

- `Ready`: pending step IDs whose dependencies are all complete.
- `Blocked`: step ID, state and structured reasons. Failed, interrupted and
  unknown steps cannot become ready automatically. Unknown outcomes require
  reconciliation before any new attempt. Pending steps name each incomplete
  dependency in their reasons.
- `Running` and `Complete`: IDs in those supplied states.

Every array is ordered by ASCII step ID; dependency reasons are ordered by
dependency ID. Reason codes are `step_failed`, `step_interrupted`, `step_unknown`
and `dependency_` followed by the incomplete dependency's state. Inputs are never
mutated. Concurrent calls may share inputs provided callers do not mutate those
inputs concurrently. Errors are `*core.Error` with a `workflow_*` code, usage exit
2, `acceptance=not-sent` and `side_effects=none`; prompt contents and reader
diagnostics are not included in errors.

Readiness describes dependencies only. Two writers can both be ready; that does
not grant either a workspace lease, model entitlement, permission or dispatch
slot. The future executor must use the existing acceptance, ownership, routing
and lease boundaries before dispatch. This slice creates no workflow run or
attempt, does not persist snapshots, execute steps, retry failures, resume native
sessions, inspect outputs, check artifacts or authorize approvals. It does not
complete the P21 persistent scheduler acceptance gate.

## Verification

Use the project's pinned Go toolchain and caches as described in
[`docs/verification.md`](../../docs/verification.md), then run from the repository
root:

```console
go test -p=2 -count=1 -timeout=60s ./internal/workflow
go vet ./internal/workflow
```

The tests parse the implement/check/review example, exercise strict input and
exact/overflow size limits, reject graph errors, move through explicit lifecycle
snapshots, preserve completed evidence, fence unknown outcomes and compare
deterministic results across input orderings and concurrent read-only calls.
They also verify that dependency-ready writers do not acquire execution rights.
These are offline package checks; the command frontend and an executing scheduler
require their own consuming checks.
