package cli

import (
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"ucli.local/ucli/assets/mascot"
)

type terminalOptions struct{ NoMotion, ASCII, NoMouse bool }

func terminalOptionsFor(p invocation) terminalOptions {
	return terminalOptions{NoMotion: p.set["no-motion"], ASCII: p.set["ascii"], NoMouse: p.set["no-mouse"]}
}

type terminalActivityMsg struct {
	ID     string
	Static bool
}

type mascotTickMsg struct {
	Generation uint64
	At         time.Time
}

type terminalMascot struct {
	id         string
	frame      string
	started    time.Time
	generation uint64
	static     bool
	closed     bool
}

// Only the terminal model advances presentation. One deadline is outstanding
// for its current generation; superseded ticks cannot restart old activity.
// This is intentionally a single foreground indicator, not a task scheduler.
func (m *terminalModel) startMascot(activity terminalActivityMsg, now time.Time) tea.Cmd {
	if m.mascot.closed {
		return nil
	}
	if activity.ID == "" {
		m.stopMascot()
		return nil
	}
	static := activity.Static || m.options.NoMotion
	if activity.ID == m.mascot.id && static == m.mascot.static {
		return nil
	}
	m.mascot.generation++
	m.mascot.id, m.mascot.static = activity.ID, static
	m.mascot.started = now
	return m.advanceMascot(now)
}

func (m *terminalModel) stopMascot() {
	m.mascot.generation++
	m.mascot.id, m.mascot.frame = "", ""
}

func (m *terminalModel) advanceMascot(now time.Time) tea.Cmd {
	if m.mascot.id == "" || m.mascot.closed {
		return nil
	}
	frame, next := mascot.Frame(m.mascot.id, now.Sub(m.mascot.started), m.mascot.static, m.options.ASCII)
	m.mascot.frame = frame
	if next <= 0 {
		return nil
	}
	generation := m.mascot.generation
	return tea.Tick(next, func(at time.Time) tea.Msg { return mascotTickMsg{generation, at} })
}

func (m terminalModel) mascotHeader() string {
	frame, _ := mascot.Frame("wave", 0, true, m.options.ASCII)
	return strings.TrimRight(frame, " ")
}

func (m terminalModel) mascotNotice(width int) terminalLine {
	line := noticeLine(m.shell.notice)
	if line.Text == "" || m.mascot.frame == "" || line.Tone == toneError || line.Tone == toneWarning {
		return line
	}
	// Status and recovery text win over decoration at constrained widths. The
	// padded slot never changes position as eyes or paws change frame.
	if cellWidth(line.Text)+cellWidth(m.mascot.frame)+1 > max(1, width-1) {
		return line
	}
	label, text, found := strings.Cut(line.Text, "] ")
	if found {
		line.Text = label + "] " + m.mascot.frame + " " + text
	}
	return line
}

func (u *terminalUI) activity(id string, static bool) error {
	select {
	case <-u.done:
		return io.ErrClosedPipe
	default:
	}
	u.program.Send(terminalActivityMsg{ID: id, Static: static})
	return u.failure()
}
