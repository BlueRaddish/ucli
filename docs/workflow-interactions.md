# Workflow interaction boundaries

The current workflow view inspects a validated plan. It does not execute steps,
start background agents or treat an agent's prose as completion evidence.

`ucli workflow check FILE` validates the graph without opening task state.
`ucli workflow view FILE` opens plan and step details. In an interactive task,
`/workflow FILE` or the visible Workflow action preserves the current composition.
Inspect dependencies and requested settings, then return without submitting work.

The view must distinguish selected, running and completed concepts rather than
implying that inspecting a step runs it. Loading, missing-file, invalid-graph and
empty states need a recovery path. Resize and Escape must preserve the plan's
navigation context and the underlying draft. Current shortcut details are in
[the CLI guide](../internal/cli/README.md).

Future workflow execution requires explicit durable step state, artifact/handoff
contracts, permission enforcement and unknown-outcome reconciliation. The
[workflow design](workflows.md) describes that target; the
[parser guide](../internal/workflow/README.md) describes the code available now.
Use [the verification route](verification.md) to test actual interaction, not just
screenshots. No proprietary workflow implementation is bundled here.
