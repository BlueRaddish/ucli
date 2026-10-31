package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

// The empty isolated home has no discovered native model rows. Provider details
// remain inspectable without checking sign-in, creating a connection or sending.
func TestPackagedConPTYModelProviderDetails(t *testing.T) {
	requireVisibleQA(t)
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	t.Setenv("TERM", "xterm-256color")
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	selection := core.Selection{Model: "demo/deep", Account: "demo", Effort: "high", Permission: "read-only"}
	seed, err := a.Draft(ctx, "", "Keep my model inspection draft", cwd, selection)
	if err != nil {
		t.Fatal(err)
	}
	p := startConPTYOptions(t, binary, home, cwd, false, "--no-color", "--no-motion")
	p.awaitVisible(t, 0, seed.Draft)
	for _, proceed := range []bool{false, true} {
		mark := p.send(t, "\t")
		p.awaitVisible(t, mark, "Actions")
		p.send(t, "Models\r")
		p.awaitVisible(t, mark, "Choose a CLI")
		mark = p.send(t, "Claude Code\r")
		p.awaitVisible(t, mark, "Choose a model")
		p.send(t, "Claude Code setup\r")
		label := "model-provider-details-back"
		if proceed {
			label = "model-provider-details-setup"
		}
		awaitShellFrame(t, p, p.resizes, label, func(frame shellCapturedFrame) bool {
			text := strings.Join(frame.Rows, "\n")
			compact := strings.Join(strings.Fields(text), "")
			want := strings.Join(strings.Fields("Model metadata is discovered natively; unreviewed builds remain unavailable for execution."), "")
			return strings.Contains(compact, want) && strings.Contains(text, "Enter setup") && strings.Contains(text, seed.Draft)
		})
		if proceed {
			mark = p.send(t, "\r")
			p.awaitVisible(t, mark, "Check existing sign-in and refresh models")
		}
		mark = p.send(t, "\x1b")
		p.awaitVisible(t, mark, "Choose a model")
		mark = p.send(t, "\x1b")
		p.awaitVisible(t, mark, "Choose a CLI")
		mark = p.send(t, "\x1b")
		p.awaitVisible(t, mark, "? shortcuts")
		p.awaitVisible(t, mark, seed.Draft)
	}
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("inspection EOF exit=%d", code)
	}
	stored, attempts, err := a.Task(ctx, seed.ID)
	if err != nil || len(attempts) != 0 || stored.Draft != seed.Draft || stored.Revision != seed.Revision || stored.Selection != selection || stored.Workspace != seed.Workspace {
		t.Fatalf("provider inspection changed composition or pins: %+v %+v %v", stored, attempts, err)
	}
	if _, err := os.Stat(filepath.Join(home, "connections-v1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only model inspection created native connection state: %v", err)
	}
}

// Only local demo catalog choices are used. Inspect durable state while the
// effort step is open, then verify one final model/effort update and no dispatch.
func TestPackagedConPTYModelDrilldown(t *testing.T) {
	requireVisibleQA(t)
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	t.Setenv("TERM", "xterm-256color")
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	pins := core.Selection{Model: "demo/deep", Effort: "high", Account: "demo", Permission: "read-only"}
	seed, err := a.Draft(ctx, "", "Keep my model and effort draft", cwd, pins)
	if err != nil {
		t.Fatal(err)
	}
	p := startConPTYOptions(t, binary, home, cwd, false, "--no-color", "--no-motion", "--ascii")
	p.awaitVisible(t, 0, seed.Draft)
	mark := p.send(t, "\t")
	p.awaitVisible(t, mark, "Actions")
	mark = p.send(t, "Models\r")
	p.awaitVisible(t, mark, "Choose a CLI")
	mark = p.send(t, "Local demo\r")
	p.awaitVisible(t, mark, "Choose a model")
	mark = p.send(t, "fast\r")
	p.awaitVisible(t, mark, "Choose effort")
	awaitShellFrame(t, p, p.resizes, "model-drilldown-effort", func(frame shellCapturedFrame) bool {
		text := strings.Join(frame.Rows, "\n")
		low, medium := false, false
		for _, row := range frame.Rows {
			low = low || strings.TrimSpace(row) == "low"
			medium = medium || strings.TrimSpace(row) == "medium"
		}
		return strings.Contains(text, "Choose effort") && low && medium && strings.Contains(text, seed.Draft)
	})
	stored, attempts, err := a.Task(ctx, seed.ID)
	if err != nil || stored.Selection != pins || stored.Revision != seed.Revision || len(attempts) != 0 {
		t.Fatal("entering effort committed a partial model selection")
	}
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "Choose a model")
	p.awaitVisible(t, mark, "Search: fast")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Choose effort")
	mark = p.send(t, "medium\r")
	p.awaitVisible(t, mark, "Model selected")
	p.awaitVisible(t, mark, seed.Draft)
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("drilldown EOF exit=%d", code)
	}
	stored, attempts, err = a.Task(ctx, seed.ID)
	want := pins
	want.Model, want.Effort = "demo/fast", "medium"
	if err != nil || stored.Selection != want || stored.Draft != seed.Draft || stored.Revision != seed.Revision+1 || len(attempts) != 0 {
		t.Fatalf("model+effort were not saved once without dispatch: %+v %+v %v", stored, attempts, err)
	}
}
