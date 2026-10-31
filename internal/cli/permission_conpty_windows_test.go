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

// This restored native task is synthetic and has no linked account. Recovery
// must finish before admission: neither cancel nor choosing a mode sends it.
func TestPackagedConPTYUnsupportedPermissionRecovery(t *testing.T) {
	requireVisibleQA(t)
	t.Setenv("TERM", "xterm-256color")
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	selection := core.Selection{Model: "claude/fixture-model", Account: "synthetic-native-account", Effort: "medium", Permission: "unrestricted"}
	seed, err := a.Draft(context.Background(), "", "Keep this permission recovery prompt", cwd, selection)
	if err != nil {
		t.Fatal(err)
	}
	p := startNativeConPTYOptions(t, binary, home, cwd, false, "--no-motion", "--ascii")
	awaitPermissionComposer(t, p, 0, "restored", seed.Draft, "unrestricted")
	mark := p.send(t, "\r")
	p.awaitVisible(t, mark, "Permission unrestricted is unsupported")
	assertPermissionFixture(t, a, home, seed, "unrestricted", seed.Revision)
	mark = p.send(t, "\x1b")
	awaitPermissionComposer(t, p, mark, "recovery-canceled", seed.Draft, "unrestricted")
	assertPermissionFixture(t, a, home, seed, "unrestricted", seed.Revision)

	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Permission unrestricted is unsupported")
	mark = p.send(t, "sandbox")
	p.awaitVisible(t, mark, "full local computer access")
	p.awaitVisible(t, mark, "no OS isolation")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Permission selected: sandbox")
	p.awaitVisible(t, mark, "prompt kept; Enter sends")
	awaitPermissionComposer(t, p, mark, "recovery-confirmed-unsent", seed.Draft, "sandbox")
	assertPermissionFixture(t, a, home, seed, "sandbox", seed.Revision+1)
	// Do not send again: this proves confirmation itself cannot dispatch work.
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("permission recovery EOF exit=%d", code)
	}
	assertPermissionFixture(t, a, home, seed, "sandbox", seed.Revision+1)
	t.Log("Restored unrestricted pin canceled unchanged; explicit Sandbox saved once; composer and independent pins preserved; zero attempts or connections")
}

func TestPackagedConPTYDemoPermissionChoices(t *testing.T) {
	requireVisibleQA(t)
	t.Setenv("TERM", "xterm-256color")
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	selection := core.Selection{Model: "demo/deep", Account: "demo", Effort: "medium", Permission: "read-only"}
	seed, err := a.Draft(context.Background(), "", "Keep this local demo draft", cwd, selection)
	if err != nil {
		t.Fatal(err)
	}
	p := startConPTYOptions(t, binary, home, cwd, false, "--no-motion", "--ascii")
	awaitPermissionComposer(t, p, 0, "demo-restored", seed.Draft, "read-only")
	mark := p.send(t, "\t")
	p.awaitVisible(t, mark, "Actions")
	mark = p.send(t, "Permission")
	p.awaitVisible(t, mark, "Search: Permission")
	mark = p.send(t, "\r")
	awaitShellFrame(t, p, p.resizes, "demo-read-only-choice", func(f shellCapturedFrame) bool {
		text := strings.Join(f.Rows, "\n")
		return f.RawBytes > mark && strings.Contains(text, "Choose permission") &&
			strings.Contains(text, "> Read-only (current)") && strings.Contains(text, "Esc cancel") &&
			!strings.Contains(text, "Sandbox") && !strings.Contains(text, "workspace-write") && !strings.Contains(text, "unrestricted")
	})
	mark = p.send(t, "sandbox")
	p.awaitVisible(t, mark, "No matches. Edit the search")
	assertPermissionFixture(t, a, home, seed, "read-only", seed.Revision)
	// Confirming an empty result cannot select the old broad permission row.
	mark = p.send(t, "\r\x1b")
	awaitPermissionComposer(t, p, mark, "demo-permission-canceled", seed.Draft, "read-only")
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("demo permission EOF exit=%d", code)
	}
	assertPermissionFixture(t, a, home, seed, "read-only", seed.Revision)
	t.Log("Demo offers read-only only; Sandbox has no match; empty confirm and cancel preserve draft, pins, workspace and zero attempts")
}

func awaitPermissionComposer(t *testing.T, p *pseudoTerminal, mark int, label, draft, permission string) {
	t.Helper()
	awaitShellFrame(t, p, p.resizes, label, func(f shellCapturedFrame) bool {
		return f.RawBytes > mark && f.Width == 80 && f.Height == 24 && len(f.Rows) == 24 &&
			strings.Contains(f.Rows[22], permission) && strings.Contains(f.Rows[20], "medium") &&
			strings.Contains(f.Rows[18], draft) && strings.Contains(f.Rows[23], "? shortcuts") && f.CursorY == 18
	})
}

func assertPermissionFixture(t *testing.T, a *app.App, home string, seed core.Task, permission string, revision int) {
	t.Helper()
	ctx := context.Background()
	want := seed.Selection
	want.Permission = permission
	got, attempts, err := a.Task(ctx, seed.ID)
	if err != nil || len(attempts) != 0 || got.Draft != seed.Draft || got.Revision != revision || got.Selection != want || got.Workspace != seed.Workspace || got.Status != seed.Status {
		t.Fatalf("permission navigation changed unrelated state or dispatched: task=%+v attempts=%d err=%v", got, len(attempts), err)
	}
	tasks, err := a.Tasks(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].ID != seed.ID {
		t.Fatalf("permission navigation created another task: count=%d err=%v", len(tasks), err)
	}
	if _, err := os.Stat(filepath.Join(home, "connections-v1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("permission navigation created connection state: %v", err)
	}
}
