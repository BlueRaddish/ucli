package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/state"
)

type taskPickerTestTerminal struct {
	events        chan keyEvent
	frames        chan terminalFrame
	width, height int
}

func newTaskPickerTestTerminal() *taskPickerTestTerminal {
	return &taskPickerTestTerminal{events: make(chan keyEvent, 32), frames: make(chan terminalFrame, 128)}
}
func (t *taskPickerTestTerminal) next(ctx context.Context) (keyEvent, error) {
	return t.nextOr(ctx, nil)
}
func (t *taskPickerTestTerminal) nextOr(ctx context.Context, wake <-chan struct{}) (keyEvent, error) {
	select {
	case <-ctx.Done():
		return keyEvent{}, ctx.Err()
	case event := <-t.events:
		if event.kind == keyResize && event.text == "usable" {
			t.width, t.height = 100, 16
		}
		return event, nil
	case <-wake:
		return keyEvent{kind: keyTaskReady}, nil
	}
}
func (t *taskPickerTestTerminal) paint(frame terminalFrame) error {
	select {
	case t.frames <- frame:
		return nil
	default:
		return errors.New("test frame buffer exhausted")
	}
}
func (t *taskPickerTestTerminal) size() (int, int) {
	if t.width == 0 || t.height == 0 {
		return 100, 16
	}
	return t.width, t.height
}

type taskPickerResult struct {
	value string
	err   error
}

func startTaskPickerTest(t *testing.T, terminal *taskPickerTestTerminal, source taskSummarySource, current string, position *taskFinderPosition) <-chan taskPickerResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	result := make(chan taskPickerResult, 1)
	go func() {
		value, err := pickTaskTerminal(ctx, terminal, source, current, position)
		result <- taskPickerResult{value, err}
	}()
	return result
}
func awaitTaskPickerFrame(t *testing.T, terminal *taskPickerTestTerminal, needle string, ready bool) terminalFrame {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case frame := <-terminal.frames:
			text := terminalPickerText(frame)
			if strings.Contains(text, "STALE RESULT") {
				t.Fatal("superseded backend result reached the visible page")
			}
			if strings.Contains(text, needle) && (!ready || !strings.Contains(text, "Searching")) {
				return frame
			}
		case <-timer.C:
			t.Fatalf("picker never displayed %q", needle)
		}
	}
}
func awaitTaskPickerResult(t *testing.T, result <-chan taskPickerResult) taskPickerResult {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(time.Second):
		t.Fatal("picker failed to return promptly")
		return taskPickerResult{}
	}
}

type taskSummaryFunc func(context.Context, string, string, int) (state.TaskSummaryPage, error)

func (f taskSummaryFunc) TaskSummaries(ctx context.Context, query, cursor string, limit int) (state.TaskSummaryPage, error) {
	return f(ctx, query, cursor, limit)
}

func TestTaskTerminalSupersedesSlowSearchWithoutBlockingKeys(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	queries := make(chan string, 8)
	source := taskSummaryFunc(func(ctx context.Context, query, cursor string, limit int) (state.TaskSummaryPage, error) {
		queries <- query
		switch query {
		case "":
			return state.TaskSummaryPage{Tasks: []state.TaskSummary{{ID: "a", Title: "Alpha"}, {ID: "b", Title: "Beta"}}}, nil
		case "slow":
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release // Deliberately return a stale success after cancellation.
			return state.TaskSummaryPage{Tasks: []state.TaskSummary{{ID: "stale", Title: "STALE RESULT"}}}, nil
		case "0 /older":
			return state.TaskSummaryPage{Tasks: []state.TaskSummary{{ID: "literal", Title: "0 /older"}}}, nil
		default:
			return state.TaskSummaryPage{}, fmt.Errorf("unexpected query %q", query)
		}
	})
	terminal := newTaskPickerTestTerminal()
	position := taskFinderPosition{}
	result := startTaskPickerTest(t, terminal, source, "a", &position)
	awaitTaskPickerFrame(t, terminal, "> Alpha (current)", true)
	terminal.events <- keyEvent{kind: keyText, text: "slow"}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow query did not start")
	}
	terminal.events <- keyEvent{kind: keyDown}
	terminal.events <- keyEvent{kind: keyEnter}
	awaitTaskPickerFrame(t, terminal, "> Beta", false)
	select {
	case value := <-result:
		t.Fatalf("pending Enter opened stale row: %+v", value)
	default:
	}
	terminal.events <- keyEvent{kind: keyClear}
	terminal.events <- keyEvent{kind: keyPaste, text: "0 /older"}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("superseded query context stayed active")
	}
	awaitTaskPickerFrame(t, terminal, "Search: 0 /older", false)
	close(release)
	awaitTaskPickerFrame(t, terminal, "> 0 /older", true)
	terminal.events <- keyEvent{kind: keyEnter}
	got := awaitTaskPickerResult(t, result)
	if got.err != nil || got.value != "literal" || position.query != "0 /older" || position.cursor != "" || position.focus != "literal" {
		t.Fatalf("latest query not selected: %+v %+v", got, position)
	}
	for _, want := range []string{"", "slow", "0 /older"} {
		select {
		case query := <-queries:
			if query != want {
				t.Fatalf("query backlog: got %q want %q", query, want)
			}
		default:
			t.Fatalf("query %q not observed", want)
		}
	}
	select {
	case query := <-queries:
		t.Fatalf("unnecessary intermediate request %q", query)
	default:
	}
}

func TestTaskTerminalEscapeCancelsReadAndKeepsLastGoodPosition(t *testing.T) {
	started, ended := make(chan struct{}), make(chan struct{})
	source := taskSummaryFunc(func(ctx context.Context, query, cursor string, limit int) (state.TaskSummaryPage, error) {
		close(started)
		<-ctx.Done()
		close(ended)
		return state.TaskSummaryPage{}, ctx.Err()
	})
	position := taskFinderPosition{"last good", "older cursor", "stable task"}
	before := position
	terminal := newTaskPickerTestTerminal()
	result := startTaskPickerTest(t, terminal, source, "active task", &position)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("read not started")
	}
	terminal.events <- keyEvent{kind: keyEscape}
	got := awaitTaskPickerResult(t, result)
	if got.err != nil || got.value != "" || position != before {
		t.Fatalf("Escape changed finder context: %+v %+v", got, position)
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("Escape left its backend read active")
	}
}

func TestTaskTerminalSearchFailureCannotSelectOldPage(t *testing.T) {
	source := taskSummaryFunc(func(ctx context.Context, query, cursor string, limit int) (state.TaskSummaryPage, error) {
		if query == "good" {
			return state.TaskSummaryPage{Tasks: []state.TaskSummary{{ID: "a", Title: "Good task"}}}, nil
		}
		return state.TaskSummaryPage{}, core.Problem("invalid_input", "Search rejected; edit the query", 2)
	})
	position := taskFinderPosition{"good", "older cursor", "a"}
	before := position
	terminal := newTaskPickerTestTerminal()
	result := startTaskPickerTest(t, terminal, source, "a", &position)
	awaitTaskPickerFrame(t, terminal, "> Good task", true)
	terminal.events <- keyEvent{kind: keyClear}
	terminal.events <- keyEvent{kind: keyText, text: "bad"}
	awaitTaskPickerFrame(t, terminal, "Search failed: Search rejected", true)
	terminal.events <- keyEvent{kind: keyEnter}
	terminal.events <- keyEvent{kind: keyEscape}
	got := awaitTaskPickerResult(t, result)
	if got.err != nil || got.value != "" || position != before {
		t.Fatalf("failed search changed context or opened old task: %+v %+v", got, position)
	}
}

func TestTaskTerminalSearchesAndPagesRealArchiveWithoutRunningTasks(t *testing.T) {
	a, err := app.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	cwd, other := t.TempDir(), t.TempDir()
	selection := core.Defaults()
	selection.Model, selection.Effort = "demo/deep", "high"
	active, err := a.Draft(ctx, "", "Ancient saved draft", cwd, selection)
	if err != nil {
		t.Fatal(err)
	}
	selection.Model, selection.Effort = "demo/fast", "low"
	literal, err := a.Draft(ctx, "", "0 /older", other, selection)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 21 {
		if _, err := a.Draft(ctx, "", fmt.Sprintf("Newer filler %02d", i), other, core.Defaults()); err != nil {
			t.Fatal(err)
		}
	}
	position := taskFinderPosition{}
	terminal := newTaskPickerTestTerminal()
	result := startTaskPickerTest(t, terminal, a, active.ID, &position)
	awaitTaskPickerFrame(t, terminal, "Open a task", true)
	// From the first task, wrapping Up reaches Recent then Older without loading
	// the archive into the frontend or interpreting numeric/slash search text.
	terminal.events <- keyEvent{kind: keyUp}
	terminal.events <- keyEvent{kind: keyUp}
	terminal.events <- keyEvent{kind: keyEnter}
	awaitTaskPickerFrame(t, terminal, "> Ancient saved draft (current)", true)
	terminal.events <- keyEvent{kind: keyEscape}
	got := awaitTaskPickerResult(t, result)
	if got.err != nil || got.value != "" || position.cursor == "" || position.query != "" || position.focus != active.ID {
		t.Fatalf("old page/cancel context: %+v %+v", got, position)
	}
	oldPosition := position
	terminal = newTaskPickerTestTerminal()
	result = startTaskPickerTest(t, terminal, a, active.ID, &position)
	awaitTaskPickerFrame(t, terminal, "> Ancient saved draft (current)", true)
	terminal.events <- keyEvent{kind: keyEscape}
	got = awaitTaskPickerResult(t, result)
	if got.err != nil || position != oldPosition {
		t.Fatal("Back/reopen lost query/page/focus")
	}
	terminal = newTaskPickerTestTerminal()
	result = startTaskPickerTest(t, terminal, a, active.ID, &position)
	awaitTaskPickerFrame(t, terminal, "> Ancient saved draft (current)", true)
	// An independent update must not refresh this caller's displayed CAS revision.
	updated, err := a.DraftAt(ctx, active.ID, "External edit", cwd, active.Selection, &active.Revision)
	if err != nil {
		t.Fatal(err)
	}
	terminal.events <- keyEvent{kind: keyPaste, text: "0 /older"}
	awaitTaskPickerFrame(t, terminal, "> 0 /older", true)
	terminal.events <- keyEvent{kind: keyEnter}
	got = awaitTaskPickerResult(t, result)
	if got.err != nil || got.value != literal.ID || position.query != "0 /older" || active.Revision == updated.Revision || active.Draft != "Ancient saved draft" || active.Selection.Model != "demo/deep" {
		t.Fatalf("literal search lost task/CAS/pin context: %+v %+v %+v", got, position, active)
	}
	page, err := a.TaskSummaries(ctx, "", "", 100)
	if err != nil || len(page.Tasks) != 23 {
		t.Fatalf("archive changed: %d %v", len(page.Tasks), err)
	}
	for _, summary := range page.Tasks {
		_, attempts, err := a.Task(ctx, summary.ID)
		if err != nil || len(attempts) != 0 {
			t.Fatalf("finder submitted task %s: %v", summary.ID, err)
		}
	}
	selected, _, err := a.TaskPage(ctx, got.value, 0, 1)
	if err != nil || selected.Draft != "0 /older" || selected.Selection.Model != "demo/fast" || selected.Selection.Effort != "low" || selected.Revision != literal.Revision {
		t.Fatal("selected ID did not preserve persisted draft/settings/revision")
	}
}

func TestTaskTerminalPageActionsRemainVisibleWithManyRows(t *testing.T) {
	page := state.TaskSummaryPage{NextCursor: "older"}
	for i := range 20 {
		page.Tasks = append(page.Tasks, state.TaskSummary{ID: fmt.Sprint(i), Title: fmt.Sprintf("Task %02d", i)})
	}
	picker := taskPagePicker(page, "0", "")
	for _, height := range []int{6, 8, 16} {
		frame := taskPickerFrame(picker, textEditor{}, 70, height)
		text := terminalPickerText(frame)
		for _, want := range []string{"> Task 00 (current)", "  Older tasks", "  Recent tasks", "Esc cancel"} {
			if !strings.Contains(text, want) {
				t.Fatalf("height %d hides %q: %s", height, want, text)
			}
		}
		if len(frame.Lines) > height-1 {
			t.Fatalf("height %d clips controls", height)
		}
	}
	picker, _ = picker.reduce(pickerAction{Kind: pickerMoveUp})
	frame := taskPickerFrame(picker, textEditor{}, 70, 16)
	focused := 0
	for _, line := range frame.Lines {
		if line.Focused {
			focused++
			if line.Text != "> Recent tasks" {
				t.Fatal("paging focus highlights a stale task")
			}
		}
	}
	if focused != 1 {
		t.Fatal("paging action has no distinct focus")
	}
}

func TestTaskTerminalJoinsCanceledReadBeforeReturning(t *testing.T) {
	canceled, release, ended := make(chan struct{}), make(chan struct{}), make(chan struct{})
	started := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	source := taskSummaryFunc(func(ctx context.Context, query, cursor string, limit int) (state.TaskSummaryPage, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		close(ended)
		return state.TaskSummaryPage{}, ctx.Err()
	})
	terminal := newTaskPickerTestTerminal()
	position := taskFinderPosition{}
	result := startTaskPickerTest(t, terminal, source, "a", &position)
	awaitTaskPickerFrame(t, terminal, "Searching", false)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	// Wait for the initial request to start, then have the canceled source take
	// one bounded cleanup window. Returning to the composer must wait for it.
	terminal.events <- keyEvent{kind: keyEscape}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("worker not canceled")
	}
	select {
	case <-result:
		t.Fatal("picker returned with its source still running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	got := awaitTaskPickerResult(t, result)
	if got.err != nil || got.value != "" {
		t.Fatalf("joined read returned selection/error: %+v", got)
	}
	select {
	case <-ended:
	default:
		t.Fatal("source still active after picker returned")
	}
}

func TestTaskTerminalReportsUncooperativeWorkerDeadline(t *testing.T) {
	started, release, ended := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	source := taskSummaryFunc(func(ctx context.Context, query, cursor string, limit int) (state.TaskSummaryPage, error) {
		close(started)
		<-release
		close(ended)
		return state.TaskSummaryPage{}, nil
	})
	terminal := newTaskPickerTestTerminal()
	position := taskFinderPosition{}
	result := startTaskPickerTest(t, terminal, source, "a", &position)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	terminal.events <- keyEvent{kind: keyEscape}
	got := awaitTaskPickerResult(t, result)
	var problem *core.Error
	if got.value != "" || !errors.As(got.err, &problem) || problem.Code != "task_search_timeout" {
		t.Fatalf("unjoined source returned success: %+v", got)
	}
	close(release)
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("test failed to release blocked source")
	}
}

func TestTaskTerminalCannotConfirmAtUnusableSize(t *testing.T) {
	source := taskSummaryFunc(func(ctx context.Context, query, cursor string, limit int) (state.TaskSummaryPage, error) {
		return state.TaskSummaryPage{Tasks: []state.TaskSummary{{ID: "a", Title: "Alpha"}}}, nil
	})
	terminal := newTaskPickerTestTerminal()
	terminal.width, terminal.height = 80, 3
	position := taskFinderPosition{}
	result := startTaskPickerTest(t, terminal, source, "a", &position)
	awaitTaskPickerFrame(t, terminal, "Resize", true) // Initial pending frame.
	awaitTaskPickerFrame(t, terminal, "Resize", true) // Backend result now available.
	terminal.events <- keyEvent{kind: keyEnter}
	awaitTaskPickerFrame(t, terminal, "Resize", true)
	select {
	case value := <-result:
		t.Fatalf("tiny terminal opened invisible task: %+v", value)
	default:
	}
	terminal.events <- keyEvent{kind: keyResize, text: "usable"}
	awaitTaskPickerFrame(t, terminal, "> Alpha (current)", true)
	terminal.events <- keyEvent{kind: keyEnter}
	got := awaitTaskPickerResult(t, result)
	if got.err != nil || got.value != "a" {
		t.Fatalf("resize did not restore explicit selection: %+v", got)
	}
}
