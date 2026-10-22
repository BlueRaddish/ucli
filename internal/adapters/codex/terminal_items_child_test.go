package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
)

// This source-shaped stream traverses real child pipes, RPC setup, item lifecycle
// and owned-process cleanup. The summary does not replace observed item receipts.
func terminalItemsFixture(mode string, write func(any)) {
	notify := func(method string, params any) { write(map[string]any{"method": method, "params": params}) }
	all := map[string]map[string]any{
		"user":       {"id": "user", "type": "userMessage", "content": []any{map[string]any{"type": "text", "text": "SECRET input", "text_elements": []any{}}}},
		"reasoning":  {"id": "reasoning", "type": "reasoning", "summary": []string{"SECRET reasoning"}, "content": []string{}},
		"earlier":    {"id": "earlier", "type": "agentMessage", "text": "Earlier answer", "phase": nil},
		"final":      {"id": "final", "type": "agentMessage", "text": "Final answer", "phase": "final_answer"},
		"commentary": {"id": "commentary", "type": "agentMessage", "text": "Later commentary", "phase": "commentary"},
		"blank":      {"id": "blank", "type": "agentMessage", "text": "  ", "phase": nil},
	}
	if mode == "not-loaded" {
		// A commentary-only turn has output but no eligible final summary item.
		all["earlier"]["phase"], all["final"]["phase"] = "commentary", "commentary"
	}
	order := []string{"user", "reasoning", "earlier", "final", "commentary", "blank"}
	for _, id := range order {
		item := all[id]
		started := make(map[string]any, len(item))
		for key, value := range item {
			started[key] = value
		}
		if item["type"] == "agentMessage" {
			started["text"] = ""
		}
		params := func(value any) map[string]any {
			return map[string]any{"threadId": "native-thread", "turnId": "native-turn", "item": value}
		}
		notify("item/started", params(started))
		if mode != "unsettled" || id != "reasoning" {
			notify("item/completed", params(item))
		}
	}
	if mode == "eof" {
		return
	}
	items := []any{all["final"]}
	view := any("summary")
	switch mode {
	case "full":
		view, items = "full", []any{}
		for _, id := range order {
			items = append(items, all[id])
		}
	case "not-loaded":
		view, items = "notLoaded", []any{}
	case "earlier":
		items = []any{all["earlier"]}
	case "commentary":
		items = []any{all["commentary"]}
	case "blank":
		items = []any{all["blank"]}
	case "duplicate":
		items = append(items, all["final"])
	case "conflicting-final":
		all["final"]["text"] = "SECRET different terminal text"
	case "malformed-view":
		view = false
	case "unknown-view":
		view = "SECRET future view"
	}
	notify("turn/completed", map[string]any{"threadId": "native-thread", "turn": map[string]any{
		"id": "native-turn", "status": "completed", "items": items, "itemsView": view, "error": nil,
	}})
}

func TestOwnedTerminalItemsViewRequiresMatchingSettledEvidence(t *testing.T) {
	for _, mode := range []string{"summary", "full", "not-loaded", "earlier", "commentary", "blank", "duplicate", "conflicting-final", "malformed-view", "unknown-view", "unsettled", "eof"} {
		t.Run(mode, func(t *testing.T) {
			req := fixtureRequest(t)
			hash := sha256.Sum256([]byte("codex\x00synthetic-native-id"))
			req.Binding = &adapters.NativeBinding{ID: "connection", Runtime: "codex", Home: req.Attempt.Workspace.Dir, HomeMode: "existing", Identity: hex.EncodeToString(hash[:]), AuthMode: "chatgpt"}
			req.Attempt.Effective = core.Route{Runtime: "codex", Provider: "openai", Model: "codex/fixture-model", Account: "connection", Effort: "max", Permission: "sandbox", Mode: "native"}
			child := fixtureChild(t, req, "inspect-terminal-items-"+mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var output strings.Builder
			out, err := executeProtocol(ctx, req, child, func(text string) error { output.WriteString(text); return nil })
			if output.String() != "Earlier answerFinal answerLater commentary  " {
				t.Fatalf("terminal duplicated, erased or replaced streamed text: %q", output.String())
			}
			if strings.Contains(fmt.Sprint(out, err, output.String()), "SECRET") {
				t.Fatal("private native detail escaped")
			}
			valid := mode == "summary" || mode == "full" || mode == "not-loaded"
			if valid {
				if out.State != "complete" || err != nil || out.NativeSession != "native-thread" || out.NativeModel != "fixture-model" {
					t.Fatalf("matching terminal refused: %+v %v", out, err)
				}
			} else if out.State != "unknown" || err == nil {
				t.Fatalf("unconfirmed terminal promoted to success: %+v %v", out, err)
			}
		})
	}
}
