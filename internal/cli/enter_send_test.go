package cli

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

func newEnterSession(t *testing.T, demo bool) (*session, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	a, err := app.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	o := newOutput(io.Discard, io.Discard, false, cancel)
	t.Cleanup(func() {
		if err := o.finish(); err != nil {
			t.Error(err)
		}
	})
	u := &terminalUI{program: &composerFrameCollector{}, events: make(chan keyEvent, 32), done: make(chan struct{}), interrupt: cancel}
	u.width.Store(80)
	u.height.Store(24)
	return &session{app: a, cwd: t.TempDir(), selection: core.Defaults(), demo: demo, terminal: u, out: o}, ctx
}

func TestEnterSendsReviewedCompositionExactlyOnce(t *testing.T) {
	for _, test := range []struct {
		name, seed, want string
		events           []keyEvent
	}{
		{"ordinary", "", "One message", []keyEvent{{kind: keyText, text: "One message"}}},
		{"multiline", "", "First line\nSecond line", []keyEvent{{kind: keyText, text: "First line"}, {kind: keyAltEnter}, {kind: keyText, text: "Second line"}}},
		{"literal-paste", "", "/exit\n/send remains text", []keyEvent{{kind: keyPaste, text: "/exit\n/send remains text"}}},
		{"restored-literal", "/send remains restored text", "/send remains restored text", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, ctx := newEnterSession(t, true)
			if test.seed != "" {
				task, err := s.app.Draft(ctx, "", test.seed, s.cwd, s.selection)
				if err != nil {
					t.Fatal(err)
				}
				s.task = &task
			}
			for _, event := range test.events {
				s.terminal.enqueue(event)
			}
			s.terminal.enqueue(keyEvent{kind: keyEnter})
			line := s.readTerminalLine(ctx)
			if line.err != nil || !line.complete || !line.replace || line.text != test.want {
				t.Fatalf("Enter did not return the full reviewed message: %+v", line)
			}
			tasks, err := s.app.Tasks(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range tasks {
				_, attempts, err := s.app.Task(ctx, task.ID)
				if err != nil || len(attempts) != 0 {
					t.Fatalf("editing dispatched before the submit boundary: %+v %v", attempts, err)
				}
			}
			if err := s.submitPrompt(ctx, line.text, line.replace); err != nil {
				t.Fatal(err)
			}
			id := s.task.ID
			if err := s.submitPrompt(ctx, "", true); err != nil {
				t.Fatal(err)
			}
			stored, attempts, err := s.app.Task(ctx, id)
			if err != nil || stored.Draft != "" || len(attempts) != 1 || attempts[0].State != "complete" || attempts[0].Input != test.want {
				t.Fatalf("one Enter/blank Enter produced the wrong durable result: %+v %+v %v", stored, attempts, err)
			}
		})
	}
}

func TestEnterPrerequisiteFailureKeepsMessageForExplicitRetry(t *testing.T) {
	s, ctx := newEnterSession(t, false)
	const prompt = "Keep this prompt through missing native setup"
	if err := s.submitPrompt(ctx, prompt, true); err == nil || problemFor(err).Exit != 3 {
		t.Fatalf("expected native prerequisite: %v", err)
	}
	id := s.task.ID
	stored, attempts, err := s.app.Task(ctx, id)
	if err != nil || stored.Draft != prompt || len(attempts) != 0 {
		t.Fatalf("failed submission lost input or claimed acceptance: %+v %+v %v", stored, attempts, err)
	}
	// Enabling a local fixture does not itself resend the saved message.
	s.demo = true
	if err := s.submitPrompt(ctx, " \t ", false); err != nil {
		t.Fatal(err)
	}
	_, attempts, err = s.app.Task(ctx, id)
	if err != nil || len(attempts) != 0 {
		t.Fatal("blank Enter submitted a hidden pending message")
	}
	if err := s.sendDraft(ctx); err != nil {
		t.Fatal(err)
	}
	stored, attempts, err = s.app.Task(ctx, id)
	if err != nil || stored.Draft != "" || len(attempts) != 1 || attempts[0].Input != prompt {
		t.Fatalf("explicit retry changed task or input: %+v %+v %v", stored, attempts, err)
	}
}

func TestEnterConflictKeepsSeparateUnsentRecoveryAndBlocksResend(t *testing.T) {
	s, ctx := newEnterSession(t, true)
	seed, err := s.app.Draft(ctx, "", "Original message", s.cwd, s.selection)
	if err != nil {
		t.Fatal(err)
	}
	s.task = &seed
	changed, err := s.app.Draft(ctx, seed.ID, "External edit", s.cwd, seed.Selection)
	if err != nil {
		t.Fatal(err)
	}
	const replacement = "Reviewed stale composition"
	if err := s.submitPrompt(ctx, replacement, true); err == nil || problemFor(err).Exit != 5 || s.recoveryID == "" {
		t.Fatalf("stale Enter was not fenced: %v recovery=%q", err, s.recoveryID)
	}
	if err := s.submitPrompt(ctx, replacement, true); err == nil {
		t.Fatal("second Enter silently resubmitted a conflict")
	}
	original, attempts, err := s.app.Task(ctx, seed.ID)
	if err != nil || original.Draft != changed.Draft || original.Revision != changed.Revision || len(attempts) != 0 {
		t.Fatalf("stale Enter overwrote the current task: %+v %+v %v", original, attempts, err)
	}
	recovery, attempts, err := s.app.Task(ctx, s.recoveryID)
	if err != nil || recovery.Draft != replacement || len(attempts) != 0 {
		t.Fatalf("separate recovery was not durable and unsent: %+v %+v %v", recovery, attempts, err)
	}
}

func TestRestoredComposerEOFDoesNotSubmitOrDuplicateMessage(t *testing.T) {
	s, ctx := newEnterSession(t, true)
	seed, err := s.app.Draft(ctx, "", "/send existing literal text", s.cwd, s.selection)
	if err != nil {
		t.Fatal(err)
	}
	s.task = &seed
	s.terminal.enqueue(keyEvent{kind: keyText, text: " plus edit"})
	s.terminal.enqueue(keyEvent{kind: keyEOF})
	line := s.readTerminalLine(ctx)
	if !errors.Is(line.err, io.EOF) || !line.replace || line.complete || line.text != seed.Draft+" plus edit" {
		t.Fatalf("EOF lost or submitted restored text: %+v", line)
	}
	if err := s.storeDraft(ctx, line.text, false); err != nil {
		t.Fatal(err)
	}
	stored, attempts, err := s.app.Task(ctx, seed.ID)
	if err != nil || stored.Draft != line.text || len(attempts) != 0 {
		t.Fatalf("EOF appended the seed twice or dispatched: %+v %+v %v", stored, attempts, err)
	}
	revision := stored.Revision
	if err := s.storeDraft(ctx, line.text, false); err != nil {
		t.Fatal(err)
	}
	stored, _, err = s.app.Task(ctx, seed.ID)
	if err != nil || stored.Revision != revision {
		t.Fatal("closing unchanged restored text rewrote its saved revision")
	}
}

func TestInterruptConfirmationPreservesWorkAndRejectsStaleTimers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := &terminalUI{events: make(chan keyEvent, 8), done: make(chan struct{}), interrupt: cancel}
	u.width.Store(80)
	u.height.Store(24)
	m := terminalModel{ui: u, shell: terminalShell{enabled: true, notice: terminalNotice{Text: "Working", Kind: toneInfo}}}
	now := time.Unix(100, 0)
	if m.confirmInterrupt(now) == nil || ctx.Err() != nil {
		t.Fatal("first Ctrl+C stopped work or omitted its expiry")
	}
	firstGeneration := m.interruptGeneration
	updated, _ := m.Update(terminalNoticeMsg{Text: "Newer progress", Kind: toneInfo})
	m = updated.(terminalModel)
	if m.interruptUntil.IsZero() || m.shell.notice.Text != "Newer progress" {
		t.Fatal("progress disarmed confirmation or lost the latest notice")
	}
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	m = updated.(terminalModel)
	if !m.interruptUntil.IsZero() || ctx.Err() != nil {
		t.Fatal("typing failed to disarm without cancellation")
	}
	if event, err := u.next(ctx); err != nil || event.text != "x" {
		t.Fatal("confirmation swallowed the editing key")
	}
	m.confirmInterrupt(now.Add(time.Second))
	updated, _ = m.Update(interruptConfirmExpiredMsg{Generation: firstGeneration})
	m = updated.(terminalModel)
	if m.interruptUntil.IsZero() {
		t.Fatal("stale deadline canceled a newer confirmation")
	}
	updated, _ = m.Update(interruptConfirmExpiredMsg{Generation: m.interruptGeneration})
	m = updated.(terminalModel)
	if !m.interruptUntil.IsZero() || ctx.Err() != nil || m.shell.notice.Text != "Newer progress" {
		t.Fatal("expiry canceled work or failed to restore current progress")
	}
	m.confirmInterrupt(now.Add(4 * time.Second))
	m.confirmInterrupt(now.Add(4*time.Second + time.Millisecond))
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("second confirmed Ctrl+C did not cancel")
	}
}
