package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/state"
)

type taskListPagination struct {
	Limit      int    `json:"limit"`
	Search     string `json:"search"`
	Cursor     string `json:"cursor"`
	NextCursor string `json:"next_cursor"`
}

func showTaskSummaries(tasks []state.TaskSummary, query, cursor, next string, limit int, o *output) {
	_ = o.text("Tasks (live list; newest first)\n")
	if query != "" {
		_ = o.text(fmt.Sprintf("Search: %q\n", query))
	}
	if len(tasks) == 0 {
		switch {
		case cursor != "":
			_ = o.text("No tasks on this page. Omit --cursor to refresh the recent list.\n")
		case query != "":
			_ = o.text("No matching tasks. Change --search or use an empty search for all tasks.\n")
		default:
			_ = o.text("No saved tasks. Try ucli run --demo \"Try a task\"\n")
		}
	}
	for _, task := range tasks {
		draft := ""
		if task.HasDraft {
			draft = " | draft saved"
		}
		_ = o.text(fmt.Sprintf("%s | %s | revision %d%s\n  %s\n  %s\n", task.ID, task.Status, task.Revision, draft, task.Title, task.Workspace.Dir))
	}
	if next != "" {
		if query == "" {
			_ = o.text(fmt.Sprintf("Older: ucli tasks list --limit %d --cursor %s\n", limit, next))
		} else {
			_ = o.text("Older: repeat with the same --search text and --cursor " + next + "\n")
		}
	}
	_ = o.text("Search: ucli tasks list --search TEXT\nInspect: ucli tasks show ID\nContinue: ucli tasks followup ID PROMPT\nReconcile stopped demo: ucli tasks recover ID\nUpdated tasks can move between pages; omit --cursor to refresh.\n")
}

// The finder retains only its query/cursor until an explicit numbered selection.
// Reading another page never refreshes the active draft or its displayed revision.
func (s *session) selectTask(ctx context.Context) error {
	if s.terminal != nil {
		return s.selectTaskTerminal(ctx)
	}
	query, cursor := s.taskSearch, s.taskCursor
	for {
		page, err := s.app.TaskSummaries(ctx, query, cursor, 20)
		if err != nil {
			var problem *core.Error
			if errors.As(err, &problem) && problem.Code == "invalid_input" && (query != s.taskSearch || cursor != s.taskCursor) {
				_ = s.out.problem(err)
				query, cursor = s.taskSearch, s.taskCursor
				continue
			}
			return err
		}
		s.taskSearch, s.taskCursor = query, cursor
		if len(page.Tasks) == 0 && s.taskSearch == "" && s.taskCursor == "" {
			_ = s.out.text("No saved tasks. Enter your work, then /send.\n")
			return nil
		}
		_ = s.out.text("Open a task (opening does not run it)\nLive list; newest first. /recent refreshes tasks that changed.\n")
		if s.taskSearch != "" {
			_ = s.out.text(fmt.Sprintf("Search: %q\n", s.taskSearch))
		}
		for i, task := range page.Tasks {
			draft := ""
			if task.HasDraft {
				draft = " | draft saved"
			}
			_ = s.out.text(fmt.Sprintf("  %d. %s | %s%s | %s\n     %s\n", i+1, task.Title, task.Status, draft, task.ID, task.Workspace.Dir))
		}
		if len(page.Tasks) == 0 {
			_ = s.out.text("No tasks on this page. Change the search or choose /recent.\n")
		}
		_ = s.out.text("  0. Back | /recent Recent tasks")
		if page.NextCursor != "" {
			_ = s.out.text(" | /older Older tasks")
		}
		_ = s.out.text("\nType a number or search titles/IDs; /search TEXT searches literal text; Enter clears search\nChoose > ")
		answer := s.input.next(ctx)
		if answer.err != nil {
			return answer.err
		}
		text := strings.TrimSpace(answer.text)
		if answer.literal {
			query, cursor = answer.text, ""
			continue
		}
		if strings.HasPrefix(answer.text, "/search ") {
			query, cursor = strings.TrimPrefix(answer.text, "/search "), ""
			continue
		}
		switch text {
		case "0", "/back":
			return nil
		case "/recent":
			cursor = ""
			continue
		case "/older":
			if page.NextCursor == "" {
				_ = s.out.text("No older tasks on this search. Choose /recent to refresh.\n")
			} else {
				cursor = page.NextCursor
			}
			continue
		}
		if number, err := strconv.Atoi(text); err == nil {
			if number < 1 || number > len(page.Tasks) {
				_ = s.out.text("Choose a listed number, search text, or 0 to go back.\n")
				continue
			}
			task, _, err := s.app.TaskPage(ctx, page.Tasks[number-1].ID, 0, 1)
			if err != nil {
				return err
			}
			s.task, s.cwd = &task, task.Workspace.Dir
			s.recoveryID = ""
			s.historyOpen, s.historyNext = false, 0
			s.selection = task.Selection.WithDefaults()
			_ = s.out.text("Opened task in " + s.cwd + ". No work started.\n")
			return nil
		}
		query, cursor = answer.text, ""
	}
}
