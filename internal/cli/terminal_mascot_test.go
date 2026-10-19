package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"ucli.local/ucli/internal/app"
)

func TestMascotDeadlineAdvancesWithoutInputOrDraftMutation(t *testing.T) {
	u, program := shellHarness(t, 80, 24, false)
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> keep this draft"}, {Text: "Enter saves"}}, CursorRow: 0, CursorCol: 17})
	_ = u.notice(terminalNotice{Text: "Ready", Kind: toneInfo})
	model, command := program.model.Update(terminalActivityMsg{ID: "wave"})
	before := model.(terminalModel)
	if command == nil || before.mascot.frame != "(^.⩊.^) " {
		t.Fatal("welcome did not schedule its first real frame deadline")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- command() }()
	var tick tea.Msg
	select {
	case tick = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("welcome frame deadline never arrived")
	}
	model, command = before.Update(tick)
	after := model.(terminalModel)
	if command == nil || after.mascot.frame == before.mascot.frame {
		t.Fatal("real timer did not advance the welcome frame")
	}
	if len(u.events) != 0 || after.shell.composer.Lines[0].Text != "> keep this draft" || len(after.shell.transcript) != 0 {
		t.Fatal("animation changed input or durable-facing transcript")
	}
	if after.View().Cursor.X != before.View().Cursor.X || after.View().Cursor.Y != before.View().Cursor.Y {
		t.Fatal("animation moved the editor cursor")
	}
}

func TestMascotSupersessionSeverityAndExitFence(t *testing.T) {
	_, p := shellHarness(t, 80, 24, false)
	model, _ := p.model.Update(terminalActivityMsg{ID: "working"})
	m := model.(terminalModel)
	old := mascotTickMsg{m.mascot.generation, m.mascot.started.Add(time.Second)}
	model, _ = m.Update(terminalActivityMsg{ID: "looking"})
	m = model.(terminalModel)
	frame, generation := m.mascot.frame, m.mascot.generation
	model, command := m.Update(old)
	m = model.(terminalModel)
	if command != nil || m.mascot.frame != frame || m.mascot.generation != generation {
		t.Fatal("a superseded timer changed the new operation")
	}
	for _, kind := range []terminalTone{toneWarning, toneError} {
		model, _ = m.Update(terminalActivityMsg{ID: "working"})
		m = model.(terminalModel)
		tick := mascotTickMsg{m.mascot.generation, m.mascot.started.Add(time.Second)}
		model, _ = m.Update(terminalNoticeMsg{Text: "Inspect the saved task", Kind: kind})
		m = model.(terminalModel)
		model, command = m.Update(tick)
		m = model.(terminalModel)
		if command != nil || m.mascot.frame != "" || m.mascot.id != "" {
			t.Fatal("severity notice left a busy animation running")
		}
	}
	model, _ = m.Update(terminalExitMsg{})
	m = model.(terminalModel)
	model, command = m.Update(terminalActivityMsg{ID: "done"})
	m = model.(terminalModel)
	if command != nil || m.mascot.frame != "" || !m.mascot.closed {
		t.Fatal("late output restarted animation after terminal exit")
	}
}

func TestMascotModesAndSettledSuccess(t *testing.T) {
	_, p := shellHarness(t, 80, 24, false)
	m := p.model
	m.options = terminalOptions{NoMotion: true, ASCII: true}
	for _, id := range []string{"wave", "working", "peek", "done"} {
		model, command := m.Update(terminalActivityMsg{ID: id})
		m = model.(terminalModel)
		if command != nil || m.mascot.frame == "" {
			t.Fatalf("static %s is missing or scheduled motion", id)
		}
		for _, r := range m.mascot.frame + m.mascotHeader() {
			if r < 32 || r > 126 {
				t.Fatalf("ASCII mode emitted %U", r)
			}
		}
	}
	m.options = terminalOptions{}
	model, command := m.Update(terminalActivityMsg{ID: "done"})
	m = model.(terminalModel)
	if command == nil {
		t.Fatal("no-color unexpectedly disabled animation")
	}
	model, command = m.Update(mascotTickMsg{m.mascot.generation, m.mascot.started.Add(10 * time.Second)})
	m = model.(terminalModel)
	if command != nil || m.mascot.frame != "(^~⩊~^) " {
		t.Fatal("one-shot completion did not settle without another tick")
	}
	model, command = m.Update(terminalNoticeMsg{Text: "Draft saved", Kind: toneSuccess})
	m = model.(terminalModel)
	if command != nil || m.mascot.frame != "" {
		t.Fatal("ordinary draft success reused task-completion celebration")
	}
}

func TestMascotNoticePreservesLabelsAndRecoverySpace(t *testing.T) {
	_, p := shellHarness(t, 80, 24, false)
	m := p.model
	m.shell.notice = terminalNotice{Text: "Working", Kind: toneInfo}
	m.startMascot(terminalActivityMsg{ID: "working", Static: true}, time.Now())
	line := m.mascotNotice(80)
	if !strings.HasPrefix(line.Text, "[Info] (^._.^) ") || !strings.HasSuffix(line.Text, "Working") {
		t.Fatalf("status label or text was replaced by artwork: %q", line.Text)
	}
	if m.mascotNotice(20).Text != noticeLine(m.shell.notice).Text {
		t.Fatal("artwork displaced status in a narrow terminal")
	}
	m.shell.notice = terminalNotice{Text: "Inspect the saved task", Kind: toneError}
	if m.mascotNotice(80).Text != "[Error] Inspect the saved task" {
		t.Fatal("error was decorated or lost its severity label")
	}
}

func TestMascotActivityUsesBoundedOutput(t *testing.T) {
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
	go func() { returned <- o.activity("working", false) }()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("activity blocked its caller on terminal Send")
	}
	select {
	case message := <-program.entered:
		if _, ok := message.(terminalActivityMsg); !ok {
			t.Fatalf("wrong delivery: %T", message)
		}
	case <-time.After(time.Second):
		t.Fatal("activity never consumed the real terminal Send path")
	}
	finished := make(chan error, 1)
	go func() { finished <- o.finish() }()
	select {
	case err := <-finished:
		if err == nil || ctx.Err() == nil {
			t.Fatal("blocked activity reported delivery success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("activity exceeded output shutdown deadline")
	}
	close(program.release)
	released = true
	select {
	case <-o.done:
	case <-time.After(time.Second):
		t.Fatal("released output worker did not exit")
	}
}

type mascotReceiptProgram struct {
	*shellTestProgram
	app       *app.App
	doneSaved bool
}

func (p *mascotReceiptProgram) Send(msg tea.Msg) {
	if activity, ok := msg.(terminalActivityMsg); ok && activity.ID == "done" {
		// Observe storage at the exact point the renderer receives completion.
		tasks, err := p.app.Tasks(context.Background())
		if err == nil && len(tasks) == 1 {
			_, attempts, err := p.app.Task(context.Background(), tasks[0].ID)
			p.doneSaved = err == nil && len(attempts) == 1 && attempts[0].State == "complete" && attempts[0].Output != ""
		}
	}
	p.shellTestProgram.Send(msg)
}

func TestMascotTaskLifecycleConsumesDurableAppResult(t *testing.T) {
	for _, demo := range []bool{true, false} {
		t.Run(map[bool]string{true: "saved-demo", false: "missing-provider"}[demo], func(t *testing.T) {
			a, err := app.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			u, shell := shellHarness(t, 80, 24, false)
			program := &mascotReceiptProgram{shellTestProgram: shell, app: a}
			u.program = program
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			o := newOutput(io.Discard, io.Discard, false, cancel)
			o.terminal = u
			_, runErr := runTask(ctx, a, app.Options{Input: "local mascot lifecycle", CWD: t.TempDir(), Demo: demo}, o)
			if err := o.finish(); err != nil {
				t.Fatal(err)
			}
			if demo {
				if runErr != nil || !program.doneSaved || shell.model.mascot.id != "done" {
					t.Fatalf("completion was not durable: %v saved=%v id=%q", runErr, program.doneSaved, shell.model.mascot.id)
				}
			} else if runErr == nil || program.doneSaved || shell.model.mascot.id != "" {
				t.Fatalf("prerequisite failure celebrated or remained busy: %v saved=%v id=%q", runErr, program.doneSaved, shell.model.mascot.id)
			}
		})
	}
}

func TestMascotFlagsDoNotChangeJSONOrRequireStateForHelp(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	code, out, diagnostic := call(t, home, "", "--no-motion", "--ascii", "--help")
	if code != 0 || !strings.Contains(out, "--no-motion") || !strings.Contains(out, "--ascii") {
		t.Fatalf("flags undiscoverable: %d %s %s", code, out, diagnostic)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("help touched task storage: %v", err)
	}
	code, out, diagnostic = call(t, t.TempDir(), "", "run", "--demo", "--json", "--no-motion", "--ascii", "local flags")
	if code != 0 {
		t.Fatalf("JSON demo failed: %d %s %s", code, out, diagnostic)
	}
	jsonLines(t, out)
	if strings.Contains(out, "(^") || strings.ContainsRune(out, '\x1b') {
		t.Fatal("mascot escaped into programmatic output")
	}
}
