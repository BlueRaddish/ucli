package app

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
)

type demoDriver struct{}

func validOutput(text string) bool { return utf8.ValidString(text) && !strings.ContainsRune(text, 0) }
func stopCause(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	return ""
}
func (demoDriver) Execute(ctx context.Context, req adapters.Request, emit func(string) error) (adapters.Outcome, error) {
	if err := ctx.Err(); err != nil {
		return adapters.Outcome{State: "interrupted"}, err
	}
	text := "[Local demonstration - no AI model was called]\nTask received: " + title(req.Attempt.Input) + "\n\nYour task, settings and this simulated result are saved. Follow up in the same task or choose New task.\n"
	if err := emit(text); err != nil {
		state := "failed"
		if errors.Is(err, context.Canceled) {
			state = "interrupted"
		}
		return adapters.Outcome{State: state}, err
	}
	return adapters.Outcome{State: "complete"}, nil
}

// All executors share one accepted-intent -> persisted deltas -> terminal receipt
// path. The driver, not a local timeout, establishes a native turn's outcome.
func (a *App) runAccepted(ctx context.Context, attempt core.Attempt, executable string, driver adapters.Driver, emit func(core.Event) error, bindings ...*adapters.NativeBinding) (core.Attempt, error) {
	lastSeq := int64(0)
	publishSaved := func() error {
		for {
			events, err := a.Store.Events(context.WithoutCancel(ctx), attempt.ID, lastSeq, 100)
			if err != nil {
				return err
			}
			for _, event := range events {
				lastSeq = event.Sequence
				if emit != nil {
					if err := a.deliver(context.WithoutCancel(ctx), emit, event); err != nil {
						return err
					}
				}
			}
			if len(events) < 100 {
				return nil
			}
		}
	}
	if err := publishSaved(); err != nil {
		return a.finishDeliveryFailure(ctx, attempt, err)
	}
	var partial strings.Builder
	appendText := func(text string) error {
		// Adapters are an internal trust boundary. Validate before allocating or
		// persisting; no silently truncated completed result is possible.
		if len(text) > core.MaxOutputBytes-partial.Len() || !validOutput(text) {
			return core.Problem("invalid_runtime_output", "Runtime output is invalid or exceeds the 4 MiB result bound.", 9)
		}
		for len(text) > 0 {
			n := min(4096, len(text))
			for n < len(text) && (text[n]&0xc0) == 0x80 {
				n--
			}
			chunk := text[:n]
			text = text[n:]
			event, err := a.Store.AppendEvent(ctx, core.Event{TaskID: attempt.TaskID, AttemptID: attempt.ID, Type: "message_delta", Text: chunk}, a.owner.ID)
			if err != nil {
				return err
			}
			partial.WriteString(chunk)
			lastSeq = event.Sequence
			if emit != nil {
				if err := a.deliver(ctx, emit, event); err != nil {
					return err
				}
			}
		}
		return nil
	}
	req := adapters.Request{Attempt: attempt, Executable: executable, StateHome: a.Home,
		OnContext: func(native core.NativeContext) error {
			if _, err := a.Store.RecordNativeContext(ctx, attempt.ID, a.owner.ID, native); err != nil {
				return err
			}
			return publishSaved()
		}}
	if len(bindings) == 1 && bindings[0] != nil {
		copy := *bindings[0]
		req.Binding = &copy
	}
	outcome, runErr := driver.Execute(ctx, req, appendText)
	if outcome.NativeModel != "" && !validNativeModel(outcome.NativeModel) {
		outcome.State, outcome.Message = "unknown", "Native model identity was invalid; inspect this attempt before new work."
		runErr = core.Problem("invalid_native_model", outcome.Message, 9)
		outcome.NativeModel = ""
	}
	switch outcome.State {
	case "complete", "failed", "interrupted", "unknown":
	default:
		outcome.State = "unknown"
		runErr = core.Problem("invalid_runtime_outcome", "Runtime returned no recognized terminal outcome. Inspect the saved attempt before new work.", 7)
	}
	message := outcome.Message
	if runErr != nil && message == "" {
		message = runErr.Error()
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if outcome.NativeModel != "" {
		if _, err := a.Store.AppendEvent(finishCtx, core.Event{TaskID: attempt.TaskID, AttemptID: attempt.ID, Type: "status", Text: "Native reported model: " + outcome.NativeModel + " | requested selection: " + attempt.Effective.Model}, a.owner.ID); err != nil {
			return attempt, err
		}
	}
	if outcome.State == "complete" && runErr != nil {
		// Confirmed work remains complete. Cleanup trouble is a durable warning,
		// visible on replay/history as well as the original live stream.
		_, warningErr := a.Store.AppendEvent(finishCtx, core.Event{TaskID: attempt.TaskID, AttemptID: attempt.ID, Type: "warning", Text: "Runtime completed, but local cleanup reported: " + runErr.Error()}, a.owner.ID)
		runErr = warningErr
	}
	cause := ""
	if outcome.State == "interrupted" || outcome.State == "unknown" {
		cause = stopCause(ctx, runErr)
	}
	var failure *core.FailureClass
	if outcome.State == "failed" {
		var p *core.Error
		if errors.As(runErr, &p) {
			failure = &core.FailureClass{Code: p.Code, Origin: p.Origin, Acceptance: p.Acceptance, SideEffects: p.SideEffects, Exit: p.Exit}
			if core.ValidateFailureClass(failure) != nil {
				// Never persist a success/unknown exit as a confirmed failure or
				// leave a known failed turn running because its metadata was bad.
				p = core.Problem("invalid_runtime_failure", "Runtime returned invalid failure metadata; its failed result is retained for inspection.", 9)
				message = p.Message
				failure = &core.FailureClass{Code: p.Code, Origin: p.Origin, Acceptance: p.Acceptance, SideEffects: p.SideEffects, Exit: p.Exit}
			}
		}
	}
	saved, err := a.Store.FinishClassified(finishCtx, attempt.ID, a.owner.ID, outcome.State, partial.String(), message, cause, failure)
	if err != nil {
		return attempt, err
	}
	attempt = saved
	deliveryErr := publishSaved()
	if outcome.State == "unknown" {
		return attempt, resultError(attempt)
	}
	if outcome.State == "interrupted" {
		return attempt, resultError(attempt)
	}
	if attempt.Failure != nil {
		// The live return and all later command replays consume one durable
		// classification and sanitized message, rather than separate mappings.
		return attempt, resultError(attempt)
	}
	if runErr != nil {
		var p *core.Error
		if errors.As(runErr, &p) {
			return attempt, runErr
		}
		p = core.Problem("runtime_failed", "Runtime execution or cleanup failed: "+runErr.Error(), 8)
		p.TaskID, p.Origin, p.Acceptance = attempt.TaskID, attempt.Effective.Runtime, "accepted"
		return attempt, p
	}
	if deliveryErr != nil {
		return attempt, deliveryErr
	}
	return attempt, resultError(attempt)
}

func validNativeModel(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:/-", r)) {
			return false
		}
	}
	return true
}
