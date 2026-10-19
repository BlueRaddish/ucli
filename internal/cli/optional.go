package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/artifact"
	"ucli.local/ucli/internal/assets"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
	"ucli.local/ucli/internal/runtimes"
)

func invocationHome(p invocation) (string, error) {
	if p.home != "" {
		return filepath.Abs(p.home)
	}
	return app.DefaultHome()
}

func optionalProblem(err error) error {
	var problem *core.Error
	if errors.As(err, &problem) {
		return err
	}
	if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && !onlyContextError(err) {
		return errors.Join(core.Problem("optional_cleanup_unconfirmed", "Optional operation cancellation had an additional failure; cleanup was not confirmed. No task was submitted.", 9), err)
	}
	if errors.Is(err, context.Canceled) {
		return core.Problem("interrupted", "Optional operation interrupted. No AI task was submitted.", 130)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return core.Problem("deadline", "Optional operation reached its deadline. No AI task was submitted.", 124)
	}
	if errors.Is(err, process.ErrUnsupportedPlatform) {
		return core.Problem("voice_platform", "Owned voice processes are not yet supported on this host.", 3)
	}
	var failure *artifact.Error
	if errors.As(err, &failure) {
		exit := 3
		switch failure.Code {
		case artifact.InvalidRequest:
			exit = 2
		case artifact.IntegrityFailed:
			exit = 6
		case artifact.CacheUnsafe:
			exit = 5
		case artifact.CacheIO:
			exit = 9
		}
		return core.Problem(string(failure.Code), failure.Message, exit)
	}
	return err
}

// A joined context error does not erase a transport or cleanup failure.
func onlyContextError(err error) bool {
	if err == nil || err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !onlyContextError(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return onlyContextError(wrapped.Unwrap())
	}
	return false
}

func optionalCommand(ctx context.Context, p invocation, in io.Reader, o *output) int {
	switch p.command {
	case "runtime list":
		entries := runtimes.Catalog()
		if o.json {
			return optionalOutput(o, o.data("runtimes", entries))
		}
		_ = o.text("Optional native runtime bytes · nothing is downloaded or executed by this list\n")
		for _, entry := range entries {
			if entry.OS == runtime.GOOS && entry.Arch == runtime.GOARCH {
				_ = o.text(fmt.Sprintf("%s %s · %.1f MB · first requested preparation only\n", entry.ID, entry.Version, float64(entry.Bytes)/1e6))
			}
		}
		_ = o.text("Use ucli runtime prepare ID when needed. Preparing bytes does not log in or enable managed AI.\n")
		return 0
	case "voice models":
		models := assets.Catalog()
		if o.json {
			return optionalOutput(o, o.data("voice_models", models))
		}
		_ = o.text("Transcription models · metadata only; no Python, microphone or download\n")
		for _, model := range models {
			var size int64
			for _, file := range model.Files {
				size += file.Bytes
			}
			_ = o.text(fmt.Sprintf("%s · %.1f MB · downloaded on first requested voice use\n", model.ID, float64(size)/1e6))
		}
		return 0
	case "runtime prepare":
		home, err := invocationHome(p)
		if err != nil {
			return o.problem(err)
		}
		if p.id != "claude" {
			return o.problem(usageError("no reviewed runtime download for " + p.id + "; inspect ucli runtime list"))
		}
		if !o.json {
			_ = o.progress("Preparing only the requested native runtime; existing installations are kept.\n")
		}
		prepared, err := runtimes.Prepare(ctx, filepath.Join(home, "cache", "optional"), p.id, nativeRuntimeCandidate(p.id), p.offline)
		if err != nil {
			return o.problem(optionalProblem(err))
		}
		if o.json {
			return optionalOutput(o, o.data("runtime_prepared", prepared))
		}
		_ = o.text(fmt.Sprintf("%s %s · %s\nVerified bytes: %s\nNo runtime was executed or account opened.\n", prepared.ID, prepared.Version, prepared.Source, prepared.Path))
		for _, warning := range prepared.Warnings {
			_ = o.progress(warning + "\n")
		}
		return 0
	default:
		return runVoiceCommand(ctx, p, in, o)
	}
}

func optionalOutput(o *output, err error) int {
	if err != nil {
		return o.problem(err)
	}
	return 0
}

func nativeRuntimeCandidate(id string) string {
	if id != "claude" {
		return ""
	}
	if configured := os.Getenv("UCLI_CLAUDE_EXE"); configured != "" {
		return configured
	}
	if home, err := os.UserHomeDir(); err == nil {
		name := "claude"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(home, ".local", "bin", name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	path, _ := exec.LookPath("claude")
	if path != "" {
		path, _ = filepath.Abs(path)
	}
	return path
}
