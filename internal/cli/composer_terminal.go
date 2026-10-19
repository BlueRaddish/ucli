package cli

import (
	"context"
	"io"
	"strings"
)

// Keep the draft and picker independent: leaving a menu cannot change draft
// text, the selected task, or its optimistic revision.
func (s *session) readTerminalLine(ctx context.Context) line {
	return s.readTerminalLineWithAction(ctx, s.action)
}

// Keep action effects injectable for terminal state tests without native login.
func (s *session) readTerminalLineWithAction(ctx context.Context, action func(context.Context, string) (bool, error)) line {
	editor := textEditor{}
	literal := false
	for {
		if err := s.syncTerminalChrome(); err != nil {
			return line{text: editor.text, literal: true, err: err}
		}
		if s.recoveryID != "" {
			_ = s.out.notice("Draft conflict | choose /tasks or /new", toneWarning)
		}
		if err := s.terminal.paintComposer(s.composerFrame(editor)); err != nil {
			return line{text: editor.text, literal: true, err: err}
		}
		event, err := s.terminal.next(ctx)
		if err != nil {
			return line{text: editor.text, literal: true, err: err}
		}
		switch event.kind {
		case keyEnter:
			_ = s.terminal.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> "}, {Text: "Working | Ctrl+C interrupts", Dim: true}}, CursorRow: -1})
			_ = s.terminal.paint(terminalFrame{CursorRow: -1})
			return line{text: editor.text, complete: true, literal: literal}
		case keyEOF:
			return line{text: editor.text, literal: true, err: io.EOF}
		case keyEscape:
			// Esc leaves text intact. Discarding a composition is explicit Ctrl+U.
		case keyTab:
			actions := []choice{{"/models", "Models - search model choices"}, {"/accounts", "Accounts - choose an authorized account"}, {"/login", "Connect account - native sign-in"}, {"/effort", "Effort - choose reasoning intent"}, {"/permission", "Permission - choose the runtime envelope"}, {"/tasks", "Tasks - open saved work"}, {"/workflow", "Workflow - inspect a plan file"}, {"/voice", "Voice - dictate into this draft"}, {"/result", "Results - inspect saved attempts"}, {"/explain", "Explain - preview the next route"}, {"/help", "Help - show every action"}}
			value, err := s.chooseTerminal(ctx, "Actions — your composition is kept", actions, "")
			if err != nil {
				return line{text: editor.text, literal: true, err: err}
			}
			if value != "" {
				if value == "/voice" {
					text, err := s.voiceText(ctx, "")
					if err == nil {
						err = insertVoiceText(&editor, text)
						if text != "" {
							literal = true
						}
					}
					if err != nil {
						if ctx.Err() != nil || problemFor(err).Exit == 9 {
							return line{text: editor.text, literal: true, err: err}
						}
						_ = s.out.problem(err)
					}
					continue
				}
				if value == "/tasks" && editor.text != "" {
					_ = s.out.text("Press Enter to save this composition before switching tasks. Your text is kept here.\n")
					continue
				}
				_, err := action(ctx, value)
				if err != nil {
					if ctx.Err() != nil || problemFor(err).Exit == 9 {
						return line{text: editor.text, literal: true, err: err}
					}
					_ = s.out.problem(err)
				}
				if ctx.Err() != nil {
					return line{text: editor.text, literal: true, err: ctx.Err()}
				}
			}
		default:
			if event.kind == keyPaste {
				literal = true
			}
			if err := editor.apply(event); err != nil {
				_ = s.out.problem(err)
			}
			if editor.text == "" {
				literal = false
			}
		}
	}
}

func (s *session) composerFrame(editor textEditor) terminalFrame {
	width, height := s.terminal.composerSize()
	var lines []terminalLine
	editorLines, row, col := editorFrame(editor, max(1, width-1), max(1, min(6, height-len(lines)-2)))
	row += len(lines)
	lines = append(lines, editorLines...)
	if len(lines) < max(1, height-1) {
		lines = append(lines, terminalLine{Text: composerHint(width), Dim: true})
	}
	return terminalFrame{Lines: lines, CursorRow: row, CursorCol: col}
}

func composerHint(width int) string {
	for _, text := range []string{
		"Enter save · /send submit · Tab actions · Alt+Enter newline · Ctrl+C exit",
		"Enter save · /send submit · Tab actions · Ctrl+C exit",
		"Enter save · Tab actions · /send submit",
		"Enter save · Tab actions",
		"Tab actions",
	} {
		if cellWidth(text) < width {
			return text
		}
	}
	return ""
}

func editorFrame(editor textEditor, width, maxRows int) ([]terminalLine, int, int) {
	all := strings.Split(editor.text, "\n")
	before := editor.beforeCursor()
	current := strings.Count(before, "\n")
	column := len(before) - strings.LastIndexByte(before, '\n') - 1
	start := max(0, current-maxRows+1)
	end := min(len(all), start+maxRows)
	lines := make([]terminalLine, 0, end-start)
	cursorCol := 0
	for index := start; index < end; index++ {
		text := all[index]
		prefix := "> "
		if index != 0 {
			prefix = "  "
		}
		if index == current {
			text, cursorCol = editorLineWindow(text, column, max(1, width-2))
			cursorCol += 2
		}
		lines = append(lines, terminalLine{Text: prefix + text})
	}
	return lines, current - start, cursorCol
}

func editorLineWindow(text string, cursor, width int) (string, int) {
	// Horizontal panning keeps the cursor visible without wrapping a wide glyph
	// or splitting a grapheme. Editing scans are O(draft bytes); a rope/index is
	// the upgrade path for repeated navigation inside near-limit 1 MiB drafts.
	left := text[:cursor]
	if cellWidth(inlineText(left)) < width {
		return text, cellWidth(inlineText(left))
	}
	e := textEditor{text: left, cursor: len(left)}
	positions := e.boundaries()
	start := len(left)
	used := 0
	for index := len(positions) - 2; index >= 0; index-- {
		part := inlineText(left[positions[index]:positions[index+1]])
		if used+cellWidth(part) > max(0, width-2) {
			break
		}
		used += cellWidth(part)
		start = positions[index]
	}
	return "‹" + text[start:], used + 1
}
