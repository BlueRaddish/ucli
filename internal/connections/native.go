package connections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ucli.local/ucli/internal/adapters/claude"
	"ucli.local/ucli/internal/adapters/codex"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

type commandResult struct {
	stdout, stderr []byte
	exit           int
}

func nativeEnvironment(c Connection) []string {
	env := []string{}
	// No API keys, OAuth tokens, provider endpoints, project variables or log
	// controls are inherited. Native stores remain owned by the selected CLI.
	for _, key := range []string{"SystemRoot", "WINDIR", "PATH", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TMP", "TEMP", "SSL_CERT_FILE", "SSL_CERT_DIR", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	env = append(env, "NO_COLOR=1", "TERM=dumb", "AGY_CLI_DISABLE_AUTO_UPDATE=true", "DISABLE_AUTOUPDATER=1", "GH_PROMPT_DISABLED=1")
	switch c.Provider {
	case "codex":
		env = append(env, "CODEX_HOME="+c.NativeHome)
	case "claude":
		home, _ := os.UserHomeDir()
		if !samePath(c.NativeHome, join(home, ".claude")) {
			env = append(env, "CLAUDE_CONFIG_DIR="+c.NativeHome)
		}
	case "github":
		env = append(env, "GH_CONFIG_DIR="+c.NativeHome)
	}
	return env
}

// run retains raw output only in bounded process memory. It never includes
// provider output, native errors, identities or URL query strings in errors.
func run(ctx context.Context, c Connection, args []string, deadline time.Duration, line func(string) error) (result commandResult, err error) {
	if err = verifyExecutable(ctx, c); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	child, err := process.Start(ctx, process.Spec{Executable: c.Executable, Args: args, Env: nativeEnvironment(c), Dir: filepath.Dir(c.Executable), FrameBytes: 64 << 10, QueueBytes: 1 << 20})
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, problem("connection_process", "Selected native authentication command could not start under owned-process control.")
	}
	defer func() {
		if closeErr := child.Close(); closeErr != nil && !(child.Settled() && onlyContext(closeErr)) {
			err = errors.Join(problem("connection_cleanup", "Native authentication process cleanup was not confirmed. Do not start another login until the owned process is inspected."), err)
			result = commandResult{}
		}
	}()
	if err = child.CloseInput(); err != nil {
		return commandResult{}, problem("connection_process", "Native authentication input could not be closed safely.")
	}
	total := 0
	pending := map[string][]byte{}
	for {
		select {
		case <-ctx.Done():
			return commandResult{}, ctx.Err()
		case chunk, ok := <-child.Events():
			if !ok {
				exit, waitErr := child.Wait(ctx)
				if waitErr != nil {
					if ctx.Err() != nil {
						return commandResult{}, ctx.Err()
					}
					return commandResult{}, problem("connection_process", "Native authentication transport failed; private diagnostics were discarded.")
				}
				result.exit = exit.Code
				if line != nil {
					for _, p := range pending {
						if len(p) > 0 {
							if err = line(string(p)); err != nil {
								return commandResult{}, err
							}
						}
					}
				}
				return result, nil
			}
			total += len(chunk.Data)
			if total > 1<<20 {
				return commandResult{}, problem("connection_process", "Native authentication exceeded its bounded output; private diagnostics were discarded.")
			}
			if line == nil {
				if chunk.Stream == "stdout" {
					result.stdout = append(result.stdout, chunk.Data...)
				} else {
					result.stderr = append(result.stderr, chunk.Data...)
				}
			} else {
				p := append(pending[chunk.Stream], chunk.Data...)
				for {
					at := bytes.IndexByte(p, '\n')
					if at < 0 {
						break
					}
					if err = line(string(p[:at])); err != nil {
						return commandResult{}, err
					}
					p = p[at+1:]
				}
				if len(p) > 64<<10 {
					return commandResult{}, problem("connection_process", "Native login line exceeded its bound.")
				}
				pending[chunk.Stream] = p
			}
		}
	}
}

var versionPattern = regexp.MustCompile(`(?:^|\s)([0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?)(?:\s|$)`)

func (s *Service) inspectNative(ctx context.Context, c Connection) (Connection, error) {
	version, err := run(ctx, c, []string{"--version"}, 10*time.Second, nil)
	if err != nil {
		return c, err
	}
	match := versionPattern.FindSubmatch(bytes.TrimSpace(append(version.stdout, version.stderr...)))
	if version.exit != 0 || len(match) != 2 {
		return c, problem("connection_version", "Native executable did not report a recognized version.")
	}
	c.Version = string(match[1])
	c.Models = nil
	switch c.Provider {
	case "github":
		return inspectGitHub(ctx, c)
	case "agy":
		c.AuthState, c.AuthMode, c.Identity = "unknown", "native-keyring", ""
		c.ManagedAI = false
		result, err := run(ctx, c, []string{"models"}, 20*time.Second, nil)
		if err != nil {
			return c, err
		}
		if result.exit != 0 {
			return c, problem("connection_catalog", "Antigravity could not list its models. No sign-in or AI task was started; its private diagnostics were discarded.")
		}
		c.Models, err = parseAGYModels(result.stdout)
		if err != nil {
			return c, err
		}
		return c, nil
	case "codex", "claude":
		updated, err := inspectAdapter(ctx, c)
		var typed *core.Error
		if c.Provider == "claude" && errors.As(err, &typed) && typed.Code == "claude_version" {
			copy := *typed
			copy.Message = fmt.Sprintf("This Claude Code installation (%s) does not match UCLI's reviewed %s build. Your native sign-in may still work; this is a version or build compatibility check, not a request to sign in again. Use Claude Code directly until this installation is reviewed.", c.Version, claude.Version)
			err = &copy
		}
		return updated, err
	}
	return c, problem("connection_input", "Unsupported native provider.")
}

// AGY currently emits tab-separated model ID/name pairs. Fail closed on drift:
// these are display-only catalog entries, never auth or model admission proof.
func parseAGYModels(raw []byte) ([]Model, error) {
	fail := func() ([]Model, error) {
		return nil, problem("connection_catalog", "Antigravity's model list did not match the supported metadata format; no models were cached.")
	}
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) {
		return fail()
	}
	lines := strings.Split(strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r"), "\n")
	if len(lines) > 256 {
		return fail()
	}
	models := make([]Model, 0, len(lines))
	seen := map[string]bool{}
	for _, line := range lines {
		parts := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(parts) != 2 || len(parts[0]) == 0 || len(parts[0]) > 128 || len(parts[1]) == 0 || len(parts[1]) > 256 || seen[parts[0]] {
			return fail()
		}
		for _, c := range parts[0] {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return fail()
			}
		}
		for _, c := range parts[1] {
			if c < 32 || c > 126 {
				return fail()
			}
		}
		if strings.TrimSpace(parts[1]) != parts[1] {
			return fail()
		}
		seen[parts[0]] = true
		models = append(models, Model{ID: parts[0], Name: parts[1]})
	}
	return models, nil
}

func inspectAdapter(ctx context.Context, c Connection) (Connection, error) {
	// Inspect the actual native identity, then let observe retain the connection's
	// original pin and persist identity-changed. Never silently rebind the label.
	binding := c.Binding()
	binding.Identity, binding.AuthMode = "", ""
	if c.Provider == "codex" {
		observed, err := codex.Inspect(ctx, binding)
		if err != nil {
			return c, err
		}
		c.Identity, c.AuthMode, c.PlanType = observed.Identity, observed.AuthMode, observed.PlanType
		c.AuthState = "authenticated"
		if observed.AuthMode == "none" {
			c.AuthState = "signed-out"
		}
		c.Models = nil
		for _, model := range observed.Models {
			c.Models = append(c.Models, Model{ID: model.ID, Name: model.Name, DefaultEffort: model.DefaultEffort, Efforts: append([]string{}, model.Efforts...), Default: model.Default})
		}
		return c, nil
	}
	if c.Provider == "claude" {
		observed, err := claude.Inspect(ctx, binding)
		if err != nil {
			return c, err
		}
		c.Identity, c.AuthMode, c.PlanType = observed.Identity, observed.AuthMode, observed.PlanType
		c.Version = observed.Version
		c.AuthState = "authenticated"
		if observed.AuthMode == "none" {
			c.AuthState = "signed-out"
		}
		c.Models = nil
		for _, model := range observed.Models {
			c.Models = append(c.Models, Model{ID: model.ID, Name: model.Name, DefaultEffort: model.DefaultEffort, Efforts: append([]string{}, model.Efforts...), Default: model.Default})
		}
		return c, nil
	}
	return c, problem("connection_input", "Unsupported structured identity adapter.")
}
func inspectGitHub(ctx context.Context, c Connection) (Connection, error) {
	result, err := run(ctx, c, []string{"auth", "status", "--active", "--hostname", "github.com", "--json", "hosts"}, 20*time.Second, nil)
	if err != nil {
		return c, err
	}
	state, err := githubAuthenticationState(result.stdout)
	if err != nil {
		return c, err
	}
	c.AuthState, c.AuthMode, c.Identity = state, "unknown", ""
	if state == "signed-out" {
		c.AuthMode = "none"
	}
	if state != "authenticated" {
		return c, nil
	}
	identity, err := run(ctx, c, []string{"api", "--hostname", "github.com", "user", "--jq", ".id"}, 20*time.Second, nil)
	if err != nil {
		return c, err
	}
	id := strings.TrimSpace(string(identity.stdout))
	number, err := strconv.ParseUint(id, 10, 64)
	if identity.exit != 0 || err != nil || number == 0 {
		return c, problem("connection_status", "GitHub's stable user identity was unavailable; no identity was persisted.")
	}
	c.AuthMode, c.Identity = "native-gh", IdentityFingerprint("github", "github.com:"+id)
	return c, nil
}
func githubAuthenticationState(raw []byte) (string, error) {
	var decoded struct {
		Hosts map[string][]struct {
			Active bool   `json:"active"`
			State  string `json:"state"`
			Host   string `json:"host"`
		} `json:"hosts"`
	}
	if json.Unmarshal(raw, &decoded) != nil || decoded.Hosts == nil {
		return "", problem("connection_status", "GitHub did not return a recognized authentication status; private output was discarded.")
	}
	if len(decoded.Hosts["github.com"]) == 0 {
		return "signed-out", nil
	}
	active := 0
	authenticated := false
	for _, account := range decoded.Hosts["github.com"] {
		if account.Active {
			active++
			authenticated = account.State == "success" && account.Host == "github.com"
		}
	}
	if active == 1 && authenticated {
		return "authenticated", nil
	}
	return "unknown", nil
}
func loginArgs(c Connection) ([]string, error) {
	switch c.Provider {
	case "codex":
		args := []string{"login", "--device-auth"}
		if c.HomeMode == "separate" {
			args = append(args, "-c", `cli_auth_credentials_store="file"`)
		}
		return args, nil
	case "claude":
		return []string{"--safe-mode", "--setting-sources", "", "auth", "login", "--claudeai"}, nil
	case "github":
		return []string{"auth", "login", "--hostname", "github.com", "--web", "--git-protocol", "https", "--skip-ssh-key"}, nil
	}
	return nil, problem("connection_login_unavailable", "This runtime requires its native interactive sign-in; UCLI will not start an inference-capable terminal for authentication.")
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
var urlPattern = regexp.MustCompile(`https://[^\s<>"']+`)
var codePattern = regexp.MustCompile(`\b[A-Z0-9]{4,6}-[A-Z0-9]{4,6}\b`)

func loginLine(provider, line string, emit func(LoginEvent) error) error {
	if !utf8.ValidString(line) || len(line) > 64<<10 {
		return problem("connection_process", "Native login emitted invalid text; private output was discarded.")
	}
	line = ansiPattern.ReplaceAllString(line, "")
	for _, raw := range urlPattern.FindAllString(line, -1) {
		raw = strings.TrimRight(raw, ".,;)")
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || len(raw) > 4096 {
			continue
		}
		if allowedLoginURL(provider, parsed) {
			if err = emit(LoginEvent{Type: "url", Message: "Open the provider's sign-in page.", URL: raw}); err != nil {
				return err
			}
		}
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "code") || strings.TrimSpace(line) == codePattern.FindString(line) {
		for _, code := range codePattern.FindAllString(line, -1) {
			if err := emit(LoginEvent{Type: "device-code", Message: "Enter this temporary code on the provider's page.", Code: code}); err != nil {
				return err
			}
		}
	}
	return nil
}

func allowedLoginURL(provider string, parsed *url.URL) bool {
	if parsed.Fragment != "" {
		return false
	}
	if provider == "codex" {
		return parsed.Host == "auth.openai.com" && parsed.Path == "/codex/device" && parsed.RawQuery == ""
	}
	if provider == "github" {
		return parsed.Host == "github.com" && parsed.Path == "/login/device" && parsed.RawQuery == ""
	}
	if provider != "claude" || parsed.Path != "/oauth/authorize" || (parsed.Host != "claude.ai" && parsed.Host != "console.anthropic.com" && parsed.Host != "platform.claude.com") {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range query {
		if len(values) != 1 {
			return false
		}
		switch key {
		case "client_id", "response_type", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method", "login_hint", "organization_uuid":
		case "code":
			if values[0] != "true" {
				return false
			}
		default:
			return false
		}
	}
	return true
}
