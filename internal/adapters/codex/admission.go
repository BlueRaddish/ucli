package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

const reviewedSHA256 = "dd13bdb162a184fa04039488b234a8258ef9b76f84e926c505d5742b19249a6a"
const reviewedVersion = "codex-cli 0.162.1"

type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	DefaultEffort string   `json:"default_effort"`
	Efforts       []string `json:"efforts"`
	Default       bool     `json:"default"`
	nativeEfforts map[string]string
}

// Inspection contains no native account identifiers, email addresses or tokens.
// A catalog is discovery, not proof that inference is entitled for every model.
type Inspection struct {
	Identity       string  `json:"identity"`
	AuthMode       string  `json:"auth_mode"`
	PlanType       string  `json:"plan_type"`
	Models         []Model `json:"models"`
	QuotaAvailable bool    `json:"quota_available"`
}

func admissionError(message string) error {
	err := core.Problem("codex_not_admitted", message, 3)
	err.Origin, err.Acceptance, err.SideEffects = "codex", "not-sent", "none"
	return err
}

func validateBinding(b adapters.NativeBinding, executing bool) error {
	if b.Runtime != "codex" || b.SHA256 != reviewedSHA256 || (b.Version != "" && b.Version != reviewedVersion && b.Version != "0.162.1") || runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return admissionError("This Codex build/platform has not been reviewed for native execution.")
	}
	if (b.HomeMode != "existing" && b.HomeMode != "separate") || !filepath.IsAbs(b.Executable) || !filepath.IsAbs(b.Home) || strings.ContainsAny(b.Executable+b.Home, "\x00\r\n") || (b.AuthMode != "" && b.AuthMode != "chatgpt") || (executing && (b.AuthMode != "chatgpt" || !validID(b.ID) || len(b.Identity) != 64)) {
		return admissionError("Codex needs an explicit native executable, home and verified ChatGPT connection.")
	}
	if b.Identity != "" {
		if raw, err := hex.DecodeString(b.Identity); err != nil || len(raw) != 32 || strings.ToLower(b.Identity) != b.Identity {
			return admissionError("Codex connection identity is invalid.")
		}
	}
	home, err := os.Stat(b.Home)
	if err != nil || !home.IsDir() {
		return admissionError("The connected Codex home is unavailable.")
	}
	info, err := os.Lstat(b.Executable)
	if err != nil || !info.Mode().IsRegular() {
		return admissionError("The connected Codex executable is unavailable or linked.")
	}
	f, err := os.Open(b.Executable)
	if err != nil {
		return admissionError("The connected Codex executable could not be verified.")
	}
	h := sha256.New()
	_, hashErr := io.Copy(h, f)
	closeErr := f.Close()
	if hashErr != nil || closeErr != nil || hex.EncodeToString(h.Sum(nil)) != b.SHA256 {
		return admissionError("The Codex executable changed. Refresh and review the connection before running.")
	}
	return nil
}

func nativeArgs(scratch, permission, homeMode string) []string {
	// CLI overrides affect this owned invocation only. Native credentials stay in
	// the explicit CODEX_HOME and remain owned/refreshed by Codex. Disable startup
	// hooks, plugins and external tools even when local command/file tools are allowed.
	// A nonempty openai_base_url overrides Codex's auth-adaptive default.
	// This ChatGPT-only connection must pin its reviewed Responses backend;
	// api.openai.com/v1 would bypass managed ChatGPT routing for inference.
	settings := []string{
		`model_provider="openai"`, `chatgpt_base_url="https://chatgpt.com/backend-api/"`, `openai_base_url="https://chatgpt.com/backend-api/codex"`, `model_providers={}`, `mcp_servers={}`, `plugins={}`, `hooks={}`, `notify=[]`,
		`approval_policy="never"`, `approvals_reviewer="user"`, `web_search="disabled"`,
		`check_for_update_on_startup=false`, `history.persistence="none"`, `analytics.enabled=false`, `feedback.enabled=false`,
		`developer_instructions=""`, `project_doc_max_bytes=0`, `model_reasoning_summary="none"`, `service_tier="default"`,
		`features.apps=false`, `features.plugins=false`, `features.remote_plugin=false`, `features.hooks=false`,
		`features.shell_snapshot=false`, `features.multi_agent=false`, `features.multi_agent_v2=false`,
		`features.browser_use=false`, `features.browser_use_external=false`, `features.computer_use=false`,
		`features.image_generation=false`, `features.code_mode=false`, `features.code_mode_host=false`,
		`features.skill_search=false`, `features.skill_mcp_dependency_install=false`, `features.skip_host_skill_discovery=true`,
		`features.memories=false`, `features.goals=false`, `features.sleep_tool=false`, `features.view_image=false`,
		`features.workspace_dependencies=false`, `features.worktrees=false`, `features.tool_suggest=false`,
		`features.realtime_conversation=false`, `features.auth_elicitation=false`, `suppress_unstable_features_warning=true`,
	}
	if permission != "sandbox" {
		settings = append(settings, `features.shell_tool=false`, `features.unified_exec=false`)
	} else {
		settings = append(settings, `features.shell_tool=true`, `features.unified_exec=true`)
	}
	if homeMode == "separate" {
		settings = append(settings, `cli_auth_credentials_store="file"`)
	}
	for _, key := range []string{"sqlite_home", "log_dir"} {
		value, _ := json.Marshal(scratch)
		settings = append(settings, key+"="+string(value))
	}
	args := []string{"app-server", "--listen", "stdio://"}
	for _, setting := range settings {
		args = append(args, "-c", setting)
	}
	return args
}

func nativeEnv(home, scratch string) []string {
	// Never inherit API keys, provider endpoints, native-session flags or agent
	// orchestration variables. This path admits ChatGPT managed authentication only.
	env := []string{"CODEX_HOME=" + home, "NO_COLOR=1", "TERM=dumb", "RUST_LOG=off", "TMP=" + scratch, "TEMP=" + scratch}
	for _, key := range []string{"SystemRoot", "WINDIR", "PATH", "PATHEXT", "USERPROFILE", "APPDATA", "LOCALAPPDATA"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func startBound(ctx context.Context, b adapters.NativeBinding, permission string, extra ...string) (*process.Child, func() error, error) {
	directory, err := os.MkdirTemp("", "ucli-codex-")
	if err != nil {
		return nil, nil, admissionError("Codex operational directory could not be created.")
	}
	parent, parentErr := filepath.Abs(os.TempDir())
	cleanup := func() error {
		absolute, absErr := filepath.Abs(directory)
		if parentErr != nil || absErr != nil || !strings.EqualFold(filepath.Dir(absolute), parent) || !strings.HasPrefix(filepath.Base(absolute), "ucli-codex-") {
			return errors.New("Codex operational cleanup boundary is invalid.")
		}
		if err := os.RemoveAll(absolute); err != nil {
			return errors.New("Codex operational cleanup failed; the private directory was retained.")
		}
		return nil
	}
	child, err := process.Start(ctx, process.Spec{Executable: b.Executable, Args: append(nativeArgs(directory, permission, b.HomeMode), extra...), Env: nativeEnv(b.Home, directory), Dir: directory, FrameBytes: maxFrame, QueueBytes: 4 * maxFrame})
	if err != nil {
		return nil, nil, errors.Join(admissionError("The connected Codex process could not be started."), cleanup())
	}
	return child, cleanup, nil
}

// The reviewed native schema otherwise permits automatic browser authorization
// for gateway login. Discovery/execution must never initiate a new login flow.
func initializeParams() map[string]any {
	return map[string]any{"clientInfo": map[string]string{"name": "ucli", "title": "UCLI", "version": "0.1.0"}, "capabilities": map[string]bool{"experimentalApi": true, "explicitGatewayOauth": true}}
}

func initializeNative(ctx context.Context, r *rpc) (json.RawMessage, error) {
	hello, _, err := r.call(ctx, "initialize", initializeParams())
	if err != nil {
		return nil, err
	}
	_, err = r.send(ctx, map[string]any{"method": "initialized", "params": map[string]any{}})
	return hello, err
}

func matchingHome(hello json.RawMessage, b adapters.NativeBinding) bool {
	home, ok := textField(objectFields(hello), "codexHome", 32768)
	if !ok {
		return false
	}
	actual, err := filepath.EvalSymlinks(home)
	expected, expectedErr := filepath.EvalSymlinks(b.Home)
	return err == nil && expectedErr == nil && strings.EqualFold(filepath.Clean(actual), filepath.Clean(expected))
}

// Inspect may bootstrap an empty expected identity, but never starts a thread or
// inference turn. Nonempty expected identities must match the current native login.
func Inspect(ctx context.Context, binding adapters.NativeBinding) (inspection Inspection, err error) {
	if ctx == nil {
		return inspection, admissionError("Codex inspection requires a context.")
	}
	if err = validateBinding(binding, false); err != nil {
		return inspection, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	child, cleanup, err := startBound(ctx, binding, "read-only")
	if err != nil {
		return inspection, err
	}
	defer func() {
		if closeOwned(child) != nil {
			inspection, err = Inspection{}, cleanupProblem()
			return // Retain private diagnostics if the owned process might remain.
		}
		if cleanupErr := cleanup(); cleanupErr != nil {
			inspection, err = Inspection{}, cleanupErr
		}
	}()
	r := &rpc{child: child}
	hello, err := initializeNative(ctx, r)
	if err != nil || !matchingHome(hello, binding) {
		return inspection, errors.Join(admissionError("Codex did not confirm the connected native home."), contextCause(err))
	}
	return inspectRPC(ctx, r, binding)
}

func inspectRPC(ctx context.Context, r *rpc, b adapters.NativeBinding) (result Inspection, err error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	account, _, err := r.call(ctx, "account/read", map[string]bool{"refreshToken": true})
	if err != nil {
		return result, errors.Join(admissionError("Codex account status is unavailable: "+err.Error()), contextCause(err))
	}
	f := objectFields(account)
	var requiresAuth bool
	if isNull(f["requiresOpenaiAuth"]) || json.Unmarshal(f["requiresOpenaiAuth"], &requiresAuth) != nil {
		return result, admissionError("Codex account status is malformed.")
	}
	if absentNullable(f["account"]) {
		if b.Identity != "" {
			return Inspection{}, admissionError("The connected Codex account is no longer signed in.")
		}
		return Inspection{AuthMode: "none"}, nil
	}
	a := objectFields(f["account"])
	plan, planOK := textField(a, "planType", 128)
	nativeID, idOK := textField(objectFields(f["workspaceRouting"]), "chatgptAccountId", 512)
	if !equals(a, "type", "chatgpt") || !planOK || !knownPlan(plan) || !idOK || nativeID == "" {
		return result, admissionError("Codex did not confirm a ChatGPT plan and stable native account identity.")
	}
	hash := sha256.Sum256([]byte("codex\x00" + nativeID))
	result.Identity, result.AuthMode, result.PlanType = hex.EncodeToString(hash[:]), "chatgpt", plan
	if b.Identity != "" && b.Identity != result.Identity {
		return Inspection{}, admissionError("The native Codex account changed. Refresh and explicitly select the connection.")
	}
	quota, _, quotaErr := r.call(ctx, "account/rateLimits/read", map[string]any{})
	if quotaErr == nil {
		quotaFields := objectFields(quota)
		limits := objectFields(quotaFields["rateLimits"])
		// Native included-usage permission is authoritative. Percentages alone
		// cannot establish entitlement or justify credits/paid usage fallback.
		var included bool
		allowed := json.Unmarshal(quotaFields["ordinaryUsageAllowed"], &included) == nil && included
		checked, available := 0, allowed
		for _, name := range []string{"primary", "secondary"} {
			window := objectFields(limits[name])
			var used float64
			if absentNullable(limits[name]) {
				continue
			}
			if len(window) != 0 && !isNull(window["usedPercent"]) && json.Unmarshal(window["usedPercent"], &used) == nil && used >= 0 && used <= 100 {
				checked++
				available = available && used < 95
			} else {
				available = false
			}
		}
		result.QuotaAvailable = checked > 0 && available
	}
	cursor := ""
	seen, cursors := map[string]bool{}, map[string]bool{}
	for page := 0; page < 10; page++ {
		params := map[string]any{"limit": 100, "includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		response, _, callErr := r.call(ctx, "model/list", params)
		if callErr != nil {
			return Inspection{}, admissionError("Codex model discovery is unavailable.")
		}
		data := objectFields(response)
		var models []json.RawMessage
		if !arrayField(data, "data", &models) || len(models) > 100 {
			return Inspection{}, admissionError("Codex returned an invalid model catalog.")
		}
		for _, raw := range models {
			m := objectFields(raw)
			id, idOK := textField(m, "model", 256)
			name, nameOK := textField(m, "displayName", 256)
			effort, effortOK := textField(m, "defaultReasoningEffort", 64)
			var options []json.RawMessage
			var isDefault bool
			if !idOK || !validID(id) || !nameOK || !effortOK || !arrayField(m, "supportedReasoningEfforts", &options) || len(options) > 20 || json.Unmarshal(m["isDefault"], &isDefault) != nil || seen[id] {
				return Inspection{}, admissionError("Codex returned an invalid model catalog.")
			}
			model := Model{ID: id, Name: name, Default: isDefault}
			rawEfforts := []string{}
			for _, option := range options {
				value, ok := textField(objectFields(option), "reasoningEffort", 64)
				if !ok || !validID(value) {
					return Inspection{}, admissionError("Codex returned invalid reasoning choices.")
				}
				rawEfforts = append(rawEfforts, value)
			}
			if !normalizeEfforts(&model, rawEfforts, effort) {
				return Inspection{}, admissionError("Codex reasoning choices cannot be mapped to universal effort.")
			}
			seen[id] = true
			result.Models = append(result.Models, model)
		}
		if absentNullable(data["nextCursor"]) {
			return result, nil
		}
		var ok bool
		cursor, ok = textField(data, "nextCursor", 2048)
		if !ok || cursor == "" || cursors[cursor] {
			return Inspection{}, admissionError("Codex model pagination did not settle.")
		}
		cursors[cursor] = true
	}
	return Inspection{}, admissionError("Codex model catalog exceeds the supported bound.")
}

// Each public effort maps to an exact advertised native value; unsupported
// explicit intent is rejected. Max means the model's highest advertised effort.
func normalizeEfforts(model *Model, raw []string, defaultRaw string) bool {
	supported := map[string]bool{}
	for _, value := range raw {
		if supported[value] {
			return false
		}
		switch value {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		default:
			return false
		}
		supported[value] = true
	}
	if !supported[defaultRaw] {
		return false
	}
	model.nativeEfforts = map[string]string{}
	for _, v := range []string{"low", "minimal", "none"} {
		if supported[v] {
			model.nativeEfforts["low"] = v
			break
		}
	}
	for _, v := range []string{"medium", "high"} {
		if supported[v] {
			model.nativeEfforts[v] = v
		}
	}
	for _, v := range []string{"ultra", "max", "xhigh", "high", "medium", "low", "minimal", "none"} {
		if supported[v] {
			model.nativeEfforts["max"] = v
			break
		}
	}
	for _, v := range []string{"low", "medium", "high", "max"} {
		if model.nativeEfforts[v] != "" {
			model.Efforts = append(model.Efforts, v)
		}
	}
	switch defaultRaw {
	case "none", "minimal", "low":
		model.DefaultEffort = "low"
	case "xhigh", "max", "ultra":
		model.DefaultEffort = "max"
	default:
		model.DefaultEffort = defaultRaw
	}
	return len(model.Efforts) > 0
}

func nativeEffort(inspection Inspection, id, effort string) (string, bool) {
	for _, model := range inspection.Models {
		if model.ID == id {
			native, ok := model.nativeEfforts[effort]
			return native, ok
		}
	}
	return "", false
}

// The protocol, transport and callback boundaries replace native/private errors
// before returning them. Preserve their fixed local reason so diagnosis does not
// require another inference merely to distinguish an RPC rejection from a bad frame.
func safeExecutionError(err error) error { return err }

func contextCause(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return nil
}

func cleanupProblem() error {
	e := core.Problem("codex_cleanup_uncertain", "Codex owned-process cleanup is uncertain; do not automatically retry.", 9)
	e.Origin, e.Acceptance, e.SideEffects = "codex", "unknown", "unknown"
	return e
}
func closeOwned(child *process.Child) error {
	err := child.Close()
	if err == nil || (child.Settled() && contextOnly(err)) {
		return nil
	}
	return err
}
func contextOnly(err error) bool {
	if err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		items := many.Unwrap()
		if len(items) == 0 {
			return false
		}
		for _, item := range items {
			if !contextOnly(item) {
				return false
			}
		}
		return true
	}
	if one, ok := err.(interface{ Unwrap() error }); ok {
		return contextOnly(one.Unwrap())
	}
	return false
}

// Empty TOML maps merge with the connected home's maps. Discover server names
// without creating a thread, then restart with explicit per-server disables.
// Same-user concurrent configuration replacement is not an adversarial CAS boundary.
func nativeLaunchOverrides(ctx context.Context, b adapters.NativeBinding, cwd string) (settings []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	child, cleanup, err := startBound(ctx, b, "read-only")
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeOwned(child) != nil {
			settings, err = nil, cleanupProblem()
			return
		}
		err = errors.Join(err, cleanup())
	}()
	r := &rpc{child: child}
	hello, err := initializeNative(ctx, r)
	if err != nil || !matchingHome(hello, b) {
		if err != nil {
			return nil, errors.Join(admissionError("Codex launch configuration could not be inspected: "+err.Error()), contextCause(err))
		}
		return nil, admissionError("Codex launch configuration did not confirm the connected home.")
	}
	config, err := readNativeConfig(ctx, r, cwd)
	if err != nil {
		return nil, err
	}
	servers := objectFields(config["mcp_servers"])
	if len(servers) > 128 {
		return nil, admissionError("Codex configured server count exceeds the supported bound.")
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		if !validConfigName(name) {
			return nil, admissionError("Codex configured server name is invalid.")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		settings = append(settings, "-c", "mcp_servers."+name+".enabled=false")
	}
	return settings, nil
}
func readNativeConfig(ctx context.Context, r *rpc, cwd string) (map[string]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	value, _, err := r.call(ctx, "config/read", map[string]any{"cwd": cwd, "includeLayers": false})
	if err != nil {
		return nil, errors.Join(admissionError("Codex effective configuration could not be verified."), contextCause(err))
	}
	config := objectFields(objectFields(value)["config"])
	if config == nil {
		return nil, admissionError("Codex effective configuration is malformed.")
	}
	return config, nil
}
func safeNativeConfig(config map[string]json.RawMessage, permission, homeMode string) bool {
	if !equals(config, "model_provider", "openai") || !equals(config, "chatgpt_base_url", "https://chatgpt.com/backend-api/") || !equals(config, "openai_base_url", "https://chatgpt.com/backend-api/codex") {
		return false
	}
	if providers := objectFields(config["model_providers"]); providers == nil || len(providers) != 0 {
		return false
	}
	servers := objectFields(config["mcp_servers"])
	if servers == nil || len(servers) > 128 {
		return false
	}
	for _, raw := range servers {
		if string(objectFields(raw)["enabled"]) != "false" {
			return false
		}
	}
	features := objectFields(config["features"])
	for _, name := range []string{"apps", "plugins", "remote_plugin", "hooks", "shell_snapshot", "multi_agent", "multi_agent_v2", "browser_use", "browser_use_external", "computer_use", "image_generation", "code_mode", "code_mode_host", "skill_search", "skill_mcp_dependency_install", "memories", "goals", "sleep_tool", "view_image", "workspace_dependencies", "worktrees", "tool_suggest", "realtime_conversation", "auth_elicitation"} {
		if string(features[name]) != "false" {
			return false
		}
	}
	if string(features["skip_host_skill_discovery"]) != "true" {
		return false
	}
	want := "false"
	if permission == "sandbox" {
		want = "true"
	}
	if string(features["shell_tool"]) != want || string(features["unified_exec"]) != want {
		return false
	}
	return homeMode != "separate" || equals(config, "cli_auth_credentials_store", "file")
}

func validConfigName(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func knownPlan(value string) bool {
	switch value {
	case "free", "go", "plus", "pro", "prolite", "promax", "team", "self_serve_business_prolite", "self_serve_business_usage_based", "business", "ent26", "enterprise_cbp_automation", "enterprise_cbp_usage_based", "enterprise", "edu", "edu_plus", "edu_pro":
		return true
	}
	return false
}

// Only schema-declared optional nullable account, cursor and quota-window fields
// use this predicate. Required fields retain their explicit type/presence guards.
func absentNullable(raw json.RawMessage) bool { return len(raw) == 0 || isNull(raw) }
