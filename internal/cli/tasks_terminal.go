package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/state"
)

const (
	taskPickerOlder  = "\x00older"
	taskPickerRecent = "\x00recent"
	taskPickerEmpty  = "\x00empty"
	taskSearchDelay  = 60 * time.Millisecond
)

type taskPickerTerminal interface {
	pickerTerminal
	nextOr(context.Context, <-chan struct{}) (keyEvent, error)
}

type taskSummarySource interface {
	TaskSummaries(context.Context, string, string, int) (state.TaskSummaryPage, error)
}

type taskFinderPosition struct{ query, cursor, focus string }
type taskSearchRequest struct {
	ctx           context.Context
	generation    uint64
	query, cursor string
	debounce      bool
}
type taskSearchResult struct {
	request taskSearchRequest
	page    state.TaskSummaryPage
	err     error
}

// One context-aware worker owns at most one backend read. Pending requests and
// results each have one slot, so typeahead cannot build a stale-query backlog.
// Store's SQL calls honor cancellation; no goroutine is created per key/query.
type taskSearchWorker struct {
	requests chan taskSearchRequest
	results  chan taskSearchResult
	wake     chan struct{}
	done     chan struct{}
}

func newTaskSearchWorker(ctx context.Context, source taskSummarySource) *taskSearchWorker {
	w := &taskSearchWorker{requests: make(chan taskSearchRequest, 1), results: make(chan taskSearchResult, 1), wake: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for {
			var request taskSearchRequest
			select {
			case <-ctx.Done():
				return
			case request = <-w.requests:
			}
			if request.debounce {
				timer := time.NewTimer(taskSearchDelay)
			waiting:
				for {
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case next := <-w.requests:
						request = next
						if !timer.Stop() {
							select {
							case <-timer.C:
							default:
							}
						}
						if !request.debounce {
							break waiting
						}
						timer.Reset(taskSearchDelay)
					case <-timer.C:
						break waiting
					}
				}
			}
			if request.ctx.Err() != nil {
				continue
			}
			page, err := source.TaskSummaries(request.ctx, request.query, request.cursor, 20)
			if ctx.Err() != nil {
				return
			}
			// A backend may finish just as cancellation arrives. Keep generation
			// checking at the consumer as well; cancellation alone is not freshness.
			select {
			case <-w.results:
			default:
			}
			select {
			case w.results <- taskSearchResult{request: request, page: page, err: err}:
			case <-ctx.Done():
				return
			}
			select {
			case w.wake <- struct{}{}:
			default:
			}
		}
	}()
	return w
}

func (w *taskSearchWorker) submit(request taskSearchRequest) {
	select {
	case <-w.requests:
	default:
	}
	w.requests <- request
}

func (s *session) selectTaskTerminal(ctx context.Context) error {
	position := taskFinderPosition{s.taskSearch, s.taskCursor, s.taskFocus}
	defer func() { s.taskSearch, s.taskCursor, s.taskFocus = position.query, position.cursor, position.focus }()
	current := ""
	if s.task != nil {
		current = s.task.ID
	}
	id, err := pickTaskTerminal(ctx, s.terminal, s.app, current, &position)
	if err != nil || id == "" {
		return err
	}
	task, _, err := s.app.TaskPage(ctx, id, 0, 1)
	if err != nil {
		return err
	}
	s.task, s.cwd = &task, task.Workspace.Dir
	s.recoveryID = ""
	s.historyOpen, s.historyNext = false, 0
	s.selection = task.Selection.WithDefaults()
	_ = s.out.text("Opened task in " + s.cwd + ". No work started.\n")
	return s.openedTaskNotice()
}

func (s *session) openedTaskNotice() error {
	if s.terminal == nil {
		return nil
	}
	text := "Task opened | enter a follow-up"
	if s.task.Draft != "" {
		if err := s.out.text("Opened draft (latest text):\n" + streamFragment(s.task.Draft) + "\n"); err != nil {
			return err
		}
		text = "Unsent message opened | review it, then Enter to send"
	}
	return s.out.notice(text, toneInfo)
}

func taskPagePicker(page state.TaskSummaryPage, current, focus string) pickerState {
	options := make([]pickerOption, 0, len(page.Tasks)+2)
	for _, task := range page.Tasks {
		detail := fmt.Sprintf("%s | %s | revision %d", task.ID, task.Status, task.Revision)
		if task.HasDraft {
			detail += " | draft saved"
		}
		detail += " | " + task.Workspace.Dir
		options = append(options, pickerOption{Value: task.ID, Label: task.Title, Detail: detail})
	}
	if len(options) == 0 {
		options = append(options, pickerOption{Value: taskPickerEmpty, Label: "No matching tasks", Detail: "Edit the search or refresh recent tasks."})
	}
	if page.NextCursor != "" {
		options = append(options, pickerOption{Value: taskPickerOlder, Label: "Older tasks", Detail: "Open the next page of this search without starting work."})
	}
	options = append(options, pickerOption{Value: taskPickerRecent, Label: "Recent tasks", Detail: "Refresh the newest matches; edited tasks may move between pages."})
	picker := newPicker("Open a task (opening does not run it)", options, current)
	if focus != "" {
		picker.focused = focus
		picker.reconcileFocus()
	}
	return picker
}

func taskPickerFrame(picker pickerState, editor textEditor, width, height int) terminalFrame {
	if height < 6 || !pickerViewportUsable(width, height) {
		return pickerTerminalFrame(picker, editor, width, height)
	}
	// Page navigation stays visible beside the task list. It remains in the same
	// focus order, so Up from the first task reaches Recent, then Older.
	base := picker
	var actions []pickerOption
	for index, option := range picker.options {
		if option.Value == taskPickerOlder || option.Value == taskPickerRecent {
			if len(actions) == 0 {
				base.options = picker.options[:index]
			}
			actions = append(actions, option)
		}
	}
	view := pickerTerminalFrame(base, editor, width, height-len(actions))
	footer := view.Lines[len(view.Lines)-1]
	view.Lines = view.Lines[:len(view.Lines)-1]
	for _, option := range actions {
		focused, prefix := option.Value == picker.focused, "  "
		if focused {
			prefix = "> "
		}
		view.Lines = append(view.Lines, terminalLine{Text: prefix + option.Label, Focused: focused, Action: "choice:" + option.Value})
	}
	view.Lines = append(view.Lines, footer)
	return view
}

func pickTaskTerminal(ctx context.Context, terminal taskPickerTerminal, source taskSummarySource, current string, position *taskFinderPosition) (value string, err error) {
	workerCtx, stop := context.WithCancel(ctx)
	worker := newTaskSearchWorker(workerCtx, source)
	var cancelSearch context.CancelFunc
	defer func() {
		if cancelSearch != nil {
			cancelSearch()
		}
		stop()
		// Join the owned read before returning to the composer. A broken source
		// gets one bounded teardown window, never a silently selected task.
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-worker.done:
		case <-timer.C:
			value = ""
			err = core.Problem("task_search_timeout", "Task search did not stop within its deadline. Leave this session before opening the finder again.", 9)
		}
	}()
	var generation uint64
	pending, blocked := true, true
	feedback := ""
	request := func(query, cursor string, debounce bool) {
		if cancelSearch != nil {
			cancelSearch()
		}
		generation++
		pending, blocked, feedback = true, true, ""
		searchCtx, cancel := context.WithCancel(workerCtx)
		cancelSearch = cancel
		worker.submit(taskSearchRequest{searchCtx, generation, query, cursor, debounce})
	}
	editor := textEditor{}
	editor.set(position.query)
	picker := newPicker("Open a task (opening does not run it)", nil, current)
	var page state.TaskSummaryPage
	request(position.query, position.cursor, false)
	for {
		width, height := 80, 24
		if sized, ok := terminal.(interface{ size() (int, int) }); ok {
			width, height = sized.size()
		}
		view := taskPickerFrame(picker, editor, width, height)
		if (pending || blocked) && pickerViewportUsable(width, height) {
			status := "Searching | Esc cancel | Up/Down preview"
			if !pending {
				status = "Search failed | Esc cancel | Edit search"
			}
			if width < 40 {
				status = "Searching | Esc"
				if !pending {
					status = "Failed | Esc"
				}
			}
			if len(view.Lines) >= 3 {
				view.Lines[len(view.Lines)-1].Text = status
			}
			if height >= 7 {
				if pending {
					view.Lines[0].Text = "Searching tasks (previous results cannot be opened)"
				} else {
					view.Lines[0].Text = "Search failed: " + feedback
				}
			}
		}
		if err := terminal.paint(view); err != nil {
			return "", err
		}
		event, err := terminal.nextOr(ctx, worker.wake)
		if err != nil {
			return "", err
		}
		switch event.kind {
		case keyTaskReady:
			select {
			case result := <-worker.results:
				if result.request.generation != generation {
					continue
				}
				pending = false
				if result.err != nil {
					blocked, feedback = true, problemFor(result.err).Message
					continue
				}
				blocked, feedback = false, ""
				page = result.page
				position.query, position.cursor = result.request.query, result.request.cursor
				picker = taskPagePicker(page, current, position.focus)
				position.focus = picker.focused
			default:
			}
		case keyEscape:
			return "", nil
		case keyEOF:
			return "", io.EOF
		case keyInterrupt:
			return "", context.Canceled
		case keyUp, keyDown:
			action := pickerMoveUp
			if event.kind == keyDown {
				action = pickerMoveDown
			}
			picker, _ = picker.reduce(pickerAction{Kind: action})
			if !blocked {
				position.focus = picker.focused
			}
		case keyEnter, keyChoice:
			if blocked || !pickerViewportUsable(width, height) {
				continue
			}
			if event.kind == keyChoice {
				valid := false
				for _, index := range picker.matches() {
					if picker.options[index].Value == event.text {
						picker.focused, valid = event.text, true
					}
				}
				if !valid {
					continue
				}
			}
			switch picker.focused {
			case taskPickerOlder:
				request(position.query, page.NextCursor, false)
			case taskPickerRecent:
				request(position.query, "", false)
			case "", taskPickerEmpty:
			default:
				return picker.focused, nil
			}
		case keyText, keyPaste, keyBackspace, keyDelete, keyLeft, keyRight, keyHome, keyEnd, keyClear, keyKillEnd:
			if event.kind == keyPaste {
				event.text = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(event.text, "\r\n", " "), "\r", " "), "\n", " ")
			}
			before := editor.text
			if err := editor.apply(event); err != nil {
				return "", err
			}
			if before != editor.text {
				request(editor.text, "", true)
			}
		}
	}
}
