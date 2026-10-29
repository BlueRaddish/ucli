package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

func permissionSession(t *testing.T, input, draft string, demo bool) (*session, *bytes.Buffer) {
	t.Helper()
	a, err := app.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	selection := core.Selection{Model: "claude/fixture", Account: "pinned-account", Effort: "high", Permission: "unrestricted"}
	if demo {
		selection.Model, selection.Account, selection.Effort = "demo/fast", "demo", "low"
	}
	cwd := t.TempDir()
	task, err := a.Draft(context.Background(), "", draft, cwd, selection)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	lines := newLineInput(strings.NewReader(input))
	t.Cleanup(func() { close(lines.stop) })
	o := newOutput(&output, &output, false, func() {})
	t.Cleanup(func() { _ = o.finish() })
	return &session{app: a, cwd: cwd, demo: demo, out: o, input: lines, selection: selection, task: &task}, &output
}

func TestPermissionMenuUnsupportedPinCancelPreservesDraft(t *testing.T) {
	s, output := permissionSession(t, "4\n0\n", "keep my prompt", false)
	before := *s.task
	if err := s.selectSetting(context.Background(), "/permission"); err != nil {
		t.Fatal(err)
	}
	_ = s.out.finish()
	saved, attempts, err := s.app.Task(context.Background(), before.ID)
	if err != nil || !reflect.DeepEqual(saved, before) || s.selection != before.Selection || len(attempts) != 0 {
		t.Fatal("invalid old menu row or Back changed the restored permission/draft")
	}
	text := output.String()
	if !strings.Contains(text, "unrestricted is unsupported") || !strings.Contains(text, "Choose a listed number") || strings.Contains(text, "workspace-write -") || strings.Contains(text, "4. unrestricted") || !strings.Contains(text, "full local computer access") {
		t.Fatalf("menu offered unimplemented authority or hid recovery: %s", text)
	}
}

func TestPermissionExplicitChoicePersistsWithoutDispatch(t *testing.T) {
	for _, draft := range []string{"keep my prompt", ""} {
		t.Run(draft, func(t *testing.T) {
			s, _ := permissionSession(t, "2\n", draft, false)
			before := *s.task
			if err := s.selectSetting(context.Background(), "/permission"); err != nil {
				t.Fatal(err)
			}
			saved, attempts, err := s.app.Task(context.Background(), before.ID)
			want := before.Selection
			want.Permission = "sandbox"
			if err != nil || saved.Selection != want || s.selection != want || saved.Draft != draft || saved.Revision != before.Revision+1 || len(attempts) != 0 {
				t.Fatalf("explicit permission choice was not one durable change: %+v %v", saved, err)
			}
		})
	}
}

func TestPermissionChoiceCASConflictDoesNotClaimNewPin(t *testing.T) {
	s, _ := permissionSession(t, "2\n", "older prompt", false)
	before := *s.task
	newer, err := s.app.Draft(context.Background(), before.ID, "newer prompt", s.cwd, before.Selection)
	if err != nil {
		t.Fatal(err)
	}
	err = s.selectSetting(context.Background(), "/permission")
	var problem *core.Error
	if !errors.As(err, &problem) || problem.Code != "revision_conflict" || problem.TaskID == "" || s.selection != before.Selection || s.task.Revision != before.Revision {
		t.Fatalf("failed permission save was presented as selected: %v", err)
	}
	saved, attempts, err := s.app.Task(context.Background(), before.ID)
	if err != nil || !reflect.DeepEqual(saved, newer) || len(attempts) != 0 {
		t.Fatal("permission CAS conflict overwrote newer work")
	}
	recovery, attempts, err := s.app.Task(context.Background(), problem.TaskID)
	if err != nil || recovery.Draft != before.Draft || recovery.Selection.Permission != "sandbox" || len(attempts) != 0 {
		t.Fatal("proposed permission and old text were not preserved as an unsent recovery draft")
	}
}

func TestPermissionRecoveryAllowsExplicitSafeDemoSend(t *testing.T) {
	s, _ := permissionSession(t, "2\n1\n", "synthetic safe recovery", true)
	err := s.sendDraft(context.Background())
	if err == nil || problemFor(err).Code != "unsupported_permission" {
		t.Fatalf("unsupported restored draft unexpectedly ran: %v", err)
	}
	if err := s.selectSetting(context.Background(), "/permission"); err != nil {
		t.Fatal(err)
	}
	// Demo must reject old native row 2 rather than offer broad authority.
	if s.selection.Permission != "read-only" {
		t.Fatal("demo permission picker admitted broad authority")
	}
	if err := s.sendDraft(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, attempts, err := s.app.Task(context.Background(), s.task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].State != "complete" || attempts[0].Input != "synthetic safe recovery" || attempts[0].Requested.Permission != "read-only" || attempts[0].Effective.Permission != "read-only" {
		t.Fatalf("explicit recovery did not send exactly once: %+v %v", attempts, err)
	}
}

func TestRichSendPermissionRecoveryNeverDispatchesOnChoiceOrCancel(t *testing.T) {
	for _, mode := range []string{"cancel", "read-only", "sandbox"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx := newEnterSession(t, false)
			s.observeCtx = ctx
			s.selection = core.Selection{Model: "claude/fixture", Account: "pinned-account", Effort: "high", Permission: "unrestricted"}
			if err := s.storeDraft(ctx, "preserve this rich prompt", false); err != nil {
				t.Fatal(err)
			}
			before := *s.task
			if mode == "cancel" {
				s.terminal.enqueue(keyEvent{kind: keyEscape})
			} else {
				if mode == "sandbox" {
					s.terminal.enqueue(keyEvent{kind: keyDown})
				}
				s.terminal.enqueue(keyEvent{kind: keyEnter})
			}
			if err := s.sendDraft(ctx); err != nil {
				t.Fatal(err)
			}
			saved, attempts, err := s.app.Task(ctx, before.ID)
			want, revision := before.Selection, before.Revision
			if mode != "cancel" {
				want.Permission = mode
				revision++
			}
			if err != nil || saved.Selection != want || s.selection != want || saved.Revision != revision || saved.Draft != before.Draft || len(attempts) != 0 || len(s.runs) != 0 {
				t.Fatalf("permission recovery dispatched or changed unrelated context: %+v %v", saved, err)
			}
		})
	}
}

func TestRichSendBusyGuardPrecedesPermissionRecovery(t *testing.T) {
	s, ctx, _, accepted, _ := managedUIFixture(t)
	s.selection.Permission = "unrestricted"
	if err := s.storeDraft(ctx, "next prompt stays unsent", false); err != nil {
		t.Fatal(err)
	}
	err := s.sendDraft(ctx)
	if err == nil || problemFor(err).Code != "task_busy" {
		t.Fatalf("permission recovery bypassed busy guard: %v", err)
	}
	saved, attempts, err := s.app.Task(ctx, accepted.TaskID)
	if err != nil || saved.Draft != "next prompt stays unsent" || saved.Selection.Permission != "unrestricted" || len(attempts) != 1 || attempts[0].Effective.Permission != accepted.Effective.Permission {
		t.Fatal("busy recovery retargeted the accepted attempt or lost the draft")
	}
}
