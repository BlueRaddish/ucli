package cli

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

func drilldownCatalog() app.ModelCatalog {
	return app.ModelCatalog{Models: []core.Model{
		{ID: "demo/fast", Label: "Fast demo", Runtime: "demo", Available: true, Efforts: []string{"low"}},
		{ID: "demo/deep", Label: "Deep demo", Runtime: "demo", Available: true, Efforts: []string{"medium", "high", "max"}},
	}}
}

func TestModelDrilldownCommitAndBackPreserveComposition(t *testing.T) {
	for _, scenario := range []string{"cli-cancel", "model-back", "effort-back", "commit", "empty-draft", "back-and-commit"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			a, err := app.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			cwd, draft := t.TempDir(), "Keep this complete composition"
			if scenario == "empty-draft" {
				draft = ""
			}
			pins := core.Selection{Model: "demo/deep", Effort: "max", Account: "independent-account", Permission: "sandbox"}
			seed, err := a.Draft(ctx, "", draft, cwd, pins)
			if err != nil {
				t.Fatal(err)
			}
			u, program := shellHarness(t, 80, 24, false)
			composer := terminalFrame{Lines: []terminalLine{{Text: "> " + draft}, {Text: "Enter send"}}, CursorRow: 0, CursorCol: 7}
			if err := u.paintComposer(composer); err != nil {
				t.Fatal(err)
			}
			events := []keyEvent{{kind: keyEscape}}
			if scenario != "cli-cancel" {
				events = []keyEvent{{kind: keyText, text: "Local demo"}, {kind: keyEnter}}
				if scenario == "model-back" {
					events = append(events, keyEvent{kind: keyEscape}, keyEvent{kind: keyEscape})
				} else {
					events = append(events, keyEvent{kind: keyText, text: "fast"}, keyEvent{kind: keyEnter})
					if scenario == "effort-back" {
						events = append(events, keyEvent{kind: keyEscape}, keyEvent{kind: keyEscape}, keyEvent{kind: keyEscape})
					} else {
						if scenario == "back-and-commit" {
							events = append(events, keyEvent{kind: keyEscape}, keyEvent{kind: keyEnter})
						}
						events = append(events, keyEvent{kind: keyText, text: "low"}, keyEvent{kind: keyEnter})
					}
				}
			}
			for _, event := range events {
				u.enqueue(event)
			}
			var output bytes.Buffer
			o := newOutput(&output, &output, false, cancel)
			s := &session{app: a, cwd: cwd, terminal: u, out: o, selection: pins, task: &seed}
			loads := 0
			err = s.selectModelCatalog(ctx, func(context.Context) (app.ModelCatalog, error) { loads++; return drilldownCatalog(), nil })
			if finish := o.finish(); err != nil || finish != nil {
				t.Fatalf("flow failed: %v %v", err, finish)
			}
			stored, attempts, err := a.Task(ctx, seed.ID)
			if err != nil || len(attempts) != 0 || stored.Draft != draft || stored.Workspace != seed.Workspace || loads != 1 || !reflect.DeepEqual(program.model.shell.composer, composer) {
				t.Fatalf("browse changed context, dispatched, or reloaded: %+v %+v %v loads=%d", stored, attempts, err, loads)
			}
			committed := scenario == "commit" || scenario == "empty-draft" || scenario == "back-and-commit"
			if committed {
				want := pins
				want.Model, want.Effort = "demo/fast", "low"
				if s.selection != want || stored.Selection != want || stored.Revision != seed.Revision+1 {
					t.Fatalf("selection was not one atomic update: %+v %+v", s.selection, stored)
				}
			} else if s.selection != pins || stored.Selection != pins || stored.Revision != seed.Revision {
				t.Fatal("abandoning drilldown partially changed model/effort or saved a revision")
			}
			modelSearches, rootSearches := 0, 0
			for _, msg := range program.msgs {
				if frame, ok := msg.(terminalFrameMsg); ok {
					text := terminalPickerText(terminalFrame(frame))
					if strings.Contains(text, "Local demo - Choose a model") && strings.Contains(text, "Search: fast") {
						modelSearches++
					}
					if strings.Contains(text, "Choose a CLI") && strings.Contains(text, "Search: Local demo") {
						rootSearches++
					}
					if strings.Contains(text, "Choose effort - Fast demo") && (strings.Contains(text, "> max") || strings.Contains(text, "max - advertised")) {
						t.Fatal("previous max pin was invented for a low-only model")
					}
				}
			}
			if scenario == "back-and-commit" && modelSearches < 2 {
				t.Fatal("Back lost model query/focus")
			}
			if scenario == "model-back" && rootSearches < 2 {
				t.Fatal("Back lost CLI query/focus")
			}
		})
	}
}

func TestModelDrilldownConflictRollsBackPins(t *testing.T) {
	a, err := app.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cwd := context.Background(), t.TempDir()
	pins := core.Selection{Model: "demo/deep", Effort: "max", Account: "kept", Permission: "sandbox"}
	seed, err := a.Draft(ctx, "", "My unsent input", cwd, pins)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := a.DraftAt(ctx, seed.ID, "A newer saved draft", cwd, pins, &seed.Revision)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	o := newOutput(&output, &output, false, func() {})
	input := newLineInput(strings.NewReader("Local demo\n1\nfast\n1\nlow\n1\n"))
	defer close(input.stop)
	s := &session{app: a, out: o, input: input, cwd: cwd, task: &seed, selection: pins}
	err = s.selectModelCatalog(ctx, func(context.Context) (app.ModelCatalog, error) { return drilldownCatalog(), nil })
	_ = o.finish()
	if err == nil || problemFor(err).Code != "revision_conflict" || s.selection != pins || s.recoveryID == "" {
		t.Fatalf("conflict changed pins or omitted recovery: %+v %v", s.selection, err)
	}
	stored, attempts, err := a.Task(ctx, seed.ID)
	if err != nil || stored.Revision != newer.Revision || stored.Draft != newer.Draft || stored.Selection != pins || len(attempts) != 0 {
		t.Fatal("picker overwrote newer task")
	}
	recovery, _, err := a.Task(ctx, s.recoveryID)
	if err != nil || recovery.Draft != seed.Draft {
		t.Fatalf("unsent input not recoverable: %+v %v", recovery, err)
	}
}

func TestModelDrilldownEffortsUseOnlyCatalogCapabilities(t *testing.T) {
	catalog := drilldownCatalog()
	for _, test := range []struct {
		model  core.Model
		values []string
	}{
		{catalog.Models[0], []string{"auto", "low", "back"}},
		{core.Model{ID: "muse/echo"}, []string{"auto", "back"}},
		{core.Model{ID: "auto"}, []string{"auto", "low", "medium", "high", "max", "back"}},
	} {
		rows := modelEffortChoices(test.model, catalog)
		var values []string
		for _, row := range rows {
			values = append(values, row.value)
		}
		if !reflect.DeepEqual(values, test.values) {
			t.Fatalf("invented/lost effort: %+v", rows)
		}
		if len(test.model.Efforts) == 0 && test.model.ID != "auto" && !strings.Contains(rows[0].label, "no adjustable effort advertised") {
			t.Fatal("fixed/no-adjustment case implied configurable effort")
		}
	}
}

func TestModelDrilldownDoesNotRetargetOwnedAttempt(t *testing.T) {
	s, ctx, execution, accepted, _ := managedUIFixture(t)
	input := newLineInput(strings.NewReader("Local demo\n1\nfast\n1\nlow\n1\n"))
	defer close(input.stop)
	s.input, s.terminal = input, nil
	if err := s.selectModelCatalog(ctx, func(context.Context) (app.ModelCatalog, error) { return drilldownCatalog(), nil }); err != nil {
		t.Fatal(err)
	}
	stored, err := s.app.Store.GetAttempt(ctx, accepted.ID)
	if err != nil || !reflect.DeepEqual(stored.Requested, accepted.Requested) || !reflect.DeepEqual(stored.Effective, accepted.Effective) || execution.cancels.Load() != 0 {
		t.Fatalf("future selection retargeted/stopped accepted work: %+v %v", stored, err)
	}
}
