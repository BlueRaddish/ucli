package cli

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

type monitorEventRead struct {
	attempt string
	after   int64
}

type monitorReadProbe struct {
	sessionMonitorSource
	reads  chan monitorEventRead
	before func(context.Context, string, int64) error
}

func (p *monitorReadProbe) AttemptEvents(ctx context.Context, attempt string, after int64, limit int) ([]core.Event, error) {
	select {
	case p.reads <- monitorEventRead{attempt, after}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p.before != nil {
		if err := p.before(ctx, attempt, after); err != nil {
			return nil, err
		}
	}
	return p.sessionMonitorSource.AttemptEvents(ctx, attempt, after, limit)
}

func catchupWorker(t *testing.T, source sessionMonitorSource) (context.Context, *sessionMonitorWorker, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	// No ordinary polling tick can explain a passing catch-up test.
	worker := newSessionMonitorWorker(ctx, source, time.Hour)
	t.Cleanup(func() {
		cancel()
		select {
		case <-worker.done:
		case <-time.After(time.Second):
			t.Error("catch-up worker outlived cancellation")
		}
	})
	return ctx, worker, cancel
}

func takeMonitorRead(t *testing.T, probe *monitorReadProbe, attempt string, after int64) {
	t.Helper()
	select {
	case got := <-probe.reads:
		if got != (monitorEventRead{attempt, after}) {
			t.Fatalf("cursor replayed, skipped or crossed attempt: %+v; want %s/%d", got, attempt, after)
		}
	case <-time.After(time.Second):
		t.Fatal("expected event read did not occur")
	}
}

func TestSessionMonitorCatchesUpDurablePagesWithoutIdleDelay(t *testing.T) {
	a, err := app.Open(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	cwd := t.TempDir()
	command := core.RunCommand{ID: "command-monitor-pages", Input: "Synthetic live monitor", Requested: core.Defaults(),
		Effective: core.Route{Runtime: "demo", Provider: "local", Model: "demo/fast", Account: "demo", Effort: "low", Permission: "read-only", Mode: "demo"},
		Workspace: core.Workspace{ID: "workspace-monitor", Root: cwd, Dir: cwd},
		Owner:     core.Owner{ID: "owner-monitor", PID: 123, StartedAt: time.Now().UTC()}}
	attempt, fresh, err := a.Store.Accept(context.Background(), command)
	if err != nil || !fresh {
		t.Fatal("synthetic running attempt not accepted", err)
	}
	for i := 0; i < 250; i++ {
		if _, err = a.Store.AppendEvent(context.Background(), core.Event{AttemptID: attempt.ID, Type: "message_delta", Text: "x"}, command.Owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	before, _, err := a.TaskPage(context.Background(), attempt.TaskID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	probe := &monitorReadProbe{sessionMonitorSource: a, reads: make(chan monitorEventRead, 8)}
	ctx, worker, _ := catchupWorker(t, probe)
	worker.requests <- sessionMonitorRequest{ctx: ctx, generation: 1, taskID: attempt.TaskID}
	result := awaitMonitorResult(t, worker, func(r sessionMonitorResult) bool { return r.snapshot.sequence == 251 })
	if result.err != nil || result.snapshot.more || result.snapshot.output != strings.Repeat("x", 250) || result.snapshot.attempt.State != "running" {
		t.Fatal("catch-up changed saved progress or fabricated completion", result)
	}
	for _, after := range []int64{0, 100, 200} {
		takeMonitorRead(t, probe, attempt.ID, after)
	}
	after, page, err := a.TaskPage(context.Background(), attempt.TaskID, 0, 1)
	if err != nil || !reflect.DeepEqual(before, after) || len(page.Attempts) != 1 || page.Attempts[0].State != "running" {
		t.Fatal("monitor mutated task, pins or execution state", err)
	}
}

func TestSessionMonitorManualRefreshKeepsCursorUntilViewOrAttemptChanges(t *testing.T) {
	source := monitorSourceFixture()
	probe := &monitorReadProbe{sessionMonitorSource: source, reads: make(chan monitorEventRead, 16)}
	ctx, worker, _ := catchupWorker(t, probe)
	request := func(generation uint64, task string) sessionMonitorSnapshot {
		worker.requests <- sessionMonitorRequest{ctx: ctx, generation: generation, taskID: task}
		result := awaitMonitorResult(t, worker, func(r sessionMonitorResult) bool { return r.request.generation == generation })
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result.snapshot
	}
	request(1, source.task.ID)
	takeMonitorRead(t, probe, source.attempt.ID, 0)
	if got := request(2, source.task.ID); got.output != "First output" || got.sequence != 2 {
		t.Fatal("refresh lost or duplicated displayed output", got)
	}
	takeMonitorRead(t, probe, source.attempt.ID, 2)
	source.mu.Lock()
	source.attempt.ID = "attempt-new"
	source.events = []core.Event{{TaskID: source.task.ID, AttemptID: source.attempt.ID, Sequence: 1, Type: "message_delta", Text: "New attempt only"}}
	source.mu.Unlock()
	if got := request(3, source.task.ID); got.output != "New attempt only" || got.sequence != 1 {
		t.Fatal("new attempt retained the prior attempt output", got)
	}
	takeMonitorRead(t, probe, "attempt-new", 0)
	source.mu.Lock()
	source.task.ID, source.attempt.TaskID = "task-new", "task-new"
	source.events[0].TaskID = "task-new"
	source.mu.Unlock()
	request(4, "task-new")
	takeMonitorRead(t, probe, "attempt-new", 0)
	request(5, "") // Returning to the list ends the detail cursor's lifetime.
	request(6, "task-new")
	takeMonitorRead(t, probe, "attempt-new", 0)
}

func queuedMonitorSource() *monitorFixture {
	source := monitorSourceFixture()
	source.events = nil
	for i := int64(1); i <= 251; i++ {
		source.events = append(source.events, core.Event{TaskID: source.task.ID, AttemptID: source.attempt.ID, Sequence: i, Type: "message_delta", Text: "x"})
	}
	return source
}

func TestSessionMonitorCatchupFailureRetainsCursorAndUsesIdleCadence(t *testing.T) {
	for _, mode := range []string{"read-error", "bad-sequence"} {
		t.Run(mode, func(t *testing.T) {
			source := queuedMonitorSource()
			probe := &monitorReadProbe{sessionMonitorSource: source, reads: make(chan monitorEventRead, 16)}
			if mode == "read-error" {
				probe.before = func(_ context.Context, _ string, after int64) error {
					if after == 100 {
						return errors.New("synthetic read failure")
					}
					return nil
				}
			} else {
				source.events[100].Sequence = 999
			}
			ctx, worker, _ := catchupWorker(t, probe)
			worker.requests <- sessionMonitorRequest{ctx: ctx, generation: 1, taskID: source.task.ID}
			result := awaitMonitorResult(t, worker, func(r sessionMonitorResult) bool { return r.err != nil })
			if result.snapshot.sequence != 100 || result.snapshot.output != strings.Repeat("x", 100) {
				t.Fatal("failed read replaced or advanced the last good snapshot", result)
			}
			takeMonitorRead(t, probe, source.attempt.ID, 0)
			takeMonitorRead(t, probe, source.attempt.ID, 100)
			select {
			case call := <-probe.reads:
				t.Fatal("failed catch-up spun without waiting for normal refresh", call)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

func TestSessionMonitorCancellationInterruptsCatchupPage(t *testing.T) {
	source := queuedMonitorSource()
	started := make(chan struct{})
	probe := &monitorReadProbe{sessionMonitorSource: source, reads: make(chan monitorEventRead, 8), before: func(ctx context.Context, _ string, after int64) error {
		if after == 100 {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	ctx, worker, cancel := catchupWorker(t, probe)
	worker.requests <- sessionMonitorRequest{ctx: ctx, generation: 1, taskID: source.task.ID}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("catch-up never reached the cancellable second page")
	}
	cancel()
	select {
	case <-worker.done:
	case <-time.After(time.Second):
		t.Fatal("worker kept reading after cancellation")
	}
	takeMonitorRead(t, probe, source.attempt.ID, 0)
	takeMonitorRead(t, probe, source.attempt.ID, 100)
	select {
	case call := <-probe.reads:
		t.Fatal("another page began after cancellation", call)
	default:
	}
}

func TestSessionMonitorSupersededCatchupPublishesOnlyCurrentView(t *testing.T) {
	source := queuedMonitorSource()
	started := make(chan struct{})
	probe := &monitorReadProbe{sessionMonitorSource: source, reads: make(chan monitorEventRead, 8), before: func(ctx context.Context, _ string, after int64) error {
		if after == 100 {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	ctx, worker, _ := catchupWorker(t, probe)
	detailCtx, cancelDetail := context.WithCancel(ctx)
	defer cancelDetail()
	worker.requests <- sessionMonitorRequest{ctx: detailCtx, generation: 1, taskID: source.task.ID}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("catch-up did not reach the second page")
	}
	// Back cancels the old detail read before posting the new list request.
	cancelDetail()
	worker.requests <- sessionMonitorRequest{ctx: ctx, generation: 2}
	result := awaitMonitorResult(t, worker, func(r sessionMonitorResult) bool { return r.request.generation == 2 })
	if result.err != nil || result.snapshot.output != "" || result.snapshot.sequence != 0 || len(result.snapshot.page.Tasks) != 1 {
		t.Fatal("superseded detail contaminated the new list", result)
	}
	takeMonitorRead(t, probe, source.attempt.ID, 0)
	takeMonitorRead(t, probe, source.attempt.ID, 100)
	select {
	case call := <-probe.reads:
		t.Fatal("another old detail page began after Back", call)
	default:
	}
}
