package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

const catalogRows = `[{"value":"default","resolvedModel":"claude-new-fixture","displayName":"Native default","supportsEffort":true,"supportedEffortLevels":["low","high","xhigh"]},{"value":"new-family","resolvedModel":"claude-new-fixture","displayName":"New native family","supportsEffort":true,"supportedEffortLevels":["low","high","max"]},{"value":"fixed","displayName":"Fixed effort","supportsEffort":false},{"value":"unknown","displayName":"Unknown effort"},{"value":"disabled","resolvedModel":"claude-disabled","displayName":"Unavailable","disabled":true,"supportsEffort":true,"supportedEffortLevels":["low"]}]`

func catalogEnvelope(rows, id string) []byte {
	return []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"` + id + `","response":{"models":` + rows + `}}}`)
}

func catalogFixtureHost() int {
	mode := os.Args[2]
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return 80
	}
	f, e := object(scanner.Bytes())
	if e != nil || text(f, "type") != "control_request" {
		return 81
	}
	r, e := object(f["request"])
	if e != nil || text(r, "subtype") != "initialize" || string(r["hooks"]) != "null" {
		return 82
	}
	if mode == "hang" {
		time.Sleep(30 * time.Second)
		return 83
	}
	id := text(f, "request_id")
	if mode == "wrong-id" {
		id = "wrong"
	}
	if strings.HasPrefix(mode, "ui-") {
		event, session := "ui.render", id
		if mode == "ui-wrong-session" {
			session = "foreign"
		}
		if mode == "ui-wrong-event" {
			event = "different"
		}
		count := 1
		if mode == "ui-overflow" {
			count = 17
		}
		for i := 0; i < count; i++ {
			v, _ := json.Marshal(map[string]any{"type": "system", "subtype": "ui_invalidate", "event": event, "session_id": session, "uuid": "00000000-0000-4000-8000-000000000001"})
			fmt.Println(string(v))
		}
	}
	switch mode {
	case "malformed":
		fmt.Println("PRIVATE_NATIVE_SECRET invalid-json")
	case "server-request":
		fmt.Println(`{"type":"control_request","request_id":"tool","request":{"subtype":"can_use_tool"}}`)
	case "oversized":
		fmt.Println(strings.Repeat("x", (256<<10)+1))
	case "eof":
		return 0
	default:
		fmt.Println(string(catalogEnvelope(catalogRows, id)))
	}
	// Metadata must be the only client message. EOF, never a user frame, follows.
	remaining, err := io.ReadAll(os.Stdin)
	if err != nil || len(remaining) != 0 {
		return 84
	}
	if mode == "exit-disagreement" {
		return 1
	}
	return 0
}

func TestOwnedCatalogInitializationSendsNoUserTurn(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("owned Darwin launch remains unverified")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"complete", "wrong-id", "malformed", "server-request", "oversized", "eof", "exit-disagreement", "hang", "ui-valid", "ui-wrong-session", "ui-wrong-event", "ui-overflow"} {
		t.Run(mode, func(t *testing.T) {
			limit := 5 * time.Second
			if mode == "hang" {
				limit = 300 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			defer cancel()
			models, err := discoverModels(ctx, process.Spec{Executable: exe, Args: []string{"--catalog-fixture", mode}, Env: environment(t.TempDir(), t.TempDir()), Dir: t.TempDir(), FrameBytes: 64 << 10, QueueBytes: 1 << 20}, "fixture-id")
			if mode == "complete" || mode == "ui-valid" {
				if err != nil || len(models) != 5 || models[1].ID != "new-family" {
					t.Fatalf("models=%+v err=%v", models, err)
				}
				return
			}
			if err == nil || len(models) != 0 || strings.Contains(err.Error(), "PRIVATE_NATIVE") {
				t.Fatalf("unsafe metadata result models=%d err=%v", len(models), err)
			}
			if mode == "hang" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestCatalogMetadataCapabilitiesAndStrictPins(t *testing.T) {
	models, err := initializationModels(catalogEnvelope(catalogRows, "id"), "id")
	if err != nil {
		t.Fatal(err)
	}
	if !models[0].Default || models[2].EffortState != "unsupported" || models[3].EffortState != "unknown" || !models[4].Unavailable {
		t.Fatalf("capabilities changed: %+v", models)
	}
	for _, tc := range []struct {
		model, effort, native string
		ok                    bool
	}{{"default", "max", "xhigh", true}, {"new-family", "max", "max", true}, {"new-family", "medium", "", false}, {"fixed", "low", "", false}, {"unknown", "low", "", false}, {"disabled", "low", "", false}, {"removed", "low", "", false}} {
		_, native, e := selectedModel(models, core.Route{Model: "claude/" + tc.model, Effort: tc.effort})
		if (e == nil) != tc.ok || native != tc.native {
			t.Fatalf("%+v native=%q err=%v", tc, native, e)
		}
	}
	for _, rows := range []string{`null`, `[]`, `[{"value":"x","displayName":"x","supportsEffort":null}]`, `[{"value":"x","displayName":"x","supportsEffort":false,"supportedEffortLevels":["low"]}]`, `[{"value":"x","displayName":"x","supportedEffortLevels":["low","low"]}]`, `[{"value":"x","displayName":"x","disabled":1}]`, `[{"value":"x","displayName":"x","resolvedModel":null}]`, `[{"value":"x","displayName":"x"},{"value":"x","displayName":"duplicate"}]`, `[{"value":"x","displayName":"bad\u001b[31m"}]`, `[{"value":"x","displayName":"x","Value":"other"}]`} {
		if _, e := initializationModels(catalogEnvelope(rows, "id"), "id"); e == nil {
			t.Fatalf("invalid metadata admitted: %s", rows)
		}
	}
	for _, tail := range []string{`,"pending_permission_requests":[{}]`, `,"pending_user_dialog_requests":null`} {
		raw := strings.Replace(string(catalogEnvelope(catalogRows, "id")), `"subtype":"success"`, `"subtype":"success"`+tail, 1)
		if _, e := initializationModels([]byte(raw), "id"); e == nil {
			t.Fatal("pending native requests admitted")
		}
	}
}

func TestResolvedModelCannotChangeBetweenCatalogAndExecution(t *testing.T) {
	req := fixtureRequest(t, "complete", "read-only")
	model := Model{ID: "default", ResolvedID: "claude-new-other-version", EffortState: "supported", Efforts: []string{"low"}}
	out, err := execute(context.Background(), req, func(string) error { t.Fatal("mismatched model output delivered"); return nil }, model)
	var p *core.Error
	if !errors.As(err, &p) || p.Code != "claude_model_changed" || out.State != "unknown" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	model.ResolvedID = ""
	out, err = execute(context.Background(), req, func(string) error { return nil }, model)
	if !errors.As(err, &p) || p.Acceptance != "not-sent" || out.State != "failed" {
		t.Fatalf("unresolved alias dispatched: %+v %v", out, err)
	}
}

func TestCatalogEnvelopeUnknownOptionalFieldsAllowed(t *testing.T) {
	var f map[string]any
	json.Unmarshal(catalogEnvelope(catalogRows, "id"), &f)
	f["future_optional"] = true
	raw, _ := json.Marshal(f)
	if _, err := initializationModels(raw, "id"); err != nil {
		t.Fatal(err)
	}
}

func TestClaudePublicEffortsMatchFreshDispatch(t *testing.T) {
	for _, levels := range [][]string{{"low"}, {"low", "high"}, {"low", "medium", "high", "xhigh"}, {"low", "max"}, {"low", "future"}} {
		for _, public := range PublicEfforts(levels) {
			m := Model{ID: "fresh", ResolvedID: "wire-fresh", EffortState: "supported", Efforts: levels}
			_, native, err := selectedModel([]Model{m}, core.Route{Model: "claude/fresh", Effort: public})
			if err != nil || native == "" {
				t.Fatalf("advertised effort refused: %v %s %v", levels, public, err)
			}
		}
	}
	if mappedEffort([]string{"low", "high"}, "max") != "high" || mappedEffort([]string{"low", "future"}, "max") != "" || mappedEffort([]string{"low", "future"}, "low") != "low" {
		t.Fatal("highest known or unknown effort mapping changed")
	}
}

func TestResolveModelDeterministicAliasPolicy(t *testing.T) {
	a := Model{ID: "default", ResolvedID: "wire", EffortState: "supported", Efforts: []string{"low", "high"}}
	b := a
	b.ID = "new-alias"
	b.Efforts = []string{"high", "low"}
	for _, models := range [][]Model{{a, b}, {b, a}} {
		if m, err := ResolveModel(models, "wire"); err != nil || m.ResolvedID != "wire" {
			t.Fatal("compatible aliases refused", err)
		}
		b.Unavailable = true
		if _, err := ResolveModel([]Model{a, b}, "wire"); err == nil {
			t.Fatal("conflicting availability accepted")
		}
		b.Unavailable = false
		b.Efforts = []string{"low"}
		if _, err := ResolveModel([]Model{a, b}, "wire"); err == nil {
			t.Fatal("conflicting efforts accepted")
		}
		b.Efforts = a.Efforts
	}
	direct := Model{ID: "wire", ResolvedID: "exact-wire", EffortState: "supported", Efforts: []string{"low"}}
	for _, models := range [][]Model{{a, direct}, {direct, a}} {
		m, err := ResolveModel(models, "wire")
		if err != nil || m.ID != "wire" || m.ResolvedID != "exact-wire" {
			t.Fatal("exact ID did not take precedence", err)
		}
	}
	args := arguments(core.Route{Model: "claude/claude-fable-fixture[1m]", Effort: "low", Permission: "read-only"}, "session")
	found := false
	for i, v := range args {
		if v == "--model" && i+1 < len(args) {
			found = args[i+1] == "claude-fable-fixture[1m]"
		}
	}
	if !found {
		t.Fatal("advertised context modifier lost")
	}
}

func TestCatalogUIInvalidationAndDiagnosticRedaction(t *testing.T) {
	base := map[string]any{"type": "system", "subtype": "ui_invalidate", "event": "ui.render", "session_id": "session", "uuid": "00000000-0000-4000-8000-000000000001"}
	for _, change := range []func(map[string]any){func(v map[string]any) { v["session_id"] = "wrong" }, func(v map[string]any) { v["uuid"] = "bad" }, func(v map[string]any) { v["event"] = "different" }, func(v map[string]any) { v["prompts_screened"] = false }, func(v map[string]any) { v["instances"] = nil }, func(v map[string]any) {
		v["instances"] = []any{map[string]any{"surface": false, "component": "x", "instance_id": "y"}}
	}} {
		v := map[string]any{}
		for k, x := range base {
			v[k] = x
		}
		change(v)
		raw, _ := json.Marshal(v)
		f, _ := object(raw)
		if catalogUIInvalidation(f, "session") {
			t.Fatal("invalid metadata accepted")
		}
	}
	base["subtype"] = "PRIVATE_UNKNOWN"
	base["private_key"] = "PRIVATE_VALUE"
	raw, _ := json.Marshal(base)
	shape, _ := json.Marshal(catalogFrameShape(raw))
	if strings.Contains(string(shape), "PRIVATE") {
		t.Fatal("diagnostic observer leaked native strings")
	}
}

func TestOwnedExecutionPreservesAdvertisedModifierAndExactWirePin(t *testing.T) {
	for _, mode := range []string{"modifier", "wire"} {
		t.Run(mode, func(t *testing.T) {
			req := fixtureRequest(t, mode, "read-only")
			selected := Model{ID: "claude-sonnet-fixture[1m]", ResolvedID: "claude-sonnet-fixture", EffortState: "supported", Efforts: []string{"low"}}
			req.Attempt.Effective.Model = "claude/" + selected.ID
			if mode == "wire" {
				req.Attempt.Effective.Model = "claude/" + selected.ResolvedID
			}
			var output strings.Builder
			out, err := execute(context.Background(), req, func(text string) error { output.WriteString(text); return nil }, selected)
			if err != nil || out.State != "complete" || out.NativeModel != selected.ResolvedID || output.String() != "fixture reply" {
				t.Fatalf("native launch value changed: %+v %v", out, err)
			}
		})
	}
}
