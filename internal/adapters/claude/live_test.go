package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
)

// Never enabled by normal test/build. The caller must explicitly authorize one
// real model turn. The marker is intentionally retained even after a failure.
func TestLiveClaude(t *testing.T) {
	if os.Getenv("UCLI_LIVE_CLAUDE") != "1" {
		t.Skip("explicit native inference authorization required")
	}
	destination := os.Getenv("UCLI_LIVE_CLAUDE_RECEIPT")
	if !filepath.IsAbs(destination) {
		t.Fatal("live receipt must be an explicit absolute path")
	}
	marker, err := os.OpenFile(destination+".attempted", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("live probe already attempted or evidence is unwritable; no inference repeated")
	}
	if _, err = marker.WriteString("One authorized tiny no-tools native probe. Do not remove to retry blindly.\n"); err == nil {
		err = marker.Sync()
	}
	closeErr := marker.Close()
	if err != nil || closeErr != nil {
		t.Fatal("cannot persist live request budget marker")
	}
	started := time.Now()
	receipt := map[string]any{"kind": "claude-live-native-no-tools", "version": 1, "native_version": Version, "permission": "read-only", "effort": "low", "model_alias": "claude/default", "exact_response": false, "attempted_inference": false, "no_billing_settings_changed": true}
	defer func() {
		receipt["elapsed_ms"] = time.Since(started).Milliseconds()
		raw, e := json.MarshalIndent(receipt, "", "  ")
		if e == nil {
			e = os.WriteFile(destination, append(raw, '\n'), 0600)
		}
		if e != nil {
			t.Error("sanitized live receipt could not be retained")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	b := adapters.NativeBinding{ID: "live-probe", Runtime: "claude", Executable: os.Getenv("UCLI_LIVE_CLAUDE_EXECUTABLE"), Home: os.Getenv("UCLI_LAUNCH_CLAUDE_HOME"), HomeMode: "existing"}
	inspection, err := Inspect(ctx, b)
	if err != nil {
		receipt["stage"] = "inspection"
		receipt["error_code"] = safeCode(err)
		t.Fatal("native account inspection failed; see sanitized receipt")
	}
	b.Identity, b.AuthMode, b.Version, b.SHA256 = inspection.Identity, inspection.AuthMode, inspection.Version, inspection.SHA256
	receipt["executable_sha256"] = b.SHA256
	receipt["subscription_type"] = inspection.PlanType
	receipt["auth_mode"] = inspection.AuthMode
	dir := t.TempDir()
	req := adapters.Request{Binding: &b, StateHome: dir, Attempt: core.Attempt{Input: "Reply with exactly UCLI_LIVE_OK and no other text.", Workspace: core.Workspace{Dir: dir}, Effective: core.Route{Runtime: "claude", Provider: "anthropic", Mode: "native", Account: b.ID, Model: "claude/default", Effort: "low", Permission: "read-only"}}, OnContext: func(c core.NativeContext) error {
		receipt["allocated_context_recorded"] = true
		raw, _ := json.Marshal(c)
		return os.WriteFile(filepath.Join(dir, "allocated-context.json"), raw, 0600)
	}}
	receipt["attempted_inference"] = true
	var result strings.Builder
	chunks := 0
	out, err := (Driver{}).Execute(ctx, req, func(text string) error { chunks++; result.WriteString(text); return nil })
	receipt["state"] = out.State
	receipt["actual_model"] = out.NativeModel
	receipt["output_bytes"] = result.Len()
	receipt["text_chunks"] = chunks
	receipt["exact_response"] = strings.TrimSpace(result.String()) == "UCLI_LIVE_OK"
	if err != nil {
		receipt["error_code"] = safeCode(err)
		t.Fatal("native adapter probe failed; see sanitized receipt")
	}
	if out.State != "complete" || strings.TrimSpace(result.String()) != "UCLI_LIVE_OK" {
		t.Fatal("native probe did not produce the exact expected completed response")
	}
}
func safeCode(err error) string {
	var p *core.Error
	if errors.As(err, &p) {
		return p.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "unclassified"
}
