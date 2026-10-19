package state

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ucli.local/ucli/internal/core"
)

func nativeFor(a core.Attempt) core.NativeContext {
	return core.NativeContext{Runtime: a.Effective.Runtime, Provider: a.Effective.Provider, Account: a.Effective.Account, SessionID: "synthetic-session", Durability: "ephemeral"}
}

func TestNativeContextAtomicReplayRestartAndFinish(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	a := accept(t, s, command("native-context", "workspace-a", "read-only"))
	native := nativeFor(a)
	native.Provenance = "allocated"
	event, err := s.RecordNativeContext(background, a.ID, a.Owner.ID, native)
	if err != nil || event.Type != "runtime_context" || event.Sequence != 2 {
		t.Fatalf("record: %+v %v", event, err)
	}
	var payload core.NativeContext
	if err = json.Unmarshal([]byte(event.Text), &payload); err != nil || payload != native {
		t.Fatalf("payload: %+v %v", payload, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, home)
	replay, err := s.RecordNativeContext(background, a.ID, a.Owner.ID, native)
	if err != nil || !reflect.DeepEqual(replay, event) {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	got, err := s.Finish(background, a.ID, a.Owner.ID, "complete", "synthetic result", "")
	if err != nil || got.NativeContext == nil || *got.NativeContext != native {
		t.Fatalf("finish lost context: %+v %v", got, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, home)
	got, err = s.GetAttempt(background, a.ID)
	if err != nil || got.NativeContext == nil || *got.NativeContext != native {
		t.Fatalf("restart lost context: %+v %v", got, err)
	}
	events, err := s.Events(background, a.ID, 0, 10)
	if err != nil || len(events) != 4 || events[1].Sequence != event.Sequence || events[2].Type != "terminal_result" {
		t.Fatalf("events: %+v %v", events, err)
	}
	_, err = s.RecordNativeContext(background, a.ID, a.Owner.ID, native)
	requireCode(t, err, "invalid_state")
}

func TestNativeContextRejectsOwnershipNamespaceAndReplacement(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("context-guards", "workspace-a", "read-only"))
	native := nativeFor(a)
	invalid := native
	invalid.Provenance = "accepted-turn"
	_, invalidErr := s.RecordNativeContext(background, a.ID, a.Owner.ID, invalid)
	requireCode(t, invalidErr, "invalid_input")
	_, err := s.RecordNativeContext(background, a.ID, "wrong-owner", native)
	requireCode(t, err, "owner_mismatch")
	_, err = s.AppendEvent(background, core.Event{AttemptID: a.ID, Type: "runtime_context", Text: "{}"}, a.Owner.ID)
	requireCode(t, err, "invalid_input")
	for _, field := range []string{"runtime", "provider", "account"} {
		changed := native
		switch field {
		case "runtime":
			changed.Runtime = "other"
		case "provider":
			changed.Provider = "other"
		case "account":
			changed.Account = "other"
		}
		_, err = s.RecordNativeContext(background, a.ID, a.Owner.ID, changed)
		requireCode(t, err, "native_context_conflict")
	}
	for _, session := range []string{"", "   ", "bad\nvalue", strings.Repeat("x", 257)} {
		changed := native
		changed.SessionID = session
		_, err = s.RecordNativeContext(background, a.ID, a.Owner.ID, changed)
		requireCode(t, err, "invalid_input")
	}
	changed := native
	changed.Durability = "probably"
	_, err = s.RecordNativeContext(background, a.ID, a.Owner.ID, changed)
	requireCode(t, err, "invalid_input")
	if _, err = s.RecordNativeContext(background, a.ID, a.Owner.ID, native); err != nil {
		t.Fatal(err)
	}
	changed = native
	changed.SessionID = "other-session"
	_, err = s.RecordNativeContext(background, a.ID, a.Owner.ID, changed)
	requireCode(t, err, "native_context_conflict")
	changed = native
	changed.Durability = "durable"
	_, err = s.RecordNativeContext(background, a.ID, a.Owner.ID, changed)
	requireCode(t, err, "native_context_conflict")
	if _, err = s.Finish(background, a.ID, a.Owner.ID, "unknown", "", ""); err != nil {
		t.Fatal(err)
	}
	_, err = s.RecordNativeContext(background, a.ID, a.Owner.ID, native)
	requireCode(t, err, "invalid_state")
}

func TestNativeContextEventFailureRollsBackAttempt(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("context-rollback", "workspace-a", "read-only"))
	_, err := s.db.ExecContext(background, "CREATE TRIGGER synthetic_context_failure BEFORE INSERT ON events WHEN json_extract(NEW.body,'$.type')='runtime_context' BEGIN SELECT RAISE(FAIL,'synthetic context event failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RecordNativeContext(background, a.ID, a.Owner.ID, nativeFor(a))
	requireCode(t, err, "state_unavailable")
	got, err := s.GetAttempt(background, a.ID)
	if err != nil || got.NativeContext != nil {
		t.Fatalf("partial context committed: %+v %v", got, err)
	}
	events, err := s.Events(background, a.ID, 0, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("unexpected events: %+v %v", events, err)
	}
	if _, err = s.db.ExecContext(background, "DROP TRIGGER synthetic_context_failure"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordNativeContext(background, a.ID, a.Owner.ID, nativeFor(a)); err != nil {
		t.Fatal(err)
	}
}

func TestNativeContextConcurrentReplayAndConflict(t *testing.T) {
	for _, same := range []bool{true, false} {
		home := t.TempDir()
		s := openTest(t, home)
		other := openTest(t, home)
		a := accept(t, s, command("context-race", "workspace-a", "read-only"))
		native := nativeFor(a)
		var results [2]core.Event
		var errs [2]error
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, store := range []*Store{s, other} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				value := native
				if !same && i == 1 {
					value.SessionID = "different"
				}
				results[i], errs[i] = store.RecordNativeContext(background, a.ID, a.Owner.ID, value)
			}()
		}
		close(start)
		wg.Wait()
		if same {
			if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
				t.Fatalf("duplicate race: %+v %+v", results, errs)
			}
		} else {
			wins, conflicts := 0, 0
			for _, err := range errs {
				if err == nil {
					wins++
				}
				if code(err) == "native_context_conflict" {
					conflicts++
				}
			}
			if wins != 1 || conflicts != 1 {
				t.Fatalf("replacement race: %+v", errs)
			}
		}
		events, err := s.Events(background, a.ID, 0, 10)
		if err != nil || len(events) != 2 {
			t.Fatalf("duplicate event: %+v %v", events, err)
		}
	}
}
