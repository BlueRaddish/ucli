package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

// Output cancellation detaches the reader. Only the control context (signals or
// a confirmed Ctrl+C) requests that this invocation's captured execution stop.
func observeForeground(ctx context.Context, a *app.App, opt app.Options, o *output, emit func(core.Event) error) (core.Attempt, error) {
	execution, err := a.Start(ctx, opt)
	if err != nil {
		return core.Attempt{}, err
	}
	control := o.control
	if control == nil {
		control = ctx // Existing embedded callers have one explicit control context.
	}
	stop := context.AfterFunc(control, func() { _ = execution.Cancel() })
	defer stop()
	var deliveryErr error
	if execution.Replayed() {
		attempt := execution.Accepted()
		deliveryErr = emit(core.Event{Version: 1, Type: "command_replayed", TaskID: attempt.TaskID, AttemptID: attempt.ID, State: attempt.State, At: time.Now().UTC()})
	} else {
		deliveryErr = execution.Follow(ctx, 0, emit)
	}
	// Remain the foreground owner after a broken pipe; the user can still stop
	// explicitly. Returning early would close the App and cancel accepted work.
	attempt, runErr := execution.Wait(control)
	if control.Err() != nil {
		// Give the owned driver its normal cleanup window before returning an
		// unsettled receipt. App.Close remains the bounded final shutdown owner.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(control), 6*time.Second)
		attempt, runErr = execution.Wait(cleanup)
		cancel()
		if errors.Is(runErr, context.DeadlineExceeded) {
			runErr = core.Problem("execution_shutdown_pending", "The runtime has not confirmed its outcome after stop was requested. Inspect saved work before starting again.", 9)
		}
	}
	if runErr != nil {
		return attempt, runErr
	}
	return attempt, deliveryErr
}

type interactiveExecution struct {
	execution ownedExecution
	taskID    string
	model     string
}

// The controller consumes ownership and observation separately. The App supplies
// the production handle; controlled handles exercise interrupted UI journeys.
type ownedExecution interface {
	Done() <-chan struct{}
	Cancel() error
	Wait(context.Context) (core.Attempt, error)
	Follow(context.Context, int64, func(core.Event) error) error
}

func (s *session) startInteractiveExecution(ctx context.Context, submitted string) error {
	_ = s.out.notice("Preparing your prompt | Ctrl+C twice to interrupt and exit", toneInfo)
	_ = s.out.activity("working", false)
	execution, err := s.app.Start(ctx, s.options())
	if err != nil {
		_ = s.out.activity("", true)
		if s.markConflict(err, submitted) {
			return err
		}
		if refreshErr := s.refresh(ctx); refreshErr != nil {
			return refreshErr
		}
		return err
	}
	accepted := execution.Accepted()
	_ = s.out.activity("", true)
	_ = s.out.notice("Working in UCLI | /stop requests stop | /sessions | keep typing", toneInfo)
	s.trackExecution(execution, accepted)
	if err := s.refresh(ctx); err != nil {
		// The worker is already accepted. Keep its identity available even when
		// a subsequent read fails; never resend or pretend acceptance failed.
		_ = s.out.notice("Task accepted | saved progress is available in /sessions", toneWarning)
		return err
	}
	s.selection = s.task.Selection.WithDefaults()
	return nil
}

func (s *session) trackExecution(execution ownedExecution, accepted core.Attempt) {
	if s.runs == nil {
		s.runs = make(map[string]interactiveExecution)
	}
	// Completed handles are UI cache entries, not durable history. Bound the
	// cache by pruning on submission; the application separately bounds workers.
	for id, run := range s.runs {
		select {
		case <-run.execution.Done():
			delete(s.runs, id)
		default:
		}
	}
	s.runs[accepted.ID] = interactiveExecution{execution, accepted.TaskID, accepted.Effective.Model}
	control := s.out.control
	if control == nil {
		control = s.observeCtx
	}
	stop := context.AfterFunc(control, func() { _ = execution.Cancel() })
	s.observers.Add(1)
	go func() {
		defer s.observers.Done()
		defer stop()
		err := execution.Follow(s.observeCtx, 0, func(event core.Event) error {
			return s.out.attemptEvent(event, accepted.Effective.Model)
		})
		if err != nil {
			if s.observeCtx.Err() == nil {
				_ = s.out.notice("Live view stopped | the task remains saved in /sessions", toneWarning)
			}
			return
		}
		attempt, err := execution.Wait(s.observeCtx)
		if s.observeCtx.Err() != nil {
			return
		}
		state, tone := "Ready for your next prompt", toneSuccess
		if err != nil {
			state, tone = sessionStateLabel(attempt.State), toneWarning
			_ = s.out.progressTone(fmt.Sprintf("Task %s | %s\n", accepted.TaskID, problemFor(err).Message), toneWarning)
		}
		_ = s.out.notice(fmt.Sprintf("%s | task %s | result saved in /sessions", state, shortExecutionID(accepted.TaskID)), tone)
	}()
}

func (s *session) stopCurrentExecution() error {
	if execution := s.currentExecution(); execution != nil {
		if err := execution.Cancel(); err != nil {
			return err
		}
		return s.out.notice("Stop requested for this task | waiting for the runtime outcome", toneWarning)
	}
	return core.Problem("no_owned_execution", "This session has no active work owned by this UCLI process. Inspect recorded work in /sessions.", 5)
}

func (s *session) currentExecution() ownedExecution {
	if s.task != nil {
		for _, run := range s.runs {
			if run.taskID != s.task.ID {
				continue
			}
			select {
			case <-run.execution.Done():
				continue
			default:
			}
			return run.execution
		}
	}
	return nil
}

func (s *session) waitAfterOutputFailure() {
	stops := make([]func() bool, 0, len(s.runs))
	for _, run := range s.runs {
		if s.out.control != nil {
			stops = append(stops, context.AfterFunc(s.out.control, func() { _ = run.execution.Cancel() }))
		}
	}
	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()
	for _, run := range s.runs {
		control := s.out.control
		if control == nil {
			control = context.Background()
		}
		_, _ = run.execution.Wait(control)
	}
}
