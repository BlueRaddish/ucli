package cli

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/connections"
	"ucli.local/ucli/internal/core"
)

func TestModelChoicesSeparateUnavailableInspectionFromSelection(t *testing.T) {
	rows := modelChoices(app.ModelCatalog{
		Models:    []core.Model{{ID: "claude/sonnet", Label: "Sonnet", Runtime: "claude"}, {ID: "agy/synthetic", Label: "Synthetic", Runtime: "agy"}, {ID: "demo/fast", Label: "Fast", Runtime: "demo", Available: true}, {ID: "claude/opus", Label: "Opus", Runtime: "claude"}},
		Providers: []app.ProviderCatalog{{ID: "claude", Name: "Claude Code", Installation: "executable-found", Authentication: "not-checked", Integration: "experimental"}},
	})
	if len(rows) != 6 || rows[1].value != "info:claude/sonnet" || rows[2].value != "info:agy/synthetic" || rows[3].value != "model:demo/fast" {
		t.Fatalf("blocked models became selection actions: %+v", rows)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.value] {
			t.Fatalf("duplicate picker focus identity: %s", row.value)
		}
		seen[row.value] = true
	}
	if !strings.Contains(rows[5].label, "sign-in not checked") || !strings.Contains(rows[5].label, "installed (version not checked)") {
		t.Fatal("discovery implied authenticated status")
	}
	terminal := &pickerTestTerminal{events: []keyEvent{{kind: keyText, text: "claude/"}, {kind: keyDown}, {kind: keyEnter}}}
	value, err := pickTerminal(context.Background(), terminal, "Models", rows, "model:demo/fast")
	if err != nil || value != "info:claude/opus" {
		t.Fatalf("blocked aliases cannot be navigated independently: %s %v", value, err)
	}
}

func TestModelPickerInspectAndCancelPreservesEveryPin(t *testing.T) {
	a, err := app.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var output bytes.Buffer
	o := newOutput(&output, &output, false, func() {})
	// The discovered metadata is synthetic: no fabricated fallback aliases or
	// real provider/account are needed to exercise the unavailable row.
	catalog := blockedModelCatalog()
	input := newLineInput(strings.NewReader("claude/discovered-fixed\n1\n0\n0\n"))
	defer close(input.stop)
	s := &session{app: a, out: o, input: input, selection: core.Selection{Model: "demo/deep", Account: "pinned", Effort: "high", Permission: "read-only"}, task: &core.Task{ID: "untouched", Revision: 7, Draft: "keep this composition"}}
	before := s.selection
	if err := s.selectModelCatalog(context.Background(), func(context.Context) (app.ModelCatalog, error) { return catalog, nil }); err != nil {
		t.Fatal(err)
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	if s.selection != before || s.task.Revision != 7 || s.task.Draft != "keep this composition" {
		t.Fatal("inspecting unavailable model changed task or pins")
	}
	for _, want := range []string{catalog.Models[0].Reason, catalog.Providers[0].Reason, catalog.Providers[0].NextStep} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("blocked model omitted concrete explanation %q", want)
		}
	}
}

func blockedModelCatalog() app.ModelCatalog {
	return app.ModelCatalog{
		Models:    []core.Model{{ID: "claude/discovered-fixed", Label: "Discovered fixed model", Runtime: "claude", Reason: "This model reports fixed effort. UCLI requires explicit effort choices; no effort setting was invented."}},
		Providers: []app.ProviderCatalog{{ID: "claude", Name: "Claude Code", Reason: "Only reviewed native builds can execute.", NextStep: "Check the existing sign-in to refresh native metadata."}},
	}
}

func TestModelInspectionRichKeepsComposerAndPins(t *testing.T) {
	for _, scenario := range []string{"model-back", "model-setup-back", "provider-only", "model-without-provider"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			u, program := shellHarness(t, 80, 24, false)
			composer := terminalFrame{Lines: []terminalLine{{Text: "> keep this composition"}, {Text: "Enter send"}}, CursorRow: 0, CursorCol: 9}
			if err := u.paintComposer(composer); err != nil {
				t.Fatal(err)
			}
			catalog := blockedModelCatalog()
			query := "claude/discovered-fixed"
			if scenario == "provider-only" {
				catalog.Models = nil
				query = "Claude Code setup"
			}
			if scenario == "model-without-provider" {
				catalog.Models[0] = core.Model{ID: "muse/echo", Label: "Muse echo", Runtime: "muse", Reason: "The installed Muse executable has not been verified."}
				catalog.Providers = nil
				query = "muse/echo"
			}
			events := []keyEvent{{kind: keyText, text: query}, {kind: keyEnter}}
			if scenario == "model-without-provider" {
				events = append(events, keyEvent{kind: keyEnter})
			} else if scenario != "model-back" {
				events = append(events, keyEvent{kind: keyEnter}, keyEvent{kind: keyEscape})
			} else {
				events = append(events, keyEvent{kind: keyEscape})
			}
			events = append(events, keyEvent{kind: keyEscape})
			for _, event := range events {
				u.enqueue(event)
			}
			var output bytes.Buffer
			o := newOutput(&output, &output, false, cancel)
			s := &session{out: o, terminal: u, selection: core.Selection{Model: "demo/deep", Account: "pinned", Effort: "high", Permission: "read-only"}, task: &core.Task{ID: "untouched", Revision: 7, Draft: "keep this composition"}}
			before := s.selection
			if err := s.selectModelCatalog(ctx, func(context.Context) (app.ModelCatalog, error) { return catalog, nil }); err != nil {
				t.Fatal(err)
			}
			if err := o.finish(); err != nil {
				t.Fatal(err)
			}
			if s.selection != before || s.task.Revision != 7 || s.task.Draft != "keep this composition" || !reflect.DeepEqual(program.model.shell.composer, composer) {
				t.Fatal("inspection changed pins, revision, draft, or composer cursor")
			}
			var details string
			for _, message := range program.msgs {
				if frame, ok := message.(terminalFrameMsg); ok && (strings.Contains(terminalPickerText(terminalFrame(frame)), "Enter setup") || strings.Contains(terminalPickerText(terminalFrame(frame)), "Enter back")) {
					for _, line := range frame.Lines {
						details += line.Text
					}
				}
			}
			want := ""
			if len(catalog.Models) != 0 {
				want = catalog.Models[0].Reason
			} else {
				want = catalog.Providers[0].Reason
			}
			if !strings.Contains(details, want) {
				t.Fatalf("specific explanation was hidden behind the picker: %q", details)
			}
			if scenario == "model-without-provider" && (strings.Contains(details, "Enter setup") || strings.Contains(details, "demonstration")) {
				t.Fatal("a blocked runtime without provider setup was presented as a demo or sign-in route")
			}
			if err := u.paintComposer(composer); err != nil {
				t.Fatal(err)
			}
			view := program.model.View()
			if view.Cursor == nil || view.Cursor.X != composer.CursorCol || !strings.Contains(view.Content, "keep this composition") {
				t.Fatal("return lost editor focus or text")
			}
		})
	}
}

func TestModelDetailsWrapScrollAndTinyGuard(t *testing.T) {
	const reason = "Native capability details remain readable, including the final recovery instruction."
	terminal := &pickerTestTerminal{width: 30, height: 6, events: []keyEvent{{kind: keyDown}, {kind: keyEnd}, {kind: keyHome}, {kind: keyEnter}}}
	proceed, err := modelDetails(context.Background(), terminal, "Unavailable model", reason+"\n\nFINAL: refresh the catalog.", true)
	if err != nil || !proceed {
		t.Fatalf("detail navigation: %v %v", proceed, err)
	}
	if terminalPickerText(terminal.frames[0]) == terminalPickerText(terminal.frames[1]) || !strings.Contains(terminalPickerText(terminal.frames[2]), "FINAL: refresh the catalog.") || terminalPickerText(terminal.frames[0]) != terminalPickerText(terminal.frames[3]) {
		t.Fatal("scrolling could not reach the full reason and return")
	}
	for _, frame := range terminal.frames {
		if len(frame.Lines) > 5 {
			t.Fatal("detail exceeded body budget")
		}
		for _, line := range frame.Lines {
			if cellWidth(line.Text) > 29 {
				t.Fatalf("unwrapped detail: %q", line.Text)
			}
		}
	}
	terminal = &pickerTestTerminal{width: 10, height: 2, events: []keyEvent{{kind: keyEnter}, {kind: keyEscape}}}
	if proceed, err = modelDetails(context.Background(), terminal, "Model", reason, true); err != nil || proceed || len(terminal.frames) != 2 {
		t.Fatal("unseen details allowed setup")
	}
}

func TestConnectionExplanationNeverOffersNonAIRouteForTasks(t *testing.T) {
	for _, provider := range []string{"agy", "github", "claude"} {
		var output bytes.Buffer
		o := newOutput(&output, &output, false, func() {})
		err := showConnection(connections.Connection{ID: "synthetic-reference", Label: "Friendly", Provider: provider, AuthState: "unknown"}, o)
		if err != nil {
			t.Fatal(err)
		}
		_ = o.finish()
		text := output.String()
		if strings.HasPrefix(text, "synthetic-reference") || strings.Contains(text, "--account") || strings.Contains(text, "sign in again") {
			t.Fatalf("misleading primary identity or executable route: %s", text)
		}
		if provider == "github" && !strings.Contains(text, "no AI models") || provider == "agy" && !strings.Contains(text, "not supported") {
			t.Fatal("non-AI connection omitted its limitation")
		}
	}
}

func TestConnectionChooserDefaultsToCheckingExistingAccount(t *testing.T) {
	var output bytes.Buffer
	o := newOutput(&output, &output, false, func() {})
	input := newLineInput(strings.NewReader("1\n"))
	defer close(input.stop)
	s := &session{out: o, input: input}
	action, err := s.chooseConnectionAction(context.Background(), "claude")
	if finishErr := o.finish(); finishErr != nil {
		t.Fatal(finishErr)
	}
	if err != nil || action != "check" || !strings.Contains(output.String(), "Use the account already signed in on this computer") {
		t.Fatalf("default action implied fresh login: %s %v", action, err)
	}
}
