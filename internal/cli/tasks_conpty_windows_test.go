package cli

import (
	"context"
	"fmt"
	"os"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

func TestPackagedConPTYRichTaskFinder(t *testing.T) {
	requireVisibleQA(t)
	t.Setenv("TERM", "xterm-256color")
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	home, cwd, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	main, err := a.Draft(ctx, "", "Original active rich draft", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	needle, err := a.Draft(ctx, "", "Oldest unique needle", elsewhere, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	for index := range 23 {
		if _, err := a.Draft(ctx, "", fmt.Sprintf("Rich filler %02d", index), elsewhere, core.Defaults()); err != nil {
			t.Fatal(err)
		}
	}
	p := startConPTYOptions(t, binary, home, cwd, false)
	p.awaitVisible(t, 0, "Original active rich draft")
	mark := p.send(t, "/tasks\r")
	p.awaitVisible(t, mark, "Open a task")
	mark = p.send(t, "needle")
	p.awaitVisible(t, mark, "Oldest unique needle")
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "Original active rich draft")
	mark = p.send(t, "/tasks\r")
	p.awaitVisible(t, mark, "Oldest unique needle")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Opened task in ")
	p.awaitVisible(t, mark, "Oldest unique needle")
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("finder EOF exit%d", code)
	}
	for _, id := range []string{main.ID, needle.ID} {
		task, attempts, err := a.Task(ctx, id)
		if err != nil || len(attempts) != 0 || task.Revision != 1 {
			t.Fatalf("finder changed/submitted task: %+v %+v %v", task, attempts, err)
		}
	}
}
