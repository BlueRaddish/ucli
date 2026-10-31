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

func TestSessionUIProgressUsesObservedBoundariesAndSavedOutcome(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> untouched"}, {Text: composerHint(80)}}, CursorRow: 0})
	p.Send(terminalJobMsg{TaskID: "task_one", Model: "claude/fixture", Phase: jobPreparing})
	if text := p.model.View().Content; !strings.Contains(text, "Preparing request") || strings.Contains(text, "Client started") || strings.Contains(text, "response saved") {
		t.Fatalf("preparation invented progress: %s", text)
	}
	o := newOutput(u.writer, u.writer, false, func() {})
	o.terminal = u
	if err := o.job(terminalJobMsg{TaskID: "task_one", AttemptID: "attempt_one", Phase: jobAccepted, Bind: true}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []core.Event{
		{Type: "accepted"},
		{Type: "runtime_context", Text: "PRIVATE_CONTEXT_SENTINEL"},
		{Type: "status", Text: core.StatusClientStarted},
		{Type: "message_delta", Text: "First paragraph.\n\nSecond paragraph."},
		{Type: "terminal_result", State: "complete"},
		{Type: "result_persisted", State: "unknown"},
	} {
		e.TaskID, e.AttemptID = "task_one", "attempt_one"
		if err := o.attemptEvent(e, "claude/fixture"); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	job, _ := p.model.jobs.selected("task_one")
	if job.Phase != jobSaved || job.State != "unknown" || !strings.Contains(job.label(), "Outcome unknown") {
		t.Fatalf("saved unknown was called success: %+v", job)
	}
	activity := strings.Join(job.Events, "\n")
	if !strings.Contains(activity, core.StatusClientStarted) || strings.Contains(activity, "PRIVATE_CONTEXT_SENTINEL") || strings.Contains(p.model.View().Content, "PRIVATE_CONTEXT_SENTINEL") {
		t.Fatalf("missing actual launch or private context rendered: %s", activity)
	}
	p.Send(terminalJobMsg{TaskID: "task_one", AttemptID: "attempt_one", Phase: jobClientRunning})
	if job, _ = p.model.jobs.selected("task_one"); job.Phase != jobSaved {
		t.Fatal("late progress replaced the durable outcome")
	}
}

func TestSessionUIInspectorOpensDuringPreparationWithoutSubmitting(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	composer := terminalFrame{Lines: []terminalLine{{Text: "> Keep this unsent"}, {Text: composerHint(80)}}, CursorRow: 0, CursorCol: 10}
	_ = u.paintComposer(composer)
	p.Send(terminalJobMsg{TaskID: "task_one", Title: "Example task", Model: "claude/example", Phase: jobPreparing})
	p.Send(tea.MouseClickMsg{X: 10, Y: 3, Button: tea.MouseLeft})
	if !p.model.jobInspector || !strings.Contains(p.model.View().Content, "Job details") || !strings.Contains(p.model.View().Content, "Preparing request") {
		t.Fatal("the request's preparation phase cannot be inspected")
	}
	p.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	p.Send(tea.KeyPressMsg{Code: 'x', Text: "x"})
	p.Send(tea.PasteMsg{Content: "Pasted text must stay out of the hidden draft"})
	if len(u.events) != 0 || p.model.shell.composer.Lines[0].Text != composer.Lines[0].Text || p.model.View().Cursor != nil {
		t.Fatal("inspector typed/submitted into the hidden composer")
	}
	p.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	if p.model.jobInspector || p.model.View().Cursor == nil || p.model.shell.composer.CursorCol != composer.CursorCol {
		t.Fatal("Back did not restore the original draft and caret")
	}
	p.Send(tea.KeyPressMsg{Code: tea.KeyF2})
	if p.model.View().MouseMode != tea.MouseModeNone {
		t.Fatal("F2 cannot release mouse reporting for native selection")
	}
	p.Send(tea.MouseClickMsg{X: 10, Y: 3, Button: tea.MouseLeft})
	if p.model.jobInspector {
		t.Fatal("click reporting disabled but click still activated")
	}
}

func TestSessionUIFooterPlacesSettingsBelowInputInBothPalettes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 20}} {
		u, p := shellHarness(t, size[0], size[1], true)
		_ = u.setChrome(terminalChrome{Version: "test", Model: "claude/example", Location: "/work/example", Effort: "high", Permission: "sandbox", Account: "Work"})
		_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> message"}, {Text: composerHint(size[0])}}, CursorRow: 0})
		for _, theme := range []terminalTheme{themeDark, themeLight} {
			p.model.theme = theme
			frame := p.model.shellFrame(size[0], size[1])
			if len(frame.Lines) != size[1] || frame.CursorRow != size[1]-6 {
				t.Fatalf("footer displaced cursor at %v: rows=%d cursor=%d", size, len(frame.Lines), frame.CursorRow)
			}
			top := terminalPickerText(terminalFrame{Lines: frame.Lines[:3]})
			bottom := terminalPickerText(terminalFrame{Lines: frame.Lines[frame.CursorRow+1:]})
			for _, value := range []string{"claude/example", "/work/example", "sandbox", "Work", "Usage not reported"} {
				if strings.Contains(top, value) || !strings.Contains(bottom, value) {
					t.Fatalf("setting %q not exclusively below input: %s", value, bottom)
				}
			}
			if frame.Lines[len(frame.Lines)-1].Text != "← agents · ? shortcuts" {
				t.Fatal("idle footer is not the requested compact navigation")
			}
			modelRow := frame.Lines[size[1]-4]
			styled := terminalStyle(modelRow, size[0], true, theme)
			if strings.Count(styled, "\x1b[") < 4 || strings.Contains(terminalStyle(modelRow, size[0], false, theme), "\x1b") {
				t.Fatal("footer lost colored fields or monochrome content")
			}
		}
	}
}

func TestSessionUIClickTargetsAreVisibleAndScoped(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> "}, {Text: composerHint(80)}}, CursorRow: 0})
	p.Send(tea.MouseClickMsg{X: 1, Y: 23, Button: tea.MouseLeft})
	e, err := u.next(context.Background())
	if err != nil || e.kind != keyAction || e.text != "/models" {
		t.Fatalf("agents footer did not navigate: %+v %v", e, err)
	}
	p.Send(tea.MouseClickMsg{X: 13, Y: 23, Button: tea.MouseLeft})
	e, err = u.next(context.Background())
	if err != nil || e.text != "/shortcuts" {
		t.Fatal("shortcuts footer did not navigate")
	}
	p.Send(tea.MouseClickMsg{X: 79, Y: 23, Button: tea.MouseLeft})
	p.Send(tea.MouseClickMsg{X: 1, Y: 23, Button: tea.MouseLeft, Mod: tea.ModShift})
	if len(u.events) != 0 {
		t.Fatal("blank/Shift-selection click activated hidden controls")
	}
	p.Send(terminalJobMsg{TaskID: "task_a", AttemptID: "attempt_a", Phase: jobAccepted})
	p.Send(terminalJobMsg{TaskID: "task_b", AttemptID: "attempt_b", Phase: jobAccepted})
	p.model.activate(jobCaptionAction("task_a", "attempt_a"))
	p.Send(terminalJobMsg{TaskID: "task_b", AttemptID: "attempt_b", Phase: jobClientRunning})
	if p.model.jobs.focus != "task_a" {
		t.Fatal("another task's activity stole inspector focus")
	}
	p.model.activate(jobCaptionAction("unretained", "attempt_a"))
	if p.model.jobs.focus != "task_a" {
		t.Fatal("unretained caption retargeted an unrelated job")
	}
}

func TestSessionUIEmptyInputShortcutsPreserveLiteralEditing(t *testing.T) {
	for _, tc := range []struct {
		name         string
		events       []keyEvent
		want, action string
	}{
		{"left empty", []keyEvent{{kind: keyLeft}, {kind: keyEOF}}, "", "/models"},
		{"question empty", []keyEvent{{kind: keyText, text: "?"}, {kind: keyChoice, text: "/models"}, {kind: keyEOF}}, "", "/models"},
		{"left editing", []keyEvent{{kind: keyText, text: "ab"}, {kind: keyLeft}, {kind: keyText, text: "?"}, {kind: keyEOF}}, "a?b", ""},
		{"pasted question", []keyEvent{{kind: keyPaste, text: "?"}, {kind: keyEOF}}, "?", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			u := &terminalUI{program: &composerFrameCollector{}, events: make(chan keyEvent, 16), done: make(chan struct{}), interrupt: cancel}
			u.width.Store(80)
			u.height.Store(24)
			for _, e := range tc.events {
				u.enqueue(e)
			}
			var sink bytes.Buffer
			o := newOutput(&sink, &sink, false, cancel)
			s := &session{selection: core.Defaults(), terminal: u, out: o}
			called := ""
			result := s.readTerminalLineWithAction(ctx, func(_ context.Context, action string) (bool, error) { called = action; return false, nil })
			if result.err != io.EOF || result.text != tc.want || called != tc.action {
				t.Fatalf("shortcut changed text/action: %+v called=%s", result, called)
			}
			if err := o.finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSessionUIPickerRejectsInvisibleClickChoices(t *testing.T) {
	terminal := &pickerTestTerminal{events: []keyEvent{{kind: keyText, text: "beta"}, {kind: keyChoice, text: "alpha"}, {kind: keyChoice, text: "beta"}}}
	value, err := pickTerminal(context.Background(), terminal, "Example", []choice{{"alpha", "Alpha"}, {"beta", "Beta"}}, "")
	if err != nil || value != "beta" || len(terminal.frames) != 3 {
		t.Fatalf("clicked hidden choice accepted: %q %v frames=%d", value, err, len(terminal.frames))
	}
	view := terminal.frames[len(terminal.frames)-1]
	choices := 0
	for _, line := range view.Lines {
		if line.Action == "choice:beta" {
			choices++
		}
		if line.Action == "choice:alpha" {
			t.Fatal("filtered choice has a hit target")
		}
	}
	if choices != 1 {
		t.Fatal("visible menu choice is not clickable")
	}
}

func TestSessionUIOldObserverCannotCompleteOrRebindNextPrompt(t *testing.T) {
	_, p := shellHarness(t, 80, 24, false)
	p.Send(terminalJobMsg{TaskID: "task_same", AttemptID: "attempt_old", Phase: jobReceiving})
	p.Send(terminalJobMsg{TaskID: "task_same", Phase: jobPreparing, Title: "Next prompt"})
	p.Send(terminalJobMsg{TaskID: "task_same", AttemptID: "attempt_old", Phase: jobSaved, State: "complete"})
	job, _ := p.model.jobs.selected("task_same")
	if job.Phase != jobPreparing || job.AttemptID != "" || p.model.mascot.id == "done" {
		t.Fatalf("old completion applied to preparation: %+v", job)
	}
	p.Send(terminalJobMsg{TaskID: "task_same", AttemptID: "attempt_old", Phase: jobAccepted})
	p.Send(terminalJobMsg{TaskID: "task_same", AttemptID: "attempt_new", Phase: jobAccepted, Bind: true})
	p.Send(terminalJobMsg{TaskID: "task_same", AttemptID: "attempt_old", Phase: jobAccepted})
	p.Send(terminalJobMsg{TaskID: "task_same", AttemptID: "attempt_old", Phase: jobSaved, State: "failed"})
	job, _ = p.model.jobs.selected("task_same")
	if job.Phase != jobAccepted || job.AttemptID != "attempt_new" || job.Title != "Next prompt" {
		t.Fatalf("old observer rebound next attempt: %+v", job)
	}
	p.Send(terminalJobMsg{TaskID: "task_same", AttemptID: "attempt_new", Phase: jobSaved, State: "complete"})
	job, _ = p.model.jobs.selected("task_same")
	if job.Phase != jobSaved || job.State != "complete" {
		t.Fatal("the bound new result could not finish")
	}
	p.Send(terminalJobMsg{TaskID: "task_same", Phase: jobPreparing})
	p.Send(terminalJobMsg{TaskID: "task_same", Phase: jobBlocked, Detail: "Preflight unavailable"})
	p.Send(terminalJobMsg{TaskID: "task_same", AttemptID: "attempt_new", Phase: jobSaved, State: "complete"})
	job, _ = p.model.jobs.selected("task_same")
	if job.Phase != jobBlocked || job.AttemptID != "" {
		t.Fatal("previous result replaced an unbound blocked request")
	}
}

func TestSessionUIClickOnlySpansRetainSelectionHighlight(t *testing.T) {
	line := terminalLine{Text: "> Example step", Focused: true}
	before := terminalStyle(line, 80, true, themeDark)
	line.Spans = []terminalSpan{{Start: 0, End: 14, Action: "choice:example"}}
	if terminalStyle(line, 80, true, themeDark) != before {
		t.Fatal("adding a click target removed the selected row's highlight")
	}
}
