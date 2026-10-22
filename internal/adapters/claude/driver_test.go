package claude

import (
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

// This is an actual owned child of the adapter, not a mocked pipe transport.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--catalog-fixture" {
		os.Exit(catalogFixtureHost())
	}
	if len(os.Args) > 1 && os.Args[1] == "--safe-mode" {
		os.Exit(fixtureHost())
	}
	os.Exit(m.Run())
}

type fixture struct{ Mode, Marker, CWD string }

func fixtureHost() int {
	if len(os.Args) > 3 && os.Args[2] == "auth" {
		if os.Args[3] == "malformed" {
			fmt.Println("PRIVATE_NATIVE_BAD_STATUS")
			return 1
		}
		logged := os.Args[3] == "inconsistent"
		data, _ := json.Marshal(map[string]any{"loggedIn": logged, "authMethod": "none", "apiProvider": "firstParty", "configDirectory": os.Args[4]})
		fmt.Println(string(data))
		return 1
	}
	args := map[string]string{}
	for i := 1; i < len(os.Args); i++ {
		if strings.HasPrefix(os.Args[i], "--") {
			args[os.Args[i]] = ""
			if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "--") {
				args[os.Args[i]] = os.Args[i+1]
				i++
			}
		}
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 8<<20))
	if err != nil {
		return 90
	}
	var input struct {
		Message   struct{ Content string }
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return 91
	}
	var f fixture
	if json.Unmarshal([]byte(input.Message.Content), &f) != nil {
		return 92
	}
	if f.Mode == "modifier" && args["--model"] != "claude-sonnet-fixture[1m]" {
		return 102
	}
	if f.Mode == "wire" && args["--model"] != "claude-sonnet-fixture" {
		return 103
	}
	if _, err := os.Stat(f.Marker); err != nil {
		return 93
	}
	if os.WriteFile(f.Marker+".dispatched", []byte("synthetic"), 0600) != nil {
		return 94
	}
	if args["--input-format"] != "stream-json" || args["--output-format"] != "stream-json" || args["--permission-prompts"] != "none" || args["--effort"] != "low" || args["--mcp-config"] != `{"mcpServers":{}}` || args["--settings"] != `{"disableAllHooks":true,"disableClaudeAiConnectors":true}` {
		return 95
	}
	for _, flag := range []string{"--safe-mode", "--strict-mcp-config", "--no-session-persistence", "--disable-slash-commands", "--print", "--verbose"} {
		if _, ok := args[flag]; !ok {
			return 96
		}
	}
	if _, ok := args["--bare"]; ok {
		return 97
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("ANTHROPIC_AUTH_TOKEN") != "" || os.Getenv("CLAUDE_CODE_USE_BEDROCK") != "" {
		return 98
	}
	cwd, _ := os.Getwd()
	tools := []string{}
	if args["--permission-mode"] == "bypassPermissions" {
		if args["--tools"] != "default" || !samePath(cwd, f.CWD) {
			return 99
		}
		tools = []string{"Bash", "Read", "Write"}
	} else if args["--permission-mode"] != "dontAsk" || args["--tools"] != "" || samePath(cwd, f.CWD) {
		return 100
	}
	session := input.SessionID
	if session != args["--session-id"] || !validUUID(session) {
		return 101
	}
	send := func(v any) { data, _ := json.Marshal(v); fmt.Println(string(data)) }
	if f.Mode == "hang" {
		for {
			time.Sleep(time.Second)
		}
	}
	if f.Mode == "malformed" {
		fmt.Println(`{"type":`)
		return 0
	}
	if f.Mode == "wrong-session" {
		session = "00000000-0000-4000-8000-000000000000"
	}
	if f.Mode == "unexpected-tool" {
		tools = []string{"Bash"}
	}
	send(map[string]any{"type": "system", "subtype": "init", "session_id": session, "permissionMode": args["--permission-mode"], "tools": tools, "mcp_servers": []string{}, "model": "claude-sonnet-fixture"})
	if strings.HasPrefix(f.Mode, "error:") {
		class := strings.TrimPrefix(f.Mode, "error:")
		send(map[string]any{"type": "assistant", "session_id": session, "error": class, "message": map[string]any{"model": "<synthetic>", "content": []any{map[string]any{"type": "text", "text": "PRIVATE_NATIVE_SECRET synthetic diagnostic"}}}})
		errorsText := []string{"PRIVATE_NATIVE_SECRET"}
		if class == "result-only" {
			errorsText = []string{"Usage limit reached for PRIVATE_NATIVE_SECRET"}
		}
		send(map[string]any{"type": "result", "session_id": session, "subtype": "error_during_execution", "is_error": true, "errors": errorsText})
		return 1
	}
	if f.Mode == "sandbox" {
		send(map[string]any{"type": "assistant", "session_id": session, "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "synthetic-only"}, "id": "tool-1"}}}})
		send(map[string]any{"type": "user", "session_id": session, "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "PRIVATE_SYNTHETIC_TOOL_RESULT", "tool_use_id": "tool-1"}}}})
	}
	send(map[string]any{"type": "assistant", "session_id": session, "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "fixture reply"}, map[string]any{"type": "thinking", "thinking": "PRIVATE_SYNTHETIC_THINKING"}}}})
	if f.Mode == "no-result" {
		return 0
	}
	subtype := "success"
	if f.Mode == "failed" {
		subtype = "error_during_execution"
	}
	send(map[string]any{"type": "result", "subtype": subtype, "is_error": f.Mode == "failed", "session_id": session, "result": "fixture reply"})
	if f.Mode == "exit-disagreement" {
		return 1
	}
	return 0
}
func fixtureRequest(t *testing.T, mode, permission string) adapters.Request {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("owned Darwin launch remains unverified")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "context-receipt")
	input, _ := json.Marshal(fixture{mode, marker, dir})
	return adapters.Request{Binding: &adapters.NativeBinding{ID: "synthetic-account", Runtime: "claude", Executable: exe, Home: dir, HomeMode: "existing", AuthMode: "claude.ai", Identity: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64), Version: Version}, StateHome: dir, Attempt: core.Attempt{Input: string(input), Workspace: core.Workspace{Dir: dir}, Effective: core.Route{Provider: "anthropic", Runtime: "claude", Mode: "native", Account: "synthetic-account", Model: "claude/default", Effort: "low", Permission: permission}}, OnContext: func(c core.NativeContext) error {
		if !validUUID(c.SessionID) || c.Durability != "ephemeral" || c.Account != "synthetic-account" {
			return errors.New("invalid synthetic provenance")
		}
		return os.WriteFile(marker, []byte(c.SessionID), 0600)
	}}
}
func TestOwnedNativeProtocol(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "must-not-cross")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "must-not-cross")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	for _, mode := range []string{"complete", "sandbox", "failed", "malformed", "wrong-session", "unexpected-tool", "no-result", "exit-disagreement"} {
		t.Run(mode, func(t *testing.T) {
			permission := "read-only"
			if mode == "sandbox" {
				permission = "sandbox"
			}
			req := fixtureRequest(t, mode, permission)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			var output strings.Builder
			out, err := fixtureExecute(ctx, req, func(s string) error { output.WriteString(s); return nil })
			if mode == "complete" || mode == "sandbox" {
				if err != nil || out.State != "complete" || output.String() != "fixture reply" || out.NativeModel != "claude-sonnet-fixture" {
					t.Fatalf("state=%s output=%q error=%v", out.State, output.String(), err)
				}
				return
			}
			want := "unknown"
			if mode == "failed" {
				want = "failed"
			}
			if err == nil || out.State != want {
				t.Fatalf("state=%s want=%s err=%v", out.State, want, err)
			}
			var p *core.Error
			if !errors.As(err, &p) || p.Acceptance == "not-sent" || p.SideEffects == "none" {
				t.Fatalf("post-dispatch error understated uncertainty: %v", err)
			}
		})
	}
}

func TestOwnedSignedOutStatusExitOne(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("owned Darwin launch remains unverified")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for _, mode := range []string{"signed-out", "malformed", "inconsistent"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			raw, err := collect(ctx, process.Spec{Executable: exe, Args: []string{"--safe-mode", "auth", mode, home}, Env: environment(home, home), Dir: home})
			if mode != "signed-out" {
				if err == nil || strings.Contains(err.Error(), "PRIVATE") {
					t.Fatalf("unsafe nonzero status admitted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			inspection, err := inspectStatus(raw, home, "synthetic-digest")
			if err != nil || inspection.AuthMode != "none" || inspection.Identity != "" {
				t.Fatalf("signed-out status lost: %+v %v", inspection, err)
			}
		})
	}
}
func TestProvenanceFailurePreventsChild(t *testing.T) {
	req := fixtureRequest(t, "complete", "read-only")
	req.OnContext = func(core.NativeContext) error { return errors.New("SENSITIVE_SYNTHETIC_STORAGE_DETAIL") }
	out, err := fixtureExecute(context.Background(), req, func(string) error { return nil })
	if err == nil || out.State != "failed" || strings.Contains(err.Error(), "SENSITIVE") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if _, err := os.Stat(filepath.Join(req.StateHome, "context-receipt.dispatched")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("child ran before durable provenance")
	}
}
func TestCancellationAfterDispatchRemainsUnknown(t *testing.T) {
	req := fixtureRequest(t, "hang", "read-only")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var out adapters.Outcome
	var err error
	go func() { out, err = fixtureExecute(ctx, req, func(string) error { return nil }); close(done) }()
	deadline := time.After(8 * time.Second)
	for {
		if _, e := os.Stat(filepath.Join(req.StateHome, "context-receipt.dispatched")); e == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("child ended early: %v", err)
		case <-deadline:
			t.Fatal("fixture never dispatched")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(7 * time.Second):
		t.Fatal("owned child did not settle")
	}
	if out.State != "unknown" || !errors.Is(err, context.Canceled) {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}
func TestPublicAdmissionRejectsFixtureExecutable(t *testing.T) {
	req := fixtureRequest(t, "complete", "read-only")
	out, err := (Driver{}).Execute(context.Background(), req, func(string) error { return nil })
	var p *core.Error
	if out.State != "failed" || !errors.As(err, &p) || p.Code != "claude_version" || p.Acceptance != "not-sent" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	for _, change := range []func(*adapters.Request){func(r *adapters.Request) { r.Binding = nil }, func(r *adapters.Request) { r.Attempt.Effective.Permission = "workspace-write" }, func(r *adapters.Request) { r.Attempt.Effective.Account = "other" }, func(r *adapters.Request) { r.Attempt.Effective.Model = "other/model" }, func(r *adapters.Request) { r.OnContext = nil }} {
		r := fixtureRequest(t, "complete", "read-only")
		change(&r)
		if err := validateRequest(context.Background(), r, func(string) error { return nil }); err == nil {
			t.Fatal("invalid native request admitted")
		}
	}
}
func TestAccountMetadataBindsStatusWithoutTokens(t *testing.T) {
	home := t.TempDir()
	metadata := `{"oauthAccount":{"accountUuid":"00000000-0000-4000-8000-000000000001","emailAddress":"synthetic@example.invalid"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(metadata), 0600); err != nil {
		t.Fatal(err)
	}
	status := map[string]any{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty", "configDirectory": home, "email": "synthetic@example.invalid", "subscriptionType": "pro"}
	raw, _ := json.Marshal(status)
	i, err := inspectStatus(raw, home, "fake-digest")
	if err != nil || len(i.Identity) != 64 || len(i.Models) != 0 {
		t.Fatalf("inspection=%+v err=%v", i, err)
	}
	status["email"] = "changed@example.invalid"
	raw, _ = json.Marshal(status)
	if _, err = inspectStatus(raw, home, "fake-digest"); err == nil || strings.Contains(err.Error(), "@example") {
		t.Fatalf("changed identity: %v", err)
	}
	status["authMethod"] = "api_key"
	raw, _ = json.Marshal(status)
	if _, err = inspectStatus(raw, home, "fake-digest"); err == nil {
		t.Fatal("API billing was accepted")
	}
	status["loggedIn"] = false
	status["authMethod"] = "none"
	raw, _ = json.Marshal(status)
	i, err = inspectStatus(raw, home, "fake-digest")
	if err != nil || i.AuthMode != "none" || i.Identity != "" || len(i.Models) != 0 {
		t.Fatalf("signed-out state must be explicit, without stale identity/catalog: %+v %v", i, err)
	}
	status["loggedIn"] = nil
	raw, _ = json.Marshal(status)
	if _, err = inspectStatus(raw, home, "fake-digest"); err == nil {
		t.Fatal("null login state was treated as signed-out")
	}
}
func TestStrictProtocolAndOutputBounds(t *testing.T) {
	for _, raw := range []string{`{"type":"system","type":"result"}`, `{"a":1,"A":2}`, `{} {}`, `null`} {
		if _, err := object([]byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	s := stream{}
	if err := s.emit(strings.Repeat("a", maxOutput+1), func(string) error { t.Fatal("oversized output delivered"); return nil }); err == nil {
		t.Fatal("unbounded output")
	}
	s = stream{}
	if err := s.emit("x", func(string) error { return errors.New("PRIVATE_SYNTHETIC_FAILURE") }); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("consumer diagnostic leaked")
	}
}

func TestNativeFailuresAreClassifiedWithoutDiagnosticLeakage(t *testing.T) {
	for _, tc := range []struct {
		native, code string
		exit         int
	}{{"authentication_failed", "claude_login", 3}, {"rate_limit", "claude_quota", 4}, {"billing_error", "claude_billing", 4}, {"model_not_found", "claude_model_unavailable", 3}, {"invalid_request", "claude_request_denied", 5}, {"result-only", "claude_quota", 4}, {"unknown", "claude_turn_failed", 8}} {
		t.Run(tc.native, func(t *testing.T) {
			req := fixtureRequest(t, "error:"+tc.native, "read-only")
			var output strings.Builder
			out, err := fixtureExecute(context.Background(), req, func(s string) error { output.WriteString(s); return nil })
			var p *core.Error
			if !errors.As(err, &p) || p.Code != tc.code || p.Exit != tc.exit || p.Acceptance != "accepted" || out.State != "failed" || output.Len() != 0 || strings.Contains(out.Message, "PRIVATE") || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("unsafe or incorrect failure out=%+v err=%v textBytes=%d", out, err, output.Len())
			}
		})
	}
}

func TestExplicitModelPinRejectsNativeFallback(t *testing.T) {
	req := fixtureRequest(t, "complete", "read-only")
	req.Attempt.Effective.Model = "claude/opus"
	var output strings.Builder
	out, err := fixtureExecute(context.Background(), req, func(s string) error { output.WriteString(s); return nil })
	var p *core.Error
	if !errors.As(err, &p) || p.Code != "claude_model_changed" || out.State != "unknown" || output.Len() != 0 {
		t.Fatalf("mismatched init model admitted: %+v %v", out, err)
	}
	s := stream{session: "fixture", permission: "read-only", requestedModel: "claude/sonnet", initialized: true, model: "claude-sonnet-fixture"}
	err = s.consume([]byte(`{"type":"assistant","session_id":"fixture","message":{"model":"claude-opus-fixture","content":[{"type":"text","text":"must not deliver"}]}}`), func(string) error { t.Fatal("fallback text delivered"); return nil })
	if !errors.As(err, &p) || p.Code != "claude_model_changed" {
		t.Fatal("assistant model switch not rejected")
	}
}

func TestSafeProtocolDiagnosticSurvivesOutcomeMessage(t *testing.T) {
	for _, tc := range []struct {
		mode, model, code, state string
		rejectOutput             bool
	}{
		{"complete", "claude/opus", "claude_model_changed", "unknown", false},
		{"malformed", "", "claude_protocol", "unknown", false},
		{"unexpected-tool", "", "claude_permission", "unknown", false},
		{"complete", "", "claude_delivery", "unknown", true},
		{"error:rate_limit", "", "claude_quota", "failed", false},
	} {
		t.Run(tc.code, func(t *testing.T) {
			req := fixtureRequest(t, tc.mode, "read-only")
			if tc.model != "" {
				req.Attempt.Effective.Model = tc.model
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := fixtureExecute(ctx, req, func(string) error {
				if tc.rejectOutput {
					return errors.New("PRIVATE_CALLBACK_SECRET")
				}
				return nil
			})
			var problem *core.Error
			if !errors.As(err, &problem) || problem.Code != tc.code || out.State != tc.state {
				t.Fatalf("unexpected state/class: %+v %v", out, err)
			}
			if problem.Message == "" || !strings.Contains(out.Message, problem.Message) || strings.Count(out.Message, problem.Message) != 1 {
				t.Fatalf("safe diagnostic lost or repeated: %+v %v", out, err)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("private native/callback detail escaped")
			}
		})
	}
}

// Fixture resolution is explicit input to the same execution boundary. No
// production prefix/alias fallback exists merely to satisfy these fake models.
func fixtureExecute(ctx context.Context, req adapters.Request, emit func(string) error) (adapters.Outcome, error) {
	resolved := "claude-sonnet-fixture"
	if req.Attempt.Effective.Model == "claude/opus" {
		resolved = "claude-opus-fixture"
	}
	return execute(ctx, req, emit, Model{ID: strings.TrimPrefix(req.Attempt.Effective.Model, "claude/"), ResolvedID: resolved, EffortState: "supported", Efforts: []string{"low"}})
}
