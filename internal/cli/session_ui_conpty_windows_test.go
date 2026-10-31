package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

// Exercise actual SGR pointer input through the packaged Windows entrypoint.
// The only submitted task is a local simulation; account browsing never signs in.
func TestPackagedConPTYSessionUI(t *testing.T) {
	requireVisibleQA(t)
	t.Setenv("NO_COLOR", "")
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a packaged Windows executable")
	}
	t.Setenv("TERM", "xterm-256color")
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	pins := core.Selection{Model: "demo/deep", Account: "demo", Effort: "high", Permission: "read-only"}
	seed, err := a.Draft(ctx, "", "Review this local example.\n\nKeep paragraphs readable.", cwd, pins)
	if err != nil {
		t.Fatal(err)
	}
	p := startConPTYOptions(t, binary, home, cwd, false, "--no-motion", "--ascii")
	awaitShellFrame(t, p, p.resizes, "session-ui-restored", func(f shellCapturedFrame) bool {
		return len(f.Rows) == 24 && strings.Contains(strings.Join(f.Rows, "\n"), "Review this local example.") && strings.Contains(f.Rows[23], "? shortcuts")
	})
	click := func(x, y int) int { return p.send(t, fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", x+1, y+1, x+1, y+1)) }
	mark := click(1, 23)
	p.awaitVisible(t, mark, "Choose a CLI")
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "? shortcuts")
	// Connect is a visible, draft-preserving navigation target, not a sign-in.
	mark = click(20, 1)
	p.awaitVisible(t, mark, "Connect an account")
	awaitShellFrame(t, p, p.resizes, "session-ui-connect", func(f shellCapturedFrame) bool {
		return strings.Contains(strings.Join(f.Rows, "\n"), "Connect an account") && strings.Contains(strings.Join(f.Rows, "\n"), "Review this local example.")
	})
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "? shortcuts")
	stored, attempts, err := a.Task(ctx, seed.ID)
	if err != nil || stored.Draft != seed.Draft || stored.Selection != pins || stored.Revision != seed.Revision || len(attempts) != 0 {
		t.Fatal("pointer navigation modified or submitted the draft")
	}
	mark = click(13, 23)
	p.awaitVisible(t, mark, "Shortcuts")
	awaitShellFrame(t, p, p.resizes, "session-ui-shortcuts", func(f shellCapturedFrame) bool {
		return strings.Contains(strings.Join(f.Rows, "\n"), "Shortcuts") && strings.Contains(strings.Join(f.Rows, "\n"), "Job details")
	})
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "? shortcuts")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Ready for your next prompt")
	response := awaitShellFrame(t, p, p.resizes, "session-ui-response", func(f shellCapturedFrame) bool {
		text := strings.Join(f.Rows, "\n")
		return len(f.Rows) == 24 && strings.Contains(text, "Assistant") && strings.Contains(f.Rows[3], "response saved") && strings.Contains(f.Rows[20], "Usage not reported") && strings.Contains(f.Rows[23], "? shortcuts")
	})
	colors := map[string]bool{}
	for _, span := range response.Styles {
		if span.Row == 20 && span.Foreground != "default" && span.Foreground != "" {
			colors[span.Foreground] = true
		}
	}
	if len(colors) < 2 {
		t.Fatal("packaged model and usage fields lost distinct semantic colors")
	}
	stored, attempts, err = a.Task(ctx, seed.ID)
	if err != nil || len(attempts) != 1 || attempts[0].State != "complete" || attempts[0].Output == "" || stored.Draft != "" {
		t.Fatalf("local simulation did not save exactly one complete result: %+v %v", attempts, err)
	}
	mark = click(10, 3)
	p.awaitVisible(t, mark, "Job details")
	awaitShellFrame(t, p, p.resizes, "session-ui-job-details", func(f shellCapturedFrame) bool {
		text := strings.Join(f.Rows, "\n")
		return strings.Contains(text, "Job details") && strings.Contains(text, "Complete") && strings.Contains(text, "demo/deep")
	})
	// Paste/Enter in the read-only inspector cannot reach the hidden composer.
	mark = p.send(t, "\x1b[200~Do not insert this hidden text\x1b[201~\r\x1b")
	p.awaitVisible(t, mark, "? shortcuts")
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("session UI EOF exit=%d", code)
	}
	stored, attempts, err = a.Task(ctx, seed.ID)
	if err != nil || stored.Draft != "" || len(attempts) != 1 || stored.Selection != pins {
		t.Fatal("inspector input submitted or changed another prompt")
	}
	t.Log("Actual pointer navigation, cached login browsing, compact shortcuts and saved job inspector preserve the draft and one local result")
}

func TestPackagedConPTYWorkflowDemo(t *testing.T) {
	requireVisibleQA(t)
	t.Setenv("NO_COLOR", "")
	binary, file := os.Getenv("UCLI_CONPTY_BINARY"), os.Getenv("UCLI_WORKFLOW_UI_DEMO")
	if binary == "" || file == "" {
		t.Skip("set packaged binary and UCLI_WORKFLOW_UI_DEMO for the offline visual demo")
	}
	t.Setenv("TERM", "xterm-256color")
	home, cwd := t.TempDir(), t.TempDir()
	p := startConPTYInvocation(t, binary, home, cwd, false, false, false, "--no-motion", "--ascii", "workflow", "view", file)
	p.awaitVisible(t, 0, "Plan · not executed")
	p.resize(t, 120, 30)
	frame := awaitShellFrame(t, p, p.resizes, "workflow-demo-plan", func(f shellCapturedFrame) bool {
		text := strings.Join(f.Rows, "\n")
		return workflowDemoFrameGeometry(f, 120, 30) && strings.Contains(text, "sample-help-page-review") &&
			strings.Contains(text, "1 ready / 3 waiting") && strings.Contains(text, "navigation [") && strings.Contains(text, "wording [") && strings.Contains(text, "review [")
	})
	row := -1
	for i, text := range frame.Rows {
		if strings.Contains(text, "navigation [") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("demo navigation step not visible")
	}
	mark := p.send(t, fmt.Sprintf("\x1b[<0;4;%dM\x1b[<0;4;%dm", row+1, row+1))
	p.awaitVisible(t, mark, "Step navigation · detail")
	p.resize(t, 60, 20)
	awaitShellFrame(t, p, p.resizes, "workflow-demo-step", func(f shellCapturedFrame) bool {
		text := strings.Join(f.Rows, "\n")
		return workflowDemoFrameGeometry(f, 60, 20) &&
			strings.Contains(text, "Plan · not executed") && strings.Contains(text, "Dependencies: inspect") && strings.Contains(text, "Step navigation")
	})
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "Filter:")
	p.send(t, "\x1b")
	if code := p.wait(t); code != 0 {
		t.Fatalf("offline demo exit=%d", code)
	}
	if _, err := os.Stat(filepath.Join(home, "state", "state.db")); !os.IsNotExist(err) {
		t.Fatalf("offline plan inspection opened a task store: %v", err)
	}
	t.Log("Actual offline workflow viewer: four steps, dependency readiness, pointer-open detail and narrow resize; no task store, sign-in or inference")
}

func workflowDemoFrameGeometry(f shellCapturedFrame, width, height int) bool {
	return f.Width == width && f.Height == height && len(f.Rows) == height &&
		shellFrameRule(f.Rows[2], width) && strings.HasPrefix(f.Rows[height-1], "Location ") &&
		strings.Contains(f.Rows[height-2], "Esc")
}

func TestSessionUIWorkflowDemoFrameRejectsStaleResize(t *testing.T) {
	frame := shellCapturedFrame{Width: 120, Height: 30, Rows: make([]string, 30)}
	// A real early observation had a resized canvas containing the old 80x24
	// application frame. Dimensions alone cannot certify the completed redraw.
	frame.Rows[2], frame.Rows[22], frame.Rows[23] = strings.Repeat("─", 79), "Esc back", "Location /work"
	if workflowDemoFrameGeometry(frame, 120, 30) {
		t.Fatal("stale application layout accepted on resized canvas")
	}
	frame.Rows[2], frame.Rows[28], frame.Rows[29] = strings.Repeat("─", 119), "Esc back", "Location /work"
	if !workflowDemoFrameGeometry(frame, 120, 30) {
		t.Fatal("completed wide redraw rejected")
	}
	frame.Rows[29] = ""
	if workflowDemoFrameGeometry(frame, 120, 30) {
		t.Fatal("missing bottom metadata accepted")
	}
}
