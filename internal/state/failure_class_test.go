package state

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"ucli.local/ucli/internal/core"
)

func quotaFailure() core.FailureClass {
	return core.FailureClass{Code: "claude_quota", Origin: "claude", Acceptance: "accepted", SideEffects: "unknown", Exit: 4}
}

func TestClassifiedFailureSurvivesRestartReplayAndCannotBeReplaced(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	cmd := command("classified-replay", "workspace", "read-only")
	a := accept(t, s, cmd)
	failure := quotaFailure()
	finished, err := s.FinishClassified(background, a.ID, a.Owner.ID, "failed", "partial output", "Sanitized quota message.", "", &failure)
	if err != nil || finished.Failure == nil || *finished.Failure != failure {
		t.Fatalf("finish=%+v err=%v", finished, err)
	}
	// The receipt owns a copy, not caller storage that can change after commit.
	failure.Exit = 9
	if finished.Failure.Exit != 4 {
		t.Fatal("caller mutation changed returned durable receipt")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, home)
	got, newly, err := s.Accept(background, cmd)
	if err != nil || newly || !reflect.DeepEqual(got, finished) {
		t.Fatalf("restart replay changed terminal class: %+v new=%v err=%v", got, newly, err)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"exit":4`) {
		t.Fatal("failure exit omitted from durable JSON")
	}
	want := quotaFailure()
	if _, err = s.FinishClassified(background, a.ID, a.Owner.ID, "failed", "partial output", "Sanitized quota message.", "", &want); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*core.FailureClass){func(f *core.FailureClass) { f.Exit = 8 }, func(f *core.FailureClass) { f.Code = "provider_failed" }, func(f *core.FailureClass) { f.Origin = "other" }, func(f *core.FailureClass) { f.Acceptance = "unknown" }, func(f *core.FailureClass) { f.SideEffects = "none" }} {
		changed := want
		change(&changed)
		_, err = s.FinishClassified(background, a.ID, a.Owner.ID, "failed", "partial output", "Sanitized quota message.", "", &changed)
		requireCode(t, err, "invalid_state")
	}
	_, err = s.FinishWithCause(background, a.ID, a.Owner.ID, "failed", "partial output", "Sanitized quota message.", "")
	requireCode(t, err, "invalid_state")
	events, err := s.Events(background, a.ID, 0, 100)
	if err != nil || len(events) != 3 || events[1].Type != "terminal_result" || events[2].Type != "result_persisted" {
		t.Fatalf("duplicate terminal events: %+v %v", events, err)
	}
}

func TestFailureMetadataBoundsAndLegacyNilReceipts(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("class-bounds", "workspace", "read-only"))
	valid := quotaFailure()
	for _, state := range []string{"complete", "interrupted", "unknown", "running"} {
		_, err := s.FinishClassified(background, a.ID, a.Owner.ID, state, "", "", "", &valid)
		requireCode(t, err, "invalid_input")
	}
	for _, exit := range []int{-1, 0, 1, 7, 124, 130, 255} {
		bad := valid
		bad.Exit = exit
		_, err := s.FinishClassified(background, a.ID, a.Owner.ID, "failed", "", "", "", &bad)
		requireCode(t, err, "invalid_input")
	}
	for _, change := range []func(*core.FailureClass){func(f *core.FailureClass) { f.Code = "" }, func(f *core.FailureClass) { f.Code = strings.Repeat("a", 129) }, func(f *core.FailureClass) { f.Code = "secret\ncode" }, func(f *core.FailureClass) { f.Origin = " " }, func(f *core.FailureClass) { f.Origin = "\x1b[0m" }, func(f *core.FailureClass) { f.Acceptance = "" }, func(f *core.FailureClass) { f.Acceptance = strings.Repeat("a", 65) }, func(f *core.FailureClass) { f.SideEffects = "bad\tvalue" }, func(f *core.FailureClass) { f.SideEffects = "bad\u2028value" }, func(f *core.FailureClass) { f.SideEffects = string([]byte{0xff}) }} {
		bad := valid
		change(&bad)
		_, err := s.FinishClassified(background, a.ID, a.Owner.ID, "failed", "", "", "", &bad)
		requireCode(t, err, "invalid_input")
	}
	still, err := s.GetAttempt(background, a.ID)
	if err != nil || still.State != "running" || still.Failure != nil {
		t.Fatalf("invalid class changed receipt: %+v %v", still, err)
	}
	old, err := s.Finish(background, a.ID, a.Owner.ID, "failed", "", "Legacy failure.")
	if err != nil || old.Failure != nil {
		t.Fatalf("legacy failure changed: %+v %v", old, err)
	}
	_, err = s.FinishWithCause(background, a.ID, a.Owner.ID, "failed", "", "Legacy failure.", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.FinishClassified(background, a.ID, a.Owner.ID, "failed", "", "Legacy failure.", "", &valid)
	requireCode(t, err, "invalid_state")
	raw, _ := json.Marshal(old)
	if strings.Contains(string(raw), `"failure"`) {
		t.Fatal("nil classification changed historical JSON shape")
	}
}

func TestFailureClassAndTerminalEventsRollbackAtomically(t *testing.T) {
	s := openTest(t, t.TempDir())
	a := accept(t, s, command("class-rollback", "workspace", "workspace-write"))
	failure := quotaFailure()
	_, err := s.db.Exec("CREATE TEMP TRIGGER synthetic_class_failure BEFORE INSERT ON events WHEN json_extract(NEW.body,'$.type')='terminal_result' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.FinishClassified(background, a.ID, a.Owner.ID, "failed", "partial", "Sanitized failure.", "", &failure)
	requireCode(t, err, "state_unavailable")
	got, err := s.GetAttempt(background, a.ID)
	if err != nil || got.State != "running" || got.Failure != nil || got.Error != "" || got.Output != "" {
		t.Fatalf("partial classified receipt escaped rollback: %+v %v", got, err)
	}
	events, err := s.Events(background, a.ID, 0, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("terminal event escaped rollback: %+v %v", events, err)
	}
	var leases int
	if err = s.db.QueryRow("SELECT count(*) FROM leases WHERE attempt_id=?", a.ID).Scan(&leases); err != nil || leases != 1 {
		t.Fatalf("lease escaped rollback: %d %v", leases, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER synthetic_class_failure"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishClassified(background, a.ID, a.Owner.ID, "failed", "partial", "Sanitized failure.", "", &failure); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("SELECT count(*) FROM leases WHERE attempt_id=?", a.ID).Scan(&leases); err != nil || leases != 0 {
		t.Fatalf("confirmed failure retained lease: %d %v", leases, err)
	}
}
