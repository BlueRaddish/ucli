package codex

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Only reviewed protocol literals may enter diagnostics. Native method names
// and payloads can contain private data; an unrecognized name stays withheld.
func reviewedNotificationError(method string, err error) error {
	if err == nil {
		return nil
	}
	var known string
	switch method {
	case "turn/plan/updated":
		known = "turn/plan/updated"
	case "turn/moderationMetadata":
		known = "turn/moderationMetadata"
	case "item/plan/delta":
		known = "item/plan/delta"
	case "item/commandExecution/terminalInteraction":
		known = "item/commandExecution/terminalInteraction"
	case "item/fileChange/patchUpdated":
		known = "item/fileChange/patchUpdated"
	case "item/mcpToolCall/progress":
		known = "item/mcpToolCall/progress"
	case "item/autoApprovalReview/started":
		known = "item/autoApprovalReview/started"
	case "item/autoApprovalReview/completed":
		known = "item/autoApprovalReview/completed"
	}
	if known == "" {
		return err
	}
	return fmt.Errorf("Codex notification %s was rejected: %w", known, err)
}

// These schemas are pinned to openai/codex rust-v0.162.1:
// app-server-protocol/src/protocol/v2/{turn,model,item}.rs and its generated
// schema/json/v2 notifications. The emitter binds thread/turn before delivery.
// These are observations, never output, authority, or terminal evidence.
func (s *turnStream) nativeNotification(method string, f map[string]json.RawMessage) (bool, error) {
	if !s.native {
		return false, nil
	}
	switch method {
	case "turn/plan/updated", "turn/moderationMetadata",
		"item/commandExecution/terminalInteraction", "item/fileChange/patchUpdated":
	default:
		return false, nil
	}
	if !equals(f, "threadId", s.thread) || !equals(f, "turnId", s.turn) {
		return true, errors.New("Codex metadata execution identity is missing or mismatched")
	}
	switch method {
	case "turn/plan/updated":
		var steps []json.RawMessage
		if !arrayField(f, "plan", &steps) || len(steps) > 256 || !optionalDetailText(f, "explanation") {
			return true, errors.New("Codex plan metadata is malformed or exceeds its limit")
		}
		for _, raw := range steps {
			step := objectFields(raw)
			if _, ok := textField(step, "step", 64*1024); !ok || (!equals(step, "status", "pending") && !equals(step, "status", "inProgress") && !equals(step, "status", "completed")) {
				return true, errors.New("Codex plan step is malformed or exceeds its limit")
			}
		}
		return true, nil
	case "turn/moderationMetadata":
		// JsonValue includes null, scalars and containers. Require presence but
		// retain none; the transport additionally caps frame size and JSON depth.
		value, present := f["metadata"]
		if !present || len(value) >= maxFrame || !json.Valid(value) {
			return true, errors.New("Codex moderation metadata is missing or malformed")
		}
		return true, nil
	}
	if !s.tools {
		return true, errors.New("Codex tool detail requires explicit full local permission")
	}
	kind := "commandExecution"
	if method == "item/fileChange/patchUpdated" {
		kind = "fileChange"
	}
	id, ok := textField(f, "itemId", 256)
	known := s.aux[id]
	if !ok || known == nil || known.complete || known.kind != kind {
		return true, errors.New("Codex native detail has no matching active item")
	}
	if kind == "commandExecution" {
		processID, processOK := textField(f, "processId", 256)
		_, inputOK := textField(f, "stdin", maxFrame)
		if !processOK || !validID(processID) || !inputOK {
			return true, errors.New("Codex terminal interaction detail is malformed")
		}
		return true, nil
	}
	var changes []json.RawMessage
	if !arrayField(f, "changes", &changes) || len(changes) > 256 {
		return true, errors.New("Codex patch metadata is malformed or exceeds its limit")
	}
	for _, raw := range changes {
		change := objectFields(raw)
		_, pathOK := textField(change, "path", 64*1024)
		_, diffOK := textField(change, "diff", maxFrame)
		changeKind := objectFields(change["kind"])
		validKind := equals(changeKind, "type", "add") || equals(changeKind, "type", "delete") ||
			(equals(changeKind, "type", "update") && optionalDetailText(changeKind, "move_path"))
		if !pathOK || !diffOK || !validKind {
			return true, errors.New("Codex patch change detail is malformed")
		}
	}
	return true, nil
}

func optionalDetailText(f map[string]json.RawMessage, key string) bool {
	if absentNullable(f[key]) {
		return true
	}
	_, ok := textField(f, key, 64*1024)
	return ok
}
