package codex

import (
	"strings"
	"testing"
	"ucli.local/ucli/internal/adapters"
)

func TestCurrentProfileDisablesAndChecksAsyncCompaction(t *testing.T) {
	old := adapters.NativeBinding{SHA256: "dd13bdb162a184fa04039488b234a8258ef9b76f84e926c505d5742b19249a6a", HomeMode: "existing"}
	current := adapters.NativeBinding{SHA256: reviewedSHA256, HomeMode: "existing"}
	for _, permission := range []string{"read-only", "sandbox"} {
		if strings.Contains(strings.Join(boundNativeArgs("scratch", permission, old), " "), "async_compaction") {
			t.Fatal("new feature sent to old profile")
		}
		if !strings.Contains(strings.Join(boundNativeArgs("scratch", permission, current), " "), "features.async_compaction=false") {
			t.Fatal("background compaction not disabled")
		}
		c := nativeConfigFixture(permission)
		if !safeBoundNativeConfig(objectFields(mustJSON(c)), permission, old) {
			t.Fatal("old profile refused")
		}
		if safeBoundNativeConfig(objectFields(mustJSON(c)), permission, current) {
			t.Fatal("missing current feature admitted")
		}
		for _, v := range []any{true, nil, "false", map[string]any{}} {
			c["features"].(map[string]any)["async_compaction"] = v
			if safeBoundNativeConfig(objectFields(mustJSON(c)), permission, current) {
				t.Fatalf("invalid feature accepted: %v", v)
			}
		}
		c["features"].(map[string]any)["async_compaction"] = false
		if !safeBoundNativeConfig(objectFields(mustJSON(c)), permission, current) {
			t.Fatal("current profile refused")
		}
		c["openai_base_url"] = "https://api.openai.com/v1"
		if safeBoundNativeConfig(objectFields(mustJSON(c)), permission, current) {
			t.Fatal("endpoint guard bypassed")
		}
	}
}
