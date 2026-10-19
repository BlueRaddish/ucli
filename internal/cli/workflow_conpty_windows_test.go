package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

func TestPackagedConPTYWorkflowPlanView(t *testing.T) {
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
	ctx := context.Background()
	original, err := a.Draft(ctx, "", "Already saved work", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	p := startConPTYOptions(t, binary, home, cwd, false)
	p.awaitVisible(t, 0, "Already saved work")
	mark := p.send(t, "Pending composition 中")
	p.awaitVisible(t, mark, "Pending composition")
	openFile := func(path string) int {
		mark := p.send(t, "\t")
		p.awaitVisible(t, mark, "Actions")
		mark = p.send(t, "workflow")
		p.awaitVisible(t, mark, "Search: workflow")
		mark = p.send(t, "\r")
		p.awaitVisible(t, mark, "Inspect workflow plan file")
		return p.send(t, "\x1b[200~"+path+"\x1b[201~\r")
	}
	mark = openFile(filepath.Join(cwd, "missing-workflow.json"))
	p.awaitVisible(t, mark, "Cannot read workflow file")
	p.awaitVisible(t, mark, "Pending composition")
	file, err := filepath.Abs(filepath.Join("..", "..", "examples", "review.workflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	mark = openFile(file)
	p.awaitVisible(t, mark, "Plan · not executed")
	mark = p.send(t, "\x1b[B\r")
	p.awaitVisible(t, mark, "Step review · detail")
	mark = p.resize(t, 60, 20)
	p.awaitVisible(t, mark, "Dependencies: inspect")
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "Filter:")
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "Pending composition")
	before, attempts, err := a.Task(ctx, original.ID)
	if err != nil || before.Draft != original.Draft || before.Revision != original.Revision || len(attempts) != 0 {
		t.Fatalf("workflow browsing changed task: %+v %+v %v", before, attempts, err)
	}
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Pending composition")
	p.send(t, "/exit\r")
	if code := p.wait(t); code != 0 {
		t.Fatalf("workflow view exit%d", code)
	}
	after, attempts, err := a.Task(ctx, original.ID)
	if err != nil || after.Draft != "Already saved work\nPending composition 中" || after.Revision != original.Revision+1 || len(attempts) != 0 {
		t.Fatalf("workflow view lost composition or ran work: %+v %+v %v", after, attempts, err)
	}
}
