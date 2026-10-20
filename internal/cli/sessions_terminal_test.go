package cli

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/state"
)

type monitorFixture struct {
	mu      sync.Mutex
	task    core.Task
	attempt core.Attempt
	events  []core.Event
	started chan struct{}
	stopped chan struct{}
	queries chan string
}

func (s *monitorFixture) TaskSummaries(ctx context.Context, query, cursor string, limit int) (state.TaskSummaryPage, error) {
	if s.queries != nil {
		select {
		case s.queries <- query:
		default:
		}
	}
	if s.started != nil {
		close(s.started)
		<-ctx.Done()
		close(s.stopped)
		return state.TaskSummaryPage{}, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return state.TaskSummaryPage{Tasks: []state.TaskSummary{{ID: s.task.ID, Title: s.task.Title, Status: s.attempt.State}}}, nil
}
func (s *monitorFixture) TaskPage(context.Context, string, int, int) (core.Task, state.AttemptPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.task, state.AttemptPage{Attempts: []core.Attempt{s.attempt}}, nil
}
func (s *monitorFixture) AttemptEvents(_ context.Context, _ string, after int64, limit int) ([]core.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var events []core.Event
	for _, event := range s.events {
		if event.Sequence > after && len(events) < limit {
			events = append(events, event)
		}
	}
	return events, nil
}
func monitorSourceFixture() *monitorFixture {
	return &monitorFixture{task: core.Task{ID: "task-monitor", Title: "Monitor progress"},
		attempt: core.Attempt{ID: "attempt-monitor", TaskID: "task-monitor", State: "running"},
		events: []core.Event{{TaskID: "task-monitor", AttemptID: "attempt-monitor", Sequence: 1, Type: "accepted"},
			{TaskID: "task-monitor", AttemptID: "attempt-monitor", Sequence: 2, Type: "message_delta", Text: "First output"}}}
}

func awaitMonitorResult(t *testing.T, worker *sessionMonitorWorker, matches func(sessionMonitorResult) bool) sessionMonitorResult {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case result := <-worker.results:
			if matches(result) {
				return result
			}
		case <-timer.C:
			t.Fatal("monitor result deadline exceeded")
		}
	}
}

func TestSessionMonitorReadsLiveDeltasThenDurableCompletion(t *testing.T) {
	source := monitorSourceFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := newSessionMonitorWorker(ctx, source, 20*time.Millisecond)
	worker.requests <- sessionMonitorRequest{ctx: ctx, generation: 1, taskID: source.task.ID}
	first := awaitMonitorResult(t, worker, func(r sessionMonitorResult) bool { return r.snapshot.output == "First output" })
	if first.err != nil || first.snapshot.attempt.State != "running" || first.snapshot.sequence != 2 {
		t.Fatal("running output was not read from durable events", first)
	}
	source.mu.Lock()
	source.events = append(source.events, core.Event{TaskID: source.task.ID, AttemptID: source.attempt.ID, Sequence: 3, Type: "message_delta", Text: " and more"})
	source.mu.Unlock()
	awaitMonitorResult(t, worker, func(r sessionMonitorResult) bool {
		return r.snapshot.output == "First output and more" && r.snapshot.sequence == 3
	})
	source.mu.Lock()
	source.attempt.State, source.attempt.Output = "complete", "Durable final result"
	source.mu.Unlock()
	last := awaitMonitorResult(t, worker, func(r sessionMonitorResult) bool { return r.snapshot.attempt.State == "complete" })
	if last.err != nil || last.snapshot.output != "Durable final result" {
		t.Fatal("terminal receipt did not replace event preview", last)
	}
	cancel()
	select {
	case <-worker.done:
	case <-time.After(time.Second):
		t.Fatal("monitor worker survived cancellation")
	}
}

func TestSessionMonitorRejectsMismatchedAndMissingEventSequences(t *testing.T) {
	for _, mutate := range []func(*core.Event){func(e *core.Event) { e.TaskID = "other" }, func(e *core.Event) { e.AttemptID = "other" }, func(e *core.Event) { e.Sequence = 3 }} {
		source := monitorSourceFixture()
		mutate(&source.events[1])
		_, err := readSessionMonitor(context.Background(), source, sessionMonitorRequest{taskID: source.task.ID}, sessionMonitorSnapshot{})
		if err == nil {
			t.Fatal("invalid progress identity/sequence accepted")
		}
	}
}

func TestSessionMonitorBoundsOutputWithoutSplittingUTF8AndLabelsUnknown(t *testing.T) {
	text, clipped := sessionOutputTail(strings.Repeat("界", sessionMonitorOutputBytes))
	if !clipped || len(text) > sessionMonitorOutputBytes || !utf8.ValidString(text) {
		t.Fatal("unsafe output tail")
	}
	snapshot := sessionMonitorSnapshot{task: core.Task{ID: "task"}, attempt: core.Attempt{ID: "attempt", State: "unknown"}, output: text, clipped: true, more: true}
	lines := strings.Join(sessionMonitorDetail(snapshot), "\n")
	for _, want := range []string{"State: Outcome unknown", "inspect before sending again", "last 64 KiB", "More saved events", "No detach, stop or retry"} {
		if !strings.Contains(lines, want) {
			t.Fatal("missing truthful detail", want)
		}
	}
}

func TestSessionMonitorCancellationJoinsActiveRead(t *testing.T) {
	source := &monitorFixture{started: make(chan struct{}), stopped: make(chan struct{})}
	terminal := newTaskPickerTestTerminal()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- inspectSessionsTerminal(ctx, terminal, source, "", time.Second) }()
	select {
	case <-source.started:
	case <-ctx.Done():
		t.Fatal("read did not start")
	}
	terminal.events <- keyEvent{kind: keyEscape}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("monitor did not return")
	}
	select {
	case <-source.stopped:
	default:
		t.Fatal("monitor returned before canceling its read")
	}
}

func TestSessionMonitorSearchPasteKeepsSpacingAndLeftEditsQuery(t *testing.T) {
	source := monitorSourceFixture()
	source.queries = make(chan string, 32)
	terminal := newTaskPickerTestTerminal()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- inspectSessionsTerminal(ctx, terminal, source, "", time.Second) }()
	awaitQuery := func(want string) {
		t.Helper()
		for {
			select {
			case query := <-source.queries:
				if query == want {
					return
				}
			case <-ctx.Done():
				t.Fatalf("search query %q not observed", want)
			}
		}
	}
	awaitQuery("")
	terminal.events <- keyEvent{kind: keyPaste, text: "  Monitor  text\r\nnext  "}
	awaitQuery("  Monitor  text next  ")
	terminal.events <- keyEvent{kind: keyClear}
	terminal.events <- keyEvent{kind: keyText, text: "ab"}
	terminal.events <- keyEvent{kind: keyLeft}
	terminal.events <- keyEvent{kind: keyText, text: "X"}
	awaitQuery("aXb")
	terminal.events <- keyEvent{kind: keyEscape}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("monitor did not return")
	}
}

func awaitMonitorFrame(t *testing.T, terminal *taskPickerTestTerminal, match func(terminalFrame) bool) terminalFrame {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case frame := <-terminal.frames:
			if match(frame) {
				return frame
			}
		case <-timer.C:
			t.Fatal("monitor frame deadline exceeded")
		}
	}
}

func TestSessionMonitorInspectBackPreservesTaskQueryAndSavedWork(t *testing.T) {
	a, err := app.Open(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	attempt, err := a.Run(ctx, app.Options{Input: "Monitor completed fixture", CWD: t.TempDir(), Demo: true, Selection: core.Defaults()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, history, err := a.Task(ctx, attempt.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	terminal := newTaskPickerTestTerminal()
	done := make(chan error, 1)
	go func() { done <- inspectSessionsTerminal(ctx, terminal, a, before.ID, 40*time.Millisecond) }()
	awaitMonitorFrame(t, terminal, func(f terminalFrame) bool {
		return strings.Contains(terminalPickerText(f), before.Title) && strings.Contains(terminalPickerText(f), "Enter inspect")
	})
	terminal.events <- keyEvent{kind: keyText, text: "Monitor"}
	awaitMonitorFrame(t, terminal, func(f terminalFrame) bool {
		return strings.Contains(terminalPickerText(f), "Search: Monitor") && strings.Contains(terminalPickerText(f), "Enter inspect")
	})
	terminal.events <- keyEvent{kind: keyEnter}
	awaitMonitorFrame(t, terminal, func(f terminalFrame) bool { return strings.Contains(terminalPickerText(f), "State: Complete") })
	terminal.events <- keyEvent{kind: keyEscape}
	frame := awaitMonitorFrame(t, terminal, func(f terminalFrame) bool {
		return strings.Contains(terminalPickerText(f), "Sessions") && strings.Contains(terminalPickerText(f), "Search: Monitor") && strings.Contains(terminalPickerText(f), "Enter inspect")
	})
	found := false
	for _, line := range frame.Lines {
		if line.Focused && strings.Contains(line.Text, before.Title) {
			found = true
		}
	}
	if !found {
		t.Fatal("return lost focused session identity")
	}
	terminal.events <- keyEvent{kind: keyEscape}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("monitor did not close")
	}
	after, latest, err := a.Task(ctx, before.ID)
	if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(history, latest) {
		t.Fatal("inspection mutated saved work", err)
	}
}

func TestSessionMonitorFailedReadKeepsBackAvailable(t *testing.T) {
	view := sessionMonitorView{taskID: "task", feedback: "Refresh failed: fixture", snapshot: sessionMonitorSnapshot{task: core.Task{ID: "task", Title: "Known work"}}}
	for _, size := range [][2]int{{80, 16}, {60, 12}, {20, 5}} {
		frame := view.frame(size[0], size[1])
		text := terminalPickerText(frame)
		if len(frame.Lines) > size[1]-1 || !strings.Contains(text, "Esc") || !strings.Contains(text, "Refresh failed") {
			t.Fatal("error hid recovery controls", size, text)
		}
	}
}
