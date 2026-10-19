package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

func TestMain(m *testing.M) {
	executable, _ := os.Executable()
	if strings.HasPrefix(filepath.Base(executable), "codex-gate-marker") {
		_ = os.WriteFile(filepath.Join(filepath.Dir(executable), "spawned"), []byte("unexpected spawn"), 0600)
		os.Exit(91)
	}
	if len(os.Args) == 4 && os.Args[1] == "codex-fixture" {
		fixtureHost(os.Args[2], os.Args[3])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fixtureRequest(t *testing.T) adapters.Request {
	t.Helper()
	return adapters.Request{StateHome: t.TempDir(), Attempt: core.Attempt{
		Input: "synthetic prompt; no account or model access", Workspace: core.Workspace{Dir: t.TempDir()},
		Effective: core.Route{Runtime: "codex", Provider: "fixture-provider", Model: "fixture-model", Account: "synthetic-account", Effort: "low", Permission: "read-only"},
	}, OnContext: func(core.NativeContext) error { return nil }}
}

func fixtureChild(t *testing.T, req adapters.Request, mode string) *process.Child {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("owned process launch on macOS is explicitly unsupported; pure protocol checks still run")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := []string{}
	if value := os.Getenv("SystemRoot"); value != "" {
		env = append(env, "SystemRoot="+value)
	}
	child, err := process.Start(context.Background(), process.Spec{Executable: executable, Args: []string{"codex-fixture", mode, req.Attempt.Workspace.Dir}, Env: env, Dir: req.Attempt.Workspace.Dir, FrameBytes: maxFrame, QueueBytes: 4 * maxFrame})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Close() })
	return child
}

func runFixture(t *testing.T, mode string) (adapters.Outcome, string, error) {
	t.Helper()
	req := fixtureRequest(t)
	child := fixtureChild(t, req, mode)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output strings.Builder
	out, err := executeProtocol(ctx, req, child, func(value string) error { output.WriteString(value); return nil })
	return out, output.String(), err
}

func TestPublicDriverCannotSpawnOrInvokeCallbacks(t *testing.T) {
	req := fixtureRequest(t)
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	req.Executable = filepath.Join(dir, "codex-gate-marker"+filepath.Ext(path))
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.OpenFile(req.Executable, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	req.StateHome = filepath.Join(dir, "must-not-create")
	req.OnContext = func(core.NativeContext) error { t.Error("public driver invoked context callback"); return nil }
	out, err := (Driver{}).Execute(context.Background(), req, func(string) error { t.Error("public driver emitted output"); return nil })
	var problem *core.Error
	if out.State != "failed" || !errors.As(err, &problem) || problem.Code != "codex_not_admitted" || problem.Exit != 3 || problem.Acceptance != "not-sent" || problem.SideEffects != "none" {
		t.Fatalf("public admission: out=%+v err=%+v", out, err)
	}
	for _, absent := range []string{filepath.Join(dir, "spawned"), req.StateHome} {
		if _, err := os.Stat(absent); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("public driver touched %s: %v", absent, err)
		}
	}
}

func TestOwnedFixtureStreamsAndRefusesServerAuthority(t *testing.T) {
	for _, mode := range []string{"normal", "approval", "split", "terminal-empty-items", "final-tail", "omit-error"} {
		t.Run(mode, func(t *testing.T) {
			out, output, err := runFixture(t, mode)
			if err != nil || out.State != "complete" || out.NativeSession != "thread-fixture" || out.NativeModel != "fixture-model" || output != "hello 世界" {
				t.Fatalf("out=%+v output=%q err=%v", out, output, err)
			}
		})
	}
}

func TestContextReceiptPrecedesDispatch(t *testing.T) {
	for _, mode := range []string{"persist", "refuse", "missing"} {
		t.Run(mode, func(t *testing.T) {
			req := fixtureRequest(t)
			marker := filepath.Join(req.Attempt.Workspace.Dir, "dispatched")
			called := 0
			req.OnContext = func(value core.NativeContext) error {
				called++
				want := core.NativeContext{Runtime: "codex", Provider: "fixture-provider", Account: "synthetic-account", SessionID: "thread-fixture", Durability: "ephemeral"}
				if value != want {
					t.Errorf("context=%+v", value)
				}
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Error("turn reached child before persistence callback")
				}
				if mode == "refuse" {
					return errors.New("SECRET callback diagnostic")
				}
				return os.WriteFile(filepath.Join(req.Attempt.Workspace.Dir, "persisted"), []byte("synthetic receipt"), 0600)
			}
			if mode == "missing" {
				req.OnContext = nil
			}
			child := fixtureChild(t, req, "context-required")
			out, err := executeProtocol(context.Background(), req, child, func(string) error { return nil })
			_, markerErr := os.Stat(marker)
			if mode == "persist" {
				if out.State != "complete" || err != nil || called != 1 || markerErr != nil {
					t.Fatalf("out=%+v err=%v calls=%d marker=%v", out, err, called, markerErr)
				}
			} else if out.State != "failed" || err == nil || strings.Contains(err.Error(), "SECRET") || !errors.Is(markerErr, os.ErrNotExist) {
				t.Fatalf("out=%+v err=%v marker=%v", out, err, markerErr)
			}
		})
	}
}

func TestMalformedOrUnsettledExecutionNeverCompletes(t *testing.T) {
	for _, mode := range []string{"wrong-turn", "wrong-thread", "orphan", "rewrite", "missing-item-complete", "empty", "tool", "reroute", "warning", "malformed", "duplicate", "oversized", "stderr-flood", "cutoff", "unsolicited", "bad-terminal", "terminal-conflict", "malformed-error", "malformed-terminal-error", "malformed-started-error", "failed"} {
		t.Run(mode, func(t *testing.T) {
			out, _, err := runFixture(t, mode)
			want := "unknown"
			if mode == "failed" {
				want = "failed"
			}
			if out.State != want || (want == "unknown" && err == nil) {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if strings.Contains(fmt.Sprint(err), "SECRET") || strings.Contains(out.Message, "SECRET") {
				t.Fatal("native diagnostics escaped")
			}
		})
	}
}

func TestSetupBindingAndQueueFailuresPreventDispatch(t *testing.T) {
	for _, mode := range []string{"wrong-model", "wrong-policy", "wrong-cwd", "old-thread", "setup-warning", "rpc-error", "queue-flood"} {
		t.Run(mode, func(t *testing.T) {
			req := fixtureRequest(t)
			child := fixtureChild(t, req, mode)
			out, err := executeProtocol(context.Background(), req, child, func(string) error { return nil })
			if out.State != "failed" || err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if _, err := os.Stat(filepath.Join(req.Attempt.Workspace.Dir, "dispatched")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid setup dispatched task text")
			}
		})
	}
}

func TestInterruptNeedsMatchingNativeTerminal(t *testing.T) {
	for _, mode := range []string{"cancel", "cancel-omit-error", "cancel-unconfirmed", "cancel-race"} {
		t.Run(mode, func(t *testing.T) {
			req := fixtureRequest(t)
			child := fixtureChild(t, req, mode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			start := time.Now()
			out, err := executeProtocol(ctx, req, child, func(string) error { cancel(); return nil })
			want := map[string]string{"cancel": "interrupted", "cancel-omit-error": "interrupted", "cancel-unconfirmed": "unknown", "cancel-race": "complete"}[mode]
			if out.State != want || time.Since(start) > 5*time.Second || (want != "complete" && !errors.Is(err, context.Canceled)) {
				t.Fatalf("out=%+v err=%v elapsed=%s", out, err, time.Since(start))
			}
		})
	}
}

func TestLostAcknowledgmentAndOutputRefusalStayUnknown(t *testing.T) {
	req := fixtureRequest(t)
	child := fixtureChild(t, req, "lost-ack")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, err := executeProtocol(ctx, req, child, func(string) error { return nil })
	if out.State != "unknown" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost ack out=%+v err=%v", out, err)
	}
	req = fixtureRequest(t)
	child = fixtureChild(t, req, "normal")
	out, err = executeProtocol(context.Background(), req, child, func(string) error { return errors.New("SECRET consumer detail") })
	if out.State != "unknown" || err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("consumer failure out=%+v err=%v", out, err)
	}
}

func TestConfirmedDeadlinePreservesCause(t *testing.T) {
	req := fixtureRequest(t)
	child := fixtureChild(t, req, "cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	out, err := executeProtocol(ctx, req, child, func(string) error { return nil })
	if out.State != "interrupted" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline out=%+v err=%v", out, err)
	}
}

func TestCancellationStopsOwnedChildDespiteBlockedConsumer(t *testing.T) {
	req := fixtureRequest(t)
	child := fixtureChild(t, req, "cancel-unconfirmed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = executeProtocol(ctx, req, child, func(string) error { close(entered); <-release; return nil })
	}()
	defer func() {
		close(release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("engine did not return after callback was released")
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("callback not entered")
	}
	cancel()
	waitCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	exit, err := child.Wait(waitCtx)
	if err != nil || !exit.Stopped {
		t.Fatalf("owned child was not stopped independently of callback: %+v %v", exit, err)
	}
}

func fixtureHost(mode, dir string) {
	if strings.HasPrefix(mode, "inspect-") {
		inspectionHost(mode, dir)
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), maxFrame)
	write := func(value any) {
		raw, _ := json.Marshal(value)
		raw = append(raw, '\n')
		if mode == "split" {
			for _, b := range raw {
				_, _ = os.Stdout.Write([]byte{b})
			}
		} else {
			_, _ = os.Stdout.Write(raw)
		}
	}
	reply := func(id json.RawMessage, value any) { write(map[string]any{"id": id, "result": value}) }
	notify := func(method string, params any) { write(map[string]any{"method": method, "params": params}) }
	item := func(text string) map[string]any {
		return map[string]any{"id": "item-fixture", "type": "agentMessage", "text": text}
	}
	itemEvent := func(method, text string) {
		notify(method, map[string]any{"threadId": "thread-fixture", "turnId": "turn-fixture", "item": item(text)})
	}
	failureFields := func() map[string]any {
		var kind any = "badRequest"
		if strings.HasPrefix(mode, "error-failed-") {
			kind = strings.TrimPrefix(mode, "error-failed-")
		}
		if mode == "error-unknown-kind" {
			kind = "SECRET future error"
		}
		if mode == "error-malformed-info" {
			kind = false
		}
		return map[string]any{"message": "SECRET native provider text", "codexErrorInfo": kind, "additionalDetails": "SECRET token or model detail"}
	}
	terminal := func(status string) {
		items := []any{item("hello 世界")}
		if mode == "terminal-empty-items" || status != "completed" {
			items = []any{}
		}
		if mode == "terminal-conflict" {
			items = []any{item("SECRET conflicting text")}
		}
		var failure any
		if status == "failed" {
			failure = map[string]any{"message": "SECRET provider diagnostic", "codexErrorInfo": nil, "additionalDetails": nil}
			if strings.HasPrefix(mode, "error-") {
				failure = failureFields()
			}
		}
		turn := map[string]any{"id": "turn-fixture", "status": status, "items": items, "error": failure}
		if strings.Contains(mode, "omit-error") && status != "failed" {
			delete(turn, "error")
		}
		if mode == "malformed-terminal-error" {
			turn["error"] = false
		}
		notify("turn/completed", map[string]any{"threadId": "thread-fixture", "turn": turn})
	}
	for scanner.Scan() {
		e, err := decode(scanner.Bytes())
		if err != nil {
			return
		}
		p := objectFields(e.Params)
		switch e.Method {
		case "initialize":
			if !equals(objectFields(p["clientInfo"]), "name", "ucli") || string(objectFields(p["capabilities"])["explicitGatewayOauth"]) != "true" {
				return
			}
			reply(e.ID, map[string]any{"userAgent": "codex-fixture/0.162.1", "codexHome": "synthetic-home", "platformOs": runtime.GOOS, "platformFamily": "synthetic"})
		case "initialized":
		case "thread/start":
			if !equals(p, "cwd", dir) || !equals(p, "approvalPolicy", "never") || !equals(p, "sandbox", "read-only") || !equals(p, "model", "fixture-model") || !equals(p, "modelProvider", "fixture-provider") || string(p["ephemeral"]) != "true" {
				return
			}
			if mode == "rpc-error" {
				write(map[string]any{"id": e.ID, "error": map[string]any{"code": -32000, "message": "SECRET server detail", "data": "SECRET token"}})
				continue
			}
			if mode == "queue-flood" {
				for i := 0; i < maxQueued+1; i++ {
					notify("metadata", map[string]any{})
				}
			}
			if mode == "setup-warning" {
				notify("configWarning", map[string]any{"message": "SECRET configuration"})
			}
			model, policy, cwd := "fixture-model", "never", dir
			turns := []any{}
			if mode == "wrong-model" {
				model = "fallback"
			}
			if mode == "wrong-policy" {
				policy = "on-request"
			}
			if mode == "wrong-cwd" {
				cwd = dir + "-different"
			}
			if mode == "old-thread" {
				turns = []any{map[string]any{"id": "previous"}}
			}
			notify("thread/started", map[string]any{"thread": map[string]any{"id": "thread-fixture"}})
			reply(e.ID, map[string]any{"thread": map[string]any{"id": "thread-fixture", "turns": turns}, "model": model, "modelProvider": "fixture-provider", "cwd": cwd, "approvalPolicy": policy, "approvalsReviewer": "user", "sandbox": map[string]any{"type": "readOnly"}})
		case "turn/start":
			if !equals(p, "threadId", "thread-fixture") || !equals(p, "model", "fixture-model") || !equals(p, "effort", "low") || !equals(p, "approvalPolicy", "never") || !equals(p, "cwd", dir) || !equals(objectFields(p["sandboxPolicy"]), "type", "readOnly") {
				return
			}
			if mode == "context-required" {
				if _, err := os.Stat(filepath.Join(dir, "persisted")); err != nil {
					return
				}
			}
			_ = os.WriteFile(filepath.Join(dir, "dispatched"), []byte("one synthetic turn"), 0600)
			if mode == "lost-ack" {
				continue
			}
			turn := map[string]any{"id": "turn-fixture", "status": "inProgress", "items": []any{}, "error": nil}
			if strings.Contains(mode, "omit-error") {
				delete(turn, "error")
			}
			if mode == "malformed-started-error" {
				turn["error"] = 0
			}
			notify("turn/started", map[string]any{"threadId": "thread-fixture", "turn": turn})
			if mode == "malformed-started-error" {
				turn["error"] = nil
			}
			if mode == "malformed-error" {
				turn["error"] = false
			}
			reply(e.ID, map[string]any{"turn": turn})
			if strings.HasPrefix(mode, "error-") {
				params := map[string]any{"threadId": "thread-fixture", "turnId": "turn-fixture", "willRetry": mode == "error-retry-complete", "error": failureFields()}
				switch mode {
				case "error-mismatch":
					params["threadId"] = "another-thread"
				case "error-missing-turn":
					delete(params, "turnId")
				case "error-null-retry":
					params["willRetry"] = nil
				case "error-missing-retry":
					delete(params, "willRetry")
				}
				notify("error", params)
				if strings.HasPrefix(mode, "error-failed-") || mode == "error-unknown-kind" {
					terminal("failed")
					continue
				}
				if mode != "error-retry-complete" {
					continue
				}
			}
			if strings.HasPrefix(mode, "warning-") {
				params := map[string]any{"message": "SECRET native warning text", "threadId": "thread-fixture"}
				switch mode {
				case "warning-omitted":
					delete(params, "threadId")
				case "warning-null":
					params["threadId"] = nil
				case "warning-wrong-thread":
					params["threadId"] = "other-thread"
				case "warning-wrong-turn":
					params["turnId"] = "other-turn"
				case "warning-invalid-thread":
					params["threadId"] = false
				case "warning-invalid-message":
					params["message"] = false
				case "warning-missing-message":
					delete(params, "message")
				}
				notify("warning", params)
				if mode == "warning-eof" {
					return
				}
				if mode == "warning-failed" {
					terminal("failed")
					continue
				}
			}
			switch mode {
			case "malformed":
				fmt.Fprintln(os.Stdout, `{invalid SECRET`)
				continue
			case "duplicate":
				fmt.Fprintln(os.Stdout, `{"method":"metadata","params":{"threadId":"a","threadId":"b"}}`)
				continue
			case "oversized":
				_, _ = io.WriteString(os.Stdout, strings.Repeat("x", maxFrame)+"\n")
				continue
			case "stderr-flood":
				chunk := strings.Repeat("SECRET", 1000)
				for written := 0; written <= maxTranscript; written += len(chunk) {
					if _, err := io.WriteString(os.Stderr, chunk); err != nil {
						return
					}
				}
				continue
			case "cutoff":
				_, _ = io.WriteString(os.Stdout, `{"method":`)
				return
			case "unsolicited":
				reply(e.ID, map[string]any{})
				continue
			case "empty":
				terminal("completed")
				continue
			case "failed":
				terminal("failed")
				continue
			case "tool":
				notify("item/started", map[string]any{"threadId": "thread-fixture", "turnId": "turn-fixture", "item": map[string]any{"id": "tool", "type": "commandExecution", "command": "SECRET"}})
				continue
			case "reroute":
				notify("model/rerouted", map[string]any{"threadId": "thread-fixture", "turnId": "turn-fixture", "toModel": "fallback"})
				continue
			case "warning":
				notify("warning", map[string]any{"message": false})
				continue
			case "approval":
				write(map[string]any{"id": "server-approval", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "thread-fixture", "turnId": "turn-fixture", "itemId": "tool", "command": "SECRET"}})
				if !scanner.Scan() {
					return
				}
				denied, err := decode(scanner.Bytes())
				if err != nil || string(denied.ID) != `"server-approval"` || denied.Error == nil || denied.Result != nil {
					return
				}
			}
			if mode != "orphan" {
				itemEvent("item/started", "")
			}
			thread, turnID := "thread-fixture", "turn-fixture"
			if mode == "wrong-thread" {
				thread = "another-thread"
			}
			if mode == "wrong-turn" {
				turnID = "another-turn"
			}
			delta := "hello 世界"
			if mode == "final-tail" {
				delta = "hello "
			}
			notify("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": turnID, "itemId": "item-fixture", "delta": delta})
			if strings.HasPrefix(mode, "cancel") {
				continue
			}
			final := "hello 世界"
			if mode == "rewrite" {
				final = "rewritten SECRET"
			}
			if mode != "missing-item-complete" {
				itemEvent("item/completed", final)
			}
			status := "completed"
			if mode == "bad-terminal" {
				status = "inProgress"
			}
			terminal(status)
		case "turn/interrupt":
			if !equals(p, "threadId", "thread-fixture") || !equals(p, "turnId", "turn-fixture") {
				return
			}
			reply(e.ID, map[string]any{})
			if mode == "error-on-interrupt" {
				terminal("failed")
				continue
			}
			if mode == "cancel-unconfirmed" || strings.HasPrefix(mode, "error-") {
				continue
			}
			if mode == "cancel-race" {
				itemEvent("item/completed", "hello 世界")
				terminal("completed")
			} else {
				terminal("interrupted")
			}
		default:
			return
		}
	}
}

func TestUnknownProtocolDiagnosticSurvivesOutcomeBoundary(t *testing.T) {
	out, _, err := runFixture(t, "wrong-turn")
	if out.State != "unknown" || err == nil || !strings.Contains(out.Message, "Codex notification execution identity mismatch") || strings.Contains(out.Message, "SECRET") {
		t.Fatalf("outcome lost safe protocol reason: %+v %v", out, err)
	}
}

func TestTurnErrorOptionalityKeepsMalformedAndFailedGuards(t *testing.T) {
	for _, status := range []string{"inProgress", "completed", "interrupted", "failed"} {
		for _, field := range []string{"absent", "null", "false", "0", "\"\"", "{}", "[]"} {
			t.Run(status+"/"+field, func(t *testing.T) {
				turn := map[string]any{"id": "turn", "items": []any{}, "status": status}
				if field != "absent" {
					turn["error"] = json.RawMessage(field)
				}
				params, _ := json.Marshal(map[string]any{"threadId": "thread", "turn": turn})
				method := "turn/completed"
				if status == "inProgress" {
					method = "turn/started"
				}
				s := newTurnStream("thread", "turn")
				s.outputBytes = 1
				state, err := s.consume(envelope{Method: method, Params: params}, func(string) error { return nil })
				wantOK := field == "absent" || field == "null"
				if status == "failed" {
					wantOK = field == `{"message":"SECRET"}`
				}
				if (err == nil || state == "failed") != wantOK {
					t.Fatalf("error field acceptance changed: %v", err)
				}
			})
		}
	}
}
