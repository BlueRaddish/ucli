package claude

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

type Driver struct{}

var _ adapters.Driver = Driver{}

func (Driver) Execute(ctx context.Context, req adapters.Request, emit func(string) error) (adapters.Outcome, error) {
	out := adapters.Outcome{State: "failed"}
	if err := validateRequest(ctx, req, emit); err != nil {
		return out, err
	}
	inspection, err := Inspect(ctx, *req.Binding)
	if err != nil {
		return out, err
	}
	if inspection.AuthMode == "none" {
		return out, fail("claude_login", "The linked native Claude profile is signed out; sign in explicitly before running.", 3)
	}
	if inspection.Identity != req.Binding.Identity || inspection.AuthMode != req.Binding.AuthMode {
		return out, fail("claude_identity_changed", "The linked native Claude account changed; relink it explicitly before running.", 5)
	}
	model, _, err := selectedModel(inspection.Models, req.Attempt.Effective)
	if err != nil {
		return out, err
	}
	return execute(ctx, req, emit, model)
}

func validateRequest(ctx context.Context, req adapters.Request, emit func(string) error) error {
	if ctx == nil || emit == nil || req.OnContext == nil {
		return fail("claude_request", "Claude requires a context, output consumer and durable provenance callback.", 2)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b := req.Binding
	r := req.Attempt.Effective
	if b == nil || b.ID == "" || b.Runtime != "claude" || b.AuthMode != "claude.ai" || len(b.Identity) != 64 || b.SHA256 == "" || b.Version != Version {
		return fail("claude_binding", "Link and verify the native Claude subscription account before dispatch.", 3)
	}
	if r.Runtime != "claude" || r.Provider != "anthropic" || r.Account != b.ID || r.Mode != "native" || (r.Permission != "read-only" && r.Permission != "sandbox") {
		return fail("claude_route", "Claude requires the linked account and an explicit no-tools or full-local-access route.", 5)
	}
	if req.Executable != "" && req.Executable != b.Executable {
		return fail("claude_binding", "Claude executable differs from its linked native binding.", 5)
	}
	if !strings.HasPrefix(r.Model, "claude/") || !modelToken(strings.TrimPrefix(r.Model, "claude/")) || (r.Effort != "low" && r.Effort != "medium" && r.Effort != "high" && r.Effort != "max") {
		return fail("claude_model", "Choose a native catalog model and an explicit supported effort.", 5)
	}
	if !filepath.IsAbs(req.StateHome) || !filepath.IsAbs(req.Attempt.Workspace.Dir) {
		return fail("claude_request", "Claude state and workspace paths must be absolute.", 2)
	}
	return core.ValidateInput(req.Attempt.Input)
}

// execute is private so fixture transports cannot bypass public native admission.
// Once the input can have reached Claude, cancellation is unknown until a matching
// result is observed. Killing the local process is never a remote stop receipt.
func execute(ctx context.Context, req adapters.Request, emit func(string) error, selected Model) (out adapters.Outcome, err error) {
	out.State = "failed"
	_, nativeEffort, err := selectedModel([]Model{selected}, req.Attempt.Effective)
	if err != nil {
		return out, err
	}
	dispatched := false
	defer func() {
		if dispatched && err != nil {
			var p *core.Error
			if errors.As(err, &p) {
				copy := *p
				copy.Acceptance, copy.SideEffects = "unknown", "unknown"
				if out.State == "failed" || out.State == "complete" {
					copy.Acceptance = "accepted"
				}
				err = &copy
			}
		}
		// App persists Message ahead of err. Run this after cleanup/classification
		// so the generic unknown placeholder cannot erase a safe local reason.
		// Native payload and callback errors are redacted at their boundaries.
		if err != nil && (out.State == "unknown" || out.State == "failed") {
			detail := err.Error()
			if out.Message == "" {
				out.Message = detail
			} else if !strings.Contains(out.Message, detail) {
				out.Message += " " + detail
			}
		}
	}()
	work, err := os.MkdirTemp("", "ucli-claude-turn-")
	if err != nil {
		return out, fail("claude_local", "Cannot create native run directory.", 9)
	}
	defer func() {
		if e := os.RemoveAll(work); e != nil {
			err = errors.Join(fail("claude_cleanup", "Native run directory cleanup failed.", 9), err)
		}
	}()
	session, err := uuid()
	if err != nil {
		return out, err
	}
	payload, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": req.Attempt.Input}, "session_id": session, "parent_tool_use_id": nil})
	payload = append(payload, '\n')
	out.NativeSession = session
	if err = req.OnContext(core.NativeContext{Runtime: "claude", Provider: "anthropic", Account: req.Binding.ID, SessionID: session, Durability: "ephemeral", Provenance: "allocated"}); err != nil {
		return out, fail("claude_context", "Native provenance could not be persisted; no turn was launched.", 9)
	}
	if err = ctx.Err(); err != nil {
		out.State = "interrupted"
		return out, err
	}
	launchRoute := req.Attempt.Effective
	// Keep advertised modifiers such as [1m]; resolvedModel is the wire-ID
	// confirmation, not a replacement for the selected native launch value.
	launchRoute.Effort = nativeEffort
	args := arguments(launchRoute, session)
	cwd := work
	if req.Attempt.Effective.Permission == "sandbox" {
		cwd = req.Attempt.Workspace.Dir
	}
	lifetime, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	child, err := process.Start(lifetime, process.Spec{Executable: req.Binding.Executable, Args: args, Env: environment(req.Binding.Home, work), Dir: cwd, FrameBytes: maxFrame, QueueBytes: 4 * maxFrame})
	if err != nil {
		return out, sanitizedProcess(err)
	}
	defer func() {
		if e := child.Close(); e != nil && (!child.Settled() || !contextOnly(e)) {
			out.State = "unknown"
			out.Message = "Native Claude cleanup is unconfirmed."
			err = errors.Join(fail("claude_cleanup", out.Message, 9), err)
		}
	}()
	// Send can partially write. Fence before calling it, not after a successful return.
	out.State, out.Message = "unknown", "Native Claude turn acceptance or outcome is unknown; do not replay blindly."
	dispatched = true
	// JSON escaping may expand a valid 1 MiB prompt. Bound each owned pipe write,
	// not the complete framed request; Claude still receives one JSON line.
	for len(payload) > 0 {
		n := min(len(payload), maxFrame)
		if err = child.Send(lifetime, payload[:n]); err != nil {
			return out, sanitizedProcess(err)
		}
		payload = payload[n:]
	}
	if err = child.CloseInput(); err != nil {
		return out, sanitizedProcess(err)
	}
	s := stream{session: session, permission: req.Attempt.Effective.Permission, requestedModel: req.Attempt.Effective.Model, expectedModel: selected.ResolvedID}
	defer func() { out.NativeModel = s.model }()
	var buffer []byte
	total := 0
	for {
		select {
		case <-lifetime.Done():
			return out, lifetime.Err()
		case chunk, ok := <-child.Events():
			if !ok {
				exit, waitErr := child.Wait(lifetime)
				if waitErr != nil {
					return out, sanitizedProcess(waitErr)
				}
				if len(buffer) != 0 || !s.terminal {
					return out, fail("claude_protocol", "Native Claude ended without a complete matching result.", 9)
				}
				if s.state == "complete" && exit.Code != 0 {
					return out, fail("claude_protocol", "Native Claude result and process exit disagree.", 9)
				}
				out.State = s.state
				if s.state == "failed" {
					err = s.failureError()
					out.Message = err.Error()
					return out, err
				}
				out.Message = "Native Claude completed the turn. Native session is ephemeral; follow-ups start fresh."
				return out, nil
			}
			total += len(chunk.Data)
			if total > 16<<20 {
				return out, fail("claude_output", "Native Claude exceeded its stream bound.", 9)
			}
			if chunk.Stream != "stdout" {
				continue
			}
			buffer = append(buffer, chunk.Data...)
			for {
				index := bytes.IndexByte(buffer, '\n')
				if index < 0 {
					break
				}
				line := buffer[:index]
				buffer = buffer[index+1:]
				if len(line) > maxFrame {
					return out, fail("claude_output", "Native Claude frame exceeded its bound.", 9)
				}
				if len(bytes.TrimSpace(line)) == 0 {
					continue
				}
				if e := s.consume(line, emit); e != nil {
					return out, e
				}
			}
			if len(buffer) > maxFrame {
				return out, fail("claude_output", "Native Claude frame exceeded its bound.", 9)
			}
		}
	}
}

func arguments(r core.Route, session string) []string {
	mode, tools, maxTurns := "dontAsk", "", "1"
	if r.Permission == "sandbox" {
		mode, tools, maxTurns = "bypassPermissions", "default", "30"
	}
	args := []string{"--safe-mode", "--setting-sources", "", "--settings", `{"disableAllHooks":true,"disableClaudeAiConnectors":true}`, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--disable-slash-commands", "--no-chrome", "--no-session-persistence", "--permission-mode", mode, "--permission-prompts", "none", "--tools", tools, "--disallowedTools", "mcp__*", "--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--max-turns", maxTurns, "--session-id", session}
	if r.Effort != "" {
		args = append(args, "--effort", r.Effort)
	}
	if r.Model != "claude/default" {
		args = append(args, "--model", strings.TrimPrefix(r.Model, "claude/"))
	}
	if r.Permission == "read-only" {
		args = append(args, "--system-prompt", "You are a helpful assistant. Respond to the user's text. No tools are available.")
	}
	return args
}
func uuid() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fail("claude_context", "Cannot create a native session identity.", 9)
	}
	value[6] = (value[6] & 15) | 64
	value[8] = (value[8] & 63) | 128
	s := hex.EncodeToString(value[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}
