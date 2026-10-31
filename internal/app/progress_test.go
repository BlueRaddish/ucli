package app

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
)

func TestClientStartedProgressPersistsBeforeOutputAndReplays(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opt := Options{Input: "synthetic progress", CWD: cwd, Demo: true, CommandID: "client-progress"}
	var calls atomic.Int32
	release := make(chan struct{}, 1)
	defer close(release)
	driver := driverFunc(func(ctx context.Context, req adapters.Request, emit func(string) error) (adapters.Outcome, error) {
		calls.Add(1)
		if req.OnClientStarted == nil {
			return adapters.Outcome{State: "failed"}, errors.New("missing progress callback")
		}
		// Repeated internal notification is one durable event, not progress spam.
		for range 2 {
			if err := req.OnClientStarted(); err != nil {
				return adapters.Outcome{State: "failed"}, err
			}
		}
		events, err := a.Store.Events(ctx, req.Attempt.ID, 0, 100)
		if err != nil || len(events) != 2 || events[1].Type != "status" || events[1].Text != core.StatusClientStarted {
			return adapters.Outcome{State: "failed"}, errors.New("progress was not persisted before output")
		}
		select {
		case <-release:
		case <-ctx.Done():
			return adapters.Outcome{State: "interrupted"}, ctx.Err()
		}
		if err := emit("synthetic response"); err != nil {
			return adapters.Outcome{State: "failed"}, err
		}
		return adapters.Outcome{State: "complete"}, nil
	})
	e, err := a.start(ctx, opt, driver)
	if err != nil {
		t.Fatal(err)
	}
	stopView := errors.New("synthetic view detached")
	err = e.Follow(ctx, 0, func(event core.Event) error {
		if event.Type == "status" && event.Text == core.StatusClientStarted {
			return stopView
		}
		return nil
	})
	if !errors.Is(err, stopView) {
		t.Fatalf("progress did not reach independent observer: %v", err)
	}
	// A detached progress view must not stop the driver. A separate gate releases it.
	select {
	case <-e.Done():
		t.Fatal("progress observer error ended the worker")
	default:
	}
	release <- struct{}{}
	result, err := e.Wait(ctx)
	if err != nil || result.State != "complete" || result.Output != "synthetic response" {
		t.Fatalf("progress changed result: %+v %v", result, err)
	}
	var original []core.Event
	if err = e.Follow(ctx, 0, func(event core.Event) error { original = append(original, event); return nil }); err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, event := range original {
		types = append(types, event.Type)
	}
	if !reflect.DeepEqual(types, []string{"accepted", "status", "message_delta", "terminal_result", "result_persisted"}) {
		t.Fatalf("unexpected progress order: %v", types)
	}
	replayed, err := a.start(ctx, opt, driver)
	if err != nil || !replayed.Replayed() {
		t.Fatalf("command did not replay: %v", err)
	}
	var again []core.Event
	if err = replayed.Follow(ctx, 0, func(event core.Event) error { again = append(again, event); return nil }); err != nil || !reflect.DeepEqual(original, again) || calls.Load() != 1 {
		t.Fatalf("progress replay launched/rewrote work: calls=%d err=%v", calls.Load(), err)
	}
}

func TestClientStartedProgressSaveFailurePreventsSyntheticDispatch(t *testing.T) {
	a, _, _, accepted := acceptedFailureFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(a.Home, "state", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER refuse_client_progress BEFORE INSERT ON events WHEN json_extract(NEW.body,'$.type')='status' BEGIN SELECT RAISE(ABORT,'PRIVATE_SYNTHETIC_DATABASE_DETAIL'); END`)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := false
	driver := driverFunc(func(_ context.Context, req adapters.Request, _ func(string) error) (adapters.Outcome, error) {
		if err := req.OnClientStarted(); err != nil {
			return adapters.Outcome{State: "failed"}, err
		}
		dispatched = true
		return adapters.Outcome{State: "complete"}, nil
	})
	result, err := a.runAccepted(context.Background(), accepted, "", driver, nil)
	var problem *core.Error
	if !errors.As(err, &problem) || problem.Code != "execution_progress_not_saved" || dispatched || result.State != "failed" || result.Output != "" || result.Error != "Native client start could not be recorded." {
		t.Fatalf("failed progress persistence dispatched or leaked data: %+v %v", result, err)
	}
	events, err := a.Store.Events(context.Background(), accepted.ID, 0, 100)
	if err != nil || len(events) != 3 || events[1].Type != "terminal_result" || events[2].Type != "result_persisted" {
		t.Fatalf("failed progress invented a started event: %+v %v", events, err)
	}
}

func TestDemoDoesNotInventClientStartedProgress(t *testing.T) {
	a, cwd := fixtureApp(t)
	var events []core.Event
	result, err := a.Run(context.Background(), Options{Demo: true, CWD: cwd, Input: "local only"}, func(event core.Event) error { events = append(events, event); return nil })
	if err != nil || result.State != "complete" {
		t.Fatal(result, err)
	}
	for _, event := range events {
		if event.Text == core.StatusClientStarted {
			t.Fatal("local demonstration pretended to launch a native client")
		}
	}
}
