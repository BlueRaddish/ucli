package muse

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

// The current test executable is an actual hidden child speaking the same stdio
// protocol when invoked with the driver's forced serve arguments.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		fakeHost()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testRequest(t *testing.T, input string) adapters.Request {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return adapters.Request{Executable: exe, StateHome: t.TempDir(), Attempt: core.Attempt{Input: input, Effective: core.Route{Runtime: "muse", Provider: "echo", Mode: "echo", Account: "none", Permission: "read-only", Model: "muse/echo", Effort: "none"}}}
}

func requireChildPlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("macOS owned process launch is explicitly unsupported pending a verified non-reaping observer")
	}
}

func execute(t *testing.T, input string) (adapters.Outcome, string, error) {
	t.Helper()
	requireChildPlatform(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	var output strings.Builder
	out, err := (Driver{}).Execute(ctx, testRequest(t, input), func(s string) error { output.WriteString(s); return nil })
	return out, output.String(), err
}

func TestEchoIsolatedStreamingAndServerRequestRefusal(t *testing.T) {
	requireChildPlatform(t)
	t.Setenv("UCLI_MUSE_FORBIDDEN_INHERITANCE", "must-not-cross")
	input := "Unicode 東京 café with spaces"
	req := testRequest(t, input)
	var output strings.Builder
	out, err := (Driver{}).Execute(context.Background(), req, func(s string) error { output.WriteString(s); return nil })
	if err != nil || out.State != "complete" || out.NativeSession != "session-fixture" || output.String() != "echo: "+input {
		t.Fatalf("out=%+v output=%q error=%v", out, output.String(), err)
	}
	entries, err := os.ReadDir(filepath.Join(req.StateHome, "runtimes", "muse-echo"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("operational directory cleanup: entries=%v err=%v", entries, err)
	}
}

func TestContextReceiptPrecedesDispatchAndFailureStopsIt(t *testing.T) {
	requireChildPlatform(t)
	for _, fail := range []bool{false, true} {
		t.Run(strconv.FormatBool(fail), func(t *testing.T) {
			req := testRequest(t, "context-receipt-synthetic")
			called := 0
			want := errors.New("durable context receipt failed")
			req.OnContext = func(value core.NativeContext) error {
				called++
				expected := core.NativeContext{Runtime: "muse", Provider: "echo", Account: "none", SessionID: "session-fixture", Durability: "ephemeral"}
				if value != expected {
					t.Errorf("context=%+v", value)
				}
				if _, err := os.Stat(filepath.Join(req.StateHome, "fixture-dispatched")); !errors.Is(err, os.ErrNotExist) {
					t.Error("turn dispatched before context receipt")
				}
				if fail {
					return want
				}
				return nil
			}
			out, err := (Driver{}).Execute(context.Background(), req, func(string) error { return nil })
			if called != 1 {
				t.Fatalf("context callbacks=%d", called)
			}
			_, markerErr := os.Stat(filepath.Join(req.StateHome, "fixture-dispatched"))
			if fail {
				if out.State != "failed" || !errors.Is(err, want) || !errors.Is(markerErr, os.ErrNotExist) {
					t.Fatalf("failed receipt out=%+v err=%v marker=%v", out, err, markerErr)
				}
			} else if out.State != "complete" || err != nil || markerErr != nil {
				t.Fatalf("successful receipt out=%+v err=%v marker=%v", out, err, markerErr)
			}
		})
	}
}

func TestUncertainOrMalformedResultsDoNotComplete(t *testing.T) {
	for _, mode := range []string{"empty", "wrong-turn", "wrong-session", "orphan-delta", "wrong-final", "cutoff", "malformed", "oversized", "duplicate-response", "bad-terminal", "early-eof", "duplicate-cursor", "failed"} {
		t.Run(mode, func(t *testing.T) {
			out, _, err := execute(t, "fixture:"+mode)
			expected := "unknown"
			if mode == "failed" {
				expected = "failed"
			}
			if out.State != expected || (expected == "unknown" && err == nil) {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestCancellationRequiresNativeTerminal(t *testing.T) {
	requireChildPlatform(t)
	for _, mode := range []string{"cancel", "cancel-unconfirmed", "cancel-race"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := testRequest(t, "fixture:"+mode)
			started := time.Now()
			out, err := (Driver{}).Execute(ctx, req, func(s string) error { cancel(); return nil })
			expected := "interrupted"
			if mode == "cancel-unconfirmed" {
				expected = "unknown"
			}
			if mode == "cancel-race" {
				expected = "complete"
			}
			if out.State != expected || time.Since(started) > 5*time.Second {
				t.Fatalf("out=%+v err=%v elapsed=%v", out, err, time.Since(started))
			}
			if expected == "unknown" && err == nil {
				t.Fatal("unconfirmed stop needs error")
			}
		})
	}
}

func TestLostTurnAckAndRejectedConsumer(t *testing.T) {
	requireChildPlatform(t)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	out, err := (Driver{}).Execute(ctx, testRequest(t, "fixture:lost-ack"), func(string) error { return nil })
	if out.State != "unknown" || err == nil {
		t.Fatalf("lost ack: out=%+v err=%v", out, err)
	}
	want := errors.New("consumer refused")
	out, err = (Driver{}).Execute(context.Background(), testRequest(t, "normal"), func(string) error { return want })
	if out.State != "unknown" || !errors.Is(err, want) {
		t.Fatalf("consumer: out=%+v err=%v", out, err)
	}
}

func TestSplitUnicodeFrames(t *testing.T) {
	input := strings.Repeat("東京 café ", 2000)
	out, output, err := execute(t, input)
	if err != nil || out.State != "complete" || output != "echo: "+input {
		t.Fatalf("split frame out=%+v err=%v exact=%t", out, err, output == "echo: "+input)
	}
}

func TestCancellationStopsChildDespiteBlockedCallback(t *testing.T) {
	requireChildPlatform(t)
	req := testRequest(t, "fixture:cancel-unconfirmed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		close(release)
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("driver did not return after callback release")
		}
	}()
	go func() {
		defer close(done)
		(Driver{}).Execute(ctx, req, func(string) error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("callback never entered")
	}
	paths, err := filepath.Glob(filepath.Join(req.StateHome, "runtimes", "muse-echo", "run-*", "workspace", "fixture.pid"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("fixture PID path: %v %v", paths, err)
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		alive, err := process.Alive(pid)
		if err != nil {
			t.Fatal(err)
		}
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned child survived stalled callback cancellation")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Execute itself still waits for the arbitrary callback, as documented.
	select {
	case <-done:
		t.Fatal("callback unexpectedly returned")
	default:
	}
}

func TestRouteGuards(t *testing.T) {
	req := testRequest(t, "hello")
	req.Attempt.Effective.Provider = "meta"
	out, err := (Driver{}).Execute(context.Background(), req, func(string) error { return nil })
	if err == nil || out.State != "failed" {
		t.Fatalf("route out=%+v err=%v", out, err)
	}
	for _, model := range []string{"", "other/model"} {
		req := testRequest(t, "hello")
		req.Attempt.Effective.Model = model
		out, err := (Driver{}).Execute(context.Background(), req, func(string) error { return nil })
		if err == nil || out.State != "failed" {
			t.Fatalf("model %q out=%+v err=%v", model, out, err)
		}
		if _, err := os.Stat(filepath.Join(req.StateHome, "runtimes")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid model created operational state")
		}
	}
	for _, effort := range []string{"", "auto", "low", "medium", "high", "max"} {
		req := testRequest(t, "hello")
		req.Attempt.Effective.Effort = effort
		out, err := (Driver{}).Execute(context.Background(), req, func(string) error { return nil })
		if err == nil || out.State != "failed" {
			t.Fatalf("effort %q out=%+v err=%v", effort, out, err)
		}
		if _, err := os.Stat(filepath.Join(req.StateHome, "runtimes")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid effort created operational state")
		}
	}
}

func TestEncodedRequestLimit(t *testing.T) {
	// Valid core input can exceed the encoded wire envelope through escaping.
	out, _, err := execute(t, "x"+strings.Repeat("\t", maxFrame/2))
	if err == nil || out.State != "failed" {
		t.Fatalf("pre-send frame rejection out=%+v err=%v", out, err)
	}
}

func TestEnvelopeGuardsAndCommandIdentity(t *testing.T) {
	for _, raw := range []string{`{"jsonrpc":"2.0","id":1,"id":2,"result":{}}`, `{"jsonrpc":"2.0","id":true,"result":{}}`, `{"jsonrpc":"2.0","id":1,"result":{},"error":{}}`, `{"jsonrpc":"2.0","method":"x","params":[]}`, `{"jsonrpc":"2.0","method":"x","params":{"sessionId":"a","SessionId":"b"}}`, strings.Repeat("[", 66) + strings.Repeat("]", 66), string([]byte{255})} {
		if _, err := decode([]byte(raw)); err == nil {
			t.Errorf("accepted malformed %q", raw)
		}
	}
	for i := 0; i < 8; i++ {
		id, err := uuid7()
		if err != nil || len(id) != 36 || id[14] != '7' || !strings.ContainsRune("89ab", rune(id[19])) {
			t.Fatalf("UUIDv7 %q %v", id, err)
		}
	}
}

func TestNativeEchoOptIn(t *testing.T) {
	requireChildPlatform(t)
	exe := os.Getenv("UCLI_TEST_MUSE_EXE")
	if exe == "" {
		t.Skip("set UCLI_TEST_MUSE_EXE to the verified installed native executable for local echo")
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	const expected = "2d1ac7af0a9ea77643cb8690eee70f52f0f8476c46b194f143c831f481aea59d"
	if hex.EncodeToString(sum[:]) != expected {
		t.Fatal("native executable is not the retained verified Muse build")
	}
	req := testRequest(t, "ucli-native-echo-東京-synthetic")
	req.Executable = exe
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output strings.Builder
	out, err := (Driver{}).Execute(ctx, req, func(s string) error { output.WriteString(s); return nil })
	if err != nil || out.State != "complete" || output.String() != "echo: "+req.Attempt.Input {
		t.Fatalf("out=%+v err=%v matches=%v", out, err, output.String() == "echo: "+req.Attempt.Input)
	}
	t.Logf("native_sha256=%s schema=%s matched_echo=true terminal=%s session_present=%t", expected, schemaFingerprint, out.State, out.NativeSession != "")
	req.Attempt.Input = "ucli-native-interrupt-synthetic"
	stopCtx, stop := context.WithCancel(context.Background())
	defer stop()
	out, err = (Driver{}).Execute(stopCtx, req, func(string) error { stop(); return nil })
	if err != nil || (out.State != "interrupted" && out.State != "complete") {
		t.Fatalf("native interruption not settled: state=%s err=%v", out.State, err)
	}
	t.Logf("native_interrupt_terminal=%s (complete is a completion race, not stop proof)", out.State)
}

func fakeHost() {
	want := []string{"serve", "--provider", "echo", "--no-session-log", "--disable-write", "--disable-shell"}
	if !reflect.DeepEqual(os.Args[1:], want) || os.Getenv("UCLI_MUSE_FORBIDDEN_INHERITANCE") != "" {
		os.Exit(21)
	}
	cwd, _ := os.Getwd()
	if err := os.WriteFile(filepath.Join(cwd, "fixture.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(30)
	}
	home := os.Getenv("HOME")
	if filepath.Base(cwd) != "workspace" || filepath.Dir(cwd) != filepath.Dir(home) || os.Getenv("MUSE_NO_AUTO_UPDATE") != "1" {
		os.Exit(22)
	}
	if _, err := os.Stat(os.Getenv("MUSE_AUTH_PATH")); !errors.Is(err, os.ErrNotExist) {
		os.Exit(23)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), maxFrame)
	send := func(value any) { raw, _ := json.Marshal(value); os.Stdout.Write(append(raw, '\n')) }
	response := func(id json.RawMessage, value any) { send(map[string]any{"jsonrpc": "2.0", "id": id, "result": value}) }
	var turn, input, mode string
	cursor := 0
	notify := func(method string, p map[string]any) {
		cursor++
		p["sessionId"] = "session-fixture"
		p["viewCursor"] = fmt.Sprintf("v:%d", cursor)
		send(map[string]any{"jsonrpc": "2.0", "method": method, "params": p})
	}
	open := func() {
		notify("item/started", map[string]any{"item": item{ID: "agent-1", Turn: turn, Kind: "agentMessage", Status: "inProgress", Revision: 1}})
	}
	delta := func(value string) {
		notify("item/delta", map[string]any{"itemId": "agent-1", "field": "text", "delta": value})
	}
	final := func(text string) {
		value := item{ID: "agent-1", Turn: turn, Kind: "agentMessage", Status: "completed", Revision: 2, Text: text}
		if mode == "cutoff" {
			value.CutOff = true
		}
		notify("item/completed", map[string]any{"item": value})
	}
	terminal := func(state string) {
		p := map[string]any{"turnId": turn, "terminal": state}
		if mode == "wrong-turn" {
			p["turnId"] = "other-turn"
		}
		if mode == "wrong-session" {
			cursor++
			send(map[string]any{"jsonrpc": "2.0", "method": "turn/completed", "params": map[string]any{"sessionId": "other-session", "viewCursor": fmt.Sprintf("v:%d", cursor), "turnId": turn, "terminal": state}})
			return
		}
		notify("turn/completed", p)
	}
	for scanner.Scan() {
		var e envelope
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			os.Exit(24)
		}
		var p struct {
			Command   string `json:"commandId"`
			Workspace string `json:"workspaceRoot"`
			Provider  string `json:"providerId"`
			Approval  string `json:"approvalMode"`
			Input     []struct {
				Text string `json:"text"`
			} `json:"input"`
		}
		json.Unmarshal(e.Params, &p)
		switch e.Method {
		case "initialize":
			// Exercise server-request denial before the first normal response.
			send(map[string]any{"jsonrpc": "2.0", "id": "approval-fixture", "method": "approval/request", "params": map[string]any{}})
			if !scanner.Scan() {
				os.Exit(25)
			}
			var denial struct {
				Error struct {
					Code int `json:"code"`
				} `json:"error"`
			}
			json.Unmarshal(scanner.Bytes(), &denial)
			if denial.Error.Code != -32601 {
				os.Exit(26)
			}
			response(e.ID, map[string]any{"schema": map[string]any{"version": 1, "fingerprint": schemaFingerprint}, "sessionDurability": "ephemeral"})
		case "initialized":
		case "session/start":
			if p.Provider != "echo" || !sameDirectory(p.Workspace, cwd) || p.Approval != "denyUnmatched" || len(p.Command) != 36 || p.Command[14] != '7' {
				os.Exit(27)
			}
			response(e.ID, map[string]any{"session": map[string]any{"sessionId": "session-fixture", "providerId": "echo", "workspaceRoot": cwd, "approvalMode": map[string]string{"mode": "denyUnmatched"}}})
		case "turn/start":
			stateHome := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(cwd))))
			if err := os.WriteFile(filepath.Join(stateHome, "fixture-dispatched"), []byte("turn-start-received"), 0600); err != nil {
				os.Exit(31)
			}
			if len(p.Input) != 1 {
				os.Exit(28)
			}
			input = p.Input[0].Text
			mode = strings.TrimPrefix(input, "fixture:")
			turn = p.Command
			if mode == "lost-ack" {
				time.Sleep(time.Minute)
				return
			}
			ack := map[string]any{"commandId": turn, "turnId": turn, "status": "accepted", "disposition": "started", "startedNewTurn": true}
			response(e.ID, ack)
			if mode == "duplicate-response" {
				response(e.ID, ack)
				continue
			}
			if mode == "malformed" {
				os.Stdout.WriteString("{not-json}\n")
				continue
			}
			if mode == "oversized" {
				os.Stdout.WriteString(strings.Repeat("x", maxFrame+1) + "\n")
				continue
			}
			if mode == "early-eof" {
				return
			}
			if mode == "empty" {
				terminal("completed")
				continue
			}
			if mode == "failed" {
				terminal("failed")
				continue
			}
			if mode == "orphan-delta" {
				delta("wrong")
				continue
			}
			open()
			text := "echo: " + input
			delta(text)
			if mode == "duplicate-cursor" {
				cursor--
				delta("again")
				continue
			}
			if strings.HasPrefix(mode, "cancel") {
				continue
			}
			if mode == "wrong-final" {
				text = "different"
			}
			final(text)
			if mode == "bad-terminal" {
				terminal("mystery")
			} else {
				terminal("completed")
			}
		case "turn/interrupt":
			response(e.ID, map[string]any{"commandId": p.Command, "turnId": turn, "status": "accepted"})
			if mode == "cancel-unconfirmed" {
				continue
			}
			if mode == "cancel-race" {
				final("echo: " + input)
				terminal("completed")
			} else {
				terminal("cancelled")
			}
		default:
			os.Exit(29)
		}
	}
}
