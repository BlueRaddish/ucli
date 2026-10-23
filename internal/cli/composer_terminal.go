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
	if s.task != nil {
		editor.set(s.task.Draft)
		// Restored prompts are content, even when their first word is /send.
		literal = editor.text != ""
	}
	completion := terminalCompletion{}
	for {
		if err := s.syncTerminalChrome(); err != nil {
			return line{text: editor.text, literal: true, replace: true, err: err}
		}
		if s.recoveryID != "" {
			_ = s.out.notice("Draft conflict | choose /tasks or /new", toneWarning)
		}
		completion.sync(editor.text, editor.cursor, literal)
		frame := s.composerFrame(editor)
		frame.Completion = completion.result()
		frame.CompletionQuery = editor.text
		width, bodyRows := s.terminal.size()
		if s.terminal.shellEnabled.Load() {
			bodyRows = terminalShellBudget(int(s.terminal.height.Load()), len(frame.Lines), s.terminal.panelSurface.Load()).body
		}
		completionReady := completionCanAccept(frame.Completion, width, bodyRows)
		if frame.Completion.Visible && len(frame.Lines) > 1 {
			hint := "Keep typing · Esc close"
			if len(frame.Completion.Rows) > 0 {
				hint = "Resize for commands · Esc close"
				if completionReady {
					hint = completionHint(width, editor.text == frame.Completion.Rows[frame.Completion.Focus].Name)
				}
			}
			frame.Lines[len(frame.Lines)-1] = terminalLine{Text: hint, Dim: true}
		}
		if err := s.terminal.paintComposer(frame); err != nil {
			return line{text: editor.text, literal: true, replace: true, err: err}
		}
		event, err := s.terminal.next(ctx)
		if err != nil {
			return line{text: editor.text, literal: true, replace: true, err: err}
		}
		if frame.Completion.Visible {
			switch event.kind {
			case keyUp, keyDown, keyPageUp, keyPageDown:
				delta := 1
				if event.kind == keyPageUp || event.kind == keyPageDown {
					delta = 5
				}
				if event.kind == keyUp || event.kind == keyPageUp {
					delta = -delta
				}
				completion.move(delta)
				continue
			case keyEscape:
				completion.dismiss()
				continue
			case keyTab, keyEnter:
				// A fully typed action retains its familiar Enter behavior. A
				// partial name only completes; accepting it cannot submit a task.
				exact := false
				result := frame.Completion
				if result.Focus >= 0 && result.Focus < len(result.Rows) {
					exact = editor.text == result.Rows[result.Focus].Name
				}
				if event.kind == keyTab || !exact && len(result.Rows) > 0 {
					if text, ok := completion.accept(); ok && completionReady {
						editor.set(text)
					}
					continue
				}
			}
		}
		switch event.kind {
		case keyEnter:
			_ = s.terminal.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> "}, {Text: "Working · Ctrl+C twice to interrupt and exit", Dim: true}}, CursorRow: -1})
			_ = s.terminal.paint(terminalFrame{CursorRow: -1})
			return line{text: editor.text, complete: true, literal: literal, replace: true}
		case keyEOF:
			return line{text: editor.text, literal: true, replace: true, err: io.EOF}
		case keyEscape:
			// Esc leaves text intact. Discarding a composition is explicit Ctrl+U.
		case keyTab:
			actions := []choice{{"/models", "Models - choose an AI model or see setup steps"}, {"/accounts", "Accounts - choose a connected AI account"}, {"/login", "Connect account - use an existing login or sign in"}, {"/sessions", "Sessions - watch progress and read results"}, {"/stop", "Stop current task - request stop without closing UCLI"}, {"/tasks", "Switch session - open earlier work for a follow-up"}, {"/new", "New session - start fresh"}, {"/effort", "Effort - choose reasoning intent"}, {"/permission", "Permission - choose what the agent may access"}, {"/workflow", "Workflow - inspect a plan and its steps"}, {"/voice", "Voice - dictate into this prompt"}, {"/result", "Results - read saved responses"}, {"/explain", "Explain - preview the next route"}, {"/help", "Help - show every action"}}
			value, err := s.chooseTerminal(ctx, "Actions — your composition is kept", actions, "")
			if err != nil {
				return line{text: editor.text, literal: true, replace: true, err: err}
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
							return line{text: editor.text, literal: true, replace: true, err: err}
						}
						_ = s.out.problem(err)
					}
					continue
				}
				if value == "/tasks" || value == "/new" {
					if s.recoveryID == "" && (editor.text != "" || s.task != nil && s.task.Draft != "") {
						if err := s.storeDraft(ctx, editor.text, false); err != nil {
							_ = s.out.problem(err)
							continue
						}
					}
				}
				_, err := action(ctx, value)
				if err != nil {
					if ctx.Err() != nil || problemFor(err).Exit == 9 {
						return line{text: editor.text, literal: true, replace: true, err: err}
					}
					_ = s.out.problem(err)
				}
				if err == nil && (value == "/tasks" || value == "/new") {
					editor.set("")
					if s.task != nil {
						editor.set(s.task.Draft)
					}
					literal = editor.text != ""
				}
				if ctx.Err() != nil {
					return line{text: editor.text, literal: true, replace: true, err: ctx.Err()}
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
	if len(lines) < max(1, height) {
		lines = append(lines, terminalLine{Text: composerHint(width), Dim: true})
	}
	return terminalFrame{Lines: lines, CursorRow: row, CursorCol: col}
}

func composerHint(width int) string {
	for _, text := range []string{
		"Enter send · Alt+Enter newline · / commands · Tab actions · Ctrl+C twice exit",
		"Enter send · Alt+Enter newline · Tab actions",
		"Enter send · Tab actions",
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
