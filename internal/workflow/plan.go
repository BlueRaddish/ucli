package workflow

import (
	"fmt"
)

// PlanReady reports dependency readiness only. The caller must supply states
// belonging to the same accepted graph revision, with completion already backed
// by required evidence. This function never infers or transitions a step state.
func PlanReady(spec Spec, snapshot map[string]State) (Plan, error) {
	if err := Validate(spec); err != nil {
		return Plan{}, err
	}
	steps := make(map[string]Step, len(spec.Steps))
	for _, step := range spec.Steps {
		steps[step.ID] = step
	}
	ids := sortedIDs(steps)
	if len(snapshot) != len(steps) {
		return Plan{}, problem("workflow_invalid_snapshot", "The state snapshot must explicitly cover exactly every workflow step.")
	}
	// Check known IDs in stable order. No missing state defaults to pending.
	for _, id := range ids {
		state, exists := snapshot[id]
		if !exists || !validState(state) {
			return Plan{}, problem("workflow_invalid_snapshot", fmt.Sprintf("Step %q requires an explicit supported state.", id))
		}
	}
	plan := Plan{Ready: []string{}, Blocked: []BlockedStep{}, Running: []string{}, Complete: []string{}}
	for _, id := range ids {
		state := snapshot[id]
		switch state {
		case Running:
			plan.Running = append(plan.Running, id)
		case Complete:
			plan.Complete = append(plan.Complete, id)
		case Failed, Interrupted, Unknown:
			why := Reason{Code: "step_" + string(state)}
			switch state {
			case Failed:
				why.Message = "The step failed; a classified, explicitly authorized new attempt is required."
			case Interrupted:
				why.Message = "The step was interrupted; inspect its evidence and supported continuation before a new attempt."
			case Unknown:
				why.Message = "The external outcome is unknown; reconcile it before any new attempt."
			}
			plan.Blocked = append(plan.Blocked, BlockedStep{ID: id, State: state, Reasons: []Reason{why}})
		case Pending:
			reasons := []Reason{}
			for _, dependency := range sortedDependencies(steps[id]) {
				dependencyState := snapshot[dependency]
				if dependencyState == Complete {
					continue
				}
				message := fmt.Sprintf("Dependency %q is %s; its verified completion is required.", dependency, dependencyState)
				if dependencyState == Unknown {
					message = fmt.Sprintf("Dependency %q has an unknown outcome and must be reconciled before it can satisfy this dependency.", dependency)
				}
				reasons = append(reasons, Reason{Code: "dependency_" + string(dependencyState), Message: message, Dependency: dependency})
			}
			if len(reasons) == 0 {
				plan.Ready = append(plan.Ready, id)
			} else {
				plan.Blocked = append(plan.Blocked, BlockedStep{ID: id, State: state, Reasons: reasons})
			}
		}
	}
	return plan, nil
}

func validState(state State) bool {
	return state == Pending || state == Running || state == Complete || state == Failed || state == Interrupted || state == Unknown
}
