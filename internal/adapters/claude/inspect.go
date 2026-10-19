// Package claude executes explicitly linked native Claude Code accounts.
// Native credentials stay with Claude; UCLI stores only an opaque fingerprint.
package claude

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
	"strings"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
	"ucli.local/ucli/internal/runtimes"
)

const Version = "2.1.299"

type Model struct {
	ID, Name, DefaultEffort string
	Efforts                 []string
	Default                 bool
}
type Inspection struct {
	Identity, AuthMode, Version, PlanType, SHA256 string
	Models                                        []Model
}

func Catalog() []Model {
	return []Model{
		{ID: "default", Name: "Claude native default", DefaultEffort: "medium", Efforts: []string{"low", "medium", "high"}, Default: true},
		{ID: "sonnet", Name: "Claude Sonnet (native alias)", DefaultEffort: "medium", Efforts: []string{"low", "medium", "high"}},
		{ID: "opus", Name: "Claude Opus (native alias)", DefaultEffort: "medium", Efforts: []string{"low", "medium", "high"}},
	}
}

func fail(code, message string, exit int) error {
	p := core.Problem(code, message, exit)
	p.Origin, p.Acceptance, p.SideEffects = "claude", "not-sent", "none"
	return p
}

// Inspect launches only native auth status, never inference or a login flow.
// Empty SHA256/Version are allowed for initial linking; supplied values must match.
// Account identity comes from native metadata, never token contents.
func Inspect(ctx context.Context, b adapters.NativeBinding) (Inspection, error) {
	if b.HomeMode != "existing" {
		return Inspection{}, fail("claude_home_mode", "Claude currently supports only an explicitly linked existing native profile; separate credential isolation is not verified.", 3)
	}
	if ctx == nil {
		return Inspection{}, fail("claude_binding", "Claude inspection requires a context.", 2)
	}
	if b.Runtime != "claude" || !filepath.IsAbs(b.Executable) || !filepath.IsAbs(b.Home) || strings.ContainsRune(b.Executable+b.Home, 0) {
		return Inspection{}, fail("claude_binding", "Choose explicit native Claude executable and configuration paths.", 2)
	}
	if info, err := os.Lstat(b.Home); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Inspection{}, fail("claude_home", "Native Claude configuration directory is unavailable or linked.", 3)
	}
	digest, err := verifiedExecutable(ctx, b)
	if err != nil {
		return Inspection{}, err
	}
	work, err := os.MkdirTemp("", "ucli-claude-inspect-")
	if err != nil {
		return Inspection{}, fail("claude_local", "Cannot prepare native inspection directory.", 9)
	}
	defer os.RemoveAll(work)
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, err := collect(probeCtx, process.Spec{Executable: b.Executable, Args: []string{"--safe-mode", "--setting-sources", "", "auth", "status"}, Env: environment(b.Home, work), Dir: work, FrameBytes: 64 << 10, QueueBytes: 256 << 10})
	if err != nil {
		return Inspection{}, err
	}
	return inspectStatus(raw, b.Home, digest)
}

func verifiedExecutable(ctx context.Context, b adapters.NativeBinding) (string, error) {
	if b.Version != "" && b.Version != Version {
		return "", fail("claude_version", "This Claude version has not been verified for the native adapter.", 3)
	}
	var expected string
	for _, entry := range runtimes.Catalog() {
		if entry.ID == "claude" && entry.Version == Version && entry.OS == runtime.GOOS && entry.Arch == runtime.GOARCH {
			expected = entry.SHA256
		}
	}
	if expected == "" {
		return "", fail("claude_platform", "No verified Claude executable is available for this platform.", 3)
	}
	file, err := os.Open(b.Executable)
	if err != nil {
		return "", fail("claude_executable", "Linked Claude executable is unavailable.", 3)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fail("claude_executable", "Claude executable must be a regular file.", 3)
	}
	h := sha256.New()
	buf := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, e := file.Read(buf)
		h.Write(buf[:n])
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", fail("claude_executable", "Cannot verify linked Claude executable.", 3)
		}
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if digest != expected || b.SHA256 != "" && b.SHA256 != digest {
		return "", fail("claude_version", "Linked Claude executable does not match its reviewed identity.", 5)
	}
	return digest, nil
}

func inspectStatus(raw []byte, home, digest string) (Inspection, error) {
	fields, err := object(raw)
	if err != nil {
		return Inspection{}, fail("claude_status", "Native Claude status was not valid bounded JSON.", 3)
	}
	var logged bool
	if string(fields["loggedIn"]) != "true" && string(fields["loggedIn"]) != "false" || json.Unmarshal(fields["loggedIn"], &logged) != nil {
		return Inspection{}, fail("claude_status", "Native Claude status omitted a valid login state.", 3)
	}
	if !samePath(text(fields, "configDirectory"), home) {
		return Inspection{}, fail("claude_identity", "Native Claude reported a different configuration directory.", 5)
	}
	if !logged {
		if text(fields, "authMethod") != "none" {
			return Inspection{}, fail("claude_auth_mode", "Native Claude reported an unsupported signed-out authentication source.", 3)
		}
		return Inspection{AuthMode: "none", Version: Version, SHA256: digest}, nil
	}
	if text(fields, "authMethod") != "claude.ai" || text(fields, "apiProvider") != "firstParty" {
		return Inspection{}, fail("claude_auth_mode", "Use an existing native Claude subscription login; API keys and third-party billing are not selected.", 3)
	}
	config := filepath.Join(home, ".claude.json")
	userHome, _ := os.UserHomeDir()
	if samePath(home, filepath.Join(userHome, ".claude")) {
		config = filepath.Join(userHome, ".claude.json")
	}
	info, err := os.Lstat(config)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return Inspection{}, fail("claude_identity", "Native Claude account metadata is unavailable; recheck the native login.", 3)
	}
	file, err := os.Open(config)
	if err != nil {
		return Inspection{}, fail("claude_identity", "Cannot read native account metadata.", 3)
	}
	data, err := io.ReadAll(io.LimitReader(file, 16<<20+1))
	file.Close()
	if err != nil || len(data) > 16<<20 {
		return Inspection{}, fail("claude_identity", "Native account metadata exceeds its bound.", 3)
	}
	metadata, err := object(data)
	if err != nil {
		return Inspection{}, fail("claude_identity", "Native account metadata is invalid.", 3)
	}
	account, err := object(metadata["oauthAccount"])
	if err != nil {
		return Inspection{}, fail("claude_identity", "Native account identity is unavailable.", 3)
	}
	id := text(account, "accountUuid")
	if !validUUID(id) || text(fields, "email") == "" || text(fields, "email") != text(account, "emailAddress") {
		return Inspection{}, fail("claude_identity", "Native auth status does not match its account metadata.", 5)
	}
	fingerprint := sha256.Sum256([]byte("claude\x00" + id))
	return Inspection{Identity: hex.EncodeToString(fingerprint[:]), AuthMode: "claude.ai", Version: Version, PlanType: text(fields, "subscriptionType"), SHA256: digest, Models: Catalog()}, nil
}

func samePath(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func environment(home, temp string) []string {
	env := []string{}
	for _, key := range []string{"SystemRoot", "WINDIR", "PATH", "PATHEXT", "COMSPEC", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "LANG"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	userHome, _ := os.UserHomeDir()
	if !samePath(home, filepath.Join(userHome, ".claude")) {
		env = append(env, "CLAUDE_CONFIG_DIR="+home)
	}
	env = append(env, "TEMP="+temp, "TMP="+temp, "TMPDIR="+temp, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "CLAUDE_CODE_DISABLE_CLAUDE_MDS=1", "CLAUDE_CODE_DISABLE_FILE_MENTIONS=1", "CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1", "CLAUDE_CODE_SKIP_PROMPT_HISTORY=1")
	return env
}

func collect(ctx context.Context, spec process.Spec) (raw []byte, err error) {
	child, err := process.Start(ctx, spec)
	if err != nil {
		return nil, sanitizedProcess(err)
	}
	defer func() {
		if e := child.Close(); e != nil && (!child.Settled() || !contextOnly(e)) {
			raw = nil
			err = errors.Join(fail("claude_cleanup", "Native Claude cleanup is unconfirmed.", 9), err)
		}
	}()
	if err = child.CloseInput(); err != nil {
		return nil, sanitizedProcess(err)
	}
	total := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case c, ok := <-child.Events():
			if !ok {
				exit, e := child.Wait(ctx)
				if e != nil {
					return nil, sanitizedProcess(e)
				}
				if exit.Code != 0 {
					// Claude's documented signed-out status exits 1. Admit only that
					// explicit status shape; inspectStatus still checks profile scope.
					fields, parseErr := object(raw)
					if exit.Code != 1 || parseErr != nil || string(fields["loggedIn"]) != "false" || text(fields, "authMethod") != "none" {
						return nil, fail("claude_status", "Native Claude status failed; inspect the native login.", 3)
					}
				}
				return raw, nil
			}
			total += len(c.Data)
			if total > 256<<10 {
				return nil, fail("claude_status", "Native status exceeded its output bound.", 3)
			}
			if c.Stream == "stdout" {
				raw = append(raw, c.Data...)
			}
		}
	}
}

func contextOnly(err error) bool {
	if err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, e := range children {
			if !contextOnly(e) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return contextOnly(wrapped.Unwrap())
	}
	return false
}
func sanitizedProcess(err error) error {
	if contextOnly(err) {
		return err
	}
	if errors.Is(err, process.ErrUnsupportedPlatform) {
		return fail("claude_platform", "Owned native processes are not supported on this host.", 3)
	}
	return fail("claude_transport", "Native Claude transport failed; no raw diagnostic was retained.", 9)
}
