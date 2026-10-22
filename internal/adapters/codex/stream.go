package codex

import (
	"encoding/json"
	"errors"
	"strings"

	"ucli.local/ucli/internal/core"
)

type agentItem struct {
	text     strings.Builder
	complete bool
}

type turnStream struct {
	thread, turn, active string
	items                map[string]*agentItem
	outputBytes          int
	native, tools        bool
	aux                  map[string]*auxItem
	errorKind            string
	willRetry            bool
	warningObserved      bool
}

type auxItem struct {
	kind     string
	complete bool
}

func newTurnStream(thread, turn string) *turnStream {
	return &turnStream{thread: thread, turn: turn, items: map[string]*agentItem{}, aux: map[string]*auxItem{}}
}

func setupEvent(e envelope, thread string) (err error) {
	defer func() { err = reviewedNotificationError(e.Method, err) }()
	f := objectFields(e.Params)
	if f == nil {
		return errors.New("Codex notification parameters are malformed")
	}
	if _, exists := f["threadId"]; exists && !equals(f, "threadId", thread) {
		return errors.New("Codex notification belongs to another thread")
	}
	if e.Method == "thread/started" {
		if !equals(objectFields(f["thread"]), "id", thread) {
			return errors.New("Codex thread notification identity mismatch")
		}
	} else if e.Method == "warning" || e.Method == "configWarning" || e.Method == "error" {
		return errors.New("Codex setup reported " + e.Method + " before an active turn was confirmed")
	} else if strings.HasPrefix(e.Method, "turn/") || strings.HasPrefix(e.Method, "item/") || e.Method == "model/rerouted" {
		return errors.New("Codex setup reported a warning, error or unexpected execution event")
	}
	return nil
}

func (s *turnStream) append(item *agentItem, delta string, emit func(string) error) error {
	if len(delta) > core.MaxOutputBytes-s.outputBytes {
		return errors.New("Codex output exceeds its limit")
	}
	if delta == "" {
		return nil
	}
	if emit(delta) != nil {
		return errors.New("Codex output consumer rejected delivery")
	}
	item.text.WriteString(delta)
	s.outputBytes += len(delta)
	return nil
}

// Agent text is streamed sequentially. Native user/reasoning items are tracked
// without displaying private payloads; command/file items require explicit full
// local permission. Rejection never implies native effects were undone.
func (s *turnStream) consume(e envelope, emit func(string) error) (state string, err error) {
	defer func() { err = reviewedNotificationError(e.Method, err) }()
	f := objectFields(e.Params)
	if f == nil {
		return "", errors.New("Codex notification parameters are malformed")
	}
	if e.Method == "warning" {
		if _, ok := textField(f, "message", maxFrame); !ok {
			return "", errors.New("Codex warning notification lacks a valid message")
		}
		if !absentNullable(f["threadId"]) && !equals(f, "threadId", s.thread) {
			return "", errors.New("Codex warning notification belongs to another or invalid thread")
		}
		if _, present := f["turnId"]; present && !equals(f, "turnId", s.turn) {
			return "", errors.New("Codex warning notification execution identity mismatch")
		}
		s.warningObserved = true
		return "", nil // Warning is informational, never a terminal receipt.
	}
	for key, want := range map[string]string{"threadId": s.thread, "turnId": s.turn} {
		if _, exists := f[key]; exists && !equals(f, key, want) {
			return "", errors.New("Codex notification execution identity mismatch")
		}
	}
	switch e.Method {
	case "thread/started":
		return "", setupEvent(e, s.thread)
	case "turn/started", "turn/completed":
		turn := objectFields(f["turn"])
		if !equals(f, "threadId", s.thread) || !equals(turn, "id", s.turn) {
			return "", errors.New("Codex turn identity mismatch")
		}
		var items []json.RawMessage
		if !arrayField(turn, "items", &items) {
			return "", errors.New("Codex turn items are malformed")
		}
		if e.Method == "turn/started" {
			if !equals(turn, "status", "inProgress") || !noTurnError(turn["error"]) || len(items) != 0 {
				return "", errors.New("Codex started event is inconsistent")
			}
			return "", nil
		}
		switch {
		case equals(turn, "status", "failed"):
			kind, err := turnErrorKind(turn["error"])
			if err != nil {
				return "", err
			}
			if kind == "other" && s.errorKind != "" && !s.willRetry && absentNullable(objectFields(turn["error"])["codexErrorInfo"]) {
				kind = s.errorKind
			}
			return "failed", confirmedTurnFailure(kind)
		case equals(turn, "status", "interrupted"):
			if !noTurnError(turn["error"]) {
				return "", errors.New("Codex interrupted terminal is inconsistent")
			}
			return "interrupted", nil
		case equals(turn, "status", "completed"):
			if !noTurnError(turn["error"]) || s.active != "" || s.outputBytes == 0 {
				return "", errors.New("Codex completion lacks settled nonempty output")
			}
			for _, item := range s.aux {
				if !item.complete {
					return "", errors.New("Codex completion has an unsettled native item")
				}
			}
			// Some app-server terminal turns omit populated items. Completion still
			// requires each observed message's item/completed receipt. When items
			// are supplied, they must exactly agree with those receipts.
			if len(items) != 0 {
				seen := map[string]bool{}
				for _, raw := range items {
					item := objectFields(raw)
					id, ok := textField(item, "id", 256)
					if auxiliary := s.aux[id]; auxiliary != nil {
						if !ok || seen[id] || !auxiliary.complete || !equals(item, "type", auxiliary.kind) {
							return "", errors.New("Codex terminal native items conflict with observed execution")
						}
						seen[id] = true
						continue
					}
					known := s.items[id]
					if !ok || known == nil || seen[id] || !known.complete || !equals(item, "type", "agentMessage") || !equals(item, "text", known.text.String()) {
						return "", errors.New("Codex terminal items conflict with completed output")
					}
					seen[id] = true
				}
				if len(seen) != len(s.items)+len(s.aux) {
					return "", errors.New("Codex terminal items omit observed output")
				}
			}
			return "complete", nil
		default:
			return "", errors.New("Codex terminal status is unsupported")
		}
	case "item/started", "item/completed", "item/agentMessage/delta":
		if !equals(f, "threadId", s.thread) || !equals(f, "turnId", s.turn) {
			return "", errors.New("Codex item execution identity is missing or mismatched")
		}
		if e.Method == "item/agentMessage/delta" {
			id, ok := textField(f, "itemId", 256)
			delta, textOK := textField(f, "delta", core.MaxOutputBytes)
			item := s.items[id]
			if !ok || !textOK || item == nil || item.complete || s.active != id {
				return "", errors.New("Codex delta has no matching active message")
			}
			return "", s.append(item, delta, emit)
		}
		item := objectFields(f["item"])
		id, ok := textField(item, "id", 256)
		kind, kindOK := textField(item, "type", 64)
		if s.native && kindOK && kind != "agentMessage" {
			allowed := kind == "userMessage" || kind == "reasoning" || (s.tools && (kind == "commandExecution" || kind == "fileChange"))
			if !allowed || !ok || !validID(id) || s.items[id] != nil {
				return "", errors.New("Codex native item is outside the selected capability envelope")
			}
			if e.Method == "item/started" {
				if s.aux[id] != nil || len(s.items)+len(s.aux) >= 256 {
					return "", errors.New("Codex native item is duplicate or exceeds its bound")
				}
				s.aux[id] = &auxItem{kind: kind}
				return "", nil
			}
			known := s.aux[id]
			if known == nil || known.complete || known.kind != kind {
				return "", errors.New("Codex native item completion has no matching start")
			}
			if (kind == "commandExecution" || kind == "fileChange") && !equals(item, "status", "completed") && !equals(item, "status", "failed") && !equals(item, "status", "declined") {
				return "", errors.New("Codex tool item did not report a terminal status")
			}
			known.complete = true
			return "", nil
		}
		text, textOK := textField(item, "text", core.MaxOutputBytes)
		if !ok || !validID(id) || !textOK || !equals(item, "type", "agentMessage") {
			return "", errors.New("Codex item is outside the text-only fixture envelope")
		}
		if e.Method == "item/started" {
			if s.items[id] != nil || s.active != "" {
				return "", errors.New("Codex item is duplicate or interleaved")
			}
			s.items[id], s.active = &agentItem{}, id
			return "", s.append(s.items[id], text, emit)
		}
		known := s.items[id]
		if known == nil || known.complete || s.active != id || !strings.HasPrefix(text, known.text.String()) {
			return "", errors.New("Codex completed item conflicts with streamed output")
		}
		if err := s.append(known, text[known.text.Len():], emit); err != nil {
			return "", err
		}
		known.complete, s.active = true, ""
		return "", nil
	case "model/rerouted":
		return "", errors.New("Codex reported a primary model change; the pinned route is no longer confirmed")
	case "error":
		return "", s.errorNotification(f)
	case "configWarning":
		return "", errors.New("Codex reported " + e.Method + " outside the admitted execution envelope")
	default:
		if handled, err := s.nativeNotification(e.Method, f); handled {
			return "", err
		}
		if s.native {
			kind := ""
			switch e.Method {
			case "item/reasoning/summaryPartAdded", "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
				kind = "reasoning"
			case "item/commandExecution/outputDelta":
				if s.tools {
					kind = "commandExecution"
				}
			case "item/fileChange/outputDelta":
				if s.tools {
					kind = "fileChange"
				}
			case "turn/diff/updated":
				if s.tools {
					return "", nil
				}
			}
			if kind != "" {
				id, ok := textField(f, "itemId", 256)
				known := s.aux[id]
				if !ok || !equals(f, "threadId", s.thread) || !equals(f, "turnId", s.turn) || known == nil || known.complete || known.kind != kind {
					return "", errors.New("Codex native detail has no matching active item")
				}
				return "", nil // Payloads may be private; never emit/store tool or reasoning details.
			}
		}
		if strings.HasPrefix(e.Method, "item/") || strings.HasPrefix(e.Method, "turn/") {
			return "", errors.New("Codex execution event is outside the text-only fixture envelope")
		}
		// Additive metadata cannot settle execution or alter the requested route.
		return "", nil
	}
}

// Turn.error is optional and nullable in the reviewed native schema. Absence
// means no error only for this field; failed terminals still require an object.
func noTurnError(raw json.RawMessage) bool { return len(raw) == 0 || isNull(raw) }
