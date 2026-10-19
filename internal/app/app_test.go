package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
)

func fixtureApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	cwd := filepath.Join(dir, "workspace")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := Open(filepath.Join(dir, "home"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, cwd
}
func TestDurableLoopFollowupAndReplay(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	var events []core.Event
	opt := Options{Input: "Review the architecture", CWD: cwd, Demo: true, CommandID: "first-command", Selection: core.Defaults()}
	attempt, err := a.Run(ctx, opt, func(e core.Event) error { events = append(events, e); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if attempt.State != "complete" || attempt.Effective.Model != "demo/deep" {
		t.Fatalf("unexpected result: %+v", attempt)
	}
	if len(events) < 4 || events[0].Type != "accepted" || events[len(events)-1].Type != "result_persisted" {
		t.Fatalf("events: %+v", events)
	}
	for i, e := range events {
		if e.Sequence != int64(i+1) {
			t.Fatal("nonmonotonic persisted sequence", events)
		}
	}
	replay, err := a.Run(ctx, opt, nil)
	if err != nil || replay.ID != attempt.ID {
		t.Fatal("replay changed execution", replay, err)
	}
	opt.Input = "a different intent"
	if _, err = a.Run(ctx, opt, nil); err == nil {
		t.Fatal("changed command intent accepted")
	}
	follow, err := a.Run(ctx, Options{Input: "Now inspect the tests", TaskID: attempt.TaskID, Demo: true, Selection: core.Defaults()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if follow.TaskID != attempt.TaskID || follow.ID == attempt.ID || follow.Revision <= attempt.Revision {
		t.Fatal("followup lost identity/revision")
	}
	_, history, err := a.Task(ctx, attempt.TaskID)
	if err != nil || len(history) != 2 || history[0].Output != attempt.Output {
		t.Fatal("old evidence changed", history, err)
	}
}
func TestUnavailableProviderPreservesDraft(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	_, err := a.Run(ctx, Options{Input: "Fix the failing test", CWD: cwd, Selection: core.Defaults()}, nil)
	var problem *core.Error
	if !errors.As(err, &problem) || problem.Exit != 3 || problem.TaskID == "" {
		t.Fatal("missing actionable preserved draft", err)
	}
	task, history, err := a.Task(ctx, problem.TaskID)
	if err != nil || task.Draft != "Fix the failing test" || len(history) != 0 {
		t.Fatal("draft dispatched/lost", task, history, err)
	}
}
func TestPinsAndWorkspaceAreNeverBroadened(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	for _, selection := range []core.Selection{{Model: "other"}, {Model: "demo/fast", Effort: "high"}, {Account: "work"}, {Permission: "workspace-write"}} {
		if _, err := a.Explain(ctx, Options{Input: "hello", Demo: true, Selection: selection}); err == nil {
			t.Fatal("pin silently substituted", selection)
		}
	}
	first, err := a.Run(ctx, Options{Input: "hello", CWD: cwd, Demo: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if _, err := a.Run(ctx, Options{Input: "again", TaskID: first.TaskID, CWD: other, Demo: true}, nil); err == nil {
		t.Fatal("task crossed workspace silently")
	}
}
func TestCanceledAndBrokenDeliveryRetainAttempt(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	count := 0
	result, err := a.Run(ctx, Options{Input: "local task", CWD: cwd, Demo: true}, func(e core.Event) error {
		count++
		if e.Type == "accepted" {
			cancel()
		}
		return nil
	})
	if err == nil || result.State != "interrupted" || count < 1 {
		t.Fatal("cancellation not durable", result, err)
	}
	_, history, loadErr := a.Task(context.Background(), result.TaskID)
	if loadErr != nil || history[0].State != "interrupted" {
		t.Fatal(history, loadErr)
	}
	result, err = a.Run(context.Background(), Options{Input: "broken output", CWD: cwd, Demo: true}, func(core.Event) error { return errors.New("output closed") })
	if err == nil || result.State != "interrupted" {
		t.Fatal("broken initial delivery dispatched", result, err)
	}
}
func TestDraftAndNoInputRecoveryDoNotDispatch(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	task, err := a.Draft(ctx, "", "unsent text", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Recover(ctx, task.ID); err == nil {
		t.Fatal("draft was resumed as execution")
	}
	task, err = a.Draft(ctx, task.ID, "", cwd, core.Defaults())
	if err != nil || task.Draft != "" {
		t.Fatal(task, err)
	}
}

func TestFollowupInheritsPinsAndExplicitAutoCanReset(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	first, err := a.Run(ctx, Options{Input: "hello", CWD: cwd, Demo: true, Selection: core.Selection{Model: "demo/deep", Effort: "high"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := a.Run(ctx, Options{Input: "hello again", TaskID: first.TaskID, Demo: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Requested.Model != "demo/deep" || next.Effective.Effort != "high" {
		t.Fatal("followup lost task pins", next)
	}
	reset, err := a.Run(ctx, Options{Input: "hello", TaskID: first.TaskID, Demo: true, Selection: core.Selection{Model: "auto", Effort: "auto"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Effective.Model != "demo/fast" || reset.Effective.Effort != "medium" {
		t.Fatal("explicit auto did not reset", reset)
	}
}

func TestStalledPresentationCannotBlockDurableLifecycle(t *testing.T) {
	a, cwd := fixtureApp(t)
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	attempt, err := a.Run(context.Background(), Options{Input: "blocked reader", CWD: cwd, Demo: true}, func(core.Event) error { <-release; return nil })
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatal("callback blocked lifecycle", attempt, err)
	}
	if attempt.State != "interrupted" {
		t.Fatal("accepted work did not settle safely", attempt)
	}
	_, history, loadErr := a.Task(context.Background(), attempt.TaskID)
	if loadErr != nil || len(history) != 1 || history[0].State != "interrupted" {
		t.Fatal("state not inspectable", history, loadErr)
	}
}

func TestStaleClientEditPreservesBothDrafts(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	first, err := a.Draft(ctx, "", "initial", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	viewRevision := first.Revision
	newer, err := a.DraftAt(ctx, first.ID, "newer terminal edit", cwd, core.Defaults(), &viewRevision)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DraftAt(ctx, first.ID, "stale submitted text", cwd, core.Defaults(), &viewRevision)
	var problem *core.Error
	if !errors.As(err, &problem) || problem.Code != "revision_conflict" || problem.TaskID == "" || problem.TaskID == first.ID {
		t.Fatal("conflict lost recovery draft", err)
	}
	current, _, err := a.Task(ctx, first.ID)
	if err != nil || current.Draft != newer.Draft {
		t.Fatal("stale edit replaced current text", current, err)
	}
	recovery, history, err := a.Task(ctx, problem.TaskID)
	if err != nil || recovery.Draft != "stale submitted text" || len(history) != 0 {
		t.Fatal("recovery was lost/dispatched", recovery, history, err)
	}
}
