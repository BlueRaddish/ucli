package cli

import (
	"encoding/base64"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rivo/uniseg"
)

// Spans are trusted UI metadata, never parsed from model output. Coordinates
// use terminal cells; provider text cannot manufacture a clickable control.
type terminalSpan struct {
	Start, End int
	Tone       terminalTone
	Bold       bool
	Action     string
}

type terminalPart struct {
	Text, Action string
	Tone         terminalTone
	Bold         bool
}

func partsLine(parts ...terminalPart) terminalLine {
	line := terminalLine{}
	position := 0
	for _, part := range parts {
		text := inlineText(part.Text)
		end := position + cellWidth(text)
		line.Text += text
		line.Spans = append(line.Spans, terminalSpan{position, end, part.Tone, part.Bold, part.Action})
		position = end
	}
	return line
}

func sliceTerminalCells(text string, start, end int) string {
	var out strings.Builder
	position := 0
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		next := position + g.Width()
		if position >= start && next <= end {
			out.WriteString(g.Str())
		}
		position = next
		if position >= end {
			break
		}
	}
	return out.String()
}

func actionAt(line terminalLine, x, width int) string {
	if x < 0 || x >= min(cellWidth(inlineText(line.Text)), max(0, width-1)) {
		return ""
	}
	for _, span := range line.Spans {
		if x >= span.Start && x < span.End && span.Action != "" {
			return span.Action
		}
	}
	return line.Action
}

func (m *terminalModel) activate(action string) {
	switch action {
	case "job:open":
		if job, ok := m.jobs.selected(m.shell.chrome.TaskID); ok {
			m.jobs.focus = job.TaskID
		}
		m.jobInspector, m.jobScroll = true, 0
	case "job:close":
		m.jobInspector = false
	case "escape":
		m.ui.enqueue(keyEvent{kind: keyEscape})
	case "prompt":
		budget := terminalShellBudget(int(m.ui.height.Load()), len(m.shell.composer.Lines), m.shell.chrome.Panel)
		m.shell.navigate(keyLatestPrompt, m.fragment, int(m.ui.width.Load()), budget.body)
	case "latest":
		m.shell.navigate(keyLatestOutput, m.fragment, int(m.ui.width.Load()), 0)
	case "/models", "/login", "/accounts", "/sessions", "/shortcuts", "/permission":
		if m.shell.composerFocus && !m.jobInspector {
			m.ui.enqueue(keyEvent{kind: keyAction, text: action})
		}
	default:
		if task, attempt, ok := parseJobCaptionAction(action); ok {
			for _, job := range m.jobs.items {
				if task == job.TaskID && attempt == job.AttemptID {
					m.jobs.focus, m.jobInspector, m.jobScroll = task, true, 0
					return
				}
			}
		}
		if value, ok := strings.CutPrefix(action, "choice:"); ok && !m.jobInspector {
			m.ui.enqueue(keyEvent{kind: keyChoice, text: value})
		}
	}
}

func (m *terminalModel) click(mouse tea.MouseClickMsg) {
	if m.mouseDisabled || mouse.Button != tea.MouseLeft || mouse.Mod&tea.ModShift != 0 {
		return
	}
	width, height := int(m.ui.width.Load()), int(m.ui.height.Load())
	if mouse.Y < 0 || mouse.Y >= height {
		return
	}
	frame := m.shellFrame(width, height)
	if mouse.Y < len(frame.Lines) {
		m.activate(actionAt(frame.Lines[mouse.Y], mouse.X, width))
	}
}

func (m *terminalModel) inspectKey(event keyEvent) bool {
	if !m.jobInspector || event.kind == keyInterrupt || event.kind == keyEOF {
		return false
	}
	switch event.kind {
	case keyEscape:
		m.jobInspector = false
	case keyUp:
		m.jobScroll = max(0, m.jobScroll-1)
	case keyDown:
		m.jobScroll++
	case keyPageUp:
		m.jobScroll = max(0, m.jobScroll-8)
	case keyPageDown:
		m.jobScroll += 8
	case keyHome:
		m.jobScroll = 0
	case keyEnd:
		m.jobScroll = 10000 // frame clamps to the retained view's last page
	case keyLeft:
		m.moveJob(-1)
	case keyRight:
		m.moveJob(1)
	}
	return true // Viewing a job cannot type or submit into the hidden editor.
}

func (m *terminalModel) wheel(mouse tea.MouseWheelMsg) {
	if m.mouseDisabled || mouse.Mod&tea.ModShift != 0 {
		return
	}
	direction := -3
	if mouse.Button == tea.MouseWheelDown {
		direction = 3
	} else if mouse.Button != tea.MouseWheelUp {
		return
	}
	if m.jobInspector {
		m.jobScroll = max(0, m.jobScroll+direction)
		return
	}
	if !m.shell.composerFocus {
		kind := keyUp
		if direction > 0 {
			kind = keyDown
		}
		m.ui.enqueue(keyEvent{kind: kind})
		return
	}
	budget := terminalShellBudget(int(m.ui.height.Load()), len(m.shell.composer.Lines), m.shell.chrome.Panel)
	kind := keyPageUp
	if direction > 0 {
		kind = keyPageDown
	}
	m.shell.navigate(kind, m.fragment, int(m.ui.width.Load()), budget.body)
}

// Captions address the displayed reply's exact attempt. IDs may contain colons
// or other punctuation, so encode each field rather than split native IDs.
// Only our trusted line metadata calls this helper; provider text is inert.
func jobCaptionAction(task, attempt string) string {
	if !validTranscriptScopeID(task) || !validTranscriptScopeID(attempt) {
		return ""
	}
	return "job:attempt:" + base64.RawURLEncoding.EncodeToString([]byte(task)) + ":" + base64.RawURLEncoding.EncodeToString([]byte(attempt))
}

func parseJobCaptionAction(action string) (task, attempt string, ok bool) {
	payload, found := strings.CutPrefix(action, "job:attempt:")
	if !found {
		return "", "", false
	}
	taskPart, attemptPart, found := strings.Cut(payload, ":")
	// Each source ID is at most 256 UTF-8 bytes. Reject before decoding so
	// even malformed trusted metadata cannot allocate an unbounded payload.
	if !found || len(taskPart) > 342 || len(attemptPart) > 342 {
		return "", "", false
	}
	decoder := base64.RawURLEncoding.Strict()
	taskBytes, taskErr := decoder.DecodeString(taskPart)
	attemptBytes, attemptErr := decoder.DecodeString(attemptPart)
	if taskErr != nil || attemptErr != nil {
		return "", "", false
	}
	task, attempt = string(taskBytes), string(attemptBytes)
	return task, attempt, validTranscriptScopeID(task) && validTranscriptScopeID(attempt) && jobCaptionAction(task, attempt) == action
}
