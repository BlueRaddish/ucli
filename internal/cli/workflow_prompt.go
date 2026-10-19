package cli

import (
	"context"
	"io"
	"strings"
)

// A file path is data; it is never a shell command, prompt or task selection.
// The caller keeps the composition and current task while this small view opens.
func (s *session) promptWorkflowFile(ctx context.Context) (string, error) {
	if s.terminal == nil {
		_ = s.out.text("Workflow plan file (empty returns unchanged) > ")
		answer := s.input.next(ctx)
		if answer.err != nil {
			return "", answer.err
		}
		return answer.text, nil
	}
	editor := textEditor{}
	for {
		width, height := s.terminal.size()
		usable := pickerViewportUsable(width, height)
		path, col := editorLineWindow(editor.text, editor.cursor, max(1, width-4))
		lines := []terminalLine{{Text: "Inspect workflow plan file"}, {Text: "Viewing starts no work. Relative paths use this task's directory.", Dim: true}, {Text: "> " + path}, {Text: "Enter open | Esc back | Type or paste a path", Dim: true}}
		row := 2
		if height < 6 {
			lines = lines[2:]
			row = 0
		}
		if !usable {
			lines = []terminalLine{{Text: "Resize terminal | Esc back"}}
			row = -1
		}
		if err := s.terminal.paint(terminalFrame{Lines: lines, CursorRow: row, CursorCol: col + 2}); err != nil {
			return "", err
		}
		event, err := s.terminal.next(ctx)
		if err != nil {
			return "", err
		}
		switch event.kind {
		case keyEscape:
			return "", nil
		case keyEOF:
			return "", io.EOF
		case keyEnter:
			if !usable {
				continue
			}
			if strings.ContainsAny(editor.text, "\n\r") {
				_ = s.out.problem(usageError("choose one workflow file path"))
				continue
			}
			return editor.text, nil
		default:
			if !usable {
				continue
			}
			if err := editor.apply(event); err != nil {
				_ = s.out.problem(err)
			}
		}
	}
}
