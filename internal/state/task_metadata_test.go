package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"ucli.local/ucli/internal/core"
)

// Retain the previous projection as a comparison oracle for validation and
// opt-in component timings. Production does not use this duplicate guard set.
func legacyMetadataProjection() string {
	limits := map[string]int{"id": 256, "updated_at": 64, "body_id": 256, "body_updated_at": 64, "title": 1024, "workspace_id": 256, "workspace_root": 32768, "workspace_dir": 32768, "status": 32, "draft_type": 16}
	parts, guards := []string{}, []string{}
	for _, column := range strings.Split(metadataColumns, ",") {
		if column == "valid_types" {
			continue
		}
		var valid, expression string
		if limit, ok := limits[column]; ok {
			valid = "typeof(" + column + ")='text' AND length(CAST(" + column + " AS BLOB))<=" + strconv.Itoa(limit)
			expression = "CASE WHEN " + valid + " THEN " + column + " END"
		} else {
			valid = "typeof(" + column + ")='integer'"
			if column == "oversized" || column == "unique_metadata" {
				valid += " AND " + column + " IN (0,1)"
			}
			expression = "CASE WHEN " + valid + " THEN " + column + " END"
		}
		parts = append(parts, expression)
		guards = append(guards, "("+column+" IS NULL OR ("+valid+"))")
	}
	parts = append(parts, "CASE WHEN typeof(valid_types)='integer' AND valid_types=1 AND "+strings.Join(guards, " AND ")+" THEN 1 ELSE 0 END")
	return strings.Join(parts, ",")
}

// Only SELECT projection text is replaced; key checking, row validation and
// selected-original auditing execute through the same production functions.
type metadataProjectionExecutor struct {
	executor
	projection string
}

func (e metadataProjectionExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return e.executor.QueryContext(ctx, strings.Replace(query, metadataReadColumns, e.projection, 1), args...)
}

func TestMaterializedSummaryParityPagingSearchDraftsAndRestart(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	tasks := []core.Task{}
	for i := range 130 {
		task := summaryFixture(i)
		if i >= 110 {
			task.Draft = strings.Repeat("x", maxInput)
		}
		if i == 0 {
			task.Title = `Needle 100% under_score path\leaf Ä`
		}
		if i > 1 {
			task.Workspace.Dir = "another/directory"
		}
		tasks = append(tasks, task)
	}
	seedSummaryTasks(t, s, tasks...)
	queries := []string{"", "TASK", "%", "_score", `\`, "Ä", "ä", "task_000", "missing"}
	before := map[string]TaskSummaryPage{}
	for _, query := range queries {
		page, err := s.ListTaskSummaries(background, query, "", 20)
		if err != nil {
			t.Fatal(err)
		}
		before[query] = page
	}
	oldDraft, err := s.RecentDraft(background, "workspace/src")
	if err != nil {
		t.Fatal(err)
	}
	migrateTest(t, s)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, home)
	for _, query := range queries {
		page, err := s.ListTaskSummaries(background, query, "", 20)
		if err != nil || !reflect.DeepEqual(page, before[query]) {
			t.Fatalf("query %q: %+v != %+v: %v", query, page, before[query], err)
		}
	}
	draft, err := s.RecentDraft(background, "workspace/src")
	if err != nil || !reflect.DeepEqual(draft, oldDraft) {
		t.Fatal("recent draft changed", draft, oldDraft, err)
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := s.ListTaskSummaries(background, "", cursor, 17)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range page.Tasks {
			if seen[task.ID] {
				t.Fatal("duplicate page", task.ID)
			}
			seen[task.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 130 {
		t.Fatal("pagination lost tasks", len(seen))
	}
	_, err = s.ListTaskSummaries(background, "changed", before[""].NextCursor, 20)
	requireCode(t, err, "invalid_input")
	_, err = s.ListTasks(background, 100, 0)
	requireCode(t, err, "history_too_large")
}

func TestMaterializedMetadataTracksEveryStoreMutationAndRawTaskUpdate(t *testing.T) {
	s := openTest(t, t.TempDir())
	migrateTest(t, s)
	cmd := command("epoch2-command", "epoch2-workspace", "read-only")
	a := accept(t, s, cmd)
	page, err := s.ListTaskSummaries(background, a.TaskID, "", 20)
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].Status != "running" {
		t.Fatal(page, err)
	}
	task, err := s.GetTask(background, a.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Draft = "next revision"
	task, err = s.SaveDraft(background, task, task.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Finish(background, a.ID, a.Owner.ID, "complete", "result", "done"); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListTaskSummaries(background, a.TaskID, "", 20)
	if err != nil || len(page.Tasks) != 1 || !page.Tasks[0].HasDraft || page.Tasks[0].Revision != task.Revision || page.Tasks[0].Status != "draft" {
		t.Fatal(page, err)
	}
	if _, err = s.db.Exec(`UPDATE tasks SET body=json_set(body,'$.title','Raw update','$.future','preserved') WHERE id=?`, a.TaskID); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListTaskSummaries(background, "Raw update", "", 20)
	if err != nil || len(page.Tasks) != 1 {
		t.Fatal(page, err)
	}
	if err = s.CheckIntegrity(background); err != nil {
		t.Fatal(err)
	}
	seedSummaryTasks(t, s, summaryFixture(0))
	if _, err = s.db.Exec("DELETE FROM tasks WHERE id='task_000'"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM task_metadata WHERE id='task_000'").Scan(&count); err != nil || count != 0 {
		t.Fatal("delete trigger", count, err)
	}
}

func TestMaterializedMetadataRejectsDamagedNonmatchesAndSelectedCache(t *testing.T) {
	changes := []string{
		"UPDATE tasks SET body='{bad' WHERE id='task_000'",
		"UPDATE tasks SET body=json_set(body,'$.id','other') WHERE id='task_000'",
		"UPDATE tasks SET body=json_set(body,'$.revision',2) WHERE id='task_000'",
		"UPDATE tasks SET body=json_set(body,'$.title',10) WHERE id='task_000'",
		"UPDATE tasks SET body=json_set(body,'$.workspace.directory','') WHERE id='task_000'",
		"UPDATE tasks SET body=json_set(body,'$.draft',null) WHERE id='task_000'",
		"UPDATE tasks SET updated_at='invalid' WHERE id='task_000'",
		`UPDATE tasks SET body=substr(body,1,length(body)-1)||',"id":"other"}' WHERE id='task_000'`,
		`UPDATE tasks SET body=json_set(body,'$.workspace',json('{"id":"workspace","root":"workspace","directory":"workspace/src","directory":"other"}')) WHERE id='task_000'`,
		"UPDATE task_metadata SET title=NULL WHERE id='task_000'",
		"DELETE FROM task_metadata WHERE id='task_000'",
		"UPDATE task_metadata SET title='tampered needle' WHERE id='task_003'",
		"UPDATE task_metadata SET body_revision=2,revision=2 WHERE id='task_003'",
		"DROP TRIGGER task_metadata_update",
	}
	for i, change := range changes {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s := openTest(t, t.TempDir())
			bad, good := summaryFixture(0), summaryFixture(3)
			bad.Title, good.Title = "not a match", "needle"
			seedSummaryTasks(t, s, bad, good)
			migrateTest(t, s)
			if _, err := s.db.Exec(change); err != nil {
				t.Fatal(err)
			}
			page, err := s.ListTaskSummaries(background, "needle", "", 20)
			requireCode(t, err, "state_corrupt")
			if len(page.Tasks) != 0 {
				t.Fatal("returned partial damaged page")
			}
			requireCode(t, s.CheckIntegrity(background), "state_corrupt")
		})
	}
}

func TestExplicitAuditDetectsUnselectedCacheDivergence(t *testing.T) {
	s := openTest(t, t.TempDir())
	seedSummaryTasks(t, s, summaryFixture(0))
	migrateTest(t, s)
	// Both fields remain well-typed, so a miss need not fetch the source body.
	// The explicit audit must still reject a stale/externally damaged cache.
	if _, err := s.db.Exec("UPDATE task_metadata SET title='changed cache'"); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListTaskSummaries(background, "missing", "", 20)
	if err != nil || len(page.Tasks) != 0 {
		t.Fatal(page, err)
	}
	requireCode(t, s.CheckIntegrity(background), "state_corrupt")
	_, err = s.Backup(background, filepath.Join(t.TempDir(), "must-not-certify.sqlite"))
	requireCode(t, err, "state_corrupt")
}

func TestMigrationPreservesSourceWhenBackfillValidationFails(t *testing.T) {
	s := openTest(t, t.TempDir())
	seedSummaryTasks(t, s, summaryFixture(0))
	if _, err := s.db.Exec("UPDATE tasks SET body=json_set(body,'$.title',10)"); err != nil {
		t.Fatal(err)
	}
	before := taskBytes(t, s)
	receipt, err := s.Migrate(background, filepath.Join(t.TempDir(), "before.sqlite"))
	requireCode(t, err, "state_corrupt")
	if receipt.Status != "not_applied" || receipt.Backup.Path == "" || !reflect.DeepEqual(before, taskBytes(t, s)) {
		t.Fatal("invalid backfill lost source or backup", receipt)
	}
	version, err := s.SchemaVersion(background)
	if err != nil || version != 1 {
		t.Fatal(version, err)
	}
}

func TestMetadataKeyChecksPreserveMissingOrphanAndNullDetection(t *testing.T) {
	cases := []struct {
		name    string
		changes []string
	}{
		{"missing", []string{"DELETE FROM task_metadata WHERE id='task_000'"}},
		{"orphan", []string{"INSERT INTO task_metadata(id) VALUES('orphan')"}},
		{"equal counts different keys", []string{"UPDATE task_metadata SET id='orphan' WHERE id='task_000'"}},
		{"null metadata", []string{"INSERT INTO task_metadata(id) VALUES(NULL)"}},
		{"null source", []string{"INSERT INTO tasks SELECT NULL,revision,updated_at,body FROM tasks WHERE id='task_000'"}},
		{"null in both", []string{
			"INSERT INTO tasks SELECT NULL,revision,updated_at,body FROM tasks WHERE id='task_000'",
			"INSERT INTO task_metadata(id) VALUES(NULL)",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t, t.TempDir())
			seedSummaryTasks(t, s, summaryFixture(0), summaryFixture(3))
			migrateTest(t, s)
			if err := checkMetadataKeys(background, s.db); err != nil {
				t.Fatal("equal keys rejected", err)
			}
			// Simulate external damage; ordinary store mutations keep the helper in
			// sync. NULL remains legal in SQLite's non-integer primary-key column.
			if _, err := s.db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
				t.Fatal(err)
			}
			for _, change := range tc.changes {
				if _, err := s.db.Exec(change); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "null in both" {
				var naiveMismatch bool
				if err := s.db.QueryRow(`SELECT EXISTS(SELECT id FROM tasks EXCEPT SELECT id FROM task_metadata)
OR EXISTS(SELECT id FROM task_metadata EXCEPT SELECT id FROM tasks)`).Scan(&naiveMismatch); err != nil || naiveMismatch {
					t.Fatal("negative control must show unguarded EXCEPT misses paired NULLs", naiveMismatch, err)
				}
			}
			requireCode(t, checkMetadataKeys(background, s.db), "state_corrupt")
			page, err := s.ListTaskSummaries(background, "missing", "", 1)
			requireCode(t, err, "state_corrupt")
			if len(page.Tasks) != 0 {
				t.Fatal("damaged keys returned partial summaries")
			}
			_, err = s.RecentDraft(background, "workspace/src")
			requireCode(t, err, "state_corrupt")
		})
	}
}

func TestMetadataProjectionValidationEquivalence(t *testing.T) {
	s := openTest(t, t.TempDir())
	seedSummaryTasks(t, s, summaryFixture(0))
	migrateTest(t, s)
	if _, err := s.db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	legacy := legacyMetadataProjection()
	// Every per-column CASE must remain byte-identical. Only the final combined
	// valid_types expression may change in this optimization.
	separator := ",CASE WHEN typeof(valid_types)"
	if strings.Split(legacy, separator)[0] != strings.Split(metadataReadColumns, separator)[0] {
		t.Fatal("a bounded per-column CASE changed")
	}
	read := func(projection string) (metadataRecord, error) {
		t.Helper()
		rows, err := s.db.Query("SELECT " + projection + ",0 FROM task_metadata")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("missing comparison row", rows.Err())
		}
		return readMetadata(rows)
	}
	textLimits := map[string]int{"id": 256, "updated_at": 64, "body_id": 256, "body_updated_at": 64, "title": 1024, "workspace_id": 256, "workspace_root": 32768, "workspace_dir": 32768, "status": 32, "draft_type": 16}
	type change struct {
		name, assignment, want string
		args                   []any
	}
	changes := []change{{"valid row", "valid_types=1", "", nil}}
	for _, column := range strings.Split(metadataColumns, ",") {
		changes = append(changes, change{column + "/null", column + "=NULL", "state_corrupt", nil})
		if limit, ok := textLimits[column]; ok {
			changes = append(changes,
				change{column + "/blob type", column + "=CAST('bad' AS BLOB)", "state_corrupt", nil},
				change{column + "/oversized text", column + "=?", "state_corrupt", []any{strings.Repeat("x", limit+1)}})
		} else {
			changes = append(changes,
				change{column + "/text type", column + "='bad'", "state_corrupt", nil},
				change{column + "/outside int64", column + "=1e100", "state_corrupt", nil})
		}
	}
	changes = append(changes,
		change{"multibyte title exceeds byte limit", "title=?", "state_corrupt", []any{strings.Repeat("é", 513)}},
		change{"absent optional pair", "draft_type=NULL,draft_bytes=NULL", "", nil},
		change{"both optional fields invalid", "draft_type=CAST('bad' AS BLOB),draft_bytes='bad'", "state_corrupt", nil},
		change{"optional type invalid with absent size", "draft_type=CAST('bad' AS BLOB),draft_bytes=NULL", "state_corrupt", nil},
		change{"optional size invalid with absent type", "draft_type=NULL,draft_bytes='bad'", "state_corrupt", nil},
		change{"draft byte ceiling", "draft_bytes=?", "state_corrupt", []any{maxInput + 1}},
		change{"negative draft bytes", "draft_bytes=-1", "state_corrupt", nil},
		change{"revision zero", "revision=0,body_revision=0", "state_corrupt", nil},
		change{"oversized flag out of range", "oversized=2", "state_corrupt", nil},
		change{"unique flag out of range", "unique_metadata=2", "state_corrupt", nil},
		change{"invalid strict valid flag", "valid_types=2", "state_corrupt", nil},
		change{"nonunique flag", "unique_metadata=0", "state_corrupt", nil},
		change{"oversized source", "oversized=1", "history_too_large", nil},
	)
	for _, c := range changes {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.db.Exec("DELETE FROM task_metadata; INSERT INTO task_metadata SELECT * FROM task_metadata_source"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("UPDATE task_metadata SET "+c.assignment, c.args...); err != nil {
				t.Fatal(err)
			}
			old, oldErr := read(legacy)
			current, currentErr := read(metadataReadColumns)
			if code(oldErr) != c.want || code(currentErr) != c.want {
				t.Fatalf("legacy=%v current=%v want=%q", oldErr, currentErr, c.want)
			}
			if c.want == "" && !reflect.DeepEqual(old.summary, current.summary) {
				t.Fatal("accepted summary changed", old.summary, current.summary)
			}
			if c.name == "both optional fields invalid" {
				unsafe := strings.Split(metadataReadColumns, separator)[0] + ",CASE WHEN typeof(valid_types)='integer' AND valid_types=1 THEN 1 ELSE 0 END"
				if _, err := read(unsafe); err != nil {
					t.Fatal("negative control must show absent-looking invalid optional pair is accepted without raw guards", err)
				}
			}
			// A nonmatching row must still be validated rather than filtered out.
			oldPage, oldErr := materializedSummaries(background, metadataProjectionExecutor{s.db, legacy}, "no-such-match", taskCursor{}, 20)
			page, err := s.ListTaskSummaries(background, "no-such-match", "", 20)
			if code(oldErr) != c.want || code(err) != c.want || len(oldPage.Tasks) != 0 || len(page.Tasks) != 0 {
				t.Fatalf("nonmatch legacy=%v current=%v want=%q; pages=%+v/%+v", oldErr, err, c.want, oldPage, page)
			}
		})
	}
}
