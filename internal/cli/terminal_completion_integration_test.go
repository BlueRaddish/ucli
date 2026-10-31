package cli

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

type completionComposerCapture struct {
	frames []terminalFrame
	menus  int
}

func (p *completionComposerCapture) Send(message tea.Msg) {
	switch value := message.(type) {
	case terminalComposerMsg:
		p.frames = append(p.frames, terminalFrame(value))
	case terminalFrameMsg:
		if len(value.Lines) > 0 {
			p.menus++
		}
	}
}
func (p *completionComposerCapture) Quit() {}

// Consume the real composer loop and editor with queued terminal events. The
// action seam records accidental menu dispatch without native or store effects.
func driveCompletionComposer(t *testing.T, width, height int, events ...keyEvent) (line, []terminalFrame) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	capture := &completionComposerCapture{}
	terminal := &terminalUI{program: capture, events: make(chan keyEvent, 64), done: make(chan struct{}), interrupt: cancel}
	terminal.width.Store(int64(width))
	terminal.height.Store(int64(height))
	// These cases intentionally replace the restored message before entering a
	// command. Clearing the editor alone must not mutate its saved counterpart.
	terminal.enqueue(keyEvent{kind: keyClear})
	for _, event := range events {
		terminal.enqueue(event)
	}
	o := newOutput(io.Discard, io.Discard, false, cancel)
	t.Cleanup(func() {
		if err := o.finish(); err != nil {
			t.Error(err)
		}
	})
	selection := core.Defaults()
	selection.Model, selection.Effort = "demo/deep", "high"
	task := core.Task{ID: "unchanged-task", Revision: 7, Draft: "Previously saved draft", Selection: selection}
	before := task
	s := &session{task: &task, selection: selection, cwd: t.TempDir(), terminal: terminal, out: o}
	actions := 0
	result := s.readTerminalLineWithAction(ctx, func(context.Context, string) (bool, error) {
		actions++
		return false, nil
	})
	if actions != 0 || capture.menus != 0 {
		t.Fatalf("completion opened or dispatched an unrelated action: calls=%d menus=%d", actions, capture.menus)
	}
	if !reflect.DeepEqual(*s.task, before) || s.selection != selection {
		t.Fatal("completion changed the selected task, saved draft, revision or pins")
	}
	if len(terminal.events) != 0 {
		t.Fatalf("composer returned before consuming the intended route: %d events left", len(terminal.events))
	}
	return result, capture.frames
}

func completionFrameFor(t *testing.T, frames []terminalFrame, query string, visible bool) terminalFrame {
	t.Helper()
	for _, frame := range frames {
		if frame.CompletionQuery == query && frame.Completion.Visible == visible {
			return frame
		}
	}
	t.Fatalf("no composer frame for query %q visible=%v", query, visible)
	return terminalFrame{}
}

func completionFooter(t *testing.T, frame terminalFrame) string {
	t.Helper()
	if len(frame.Lines) < 2 {
		t.Fatal("test expected an editor and contextual footer")
	}
	return frame.Lines[len(frame.Lines)-1].Text
}

func TestCompletionComposerPartialKeysOnlyComplete(t *testing.T) {
	for _, key := range []keyKind{keyEnter, keyTab} {
		t.Run(map[keyKind]string{keyEnter: "Enter", keyTab: "Tab"}[key], func(t *testing.T) {
			result, frames := driveCompletionComposer(t, 80, 24,
				keyEvent{kind: keyText, text: "/mo"}, keyEvent{kind: key}, keyEvent{kind: keyEOF})
			if !errors.Is(result.err, io.EOF) || result.complete || result.text != "/models " {
				t.Fatalf("partial command executed or lost its completed text: %+v", result)
			}
			hint := completionFooter(t, completionFrameFor(t, frames, "/mo", true))
			if !strings.Contains(hint, "Enter/Tab complete") || strings.Contains(hint, "? shortcuts") || strings.Contains(hint, "Enter save") {
				t.Fatalf("partial-command footer describes the wrong key action: %q", hint)
			}
			if !strings.Contains(completionFooter(t, completionFrameFor(t, frames, "/models ", false)), "? shortcuts") {
				t.Fatal("normal editor hint did not return after completion")
			}
		})
	}
}

func TestCompletionComposerExactEnterReturnsExistingCommandPath(t *testing.T) {
	result, frames := driveCompletionComposer(t, 80, 24,
		keyEvent{kind: keyText, text: "/send"}, keyEvent{kind: keyEnter})
	if result.err != nil || !result.complete || result.literal || result.text != "/send" {
		t.Fatalf("fully typed command lost its original Enter semantics: %+v", result)
	}
	hint := completionFooter(t, completionFrameFor(t, frames, "/send", true))
	if !strings.Contains(hint, "Enter run") || !strings.Contains(hint, "Tab complete") || strings.Contains(hint, "Enter save") {
		t.Fatalf("exact-command footer misstates dispatch versus completion: %q", hint)
	}
}

func TestCompletionComposerNoMatchAndEscapeKeepEditing(t *testing.T) {
	result, frames := driveCompletionComposer(t, 80, 24,
		keyEvent{kind: keyText, text: "/unknown-command"}, keyEvent{kind: keyTab}, keyEvent{kind: keyEOF})
	if !errors.Is(result.err, io.EOF) || result.complete || result.text != "/unknown-command" {
		t.Fatalf("no-match Tab changed or executed input: %+v", result)
	}
	frame := completionFrameFor(t, frames, "/unknown-command", true)
	if len(frame.Completion.Rows) != 0 || !strings.Contains(completionFooter(t, frame), "Keep typing") {
		t.Fatal("no-match state advertises selection or unrelated actions")
	}
	result, frames = driveCompletionComposer(t, 80, 24,
		keyEvent{kind: keyText, text: "/mo"}, keyEvent{kind: keyEscape}, keyEvent{kind: keyText, text: "d"}, keyEvent{kind: keyEOF})
	if !errors.Is(result.err, io.EOF) || result.text != "/mod" || result.complete {
		t.Fatalf("Escape discarded the query or stopped later editing: %+v", result)
	}
	completionFrameFor(t, frames, "/mo", false)
	completionFrameFor(t, frames, "/mod", true)
}

func TestCompletionComposerLiteralPasteBypassesCommands(t *testing.T) {
	result, frames := driveCompletionComposer(t, 80, 24,
		keyEvent{kind: keyPaste, text: "/send"}, keyEvent{kind: keyEnter})
	if result.err != nil || !result.complete || !result.literal || result.text != "/send" {
		t.Fatalf("pasted command-like text lost its literal boundary: %+v", result)
	}
	for _, frame := range frames {
		if frame.Completion.Visible {
			t.Fatal("literal paste displayed command suggestions")
		}
	}
}

func TestCompletionComposerCannotAcceptAnInvisibleSuggestion(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"one-row", 80, 1}, {"two-rows", 80, 2}, {"narrow", 5, 24}} {
		t.Run(size.name, func(t *testing.T) {
			result, frames := driveCompletionComposer(t, size.width, size.height,
				keyEvent{kind: keyText, text: "/mo"}, keyEvent{kind: keyEnter}, keyEvent{kind: keyTab}, keyEvent{kind: keyEOF})
			if !errors.Is(result.err, io.EOF) || result.complete || result.text != "/mo" {
				t.Fatalf("invisible completion was accepted: %+v", result)
			}
			frame := completionFrameFor(t, frames, "/mo", true)
			if len(frame.Lines) > 1 && !strings.Contains(completionFooter(t, frame), "Resize for commands") {
				t.Fatal("blocked selection lacks resize guidance where a hint fits")
			}
		})
	}
}

func TestCompletionDelimiterPreservesDraftArgumentBytes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := app.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cwd := t.TempDir()
	seed, err := a.Draft(ctx, "", "Existing saved text", cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	o := newOutput(io.Discard, io.Discard, false, cancel)
	t.Cleanup(func() {
		if err := o.finish(); err != nil {
			t.Error(err)
		}
	})
	s := &session{app: a, task: &seed, cwd: cwd, selection: seed.Selection, out: o}
	for _, action := range []string{"/demo ", "/help ", "/draft "} {
		if exit, err := s.action(ctx, action); err != nil || exit {
			t.Fatalf("completion delimiter was treated as an unwanted argument for %q: %v", action, err)
		}
	}
	if !s.demo || s.task.Draft != "Existing saved text" || s.task.Revision != seed.Revision {
		t.Fatal("delimiter-only /draft changed the saved text or revision")
	}
	const argument = "  explicit bytes stay  "
	if _, err := s.action(ctx, "/draft "+argument); err != nil {
		t.Fatal(err)
	}
	stored, attempts, err := a.Task(ctx, seed.ID)
	if err != nil || stored.Draft != argument || len(attempts) != 0 {
		t.Fatalf("argument normalization changed explicit draft bytes or started work: draft=%q attempts=%d err=%v", stored.Draft, len(attempts), err)
	}
}
