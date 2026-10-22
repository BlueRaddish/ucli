package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

// Both reviewed emitters summarize only the last completed, nonblank agent
// message whose phase is final_answer or absent/null. Other completed items
// remain established by their live lifecycle, not repeated in the summary.
func TestTerminalItemsViewMatchesReviewedSummaryContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		view   any
		ids    []string
		change func(*turnStream, map[string]any)
		ok     bool
	}{
		{name: "summary", view: "summary", ids: []string{"final"}, ok: true},
		{name: "full", view: "full", ids: []string{"user", "reasoning", "earlier", "final", "commentary", "blank"}, ok: true},
		{name: "summary-earlier-final", view: "summary", ids: []string{"earlier"}},
		{name: "summary-commentary", view: "summary", ids: []string{"commentary"}},
		{name: "summary-blank", view: "summary", ids: []string{"blank"}},
		{name: "summary-empty", view: "summary"},
		{name: "summary-duplicate", view: "summary", ids: []string{"final", "final"}},
		{name: "summary-auxiliary", view: "summary", ids: []string{"reasoning"}},
		{name: "full-incomplete", view: "full", ids: []string{"final"}},
		{name: "legacy-incomplete", ids: []string{"final"}},
		{name: "legacy-empty", ok: true},
		{name: "unknown-view", view: "SECRET-future", ids: []string{"user", "reasoning", "earlier", "final", "commentary", "blank"}},
		{name: "malformed-view", view: false, ids: []string{"user", "reasoning", "earlier", "final", "commentary", "blank"}},
		{name: "explicit-null-view", view: json.RawMessage("null"), ids: []string{"user", "reasoning", "earlier", "final", "commentary", "blank"}},
		{name: "not-loaded-nonempty", view: "notLoaded", ids: []string{"final"}},
		{name: "not-loaded-omits-final", view: "notLoaded"},
		{name: "summary-not-native", view: "summary", ids: []string{"final"}, change: func(s *turnStream, _ map[string]any) { s.native = false }},
		{name: "summary-unsettled-aux", view: "summary", ids: []string{"final"}, change: func(s *turnStream, _ map[string]any) { s.aux["reasoning"].complete = false }},
		{name: "summary-unsettled-message", view: "summary", ids: []string{"final"}, change: func(s *turnStream, _ map[string]any) { s.items["earlier"].complete = false }},
		{name: "summary-wrong-phase", view: "summary", ids: []string{"final"}, change: func(_ *turnStream, turn map[string]any) {
			turn["items"].([]any)[0].(map[string]any)["phase"] = "commentary"
		}},
		{name: "summary-unknown-phase", view: "summary", ids: []string{"final"}, change: func(_ *turnStream, turn map[string]any) {
			turn["items"].([]any)[0].(map[string]any)["phase"] = "SECRET"
		}},
		{name: "summary-conflicting-text", view: "summary", ids: []string{"final"}, change: func(_ *turnStream, turn map[string]any) { turn["items"].([]any)[0].(map[string]any)["text"] = "SECRET" }},
		{name: "summary-foreign-turn", view: "summary", ids: []string{"final"}, change: func(_ *turnStream, turn map[string]any) { turn["id"] = "foreign" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTurnStream("thread", "turn")
			s.native = true
			all := terminalViewItems(t, s)
			items := []any{}
			for _, id := range tc.ids {
				items = append(items, all[id])
			}
			turn := map[string]any{"id": "turn", "status": "completed", "items": items}
			if tc.view != nil {
				turn["itemsView"] = tc.view
			}
			if tc.change != nil {
				tc.change(s, turn)
			}
			state, err := s.consume(streamEvent("turn/completed", map[string]any{"threadId": "thread", "turn": turn}), func(string) error { t.Fatal("terminal emitted repeated text"); return nil })
			if tc.ok && (state != "complete" || err != nil) || !tc.ok && (state != "" || err == nil) {
				t.Fatalf("unexpected state=%q err=%v", state, err)
			}
			if err != nil && strings.Contains(err.Error(), "SECRET") {
				t.Fatal("native details leaked")
			}
		})
	}
}

func terminalViewItems(t *testing.T, s *turnStream) map[string]map[string]any {
	t.Helper()
	items := map[string]map[string]any{
		"user":       {"id": "user", "type": "userMessage"},
		"reasoning":  {"id": "reasoning", "type": "reasoning"},
		"earlier":    {"id": "earlier", "type": "agentMessage", "text": "Earlier answer", "phase": nil},
		"final":      {"id": "final", "type": "agentMessage", "text": "Final answer", "phase": "final_answer"},
		"commentary": {"id": "commentary", "type": "agentMessage", "text": "Later commentary", "phase": "commentary"},
		"blank":      {"id": "blank", "type": "agentMessage", "text": "  ", "phase": nil},
	}
	for _, id := range []string{"user", "reasoning", "earlier", "final", "commentary", "blank"} {
		item := items[id]
		for _, method := range []string{"item/started", "item/completed"} {
			if _, err := s.consume(streamEvent(method, map[string]any{"threadId": "thread", "turnId": "turn", "item": item}), func(string) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	return items
}

func TestTerminalSummaryLegacyPhaseAndNotLoaded(t *testing.T) {
	for _, phase := range []any{nil, "final_answer", "commentary", "partial_answer", "SECRET", false} {
		s := newTurnStream("thread", "turn")
		s.native = true
		item := map[string]any{"id": "message", "type": "agentMessage", "text": "answer"}
		_, err := s.consume(streamEvent("item/started", map[string]any{"threadId": "thread", "turnId": "turn", "item": item}), func(string) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		item["phase"] = phase
		_, err = s.consume(streamEvent("item/completed", map[string]any{"threadId": "thread", "turnId": "turn", "item": item}), func(string) error { return nil })
		if phase == "SECRET" || phase == false {
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatal("malformed phase accepted or exposed")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		items, view := []any{item}, "summary"
		if phase == "commentary" || phase == "partial_answer" {
			items, view = []any{}, "notLoaded"
		} else if phase == nil {
			delete(item, "phase") // Absent and explicit null are the same legacy phase.
		}
		state, err := s.consume(streamEvent("turn/completed", map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn", "status": "completed", "items": items, "itemsView": view}}), func(string) error { return nil })
		if state != "complete" || err != nil {
			t.Fatalf("valid phase/view refused: %q %v", state, err)
		}
	}
}
