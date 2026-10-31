package cli

import (
	"strings"
	"unicode/utf8"

	"ucli.local/ucli/internal/core"
)

// attemptEvent scopes only the rich display. The durable event, JSON wire format
// and plain foreground text retain their existing behavior.
func (o *output) attemptEvent(event core.Event, model string) error {
	if o.terminal != nil && !o.json && event.Type != "message_delta" && validTranscriptScopeID(event.TaskID) && validTranscriptScopeID(event.AttemptID) {
		if err := o.jobEvent(event, model); err != nil {
			return err
		}
		switch event.Type {
		case "accepted", "status", "result_persisted":
			return nil // The structured job surface owns progress, not chat text.
		}
	}
	if o.terminal == nil || o.json || event.Type != "message_delta" {
		return o.event(event)
	}
	if !validTranscriptScopeID(event.TaskID) || !validTranscriptScopeID(event.AttemptID) {
		return core.Problem("display_scope", "A task and attempt identity are required for scoped output.", 9)
	}
	// Keep a short session tag: two concurrent tasks can use the same model.
	// The heading opens its job inspector; full identities stay out of the body.
	label := "Assistant · task " + inlineText(shortExecutionID(event.TaskID))
	if model != "" {
		label += " · " + clipCells(inlineText(model), 96)
	}
	message := terminalMessageMsg{Role: transcriptAssistant, Label: label, TaskID: event.TaskID, AttemptID: event.AttemptID}
	return o.write(terminalMessageWriter{o.terminal, message}, event.Text)
}

// Match the durable identity byte ceiling without truncating identity equality.
// The label is separately escaped; these exact keys never become terminal text.
func validTranscriptScopeID(id string) bool {
	return len(id) > 0 && len(id) <= 256 && utf8.ValidString(id) && !strings.ContainsAny(id, "\x00\r\n\t")
}

func shortExecutionID(id string) string {
	if len(id) > 12 {
		return id[len(id)-12:]
	}
	return id
}
