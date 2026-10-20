package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

// This consumes the packaged entrypoint in an owned hidden console. Every
// operation is local UI, local draft storage, or the explicit demo executor.
func TestPackagedConPTYSlashCompletion(t *testing.T) {
	requireVisibleQA(t)
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	t.Setenv("TERM", "xterm-256color")
	home := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "completion-workspace")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	seed, err := a.Draft(ctx, "", "Saved completion fixture", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	p := startConPTYOptions(t, binary, home, cwd, false, "--no-motion", "--ascii")
	width, height := 80, 24
	observe := func(label, text, selected, feedback string, popup bool) shellCapturedFrame {
		t.Helper()
		return awaitShellFrame(t, p, p.resizes, label, func(f shellCapturedFrame) bool {
			return completionShellFrame(f, width, height, text, selected, feedback, popup) &&
				shellFrameFooter(f, "auto", filepath.Base(cwd))
		})
	}
	unchanged := func() {
		t.Helper()
		got, attempts, err := a.Task(ctx, seed.ID)
		if err != nil || got.Draft != seed.Draft || got.Revision != seed.Revision || got.Selection != seed.Selection || len(attempts) != 0 {
			t.Fatalf("completion changed saved work: task=%+v attempts=%d err=%v", got, len(attempts), err)
		}
	}
	p.awaitVisible(t, 0, seed.Draft)
	p.send(t, "\x15")
	observe("ready", "", "", "", false)
	p.send(t, "/")
	// Check reachability through the actual bounded popup, not just that a
	// registry contains the commands. Down scrolls the focus into its viewport.
	names := make([]string, len(slashCommands))
	for i, spec := range slashCommands {
		names[i] = spec.Name
	}
	for index, name := range names {
		if index != 0 {
			p.send(t, "\x1b[B")
		}
		observe("all-"+strings.TrimPrefix(name, "/"), "/", name, "Commands", true)
	}
	p.send(t, "\x1b[B")
	observe("down-wrap", "/", names[0], "Commands", true)
	p.send(t, "\x1b[A")
	observe("up-wrap", "/", names[len(names)-1], "Commands", true)
	unchanged()

	p.send(t, "\x15/mo")
	observe("prefix-model", "/mo", "/models", "Commands matching /mo", true)
	p.send(t, "\t")
	observe("tab-completed-only", "/models ", "", "", false)
	unchanged()
	mark := p.send(t, "\r")
	p.awaitVisible(t, mark, "Choose a model")
	p.send(t, "\x1b")
	p.awaitVisible(t, 0, seed.Draft)
	p.send(t, "\x15")
	observe("models-cancel", "", "", "", false)
	unchanged()

	p.send(t, "/ef")
	observe("prefix-effort", "/ef", "/effort", "Commands matching /ef", true)
	p.send(t, "\r")
	observe("enter-completed-only", "/effort ", "", "", false)
	unchanged()
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Choose effort")
	p.send(t, "\x1b")
	p.awaitVisible(t, 0, seed.Draft)
	p.send(t, "\x15")
	observe("effort-cancel", "", "", "", false)
	unchanged()

	p.send(t, "/")
	observe("resize-before", "/", names[0], "Commands", true)
	p.send(t, "\x1b[B")
	observe("resize-selected", "/", names[1], "Commands", true)
	p.resize(t, 60, 20)
	width, height = 60, 20
	observe("resize-retained", "/", names[1], "Commands", true)
	p.send(t, "mo")
	observe("narrow-prefix", "/mo", "/models", "Commands matching /mo", true)
	p.send(t, "\x1b")
	observe("escape-preserved", "/mo", "", "", false)
	p.send(t, "d")
	observe("edit-reopens", "/mod", "/models", "Commands matching /mod", true)
	p.send(t, "\x15/zz")
	observe("no-match", "/zz", "", "No commands match /zz", true)
	p.send(t, "\x7f\x7f")
	observe("no-match-recovered", "/", names[0], "Commands", true)
	unchanged()

	const pasted = "/send literal completion fixture"
	p.send(t, "\x15\x1b[200~"+pasted+"\x1b[201~")
	observe("literal-paste-no-popup", pasted, "", "", false)
	unchanged()
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Ready for your next prompt")
	observe("literal-sent-once", "", "", "", false)
	got, attempts, err := a.Task(ctx, seed.ID)
	if err != nil || got.Draft != "" || got.Selection != seed.Selection || len(attempts) != 1 || attempts[0].Input != pasted || attempts[0].State != "complete" {
		t.Fatalf("literal paste was not sent exactly once as text: task=%+v attempts=%+v err=%v", got, attempts, err)
	}
	sentRevision := got.Revision
	p.send(t, "/re")
	observe("interrupt-prefix", "/re", "/result", "Commands matching /re", true)
	p.send(t, "\x03\x03")
	if code := p.wait(t); code != 130 {
		t.Fatalf("interrupt exit=%d", code)
	}
	got, attempts, err = a.Task(ctx, seed.ID)
	if err != nil || got.Draft != "/re" || got.Revision != sentRevision+1 || got.Selection != seed.Selection || len(attempts) != 1 {
		t.Fatalf("interrupt lost prefix or dispatched work: task=%+v attempts=%d err=%v", got, len(attempts), err)
	}
}

func completionShellFrame(f shellCapturedFrame, width, height int, input, selected, feedback string, popup bool) bool {
	if !shellFrameBase(f, width, height) || !shellFrameRule(f.Rows[height-5], width) ||
		strings.TrimSpace(f.Rows[height-4]) != strings.TrimSpace("> "+input) ||
		f.CursorY != height-4 || f.CursorX != 2+len(input) {
		return false
	}
	body := strings.Join(f.Rows[3:height-6], "\n")
	if !popup {
		return !strings.Contains(body, "Commands") && !strings.Contains(body, "No commands match") &&
			strings.Contains(f.Rows[height-1], "Tab actions")
	}
	if !strings.Contains(body, feedback) || !strings.Contains(body, "Esc close") {
		return false
	}
	foot := f.Rows[height-1]
	if strings.Contains(foot, "Tab actions") || !strings.Contains(foot, "Esc close") {
		return false
	}
	if selected == "" {
		return !strings.Contains(body, "> /") && strings.Contains(body, "Keep typing") &&
			strings.Contains(foot, "Keep typing") && !strings.Contains(foot, "Tab complete")
	}
	enter := "Enter/Tab complete"
	if input == selected {
		enter = "Enter run"
	}
	if !strings.Contains(body, enter) || !strings.Contains(foot, enter) || !strings.Contains(foot, "Tab complete") {
		return false
	}
	for _, row := range f.Rows[3 : height-6] {
		if strings.HasPrefix(strings.TrimSpace(row), "> "+selected+" ") {
			return true
		}
	}
	return false
}

func TestCompletionFrameRejectsDetachedCursorAndLostInput(t *testing.T) {
	f := shellCapturedFrame{Width: 80, Height: 24, Rows: make([]string, 24), CursorX: 5, CursorY: 20}
	f.Rows[0], f.Rows[1] = "UCLI version", "read-only | effort auto"
	f.Rows[2], f.Rows[19], f.Rows[21] = strings.Repeat("\u2500", 79), strings.Repeat("\u2500", 79), strings.Repeat("\u2500", 79)
	f.Rows[14], f.Rows[15], f.Rows[16] = "Commands matching /mo", "> /models  choose models", "Up/Down select | Enter/Tab complete | Esc close"
	f.Rows[20], f.Rows[22], f.Rows[23] = "> /mo", "Model auto | /work/completion-workspace", "Up/Down select | Enter/Tab complete | Esc close"
	if !completionShellFrame(f, 80, 24, "/mo", "/models", "Commands matching /mo", true) {
		t.Fatal("valid completion frame rejected")
	}
	for _, row := range []int{14, 15, 16, 20, 22, 23} {
		bad := f
		bad.Rows = append([]string(nil), f.Rows...)
		bad.Rows[row] = ""
		if completionShellFrame(bad, 80, 24, "/mo", "/models", "Commands matching /mo", true) {
			t.Fatalf("missing completion/input/footer row %d accepted", row)
		}
	}
	f.Rows[23] = "Tab actions | Esc close"
	if completionShellFrame(f, 80, 24, "/mo", "/models", "Commands matching /mo", true) {
		t.Fatal("stale actions footer accepted while suggestions were open")
	}
	f.Rows[23] = "Up/Down select | Enter/Tab complete | Esc close"
	f.CursorY = 15
	if completionShellFrame(f, 80, 24, "/mo", "/models", "Commands matching /mo", true) {
		t.Fatal("cursor moved into suggestions was accepted")
	}
	f.CursorY, f.CursorX = 20, 4
	if completionShellFrame(f, 80, 24, "/mo", "/models", "Commands matching /mo", true) {
		t.Fatal("lost editing position was accepted")
	}
	f.CursorX = 5
	f.Rows[14], f.Rows[15], f.Rows[16], f.Rows[20] = "Commands matching /zz", "No commands match /zz", "Keep typing | Esc close", "> /zz"
	f.Rows[23] = "Keep typing | Esc close"
	if !completionShellFrame(f, 80, 24, "/zz", "", "No commands match /zz", true) {
		t.Fatal("editable no-match frame rejected")
	}
	f.Rows[23] = "Tab actions | Esc close"
	if completionShellFrame(f, 80, 24, "/zz", "", "No commands match /zz", true) {
		t.Fatal("stale actions footer accepted for empty suggestions")
	}
	f.Rows[23] = "Keep typing | Esc close"
	f.Rows[16] = "Enter/Tab complete | Esc close"
	if completionShellFrame(f, 80, 24, "/zz", "", "No commands match /zz", true) {
		t.Fatal("empty results advertised a selectable command")
	}
}
