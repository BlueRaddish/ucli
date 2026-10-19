package voiceenv

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"ucli.local/ucli/internal/process"
)

//go:embed probe.py
var metadataScript string

type profile struct {
	Versions map[string]string `json:"versions"`
	Modules  map[string]bool   `json:"modules"`
}
type report struct {
	Format        int     `json:"format"`
	Python        string  `json:"python"`
	PythonVersion string  `json:"python_version"`
	ABI           string  `json:"abi"`
	UserSite      string  `json:"user_site"`
	Global        profile `json:"global"`
	Selected      profile `json:"selected"`
}

func environment() []string {
	var env []string
	// User-location variables are needed only to derive this interpreter's exact
	// user site. -I -S ignores PYTHONPATH and executes no site/.pth startup code.
	for _, key := range []string{"SystemRoot", "WINDIR", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TMP", "TEMP"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func command(ctx context.Context, python string, args []string, directory string, deadline time.Duration, overrides ...string) (output []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	env := environment()
	for _, entry := range overrides {
		key, _, _ := strings.Cut(entry, "=")
		filtered := env[:0]
		for _, old := range env {
			oldKey, _, _ := strings.Cut(old, "=")
			if !strings.EqualFold(key, oldKey) {
				filtered = append(filtered, old)
			}
		}
		env = append(filtered, entry)
	}
	child, err := process.Start(ctx, process.Spec{Executable: python, Args: args, Dir: directory, Env: env, FrameBytes: 64 << 10, QueueBytes: 1 << 20})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, problem("voice_python_unavailable", "Selected Python could not start under owned-process control.")
	}
	defer func() {
		if closeErr := child.Close(); closeErr != nil {
			// Close returns the cached lifetime cancellation even after confirmed
			// owned-tree teardown. Keep cancellation, but permit own-stage cleanup
			// only when owned-tree teardown is settled and no other error exists.
			if child.Settled() && onlyContextError(closeErr) {
				return
			}
			err = errors.Join(problem("voice_process_cleanup", "Voice dependency process cleanup was not confirmed; its private state was retained."), err)
			output = nil
		}
	}()
	total := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case chunk, ok := <-child.Events():
			if !ok {
				exit, waitErr := child.Wait(ctx)
				if waitErr != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					return nil, problem("voice_process_failed", "Voice dependency process transport failed.")
				}
				if exit.Code != 0 {
					return nil, problem("voice_process_failed", "Voice dependency process failed; native output was not copied into diagnostics.")
				}
				return output, nil
			}
			total += len(chunk.Data)
			if total > 2<<20 {
				return nil, problem("voice_process_output", "Voice dependency process exceeded its bounded output.")
			}
			if chunk.Stream == "stdout" {
				output = append(output, chunk.Data...)
			}
		}
	}
}

func onlyContextError(err error) bool {
	if err == nil {
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
	return err == context.Canceled || err == context.DeadlineExceeded
}

func probe(ctx context.Context, python, siteDir string) (report, error) {
	args := []string{"-I", "-S", "-B", "-c", metadataScript}
	if siteDir != "" {
		args = append(args, siteDir)
	}
	raw, err := command(ctx, python, args, filepath.Dir(python), 15*time.Second)
	if err != nil {
		return report{}, err
	}
	return decodeReport(raw)
}

func decodeReport(raw []byte) (report, error) {
	var value report
	if len(raw) > 512<<10 || !utf8.Valid(raw) {
		return value, problem("voice_probe_invalid", "Python metadata response exceeded its bound or encoding.")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value.Format != 1 || !validText(value.PythonVersion, 64) || !validText(value.ABI, 128) || !filepath.IsAbs(value.Python) || !validText(value.Python, 32768) || !filepath.IsAbs(value.UserSite) || !validText(value.UserSite, 32768) {
		return report{}, problem("voice_probe_invalid", "Python metadata response was invalid.")
	}
	for _, p := range []profile{value.Global, value.Selected} {
		if len(p.Versions) > 2048 || len(p.Modules) != 7 {
			return report{}, problem("voice_probe_invalid", "Python package metadata exceeded its bound.")
		}
		for name, version := range p.Versions {
			if !validText(name, 128) || !validText(version, 128) {
				return report{}, problem("voice_probe_invalid", "Python package metadata was invalid.")
			}
		}
		for name := range p.Modules {
			if name != "faster-whisper" && name != "ctranslate2" && name != "numpy" && name != "tokenizers" && name != "huggingface-hub" && name != "sounddevice" && name != "pip" {
				return report{}, problem("voice_probe_invalid", "Python module metadata was invalid.")
			}
		}
	}
	return value, nil
}

func validText(value string, bound int) bool {
	return value != "" && len(value) <= bound && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func compatible(p profile, capture bool) bool {
	for name, major := range map[string]string{"faster-whisper": "1.", "ctranslate2": "4.", "numpy": "", "tokenizers": "", "huggingface-hub": ""} {
		if !p.Modules[name] || p.Versions[name] == "" || major != "" && !strings.HasPrefix(p.Versions[name], major) {
			return false
		}
	}
	return !capture || p.Modules["sounddevice"] && strings.HasPrefix(p.Versions["sounddevice"], "0.")
}

func pipArguments(target string, capture bool) []string {
	args := []string{"-I", "-B", "-m", "pip", "--isolated", "--no-input", "--disable-pip-version-check", "install", "--only-binary=:all:", "--index-url", "https://pypi.org/simple", "--target", target, "--ignore-installed", "--no-compile", "--no-cache-dir", "--timeout", "30", "--retries", "1", "faster-whisper==1.2.1"}
	if capture {
		args = append(args, "sounddevice==0.5.6")
	}
	return args
}

func install(ctx context.Context, python, target string, capture bool) error {
	temporary := filepath.Join(filepath.Dir(target), "tmp")
	if err := os.Mkdir(temporary, 0700); err != nil {
		return problem("voice_env_cache", "Private wheel temporary directory could not be created.")
	}
	if err := secureCreated(temporary, true); err != nil {
		return problem("voice_env_cache", "Private wheel temporary directory could not be secured.")
	}
	// PIP_CONFIG_FILE is a special pip configuration override even in isolated
	// mode. Python's os.devnull spelling on Windows is lowercase, unlike Go's.
	null := os.DevNull
	if runtime.GOOS == "windows" {
		null = "nul"
	}
	_, err := command(ctx, python, pipArguments(target, capture), filepath.Dir(target), 10*time.Minute, "TMP="+temporary, "TEMP="+temporary, "TMPDIR="+temporary, "PIP_CONFIG_FILE="+null)
	return err
}
