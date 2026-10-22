package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
)

var reviewedMethods = []string{
	"turn/plan/updated", "turn/moderationMetadata", "item/plan/delta",
	"item/commandExecution/terminalInteraction", "item/fileChange/patchUpdated",
	"item/mcpToolCall/progress", "item/autoApprovalReview/started", "item/autoApprovalReview/completed",
}

func planFixture() map[string]any {
	return map[string]any{"threadId": "native-thread", "turnId": "native-turn", "explanation": nil,
		"plan": []any{map[string]any{"step": "SECRET checklist", "status": "completed"}}}
}

// Runs inside the actual owned test child, after its native-style handshake and
// turn ACK. Only a matching turn/completed below can establish a terminal result.
func notificationFixture(mode string, write func(any)) {
	notify := func(method string, params any) { write(map[string]any{"method": method, "params": params}) }
	p := planFixture()
	method := "turn/plan/updated"
	if strings.HasPrefix(mode, "diagnostic:") {
		notify(strings.TrimPrefix(mode, "diagnostic:"), map[string]any{"threadId": "native-thread", "turnId": "native-turn", "payload": "SECRET payload"})
		return
	}
	switch mode {
	case "moderation":
		method = "turn/moderationMetadata"
		p = map[string]any{"threadId": "native-thread", "turnId": "native-turn", "metadata": map[string]any{"private": "SECRET", "authorization": true}}
	case "warning":
		notify("warning", map[string]any{"threadId": "native-thread", "message": "SECRET warning"})
	case "foreign-thread":
		p["threadId"] = "foreign"
	case "foreign-turn":
		p["turnId"] = "foreign"
	case "missing-identity":
		delete(p, "threadId")
	case "malformed":
		p["plan"] = nil
	case "bound":
		p["explanation"] = strings.Repeat("x", 64*1024+1)
	}
	notify(method, p)
	if mode == "eof" || mode == "foreign-thread" || mode == "foreign-turn" || mode == "missing-identity" || mode == "malformed" || mode == "bound" {
		return
	}
	if mode == "tools" {
		for _, kind := range []string{"commandExecution", "fileChange"} {
			item := map[string]any{"id": kind, "type": kind, "status": "inProgress"}
			params := map[string]any{"threadId": "native-thread", "turnId": "native-turn", "item": item}
			notify("item/started", params)
			detail := map[string]any{"threadId": "native-thread", "turnId": "native-turn", "itemId": kind}
			if kind == "commandExecution" {
				detail["processId"], detail["stdin"] = "synthetic-process", "SECRET input"
				notify("item/commandExecution/terminalInteraction", detail)
			} else {
				detail["changes"] = []any{map[string]any{"path": "SECRET/path", "diff": "SECRET diff", "kind": map[string]any{"type": "update", "move_path": nil}}}
				notify("item/fileChange/patchUpdated", detail)
			}
			item["status"] = "completed"
			notify("item/completed", params)
		}
	}
	status := "completed"
	var failure any
	if mode == "failed" {
		status = "failed"
		failure = map[string]any{"message": "SECRET failure", "codexErrorInfo": "usageLimitExceeded"}
	} else {
		item := map[string]any{"id": "text", "type": "agentMessage", "text": ""}
		params := map[string]any{"threadId": "native-thread", "turnId": "native-turn", "item": item}
		notify("item/started", params)
		item["text"] = "synthetic complete"
		notify("item/completed", params)
	}
	notify("turn/completed", map[string]any{"threadId": "native-thread", "turn": map[string]any{"id": "native-turn", "status": status, "items": []any{}, "error": failure}})
}

func runNotificationFixture(t *testing.T, mode string) (adapters.Outcome, string, error) {
	t.Helper()
	req := fixtureRequest(t)
	hash := sha256.Sum256([]byte("codex\x00synthetic-native-id"))
	req.Binding = &adapters.NativeBinding{ID: "connection", Runtime: "codex", Home: req.Attempt.Workspace.Dir, HomeMode: "existing", Identity: hex.EncodeToString(hash[:]), AuthMode: "chatgpt"}
	req.Attempt.Effective = core.Route{Runtime: "codex", Provider: "openai", Model: "codex/fixture-model", Account: "connection", Effort: "max", Permission: "sandbox", Mode: "native"}
	child := fixtureChild(t, req, "inspect-notification-"+mode)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output strings.Builder
	out, err := executeProtocol(ctx, req, child, func(text string) error { output.WriteString(text); return nil })
	if strings.Contains(fmt.Sprint(out, err, output.String()), "SECRET") {
		t.Fatal("private notification data escaped")
	}
	return out, output.String(), err
}

func TestOwnedMetadataRequiresTerminalEvidence(t *testing.T) {
	for _, mode := range []string{"plan", "moderation", "warning", "tools", "failed", "eof", "foreign-thread", "foreign-turn", "missing-identity", "malformed", "bound"} {
		t.Run(mode, func(t *testing.T) {
			out, output, err := runNotificationFixture(t, mode)
			switch mode {
			case "plan", "moderation", "warning", "tools":
				if out.State != "complete" || err != nil || output != "synthetic complete" {
					t.Fatalf("matching completion not reached: %+v %q %v", out, output, err)
				}
			case "failed":
				if out.State != "failed" || err == nil || output != "" {
					t.Fatalf("metadata masked failed terminal: %+v %q %v", out, output, err)
				}
			default:
				if out.State != "unknown" || err == nil || output != "" {
					t.Fatalf("metadata settled unconfirmed execution: %+v %q %v", out, output, err)
				}
			}
		})
	}
}

func TestOwnedReviewedDiagnosticsUseOnlyFixedNames(t *testing.T) {
	for _, method := range append(append([]string{}, reviewedMethods...), "item/SECRET-unreviewed") {
		t.Run(method, func(t *testing.T) {
			out, _, err := runNotificationFixture(t, "diagnostic:"+method)
			if err == nil || out.State != "unknown" {
				t.Fatalf("unsupported event escaped: %+v %v", out, err)
			}
			if method != "item/SECRET-unreviewed" && !strings.Contains(out.Message, method) {
				t.Fatalf("known method diagnostic lost: %+v", out)
			}
		})
	}
}

func TestMetadataSchemasRetainNoPayloadOrTerminalState(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		ok   bool
	}{
		{"valid", func(map[string]any) {}, true},
		{"empty-list", func(p map[string]any) { p["plan"] = []any{} }, true},
		{"omitted-explanation", func(p map[string]any) { delete(p, "explanation") }, true},
		{"additive-field", func(p map[string]any) { p["future"] = "SECRET" }, true},
		{"null-list", func(p map[string]any) { p["plan"] = nil }, false},
		{"missing-list", func(p map[string]any) { delete(p, "plan") }, false},
		{"null-step", func(p map[string]any) { p["plan"] = []any{nil} }, false},
		{"null-text", func(p map[string]any) { p["plan"] = []any{map[string]any{"step": nil, "status": "pending"}} }, false},
		{"unknown-status", func(p map[string]any) { p["plan"] = []any{map[string]any{"step": "private", "status": "done"}} }, false},
		{"large-step", func(p map[string]any) {
			p["plan"] = []any{map[string]any{"step": strings.Repeat("x", 64*1024+1), "status": "pending"}}
		}, false},
		{"many-steps", func(p map[string]any) { p["plan"] = make([]any, 257) }, false},
		{"bad-explanation", func(p map[string]any) { p["explanation"] = false }, false},
		{"missing-turn", func(p map[string]any) { delete(p, "turnId") }, false},
		{"foreign-thread", func(p map[string]any) { p["threadId"] = "other" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTurnStream("native-thread", "native-turn")
			s.native = true // Read-only may observe turn metadata; no tools enabled.
			p := planFixture()
			tc.edit(p)
			state, err := s.consume(streamEvent("turn/plan/updated", p), func(string) error { t.Fatal("metadata emitted"); return nil })
			if (err == nil) != tc.ok || state != "" || s.outputBytes != 0 || len(s.items)+len(s.aux) != 0 {
				t.Fatalf("unexpected metadata result: %q %v %+v", state, err, s)
			}
		})
	}
	for _, value := range []any{nil, "SECRET", true, 7, []any{1}, map[string]any{"SECRET": true}} {
		s := newTurnStream("native-thread", "native-turn")
		s.native = true
		p := map[string]any{"threadId": s.thread, "turnId": s.turn, "metadata": value}
		if state, err := s.consume(streamEvent("turn/moderationMetadata", p), func(string) error { t.Fatal("metadata emitted"); return nil }); state != "" || err != nil {
			t.Fatalf("valid JsonValue rejected: %v", err)
		}
		delete(p, "metadata")
		if _, err := s.consume(streamEvent("turn/moderationMetadata", p), nil); err == nil {
			t.Fatal("missing moderation field accepted")
		}
	}
}

func TestToolDetailsRequireActiveAllowedItemAndSchema(t *testing.T) {
	for _, kind := range []string{"commandExecution", "fileChange"} {
		for _, bad := range []string{"", "read-only", "orphan", "completed", "wrong-kind", "foreign-turn", "missing-thread", "missing-payload", "malformed-payload"} {
			t.Run(kind+"/"+bad, func(t *testing.T) {
				s := newTurnStream("thread", "turn")
				s.native, s.tools = true, bad != "read-only"
				s.aux["item"] = &auxItem{kind: kind, complete: bad == "completed"}
				if bad == "orphan" {
					delete(s.aux, "item")
				}
				if bad == "wrong-kind" {
					s.aux["item"].kind = "reasoning"
				}
				p := map[string]any{"threadId": "thread", "turnId": "turn", "itemId": "item"}
				method, field := "item/commandExecution/terminalInteraction", "stdin"
				p["processId"], p[field] = "process", "SECRET"
				if kind == "fileChange" {
					method, field = "item/fileChange/patchUpdated", "changes"
					p[field] = []any{map[string]any{"path": "SECRET", "diff": "SECRET", "kind": map[string]any{"type": "update", "move_path": nil}}}
				}
				switch bad {
				case "foreign-turn":
					p["turnId"] = "other"
				case "missing-thread":
					delete(p, "threadId")
				case "missing-payload":
					delete(p, field)
				case "malformed-payload":
					p[field] = false
				}
				state, err := s.consume(streamEvent(method, p), func(string) error { t.Fatal("tool detail emitted"); return nil })
				if state != "" || (err == nil) != (bad == "") {
					t.Fatalf("unexpected tool detail admission: %q %v", state, err)
				}
			})
		}
	}
	// A known method at setup is still forbidden and gets a safe fixed label.
	err := setupEvent(streamEvent("turn/plan/updated", planFixture()), "native-thread")
	if err == nil || !strings.Contains(err.Error(), "turn/plan/updated") || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("setup diagnostic boundary", err)
	}
}

func TestPatchKindAndMetadataPayloadBounds(t *testing.T) {
	s := newTurnStream("thread", "turn")
	s.native, s.tools = true, true
	s.aux["file"] = &auxItem{kind: "fileChange"}
	for _, kind := range []any{map[string]any{"type": "add"}, map[string]any{"type": "delete"}, map[string]any{"type": "update"}, map[string]any{"type": "update", "move_path": "SECRET"}, map[string]any{"type": "future"}, map[string]any{"type": "update", "move_path": false}, nil} {
		change := map[string]any{"path": "SECRET", "diff": "SECRET", "kind": kind}
		p := map[string]any{"threadId": "thread", "turnId": "turn", "itemId": "file", "changes": []any{change}}
		_, err := s.consume(streamEvent("item/fileChange/patchUpdated", p), nil)
		raw, _ := json.Marshal(kind)
		bad := kind == nil || strings.Contains(string(raw), "future") || strings.Contains(string(raw), "false")
		if (err != nil) != bad {
			t.Fatalf("kind %s: %v", raw, err)
		}
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{`), json.RawMessage(`"` + strings.Repeat("x", maxFrame) + `"`)} {
		_, err := s.nativeNotification("turn/moderationMetadata", map[string]json.RawMessage{"threadId": json.RawMessage(`"thread"`), "turnId": json.RawMessage(`"turn"`), "metadata": raw})
		if err == nil {
			t.Fatal("unbounded/invalid moderation accepted")
		}
	}
}
