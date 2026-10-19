// Package muse is an experimental, local-only Muse echo driver. It never selects
// an authenticated provider and does not establish provider or OS admission.
package muse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

const schemaFingerprint = "sha256:6de32db08bbbe0deb478d2bfcaba001e9fb490986bc80db3aceb4d996abf7df9"
const requestTimeout = 8 * time.Second
const turnTimeout = 30 * time.Second
const interruptTimeout = 2 * time.Second

type Driver struct{}

// Execute starts one isolated memory-only native session and one echo turn.
// emit must return promptly (the app imposes its own bounded delivery gate).
// Cancellation independently closes the child even if emit violates that rule;
// Go cannot forcibly stop an arbitrary callback, so Execute itself may then wait.
func (Driver) Execute(ctx context.Context, req adapters.Request, emit func(string) error) (out adapters.Outcome, err error) {
	out.State = "failed"
	dispatched := false
	if ctx == nil || emit == nil {
		return out, errors.New("Muse requires a context and output consumer")
	}
	if err := validate(req); err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		out.State = "interrupted"
		return out, err
	}
	base := filepath.Join(req.StateHome, "runtimes", "muse-echo")
	if err := os.MkdirAll(base, 0700); err != nil {
		return out, errors.New("cannot prepare isolated Muse state")
	}
	root, err := os.MkdirTemp(base, "run-")
	if err != nil {
		return out, errors.New("cannot create isolated Muse state")
	}
	env, work, err := isolatedEnvironment(root)
	if err != nil {
		return out, errors.Join(err, removeIsolated(root))
	}
	// The caller's cancellation first gets a native interrupt opportunity. A
	// separate watcher below bounds child lifetime if callbacks stop cooperating.
	child, err := process.Start(context.WithoutCancel(ctx), process.Spec{
		Executable: req.Executable, Args: []string{"serve", "--provider", "echo", "--no-session-log", "--disable-write", "--disable-shell"},
		Env: env, Dir: work, FrameBytes: maxFrame, QueueBytes: 4 * maxFrame,
	})
	if err != nil {
		return out, errors.Join(fmt.Errorf("start isolated Muse: %w", err), removeIsolated(root))
	}
	finished := make(chan struct{})
	stopWatch := context.AfterFunc(ctx, func() {
		timer := time.NewTimer(interruptTimeout + time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			child.Close()
		case <-finished:
		}
	})
	defer func() {
		close(finished)
		stopWatch()
		if closeErr := child.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("Muse owned-process cleanup: %w", closeErr))
			// A valid remote terminal stays a fact, but incomplete local cleanup
			// retains the app fence until reconciliation.
			out.State = "unknown"
			out.Message = "Muse cleanup is uncertain; isolated state retained."
			return
		}
		err = errors.Join(err, removeIsolated(root))
		if !dispatched && out.State == "failed" && ctx.Err() != nil {
			out.State = "interrupted"
			out.Message = "Stopped before a native turn was dispatched."
		}
	}()
	r := &rpc{child: child}
	request := func(method string, params any) (json.RawMessage, bool, error) {
		callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		return r.call(callCtx, method, params)
	}
	hello, _, err := request("initialize", map[string]any{"clientInfo": map[string]string{"name": "ucli_echo", "version": "0.0.0"}, "capabilities": map[string]bool{"userInputDialogs": false}})
	if err != nil {
		return out, err
	}
	var hi struct {
		Schema struct {
			Version     int    `json:"version"`
			Fingerprint string `json:"fingerprint"`
		} `json:"schema"`
		Durability string `json:"sessionDurability"`
	}
	if json.Unmarshal(hello, &hi) != nil || hi.Schema.Version != 1 || hi.Schema.Fingerprint != schemaFingerprint || hi.Durability != "ephemeral" {
		return out, errors.New("Muse schema or ephemeral session mode is not the verified echo contract")
	}
	if _, err := r.send(ctx, map[string]any{"jsonrpc": "2.0", "method": "initialized"}); err != nil {
		return out, err
	}
	commandID, err := uuid7()
	if err != nil {
		return out, err
	}
	session, _, err := request("session/start", map[string]any{"commandId": commandID, "workspaceRoot": work, "providerId": "echo", "approvalMode": "denyUnmatched"})
	if err != nil {
		return out, err
	}
	var start struct {
		Session struct {
			ID        string `json:"sessionId"`
			Provider  string `json:"providerId"`
			Workspace string `json:"workspaceRoot"`
			Approval  struct {
				Mode string `json:"mode"`
			} `json:"approvalMode"`
		} `json:"session"`
	}
	if json.Unmarshal(session, &start) != nil || !validID(start.Session.ID) || start.Session.Provider != "echo" || start.Session.Approval.Mode != "denyUnmatched" || !sameDirectory(start.Session.Workspace, work) {
		return out, errors.New("Muse session did not confirm isolated echo routing")
	}
	out.NativeSession = start.Session.ID
	if req.OnContext != nil {
		provenance := core.NativeContext{Runtime: "muse", Provider: "echo", Account: "none", SessionID: out.NativeSession, Durability: "ephemeral"}
		if err := req.OnContext(provenance); err != nil {
			return out, err
		}
	}
	commandID, err = uuid7()
	if err != nil {
		return out, err
	}
	ack, sent, err := request("turn/start", map[string]any{"commandId": commandID, "sessionId": out.NativeSession, "input": []any{map[string]string{"type": "text", "text": req.Attempt.Input}}})
	if sent {
		dispatched = true
		out.State = "unknown"
		out.Message = "Muse turn acceptance or terminal outcome is unknown."
	}
	if err != nil {
		return out, err
	}
	var turn struct {
		ID          string `json:"turnId"`
		Command     string `json:"commandId"`
		Status      string `json:"status"`
		Disposition string `json:"disposition"`
		Started     bool   `json:"startedNewTurn"`
	}
	if json.Unmarshal(ack, &turn) != nil || !validID(turn.ID) || turn.Command != commandID || turn.Status != "accepted" || turn.Disposition != "started" || !turn.Started {
		return out, errors.New("Muse did not acknowledge a fresh matching turn")
	}
	stream := newTurnStream(out.NativeSession, turn.ID, req.Attempt.Input)
	runCtx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	for {
		event, readErr := r.event(runCtx)
		if readErr != nil {
			if ctx.Err() != nil {
				return interrupt(r, stream, out, emit)
			}
			return out, readErr
		}
		state, consumeErr := stream.consume(event, emit)
		if consumeErr != nil {
			if ctx.Err() != nil {
				return interrupt(r, stream, out, emit)
			}
			return out, consumeErr
		}
		if state != "" {
			out.State = state
			out.Message = terminalMessage(state)
			return out, nil
		}
	}
}

func interrupt(r *rpc, stream *turnStream, out adapters.Outcome, emit func(string) error) (adapters.Outcome, error) {
	ctx, cancel := context.WithTimeout(context.Background(), interruptTimeout)
	defer cancel()
	id, err := uuid7()
	if err != nil {
		return out, err
	}
	ack, _, err := r.call(ctx, "turn/interrupt", map[string]string{"commandId": id, "sessionId": stream.session, "turnId": stream.turn})
	if err != nil {
		return out, fmt.Errorf("Muse interrupt unconfirmed: %w", err)
	}
	var v struct {
		ID      string `json:"turnId"`
		Command string `json:"commandId"`
		Status  string `json:"status"`
	}
	if json.Unmarshal(ack, &v) != nil || v.ID != stream.turn || v.Command != id || v.Status != "accepted" {
		return out, errors.New("Muse interrupt acknowledgment identity mismatch")
	}
	for {
		e, err := r.event(ctx)
		if err != nil {
			return out, fmt.Errorf("Muse stop unconfirmed: %w", err)
		}
		state, err := stream.consume(e, emit)
		if err != nil {
			return out, err
		}
		if state != "" {
			out.State = state
			out.Message = terminalMessage(state)
			return out, nil
		}
	}
}

func terminalMessage(state string) string {
	switch state {
	case "complete":
		return "Local Muse echo completed; no model inference."
	case "interrupted":
		return "Muse confirmed the turn was cancelled."
	default:
		return "Muse reported a failed turn."
	}
}

func validate(req adapters.Request) error {
	r := req.Attempt.Effective
	if r.Runtime != "muse" || r.Provider != "echo" || r.Model != "muse/echo" || r.Effort != "none" || r.Mode != "echo" || r.Account != "none" || r.Permission != "read-only" {
		return errors.New("Muse driver accepts only model muse/echo with no reasoning, account none and read-only policy")
	}
	if !filepath.IsAbs(req.StateHome) || !filepath.IsAbs(req.Executable) {
		return errors.New("Muse executable and state home must be absolute paths")
	}
	return core.ValidateInput(req.Attempt.Input)
}

func isolatedEnvironment(root string) ([]string, string, error) {
	env := []string{}
	for _, key := range []string{"SystemRoot", "WINDIR", "COMSPEC", "PATH", "PATHEXT", "LANG"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	for _, pair := range [][2]string{{"HOME", "home"}, {"USERPROFILE", "home"}, {"APPDATA", "config"}, {"LOCALAPPDATA", "local"}, {"XDG_CONFIG_HOME", "config"}, {"XDG_DATA_HOME", "data"}, {"XDG_STATE_HOME", "state"}, {"XDG_CACHE_HOME", "cache"}, {"TEMP", "tmp"}, {"TMP", "tmp"}} {
		path := filepath.Join(root, pair[1])
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, "", errors.New("cannot create isolated Muse directories")
		}
		env = append(env, pair[0]+"="+path)
	}
	env = append(env, "MUSE_AUTH_PATH="+filepath.Join(root, "absent-auth.json"), "MUSE_NO_AUTO_UPDATE=1")
	work := filepath.Join(root, "workspace")
	if err := os.Mkdir(work, 0700); err != nil {
		return nil, "", errors.New("cannot create isolated Muse workspace")
	}
	return env, work, nil
}

func sameDirectory(a, b string) bool {
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && x.IsDir() && os.SameFile(x, y)
}

func removeIsolated(root string) error {
	if err := os.RemoveAll(root); err != nil {
		return errors.New("could not remove isolated Muse state")
	}
	return nil
}

func validID(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "\x00\r\n")
}

func uuid7() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("cannot generate native command identity")
	}
	ms := uint64(time.Now().UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	b[6] = (b[6] & 15) | 0x70
	b[8] = (b[8] & 63) | 0x80
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}
