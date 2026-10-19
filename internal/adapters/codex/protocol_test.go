package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ucli.local/ucli/internal/core"
)

func TestStrictEnvelopeBoundaries(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{"id":1,"result":{}} {}`, `{"id":1,"result":{},"result":{}}`,
		`{"id":1,"ID":2,"result":{}}`, `{"id":null,"result":{}}`, `{"id":1.2,"result":{}}`,
		`{"id":1,"result":{},"error":{}}`, `{"id":1,"result":{},"params":{}}`,
		`{"id":1,"result":{"x":"\ud800"}}`, `{"id":1,"result":{"x":"\udc00"}}`,
		`{"id":1,"result":{"x":"\ud800\u0041"}}`, `{"id":1,"result":{"x":1,"\u0078":2}}`,
		`{"jsonrpc":"2.0","id":1,"result":{}}`, `{"id":1,"error":"SECRET"}`,
		`{"method":"x","params":null}`, `{"method":"x","params":{},"result":{}}`,
		`{"method":"x","params":{"a":{"a":1,"A":2}}}`, `{"method":"x","params":{"bad":"` + string([]byte{255}) + `"}}`,
		`{"method":"x","params":{"nested":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}}`,
		strings.Repeat(" ", maxFrame) + `{}`,
	} {
		if _, err := decode([]byte(raw)); err == nil {
			t.Fatalf("accepted malformed envelope %.100q", raw)
		}
	}
	for _, raw := range []string{`{"id":1,"result":{"text":"\ud83d\ude00"}}`, `{"id":"approval","method":"x","params":{}}`, `{"method":"metadata","params":{"text":"\\ud800"}}`} {
		if _, err := decode([]byte(raw)); err != nil {
			t.Fatalf("rejected valid envelope %q: %v", raw, err)
		}
	}
}

func streamEvent(method string, params any) envelope {
	raw, _ := json.Marshal(params)
	return envelope{Method: method, Params: raw}
}

func TestStreamOutputBoundAndIdentityState(t *testing.T) {
	itemEvent := func(method, id, text string) envelope {
		return streamEvent(method, map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]string{"id": id, "type": "agentMessage", "text": text}})
	}
	delta := func(id, text string) envelope {
		return streamEvent("item/agentMessage/delta", map[string]string{"threadId": "thread", "turnId": "turn", "itemId": id, "delta": text})
	}
	for _, invalid := range []envelope{
		itemEvent("item/started", "item", ""),  // duplicate
		itemEvent("item/started", "other", ""), // interleaved
		delta("other", "wrong item"),
		streamEvent("item/agentMessage/delta", map[string]string{"ThreadId": "thread", "turnId": "turn", "itemId": "item", "delta": "wrong casing"}),
		streamEvent("turn/completed", map[string]any{"threadId": "thread", "turn": map[string]any{"id": "other", "status": "interrupted", "error": nil, "items": []any{}}}),
	} {
		s := newTurnStream("thread", "turn")
		if _, err := s.consume(itemEvent("item/started", "item", ""), func(string) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if _, err := s.consume(invalid, func(string) error { return nil }); err == nil {
			t.Fatalf("accepted invalid transition %s", invalid.Method)
		}
	}
	s := newTurnStream("thread", "turn")
	count := 0
	emit := func(text string) error { count += len(text); return nil }
	_, _ = s.consume(itemEvent("item/started", "item", ""), emit)
	chunk := strings.Repeat("x", maxFrame/2)
	for i := 0; i < core.MaxOutputBytes/len(chunk); i++ {
		if _, err := s.consume(delta("item", chunk), emit); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.consume(delta("item", "x"), emit); err == nil || count != core.MaxOutputBytes {
		t.Fatal("output cap did not stop delivery")
	}
}

func TestFrameEventAndRequestBudgets(t *testing.T) {
	r := &rpc{buffer: []byte("{\"method\":\"metadata\",\"params\":{}}\n"), frames: maxEvents}
	if _, err := r.read(context.Background()); err == nil {
		t.Fatal("event count exceeded without error")
	}
	r = &rpc{buffer: []byte(strings.Repeat("x", maxFrame))}
	if _, err := r.read(context.Background()); err == nil {
		t.Fatal("unterminated oversized frame accepted")
	}
	r = &rpc{}
	if sent, err := r.send(context.Background(), map[string]string{"input": strings.Repeat("x", maxFrame)}); err == nil || sent {
		t.Fatal("oversized request attempted a write")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sent, err := r.send(ctx, map[string]string{}); !errors.Is(err, context.Canceled) || sent {
		t.Fatal("canceled request attempted a write")
	}
}

func TestNativeToolsRequireExplicitPermissionAndMatchingLifecycle(t *testing.T) {
	event := func(method, id, kind, status string) envelope {
		return streamEvent(method, map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]string{"id": id, "type": kind, "status": status, "text": "SECRET tool payload"}})
	}
	emit := func(string) error { t.Fatal("non-agent payload emitted"); return nil }
	for _, kind := range []string{"userMessage", "reasoning", "commandExecution", "fileChange"} {
		s := newTurnStream("thread", "turn")
		s.native = true
		s.tools = true
		if _, err := s.consume(event("item/started", "aux", kind, "inProgress"), emit); err != nil {
			t.Fatal(kind, err)
		}
		if kind == "commandExecution" {
			if _, err := s.consume(streamEvent("item/commandExecution/outputDelta", map[string]string{"threadId": "thread", "turnId": "turn", "itemId": "aux", "delta": "SECRET"}), emit); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.consume(event("item/completed", "aux", kind, "completed"), emit); err != nil {
			t.Fatal(kind, err)
		}
		if _, err := s.consume(event("item/completed", "aux", kind, "completed"), emit); err == nil {
			t.Fatal("duplicate completion")
		}
	}
	read := newTurnStream("thread", "turn")
	read.native = true
	if _, err := read.consume(event("item/started", "aux", "commandExecution", "inProgress"), emit); err == nil {
		t.Fatal("read-only admitted command tool")
	}
	full := newTurnStream("thread", "turn")
	full.native = true
	full.tools = true
	if _, err := full.consume(event("item/completed", "aux", "commandExecution", "completed"), emit); err == nil {
		t.Fatal("orphan tool completed")
	}
	full.consume(event("item/started", "aux", "commandExecution", "inProgress"), emit)
	if _, err := full.consume(event("item/completed", "aux", "commandExecution", "inProgress"), emit); err == nil {
		t.Fatal("nonterminal tool completed")
	}
	if _, err := full.consume(streamEvent("item/commandExecution/outputDelta", map[string]string{"threadId": "wrong", "turnId": "turn", "itemId": "aux"}), emit); err == nil {
		t.Fatal("foreign tool detail")
	}
	full.outputBytes = 1
	if _, err := full.consume(streamEvent("turn/completed", map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn", "status": "completed", "error": nil, "items": []any{}}}), emit); err == nil {
		t.Fatal("unsettled native item completed turn")
	}
}
