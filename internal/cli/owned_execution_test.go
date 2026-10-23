package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/workspace"
)

// Controlled UI handle; App coordinator tests separately exercise actual owned
// drivers. This fixture never claims that a recorded running row is a worker.
type controlledUIExecution struct {
	done    chan struct{}
	entered chan struct{}
	once    sync.Once
	result  core.Attempt
	err     error
	waited  chan struct{}
	waitOne sync.Once
	stopped chan struct{}
	cancels atomic.Int32
}

func (e *controlledUIExecution) Done() <-chan struct{} { return e.done }
func (e *controlledUIExecution) Cancel() error {
	e.cancels.Add(1)
	if e.stopped != nil {
		select {
		case e.stopped <- struct{}{}:
		default:
		}
	}
	return nil
}
func (e *controlledUIExecution) Wait(ctx context.Context) (core.Attempt, error) {
	if e.waited != nil {
		e.waitOne.Do(func() { close(e.waited) })
	}
	select {
	case <-e.done:
		return e.result, e.err
	case <-ctx.Done():
		return core.Attempt{}, ctx.Err()
	}
}
func (e *controlledUIExecution) Follow(ctx context.Context, after int64, emit func(core.Event) error) error {
	e.once.Do(func() { close(e.entered) })
	select {
	case <-e.done:
		return emit(core.Event{Version: 1, TaskID: e.result.TaskID, AttemptID: e.result.ID, Sequence: after + 1, Type: "message_delta", Text: e.result.Output})
	case <-ctx.Done():
		return ctx.Err()
	}
}

func managedUIFixture(t *testing.T) (*session, context.Context, *controlledUIExecution, core.Attempt, context.CancelFunc) {
	t.Helper()
	s, ctx := newEnterSession(t, true)
	observing, stop := context.WithCancel(ctx)
	s.observeCtx, s.out.control = observing, context.Background()
	t.Cleanup(func() { stop(); s.observers.Wait() })
	w, err := workspace.Resolve(s.cwd)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := s.app.Draft(ctx, "", "First accepted prompt", s.cwd, s.selection)
	if err != nil {
		t.Fatal(err)
	}
	owner := core.Owner{ID: "ui-controller-fixture", PID: 1, StartedAt: time.Now().UTC()}
	attempt, fresh, err := s.app.Store.Accept(ctx, core.RunCommand{ID: "ui-controller-command", TaskID: seed.ID, ExpectedRevision: seed.Revision,
		Input: seed.Draft, Requested: seed.Selection, Workspace: w, Owner: owner,
		Effective: core.Route{Runtime: "demo", Model: "demo/fast", Account: "demo", Effort: "low", Permission: "read-only", Mode: "demo"}})
	if err != nil || !fresh {
		t.Fatalf("fixture acceptance: fresh=%v err=%v", fresh, err)
	}
	if s.task, err = taskPointer(s.app, ctx, attempt.TaskID); err != nil {
		t.Fatal(err)
	}
	e := &controlledUIExecution{done: make(chan struct{}), entered: make(chan struct{}), waited: make(chan struct{}), stopped: make(chan struct{}, 1), result: attempt}
	s.trackExecution(e, attempt)
	select {
	case <-e.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return s, ctx, e, attempt, stop
}

func taskPointer(a *app.App, ctx context.Context, id string) (*core.Task, error) {
	task, _, err := a.Task(ctx, id)
	return &task, err
}

func TestOwnedSessionBusyEnterPreservesDraftWithoutQueuing(t *testing.T) {
	s, ctx, execution, accepted, _ := managedUIFixture(t)
	const next = "Keep this next prompt unsent"
	err := s.submitPrompt(ctx, next, true)
	if err == nil || problemFor(err).Code != "task_busy" {
		t.Fatalf("busy input was not explained: %v", err)
	}
	task, attempts, err := s.app.Task(ctx, accepted.TaskID)
	if err != nil || task.Draft != next || len(attempts) != 1 || attempts[0].Input != accepted.Input || !reflect.DeepEqual(attempts[0].Effective, accepted.Effective) {
		t.Fatalf("busy input changed accepted work or lost draft: %+v %+v %v", task, attempts, err)
	}
	if _, err := s.action(ctx, "/stop"); err != nil || execution.cancels.Load() != 1 {
		t.Fatalf("stop did not target the owned execution: count=%d err=%v", execution.cancels.Load(), err)
	}
	task, attempts, err = s.app.Task(ctx, accepted.TaskID)
	if err != nil || task.Draft != next || attempts[0].State != "running" {
		t.Fatal("requesting stop invented a settled state or erased input")
	}
}

func TestOwnedSessionObserverDetachDoesNotRequestStop(t *testing.T) {
	s, _, execution, _, stop := managedUIFixture(t)
	stop()
	s.observers.Wait()
	if execution.cancels.Load() != 0 {
		t.Fatal("closing an observer canceled execution")
	}
	select {
	case <-execution.Done():
		t.Fatal("closing an observer invented completion")
	default:
	}
}

func TestOwnedSessionLateCompletionKeepsNewTaskAndComposition(t *testing.T) {
	s, ctx, execution, accepted, _ := managedUIFixture(t)
	if _, err := s.action(ctx, "/new"); err != nil {
		t.Fatal(err)
	}
	if err := s.storeDraft(ctx, "My new task's unsent composition", false); err != nil {
		t.Fatal(err)
	}
	want := *s.task
	finished, err := s.app.Store.Finish(ctx, accepted.ID, accepted.Owner.ID, "complete", "Old task output", "")
	if err != nil {
		t.Fatal(err)
	}
	execution.result = finished
	close(execution.done)
	s.observers.Wait()
	if s.task.ID != want.ID || s.task.Revision != want.Revision || s.task.Draft != want.Draft || s.selection != want.Selection {
		t.Fatalf("old completion replaced current context: got=%+v want=%+v", s.task, want)
	}
	stored, attempts, err := s.app.Task(ctx, want.ID)
	if err != nil || stored.Draft != want.Draft || len(attempts) != 0 {
		t.Fatal("late completion sent or altered the new draft")
	}
	if err := s.stopCurrentExecution(); err == nil || problemFor(err).Code != "no_owned_execution" {
		t.Fatalf("stop crossed task identity: %v", err)
	}
}

func TestOwnedSessionStartReturnsAcceptedHandleAndKeepsNavigation(t *testing.T) {
	s, ctx := newEnterSession(t, true)
	observing, stop := context.WithCancel(ctx)
	s.observeCtx, s.out.control = observing, context.Background()
	t.Cleanup(func() { stop(); s.observers.Wait() })
	if err := s.submitPrompt(ctx, "Actual App demo through managed submit", true); err != nil {
		t.Fatal(err)
	}
	if len(s.runs) != 1 || s.task == nil || s.task.Draft != "" {
		t.Fatal("submit did not retain exactly one accepted handle")
	}
	for _, run := range s.runs {
		attempt, err := run.execution.Wait(ctx)
		if err != nil || attempt.State != "complete" {
			t.Fatalf("demo worker did not persist completion: %+v %v", attempt, err)
		}
	}
	if _, err := s.action(ctx, "/new"); err != nil || s.task != nil {
		t.Fatal("managed submit blocked new-task navigation")
	}
}

func TestForegroundObserverFailureRetainsCompletedReceipt(t *testing.T) {
	s, ctx := newEnterSession(t, true)
	s.out.control = context.Background()
	broken := errors.New("observer disconnected")
	attempt, err := observeForeground(ctx, s.app, app.Options{Input: "broken observer", CWD: s.cwd, Demo: true}, s.out, func(core.Event) error { return broken })
	if !errors.Is(err, broken) || attempt.State != "complete" || attempt.Output == "" {
		t.Fatalf("observer failure stopped accepted work: %+v %v", attempt, err)
	}
}

func TestOwnedSessionOutputFailureWaitKeepsExplicitInterruption(t *testing.T) {
	s, ctx, execution, _, stopObserving := managedUIFixture(t)
	stopObserving()
	s.observers.Wait()
	control, interrupt := context.WithCancel(ctx)
	defer interrupt()
	s.out.control = control
	drained := make(chan struct{})
	go func() { s.waitAfterOutputFailure(); close(drained) }()
	select {
	case <-execution.waited:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if execution.cancels.Load() != 0 {
		t.Fatal("reader failure requested stop")
	}
	select {
	case <-drained:
		t.Fatal("owner returned while accepted work remained")
	default:
	}
	interrupt()
	select {
	case <-execution.stopped:
	case <-ctx.Done():
		t.Fatal("explicit interruption did not request stop")
	}
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatal("unresponsive worker prevented bounded host shutdown")
	}
}

func TestOwnedSessionFailureExplainsCapturedTaskWithoutReplacingDraft(t *testing.T) {
	s, ctx, execution, accepted, _ := managedUIFixture(t)
	var transcript bytes.Buffer
	s.out.out, s.out.errOut = &transcript, &transcript
	execution.err = core.Problem("runtime_failed", "The controlled runtime failed; inspect its saved attempt.", 8)
	failed, err := s.app.Store.Finish(ctx, accepted.ID, accepted.Owner.ID, "failed", "", execution.err.Error())
	if err != nil {
		t.Fatal(err)
	}
	execution.result = failed
	close(execution.done)
	s.observers.Wait()
	if err := s.out.finish(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(transcript.String(), accepted.TaskID) || !strings.Contains(transcript.String(), "The controlled runtime failed") {
		t.Fatalf("asynchronous failure lost its identity or reason: %q", transcript.String())
	}
	if s.task.ID != accepted.TaskID || s.task.Draft != "" {
		t.Fatal("failure changed the active composition")
	}
}
