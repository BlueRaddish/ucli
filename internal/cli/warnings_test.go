package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/workspace"
)

func TestSavedWarningVisibleInStreamReplayAndHistory(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	w, err := workspace.Resolve(cwd)
	if err != nil {
		t.Fatal(err)
	}
	route, err := a.Explain(ctx, app.Options{CWD: cwd, Demo: true})
	if err != nil {
		t.Fatal(err)
	}
	owner := core.Owner{ID: core.NewID("owner"), PID: os.Getpid(), StartedAt: time.Now().UTC()}
	input := "Synthetic completed task"
	accepted, _, err := a.Store.Accept(ctx, core.RunCommand{ID: "warning-replay", Input: input,
		Requested: core.Defaults(), Effective: route, Workspace: w, Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	warning := "Synthetic cleanup warning; result is complete.\x1b[2J"
	event, err := a.Store.AppendEvent(ctx, core.Event{TaskID: accepted.TaskID, AttemptID: accepted.ID, Type: "warning", Text: warning}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	finished, err := a.Store.Finish(ctx, accepted.ID, owner.ID, "complete", "Synthetic saved result", "")
	if err != nil || len(finished.Warnings) != 1 {
		t.Fatal("fixture warning not durable", err)
	}
	var out, diagnostic bytes.Buffer
	_, cancel := context.WithCancel(ctx)
	defer cancel()
	o := newOutput(&out, &diagnostic, false, cancel)
	if err := o.event(event); err != nil {
		t.Fatal(err)
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 || strings.Count(diagnostic.String(), "Warning:") != 1 || !strings.Contains(diagnostic.String(), safe(warning)) || strings.ContainsRune(diagnostic.String(), '\x1b') {
		t.Fatalf("live warning lost or unsafe: %q %q", out.String(), diagnostic.String())
	}
	code, result, progress := call(t, home, "", "run", "--demo", "--cwd", cwd, "--command-id", "warning-replay", input)
	if code != 0 || !strings.Contains(result, "Synthetic saved result") || strings.Count(progress, "Warning:") != 1 || !strings.Contains(progress, safe(warning)) {
		t.Fatalf("replayed warning disappeared or became failure: %d %s %s", code, result, progress)
	}
	code, result, progress = call(t, home, "", "tasks", "show", finished.TaskID)
	if code != 0 || progress != "" || !strings.Contains(result, "Warning: "+safe(warning)) || !strings.Contains(result, "| complete") {
		t.Fatalf("inspection lost warning: %d %s %s", code, result, progress)
	}
	_, history := taskReceipt(t, home, finished.TaskID)
	if len(history) != 1 || len(history[0].Warnings) != 1 || history[0].Warnings[0] != warning || history[0].ID != accepted.ID {
		t.Fatal("replay duplicated work or JSON lost durable warning")
	}
	if _, err := a.Draft(ctx, finished.TaskID, "Unsent follow-up", cwd, core.Defaults()); err != nil {
		t.Fatal(err)
	}
	code, result = runInteractive(t, home, cwd, "/result\n/exit\n", true)
	if code != 0 || !strings.Contains(result, "Warning: "+safe(warning)) || !strings.Contains(result, "Unsent follow-up") {
		t.Fatalf("interactive result lost warning or draft: %d %s", code, result)
	}
}
