package cli

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const interruptConfirmWindow = 2 * time.Second

type interruptConfirmExpiredMsg struct{ Generation uint64 }

func (m *terminalModel) clearInterruptConfirmation() {
	m.interruptUntil = time.Time{}
	m.interruptGeneration++
}

func (m *terminalModel) confirmInterrupt(now time.Time) tea.Cmd {
	if now.Before(m.interruptUntil) {
		m.clearInterruptConfirmation()
		m.stopMascot()
		// Keep rendering until the application has saved input and the owned
		// runtime has reported its outcome. A first press never cancels work.
		m.ui.interrupt()
		return nil
	}
	m.interruptUntil = now.Add(interruptConfirmWindow)
	m.interruptGeneration++
	generation := m.interruptGeneration
	return tea.Tick(interruptConfirmWindow, func(time.Time) tea.Msg {
		return interruptConfirmExpiredMsg{Generation: generation}
	})
}

func (m terminalModel) interruptNotice(width int) terminalLine {
	full := noticeLine(terminalNotice{Text: "Press Ctrl+C again to exit · any other key keeps working", Kind: toneWarning})
	if cellWidth(full.Text) < width {
		return full
	}
	for _, text := range []string{"Ctrl+C again to exit · any other key cancels", "Ctrl+C again to exit", "Ctrl+C again", "^C?"} {
		if cellWidth(text) < width {
			return terminalLine{Text: text, Tone: toneWarning}
		}
	}
	return terminalLine{}
}
