package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/workspace"
)

func TestLargeSavedResultsRemainReadableWithoutTruncation(t *testing.T) {
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
	input, output := strings.Repeat("i", core.MaxInputBytes), strings.Repeat("x", core.MaxOutputBytes)
	var last core.Attempt
	var ids []string
	for range 4 {
		cmd := core.RunCommand{ID: core.NewID("command"), TaskID: last.TaskID, ExpectedRevision: last.Revision, Input: input,
			Requested: core.Defaults(), Effective: route, Workspace: w, Owner: owner}
		accepted, _, err := a.Store.Accept(ctx, cmd)
		if err != nil {
			t.Fatal(err)
		}
		last, err = a.Store.Finish(ctx, accepted.ID, owner.ID, "complete", output, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, last.ID)
	}
	// Negative control proves this fixture reaches the real aggregate page cap.
	_, _, err = a.TaskPage(ctx, last.TaskID, 0, 5)
	var problem *core.Error
	if !errors.As(err, &problem) || problem.Code != "history_too_large" {
		t.Fatalf("fixture missed history cap: %v", err)
	}
	draft, err := a.Draft(ctx, last.TaskID, "Large history must not lose this draft", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	before := 0
	seen := map[string]bool{}
	for pageNumber := range 2 {
		code, text, diagnostic := call(t, home, "", "tasks", "show", last.TaskID, "--before-revision", fmt.Sprint(before), "--json")
		if code != 0 || diagnostic != "" {
			t.Fatalf("large history failed: code=%d diagnostic=%s", code, diagnostic)
		}
		var page struct {
			Task       core.Task         `json:"task"`
			Attempts   []core.Attempt    `json:"attempts"`
			Pagination historyPagination `json:"pagination"`
		}
		if err := json.Unmarshal(jsonLines(t, text)[0].Data, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Attempts) != 2 || (pageNumber == 0 && page.Pagination.Limit != 2) || page.Task.Draft != draft.Draft {
			t.Fatalf("unexpected bounded page: count=%d metadata=%+v", len(page.Attempts), page.Pagination)
		}
		for _, attempt := range page.Attempts {
			if seen[attempt.ID] || attempt.Input != input || attempt.Output != output {
				t.Fatal("history repeated or truncated a complete attempt")
			}
			seen[attempt.ID] = true
		}
		before = page.Pagination.NextBeforeRevision
	}
	if before != 0 || len(seen) != 4 {
		t.Fatalf("lost older evidence: cursor=%d count=%d", before, len(seen))
	}
	code, text := runInteractive(t, home, cwd, "/result\n/older\n/exit\n", true)
	if code != 0 || !strings.Contains(text, "Showing 2 saved attempts") || !strings.Contains(text, "Older: /older") {
		t.Fatalf("interactive large history failed: exit=%d", code)
	}
	for _, id := range ids {
		if strings.Count(text, "Attempt "+id+" |") != 1 {
			t.Fatalf("interactive history skipped or repeated %s", id)
		}
	}
	stored, err := a.Store.GetTask(ctx, draft.ID)
	if err != nil || stored.Draft != draft.Draft || stored.Revision != draft.Revision {
		t.Fatal("reading large history changed the active draft", err)
	}
}
