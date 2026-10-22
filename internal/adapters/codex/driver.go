// Package codex runs a bounded app-server protocol through an explicit, pinned
// native connection. Discovery and execution never copy native credentials.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

const requestTimeout = 8 * time.Second
const turnTimeout = 30 * time.Second
const interruptTimeout = 2 * time.Second

type Driver struct{}

var _ adapters.Driver = Driver{}

func (Driver) Execute(ctx context.Context, req adapters.Request, emit func(string) error) (out adapters.Outcome, err error) {
	out.State = "failed"
	defer func() { retainProtocolDiagnostic(&out, err) }()
	if req.Binding == nil || ctx == nil || emit == nil || req.OnContext == nil {
		err = admissionError("Codex execution requires an explicit verified connection and durable context receipt.")
		return out, err
	}
	if err = validateBinding(*req.Binding, true); err != nil {
		return out, err
	}
	route := req.Attempt.Effective
	if route.Runtime != "codex" || route.Provider != "openai" || route.Account != req.Binding.ID || (route.Permission != "read-only" && route.Permission != "sandbox") || route.Mode != "native" || !strings.HasPrefix(route.Model, "codex/") {
		return out, admissionError("Codex requires the exact connected account, model and explicit supported permission.")
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	// Protocol cancellation gets its bounded native interrupt window before the
	// owned child is stopped; killing a local child cannot prove remote stop.
	overrides, err := nativeLaunchOverrides(ctx, *req.Binding, req.Attempt.Workspace.Dir)
	if err != nil {
		return out, err
	}
	child, cleanup, err := startBound(context.Background(), *req.Binding, route.Permission, overrides...)
	if err != nil {
		return out, err
	}
	defer func() {
		if closeErr := closeOwned(child); closeErr != nil {
			out.State = "unknown"
			err = errors.Join(err, errors.New("Codex owned-process cleanup is uncertain."))
			return
		}
		if cleanupErr := cleanup(); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	out, err = executeProtocol(ctx, req, child, emit)
	return out, safeExecutionError(err)
}

// executeProtocol consumes an owned transport. Public callers must pass through
// pinned native admission; fixture tests supply synthetic transports directly.
// Callbacks must return promptly; cancellation bounds child lifetime even when a
// callback blocks, but Go cannot forcibly return from that callback.
func executeProtocol(ctx context.Context, req adapters.Request, child *process.Child, emit func(string) error) (out adapters.Outcome, err error) {
	out.State = "failed"
	if child == nil {
		return out, errors.New("Codex fixture transport is required")
	}
	dispatched := false
	var stream *turnStream
	defer func() {
		if closeOwned(child) != nil {
			out.State, out.Message = "unknown", "Codex owned-process cleanup is uncertain."
			err = errors.Join(err, errors.New(out.Message))
		}
		if !dispatched && out.State == "failed" && ctx != nil && ctx.Err() != nil {
			out.State, out.Message = "interrupted", "Stopped before a Codex turn was dispatched."
		}
		retainProtocolDiagnostic(&out, err)
		if stream != nil && stream.warningObserved {
			const note = "A native warning was observed; private warning text was withheld."
			if out.Message == "" {
				out.Message = note
			} else {
				out.Message += " " + note
			}
		}
	}()
	if ctx == nil || emit == nil || req.OnContext == nil {
		return out, errors.New("Codex fixture requires a context, output consumer and durable context callback")
	}
	route := req.Attempt.Effective
	if route.Runtime != "codex" || (route.Permission != "read-only" && !(req.Binding != nil && route.Permission == "sandbox")) || !validID(route.Provider) || !validID(route.Model) || !validID(route.Account) || !filepath.IsAbs(req.Attempt.Workspace.Dir) {
		return out, errors.New("Codex fixture requires an explicit route and absolute read-only workspace")
	}
	// This bounded experiment only maps these exact effort values. Universal
	// auto/max must be resolved using an admitted catalog before reaching a driver.
	if (req.Binding == nil && route.Effort != "low" && route.Effort != "medium" && route.Effort != "high") || !validID(route.Effort) || route.Effort == "auto" {
		return out, errors.New("Codex fixture effort must be explicitly low, medium or high")
	}
	if core.ValidateInput(req.Attempt.Input) != nil {
		return out, errors.New("Codex input is invalid or exceeds its limit")
	}
	finished := make(chan struct{})
	stopWatch := context.AfterFunc(ctx, func() {
		timer := time.NewTimer(interruptTimeout + time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			child.Close()
		case <-finished:
		}
	})
	defer func() { close(finished); stopWatch() }()
	r := &rpc{child: child}
	call := func(method string, params any) (json.RawMessage, bool, error) {
		timeout := requestTimeout
		if req.Binding != nil && method == "initialize" {
			timeout = 45 * time.Second
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return r.call(callCtx, method, params)
	}
	hello, _, err := call("initialize", initializeParams())
	if err != nil {
		return out, err
	}
	fields := objectFields(hello)
	for _, key := range []string{"codexHome", "platformFamily", "platformOs", "userAgent"} {
		if _, ok := textField(fields, key, 4096); !ok {
			return out, errors.New("Codex initialize response is incomplete")
		}
	}
	// A reported userAgent is parsed, never treated as binary/schema attestation.
	if _, err = r.send(ctx, map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return out, err
	}
	sandboxMode, sandboxType := "read-only", "readOnly"
	if req.Binding != nil {
		if !matchingHome(hello, *req.Binding) {
			return out, admissionError("Codex did not confirm the connected native home.")
		}
		config, configErr := readNativeConfig(ctx, r, req.Attempt.Workspace.Dir)
		if configErr != nil {
			return out, configErr
		}
		if !safeBoundNativeConfig(config, route.Permission, *req.Binding) {
			return out, admissionError("Codex effective tool/auth configuration is outside the selected permission envelope.")
		}
		inspection, checkErr := inspectRPC(ctx, r, *req.Binding)
		if checkErr != nil {
			return out, checkErr
		}
		route.Model = strings.TrimPrefix(route.Model, "codex/")
		if !inspection.QuotaAvailable {
			problem := core.Problem("codex_capacity_blocked", "Codex included-usage permission or plan capacity is unavailable, unknown or nearly exhausted; no turn was sent.", 4)
			problem.Origin, problem.Acceptance, problem.SideEffects = "codex", "not-sent", "none"
			return out, problem
		}
		native, supported := nativeEffort(inspection, route.Model, route.Effort)
		if !supported {
			return out, admissionError("The connected Codex catalog does not support this exact model and effort.")
		}
		route.Effort = native
		if route.Permission == "sandbox" {
			sandboxMode, sandboxType = "danger-full-access", "dangerFullAccess"
		}
	}
	threadParams := map[string]any{"cwd": req.Attempt.Workspace.Dir, "approvalPolicy": "never", "approvalsReviewer": "user", "sandbox": sandboxMode, "ephemeral": true, "model": route.Model, "modelProvider": route.Provider}
	if req.Binding != nil {
		threadParams["allowProviderModelFallback"] = false
		if route.Permission == "read-only" {
			threadParams["environments"] = []any{}
		}
		threadParams["baseInstructions"] = "You are an assistant responding to the user's task. Follow the explicitly selected permissions."
		threadParams["developerInstructions"] = "Use only the selected model and effort. Do not initiate paid overages, purchases, account changes, or external messages."
	}
	session, _, err := call("thread/start", threadParams)
	if err != nil {
		return out, err
	}
	f := objectFields(session)
	sandbox, thread := objectFields(f["sandbox"]), objectFields(f["thread"])
	threadID, idOK := textField(thread, "id", 256)
	var turns []json.RawMessage
	if !idOK || !validID(threadID) || !equals(f, "cwd", req.Attempt.Workspace.Dir) || !equals(f, "model", route.Model) || !equals(f, "modelProvider", route.Provider) || !equals(f, "approvalPolicy", "never") || !equals(f, "approvalsReviewer", "user") || !equals(sandbox, "type", sandboxType) || !arrayField(thread, "turns", &turns) || len(turns) != 0 {
		return out, errors.New("Codex thread response did not confirm the requested fresh route and policy")
	}
	out.NativeSession = threadID
	out.NativeModel = route.Model
	// Inspect notifications already received during setup before sending input.
	for _, event := range r.events {
		if err := setupEvent(event, threadID); err != nil {
			return out, err
		}
	}
	r.events = nil
	nativeContext := core.NativeContext{Runtime: "codex", Provider: route.Provider, Account: route.Account, SessionID: threadID, Durability: "ephemeral"}
	if req.Binding != nil {
		nativeContext.Provenance = "confirmed"
	}
	if req.OnContext(nativeContext) != nil {
		return out, errors.New("Codex context receipt could not be persisted; no turn was dispatched")
	}
	ack, sent, err := call("turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]string{"type": "text", "text": req.Attempt.Input}}, "cwd": req.Attempt.Workspace.Dir, "model": route.Model, "effort": route.Effort, "approvalPolicy": "never", "approvalsReviewer": "user", "sandboxPolicy": map[string]string{"type": sandboxType}})
	if sent {
		dispatched = true
		out.State, out.Message = "unknown", "Codex turn acceptance or terminal outcome is unknown."
	}
	if err != nil {
		return out, err
	}
	turn := objectFields(objectFields(ack)["turn"])
	turnID, ok := textField(turn, "id", 256)
	if !ok || !validID(turnID) || !equals(turn, "status", "inProgress") || !noTurnError(turn["error"]) || !arrayField(turn, "items", &turns) || len(turns) != 0 {
		return out, errors.New("Codex did not acknowledge a valid active turn")
	}
	stream = newTurnStream(threadID, turnID)
	defer func() {
		if out.State == "unknown" {
			err = errors.Join(err, stream.unsettledError())
		}
	}()
	stream.native = req.Binding != nil
	stream.tools = stream.native && route.Permission == "sandbox"
	runCtx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	// Watch the internal deadline as well as caller cancellation, including when
	// a consumer violates the prompt-return callback contract.
	stopTurnWatch := context.AfterFunc(runCtx, func() {
		timer := time.NewTimer(interruptTimeout + time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			child.Close()
		case <-finished:
		}
	})
	defer stopTurnWatch()
	for {
		event, readErr := r.event(runCtx)
		if readErr != nil {
			if runCtx.Err() != nil {
				return interrupt(r, stream, out, emit, runCtx.Err())
			}
			return out, readErr
		}
		state, consumeErr := stream.consume(event, emit)
		if state != "" {
			out.State, out.Message = state, terminalMessage(state)
			if consumeErr != nil {
				out.Message = consumeErr.Error()
			}
			return out, consumeErr
		}
		if consumeErr != nil {
			return out, consumeErr
		}
	}
}

func interrupt(r *rpc, stream *turnStream, out adapters.Outcome, emit func(string) error, cause error) (adapters.Outcome, error) {
	ctx, cancel := context.WithTimeout(context.Background(), interruptTimeout)
	defer cancel()
	ack, _, err := r.call(ctx, "turn/interrupt", map[string]string{"threadId": stream.thread, "turnId": stream.turn})
	if err != nil || len(objectFields(ack)) != 0 {
		return out, errors.Join(cause, errors.New("Codex interrupt acknowledgment is unconfirmed"))
	}
	for {
		e, err := r.event(ctx)
		if err != nil {
			return out, errors.Join(cause, errors.New("Codex terminal outcome after interruption is unconfirmed"))
		}
		state, err := stream.consume(e, emit)
		if state != "" {
			out.State, out.Message = state, terminalMessage(state)
			if err != nil {
				out.Message = err.Error()
				return out, err
			}
			if state == "interrupted" {
				return out, cause
			}
			return out, nil // A matching terminal may win the cancellation race.
		}
		if err != nil {
			return out, err
		}
	}
}

func terminalMessage(state string) string {
	switch state {
	case "complete":
		return "Codex confirmed a completed response."
	case "interrupted":
		return "Codex confirmed interruption."
	default:
		return "Codex reported failure."
	}
}

func validID(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

// App persistence treats Message as the driver's durable explanation. Preserve
// our locally generated, already-redacted failure reason there as well as err;
// a generic acceptance placeholder must not erase the reason after dispatch.
func retainProtocolDiagnostic(out *adapters.Outcome, err error) {
	if err == nil || (out.State != "failed" && out.State != "unknown") {
		return
	}
	detail := err.Error()
	if out.Message == "" {
		out.Message = detail
	} else if !strings.Contains(out.Message, detail) {
		out.Message += " " + detail
	}
}
