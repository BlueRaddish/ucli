package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkflowClickInspectsOnlyMatchingPlanStep(t *testing.T) {
	spec, plan := workflowFixture(t)
	before, _ := json.Marshal(spec)
	terminal := &workflowTestTerminal{width: 80, height: 20, events: []keyEvent{
		{kind: keyText, text: "lint"},
		{kind: keyChoice, text: "review"}, // excluded by the filter
		{kind: keyChoice, text: "lint"},
		{kind: keyEscape}, {kind: keyEscape},
	}}
	if err := inspectWorkflowTerminal(context.Background(), terminal, spec, plan); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(terminalPickerText(terminal.frames[2]), "· detail") || !strings.Contains(terminalPickerText(terminal.frames[3]), "Step lint · detail") {
		t.Fatal("pointer did not respect the current workflow filter")
	}
	if !strings.Contains(terminalPickerText(terminal.frames[4]), "Filter: lint") {
		t.Fatal("Back lost the workflow filter")
	}
	after, _ := json.Marshal(spec)
	if string(before) != string(after) {
		t.Fatal("inspection modified the plan")
	}
	for _, line := range terminal.frames[1].Lines {
		for _, span := range line.Spans {
			if span.Action != "choice:lint" {
				t.Fatal("filtered step retained a click target")
			}
		}
	}
}
