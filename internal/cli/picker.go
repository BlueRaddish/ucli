package cli

import (
	"fmt"
	"strings"
)

const pickerVisibleRows = 6

// Picker state is a local preview. Only a confirmed outcome asks its caller to
// change the selected setting/task; navigation and cancellation never do so.
type pickerOption struct {
	Value, Label, Detail, SearchText string
}

type pickerActionKind uint8

const (
	pickerMoveUp pickerActionKind = iota
	pickerMoveDown
	pickerHome
	pickerEnd
	pickerReplaceQuery
	pickerConfirm
	pickerCancel
)

type pickerAction struct {
	Kind pickerActionKind
	Text string
}

type pickerOutcome struct {
	Value               string
	Confirmed, Canceled bool
}

type pickerState struct {
	title, current, query, focused string
	options                        []pickerOption
	top                            int
	done                           bool
}

type pickerLine struct {
	Text         string
	Focused, Dim bool
}

// The terminal screen owns grapheme/cell measurement, clipping, sanitization and
// trusted ANSI highlighting. These lines contain semantic text, never styling
// escape sequences. SearchRow lets the shared editor restore its query caret.
type pickerFrame struct {
	Lines     []pickerLine
	SearchRow int
}

func newPicker(title string, options []pickerOption, current string) pickerState {
	p := pickerState{title: title, current: current, options: append([]pickerOption(nil), options...)}
	p.reconcileFocus()
	return p
}

func (p pickerState) matches() []int {
	query := strings.ToLower(p.query)
	visible := make([]int, 0, len(p.options))
	// Local catalogs and task pages are bounded. This linear substring filter is
	// intentionally simple; whole-archive task search stays in the app/store.
	for i, option := range p.options {
		text := option.Value + " " + option.Label + " " + option.Detail + " " + option.SearchText
		if strings.Contains(strings.ToLower(text), query) {
			visible = append(visible, i)
		}
	}
	return visible
}

func (p pickerState) focusIndex(visible []int) int {
	for i, index := range visible {
		if p.options[index].Value == p.focused {
			return i
		}
	}
	return -1
}

func (p *pickerState) reconcileFocus() {
	visible := p.matches()
	if p.focusIndex(visible) < 0 {
		p.focused = ""
		for _, index := range visible {
			if p.options[index].Value == p.current {
				p.focused = p.current
				break
			}
		}
		if p.focused == "" && len(visible) > 0 {
			p.focused = p.options[visible[0]].Value
		}
	}
	p.top = pickerWindow(p.top, p.focusIndex(visible), len(visible), pickerVisibleRows)
}

func pickerWindow(top, focus, count, rows int) int {
	top = min(max(top, 0), max(count-rows, 0))
	if focus >= 0 && focus < top {
		top = focus
	} else if focus >= top+rows {
		top = focus - rows + 1
	}
	return top
}

// Query replacement deliberately consumes the shared line editor's full value,
// so grapheme movement, deletion and protected paste have one implementation.
func (p pickerState) reduce(action pickerAction) (pickerState, pickerOutcome) {
	if p.done {
		return p, pickerOutcome{}
	}
	visible := p.matches()
	focus := p.focusIndex(visible)
	switch action.Kind {
	case pickerReplaceQuery:
		p.query = action.Text
		p.reconcileFocus()
		return p, pickerOutcome{}
	case pickerMoveUp:
		focus--
		if focus < 0 {
			focus = len(visible) - 1
		}
	case pickerMoveDown:
		focus++
		if focus >= len(visible) {
			focus = 0
		}
	case pickerHome:
		focus = 0
	case pickerEnd:
		focus = len(visible) - 1
	case pickerConfirm:
		if focus >= 0 && focus < len(visible) {
			p.done = true
			return p, pickerOutcome{Value: p.options[visible[focus]].Value, Confirmed: true}
		}
	case pickerCancel:
		p.done = true
		return p, pickerOutcome{Canceled: true}
	}
	if focus >= 0 && focus < len(visible) {
		p.focused = p.options[visible[focus]].Value
	}
	p.top = pickerWindow(p.top, focus, len(visible), pickerVisibleRows)
	return p, pickerOutcome{}
}

func (p pickerState) frame(maxRows int) pickerFrame {
	maxRows = min(max(maxRows, 1), pickerVisibleRows)
	current := p.current
	for _, option := range p.options {
		if option.Value == p.current {
			current = option.Label
			break
		}
	}
	if current == "" {
		current = "none"
	}
	frame := pickerFrame{SearchRow: 2, Lines: []pickerLine{
		{Text: p.title},
		{Text: "Current: " + current, Dim: true},
		{Text: "Search: " + p.query},
	}}
	visible := p.matches()
	focus := p.focusIndex(visible)
	top := pickerWindow(p.top, focus, len(visible), maxRows)
	if len(visible) == 0 {
		message := "No matches. Edit the search or Esc to return unchanged."
		if len(p.options) == 0 {
			message = "No choices available. Esc returns unchanged."
		}
		frame.Lines = append(frame.Lines, pickerLine{Text: message, Dim: true})
	}
	for position := top; position < min(top+maxRows, len(visible)); position++ {
		option := p.options[visible[position]]
		prefix := "  "
		if position == focus {
			prefix = "> "
		}
		text := prefix + option.Label
		if option.Value == p.current {
			text += " (current)"
		}
		frame.Lines = append(frame.Lines, pickerLine{Text: text, Focused: position == focus})
	}
	if len(visible) > maxRows {
		frame.Lines = append(frame.Lines, pickerLine{Text: fmt.Sprintf("Showing %d-%d of %d matches", top+1, min(top+maxRows, len(visible)), len(visible)), Dim: true})
	}
	if focus >= 0 && p.options[visible[focus]].Detail != "" {
		frame.Lines = append(frame.Lines, pickerLine{Text: p.options[visible[focus]].Detail, Dim: true})
	}
	frame.Lines = append(frame.Lines, pickerLine{Text: "Up/Down move | Type to filter | Enter select | Esc cancel", Dim: true})
	return frame
}
