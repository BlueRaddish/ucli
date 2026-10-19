package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
)

func TestErrorNotificationWaitsForConfirmedFailedTerminal(t *testing.T) {
	for _, tc := range []struct {
		kind string
		exit int
	}{{"unauthorized", 3}, {"usageLimitExceeded", 4}, {"badRequest", 5}, {"serverOverloaded", 8}} {
		t.Run(tc.kind, func(t *testing.T) {
			out, output, err := runFixture(t, "error-failed-"+tc.kind)
			var problem *core.Error
			if out.State != "failed" || output != "" || !errors.As(err, &problem) || problem.Exit != tc.exit || problem.Acceptance != "accepted" || problem.SideEffects != "unknown" || out.Message != problem.Message || !strings.Contains(out.Message, tc.kind) {
				t.Fatalf("wrong confirmed failure: %+v %v", out, err)
			}
			if strings.Contains(out.Message, "SECRET") || strings.Contains(err.Error(), "SECRET") {
				t.Fatal("native diagnostic escaped")
			}
		})
	}
}
func TestRetryNotificationCanRecoverWithoutLocalRedispatch(t *testing.T) {
	out, output, err := runFixture(t, "error-retry-complete")
	if err != nil || out.State != "complete" || output != "hello \u4e16\u754c" || strings.Contains(out.Message, "SECRET") {
		t.Fatal(out, output, err)
	}
}
func TestMalformedAndUnboundErrorNotificationsStayUnknown(t *testing.T) {
	for _, mode := range []string{"error-mismatch", "error-missing-turn", "error-null-retry", "error-missing-retry", "error-malformed-info"} {
		t.Run(mode, func(t *testing.T) {
			out, _, err := runFixture(t, mode)
			if out.State != "unknown" || err == nil || strings.Contains(out.Message, "SECRET") {
				t.Fatal(out, err)
			}
		})
	}
}
func TestErrorWithoutTerminalRemainsUnknownAtDeadline(t *testing.T) {
	req := fixtureRequest(t)
	child := fixtureChild(t, req, "error-no-terminal")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	out, err := executeProtocol(ctx, req, child, func(string) error { t.Fatal("unexpected output"); return nil })
	if out.State != "unknown" || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(out.Message, "badRequest; willRetry=false") || strings.Contains(out.Message, "SECRET") {
		t.Fatal(out, err)
	}
}
func TestFutureErrorLabelIsRedactedOnConfirmedFailure(t *testing.T) {
	out, _, err := runFixture(t, "error-unknown-kind")
	var problem *core.Error
	if out.State != "failed" || !errors.As(err, &problem) || problem.Exit != 8 || !strings.Contains(out.Message, "other") || strings.Contains(out.Message, "SECRET") {
		t.Fatal(out, err)
	}
}
func TestErrorEnumShapesAndRequiredMessage(t *testing.T) {
	for _, tc := range []struct {
		info  string
		kind  string
		valid bool
	}{
		{`null`, "other", true}, {`"SECRET future"`, "other", true}, {`{"SECRET future":false}`, "other", true},
		{`{"httpConnectionFailed":{"httpStatusCode":401}}`, "httpConnectionFailed", true},
		{`{"responseStreamDisconnected":{}}`, "responseStreamDisconnected", true},
		{`{"activeTurnNotSteerable":{"turnKind":"compact"}}`, "activeTurnNotSteerable", true},
		{`false`, "", false}, {`[]`, "", false}, {`{"httpConnectionFailed":false}`, "", false},
		{`{"httpConnectionFailed":{"httpStatusCode":65536}}`, "", false}, {`{"httpConnectionFailed":{"httpStatusCode":"401"}}`, "", false},
		{`{"activeTurnNotSteerable":{"turnKind":"SECRET future"}}`, "", false},
	} {
		raw := json.RawMessage(`{"message":"SECRET","codexErrorInfo":` + tc.info + `}`)
		kind, err := turnErrorKind(raw)
		if (err == nil) != tc.valid || kind != tc.kind {
			t.Fatalf("enum acceptance mismatch: %q %v", kind, err)
		}
	}
	for _, raw := range []string{`{}`, `{"message":null}`, `{"message":false}`, `{"message":"SECRET","additionalDetails":false}`} {
		if _, err := turnErrorKind(json.RawMessage(raw)); err == nil {
			t.Fatal("malformed error accepted")
		}
	}
}

func TestMatchingFailureCanSettleDuringInterrupt(t *testing.T) {
	req := fixtureRequest(t)
	child := fixtureChild(t, req, "error-on-interrupt")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	out, err := executeProtocol(ctx, req, child, func(string) error { t.Fatal("unexpected output"); return nil })
	var problem *core.Error
	if out.State != "failed" || !errors.As(err, &problem) || problem.Exit != 5 || out.Message != problem.Message {
		t.Fatal(out, err)
	}
}
func TestConfigWarningRemainsRejectedAndRedacted(t *testing.T) {
	for _, method := range []string{"configWarning"} {
		s := newTurnStream("thread", "turn")
		state, err := s.consume(envelope{Method: method, Params: json.RawMessage(`{"message":"SECRET"}`)}, func(string) error { return nil })
		if state != "" || err == nil || !strings.Contains(err.Error(), method) || strings.Contains(err.Error(), "SECRET") {
			t.Fatal(state, err)
		}
	}
}

func TestTerminalClassificationSupersedesTransientError(t *testing.T) {
	for _, tc := range []struct {
		retry        bool
		terminalInfo string
		exit         int
	}{
		{false, "null", 3}, {true, "null", 8}, {false, `"badRequest"`, 5},
	} {
		s := newTurnStream("thread", "turn")
		notification := map[string]any{"threadId": "thread", "turnId": "turn", "willRetry": tc.retry, "error": map[string]any{"message": "SECRET", "codexErrorInfo": "unauthorized"}}
		if state, err := s.consume(envelope{Method: "error", Params: mustJSON(notification)}, func(string) error { return nil }); state != "" || err != nil {
			t.Fatal(state, err)
		}
		terminal := json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","items":[],"status":"failed","error":{"message":"SECRET","codexErrorInfo":` + tc.terminalInfo + `}}}`)
		state, err := s.consume(envelope{Method: "turn/completed", Params: terminal}, func(string) error { return nil })
		var problem *core.Error
		if state != "failed" || !errors.As(err, &problem) || problem.Exit != tc.exit || strings.Contains(err.Error(), "SECRET") {
			t.Fatal(state, err)
		}
	}
}
