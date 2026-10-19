package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/workflow"
)

// Offline checking intentionally runs before app.Open: even a malformed file or
// unavailable home must not create a task, credentials, runtime or local store.
func checkWorkflow(ctx context.Context, p invocation, in io.Reader, o *output) int {
	spec, plan, err := loadWorkflow(ctx, p, in)
	if err != nil {
		return workflowFailure(o, err)
	}
	return showWorkflowCheck(spec, plan, o)
}

// Both offline surfaces use the same bounded parser and dependency validation.
// No app, state store, provider or credential discovery is involved.
func loadWorkflow(ctx context.Context, p invocation, in io.Reader) (workflow.Spec, workflow.Plan, error) {
	fail := func(err error) (workflow.Spec, workflow.Plan, error) {
		return workflow.Spec{}, workflow.Plan{}, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	reader := in
	if p.file == "-" {
		if isTerminal(in) {
			return fail(usageError("redirect workflow JSON into ucli workflow check -, or pass a file path"))
		}
	} else {
		info, err := os.Stat(p.file)
		if err != nil {
			return fail(core.Problem("workflow_read", "Cannot read workflow file: "+err.Error(), 2))
		}
		if !info.Mode().IsRegular() {
			return fail(core.Problem("workflow_read", "Workflow input must be a regular file, or - for redirected stdin.", 2))
		}
		file, err := os.Open(p.file)
		if err != nil {
			return fail(core.Problem("workflow_read", "Cannot open workflow file: "+err.Error(), 2))
		}
		defer file.Close()
		info, err = file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return fail(core.Problem("workflow_read", "Workflow input changed or is not a regular file.", 2))
		}
		reader = file
	}
	spec, err := parseWorkflow(ctx, reader)
	if err != nil {
		var problem *core.Error
		if errors.As(err, &problem) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fail(err)
		}
		return fail(core.Problem("workflow_read", "Cannot read workflow input: "+err.Error(), 2))
	}
	states := make(map[string]workflow.State, len(spec.Steps))
	for _, step := range spec.Steps {
		states[step.ID] = workflow.Pending
	}
	plan, err := workflow.PlanReady(spec, states)
	if err != nil {
		return fail(err)
	}
	return spec, plan, nil
}

func showWorkflowCheck(spec workflow.Spec, plan workflow.Plan, o *output) int {
	if o.json {
		_ = o.data("workflow_plan", struct {
			SpecID     string        `json:"spec_id"`
			Offline    bool          `json:"offline"`
			Dispatched bool          `json:"dispatched"`
			Plan       workflow.Plan `json:"plan"`
		}{spec.ID, true, false, plan})
		return 0
	}
	_ = o.text(fmt.Sprintf("Workflow %s | %d steps\nOffline check passed. No tasks were accepted or started.\nAll steps are pending; readiness describes dependencies only.\n", spec.ID, len(spec.Steps)))
	_ = o.text(fmt.Sprintf("Ready (%d):\n", len(plan.Ready)))
	for _, id := range plan.Ready {
		_ = o.text("  " + id + "\n")
	}
	_ = o.text(fmt.Sprintf("Blocked (%d):\n", len(plan.Blocked)))
	for _, step := range plan.Blocked {
		_ = o.text("  " + step.ID + "\n")
		for _, reason := range step.Reasons {
			_ = o.text("    " + reason.Message + "\n")
		}
	}
	_ = o.text("Edit the file and check again. Workflow execution is not implemented.\n")
	return 0
}

func workflowFailure(o *output, err error) int {
	problem := *problemFor(err)
	problem.Acceptance, problem.SideEffects = "not-sent", "none"
	if errors.Is(err, context.Canceled) {
		problem.Message = "Workflow check interrupted. No tasks were accepted or started."
	} else if errors.Is(err, context.DeadlineExceeded) {
		problem.Message = "Workflow check deadline exceeded. No tasks were accepted or started."
	}
	return o.problem(&problem)
}

// Like redirected prompt input, caller-owned stdin can remain blocked after
// cancellation until the caller closes it. At most one reader worker is used.
func parseWorkflow(ctx context.Context, reader io.Reader) (workflow.Spec, error) {
	type result struct {
		spec workflow.Spec
		err  error
	}
	done := make(chan result, 1)
	go func() { spec, err := workflow.Parse(reader); done <- result{spec, err} }()
	select {
	case value := <-done:
		return value.spec, value.err
	case <-ctx.Done():
		return workflow.Spec{}, ctx.Err()
	}
}
