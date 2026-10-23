package app

import (
	"context"
	"time"

	"ucli.local/ucli/internal/core"
)

type observerCall struct {
	event  core.Event
	answer chan error
}

// At most sixteen callback workers exist per App, including a callback that
// ignores cancellation. A stalled callback retains its slot until it returns;
// it never owns the database, execution lifetime, or host shutdown join.
func (a *App) newObserver(emit func(core.Event) error) (func(context.Context, core.Event) error, func(), error) {
	select {
	case a.host.observers <- struct{}{}:
	default:
		return nil, nil, core.Problem("observer_limit", "Too many observers are active or stalled. Saved work continues; close an observer before reconnecting.", 9)
	}
	calls := make(chan observerCall)
	stop := make(chan struct{})
	go func() {
		defer func() { <-a.host.observers }()
		for {
			select {
			case <-stop:
				return
			case call := <-calls:
				call.answer <- emit(call.event)
			}
		}
	}()
	send := func(ctx context.Context, event core.Event) error {
		ctx, cancel := context.WithTimeout(ctx, observerDeadline)
		defer cancel()
		call := observerCall{event: event, answer: make(chan error, 1)}
		select {
		case calls <- call:
		case <-ctx.Done():
			return observerError(ctx)
		}
		select {
		case err := <-call.answer:
			return err
		case <-ctx.Done():
			return observerError(ctx)
		}
	}
	return send, func() { close(stop) }, nil
}

func observerError(ctx context.Context) error {
	if ctx.Err() == context.Canceled {
		return context.Canceled
	}
	return core.Problem("output_stalled", "Output observation stalled. Work continues independently; reconnect to its saved events.", 9)
}

// Follow delivers only durable, contiguous events for this attempt. Capture a
// coalesced hint before reading; available pages drain before waiting, avoiding
// lost wakeups. A timer also observes writes from other App/process instances.
// Callback/context failure detaches this reader only, never the owned worker.
func (e *Execution) Follow(ctx context.Context, afterSeq int64, emit func(core.Event) error) error {
	if afterSeq < 0 {
		return core.Problem("invalid_input", "Event sequence must be nonnegative.", 2)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if afterSeq > 0 {
		prior, err := e.app.Store.Events(ctx, e.accepted.ID, afterSeq-1, 1)
		if err != nil {
			return err
		}
		if len(prior) != 1 || prior[0].Version != 1 || prior[0].Sequence != afterSeq || prior[0].AttemptID != e.accepted.ID || prior[0].TaskID != e.accepted.TaskID {
			return core.Problem("event_cursor_invalid", "The event cursor does not belong to this saved attempt.", 9)
		}
	}
	if emit == nil {
		emit = func(core.Event) error { return nil }
	}
	send, stop, err := e.app.newObserver(emit)
	if err != nil {
		return err
	}
	defer stop()
	terminalSeen := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.app.host.mu.Lock()
		changed := e.app.host.changed
		e.app.host.mu.Unlock()
		events, err := e.app.Store.Events(ctx, e.accepted.ID, afterSeq, 100)
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.Version != 1 || event.AttemptID != e.accepted.ID || event.TaskID != e.accepted.TaskID || event.Sequence != afterSeq+1 {
				return core.Problem("event_identity", "Saved events do not match the captured attempt and cursor. Inspect the saved state before continuing.", 9)
			}
			if err = send(ctx, event); err != nil {
				return err
			}
			afterSeq = event.Sequence
		}
		if len(events) == 100 {
			continue
		}
		if terminalSeen {
			return nil
		}
		current, err := e.app.Store.GetAttempt(ctx, e.accepted.ID)
		if err != nil {
			return err
		}
		switch current.State {
		case "complete", "failed", "interrupted", "unknown":
			// Finish may have committed after the preceding event read. Drain
			// once more before reporting completion, even if no hint was seen.
			terminalSeen = true
			continue
		}
		// A storage failure can end a worker without a terminal commit. Surface
		// that failure instead of waiting forever or inventing a terminal state.
		if e.work != nil {
			select {
			case <-e.work.done:
				switch e.work.result.State {
				case "complete", "failed", "interrupted", "unknown":
					terminalSeen = true
					continue
				}
				if e.work.err != nil {
					return e.work.err
				}
				return core.Problem("execution_unsettled", "The worker ended without a saved terminal receipt. Inspect this attempt before sending again.", 7)
			default:
			}
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
