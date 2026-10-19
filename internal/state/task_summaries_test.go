package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/core"
)

func summaryFixture(i int) core.Task {
	at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC).Add(time.Duration(i/3) * time.Second)
	return core.Task{ID: fmt.Sprintf("task_%03d", i), Title: fmt.Sprintf("Task %03d", i), Revision: 1,
		Workspace: core.Workspace{ID: "workspace", Root: "workspace", Dir: "workspace/src"},
		Selection: core.Defaults(), Draft: "small draft", Status: "draft", CreatedAt: at, UpdatedAt: at}
}

func seedSummaryTasks(t *testing.T, store *Store, tasks ...core.Task) {
	t.Helper()
	if err := store.write(background, func(conn executor) error {
		for _, task := range tasks {
			if err := saveTask(background, conn, task, true); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskSummariesBeyond100WithoutDraftBodiesAcrossRestart(t *testing.T) {
	home := t.TempDir()
	s := openTest(t, home)
	var tasks []core.Task
	for i := range 130 {
		task := summaryFixture(i)
		if i >= 110 {
			task.Draft = strings.Repeat("d", maxInput)
		}
		tasks = append(tasks, task)
	}
	seedSummaryTasks(t, s, tasks...)
	_, err := s.ListTasks(background, 100, 0)
	requireCode(t, err, "history_too_large") // Prove the former full-draft limit.
	cursor := ""
	seen := map[string]bool{}
	for pageNumber := 0; ; pageNumber++ {
		page, err := s.ListTaskSummaries(background, "tAsK", cursor, 30)
		if err != nil || len(page.Tasks) == 0 || len(page.Tasks) > 30 {
			t.Fatalf("page %d: %+v %v", pageNumber, page, err)
		}
		for _, summary := range page.Tasks {
			want := tasks[129-len(seen)]
			if seen[summary.ID] || summary.ID != want.ID || summary.Title != want.Title || summary.Revision != 1 || summary.Workspace != want.Workspace || !summary.HasDraft || !summary.UpdatedAt.Equal(want.UpdatedAt) {
				t.Fatalf("bad metadata/order: %+v", summary)
			}
			seen[summary.ID] = true
		}
		encoded, err := json.Marshal(page)
		if err != nil || strings.Contains(string(encoded), `"draft":`) || strings.Contains(string(encoded), `"selection":`) || len(encoded) > 30000 {
			t.Fatal("draft body escaped summary projection", len(encoded), err)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pageNumber == 0 {
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTest(t, home)
		}
	}
	if len(seen) != 130 {
		t.Fatal("older tasks inaccessible", len(seen))
	}
}

func TestTaskSummarySearchIsASCIIFoldedLiteralSubstring(t *testing.T) {
	s := openTest(t, t.TempDir())
	titles := []string{"Report 100% done", "Report 100X done", "under_score", "underXscore", `path\leaf`, "path/leaf", "MiXeD title", "\u00c4rger", "\u00e4rger", "quote' ? --"}
	var tasks []core.Task
	for i, title := range titles {
		task := summaryFixture(i)
		task.Title, task.Draft = title, ""
		tasks = append(tasks, task)
	}
	seedSummaryTasks(t, s, tasks...)
	for _, tc := range []struct{ query, want string }{{"%", titles[0]}, {"_score", titles[2]}, {`\`, titles[4]}, {"/", titles[5]}, {"mIxEd", titles[6]}, {"\u00c4", titles[7]}, {"\u00e4", titles[8]}, {"' ? --", titles[9]}, {"TASK_004", titles[4]}} {
		page, err := s.ListTaskSummaries(background, tc.query, "", 100)
		if err != nil || len(page.Tasks) != 1 || page.Tasks[0].Title != tc.want || page.Tasks[0].HasDraft {
			t.Fatalf("query %q: %+v %v", tc.query, page, err)
		}
	}
	page, err := s.ListTaskSummaries(background, "absent", "", 20)
	if err != nil || page.Tasks == nil || len(page.Tasks) != 0 || page.NextCursor != "" {
		t.Fatal("empty search page", page, err)
	}
}

func TestTaskSummaryCursorValidationAndLiveUpdates(t *testing.T) {
	s := openTest(t, t.TempDir())
	seedSummaryTasks(t, s, summaryFixture(0), summaryFixture(1), summaryFixture(2))
	first, err := s.ListTaskSummaries(background, "Task", "", 1)
	if err != nil || first.NextCursor == "" || first.Tasks[0].ID != "task_002" {
		t.Fatal(first, err)
	}
	for _, tc := range []struct {
		query, cursor string
		limit         int
	}{
		{"", "", 0}, {"", "", 1001}, {strings.Repeat("x", 257), "", 1}, {"x\x00", "", 1}, {string([]byte{255}), "", 1},
		{"task", first.NextCursor, 1}, {"Task", "not-a-cursor", 1}, {"Task", first.NextCursor + "\n", 1}, {"Task", strings.Repeat("x", 2049), 1},
	} {
		_, err := s.ListTaskSummaries(background, tc.query, tc.cursor, tc.limit)
		requireCode(t, err, "invalid_input")
	}
	position, err := decodeTaskCursor(first.NextCursor, "Task")
	if err != nil {
		t.Fatal(err)
	}
	position.Version = 2
	_, err = s.ListTaskSummaries(background, "Task", encodeTaskCursor(position), 1)
	requireCode(t, err, "invalid_input")
	raw, _ := base64.RawURLEncoding.DecodeString(first.NextCursor)
	for _, suffix := range []string{`,"v":1}`, `,"unknown":true}`} {
		bad := base64.RawURLEncoding.EncodeToString(append(raw[:len(raw)-1:len(raw)-1], []byte(suffix)...))
		_, err = s.ListTaskSummaries(background, "Task", bad, 1)
		requireCode(t, err, "invalid_input")
	}
	// The old page cursor stays a fixed exclusive position. Editing an unseen
	// task can move it above that position; only restarting the list reveals it.
	edit, err := s.GetTask(background, "task_000")
	if err != nil {
		t.Fatal(err)
	}
	edit.Draft = "new draft"
	if _, err = s.SaveDraft(background, edit, edit.Revision); err != nil {
		t.Fatal(err)
	}
	older, err := s.ListTaskSummaries(background, "Task", first.NextCursor, 20)
	if err != nil || len(older.Tasks) != 1 || older.Tasks[0].ID != "task_001" || older.NextCursor != "" {
		t.Fatal("cursor moved with live edit", older, err)
	}
	latest, err := s.ListTaskSummaries(background, "Task", "", 1)
	if err != nil || latest.Tasks[0].ID != "task_000" {
		t.Fatal("refresh missed edited task", latest, err)
	}
}

func TestTaskSummaryPreservesIndexedTimestampSpelling(t *testing.T) {
	s := openTest(t, t.TempDir())
	seedSummaryTasks(t, s, summaryFixture(0), summaryFixture(1))
	// Same instant, alternate valid RFC3339 spelling. Index/body equality is
	// temporal, while page ordering/cursor preserve the existing stored text.
	if _, err := s.db.Exec("UPDATE tasks SET updated_at=? WHERE id=?", "2026-10-18T01:00:00Z", "task_001"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE tasks SET body=json_set(body,'$.updated_at',?) WHERE id=?", "2026-10-18T02:00:00+01:00", "task_001"); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListTaskSummaries(background, "", "", 1)
	if err != nil || page.Tasks[0].ID != "task_001" {
		t.Fatal(page, err)
	}
	position, err := decodeTaskCursor(page.NextCursor, "")
	if err != nil || position.Updated != "2026-10-18T01:00:00Z" {
		t.Fatal("cursor normalized existing index text", position, err)
	}
	page, err = s.ListTaskSummaries(background, "", page.NextCursor, 1)
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != "task_000" {
		t.Fatal(page, err)
	}
}

func TestTaskSummaryDamageIsNotFilteredOrPartiallyReturned(t *testing.T) {
	for _, change := range []string{
		"body='{bad'", "body=json_set(body,'$.id','other')", "body=json_set(body,'$.revision',2)",
		"body=json_set(body,'$.updated_at','2025-01-01T00:00:00Z')", "body=json_set(body,'$.title',10)",
		"body=json_remove(body,'$.title')", "body=json_set(body,'$.workspace.directory','')",
		"body=json_set(body,'$.draft',null)", "body=json_set(body,'$.status','queued')", "updated_at='invalid'",
		`body=substr(body,1,length(body)-1)||',"id":"other"}'`,
		`body=json_set(body,'$.workspace',json('{"id":"workspace","root":"workspace","directory":"workspace/src","directory":"other"}'))`,
	} {
		t.Run(change, func(t *testing.T) {
			s := openTest(t, t.TempDir())
			bad, good := summaryFixture(0), summaryFixture(3)
			bad.Title, good.Title = "not the search", "needle"
			seedSummaryTasks(t, s, bad, good)
			if _, err := s.db.Exec("UPDATE tasks SET " + change + " WHERE id='task_000'"); err != nil {
				t.Fatal(err)
			}
			page, err := s.ListTaskSummaries(background, "needle", "", 20)
			requireCode(t, err, "state_corrupt")
			if len(page.Tasks) != 0 || page.NextCursor != "" {
				t.Fatal("partial page escaped damage failure", page)
			}
		})
	}
}

func TestTaskSummaryPageCapAndSchemaFence(t *testing.T) {
	s := openTest(t, t.TempDir())
	var tasks []core.Task
	for i := range 260 {
		task := summaryFixture(i)
		task.Workspace.Root = strings.Repeat("r", 32768)
		task.Workspace.Dir = strings.Repeat("d", 32768)
		tasks = append(tasks, task)
	}
	seedSummaryTasks(t, s, tasks...)
	page, err := s.ListTaskSummaries(background, "", "", 1000)
	requireCode(t, err, "history_too_large")
	if len(page.Tasks) != 0 {
		t.Fatal("oversized partial page escaped", len(page.Tasks))
	}
	page, err = s.ListTaskSummaries(background, "", "", 100)
	if err != nil || len(page.Tasks) != 100 || page.NextCursor == "" {
		t.Fatal("bounded page failed", len(page.Tasks), err)
	}
	if _, err = s.db.Exec("PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	_, err = s.ListTaskSummaries(background, "", "", 1)
	requireCode(t, err, "schema_newer")
	_, err = s.RecentDraft(background, "workspace/src")
	requireCode(t, err, "schema_newer")
}

func TestRecentDraftFindsOnlyMatchingWorkspaceBeyond100(t *testing.T) {
	s := openTest(t, t.TempDir())
	var tasks []core.Task
	for i := range 130 {
		task := summaryFixture(i)
		task.Workspace.Dir = "other/workspace"
		if i <= 2 {
			task.Workspace.Dir = "target/Workspace"
		}
		if i == 2 {
			task.Draft = "" // A newer empty task must not hide an older draft.
		}
		if i >= 110 {
			task.Draft = strings.Repeat("x", maxInput)
		}
		tasks = append(tasks, task)
	}
	seedSummaryTasks(t, s, tasks...)
	got, err := s.RecentDraft(background, "target/Workspace")
	if err != nil || got == nil || !reflect.DeepEqual(*got, tasks[1]) {
		t.Fatal("old matching draft not restored", got, err)
	}
	got, err = s.RecentDraft(background, "target/workspace")
	if err != nil || runtime.GOOS == "windows" && (got == nil || got.ID != tasks[1].ID) || runtime.GOOS != "windows" && got != nil {
		t.Fatal("lookup changed platform directory matching", got, err)
	}
	for _, directory := range []string{"", "bad\x00path", strings.Repeat("x", 32769), string([]byte{255})} {
		_, err = s.RecentDraft(background, directory)
		requireCode(t, err, "invalid_input")
	}
	ctx, cancel := context.WithCancel(background)
	cancel()
	_, err = s.RecentDraft(ctx, "target/Workspace")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("draft lookup lost cancellation identity", err)
	}
}

func TestRecentDraftPreservesPlatformPathCaseAndCleaning(t *testing.T) {
	s := openTest(t, t.TempDir())
	task := summaryFixture(0)
	task.Workspace.Dir = "Project/\u00dcnicode/src"
	seedSummaryTasks(t, s, task)
	got, err := s.RecentDraft(background, "project/\u00fcnicode/other/../src")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if got == nil || got.ID != task.ID || got.Workspace.Dir != task.Workspace.Dir {
			t.Fatal("Windows Unicode/canonical comparison changed stored path", got)
		}
	} else if got != nil {
		t.Fatal("case-sensitive platform broadened directory matching", got)
	}
	got, err = s.RecentDraft(background, task.Workspace.Dir)
	if err != nil || got == nil || got.ID != task.ID {
		t.Fatal("exact execution directory failed", got, err)
	}
}

func TestRecentDraftValidatesSelectedFullTask(t *testing.T) {
	s := openTest(t, t.TempDir())
	task := summaryFixture(0)
	task.Selection.Effort = "invalid"
	seedSummaryTasks(t, s, task)
	// Summary browsing deliberately omits settings; restoring the selected draft
	// must validate them before returning an editable full task to the caller.
	page, err := s.ListTaskSummaries(background, "", "", 20)
	if err != nil || len(page.Tasks) != 1 {
		t.Fatal(page, err)
	}
	_, err = s.RecentDraft(background, task.Workspace.Dir)
	requireCode(t, err, "state_corrupt")
}
