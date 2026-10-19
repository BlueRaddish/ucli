package cli

import (
	"fmt"
	"strings"
)

// Session metadata changes only on accepted UI actions. Avoid native inspection
// or filesystem catalog reads on the per-keystroke rendering path.
func (s *session) syncTerminalChrome() error {
	if s.terminal == nil {
		return nil
	}
	if s.accountLabels == nil {
		s.accountLabels = make(map[string]string)
		if s.app != nil {
			for _, account := range s.app.Accounts() {
				s.accountLabels[account.ID] = account.Label
			}
		}
	}
	selection := s.selection.WithDefaults()
	account := selection.Account
	if label := s.accountLabels[account]; label != "" {
		account = label
	}
	context := fmt.Sprintf("%s | effort %s | account %s", selection.Permission, selection.Effort, account)
	if s.demo {
		context += " | DEMO"
	}
	chrome := terminalChrome{Version: Version, Location: s.cwd, Model: selection.Model, Context: context}
	if s.chromeSet && s.chrome == chrome {
		return nil
	}
	if err := s.terminal.setChrome(chrome); err != nil {
		return err
	}
	s.chrome, s.chromeSet = chrome, true
	return nil
}

func (o *output) notice(text string, tone terminalTone) error {
	if o.terminal == nil || o.json {
		return nil
	}
	return o.write(terminalNoticeWriter{terminal: o.terminal, tone: tone}, strings.TrimSpace(text))
}

// Use the same bounded output worker as transcript writes. Program.Send can
// wait for an unresponsive renderer; it must never block the task/session caller.
type terminalNoticeWriter struct {
	terminal *terminalUI
	tone     terminalTone
}

func (w terminalNoticeWriter) Write(text []byte) (int, error) {
	if err := w.terminal.notice(terminalNotice{Text: string(text), Kind: w.tone}); err != nil {
		return 0, err
	}
	return len(text), nil
}

// Activity uses the same bounded queue as text and notices. It never waits for
// animation to finish and adds no frames to plain/JSON output or saved events.
func (o *output) activity(id string, static bool) error {
	if o.terminal == nil || o.json {
		return nil
	}
	return o.write(terminalActivityWriter{terminal: o.terminal, static: static}, id)
}

type terminalActivityWriter struct {
	terminal *terminalUI
	static   bool
}

func (w terminalActivityWriter) Write(id []byte) (int, error) {
	if err := w.terminal.activity(string(id), w.static); err != nil {
		return 0, err
	}
	return len(id), nil
}
