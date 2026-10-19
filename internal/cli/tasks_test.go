package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/state"
)

// The oldest draft is in the active directory; 105 newer tasks are elsewhere.
// The newest 17 have maximum-size drafts, exceeding the legacy full-list bound.
func taskArchiveFixture(t *testing.T, home, cwd string) (core.Task, core.Task) {
	t.Helper()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	pinned := core.Defaults()
	pinned.Model, pinned.Effort = "demo/deep", "high"
	current, err := a.Draft(ctx, "", "Ancient comfort task", cwd, pinned)
	if err != nil {
		t.Fatal(err)
	}
	pinned.Model, pinned.Effort = "demo/fast", "low"
	alternate, err := a.Draft(ctx, "", "Alternate work", t.TempDir(), pinned)
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("private-draft-body-", core.MaxInputBytes/19+1)[:core.MaxInputBytes]
	for i := range 104 {
		draft := "Unsent filler"
		if i >= 87 {
			draft = large
		}
		_, err = a.Store.SaveDraft(ctx, core.Task{Title: fmt.Sprintf("Archive filler %03d", i), Workspace: alternate.Workspace, Selection: core.Defaults(), Draft: draft}, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = a.Tasks(ctx)
	var problem *core.Error
	if !errors.As(err, &problem) || problem.Code != "history_too_large" {
		t.Fatalf("fixture did not exceed the old full-list boundary: %v", err)
	}
	return current, alternate
}

func taskListReceipt(t *testing.T, home string, args ...string) ([]state.TaskSummary, taskListPagination) {
	t.Helper()
	code, out, diagnostic := call(t, home, "", append(args, "--json")...)
	if code != 0 || diagnostic != "" {
		t.Fatalf("task list: %d %s %s", code, out, diagnostic)
	}
	items := jsonLines(t, out)
	var reply struct {
		Data       []state.TaskSummary `json:"data"`
		Pagination taskListPagination  `json:"pagination"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Type != "tasks" || reply.Data == nil || len(out) > 100000 || strings.Contains(out, "private-draft-body-") || strings.Contains(out, `"draft":`) || strings.Contains(out, `"selection":`) {
		t.Fatalf("summary leaked bodies or changed the array envelope: %.1000s", out)
	}
	return reply.Data, reply.Pagination
}

func TestTaskSummarySearchCursorAndBounds(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	oldest, _ := taskArchiveFixture(t, home, cwd)
	rows, paging := taskListReceipt(t, home, "tasks")
	if len(rows) != 20 || paging.Limit != 20 || paging.Cursor != "" || paging.Search != "" || paging.NextCursor == "" || !rows[0].HasDraft {
		t.Fatalf("default list metadata: %d %+v", len(rows), paging)
	}
	firstCursor := paging.NextCursor
	seen := map[string]bool{}
	cursor := ""
	for _, want := range []int{100, 6} {
		rows, paging = taskListReceipt(t, home, "tasks", "list", "--limit", "100", "--search", "", "--cursor", cursor)
		if len(rows) != want || paging.Limit != 100 || paging.Cursor != cursor {
			t.Fatalf("page count/cursor: %d %+v", len(rows), paging)
		}
		for _, row := range rows {
			if seen[row.ID] {
				t.Fatalf("duplicate task %s", row.ID)
			}
			seen[row.ID] = true
		}
		cursor = paging.NextCursor
	}
	if cursor != "" || len(seen) != 106 || !seen[oldest.ID] {
		t.Fatalf("archive not reachable: %d ids, cursor %q", len(seen), cursor)
	}
	rows, paging = taskListReceipt(t, home, "--search", "Ancient comfort", "tasks", "list")
	if len(rows) != 1 || rows[0].ID != oldest.ID || paging.Search != "Ancient comfort" || paging.NextCursor != "" {
		t.Fatalf("search missed oldest task: %+v %+v", rows, paging)
	}
	rows, _ = taskListReceipt(t, home, "tasks", "--search", oldest.ID)
	if len(rows) != 1 || rows[0].ID != oldest.ID {
		t.Fatal("ID search missed task")
	}
	for _, args := range [][]string{
		{"tasks", "--cursor", "malformed"},
		{"tasks", "--cursor", firstCursor, "--search", "different"},
		{"tasks", "--search", strings.Repeat("q", 257)},
	} {
		code, out, diagnostic := call(t, home, "", append(args, "--json")...)
		if code != 2 || diagnostic != "" || !strings.Contains(out, `"code":"invalid_input"`) {
			t.Fatalf("invalid query/cursor: %d %s %s", code, out, diagnostic)
		}
	}
	code, human, diagnostic := call(t, home, "", "tasks", "list", "--limit", "2")
	if code != 0 || diagnostic != "" || !strings.Contains(human, "Older: ucli tasks list --limit 2 --cursor ") || !strings.Contains(human, "Updated tasks can move") || strings.Contains(human, "private-draft-body-") {
		t.Fatalf("human discovery: %d %.1000s %s", code, human, diagnostic)
	}
	missing := filepath.Join(t.TempDir(), "not-created")
	for _, args := range [][]string{
		{"tasks", "--limit", "0"}, {"tasks", "--limit", "101"},
		{"tasks", "--before-revision", "0"},
		{"tasks", "show", oldest.ID, "--search", "x"},
		{"tasks", "followup", oldest.ID, "text", "--cursor", "x"},
		{"models", "--search", "x"}, {"--cursor", "x"},
	} {
		if code, _, _ := call(t, missing, "", args...); code != 2 {
			t.Fatalf("invalid list options accepted: %v exit%d", args, code)
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid scoped flag opened state: %v", err)
	}
}

func TestInteractiveTaskFinderRestoresOldDraftAndRetainsNavigation(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	original, alternate := taskArchiveFixture(t, home, cwd)
	input := "/tasks\n/older\n0\n/tasks\n/search Alternate work\n0\n/tasks\n" + strings.Repeat("q", 257) + "\n1\nFollowed from finder\n/exit\n"
	code, text := runInteractive(t, home, cwd, input, true)
	if code != 0 {
		t.Fatalf("finder: %d %.2000s", code, text)
	}
	for _, want := range []string{"Restored a saved draft", "Saved draft:\nAncient comfort task", "model demo/deep | effort high", "/older Older tasks", "/search TEXT", "Opened task in " + alternate.Workspace.Dir, "model demo/fast | effort low"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in finder output", want)
		}
	}
	if strings.Count(text, "  1. Archive filler 083") != 2 || strings.Count(text, "Search: \"Alternate work\"") != 3 || strings.Contains(text, "private-draft-body-") {
		t.Fatalf("page/search was lost on Back, reopen or invalid query: %.3000s", text)
	}
	unchanged, attempts := taskReceipt(t, home, original.ID)
	if unchanged.Draft != original.Draft || unchanged.Revision != original.Revision || unchanged.Selection != original.Selection || len(attempts) != 0 {
		t.Fatalf("browsing changed active task: %+v", unchanged)
	}
	opened, attempts := taskReceipt(t, home, alternate.ID)
	if opened.Draft != alternate.Draft+"\nFollowed from finder" || opened.Revision != alternate.Revision+1 || opened.Selection != alternate.Selection || len(attempts) != 0 {
		t.Fatalf("explicit choice lost identity/pins or ran work: %+v", opened)
	}
	code, text = runInteractive(t, home, cwd, "/tasks\nNo matching saved task\n", true)
	if code != 0 || !strings.Contains(text, "Input closed. Saved drafts remain unchanged") || !strings.Contains(text, "No tasks on this page") {
		t.Fatalf("finder EOF recovery: %d %s", code, text)
	}
	unchanged, attempts = taskReceipt(t, home, original.ID)
	if unchanged.Revision != original.Revision || unchanged.Draft != original.Draft || len(attempts) != 0 {
		t.Fatal("finder EOF modified or submitted the active draft")
	}
}
