package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestCodexNativeProbe(t *testing.T) {
	destination := os.Getenv("UCLI_CODEX_PROBE_RECEIPT")
	if destination == "" {
		t.Skip("explicit native probe only")
	}
	b := adapters.NativeBinding{ID: "codex-probe", Runtime: "codex", Executable: os.Getenv("UCLI_CODEX_PROBE_EXE"), Home: os.Getenv("UCLI_CODEX_PROBE_HOME"), SHA256: reviewedSHA256, Version: reviewedVersion, AuthMode: "chatgpt", HomeMode: "existing"}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	receipt := map[string]any{"version": 1, "native_sha256": reviewedSHA256, "native_version": reviewedVersion, "inference_attempted": false, "passed": false}
	defer func() {
		data, _ := json.MarshalIndent(receipt, "", "  ")
		if err := os.WriteFile(destination, append(data, '\n'), 0600); err != nil {
			t.Error("sanitized receipt could not be saved")
		}
	}()
	inspectionStart := time.Now()
	inspection, err := Inspect(ctx, b)
	receipt["inspection_ms"] = time.Since(inspectionStart).Milliseconds()
	if err != nil {
		receipt["failure"] = err.Error()
		t.Fatal(err)
	}
	receipt["inspection"] = inspection
	if inspection.AuthMode != "chatgpt" {
		t.Fatal("explicit probe requires signed-in ChatGPT connection")
	}
	b.Identity = inspection.Identity
	if os.Getenv("UCLI_CODEX_PROBE_INFER") != "1" {
		workspaceCheck := t.TempDir()
		launchStart := time.Now()
		overrides, err := nativeLaunchOverrides(ctx, b, workspaceCheck)
		receipt["launch_preflight_ms"] = time.Since(launchStart).Milliseconds()
		if err != nil {
			receipt["failure"] = err.Error()
			t.Fatal(err)
		}
		child, cleanup, err := startBound(context.Background(), b, "read-only", overrides...)
		if err != nil {
			t.Fatal(err)
		}
		r := &rpc{child: child}
		_, err = initializeNative(ctx, r)
		if err == nil {
			var config map[string]json.RawMessage
			config, err = readNativeConfig(ctx, r, workspaceCheck)
			if err == nil && !safeNativeConfig(config, "read-only", b.HomeMode) {
				err = admissionError("Native launch configuration did not preserve the selected envelope.")
			}
		}
		closeErr := closeOwned(child)
		if closeErr == nil {
			closeErr = cleanup()
		}
		if err != nil || closeErr != nil {
			receipt["failure"] = "native launch configuration/cleanup failed"
			t.Fatal(err, closeErr)
		}
		receipt["effective_tool_auth_envelope_verified"] = true
		receipt["passed"] = true
		return

	}
	modelID := os.Getenv("UCLI_CODEX_PROBE_MODEL")
	var selected Model
	for _, model := range inspection.Models {
		if model.ID == modelID {
			selected = model
		}
	}
	if selected.ID == "" {
		t.Fatal("select an exact previously inspected native model")
	}
	effort := ""
	for _, candidate := range []string{"none", "minimal", "low"} {
		for _, supported := range selected.Efforts {
			if candidate == supported && effort == "" {
				effort = candidate
			}
		}
	}
	if effort == "" {
		t.Fatal("selected model has no admitted low-cost effort")
	}
	b.Identity = inspection.Identity
	workspace := t.TempDir()
	req := adapters.Request{Binding: &b, StateHome: t.TempDir(), Attempt: core.Attempt{Input: "Reply with exactly UCLI_LIVE_OK. Do not use tools, browse, read or write files, or delegate.", Workspace: core.Workspace{ID: "probe", Root: workspace, Dir: workspace}, Effective: core.Route{Runtime: "codex", Provider: "openai", Model: "codex/" + selected.ID, Account: b.ID, Effort: effort, Permission: "read-only", Mode: "native"}}}
	contextSaved := false
	req.OnContext = func(c core.NativeContext) error {
		contextSaved = c.Runtime == "codex" && c.Account == b.ID && c.Durability == "ephemeral" && c.SessionID != ""
		return nil
	}
	var output strings.Builder
	receipt["inference_attempted"] = true
	receipt["model"], receipt["effort"] = selected.ID, effort
	driverStart := time.Now()
	out, err := (Driver{}).Execute(ctx, req, func(text string) error { output.WriteString(text); return nil })
	receipt["driver_ms"] = time.Since(driverStart).Milliseconds()
	receipt["driver_native_process_count"] = 2
	receipt["state"], receipt["context_saved"], receipt["exact_marker"] = out.State, contextSaved, strings.TrimSpace(output.String()) == "UCLI_LIVE_OK"
	if err != nil {
		receipt["failure"] = err.Error()
	}
	if err != nil || out.State != "complete" || !contextSaved || strings.TrimSpace(output.String()) != "UCLI_LIVE_OK" {
		t.Fatal("live probe did not establish exact completed output", out.State, err)
	}
	receipt["passed"] = true
}

func TestNativeLaunchDoesNotInheritKeysAndSeparatesPermissions(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "SECRET")
	t.Setenv("CODEX_API_KEY", "SECRET")
	t.Setenv("CODEX_HOME", "wrong-home")
	env := nativeEnv("explicit-home", "operational-directory")
	if strings.Contains(strings.Join(env, "\n"), "SECRET") || strings.Contains(strings.Join(env, "\n"), "wrong-home") {
		t.Fatal("ambient credentials/home inherited")
	}
	read := strings.Join(nativeArgs(t.TempDir(), "read-only", "existing"), " ")
	full := strings.Join(nativeArgs(t.TempDir(), "sandbox", "existing"), " ")
	for _, guard := range []string{"mcp_servers={}", "features.hooks=false", "features.plugins=false", "features.apps=false"} {
		if !strings.Contains(read, guard) || !strings.Contains(full, guard) {
			t.Fatal("missing startup guard", guard)
		}
	}
	if !strings.Contains(read, "features.shell_tool=false") || strings.Contains(full, "features.shell_tool=false") {
		t.Fatal("permission-specific tool envelope lost")
	}
}

func TestNativeBindingNegativesRefuseBeforeSpawn(t *testing.T) {
	req := fixtureRequest(t)
	for _, b := range []adapters.NativeBinding{
		{},
		{Runtime: "codex", SHA256: "foreign", Executable: filepath.Join(t.TempDir(), "missing"), Home: t.TempDir()},
		{Runtime: "codex", SHA256: reviewedSHA256, Executable: "relative", Home: t.TempDir()},
		{Runtime: "codex", SHA256: reviewedSHA256, Executable: filepath.Join(t.TempDir(), "missing"), Home: t.TempDir(), AuthMode: "apiKey"},
	} {
		req.Binding = &b
		out, err := (Driver{}).Execute(context.Background(), req, func(string) error { t.Fatal("unexpected output"); return nil })
		if err == nil || out.State != "failed" {
			t.Fatal("invalid binding admitted")
		}
	}
}

func inspectionHost(mode, dir string) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), maxFrame)
	write := func(value any) { raw, _ := json.Marshal(value); os.Stdout.Write(append(raw, '\n')) }
	for scanner.Scan() {
		e, err := decode(scanner.Bytes())
		if err != nil {
			return
		}
		var result any
		switch e.Method {
		case "initialize":
			if string(objectFields(objectFields(e.Params)["capabilities"])["explicitGatewayOauth"]) != "true" {
				return
			}
			result = map[string]any{"codexHome": dir, "platformFamily": "fixture", "platformOs": "fixture", "userAgent": "fixture"}
		case "initialized":
			continue
		case "account/read":
			a := map[string]any{"type": "chatgpt", "email": "SECRET", "planType": "pro"}
			routing := map[string]any{"chatgptAccountId": "synthetic-native-id"}
			if mode == "inspect-api" {
				a["type"] = "apiKey"
			}
			if mode == "inspect-missing-id" {
				delete(routing, "chatgptAccountId")
			}
			account := map[string]any{"account": a, "workspaceRouting": routing, "requiresOpenaiAuth": true}
			switch mode {
			case "inspect-signed-out":
				account["account"] = nil
				account["workspaceRouting"] = nil
			case "inspect-signed-out-omitted":
				delete(account, "account")
				delete(account, "workspaceRouting")
			case "inspect-auth-missing":
				delete(account, "requiresOpenaiAuth")
			case "inspect-auth-null":
				account["requiresOpenaiAuth"] = nil
			case "inspect-auth-number":
				account["requiresOpenaiAuth"] = 0
			case "inspect-auth-string":
				account["requiresOpenaiAuth"] = "true"
			case "inspect-account-malformed":
				account["account"] = false
			}
			result = account
		case "account/rateLimits/read":
			used := any(10)
			if mode == "inspect-exhausted" {
				used = 99
			}
			if mode == "inspect-bad-window" {
				used = nil
			}
			limits := map[string]any{"primary": map[string]any{"usedPercent": used}, "secondary": nil}
			quota := map[string]any{"rateLimits": limits, "ordinaryUsageAllowed": true}
			switch mode {
			case "inspect-window-omitted":
				delete(limits, "secondary")
			case "inspect-windows-omitted":
				delete(limits, "primary")
				delete(limits, "secondary")
			case "inspect-sibling-malformed":
				limits["secondary"] = false
			case "inspect-sibling-empty":
				limits["secondary"] = map[string]any{}
			case "inspect-sibling-exhausted":
				limits["secondary"] = map[string]any{"usedPercent": 99}
			case "inspect-usage-false":
				quota["ordinaryUsageAllowed"] = false
			case "inspect-usage-null":
				quota["ordinaryUsageAllowed"] = nil
			case "inspect-usage-missing":
				delete(quota, "ordinaryUsageAllowed")
			case "inspect-usage-malformed":
				quota["ordinaryUsageAllowed"] = "true"
			}
			result = quota
		case "config/read":
			config := nativeConfigFixture("sandbox")
			if mode == "inspect-wrong-api-endpoint" {
				config["openai_base_url"] = "https://api.openai.com/v1"
			}
			result = map[string]any{"config": config}
		case "thread/start":
			os.WriteFile(filepath.Join(dir, "thread-created"), []byte("synthetic"), 0600)
			params := objectFields(e.Params)
			if !equals(params, "sandbox", "danger-full-access") || !equals(params, "model", "fixture-model") || !equals(params, "approvalPolicy", "never") {
				return
			}
			result = map[string]any{"thread": map[string]any{"id": "native-thread", "turns": []any{}}, "model": "fixture-model", "modelProvider": "openai", "cwd": dir, "approvalPolicy": "never", "approvalsReviewer": "user", "sandbox": map[string]string{"type": "dangerFullAccess"}}
		case "turn/start":
			params := objectFields(e.Params)
			if !equals(params, "effort", "ultra") || !equals(objectFields(params["sandboxPolicy"]), "type", "dangerFullAccess") {
				return
			}
			os.WriteFile(filepath.Join(dir, "dispatched"), []byte("synthetic"), 0600)
			write(map[string]any{"id": e.ID, "result": map[string]any{"turn": map[string]any{"id": "native-turn", "status": "inProgress", "items": []any{}}}})
			notify := func(method string, item map[string]any) {
				write(map[string]any{"method": method, "emittedAtMs": 1, "params": map[string]any{"threadId": "native-thread", "turnId": "native-turn", "item": item}})
			}
			for _, kind := range []string{"userMessage", "reasoning", "commandExecution", "fileChange"} {
				notify("item/started", map[string]any{"id": kind, "type": kind, "status": "inProgress"})
				notify("item/completed", map[string]any{"id": kind, "type": kind, "status": "completed"})
			}
			notify("item/started", map[string]any{"id": "message", "type": "agentMessage", "text": ""})
			notify("item/completed", map[string]any{"id": "message", "type": "agentMessage", "text": "synthetic complete"})
			write(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "native-thread", "turn": map[string]any{"id": "native-turn", "status": "completed", "items": []any{}}}})
			continue
		case "model/list":
			m := map[string]any{"model": "fixture-model", "displayName": "Fixture", "defaultReasoningEffort": "low", "isDefault": true, "supportedReasoningEfforts": []any{map[string]string{"reasoningEffort": "low"}, map[string]string{"reasoningEffort": "high"}, map[string]string{"reasoningEffort": "ultra"}}}
			data := []any{m}
			var next any
			if mode == "inspect-duplicate" {
				data = append(data, m)
			}
			if mode == "inspect-loop" {
				next = "repeat"
				data = []any{}
			}
			catalog := map[string]any{"data": data, "nextCursor": next}
			switch mode {
			case "inspect-cursor-omitted":
				delete(catalog, "nextCursor")
			case "inspect-cursor-malformed":
				catalog["nextCursor"] = false
			case "inspect-cursor-empty":
				catalog["nextCursor"] = ""
			}
			result = catalog
		default:
			return
		}
		write(map[string]any{"id": e.ID, "result": result})
	}
}

func TestInspectionBindsIdentityCatalogAndCapacity(t *testing.T) {
	for _, mode := range []string{"inspect-good", "inspect-api", "inspect-missing-id", "inspect-identity-changed", "inspect-exhausted", "inspect-bad-window", "inspect-duplicate", "inspect-loop"} {
		t.Run(mode, func(t *testing.T) {
			req := fixtureRequest(t)
			child := fixtureChild(t, req, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			b := adapters.NativeBinding{Home: req.Attempt.Workspace.Dir}
			hash := sha256.Sum256([]byte("codex\x00synthetic-native-id"))
			want := hex.EncodeToString(hash[:])
			b.Identity = want
			if mode == "inspect-identity-changed" {
				b.Identity = strings.Repeat("0", 64)
			}
			result, err := inspectRPC(ctx, &rpc{child: child}, b)
			good := mode == "inspect-good" || mode == "inspect-exhausted" || mode == "inspect-bad-window"
			if (err == nil) != good {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if good {
				if result.Identity != want || result.AuthMode != "chatgpt" || len(result.Models) != 1 {
					t.Fatal("identity/catalog not bound")
				}
				native, ok := nativeEffort(result, "fixture-model", "max")
				if !ok || native != "ultra" {
					t.Fatal("max mapping")
				}
				if result.QuotaAvailable != (mode == "inspect-good") {
					t.Fatal("capacity guard")
				}
			}
			if strings.Contains(string(mustJSON(result)), "SECRET") {
				t.Fatal("private account value escaped")
			}
		})
	}
}
func mustJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }
func TestUniversalEffortMapping(t *testing.T) {
	for _, raw := range [][]string{{"none"}, {"minimal"}, {"low", "medium", "high", "xhigh", "max", "ultra"}} {
		m := Model{}
		if !normalizeEfforts(&m, raw, raw[0]) {
			t.Fatal(raw)
		}
		if m.DefaultEffort != "low" || m.nativeEfforts["max"] != raw[len(raw)-1] {
			t.Fatal(m)
		}
	}
	for _, raw := range [][]string{{"unknown"}, {"low", "low"}, {}} {
		if normalizeEfforts(&Model{}, raw, "low") {
			t.Fatal("unsupported effort admitted", raw)
		}
	}
}
func TestNativeTimestampEnvelope(t *testing.T) {
	for _, stamp := range []string{"null", "-1", "1.1", `"1"`} {
		if _, err := decode([]byte(`{"method":"metadata","params":{},"emittedAtMs":` + stamp + `}`)); err == nil {
			t.Fatal("invalid timestamp")
		}
	}
	if _, err := decode([]byte(`{"method":"metadata","params":{},"emittedAtMs":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := decode([]byte(`{"id":1,"result":{},"emittedAtMs":1}`)); err == nil {
		t.Fatal("timestamp response dialect admitted")
	}
}

func TestExplicitSignedOutIsNotUnknownAuth(t *testing.T) {
	req := fixtureRequest(t)
	child := fixtureChild(t, req, "inspect-signed-out")
	got, err := inspectRPC(context.Background(), &rpc{child: child}, adapters.NativeBinding{})
	if err != nil || got.AuthMode != "none" || got.Identity != "" {
		t.Fatal(got, err)
	}
}
func TestSeparateHomeForcesFileCredentialStore(t *testing.T) {
	args := strings.Join(nativeArgs(t.TempDir(), "read-only", "separate"), " ")
	if !strings.Contains(args, `cli_auth_credentials_store="file"`) {
		t.Fatal("separate home could fall back to global keyring")
	}
}
func TestContextOnlyCleanupClassification(t *testing.T) {
	if !contextOnly(context.Canceled) || !contextOnly(context.DeadlineExceeded) || contextOnly(nil) || contextOnly(admissionError("x")) {
		t.Fatal("cleanup classification")
	}
}

func nativeConfigFixture(permission string) map[string]any {
	config := map[string]any{"model_provider": "openai", "chatgpt_base_url": "https://chatgpt.com/backend-api/", "openai_base_url": "https://chatgpt.com/backend-api/codex", "model_providers": map[string]any{}, "mcp_servers": map[string]any{"fixture": map[string]any{"enabled": false}}}
	features := map[string]any{}
	for _, arg := range nativeArgs("scratch", permission, "existing") {
		if strings.HasPrefix(arg, "features.") {
			parts := strings.SplitN(strings.TrimPrefix(arg, "features."), "=", 2)
			features[parts[0]] = parts[1] == "true"
		}
	}
	config["features"] = features
	return config
}
func TestNativeConfigurationAndServerKeyGuards(t *testing.T) {
	rawConfig := func(v map[string]any) map[string]json.RawMessage { return objectFields(mustJSON(v)) }
	for _, permission := range []string{"read-only", "sandbox"} {
		c := nativeConfigFixture(permission)
		if !safeNativeConfig(rawConfig(c), permission, "existing") {
			t.Fatal("valid envelope refused")
		}
		c["mcp_servers"] = map[string]any{"server": map[string]bool{"enabled": true}}
		if safeNativeConfig(rawConfig(c), permission, "existing") {
			t.Fatal("enabled MCP accepted")
		}
	}
	for _, name := range []string{"", "dot.name", `quote"name`, "x\ny", "x y"} {
		if validConfigName(name) {
			t.Fatal("unsafe native dotted key", name)
		}
	}
	if !validConfigName("local-server_1") {
		t.Fatal("valid key refused")
	}
	c := nativeConfigFixture("read-only")
	c["model_providers"] = map[string]any{"openai": map[string]string{"base_url": "https://foreign.invalid"}}
	if safeNativeConfig(rawConfig(c), "read-only", "existing") {
		t.Fatal("provider override accepted")
	}
	c = nativeConfigFixture("read-only")
	if safeNativeConfig(rawConfig(c), "read-only", "separate") {
		t.Fatal("global credential fallback accepted")
	}
	c["cli_auth_credentials_store"] = "file"
	if !safeNativeConfig(rawConfig(c), "read-only", "separate") {
		t.Fatal("file credential store refused")
	}
}
func TestOwnedNativeProtocolSandboxRoute(t *testing.T) {
	req := fixtureRequest(t)
	hash := sha256.Sum256([]byte("codex\x00synthetic-native-id"))
	req.Binding = &adapters.NativeBinding{ID: "connection", Runtime: "codex", Home: req.Attempt.Workspace.Dir, HomeMode: "existing", Identity: hex.EncodeToString(hash[:]), AuthMode: "chatgpt"}
	req.Attempt.Effective = core.Route{Runtime: "codex", Provider: "openai", Model: "codex/fixture-model", Account: "connection", Effort: "max", Permission: "sandbox", Mode: "native"}
	receipt := false
	req.OnContext = func(value core.NativeContext) error {
		receipt = value.Provenance == "confirmed" && value.Account == "connection"
		return nil
	}
	child := fixtureChild(t, req, "inspect-native")
	var output strings.Builder
	out, err := executeProtocol(context.Background(), req, child, func(s string) error { output.WriteString(s); return nil })
	if err != nil || out.State != "complete" || out.NativeModel != "fixture-model" || !receipt || output.String() != "synthetic complete" {
		t.Fatalf("out=%+v err=%v receipt=%t output=%q", out, err, receipt, output.String())
	}
}

func TestSanitizedProtocolReasonAndContextRemainVisible(t *testing.T) {
	original := errors.Join(errors.New("Codex delta has no matching active message"), context.DeadlineExceeded)
	got := safeExecutionError(original)
	if !errors.Is(got, context.DeadlineExceeded) || !strings.Contains(got.Error(), "no matching active message") {
		t.Fatal("lost local diagnostic/cause")
	}
	if knownPlan("") || knownPlan("unknown") || knownPlan("future-plan") || !knownPlan("prolite") {
		t.Fatal("unknown plan admitted")
	}
}

func TestInspectionInitializeRequiresExplicitGatewayAuthorization(t *testing.T) {
	req := fixtureRequest(t)
	child := fixtureChild(t, req, "inspect-good")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := &rpc{child: child}
	hello, err := initializeNative(ctx, r)
	if err != nil || !matchingHome(hello, adapters.NativeBinding{Home: req.Attempt.Workspace.Dir}) {
		t.Fatal("native handshake did not enforce explicit gateway authorization", err)
	}
	got, err := inspectRPC(ctx, r, adapters.NativeBinding{})
	if err != nil || got.AuthMode != "chatgpt" {
		t.Fatal(got, err)
	}
}

func TestInspectionOptionalFieldsAndIncludedUsage(t *testing.T) {
	for _, tc := range []struct {
		mode           string
		good, capacity bool
	}{
		{"inspect-window-omitted", true, true}, {"inspect-cursor-omitted", true, true},
		{"inspect-windows-omitted", true, false}, {"inspect-sibling-malformed", true, false}, {"inspect-sibling-empty", true, false}, {"inspect-sibling-exhausted", true, false},
		{"inspect-usage-false", true, false}, {"inspect-usage-null", true, false}, {"inspect-usage-missing", true, false}, {"inspect-usage-malformed", true, false},
		{"inspect-auth-missing", false, false}, {"inspect-auth-null", false, false}, {"inspect-auth-number", false, false}, {"inspect-auth-string", false, false}, {"inspect-account-malformed", false, false},
		{"inspect-cursor-malformed", false, false}, {"inspect-cursor-empty", false, false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			req := fixtureRequest(t)
			child := fixtureChild(t, req, tc.mode)
			got, err := inspectRPC(context.Background(), &rpc{child: child}, adapters.NativeBinding{})
			if (err == nil) != tc.good {
				t.Fatalf("wrong admission: %+v %v", got, err)
			}
			if tc.good && (got.AuthMode != "chatgpt" || len(got.Models) != 1 || got.QuotaAvailable != tc.capacity) {
				t.Fatalf("catalog/auth/capacity mismatch: %+v", got)
			}
			if _, err := os.Stat(filepath.Join(req.Attempt.Workspace.Dir, "dispatched")); !os.IsNotExist(err) {
				t.Fatal("inspection dispatched")
			}
		})
	}
}
func TestOmittedSignedOutAccountCannotUnpinExecution(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		req := fixtureRequest(t)
		child := fixtureChild(t, req, "inspect-signed-out-omitted")
		b := adapters.NativeBinding{}
		if pinned {
			b.Identity = strings.Repeat("a", 64)
		}
		got, err := inspectRPC(context.Background(), &rpc{child: child}, b)
		if pinned {
			if err == nil {
				t.Fatal("signed-out pinned execution admitted")
			}
		} else if err != nil || got.AuthMode != "none" || got.Identity != "" || len(got.Models) != 0 {
			t.Fatal("omitted signed-out account not recognized", got, err)
		}
		if _, err := os.Stat(filepath.Join(req.Attempt.Workspace.Dir, "dispatched")); !os.IsNotExist(err) {
			t.Fatal("signed-out inspection dispatched")
		}
	}
}
func TestNativeCapacityBlockIsNotAnAuthenticationFailure(t *testing.T) {
	for _, mode := range []string{"inspect-usage-false", "inspect-usage-null", "inspect-usage-missing", "inspect-windows-omitted", "inspect-sibling-malformed", "inspect-exhausted"} {
		t.Run(mode, func(t *testing.T) {
			req := fixtureRequest(t)
			hash := sha256.Sum256([]byte("codex\x00synthetic-native-id"))
			req.Binding = &adapters.NativeBinding{ID: "connection", Runtime: "codex", Home: req.Attempt.Workspace.Dir, HomeMode: "existing", Identity: hex.EncodeToString(hash[:]), AuthMode: "chatgpt"}
			req.Attempt.Effective = core.Route{Runtime: "codex", Provider: "openai", Model: "codex/fixture-model", Account: "connection", Effort: "max", Permission: "sandbox", Mode: "native"}
			req.OnContext = func(core.NativeContext) error { t.Fatal("capacity-blocked execution created a context"); return nil }
			child := fixtureChild(t, req, mode)
			out, err := executeProtocol(context.Background(), req, child, func(string) error { t.Fatal("capacity-blocked execution emitted output"); return nil })
			var problem *core.Error
			if out.State != "failed" || !errors.As(err, &problem) || problem.Code != "codex_capacity_blocked" || problem.Exit != 4 || problem.Acceptance != "not-sent" {
				t.Fatalf("wrong capacity classification: %+v %v", out, err)
			}
			for _, marker := range []string{"thread-created", "dispatched"} {
				if _, err := os.Stat(filepath.Join(req.Attempt.Workspace.Dir, marker)); !os.IsNotExist(err) {
					t.Fatal("capacity-blocked execution crossed boundary", marker)
				}
			}
		})
	}
}

func TestManagedChatGPTInferenceEndpointIsPinned(t *testing.T) {
	for _, permission := range []string{"read-only", "sandbox"} {
		args := strings.Join(nativeArgs("scratch", permission, "existing"), " ")
		if !strings.Contains(args, `openai_base_url="https://chatgpt.com/backend-api/codex"`) || strings.Contains(args, "api.openai.com/v1") {
			t.Error("managed ChatGPT inference endpoint not pinned")
		}
		c := nativeConfigFixture(permission)
		c["openai_base_url"] = "https://chatgpt.com/backend-api/codex"
		if !safeNativeConfig(objectFields(mustJSON(c)), permission, "existing") {
			t.Error("reviewed managed endpoint refused")
		}
		for _, url := range []string{"https://api.openai.com/v1", "https://foreign.invalid", "https://chatgpt.com/backend-api/", ""} {
			c["openai_base_url"] = url
			if safeNativeConfig(objectFields(mustJSON(c)), permission, "existing") {
				t.Error("wrong inference endpoint accepted")
			}
		}
	}
}
func TestNativeWrongAPIEndpointRefusesBeforeThread(t *testing.T) {
	req := fixtureRequest(t)
	hash := sha256.Sum256([]byte("codex\x00synthetic-native-id"))
	req.Binding = &adapters.NativeBinding{ID: "connection", Runtime: "codex", Home: req.Attempt.Workspace.Dir, HomeMode: "existing", Identity: hex.EncodeToString(hash[:]), AuthMode: "chatgpt"}
	req.Attempt.Effective = core.Route{Runtime: "codex", Provider: "openai", Model: "codex/fixture-model", Account: "connection", Effort: "max", Permission: "sandbox", Mode: "native"}
	req.OnContext = func(core.NativeContext) error { t.Fatal("wrong endpoint created context"); return nil }
	child := fixtureChild(t, req, "inspect-wrong-api-endpoint")
	out, err := executeProtocol(context.Background(), req, child, func(string) error { t.Fatal("wrong endpoint emitted output"); return nil })
	var problem *core.Error
	if out.State != "failed" || !errors.As(err, &problem) || problem.Code != "codex_not_admitted" || problem.Acceptance != "not-sent" {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(req.Attempt.Workspace.Dir, "thread-created")); !os.IsNotExist(err) {
		t.Fatal("wrong endpoint crossed thread boundary")
	}
}
