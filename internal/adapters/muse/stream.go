package muse

import (
	"encoding/json"
	"errors"
	"strings"
)

type item struct {
	ID        string `json:"itemId"`
	Turn      string `json:"turnId"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Text      string `json:"text"`
	Revision  int    `json:"revision"`
	Truncated bool   `json:"truncated"`
	CutOff    bool   `json:"cutOff"`
}

type itemState struct {
	item
	complete bool
}

type turnStream struct {
	session, turn, expected string
	items                   map[string]*itemState
	cursors                 map[string]bool
	output                  strings.Builder
}

func newTurnStream(session, turn, input string) *turnStream {
	return &turnStream{session: session, turn: turn, expected: "echo: " + input, items: map[string]*itemState{}, cursors: map[string]bool{}}
}

// consume trusts neither a session ID alone nor the client's currently active
// turn. Muse deltas name an item, whose full object establishes turn ownership.
func (s *turnStream) consume(e envelope, emit func(string) error) (string, error) {
	switch e.Method {
	case "item/started", "item/updated", "item/completed", "item/delta", "turn/completed", "turn/started":
	default:
		return "", nil // Additive unrelated notifications do not complete a turn.
	}
	var p struct {
		Session  string  `json:"sessionId"`
		Turn     string  `json:"turnId"`
		Cursor   string  `json:"viewCursor"`
		Item     item    `json:"item"`
		ItemID   string  `json:"itemId"`
		Field    string  `json:"field"`
		Delta    *string `json:"delta"`
		Terminal string  `json:"terminal"`
	}
	if json.Unmarshal(e.Params, &p) != nil || p.Session != s.session || p.Cursor == "" {
		return "", errors.New("native event has invalid session or cursor")
	}
	if s.cursors[p.Cursor] {
		return "", errors.New("duplicate native view cursor")
	}
	s.cursors[p.Cursor] = true
	if e.Method == "turn/started" {
		if p.Turn != s.turn {
			return "", errors.New("native turn start identity mismatch")
		}
		return "", nil
	}
	if e.Method == "turn/completed" {
		if p.Turn != s.turn {
			return "", errors.New("native terminal turn identity mismatch")
		}
		switch p.Terminal {
		case "cancelled":
			return "interrupted", nil
		case "failed":
			return "failed", nil
		case "completed":
			for _, it := range s.items {
				if it.Kind == "agentMessage" && !it.complete {
					return "", errors.New("native turn completed before agent item settled")
				}
			}
			if s.output.Len() == 0 || s.output.String() != s.expected {
				return "", errors.New("native completion lacks the expected genuine echo output")
			}
			return "complete", nil
		default:
			return "", errors.New("unknown native terminal state")
		}
	}
	if e.Method == "item/delta" {
		it, exists := s.items[p.ItemID]
		if !exists || it.complete || p.Delta == nil {
			return "", errors.New("native delta has no open owned item")
		}
		if it.Kind != "agentMessage" {
			return "", nil
		}
		if p.Field != "" && p.Field != "text" {
			return "", errors.New("unsupported agent message field")
		}
		if len(it.Text)+len(*p.Delta) > maxFrame {
			return "", errors.New("echo output exceeds limit")
		}
		it.Text += *p.Delta
		return "", s.append(*p.Delta, emit)
	}
	value := p.Item
	if value.ID == "" || value.Turn != s.turn || value.Kind == "" || value.Status == "" || value.Revision < 1 {
		return "", errors.New("native item has invalid identity or state")
	}
	previous, exists := s.items[value.ID]
	if exists && (previous.Kind != value.Kind || previous.complete || value.Revision <= previous.Revision) {
		return "", errors.New("native item revision or kind is inconsistent")
	}
	if e.Method == "item/started" && (exists || value.Status != "inProgress") {
		return "", errors.New("invalid native item start")
	}
	complete := e.Method == "item/completed"
	if complete && value.Status == "inProgress" {
		return "", errors.New("native completed item is still in progress")
	}
	if value.Kind == "agentMessage" {
		if value.Truncated || value.CutOff || (complete && value.Status != "completed") {
			return "", errors.New("native agent output is incomplete")
		}
		prior := ""
		if exists {
			prior = previous.Text
		}
		if !strings.HasPrefix(value.Text, prior) {
			return "", errors.New("native final text disagrees with streamed output")
		}
		if err := s.append(value.Text[len(prior):], emit); err != nil {
			return "", err
		}
	}
	s.items[value.ID] = &itemState{item: value, complete: complete}
	return "", nil
}

func (s *turnStream) append(text string, emit func(string) error) error {
	if s.output.Len()+len(text) > maxFrame {
		return errors.New("echo output exceeds limit")
	}
	if text == "" {
		return nil
	}
	if err := emit(text); err != nil {
		return err
	}
	s.output.WriteString(text)
	return nil
}
