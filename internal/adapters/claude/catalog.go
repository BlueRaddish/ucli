package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/process"
)

// DiscoverModels performs the SDK initialization handshake without sending a
// user message. Its exact file pin is checked, but discovery is not execution
// admission: an unreviewed build can supply a catalog, never a runnable route.
func DiscoverModels(ctx context.Context, b adapters.NativeBinding) ([]Model, error) {
	return discoverCatalog(ctx, b, nil)
}

func discoverCatalog(ctx context.Context, b adapters.NativeBinding, observe func([]byte)) ([]Model, error) {
	if err := validateInspection(ctx, b); err != nil {
		return nil, err
	}
	if b.SHA256 == "" {
		return nil, fail("claude_binding", "Catalog discovery requires an explicit executable fingerprint.", 5)
	}
	if _, err := executableDigest(ctx, b); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp("", "ucli-claude-catalog-")
	if err != nil {
		return nil, fail("claude_local", "Cannot prepare native catalog directory.", 9)
	}
	defer os.RemoveAll(work)
	id, err := uuid()
	if err != nil {
		return nil, err
	}
	probe, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := arguments(core.Route{Permission: "read-only", Model: "claude/default"}, id)
	return discoverModels(probe, process.Spec{Executable: b.Executable, Args: args, Env: environment(b.Home, work), Dir: work, FrameBytes: 64 << 10, QueueBytes: 1 << 20}, id, observe)
}

// The wire envelope follows Anthropic's SDK Query.initialize/_send_control_request.
// Only this one correlated response is accepted; callbacks cannot start tools.
func discoverModels(ctx context.Context, spec process.Spec, id string, observers ...func([]byte)) (models []Model, err error) {
	child, err := process.Start(ctx, spec)
	if err != nil {
		return nil, sanitizedProcess(err)
	}
	defer func() {
		if e := child.Close(); e != nil && (!child.Settled() || !contextOnly(e)) {
			models = nil
			err = errors.Join(fail("claude_cleanup", "Native Claude catalog cleanup is unconfirmed.", 9), err)
		}
	}()
	request, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "initialize", "hooks": nil}})
	if err = child.Send(ctx, append(request, '\n')); err != nil {
		return nil, sanitizedProcess(err)
	}
	var pending []byte
	total, metadata, received := 0, 0, false
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case chunk, ok := <-child.Events():
			if !ok {
				exit, e := child.Wait(ctx)
				if e != nil {
					return nil, sanitizedProcess(e)
				}
				if !received {
					return nil, catalogFailure("missing-response")
				}
				if len(bytes.TrimSpace(pending)) != 0 {
					return nil, catalogFailure("incomplete-frame")
				}
				if exit.Code != 0 {
					return nil, catalogFailure("nonzero-exit")
				}
				return models, nil
			}
			total += len(chunk.Data)
			if total > 1<<20 {
				return nil, catalogError()
			}
			if chunk.Stream != "stdout" {
				continue
			}
			pending = append(pending, chunk.Data...)
			for {
				at := bytes.IndexByte(pending, '\n')
				if at < 0 {
					break
				}
				line := bytes.TrimSpace(pending[:at])
				pending = pending[at+1:]
				if len(line) == 0 {
					continue
				}
				if len(observers) > 0 && observers[0] != nil {
					observers[0](line)
				}
				if f, e := object(line); e == nil && text(f, "type") == "system" && text(f, "subtype") == "ui_invalidate" {
					metadata++
					if metadata > 16 || !catalogUIInvalidation(f, id) {
						return nil, catalogFailure("invalid-ui-metadata")
					}
					continue
				}
				if received {
					return nil, catalogFailure("extra-frame")
				}
				if len(line) > 256<<10 {
					return nil, catalogFailure("frame-limit")
				}
				models, err = initializationModels(line, id)
				if err != nil {
					return nil, err
				}
				received = true
				if err = child.CloseInput(); err != nil {
					return nil, sanitizedProcess(err)
				}
			}
			if len(pending) > 256<<10 {
				return nil, catalogError()
			}
		}
	}
}

// The installed native schema's ui_invalidate invalidates render caches only.
// Discovery never requests UI/plugin rendering or grants authority from it.
func catalogUIInvalidation(f map[string]json.RawMessage, session string) bool {
	if text(f, "session_id") != session || !validUUID(text(f, "uuid")) || text(f, "event") != "ui.render" {
		return false
	}
	if raw, ok := f["prompts_screened"]; ok && string(raw) != "true" {
		return false
	}
	if raw, ok := f["instances"]; ok {
		var entries []json.RawMessage
		if json.Unmarshal(raw, &entries) != nil || string(raw) == "null" || len(entries) > 256 {
			return false
		}
		for _, entry := range entries {
			v, e := object(entry)
			if e != nil {
				return false
			}
			for _, key := range []string{"surface", "component", "instance_id"} {
				var value string
				if json.Unmarshal(v[key], &value) != nil || !catalogText(value, 4096) {
					return false
				}
			}
		}
	}
	return true
}

func catalogError() error { return catalogFailure("invalid-metadata") }

// reason is always a local literal, never a native key/value or diagnostic.
func catalogFailure(reason string) error {
	return fail("claude_catalog", "Native Claude model metadata did not match the bounded initialization protocol ("+reason+"); no task was sent and private diagnostics were discarded.", 5)
}

func initializationModels(raw []byte, requestID string) ([]Model, error) {
	f, err := object(raw)
	if err != nil {
		return nil, catalogFailure("invalid-json")
	}
	if text(f, "type") != "control_response" {
		return nil, catalogFailure("unexpected-frame-type")
	}
	r, err := object(f["response"])
	if err != nil {
		return nil, catalogFailure("invalid-response-envelope")
	}
	if text(r, "request_id") != requestID {
		return nil, catalogFailure("response-id-mismatch")
	}
	if text(r, "subtype") != "success" {
		return nil, catalogFailure("response-not-success")
	}
	for _, key := range []string{"pending_permission_requests", "pending_user_dialog_requests"} {
		if v, exists := r[key]; exists {
			var entries []json.RawMessage
			if json.Unmarshal(v, &entries) != nil || string(v) == "null" || len(entries) != 0 {
				return nil, catalogError()
			}
		}
	}
	result, err := object(r["response"])
	if err != nil {
		return nil, catalogError()
	}
	var entries []json.RawMessage
	if json.Unmarshal(result["models"], &entries) != nil || len(entries) == 0 || len(entries) > 256 {
		return nil, catalogError()
	}
	models := make([]Model, 0, len(entries))
	seen := map[string]bool{}
	for _, raw := range entries {
		entry, err := object(raw)
		if err != nil {
			return nil, catalogError()
		}
		m := Model{ID: text(entry, "value"), Name: text(entry, "displayName"), EffortState: "unknown"}
		if !modelToken(m.ID) || !catalogText(m.Name, 256) || m.Name == "" || seen[m.ID] {
			return nil, catalogError()
		}
		seen[m.ID], m.Default = true, m.ID == "default"
		if v, ok := entry["resolvedModel"]; ok {
			if json.Unmarshal(v, &m.ResolvedID) != nil || !modelToken(m.ResolvedID) {
				return nil, catalogError()
			}
		}
		if v, ok := entry["disabled"]; ok {
			if string(v) != "true" && string(v) != "false" {
				return nil, catalogError()
			}
			m.Unavailable = string(v) == "true"
		}
		support, present := entry["supportsEffort"]
		if present && string(support) != "true" && string(support) != "false" {
			return nil, catalogError()
		}
		if v, ok := entry["supportedEffortLevels"]; ok {
			if json.Unmarshal(v, &m.Efforts) != nil || string(v) == "null" || len(m.Efforts) > 16 {
				return nil, catalogError()
			}
			levels := map[string]bool{}
			for _, level := range m.Efforts {
				if !modelToken(level) || len(level) > 32 || levels[level] {
					return nil, catalogError()
				}
				levels[level] = true
			}
		}
		if present && string(support) == "false" {
			if len(m.Efforts) != 0 {
				return nil, catalogError()
			}
			m.EffortState = "unsupported"
		} else if present && len(m.Efforts) > 0 {
			m.EffortState = "supported"
		}
		models = append(models, m)
	}
	return models, nil
}

func catalogText(s string, limit int) bool {
	if len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
func modelToken(s string) bool {
	return s != "" && !strings.HasPrefix(s, "-") && catalogText(s, 256) && strings.IndexFunc(s, unicode.IsSpace) < 0
}

// ResolveModel gives an exact advertised ID precedence. A wire-ID fallback is
// usable only when every alias describing that wire ID agrees on capabilities.
func ResolveModel(models []Model, id string) (Model, error) {
	for _, m := range models {
		if m.ID == id {
			return m, nil
		}
	}
	var selected Model
	found := false
	for _, m := range models {
		if m.ResolvedID == "" || m.ResolvedID != id {
			continue
		}
		if found && !sameCapabilities(selected, m) {
			return Model{}, fail("claude_model_ambiguous", "Native aliases disagree about the selected exact model's capabilities. Refresh the catalog; no alias was chosen.", 5)
		}
		selected, found = m, true
	}
	if found {
		return selected, nil
	}
	return Model{}, fail("claude_model", "The selected model is absent from the fresh native catalog. No alias or model was substituted.", 5)
}
func sameCapabilities(a, b Model) bool {
	if a.ResolvedID != b.ResolvedID || a.EffortState != b.EffortState || a.Unavailable != b.Unavailable || len(a.Efforts) != len(b.Efforts) {
		return false
	}
	seen := map[string]bool{}
	for _, e := range a.Efforts {
		seen[e] = true
	}
	for _, e := range b.Efforts {
		if !seen[e] {
			return false
		}
	}
	return true
}
func selectedModel(models []Model, route core.Route) (Model, string, error) {
	m, err := ResolveModel(models, strings.TrimPrefix(route.Model, "claude/"))
	if err != nil {
		return Model{}, "", err
	}
	if m.Unavailable || m.ResolvedID == "" || m.EffortState != "supported" {
		return Model{}, "", fail("claude_model", "The selected native model's resolution or effort capability is unavailable. Refresh its catalog; no model was substituted.", 5)
	}
	if native := mappedEffort(m.Efforts, route.Effort); native != "" {
		return m, native, nil
	}
	return Model{}, "", fail("claude_effort", "The selected native model does not advertise the requested effort. No lower effort was substituted.", 5)
}

// PublicEfforts maps only reviewed native levels. Max is the highest advertised
// level, not a guessed synonym; unknown future levels block max only.
func PublicEfforts(levels []string) []string {
	var result []string
	for _, public := range []string{"low", "medium", "high", "max"} {
		if mappedEffort(levels, public) != "" {
			result = append(result, public)
		}
	}
	return result
}
func mappedEffort(levels []string, public string) string {
	seen := map[string]bool{}
	unknown := false
	for _, level := range levels {
		switch level {
		case "low", "medium", "high", "xhigh", "max":
		default:
			unknown = true
		}
		seen[level] = true
	}
	if public == "max" {
		if unknown {
			return ""
		}
		for _, native := range []string{"max", "xhigh", "high", "medium", "low"} {
			if seen[native] {
				return native
			}
		}
		return ""
	}
	if (public == "low" || public == "medium" || public == "high") && seen[public] {
		return public
	}
	return ""
}
