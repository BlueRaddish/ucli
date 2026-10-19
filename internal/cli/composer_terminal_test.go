package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

func TestComposerViewportPreservesFullDraft(t *testing.T) {
	e := textEditor{}
	e.set("first\nsecond\n" + strings.Repeat("中", 60) + "e\u0301")
	before := e
	lines, row, col := editorFrame(e, 30, 2)
	if len(lines) != 2 || row != 1 || col >= 30 || !strings.HasPrefix(lines[1].Text, "  ‹") {
		t.Fatalf("offscreen cursor: %#v %d %d", lines, row, col)
	}
	if e != before {
		t.Fatal("display truncated draft")
	}
	_ = e.apply(keyEvent{kind: keyUp})
	if e.beforeCursor() != "first\nsecond" {
		t.Fatalf("vertical move: %q", e.beforeCursor())
	}
	_ = e.apply(keyEvent{kind: keyHome})
	_ = e.apply(keyEvent{kind: keyDown})
	if e.beforeCursor() != "first\nsecond\n" {
		t.Fatalf("vertical home: %q", e.beforeCursor())
	}
}

type composerFrameCollector struct{ frames []terminalFrame }

func (p *composerFrameCollector) Send(msg tea.Msg) {
	if frame, ok := msg.(terminalFrameMsg); ok {
		p.frames = append(p.frames, terminalFrame(frame))
	}
}
func (p *composerFrameCollector) Quit() {}

func TestComposerCleanupFailureStopsTabActionsAndPreservesComposition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	collector := &composerFrameCollector{}
	terminal := &terminalUI{program: collector, events: make(chan keyEvent, 16), done: make(chan struct{}), interrupt: cancel}
	terminal.width.Store(80)
	terminal.height.Store(24)
	const composition = "/send is still unsent literal composition"
	for _, event := range []keyEvent{{kind: keyText, text: composition}, {kind: keyTab}, {kind: keyText, text: "Connect account"}, {kind: keyEnter}, {kind: keyTab}, {kind: keyText, text: "Connect account"}, {kind: keyEnter}} {
		terminal.enqueue(event)
	}
	var output bytes.Buffer
	o := newOutput(&output, &output, false, cancel)
	a, err := app.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cwd := t.TempDir()
	seed, err := a.Draft(ctx, "", "Existing draft", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	s := &session{app: a, task: &seed, cwd: cwd, selection: seed.Selection, terminal: terminal, out: o}
	cleanup := core.Problem("login_cleanup_unconfirmed", "Synthetic owned sign-in cleanup is unconfirmed.", 9)
	calls := 0
	result := s.readTerminalLineWithAction(ctx, func(_ context.Context, action string) (bool, error) {
		calls++
		if action != "/login" {
			t.Fatalf("wrong menu action %q", action)
		}
		return false, cleanup
	})
	if result.err != cleanup || result.text != composition || !result.literal || result.complete || calls != 1 || len(terminal.events) != 3 || ctx.Err() != nil {
		t.Fatalf("cleanup failure allowed continuation or lost composition: %+v calls=%d queued=%d", result, calls, len(terminal.events))
	}
	// Consume the returned partial text through the session's actual durable
	// draft path. The outer interactive loop does this before returning exit9.
	if err = s.appendDraft(ctx, result.text); err != nil {
		t.Fatal(err)
	}
	stored, attempts, err := a.Task(ctx, seed.ID)
	if err != nil || stored.Draft != "Existing draft\n"+composition || stored.Selection != seed.Selection || len(attempts) != 0 {
		t.Fatalf("fatal menu return did not preserve literal unsent input: %+v %+v %v", stored, attempts, err)
	}
	if problemFor(result.err).Exit != 9 {
		t.Fatal("Tab route changed the typed action's fatal cleanup exit")
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestComposerRecoverableActionStillReturnsToDraft(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	terminal := &terminalUI{program: &composerFrameCollector{}, events: make(chan keyEvent, 8), done: make(chan struct{}), interrupt: cancel}
	terminal.width.Store(80)
	terminal.height.Store(24)
	for _, event := range []keyEvent{{kind: keyText, text: "kept"}, {kind: keyTab}, {kind: keyText, text: "Connect account"}, {kind: keyEnter}, {kind: keyEOF}} {
		terminal.enqueue(event)
	}
	var output bytes.Buffer
	o := newOutput(&output, &output, false, cancel)
	s := &session{selection: core.Defaults(), terminal: terminal, out: o}
	result := s.readTerminalLineWithAction(ctx, func(context.Context, string) (bool, error) {
		return false, core.Problem("sign_in_required", "Synthetic sign-in prerequisite.", 3)
	})
	if !errors.Is(result.err, io.EOF) || result.text != "kept" {
		t.Fatalf("recoverable prerequisite closed the composer: %+v", result)
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestComposerTypedAndTabFatalActionParity(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	// Damage only UCLI's synthetic connection metadata. Both paths must stop
	// before provider discovery or login, rather than consume the next action.
	directory := filepath.Join(home, "connections-v1")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "damaged.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	exit, _ := runInteractive(t, home, cwd, "Saved draft\n/accounts\nMust not be accepted\n", true)
	if exit != 9 {
		t.Fatalf("typed fatal action exit=%d", exit)
	}
	tasks := savedTasks(t, home)
	if len(tasks) != 1 || tasks[0].Draft != "Saved draft" {
		t.Fatalf("typed fatal action continued: %+v", tasks)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	terminal := &terminalUI{program: &composerFrameCollector{}, events: make(chan keyEvent, 8), done: make(chan struct{}), interrupt: cancel}
	terminal.width.Store(80)
	terminal.height.Store(24)
	for _, event := range []keyEvent{{kind: keyText, text: "Unsent fragment"}, {kind: keyTab}, {kind: keyText, text: "Accounts"}, {kind: keyEnter}, {kind: keyText, text: "Must not be accepted"}} {
		terminal.enqueue(event)
	}
	var output bytes.Buffer
	o := newOutput(&output, &output, false, cancel)
	s := &session{app: a, cwd: cwd, task: &tasks[0], selection: tasks[0].Selection, terminal: terminal, out: o}
	result := s.readTerminalLine(ctx)
	if result.err == nil || problemFor(result.err).Exit != exit || result.text != "Unsent fragment" || !result.literal || len(terminal.events) != 1 {
		t.Fatalf("Tab fatal action differs from typed route: %+v", result)
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestComposerHintsFitWithoutPartialControls(t *testing.T) {
	for _, width := range []int{80, 60, 40, 24, 12, 5} {
		hint := composerHint(width)
		if cellWidth(hint) >= width || (width >= 12 && !strings.Contains(hint, "Tab actions")) {
			t.Fatalf("width %d clips discovery: %q", width, hint)
		}
		if width >= 60 && !strings.Contains(hint, "Ctrl+C exit") {
			t.Fatalf("width %d loses complete exit hint: %q", width, hint)
		}
	}
}
