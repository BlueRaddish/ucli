package state

import "testing"

func TestSuccessfulMessageIsEventTextNotError(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	a := accept(t, s, command("success-info", "workspace-a", "read-only"))
	const message = "Local echo completed; no model inference."
	finished, err := s.Finish(background, a.ID, a.Owner.ID, "complete", "synthetic output", message)
	if err != nil || finished.Error != "" {
		t.Fatalf("success became error: %+v %v", finished, err)
	}
	events, err := s.Events(background, a.ID, 0, 10)
	if err != nil || len(events) != 3 || events[1].Type != "terminal_result" || events[1].Text != message {
		t.Fatalf("completion info missing: %+v %v", events, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, home)
	replay, err := s.Finish(background, a.ID, a.Owner.ID, "complete", "synthetic output", message)
	if err != nil || replay.Error != "" || replay.ID != finished.ID {
		t.Fatalf("success replay: %+v %v", replay, err)
	}
	_, err = s.Finish(background, a.ID, a.Owner.ID, "complete", "synthetic output", "changed message")
	requireCode(t, err, "invalid_state")
	events, err = s.Events(background, a.ID, 0, 10)
	if err != nil || len(events) != 3 || events[1].Text != message {
		t.Fatalf("replay changed event: %+v %v", events, err)
	}
}

func TestNonSuccessMessagesRemainErrorAndEventText(t *testing.T) {
	for _, state := range []string{"failed", "interrupted", "unknown"} {
		t.Run(state, func(t *testing.T) {
			s := openTest(t, t.TempDir())
			a := accept(t, s, command("non-success", "workspace-a", "read-only"))
			finished, err := s.Finish(background, a.ID, a.Owner.ID, state, "partial", "reason for non-success")
			if err != nil || finished.Error != "reason for non-success" {
				t.Fatalf("error lost: %+v %v", finished, err)
			}
			events, err := s.Events(background, a.ID, 0, 10)
			if err != nil || len(events) != 3 || events[1].Text != finished.Error {
				t.Fatalf("event lost: %+v %v", events, err)
			}
		})
	}
}
