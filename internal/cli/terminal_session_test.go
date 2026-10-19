package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"ucli.local/ucli/internal/core"
)

func TestTaskContextChangeReplacesNoticeAndShowsOpenedDraft(t *testing.T) {
	for _, action := range []string{"new", "open"} {
		t.Run(action, func(t *testing.T) {
			u, p := shellHarness(t, 80, 24, false)
			_ = u.notice(terminalNotice{Text: "Draft saved | /send submits it", Kind: toneSuccess})
			var transcript bytes.Buffer
			o := newOutput(&transcript, &transcript, false, func() {})
			o.terminal = u
			s := &session{terminal: u, out: o, task: &core.Task{Draft: "Opened task text"}}
			if action == "new" {
				if _, err := s.action(context.Background(), "/new"); err != nil {
					t.Fatal(err)
				}
			} else if err := s.openedTaskNotice(); err != nil {
				t.Fatal(err)
			}
			if err := o.finish(); err != nil {
				t.Fatal(err)
			}
			if p.model.shell.notice.Kind != toneInfo || strings.Contains(p.model.shell.notice.Text, "Draft saved") {
				t.Fatalf("previous task notice survived context switch: %+v", p.model.shell.notice)
			}
			if action == "open" && !strings.Contains(transcript.String(), "Opened task text") {
				t.Fatal("opened draft became invisible")
			}
		})
	}
}

type chromeCollector struct{ chrome []terminalChrome }

func (p *chromeCollector) Send(msg tea.Msg) {
	if chrome, ok := msg.(terminalChromeMsg); ok {
		p.chrome = append(p.chrome, terminalChrome(chrome))
	}
}
func (p *chromeCollector) Quit() {}

func TestSessionChromeTracksAcceptedSettingsWithoutRepaintingOnTyping(t *testing.T) {
	collector := &chromeCollector{}
	terminal := &terminalUI{program: collector, done: make(chan struct{})}
	s := &session{terminal: terminal, cwd: "workspace", selection: core.Defaults(), demo: true,
		accountLabels: map[string]string{"connected": "Work account"}}
	for range 100 {
		if err := s.syncTerminalChrome(); err != nil {
			t.Fatal(err)
		}
	}
	if len(collector.chrome) != 1 || !strings.Contains(collector.chrome[0].Context, "DEMO") {
		t.Fatalf("unchanged typing repeatedly rebuilt chrome: %+v", collector.chrome)
	}
	s.selection.Account = "connected"
	s.selection.Effort = "high"
	if err := s.syncTerminalChrome(); err != nil {
		t.Fatal(err)
	}
	if len(collector.chrome) != 2 || !strings.Contains(collector.chrome[1].Context, "effort high | account Work account") {
		t.Fatalf("accepted selection was not reflected: %+v", collector.chrome)
	}
}

func TestSessionNoticeUsesBoundedOutputWhenTerminalSendBlocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	program := &shutdownGateProgram{ctx: context.Background(), entered: make(chan tea.Msg, 1), release: make(chan struct{})}
	released := false
	defer func() {
		if !released {
			close(program.release)
		}
	}()
	o := newOutput(io.Discard, io.Discard, false, cancel)
	o.terminal = &terminalUI{program: program, done: make(chan struct{})}
	returned := make(chan error, 1)
	go func() { returned <- o.notice("Draft saved", toneSuccess) }()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("notice blocked the session caller on terminal Send")
	}
	select {
	case <-program.entered:
	case <-time.After(time.Second):
		t.Fatal("notice never reached the actual terminal Send path")
	}
	finished := make(chan error, 1)
	go func() { finished <- o.finish() }()
	select {
	case err := <-finished:
		if err == nil || ctx.Err() == nil {
			t.Fatal("blocked notice reported successful delivery")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked notice exceeded the output shutdown deadline")
	}
	close(program.release)
	released = true
	select {
	case <-o.done:
	case <-time.After(time.Second):
		t.Fatal("output worker did not leave after the held terminal was released")
	}
}
