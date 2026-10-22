package claude

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const maxFrame = 1 << 20
const maxOutput = 4 << 20

func object(raw []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("not an object")
	}
	out := map[string]json.RawMessage{}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		lower := strings.ToLower(name)
		if err != nil || !ok || seen[lower] {
			return nil, errors.New("duplicate object key")
		}
		seen[lower] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, errors.New("invalid JSON value")
		}
		out[name] = value
	}
	if _, err = d.Token(); err != nil || d.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return out, nil
}
func text(f map[string]json.RawMessage, key string) string {
	var s string
	if json.Unmarshal(f[key], &s) != nil {
		return ""
	}
	return s
}
func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

type stream struct {
	session, permission, model, requestedModel, expectedModel string
	failure                                                   string
	initialized, terminal                                     bool
	state                                                     string
	textBytes                                                 int
	assistantText                                             bool
}

func (s *stream) consume(raw []byte, emit func(string) error) error {
	f, err := object(raw)
	if err != nil {
		return fail("claude_protocol", "Native Claude emitted invalid JSON.", 9)
	}
	kind := text(f, "type")
	if s.terminal {
		return fail("claude_protocol", "Native Claude emitted data after its final result.", 9)
	}
	if session := text(f, "session_id"); session != "" && session != s.session {
		return fail("claude_protocol", "Native Claude response belongs to another session.", 9)
	}
	switch kind {
	case "system":
		if strings.HasPrefix(text(f, "subtype"), "hook_") {
			return fail("claude_permission", "Native Claude reported a hook outside the requested configuration envelope.", 5)
		}
		if text(f, "subtype") != "init" {
			return nil
		}
		if s.initialized || text(f, "session_id") != s.session || text(f, "model") == "" {
			return fail("claude_protocol", "Native Claude session initialization did not match.", 9)
		}
		if s.expectedModel == "" || s.expectedModel != text(f, "model") {
			return fail("claude_model_changed", "Native Claude initialized a model different from the selected catalog resolution.", 5)
		}
		expected := "dontAsk"
		if s.permission == "sandbox" {
			expected = "bypassPermissions"
		}
		if text(f, "permissionMode") != expected {
			return fail("claude_permission", "Native Claude did not confirm the requested tool permission mode.", 5)
		}
		var tools, mcp []json.RawMessage
		if bytes.Equal(f["tools"], []byte("null")) || bytes.Equal(f["mcp_servers"], []byte("null")) || json.Unmarshal(f["tools"], &tools) != nil || json.Unmarshal(f["mcp_servers"], &mcp) != nil || len(mcp) != 0 || s.permission == "read-only" && len(tools) != 0 {
			return fail("claude_permission", "Native Claude exposed tools outside the requested envelope.", 5)
		}
		s.initialized = true
		s.model = text(f, "model")
	case "assistant":
		if !s.initialized || text(f, "session_id") != s.session {
			return fail("claude_protocol", "Native assistant output preceded session confirmation.", 9)
		}
		message, err := object(f["message"])
		if err != nil {
			return fail("claude_protocol", "Native assistant message was malformed.", 9)
		}
		nativeModel := text(message, "model")
		if nativeModel != "" && nativeModel != "<synthetic>" && nativeModel != s.model {
			return fail("claude_model_changed", "Native Claude changed models during the selected turn.", 5)
		}
		diagnostic := text(f, "error") != "" || nativeModel == "<synthetic>"
		if diagnostic {
			s.failure = classifyFailure(text(f, "error"), s.failure)
		}
		var blocks []json.RawMessage
		if json.Unmarshal(message["content"], &blocks) != nil {
			return fail("claude_protocol", "Native assistant content was malformed.", 9)
		}
		for _, rawBlock := range blocks {
			block, err := object(rawBlock)
			if err != nil {
				return fail("claude_protocol", "Native assistant block was malformed.", 9)
			}
			switch text(block, "type") {
			case "text":
				if diagnostic {
					s.failure = classifyFailure(text(block, "text"), s.failure)
					continue
				}
				if err := s.emit(text(block, "text"), emit); err != nil {
					return err
				}
				s.assistantText = true
			case "tool_use", "server_tool_use":
				if s.permission != "sandbox" {
					return fail("claude_permission", "A tool was requested during a no-tools turn.", 5)
				}
			case "thinking", "redacted_thinking": // Never render private reasoning or signatures.
			default:
				return fail("claude_protocol", "Unsupported native assistant content.", 9)
			}
		}
	case "user":
		// Tool replies and echoed user input may contain credentials or paths.
		// They are not forwarded into the user-facing text receipt.
		if !s.initialized || text(f, "session_id") != s.session {
			return fail("claude_protocol", "Native tool output session did not match.", 9)
		}
	case "result":
		if !s.initialized || text(f, "session_id") != s.session {
			return fail("claude_protocol", "Native final result session did not match.", 9)
		}
		var isError bool
		if bytes.Equal(f["is_error"], []byte("null")) || json.Unmarshal(f["is_error"], &isError) != nil {
			return fail("claude_protocol", "Native final result omitted its outcome.", 9)
		}
		if !isError && text(f, "subtype") == "success" {
			if !s.assistantText {
				if err := s.emit(text(f, "result"), emit); err != nil {
					return err
				}
			}
			s.state = "complete"
		} else if isError {
			s.failure = classifyFailure(text(f, "result"), s.failure)
			var messages []json.RawMessage
			if json.Unmarshal(f["errors"], &messages) == nil {
				remaining := 64 << 10
				for _, raw := range messages {
					if remaining <= 0 {
						break
					}
					var message string
					if json.Unmarshal(raw, &message) != nil {
						continue
					}
					if len(message) > remaining {
						message = message[:remaining]
					}
					remaining -= len(message)
					s.failure = classifyFailure(message, s.failure)
				}
			}
			if s.failure == "" && text(f, "subtype") == "error_max_turns" {
				s.failure = "turn_limit"
			}
			s.state = "failed"
		} else {
			return fail("claude_protocol", "Native final result had an unsupported outcome.", 9)
		}
		s.terminal = true
	case "rate_limit_event", "tool_progress", "tool_use_summary", "auth_status", "keep_alive": // Metadata only, never raw diagnostic text.
	default:
		return fail("claude_protocol", "Unsupported native Claude message type.", 9)
	}
	return nil
}

// Only an allowlisted category leaves this in-memory inspection. Native error
// prose can contain credentials/paths and must never become a durable message.
func classifyFailure(value, prior string) string {
	if prior != "" {
		return prior
	}
	if len(value) > 64<<10 {
		value = value[:64<<10]
	}
	v := strings.ToLower(value)
	has := func(needles ...string) bool {
		for _, needle := range needles {
			if strings.Contains(v, needle) {
				return true
			}
		}
		return false
	}
	switch {
	case has("authentication_failed", "oauth_org_not_allowed", "authentication failed", "not logged in", "invalid api key", "oauth token", "token has expired", "unauthorized", "401"):
		return "auth"
	case has("billing_error", "credit balance", "billing", "payment required", "extra usage"):
		return "billing"
	case has("rate_limit", "rate limit", "usage limit", "out of usage", "quota", "too many requests", "429"):
		return "quota"
	case has("model_not_found") || has("model") && has("not found", "does not exist", "not available", "unavailable", "not have access", "access denied"):
		return "model"
	case has("invalid_request", "invalid request"):
		return "request"
	case has("max_output_tokens"):
		return "output_limit"
	case has("overloaded", "server_error"):
		return "provider"
	}
	return ""
}

func (s *stream) failureError() error {
	switch s.failure {
	case "auth":
		return fail("claude_login", "Native Claude rejected authentication; check the linked native login before retrying.", 3)
	case "quota":
		return fail("claude_quota", "Native Claude reported a usage or rate limit; wait for capacity or explicitly choose another eligible account.", 4)
	case "billing":
		return fail("claude_billing", "Native Claude reported a billing or credit limit. UCLI has not enabled extra usage or changed billing settings.", 4)
	case "model":
		return fail("claude_model_unavailable", "The selected native Claude model is unavailable to this account; choose another model explicitly.", 3)
	case "request":
		return fail("claude_request_denied", "Native Claude rejected this request or configuration; inspect the selected capabilities.", 5)
	case "turn_limit":
		return fail("claude_turn_limit", "Native Claude reached the bounded turn limit; inspect the saved partial result before continuing.", 8)
	case "output_limit":
		return fail("claude_output_limit", "Native Claude reached its output limit; inspect the saved partial result before continuing.", 8)
	default:
		return fail("claude_turn_failed", "Native Claude reported a failed turn; inspect account/model availability.", 8)
	}
}
func (s *stream) emit(value string, emit func(string) error) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || s.textBytes+len(value) > maxOutput {
		return fail("claude_output", "Native Claude output exceeded its text bound.", 9)
	}
	s.textBytes += len(value)
	if value == "" {
		return nil
	}
	if err := emit(value); err != nil {
		return fail("claude_delivery", "Native Claude output could not be preserved by its consumer.", 9)
	}
	return nil
}
