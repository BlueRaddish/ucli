package cli

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Completion is an editor preview, never a command dispatcher. Rows come from
// the same supported-command registry as help; finder-local and unavailable
// actions therefore cannot accidentally become global suggestions.
type terminalCompletion struct {
	query     string
	rows      []slashSpec
	focus     int
	eligible  bool
	dismissed bool
}

type completionResult struct {
	Rows    []slashSpec
	Focus   int  // -1 when there is no selectable row
	Visible bool // includes an eligible query with no matches
}

func (c *terminalCompletion) sync(text string, cursor int, literal bool) {
	if text != c.query {
		c.query, c.focus, c.dismissed = text, 0, false
	}
	// Only complete the entire initial token. Editing before its end, arguments,
	// multiline input and literal paste retain their ordinary editor behavior.
	c.eligible = !literal && cursor == len(text) && strings.HasPrefix(text, "/") && utf8.ValidString(text)
	if c.eligible && strings.IndexFunc(text, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		c.eligible = false
	}
	if !c.eligible {
		return
	}
	// This bounded local registry needs neither asynchronous search nor a cache.
	c.rows = c.rows[:0]
	for _, spec := range slashCommands {
		if strings.HasPrefix(spec.Name, text) {
			c.rows = append(c.rows, spec)
		}
	}
	if c.focus >= len(c.rows) {
		c.focus = 0
	}
}

func (c *terminalCompletion) move(delta int) {
	if !c.eligible || c.dismissed || len(c.rows) == 0 {
		return
	}
	count := len(c.rows)
	c.focus = (c.focus + delta%count + count) % count
}

func (c *terminalCompletion) dismiss() {
	if c.eligible {
		c.dismissed = true
	}
}

func (c terminalCompletion) result() completionResult {
	if !c.eligible || c.dismissed {
		return completionResult{Focus: -1}
	}
	result := completionResult{Rows: append([]slashSpec(nil), c.rows...), Focus: -1, Visible: true}
	if len(result.Rows) > 0 {
		result.Focus = c.focus
	}
	return result
}

func (c terminalCompletion) accept() (string, bool) {
	if !c.eligible || c.dismissed || len(c.rows) == 0 {
		return "", false
	}
	return c.rows[c.focus].Name + " ", true
}
