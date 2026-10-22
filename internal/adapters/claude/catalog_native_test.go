package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
)

// Explicit opt-in only. This sends initialize, never a user message, login,
// account switch or model inference. The receipt contains public catalog facts.
func TestNativeCatalogOptIn(t *testing.T) {
	exe := os.Getenv("UCLI_CLAUDE_CATALOG_EXE")
	if exe == "" {
		t.Skip("native zero-user-turn catalog probe requires explicit opt-in")
	}
	home, receipt := os.Getenv("UCLI_CLAUDE_CATALOG_HOME"), os.Getenv("UCLI_CLAUDE_CATALOG_RECEIPT")
	if !filepath.IsAbs(exe) || !filepath.IsAbs(home) || !filepath.IsAbs(receipt) {
		t.Fatal("explicit absolute probe paths required")
	}
	if _, err := os.Stat(receipt); !os.IsNotExist(err) {
		t.Fatal("fresh receipt path required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b := adapters.NativeBinding{Runtime: "claude", Executable: exe, Home: home, HomeMode: "existing"}
	digest, err := executableDigest(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	b.SHA256 = digest
	started := time.Now()
	var frames []map[string]any
	models, err := discoverCatalog(ctx, b, func(raw []byte) { frames = append(frames, catalogFrameShape(raw)) })
	status, code, reason := "passed", "", ""
	if err != nil {
		status, code = "failed", "local_error"
		var p *core.Error
		if errors.As(err, &p) {
			code = p.Code
			reason = p.Message
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "deadline"
		}
	}
	data, _ := json.MarshalIndent(map[string]any{"status": status, "error_code": code, "safe_reason": reason, "executable_sha256": digest, "elapsed_ms": time.Since(started).Milliseconds(), "user_turns": 0, "login_started": false, "execution_admitted": false, "protocol": "initialize", "models": models, "frames": frames}, "", "  ")
	if e := os.MkdirAll(filepath.Dir(receipt), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(receipt, append(data, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	if err != nil {
		t.Fatalf("zero-user-turn catalog probe failed: %s", code)
	}
}

// QA records only allowlisted structural names/types, never native values,
// messages, request IDs, credentials, configuration or tool/command contents.
func catalogFrameShape(raw []byte) map[string]any {
	shape := map[string]any{"bytes": len(raw)}
	f, err := object(raw)
	if err != nil {
		shape["json"] = "invalid"
		return shape
	}
	for key, allowed := range map[string][]string{"type": {"system", "assistant", "user", "result", "control_request", "control_response", "stream_event", "keep_alive"}, "subtype": {"init", "status", "hook_started", "hook_response", "hook_progress", "session_state_changed", "success", "error", "ui_invalidate"}} {
		value := text(f, key)
		for _, literal := range allowed {
			if value == literal {
				shape[key] = literal
			}
		}
	}
	fields := map[string]string{}
	for _, key := range []string{"type", "subtype", "session_id", "model", "permissionMode", "tools", "mcp_servers", "response", "request_id", "request", "message", "status", "uuid"} {
		if raw, ok := f[key]; ok {
			fields[key] = jsonShape(raw)
		}
	}
	shape["fields"] = fields
	if response, e := object(f["response"]); e == nil {
		for _, key := range []string{"subtype", "request_id", "response", "pending_permission_requests", "pending_user_dialog_requests"} {
			if raw, ok := response[key]; ok {
				fields["response."+key] = jsonShape(raw)
			}
		}
		if result, e := object(response["response"]); e == nil {
			for _, key := range []string{"models", "commands", "agents", "account", "claude_code_version"} {
				if raw, ok := result[key]; ok {
					fields["response.response."+key] = jsonShape(raw)
				}
			}
		}
	}
	return shape
}
func jsonShape(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "invalid"
	}
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		return "number"
	case []any:
		return fmt.Sprintf("array:%d", len(x))
	case map[string]any:
		return "object"
	}
	return "unknown"
}
