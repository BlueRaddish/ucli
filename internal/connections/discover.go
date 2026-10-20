package connections

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func defaultHome(provider string) string {
	home, _ := os.UserHomeDir()
	switch provider {
	case "codex":
		if v := os.Getenv("CODEX_HOME"); v != "" {
			return v
		}
		return join(home, ".codex")
	case "claude":
		if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
			return v
		}
		return join(home, ".claude")
	case "agy":
		return join(home, ".gemini", "antigravity-cli")
	case "github":
		if v := os.Getenv("GH_CONFIG_DIR"); v != "" {
			return v
		}
		if runtime.GOOS == "windows" {
			return join(os.Getenv("APPDATA"), "GitHub CLI")
		}
		if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
			return join(v, "gh")
		}
		return join(home, ".config", "gh")
	}
	return ""
}
func findExecutable(provider string) string {
	name := provider
	if name == "github" {
		name = "gh"
	}
	paths := []string{}
	if p, err := exec.LookPath(name); err == nil {
		paths = append(paths, p)
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		switch provider {
		case "codex":
			arch := runtime.GOARCH
			if arch == "amd64" {
				arch = "x64"
			}
			paths = append(paths, join(os.Getenv("APPDATA"), "npm", "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-"+arch, "vendor", codexTriple(), "bin", "codex.exe"))
		case "claude":
			paths = append(paths, join(home, ".local", "bin", "claude.exe"))
		case "agy":
			paths = append(paths, join(os.Getenv("LOCALAPPDATA"), "agy", "bin", "agy.exe"))
		case "github":
			paths = append(paths, join(os.Getenv("ProgramFiles"), "GitHub CLI", "gh.exe"))
		}
	} else {
		paths = append(paths, join(home, ".local", "bin", name))
	}
	for _, p := range paths {
		if runtime.GOOS == "windows" && filepath.Ext(p) != ".exe" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			continue
		}
		if info, err := os.Stat(resolved); err == nil && info.Mode().IsRegular() {
			return resolved
		}
	}
	return ""
}
func codexTriple() string {
	if runtime.GOARCH == "arm64" {
		return "aarch64-pc-windows-msvc"
	}
	return "x86_64-pc-windows-msvc"
}

// ExecutablePresent checks known local paths only. Presence is not a verified
// version, sign-in, entitlement or an executable admission decision.
func ExecutablePresent(provider string) bool {
	return providerKnown(provider) && findExecutable(provider) != ""
}

// Discover explicitly inspects known install locations and hashes native files.
// It never executes a shim/binary, reads credentials, or creates a native home.
func (s *Service) Discover(ctx context.Context) ([]Discovered, error) {
	if ctx == nil {
		return nil, problem("connection_input", "Discovery requires a context.")
	}
	result := []Discovered{}
	for _, provider := range []string{"codex", "claude", "agy", "github"} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := Discovered{Provider: provider, Executable: findExecutable(provider), NativeHome: defaultHome(provider), Reason: "Native executable was not found; provide its absolute path."}
		if entry.Executable != "" {
			digest, err := executableHash(ctx, entry.Executable)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				entry.Reason = "Native executable could not be validated; no process was started."
			} else {
				entry.SHA256, entry.Available = digest, true
				entry.Reason = "Native bytes found; sign-in, identity, isolation and inference are not checked by discovery."
			}
		}
		result = append(result, entry)
	}
	return result, nil
}
