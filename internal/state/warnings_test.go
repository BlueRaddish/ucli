package state

import (
	"strings"
	"testing"
	"ucli.local/ucli/internal/core"
)

func TestWarningReceiptAndEventCommitTogether(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("warning", "workspace", "read-only"))
	_, err := s.db.Exec("CREATE TEMP TRIGGER warning_failure BEFORE INSERT ON events WHEN json_extract(NEW.body,'$.type')='warning' BEGIN SELECT RAISE(ABORT,'synthetic'); END")
	if err != nil {
		t.Fatal(err)
	}
	e := core.Event{AttemptID: a.ID, Type: "warning", Text: "synthetic cleanup warning"}
	if _, err = s.AppendEvent(background, e, a.Owner.ID); err == nil {
		t.Fatal("injected failure passed")
	}
	got, err := s.GetAttempt(background, a.ID)
	if err != nil || len(got.Warnings) != 0 {
		t.Fatal("warning receipt escaped rollback", got, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER warning_failure"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppendEvent(background, e, a.Owner.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.Finish(background, a.ID, a.Owner.ID, "complete", "confirmed output", "completed")
	if err != nil || len(got.Warnings) != 1 || got.Warnings[0] != e.Text || got.Error != "" {
		t.Fatal(got, err)
	}
	events, err := s.Events(background, a.ID, 0, 100)
	if err != nil || len(events) != 4 || events[1].Type != "warning" {
		t.Fatal(events, err)
	}
}

func TestWarningBoundsCannotGrowReceipt(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("warning-limits", "workspace", "read-only"))
	for i := 0; i < 16; i++ {
		if _, err := s.AppendEvent(background, core.Event{AttemptID: a.ID, Type: "warning", Text: "warning"}, a.Owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.AppendEvent(background, core.Event{AttemptID: a.ID, Type: "warning", Text: "extra"}, a.Owner.ID)
	requireCode(t, err, "invalid_input")
	b := accept(t, s, command("warning-bytes", "workspace-b", "read-only"))
	if _, err = s.AppendEvent(background, core.Event{AttemptID: b.ID, Type: "warning", Text: strings.Repeat("x", maxText)}, b.Owner.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.AppendEvent(background, core.Event{AttemptID: b.ID, Type: "warning", Text: "x"}, b.Owner.ID)
	requireCode(t, err, "invalid_input")
}
