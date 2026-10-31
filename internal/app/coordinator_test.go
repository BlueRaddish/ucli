package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
)

func awaitExecution(t *testing.T, e *Execution) (core.Attempt, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return e.Wait(ctx)
}

func startFixture(t *testing.T, a *App, cwd, command string, driver driverFunc) *Execution {
	t.Helper()
	e, err := a.start(context.Background(), Options{Input: "owned fixture", CommandID: command, CWD: cwd, Demo: true}, driver)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestExecutionObservationDetachesWithoutCanceling(t *testing.T) {
	a, cwd := fixtureApp(t)
	admission, cancelAdmission := context.WithCancel(context.Background())
	defer cancelAdmission()
	release := make(chan struct{})
	observedCancel := make(chan struct{}, 1)
	e, err := a.start(admission, Options{Input: "independent observation", CWD: cwd, Demo: true}, driverFunc(func(ctx context.Context, _ adapters.Request, emit func(string) error) (adapters.Outcome, error) {
		if err := emit("first"); err != nil {
			return adapters.Outcome{State: "failed"}, err
		}
		select {
		case <-ctx.Done():
			observedCancel <- struct{}{}
			return adapters.Outcome{State: "interrupted"}, ctx.Err()
		case <-release:
		}
		if err := emit(" second"); err != nil {
			return adapters.Outcome{State: "failed"}, err
		}
		return adapters.Outcome{State: "complete"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	cancelAdmission() // after acceptance does not cancel owned work
	waitCtx, stopWait := context.WithCancel(context.Background())
	stopWait()
	if _, err = e.Wait(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatal("wait did not detach", err)
	}
	wanted := errors.New("synthetic observer closed")
	var cursor int64
	followCtx, stopFollow := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopFollow()
	err = e.Follow(followCtx, 0, func(event core.Event) error {
		if event.Type == "message_delta" {
			return wanted
		}
		cursor = event.Sequence
		return nil
	})
	if !errors.Is(err, wanted) {
		t.Fatal(err)
	}
	select {
	case <-observedCancel:
		t.Fatal("observer/wait/admission cancellation reached driver")
	default:
	}
	close(release)
	result, err := awaitExecution(t, e)
	if err != nil || result.State != "complete" || result.Output != "first second" {
		t.Fatal(result, err)
	}
	var replay []core.Event
	if err = e.Follow(context.Background(), cursor, func(event core.Event) error { replay = append(replay, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(replay) < 4 || replay[0].Sequence != cursor+1 || replay[len(replay)-1].Type != "result_persisted" {
		t.Fatal(replay)
	}
	for i, event := range replay {
		if event.Sequence != cursor+int64(i)+1 {
			t.Fatal("cursor gap", replay)
		}
	}
}

func TestExecutionStalledObserverDoesNotHoldWorkerOrStore(t *testing.T) {
	a, cwd := fixtureApp(t)
	blocked, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	e := startFixture(t, a, cwd, "stalled-observer", func(ctx context.Context, _ adapters.Request, emit func(string) error) (adapters.Outcome, error) {
		<-blocked
		if err := emit("durable despite stalled observer"); err != nil {
			return adapters.Outcome{State: "failed"}, err
		}
		return adapters.Outcome{State: "complete"}, nil
	})
	followDone := make(chan error, 1)
	go func() {
		followDone <- e.Follow(context.Background(), 0, func(core.Event) error { close(blocked); <-release; return nil })
	}()
	result, err := awaitExecution(t, e)
	if err != nil || result.State != "complete" {
		t.Fatal(result, err)
	}
	select {
	case err = <-followDone:
		var p *core.Error
		if !errors.As(err, &p) || p.Code != "output_stalled" {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("observer wait was not bounded")
	}
	if err = a.closeWithin(time.Second); err != nil {
		t.Fatal("stalled observer held store close", err)
	}
}

func TestExecutionReplayDoesNotAdoptOrDuplicate(t *testing.T) {
	a, cwd := fixtureApp(t)
	other, err := Open(a.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	release := make(chan struct{})
	var calls atomic.Int32
	opt := Options{Input: "owned fixture", CWD: cwd, Demo: true, CommandID: "one-owner"}
	driver := driverFunc(func(ctx context.Context, _ adapters.Request, emit func(string) error) (adapters.Outcome, error) {
		calls.Add(1)
		select {
		case <-ctx.Done():
			return adapters.Outcome{State: "interrupted"}, ctx.Err()
		case <-release:
		}
		if err := emit("only once"); err != nil {
			return adapters.Outcome{State: "failed"}, err
		}
		return adapters.Outcome{State: "complete"}, nil
	})
	e, err := a.start(context.Background(), opt, driver)
	if err != nil {
		t.Fatal(err)
	}
	local, err := a.start(context.Background(), opt, driver)
	if err != nil || !local.Replayed() || local.work != e.work {
		t.Fatal("local replay lost original worker", err)
	}
	foreign, err := other.start(context.Background(), opt, driver)
	if err != nil || !foreign.Replayed() || foreign.work != nil {
		t.Fatal("foreign worker adopted", err)
	}
	if err = foreign.Cancel(); err == nil {
		t.Fatal("foreign replay canceled worker")
	}
	if _, err = foreign.Wait(context.Background()); err == nil {
		t.Fatal("foreign running receipt claimed completion")
	}
	opt.Input = "changed intent"
	if _, err = a.start(context.Background(), opt, driver); err == nil {
		t.Fatal("changed replay dispatched")
	}
	close(release)
	result, err := awaitExecution(t, e)
	if err != nil || result.State != "complete" || calls.Load() != 1 {
		t.Fatal(result, err, calls.Load())
	}
	var events []core.Event
	if err = foreign.Follow(context.Background(), 0, func(event core.Event) error { events = append(events, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if events[len(events)-1].Type != "result_persisted" {
		t.Fatal("foreign read missed completion", events)
	}
	accepted := e.Accepted()
	accepted.Effective.Reasons[0] = "mutated caller copy"
	if reflect.DeepEqual(accepted, e.Accepted()) {
		t.Fatal("accepted receipt shares mutable storage")
	}
}

func TestExecutionCancelPreservesUnknownFence(t *testing.T) {
	for _, state := range []string{"interrupted", "unknown"} {
		t.Run(state, func(t *testing.T) {
			a, cwd := fixtureApp(t)
			entered := make(chan struct{})
			e := startFixture(t, a, cwd, "cancel-target", func(ctx context.Context, _ adapters.Request, emit func(string) error) (adapters.Outcome, error) {
				if err := emit("partial"); err != nil {
					return adapters.Outcome{State: "failed"}, err
				}
				close(entered)
				<-ctx.Done()
				return adapters.Outcome{State: state}, ctx.Err()
			})
			<-entered
			if err := e.Cancel(); err != nil {
				t.Fatal(err)
			}
			result, err := awaitExecution(t, e)
			if err == nil || result.State != state || result.StopCause != "canceled" || result.Output != "partial" {
				t.Fatal(result, err)
			}
			if state == "unknown" {
				if _, err = a.Start(context.Background(), Options{TaskID: result.TaskID, Input: "must not replay", Demo: true}); err == nil {
					t.Fatal("unknown task fence released")
				}
			}
		})
	}
}

func TestExecutionDeadlineSurvivesAdmissionContextDetach(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	e, err := a.start(ctx, Options{Input: "deadline fixture", Demo: true, CWD: cwd}, driverFunc(func(ctx context.Context, _ adapters.Request, _ func(string) error) (adapters.Outcome, error) {
		<-ctx.Done()
		return adapters.Outcome{State: "interrupted"}, ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := awaitExecution(t, e)
	var p *core.Error
	if !errors.As(err, &p) || p.Exit != 124 || result.StopCause != "deadline" {
		t.Fatal(result, err)
	}
}

func TestExecutionCloseRetainsStoreUntilUnresponsiveWorkerJoins(t *testing.T) {
	a, cwd := fixtureApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	e := startFixture(t, a, cwd, "close-order", func(ctx context.Context, _ adapters.Request, _ func(string) error) (adapters.Outcome, error) {
		close(entered)
		<-release
		return adapters.Outcome{State: "interrupted"}, ctx.Err()
	})
	<-entered
	err := a.closeWithin(20 * time.Millisecond)
	var p *core.Error
	if !errors.As(err, &p) || p.Code != "execution_shutdown_pending" {
		t.Fatal(err)
	}
	if _, err = a.Store.GetAttempt(context.Background(), e.Accepted().ID); err != nil {
		t.Fatal("store closed under worker", err)
	}
	if _, err = a.Start(context.Background(), Options{Input: "not accepted", Demo: true, CWD: cwd}); !errors.As(err, &p) || p.Code != "app_closing" {
		t.Fatal(err)
	}
	close(release)
	result, _ := awaitExecution(t, e)
	if result.State != "interrupted" {
		t.Fatal("worker could not persist before close", result)
	}
	if err = a.closeWithin(time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.GetAttempt(context.Background(), result.ID); err == nil {
		t.Fatal("joined host left store open")
	}
}

func TestExecutionBurstFollowAndCursorValidation(t *testing.T) {
	a, cwd := fixtureApp(t)
	release := make(chan struct{})
	var emitted atomic.Int32
	e := startFixture(t, a, cwd, "burst", func(ctx context.Context, _ adapters.Request, emit func(string) error) (adapters.Outcome, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return adapters.Outcome{State: "interrupted"}, ctx.Err()
		}
		for i := 0; i < 251; i++ {
			if err := emit(fmt.Sprintf("%d,", i)); err != nil {
				return adapters.Outcome{State: "failed"}, err
			}
			emitted.Add(1)
		}
		return adapters.Outcome{State: "complete"}, nil
	})
	// This is a delivery/cursor test, not a latency benchmark. Allow bounded
	// time for 251 real durable writes plus concurrent reads on slower CI disks.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var events []core.Event
	err := e.Follow(ctx, 0, func(event core.Event) error {
		events = append(events, event)
		if event.Type == "accepted" {
			close(release)
		}
		return nil
	})
	if err != nil || len(events) != 254 || events[len(events)-1].Type != "result_persisted" {
		t.Fatalf("burst or completion lost: observed=%d emitted=%d err=%v", len(events), emitted.Load(), err)
	}
	for i, event := range events {
		if event.Sequence != int64(i)+1 {
			t.Fatal("event gap", event)
		}
		if i >= 1 && i <= 251 && (event.Type != "message_delta" || event.Text != fmt.Sprintf("%d,", i-1)) {
			t.Fatal("burst changed or omitted a message", event)
		}
	}
	cursorCtx, cancelCursor := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCursor()
	for _, cursor := range []struct {
		sequence int64
		code     string
		exit     int
	}{{-1, "invalid_input", 2}, {9999, "event_cursor_invalid", 9}} {
		var problem *core.Error
		if err := e.Follow(cursorCtx, cursor.sequence, nil); !errors.As(err, &problem) || problem.Code != cursor.code || problem.Exit != cursor.exit {
			t.Fatalf("wrong invalid-cursor outcome for %d: %v", cursor.sequence, err)
		}
	}
}

func TestExecutionLimitsAreBoundedAndRelease(t *testing.T) {
	a, cwd := fixtureApp(t)
	var executions []*Execution
	for i := 0; i < maxExecutions; i++ {
		e := startFixture(t, a, cwd, fmt.Sprintf("bounded-%d", i), func(ctx context.Context, _ adapters.Request, _ func(string) error) (adapters.Outcome, error) {
			<-ctx.Done()
			return adapters.Outcome{State: "interrupted"}, ctx.Err()
		})
		executions = append(executions, e)
	}
	_, err := a.Start(context.Background(), Options{Input: "overflow", CWD: cwd, Demo: true})
	var p *core.Error
	if !errors.As(err, &p) || p.Code != "execution_limit" {
		t.Fatal(err)
	}
	// Existing commands remain inspectable while all dispatch slots are full.
	replay, err := a.Start(context.Background(), Options{Input: "owned fixture", CommandID: "bounded-0", CWD: cwd, Demo: true})
	if err != nil || !replay.Replayed() || replay.Accepted().ID != executions[0].Accepted().ID {
		t.Fatal("worker capacity blocked a durable replay", err)
	}
	for _, e := range executions {
		_ = e.Cancel()
	}
	for _, e := range executions {
		_, _ = awaitExecution(t, e)
	}
	// Close joins resource release, not just terminal receipt visibility.
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionPreservesNewerDraft(t *testing.T) {
	a, cwd := fixtureApp(t)
	release := make(chan struct{})
	e := startFixture(t, a, cwd, "draft-survives", func(ctx context.Context, _ adapters.Request, _ func(string) error) (adapters.Outcome, error) {
		<-release
		return adapters.Outcome{State: "complete"}, nil
	})
	accepted := e.Accepted()
	revision := accepted.Revision
	draft, err := a.DraftAt(context.Background(), accepted.TaskID, "unsent newer composition", cwd, core.Selection{Model: "demo/deep"}, &revision)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	result, err := awaitExecution(t, e)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := a.Task(context.Background(), accepted.TaskID)
	if err != nil || task.Draft != draft.Draft || task.Revision != draft.Revision || task.Selection != draft.Selection || result.Revision != accepted.Revision || !reflect.DeepEqual(result.Effective, accepted.Effective) {
		t.Fatal("background completion replaced newer editing context", task, result, err)
	}
}

func TestForegroundCancellationJoinIsBounded(t *testing.T) {
	a, cwd := fixtureApp(t)
	release := make(chan struct{})
	e := startFixture(t, a, cwd, "foreground-unresponsive", func(ctx context.Context, _ adapters.Request, _ func(string) error) (adapters.Outcome, error) {
		<-release
		return adapters.Outcome{State: "unknown"}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := waitForeground(ctx, e, 20*time.Millisecond)
	var p *core.Error
	if !errors.As(err, &p) || p.Code != "execution_shutdown_pending" || result.State != "running" {
		t.Fatal(result, err)
	}
	close(release)
	result, err = awaitExecution(t, e)
	if result.State != "unknown" || err == nil {
		t.Fatal("unconfirmed stop was settled", result, err)
	}
}

func TestExecutionFollowRefusesForeignAndNoncontiguousEvents(t *testing.T) {
	for _, field := range []string{"attempt_id", "task_id", "sequence", "version"} {
		t.Run(field, func(t *testing.T) {
			a, cwd := fixtureApp(t)
			e, err := a.Start(context.Background(), Options{Input: "synthetic event damage", CWD: cwd, Demo: true})
			if err != nil {
				t.Fatal(err)
			}
			result, err := awaitExecution(t, e)
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(a.Home, "state", "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			var value any = "foreign-fixture"
			if field == "sequence" || field == "version" {
				value = 99
			}
			_, err = db.Exec("UPDATE events SET body=json_set(body,?,?) WHERE attempt_id=? AND sequence=2", "$."+field, value, result.ID)
			closeErr := db.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			err = e.Follow(context.Background(), 0, nil)
			var p *core.Error
			if !errors.As(err, &p) || p.Code != "event_identity" {
				t.Fatal("damaged event was observed", err)
			}
			err = e.Follow(context.Background(), 2, nil)
			if !errors.As(err, &p) || p.Code != "event_cursor_invalid" {
				t.Fatal("damaged prior cursor was trusted", err)
			}
		})
	}
}

func TestExecutionObserverBoundIncludesStalledCallbacks(t *testing.T) {
	a, cwd := fixtureApp(t)
	e, err := a.Start(context.Background(), Options{Input: "observer slots", CWD: cwd, Demo: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = awaitExecution(t, e); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var entered sync.WaitGroup
	entered.Add(maxObservers)
	done := make(chan error, maxObservers)
	for i := 0; i < maxObservers; i++ {
		go func() {
			done <- e.Follow(context.Background(), 0, func(core.Event) error { entered.Done(); <-release; return nil })
		}()
	}
	entered.Wait()
	for i := 0; i < maxObservers; i++ {
		if err = <-done; err == nil {
			t.Fatal("stalled observer succeeded")
		}
	}
	var p *core.Error
	if err = e.Follow(context.Background(), 0, nil); !errors.As(err, &p) || p.Code != "observer_limit" {
		t.Fatal("blocked callbacks escaped bound", err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for len(a.host.observers) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(a.host.observers) != 0 {
		t.Fatal("released callbacks retained slots")
	}
	if err = e.Follow(context.Background(), 0, nil); err != nil {
		t.Fatal("observer slot was not reusable", err)
	}
}
