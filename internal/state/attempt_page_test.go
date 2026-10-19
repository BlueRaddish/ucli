package state

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
)

func finishHistory(t *testing.T, s *Store, cmd core.RunCommand) core.Attempt {
	t.Helper()
	a := accept(t, s, cmd)
	a, err := s.Finish(background, a.ID, a.Owner.ID, "complete", "synthetic history", "")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAttemptPagesNewestWindowWithChronologicalOrderAndDraftGaps(t *testing.T) {
	s := openTest(t, t.TempDir())
	cmd := command("page-0", "workspace-a", "read-only")
	var attempts []core.Attempt
	for i := 0; i < 5; i++ {
		cmd.ID = fmt.Sprint("page-", i)
		if i > 0 {
			task, err := s.GetTask(background, attempts[i-1].TaskID)
			if err != nil {
				t.Fatal(err)
			}
			task.Draft = "edited draft between attempts"
			task, err = s.SaveDraft(background, task, task.Revision)
			if err != nil {
				t.Fatal(err)
			}
			cmd.TaskID, cmd.ExpectedRevision = task.ID, task.Revision
		}
		attempts = append(attempts, finishHistory(t, s, cmd))
	}
	page, err := s.ListAttempts(background, attempts[0].TaskID, 0, 2)
	if err != nil || !reflect.DeepEqual(page.Attempts, attempts[3:]) || page.NextBeforeRevision != attempts[3].Revision {
		t.Fatalf("latest: %+v %v", page, err)
	}
	page, err = s.ListAttempts(background, attempts[0].TaskID, page.NextBeforeRevision, 2)
	if err != nil || !reflect.DeepEqual(page.Attempts, attempts[1:3]) || page.NextBeforeRevision != attempts[1].Revision {
		t.Fatalf("older: %+v %v", page, err)
	}
	page, err = s.ListAttempts(background, attempts[0].TaskID, page.NextBeforeRevision, 2)
	if err != nil || !reflect.DeepEqual(page.Attempts, attempts[:1]) || page.NextBeforeRevision != 0 {
		t.Fatalf("oldest: %+v %v", page, err)
	}
	page, err = s.ListAttempts(background, attempts[0].TaskID, attempts[0].Revision, 2)
	if err != nil || len(page.Attempts) != 0 || page.Attempts == nil || page.NextBeforeRevision != 0 {
		t.Fatalf("empty: %+v %v", page, err)
	}
	full, err := s.Attempts(background, attempts[0].TaskID)
	if err != nil || !reflect.DeepEqual(full, attempts) {
		t.Fatalf("full API changed: %+v %v", full, err)
	}
}

func TestAttemptCursorSurvivesOtherStoreAppendAndRestart(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	writer := openTest(t, home)
	cmd := command("stable-1", "workspace-a", "read-only")
	first := finishHistory(t, s, cmd)
	cmd.ID = "stable-2"
	cmd.TaskID = first.TaskID
	cmd.ExpectedRevision = first.Revision
	second := finishHistory(t, s, cmd)
	page, err := s.ListAttempts(background, first.TaskID, 0, 1)
	if err != nil || len(page.Attempts) != 1 || page.Attempts[0].ID != second.ID {
		t.Fatalf("latest: %+v %v", page, err)
	}
	cmd.ID = "stable-3"
	cmd.ExpectedRevision = second.Revision
	third := finishHistory(t, writer, cmd)
	// Clock and same-timestamp artifacts do not change accepted revision order.
	if _, err = writer.db.ExecContext(background, "UPDATE attempts SET started_at=? WHERE task_id=?", time.Unix(0, 0).Format(time.RFC3339Nano), first.TaskID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, home)
	older, err := s.ListAttempts(background, first.TaskID, page.NextBeforeRevision, 1)
	if err != nil || len(older.Attempts) != 1 || older.Attempts[0].ID != first.ID || older.NextBeforeRevision != 0 {
		t.Fatalf("cursor shifted: %+v %v", older, err)
	}
	latest, err := s.ListAttempts(background, first.TaskID, 0, 1)
	if err != nil || len(latest.Attempts) != 1 || latest.Attempts[0].ID != third.ID {
		t.Fatalf("new latest: %+v %v", latest, err)
	}
}

func TestAttemptPagesRemainAccessibleBeyondFullHistoryCaps(t *testing.T) {
	s := openTest(t, t.TempDir())
	cmd := command("large-page", "workspace-a", "read-only")
	var last core.Attempt
	for i := 0; i < 4; i++ {
		cmd.ID = fmt.Sprint("large-page-", i)
		if i > 0 {
			cmd.TaskID = last.TaskID
			cmd.ExpectedRevision = last.Revision
		}
		last = accept(t, s, cmd)
		var err error
		last, err = s.Finish(background, last.ID, last.Owner.ID, "complete", strings.Repeat("x", core.MaxOutputBytes), "")
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Attempts(background, last.TaskID)
	requireCode(t, err, "history_too_large")
	page, err := s.ListAttempts(background, last.TaskID, 0, 1)
	if err != nil || len(page.Attempts) != 1 || page.Attempts[0].ID != last.ID || page.NextBeforeRevision != last.Revision {
		t.Fatalf("recent large body inaccessible: len=%d cursor=%d err=%v", len(page.Attempts), page.NextBeforeRevision, err)
	}
	page, err = s.ListAttempts(background, last.TaskID, 0, 4)
	requireCode(t, err, "history_too_large")
	if len(page.Attempts) != 0 || page.NextBeforeRevision != 0 {
		t.Fatal("failed page leaked partial success")
	}
	// Seed 1001 indexed historical rows in a single transaction, avoiding 1001
	// acceptance/fsync cycles. Reads below still exercise the real Store API.
	task, err := s.SaveDraft(background, core.Task{Workspace: cmd.Workspace, Draft: "row fixture", Selection: cmd.Requested}, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = s.write(background, func(conn executor) error {
		for revision := 1; revision <= 1001; revision++ {
			a := last
			a.ID = fmt.Sprint("indexed-", revision)
			a.TaskID = task.ID
			a.CommandID = a.ID
			a.Revision = revision
			a.Input = "small"
			a.Output = "small"
			raw, err := encode(a)
			if err != nil {
				return err
			}
			if _, err = conn.ExecContext(background, "INSERT INTO attempts(id,task_id,command_id,intent_hash,revision,state,owner_id,started_at,body) VALUES(?,?,?,?,?,?,?,?,?)", a.ID, a.TaskID, a.CommandID, "synthetic", revision, "complete", a.Owner.ID, "same-time", raw); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Attempts(background, task.ID)
	requireCode(t, err, "history_too_large")
	page, err = s.ListAttempts(background, task.ID, 0, 1000)
	if err != nil || len(page.Attempts) != 1000 || page.Attempts[0].Revision != 2 || page.Attempts[999].Revision != 1001 || page.NextBeforeRevision != 2 {
		t.Fatalf("row-cap pagination len=%d cursor=%d err=%v", len(page.Attempts), page.NextBeforeRevision, err)
	}
	page, err = s.ListAttempts(background, task.ID, page.NextBeforeRevision, 1000)
	if err != nil || len(page.Attempts) != 1 || page.Attempts[0].Revision != 1 || page.NextBeforeRevision != 0 {
		t.Fatalf("row-cap remainder %+v %v", page, err)
	}
}

func TestAttemptPageValidationEmptyAndSchemaFence(t *testing.T) {
	s := openTest(t, t.TempDir())
	cmd := command("empty-page", "workspace-a", "read-only")
	task, err := s.SaveDraft(background, core.Task{Workspace: cmd.Workspace, Draft: cmd.Input, Selection: cmd.Requested}, 0)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ListAttempts(background, task.ID, 0, 10)
	if err != nil || page.Attempts == nil || len(page.Attempts) != 0 || page.NextBeforeRevision != 0 {
		t.Fatalf("empty task %+v %v", page, err)
	}
	for _, limit := range []int{-1, 0, 1001} {
		_, err = s.ListAttempts(background, task.ID, 0, limit)
		requireCode(t, err, "invalid_input")
	}
	_, err = s.ListAttempts(background, task.ID, -1, 10)
	requireCode(t, err, "invalid_input")
	_, err = s.ListAttempts(background, "", 0, 10)
	requireCode(t, err, "invalid_input")
	_, err = s.ListAttempts(background, "missing", 0, 10)
	requireCode(t, err, "not_found")
	cmd.TaskID, cmd.ExpectedRevision = task.ID, task.Revision
	a := finishHistory(t, s, cmd)
	if _, err = s.db.ExecContext(background, "UPDATE attempts SET revision=? WHERE id=?", a.Revision+1, a.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.ListAttempts(background, task.ID, 0, 10)
	requireCode(t, err, "state_corrupt")
	if _, err = s.db.ExecContext(background, "PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	_, err = s.ListAttempts(background, task.ID, 0, 10)
	requireCode(t, err, "schema_newer")
}
