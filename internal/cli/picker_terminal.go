package cli

import (
	"context"
	"io"
	"strings"
)

// The picker consumes semantic events, leaving terminal modes, sanitization and
// screen restoration to the shared terminal implementation.
type pickerTerminal interface {
	next(context.Context) (keyEvent, error)
	paint(terminalFrame) error
}

func (s *session) chooseTerminal(ctx context.Context, title string, rows []choice, current string) (string, error) {
	return pickTerminal(ctx, s.terminal, title, rows, current)
}

func pickTerminal(ctx context.Context, terminal pickerTerminal, title string, rows []choice, current string) (string, error) {
	return pickTerminalRemember(ctx, terminal, title, rows, current, nil)
}

// Kept only for a continuing local drilldown, never persisted as a selection.
type pickerMemory struct {
	editor  textEditor
	focused string
	top     int
}

func pickTerminalRemember(ctx context.Context, terminal pickerTerminal, title string, rows []choice, current string, memory *pickerMemory) (string, error) {
	options := make([]pickerOption, 0, len(rows))
	for _, row := range rows {
		label, detail, found := strings.Cut(row.label, " - ")
		if !found {
			label, detail, _ = strings.Cut(row.label, " | ")
		}
		options = append(options, pickerOption{Value: row.value, Label: label, Detail: detail, SearchText: row.label})
	}
	picker := newPicker(title, options, current)
	var editor textEditor
	if memory != nil {
		editor = memory.editor
		picker.query, picker.top = editor.text, memory.top
		if memory.focused != "" {
			picker.focused = memory.focused
		}
		picker.reconcileFocus()
		defer func() { *memory = pickerMemory{editor, picker.focused, picker.top} }()
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		width, height := 80, 24
		if sized, ok := terminal.(interface{ size() (int, int) }); ok {
			width, height = sized.size()
		}
		view := pickerTerminalFrame(picker, editor, width, height)
		if err := terminal.paint(view); err != nil {
			return "", err
		}
		event, err := terminal.next(ctx)
		if err != nil {
			return "", err
		}
		var action pickerAction
		switch event.kind {
		case keyChoice:
			if !pickerViewportUsable(width, height) {
				continue
			}
			for _, index := range picker.matches() {
				if picker.options[index].Value == event.text {
					return event.text, nil
				}
			}
			continue
		case keyEnter:
			if !pickerViewportUsable(width, height) {
				continue
			}
			action.Kind = pickerConfirm
		case keyEscape:
			action.Kind = pickerCancel
		case keyUp:
			action.Kind = pickerMoveUp
		case keyDown:
			action.Kind = pickerMoveDown
		case keyEOF:
			return "", io.EOF
		case keyInterrupt:
			return "", context.Canceled
		case keyText, keyPaste, keyBackspace, keyDelete, keyLeft, keyRight, keyHome, keyEnd, keyClear, keyKillEnd:
			if event.kind == keyPaste {
				// A multiline paste is literal search text, never menu commands.
				// Keep it on one search row without changing the composer draft.
				event.text = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(event.text, "\r\n", " "), "\r", " "), "\n", " ")
			}
			if err := editor.apply(event); err != nil {
				return "", err
			}
			action = pickerAction{Kind: pickerReplaceQuery, Text: editor.text}
		default:
			continue // Resize repaints; unsupported keys do not commit a choice.
		}
		var outcome pickerOutcome
		picker, outcome = picker.reduce(action)
		if outcome.Confirmed {
			return outcome.Value, nil
		}
		if outcome.Canceled {
			return "", nil
		}
	}
}

func pickerTerminalFrame(picker pickerState, editor textEditor, width, height int) terminalFrame {
	budget := max(1, height-1) // The shared renderer reserves its last terminal row.
	if !pickerViewportUsable(width, height) {
		lines := []terminalLine{{Text: "Resize terminal"}, {Text: "Esc cancel", Dim: true}}
		if budget == 1 {
			lines = []terminalLine{{Text: "Resize / Esc"}}
		}
		return terminalFrame{Lines: lines, CursorRow: -1}
	}
	compact := height < 7
	rows := min(pickerVisibleRows, max(1, budget-6))
	if compact {
		rows = min(pickerVisibleRows, max(1, budget-2))
	}
	frame := picker.frame(rows)
	prefix := "Search: "
	if width < 16 {
		prefix = "? "
	}
	query, column := editorLineWindow(editor.text, editor.cursor, max(1, width-cellWidth(prefix)-1))
	frame.Lines[frame.SearchRow].Text = prefix + query
	view := terminalFrame{CursorRow: frame.SearchRow, CursorCol: cellWidth(prefix) + column}
	appendLine := func(line pickerLine) {
		view.Lines = append(view.Lines, terminalLine{Text: line.Text, Focused: line.Focused, Dim: line.Dim})
	}
	if !compact {
		appendLine(frame.Lines[0])
		appendLine(frame.Lines[1])
	} else {
		view.CursorRow = 0
	}
	appendLine(frame.Lines[frame.SearchRow])
	count := max(1, min(rows, len(picker.matches()))) // Empty results have a recovery row.
	optionsEnd := frame.SearchRow + 1 + count
	matches := picker.matches()
	top := pickerWindow(picker.top, picker.focusIndex(matches), len(matches), rows)
	for position, line := range frame.Lines[frame.SearchRow+1 : optionsEnd] {
		appendLine(line)
		if top+position < len(matches) {
			view.Lines[len(view.Lines)-1].Action = "choice:" + picker.options[matches[top+position]].Value
		}
	}
	if !compact {
		// Prefer the focused candidate's detail over a page count when only one
		// optional row fits. Focus, current scope and controls always come first.
		for index := len(frame.Lines) - 2; index >= optionsEnd && len(view.Lines) < budget-1; index-- {
			appendLine(frame.Lines[index])
		}
	}
	footer := frame.Lines[len(frame.Lines)-1]
	if compact {
		footer.Text = "Enter select | Esc cancel | Up/Down"
	}
	if width < 40 {
		footer.Text = "Enter / Esc"
	}
	appendLine(footer)
	return view
}

func pickerViewportUsable(width, height int) bool { return width >= 16 && height >= 4 }
