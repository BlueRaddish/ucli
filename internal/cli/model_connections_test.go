package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
	// Search exact alias, inspect it, back out of setup, then leave the picker.
	input := newLineInput(strings.NewReader("claude/sonnet\n1\n0\n0\n"))
	defer close(input.stop)
	s := &session{app: a, out: o, input: input, selection: core.Selection{Model: "demo/deep", Account: "pinned", Effort: "high", Permission: "read-only"}, task: &core.Task{ID: "untouched", Revision: 7, Draft: "keep this composition"}}
	before := s.selection
	if err := s.selectModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	if s.selection != before || s.task.Revision != 7 || s.task.Draft != "keep this composition" {
		t.Fatal("inspecting unavailable model changed task or pins")
	}
	if !strings.Contains(output.String(), "Other installed versions remain unavailable until reviewed") {
		t.Fatal("blocked model omitted concrete compatibility explanation")
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
