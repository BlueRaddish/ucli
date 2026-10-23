package app

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/workspace"
)

type driverFunc func(context.Context, adapters.Request, func(string) error) (adapters.Outcome, error)

func (f driverFunc) Execute(ctx context.Context, req adapters.Request, emit func(string) error) (adapters.Outcome, error) {
	return f(ctx, req, emit)
}

func TestObservedNativeModelIsSavedSeparatelyFromRequestedAlias(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	w, err := workspace.Resolve(cwd)
	if err != nil {
		t.Fatal(err)
	}
	route, err := a.Explain(ctx, Options{Demo: true, CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	accepted, _, err := a.Store.Accept(ctx, core.RunCommand{ID: core.NewID("command"), Input: "synthetic", Workspace: w, Owner: a.owner, Requested: core.Defaults(), Effective: route})
	if err != nil {
		t.Fatal(err)
	}
	driver := driverFunc(func(context.Context, adapters.Request, func(string) error) (adapters.Outcome, error) {
		return adapters.Outcome{State: "complete", NativeModel: "native-model-version"}, nil
	})
	result, err := a.runAccepted(ctx, accepted, "", driver, nil)
	if err != nil || result.Effective.Model != route.Model {
		t.Fatalf("accepted alias was changed: %+v %v", result, err)
	}
	events, err := a.Store.Events(ctx, accepted.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "status" && strings.Contains(event.Text, "native-model-version") {
			found = true
		}
	}
	if !found || validNativeModel("invalid\nprivate-text") {
		t.Fatal("observed model provenance or input guard missing")
	}
}

func TestSharedExecutorPersistsContextAndUnknownFence(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	w, err := workspace.Resolve(cwd)
	if err != nil {
		t.Fatal(err)
	}
	route, err := a.Explain(ctx, Options{Demo: true, CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	accepted, _, err := a.Store.Accept(ctx, core.RunCommand{ID: core.NewID("command"), Input: "synthetic", Workspace: w, Owner: a.owner, Requested: core.Defaults(), Effective: route})
	if err != nil {
		t.Fatal(err)
	}
	var events []core.Event
	driver := driverFunc(func(ctx context.Context, req adapters.Request, emit func(string) error) (adapters.Outcome, error) {
		native := core.NativeContext{Runtime: route.Runtime, Provider: route.Provider, Account: route.Account, SessionID: "fixture-native", Durability: "ephemeral"}
		if err := req.OnContext(native); err != nil {
			t.Fatal(err)
		}
		stored, err := a.Store.GetAttempt(ctx, accepted.ID)
		if err != nil || stored.NativeContext == nil || stored.NativeContext.SessionID != native.SessionID {
			t.Fatal("context not durable before dispatch", stored, err)
		}
		if err := emit("partial 東京"); err != nil {
			t.Fatal(err)
		}
		return adapters.Outcome{State: "unknown", Message: "stop unconfirmed"}, context.DeadlineExceeded
	})
	result, err := a.runAccepted(ctx, accepted, "", driver, nil)
	events, readErr := a.Store.Events(ctx, accepted.ID, 0, 100)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var p *core.Error
	if !errors.As(err, &p) || p.Exit != 7 || result.State != "unknown" || result.Output != "partial 東京" || result.NativeContext == nil {
		t.Fatal("unknown outcome lost", result, err)
	}
	if len(events) < 5 || events[1].Type != "runtime_context" || events[len(events)-1].Type != "result_persisted" {
		t.Fatal("event order", events)
	}
	_, err = a.Run(ctx, Options{Input: "no replay", TaskID: result.TaskID, Demo: true}, nil)
	if !errors.As(err, &p) || p.Exit != 5 {
		t.Fatal("unknown fence released", err)
	}
}

func TestDemoResolverAndExplicitEchoStaySeparate(t *testing.T) {
	a, cwd := fixtureApp(t)
	t.Setenv("UCLI_MUSE_EXE", "missing-relative-runtime")
	route, err := a.Explain(context.Background(), Options{Input: "review architecture", CWD: cwd, Demo: true})
	if err != nil || route.Model != "demo/deep" || !strings.Contains(strings.Join(route.Reasons, " "), "snapshot") {
		t.Fatal("resolver not consumed", route, err)
	}
	_, err = a.Explain(context.Background(), Options{CWD: cwd, Demo: true, Selection: core.Selection{Model: "muse/echo", Effort: "high"}})
	var p *core.Error
	if !errors.As(err, &p) || p.Code != "effort_unsupported" {
		t.Fatal("echo pretended reasoning", err)
	}
	_, err = a.Run(context.Background(), Options{Input: "saved echo input", CWD: cwd, Demo: true, Selection: core.Selection{Model: "muse/echo"}}, nil)
	if !errors.As(err, &p) || p.Exit != 3 || p.TaskID == "" {
		t.Fatal("missing echo runtime not preserved", err)
	}
	task, history, err := a.Task(context.Background(), p.TaskID)
	if err != nil || task.Draft != "saved echo input" || len(history) != 0 {
		t.Fatal("unavailable echo dispatched", task, history, err)
	}
}

func TestUnsupportedRouteCannotOverwriteStaleDraft(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	task, err := a.Draft(ctx, "", "first", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	rev := task.Revision
	_, err = a.Draft(ctx, task.ID, "newer draft", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Run(ctx, Options{Input: "stale unavailable request", TaskID: task.ID, ExpectedRevision: &rev}, nil)
	var p *core.Error
	if !errors.As(err, &p) || p.Code != "revision_conflict" || p.TaskID == task.ID || p.TaskID == "" {
		t.Fatal("missing separate recovery draft", err)
	}
	current, _, err := a.Task(ctx, task.ID)
	if err != nil || current.Draft != "newer draft" {
		t.Fatal("newer draft overwritten", current, err)
	}
}

func TestStoppedDeadlineKeepsItsExitAcrossReplay(t *testing.T) {
	for _, tc := range []struct {
		state string
		cause error
		exit  int
		stop  string
	}{
		{"interrupted", context.DeadlineExceeded, 124, "deadline"},
		{"interrupted", context.Canceled, 130, "canceled"},
		{"unknown", context.DeadlineExceeded, 7, "deadline"},
	} {
		t.Run(tc.state+tc.stop, func(t *testing.T) {
			a, cwd := fixtureApp(t)
			ctx := context.Background()
			w, err := workspace.Resolve(cwd)
			if err != nil {
				t.Fatal(err)
			}
			route, err := a.Explain(ctx, Options{Demo: true, CWD: cwd})
			if err != nil {
				t.Fatal(err)
			}
			command := core.RunCommand{ID: "cause-replay", Input: "synthetic stop", Workspace: w, Owner: a.owner, Requested: core.Defaults(), Effective: route}
			accepted, _, err := a.Store.Accept(ctx, command)
			if err != nil {
				t.Fatal(err)
			}
			driver := driverFunc(func(context.Context, adapters.Request, func(string) error) (adapters.Outcome, error) {
				return adapters.Outcome{State: tc.state}, tc.cause
			})
			attempt, err := a.runAccepted(ctx, accepted, "", driver, nil)
			var p *core.Error
			if !errors.As(err, &p) || p.Exit != tc.exit || attempt.StopCause != tc.stop {
				t.Fatal(attempt, err)
			}
			replayed, err := a.Run(ctx, Options{Input: command.Input, CWD: cwd, CommandID: command.ID, Demo: true}, nil)
			if !errors.As(err, &p) || p.Exit != tc.exit || replayed.ID != attempt.ID || replayed.StopCause != tc.stop {
				t.Fatal("replay reclassified stop", replayed, err)
			}
			_, err = a.Store.FinishWithCause(ctx, attempt.ID, a.owner.ID, tc.state, attempt.Output, attempt.Error, "invalid")
			if err == nil {
				t.Fatal("invalid cause accepted")
			}
			_, err = a.Store.FinishWithCause(ctx, attempt.ID, a.owner.ID, tc.state, attempt.Output, attempt.Error, "")
			if err == nil {
				t.Fatal("replay replaced stop cause")
			}
		})
	}
}

func TestCompleteCleanupWarningIsDurableOnReplay(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	w, err := workspace.Resolve(cwd)
	if err != nil {
		t.Fatal(err)
	}
	route, err := a.Explain(ctx, Options{Demo: true, CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	command := core.RunCommand{ID: "cleanup-replay", Input: "synthetic completed work", Workspace: w, Owner: a.owner, Requested: core.Defaults(), Effective: route}
	accepted, _, err := a.Store.Accept(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	driver := driverFunc(func(_ context.Context, _ adapters.Request, emit func(string) error) (adapters.Outcome, error) {
		if err := emit("confirmed result"); err != nil {
			t.Fatal(err)
		}
		return adapters.Outcome{State: "complete", Message: "completed"}, errors.New("synthetic directory cleanup failure")
	})
	attempt, err := a.runAccepted(ctx, accepted, "", driver, nil)
	if err != nil || attempt.State != "complete" || attempt.Error != "" || len(attempt.Warnings) != 1 || !strings.Contains(attempt.Warnings[0], "cleanup failure") {
		t.Fatal(attempt, err)
	}
	replay, err := a.Run(ctx, Options{Input: command.Input, CWD: cwd, CommandID: command.ID, Demo: true}, nil)
	if err != nil || replay.ID != attempt.ID || len(replay.Warnings) != 1 || replay.Warnings[0] != attempt.Warnings[0] {
		t.Fatal("cleanup warning disappeared on replay", replay, err)
	}
}

func TestObserverDeadlineDoesNotBecomeExecutionDeadline(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	opt := Options{Input: "synthetic pre-dispatch deadline", CWD: cwd, CommandID: "pre-dispatch-deadline", Demo: true}
	attempt, err := a.Run(ctx, opt, func(core.Event) error { return context.DeadlineExceeded })
	if !errors.Is(err, context.DeadlineExceeded) || attempt.State != "complete" || attempt.StopCause != "" || attempt.Output == "" {
		t.Fatal(attempt, err)
	}
	replayed, err := a.Run(ctx, opt, nil)
	if err != nil || replayed.ID != attempt.ID || replayed.State != "complete" || replayed.StopCause != "" {
		t.Fatal("observer failure changed durable replay", replayed, err)
	}
}

func acceptedFailureFixture(t *testing.T) (*App, string, core.RunCommand, core.Attempt) {
	t.Helper()
	a, cwd := fixtureApp(t)
	w, err := workspace.Resolve(cwd)
	if err != nil {
		t.Fatal(err)
	}
	route, err := a.Explain(context.Background(), Options{Demo: true, CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	cmd := core.RunCommand{ID: "classified-failure", Input: "synthetic classified failure", Workspace: w, Owner: a.owner, Requested: core.Defaults(), Effective: route}
	attempt, _, err := a.Store.Accept(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	return a, cwd, cmd, attempt
}

func TestNativeFailureClassificationSurvivesSameCommandRestartReplay(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"native_login", 3}, {"native_quota", 4}, {"native_model_denied", 5}, {"native_failed", 8}, {"native_cleanup", 9}} {
		t.Run(tc.code, func(t *testing.T) {
			a, cwd, cmd, accepted := acceptedFailureFixture(t)
			calls := 0
			driver := driverFunc(func(_ context.Context, _ adapters.Request, emit func(string) error) (adapters.Outcome, error) {
				calls++
				if err := emit("saved partial output"); err != nil {
					return adapters.Outcome{State: "unknown"}, err
				}
				p := core.Problem(tc.code, "Immediate native error text.", tc.exit)
				p.Origin, p.Acceptance, p.SideEffects = "claude", "accepted", "unknown"
				return adapters.Outcome{State: "failed", Message: "Sanitized durable failure message."}, p
			})
			attempt, err := a.runAccepted(context.Background(), accepted, "", driver, nil)
			var first *core.Error
			if !errors.As(err, &first) || first.Code != tc.code || first.Exit != tc.exit || first.TaskID != attempt.TaskID || first.Message != attempt.Error || attempt.Failure == nil || attempt.Output != "saved partial output" {
				t.Fatalf("live failure is not its durable classification: %+v %v", attempt, err)
			}
			home := a.Home
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = Open(home)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			var events []core.Event
			// No demo flag or linked provider is needed: this must consume the
			// same receipt before routing, without dispatching another executor.
			replayed, err := a.Run(context.Background(), Options{Input: cmd.Input, CWD: cwd, CommandID: cmd.ID}, func(e core.Event) error { events = append(events, e); return nil })
			var again *core.Error
			if !errors.As(err, &again) || !reflect.DeepEqual(first, again) || !reflect.DeepEqual(attempt, replayed) || calls != 1 {
				t.Fatalf("restart replay changed classification: first=%+v replay=%+v attempt=%+v err=%v", first, again, replayed, err)
			}
			if len(events) != 1 || events[0].Type != "command_replayed" {
				t.Fatalf("replay emitted new work: %+v", events)
			}
			_, history, err := a.Task(context.Background(), attempt.TaskID)
			if err != nil || len(history) != 1 {
				t.Fatalf("replay created another attempt: %d %v", len(history), err)
			}
		})
	}
}

func TestInvalidPersistedFailureCannotReplaySuccess(t *testing.T) {
	a, cwd, cmd, accepted := acceptedFailureFixture(t)
	failure := &core.FailureClass{Code: "native_quota", Origin: "claude", Acceptance: "accepted", SideEffects: "unknown", Exit: 4}
	if _, err := a.Store.FinishClassified(context.Background(), accepted.ID, a.owner.ID, "failed", "", "Sanitized failure.", "", failure); err != nil {
		t.Fatal(err)
	}
	home := a.Home
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Deliberately corrupt only this synthetic receipt, bypassing the checked
	// writer, then consume the real public replay path after process reopening.
	db, err := sql.Open("sqlite", filepath.Join(home, "state", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("UPDATE attempts SET body=json_set(body,'$.failure.exit',0) WHERE id=?", accepted.ID)
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("synthetic damage failed: %v %v", err, closeErr)
	}
	a, err = Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	got, err := a.Run(context.Background(), Options{Input: cmd.Input, CWD: cwd, CommandID: cmd.ID}, nil)
	var p *core.Error
	if !errors.As(err, &p) || p.Exit != 9 || p.Code != "invalid_stored_failure" || got.ID != accepted.ID {
		t.Fatalf("invalid stored class became success: %+v %v", got, err)
	}
}

func TestFailureClassificationDoesNotSettleUnknownOrAlterLegacyReceipts(t *testing.T) {
	for _, tc := range []struct {
		state string
		exit  int
	}{{"unknown", 7}, {"interrupted", 130}} {
		t.Run(tc.state, func(t *testing.T) {
			a, _, _, accepted := acceptedFailureFixture(t)
			driver := driverFunc(func(context.Context, adapters.Request, func(string) error) (adapters.Outcome, error) {
				return adapters.Outcome{State: tc.state}, core.Problem("native_quota", "Synthetic quota diagnostic.", 4)
			})
			got, err := a.runAccepted(context.Background(), accepted, "", driver, nil)
			var p *core.Error
			if !errors.As(err, &p) || p.Exit != tc.exit || got.Failure != nil || got.State != tc.state {
				t.Fatalf("classification settled uncertain work: %+v %v", got, err)
			}
		})
	}
	legacy := core.Attempt{State: "failed", Error: "Legacy failure."}
	var p *core.Error
	if err := resultError(legacy); !errors.As(err, &p) || p.Exit != 8 || p.Code != "task_failed" {
		t.Fatal("legacy failed receipt changed", err)
	}
	for _, state := range []string{"complete", "unknown", "interrupted"} {
		bad := legacy
		bad.State = state
		bad.Failure = &core.FailureClass{Code: "native_quota", Origin: "claude", Acceptance: "accepted", SideEffects: "unknown", Exit: 4}
		if err := resultError(bad); !errors.As(err, &p) || p.Exit != 9 {
			t.Fatalf("class accepted on nonfailed state %s: %v", state, err)
		}
	}
}

func TestInvalidDriverFailureMetadataIsDurablyRejected(t *testing.T) {
	a, _, _, accepted := acceptedFailureFixture(t)
	driver := driverFunc(func(context.Context, adapters.Request, func(string) error) (adapters.Outcome, error) {
		return adapters.Outcome{State: "failed"}, core.Problem("invalid_native_error", "Synthetic error with success exit.", 0)
	})
	got, err := a.runAccepted(context.Background(), accepted, "", driver, nil)
	var p *core.Error
	if !errors.As(err, &p) || p.Exit != 9 || p.Code != "invalid_runtime_failure" || got.State != "failed" || got.Failure == nil || got.Failure.Exit != 9 {
		t.Fatalf("invalid native class escaped or stranded attempt: %+v %v", got, err)
	}
}
