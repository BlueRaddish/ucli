package cli

import (
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rivo/uniseg"
)

type terminalTone uint8

const (
	toneNormal terminalTone = iota
	toneInfo
	toneSuccess
	toneWarning
	toneError
)

type terminalChrome struct {
	Version, Location, Model, Context string
}

type terminalNotice struct {
	Text string
	Kind terminalTone
}

type terminalChromeMsg terminalChrome
type terminalNoticeMsg terminalNotice
type terminalComposerMsg terminalFrame
type terminalTranscriptMsg string
type terminalExitMsg struct{}

type terminalTheme uint8

const (
	themeUnknown terminalTheme = iota
	themeDark
	themeLight
)

type terminalShell struct {
	enabled       bool
	chrome        terminalChrome
	notice        terminalNotice
	composer      terminalFrame
	composerFocus bool
	transcript    []string
	bytes         int
	dropped       bool
	scroll        int // wrapped rows above the live tail
}

// Display history is deliberately bounded; durable task results remain in the
// app store. A cached wrapped index is the next step if larger in-memory history
// is needed. The current ceiling keeps each resize/repaint bounded to 64 KiB.
const shellTranscriptBytes = 64 * 1024
const shellTranscriptLines = 400

func (s *terminalShell) append(text string) {
	text = streamFragment(text)
	s.transcript = append(s.transcript, text)
	s.bytes += len(text)
	for s.bytes > shellTranscriptBytes || len(s.transcript) > shellTranscriptLines {
		s.bytes -= len(s.transcript[0])
		s.transcript = s.transcript[1:]
		s.dropped = true
	}
}

type shellBudget struct {
	header, body, notice, composer int
}

func terminalShellBudget(height, composerRows int) shellBudget {
	height = max(1, height)
	b := shellBudget{}
	// At tiny sizes, keep an actionable editor and notice instead of clipping a
	// cursor below invisible chrome. Overlay confirmation uses the body budget.
	if height < 4 {
		b.composer = 1
		if height >= 2 {
			b.notice = 1
		}
		b.body = height - b.composer - b.notice
		return b
	}
	b.header = 1
	if height >= 8 {
		b.header = 2
	}
	if height >= 11 {
		b.header = 3
	}
	b.notice = 1 // reserve the same row when no notice is active
	b.composer = min(max(2, composerRows), min(7, height-b.header-b.notice-1))
	b.composer = max(1, b.composer)
	b.body = max(0, height-b.header-b.notice-b.composer)
	return b
}

func (u *terminalUI) setChrome(chrome terminalChrome) error {
	if err := u.failure(); err != nil {
		return err
	}
	u.shellEnabled.Store(true)
	u.program.Send(terminalChromeMsg(chrome))
	return u.failure()
}

func (u *terminalUI) paintComposer(frame terminalFrame) error {
	if err := u.failure(); err != nil {
		return err
	}
	u.composerRows.Store(int64(len(frame.Lines)))
	u.program.Send(terminalComposerMsg(frame))
	return u.failure()
}

func (u *terminalUI) notice(notice terminalNotice) error {
	// A final diagnostic must remain deliverable after input failure, just like
	// terminalWriter. The output worker owns the bounded delivery deadline.
	select {
	case <-u.done:
		return io.ErrClosedPipe
	default:
	}
	u.program.Send(terminalNoticeMsg(notice))
	return nil
}

func (u *terminalUI) composerSize() (int, int) {
	width, height := int(u.width.Load()), int(u.height.Load())
	if !u.shellEnabled.Load() {
		return u.size()
	}
	budget := terminalShellBudget(height, 7)
	return width, max(1, budget.composer)
}

func terminalStyle(line terminalLine, width int, color bool, theme terminalTheme) string {
	text := clipCells(inlineText(line.Text), max(1, width-1))
	if !color || text == "" {
		return text
	}
	style := ""
	switch line.Tone {
	case toneInfo:
		style = "36"
	case toneSuccess:
		style = "32"
	case toneWarning:
		style = "33"
	case toneError:
		style = "31"
	}
	if theme != themeUnknown && line.Tone != toneNormal {
		// Use an observed terminal background when available. Otherwise ANSI
		// semantic colors defer to the user's palette; no background is forced.
		palette := [...]string{"", "89;194;255", "115;209;61", "255;209;102", "255;107;107"}
		if theme == themeLight {
			palette = [...]string{"", "0;94;168", "23;106;47", "138;88;0", "180;35;24"}
		}
		if line.Tone <= toneError {
			style = "38;2;" + palette[line.Tone]
		}
	}
	if line.Focused {
		style = "7;36"
	} else if style == "" && line.Dim {
		style = "2"
	}
	if style == "" {
		return text
	}
	return "\x1b[" + style + "m" + text + "\x1b[0m"
}

func noticeLine(notice terminalNotice) terminalLine {
	if notice.Text == "" {
		return terminalLine{}
	}
	label := "Info"
	switch notice.Kind {
	case toneSuccess:
		label = "OK"
	case toneWarning:
		label = "Warning"
	case toneError:
		label = "Error"
	}
	return terminalLine{Text: "[" + label + "] " + notice.Text, Tone: notice.Kind}
}

func (s terminalShell) header(width, rows int) []terminalLine {
	if rows == 0 {
		return nil
	}
	brand := "=^.^= UCLI " + s.chrome.Version
	model := "Model " + s.chrome.Model
	if s.chrome.Context != "" {
		model += " · " + s.chrome.Context
	}
	if rows == 1 {
		return []terminalLine{{Text: brand + " · " + s.chrome.Model, Tone: toneInfo}}
	}
	if rows == 2 {
		return []terminalLine{{Text: brand + " · " + tailCells(inlineText(s.chrome.Location), max(1, width-cellWidth(brand)-4)), Tone: toneInfo}, {Text: model}}
	}
	return []terminalLine{{Text: brand, Tone: toneInfo}, {Text: "Location " + tailCells(inlineText(s.chrome.Location), max(1, width-10)), Dim: true}, {Text: model}}
}

func tailCells(text string, width int) string {
	if cellWidth(text) <= width {
		return text
	}
	positions := []int{0}
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		_, end := g.Positions()
		positions = append(positions, end)
	}
	start, used := len(text), 0
	for i := len(positions) - 2; i >= 0; i-- {
		part := text[positions[i]:positions[i+1]]
		if used+cellWidth(part) > max(0, width-1) {
			break
		}
		start, used = positions[i], used+cellWidth(part)
	}
	return "…" + text[start:]
}

func wrapTerminalText(text string, width int) []terminalLine {
	text, width = inlineText(text), max(1, width)
	var rows []terminalLine
	var line strings.Builder
	used := 0
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		part, cells := g.Str(), g.Width()
		if used+cells > width && line.Len() > 0 {
			rows = append(rows, terminalLine{Text: line.String()})
			line.Reset()
			used = 0
		}
		if cells > width {
			part, cells = "�", 1
		}
		line.WriteString(part)
		used += cells
	}
	rows = append(rows, terminalLine{Text: line.String()})
	return rows
}

func (s terminalShell) historyLines(fragment string, width int) []terminalLine {
	var all []terminalLine
	if s.dropped {
		all = append(all, terminalLine{Text: "Earlier display lines omitted · /result opens saved task output.", Dim: true})
	}
	for _, text := range s.transcript {
		all = append(all, wrapTerminalText(text, max(1, width-1))...)
	}
	if fragment != "" {
		all = append(all, wrapTerminalText(fragment, max(1, width-1))...)
	}
	return all
}

func (s terminalShell) history(fragment string, width, rows int) []terminalLine {
	if rows == 0 {
		return nil
	}
	all := s.historyLines(fragment, width)
	contentRows := rows
	if rows >= 3 && len(all) > rows {
		contentRows--
	}
	end := max(contentRows, len(all)-s.scroll)
	end = min(end, len(all))
	start := max(0, end-contentRows)
	visible := append([]terminalLine(nil), all[start:end]...)
	if contentRows < rows {
		for len(visible) < contentRows {
			visible = append(visible, terminalLine{})
		}
		visible = append(visible, terminalLine{Text: fmt.Sprintf("PgUp/PgDn output · %d–%d of %d", start+1, end, len(all)), Dim: true})
	}
	return visible
}

// composerViewport preserves the hint on the final row and pans the editor
// around its cursor. Inactive overlays retain the exact composer text.
func composerViewport(frame terminalFrame, rows int, active bool) ([]terminalLine, int) {
	if rows <= 0 {
		return nil, -1
	}
	lines := frame.Lines
	if len(lines) == 0 {
		lines = []terminalLine{{Text: "> "}, {Text: "Tab actions", Dim: true}}
	}
	if !active && len(lines) > 1 {
		lines = append([]terminalLine(nil), lines...)
		lines[len(lines)-1] = terminalLine{Text: "Draft kept · finish this panel to edit", Dim: true}
	}
	if len(lines) <= rows {
		padding := rows - len(lines)
		out := make([]terminalLine, padding, rows)
		out = append(out, lines...)
		row := frame.CursorRow + padding
		if !active || frame.CursorRow < 0 {
			row = -1
		}
		return out, row
	}
	if rows == 1 {
		index := min(max(0, frame.CursorRow), len(lines)-1)
		row := -1
		if active && frame.CursorRow >= 0 {
			row = 0
		}
		return []terminalLine{lines[index]}, row
	}
	count := rows - 1
	start := min(max(0, frame.CursorRow-count+1), len(lines)-1-count)
	out := append([]terminalLine(nil), lines[start:start+count]...)
	out = append(out, lines[len(lines)-1])
	row := frame.CursorRow - start
	if !active || row < 0 || row >= count {
		row = -1
	}
	return out, row
}

func (m terminalModel) shellView(width, height int) tea.View {
	s := m.shell
	budget := terminalShellBudget(height, len(s.composer.Lines))
	lines := s.header(width, budget.header)
	body := s.history(m.fragment, width, budget.body)
	cursorRow, cursorCol := -1, 0
	if !s.composerFocus && len(m.frame.Lines) > 0 {
		body = m.frame.Lines[:min(len(m.frame.Lines), budget.body)]
		if m.frame.CursorRow >= 0 && m.frame.CursorRow < len(body) {
			cursorRow, cursorCol = budget.header+m.frame.CursorRow, m.frame.CursorCol
		}
	}
	lines = append(lines, body...)
	for len(lines) < budget.header+budget.body {
		lines = append(lines, terminalLine{})
	}
	if budget.notice > 0 {
		lines = append(lines, noticeLine(s.notice))
	}
	composer, row := composerViewport(s.composer, budget.composer, s.composerFocus)
	if row >= 0 {
		cursorRow, cursorCol = len(lines)+row, s.composer.CursorCol
	}
	lines = append(lines, composer...)
	rendered := make([]string, len(lines))
	for i, line := range lines {
		rendered[i] = terminalStyle(line, width, m.color, m.theme)
	}
	view := tea.NewView(strings.Join(rendered, "\n"))
	view.AltScreen = true
	if cursorRow >= 0 && cursorRow < height {
		view.Cursor = tea.NewCursor(min(max(0, cursorCol), max(0, width-2)), cursorRow)
	}
	return view
}
