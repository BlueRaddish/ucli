package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/state"
)

const sessionMonitorOutputBytes = 64 << 10

type sessionMonitorSource interface {
	TaskSummaries(context.Context, string, string, int) (state.TaskSummaryPage, error)
	TaskPage(context.Context, string, int, int) (core.Task, state.AttemptPage, error)
	AttemptEvents(context.Context, string, int64, int) ([]core.Event, error)
}

type sessionMonitorRequest struct {
	ctx                   context.Context
	generation            uint64
	query, cursor, taskID string
}

type sessionMonitorSnapshot struct {
	page            state.TaskSummaryPage
	task            core.Task
	attempt         core.Attempt
	output, status  string
	sequence        int64
	more, clipped   bool
	readAt, eventAt time.Time
}

type sessionMonitorResult struct {
	request  sessionMonitorRequest
	snapshot sessionMonitorSnapshot
	err      error
}

type sessionMonitorWorker struct {
	requests chan sessionMonitorRequest
	results  chan sessionMonitorResult
	wake     chan struct{}
	done     chan struct{}
}

// One worker owns reads and the refresh timer. Results contain complete bounded
// view snapshots, so replacing an unpainted result cannot lose output deltas.
// The 64 KiB display tail is not an exported transcript; /result retains access
// to the full durable result. This keeps repaint cost bounded as history grows.
func newSessionMonitorWorker(ctx context.Context, source sessionMonitorSource, interval time.Duration) *sessionMonitorWorker {
	w := &sessionMonitorWorker{make(chan sessionMonitorRequest, 1), make(chan sessionMonitorResult, 1), make(chan struct{}, 1), make(chan struct{})}
	go func() {
		defer close(w.done)
		timer := time.NewTimer(time.Hour)
		defer timer.Stop()
		timer.Stop()
		var request sessionMonitorRequest
		var snapshot sessionMonitorSnapshot
		for {
			select {
			case <-ctx.Done():
				return
			case next := <-w.requests:
				// Explicit refresh continues this detail cursor; changing the view or
				// task starts fresh. readSessionMonitor also resets on attempt change.
				if next.taskID == "" || next.taskID != request.taskID {
					snapshot = sessionMonitorSnapshot{}
				}
				request = next
			case <-timer.C:
				if request.ctx == nil || request.ctx.Err() != nil {
					continue
				}
			}
			readCtx, cancel := context.WithTimeout(request.ctx, 3*time.Second)
			updated, err := readSessionMonitor(readCtx, source, request, snapshot)
			cancel()
			if ctx.Err() != nil {
				return
			}
			if request.ctx.Err() != nil {
				continue
			}
			if err == nil {
				snapshot = updated
			}
			select {
			case <-w.results:
			default:
			}
			w.results <- sessionMonitorResult{request, snapshot, err}
			select {
			case w.wake <- struct{}{}:
			default:
			}
			delay := interval
			if err == nil && snapshot.more {
				// Drain one bounded page per select iteration, so cancellation and
				// superseding requests remain eligible between available pages.
				delay = 0
			}
			timer.Reset(delay)
		}
	}()
	return w
}

func readSessionMonitor(ctx context.Context, source sessionMonitorSource, request sessionMonitorRequest, previous sessionMonitorSnapshot) (sessionMonitorSnapshot, error) {
	if request.taskID == "" {
		page, err := source.TaskSummaries(ctx, request.query, request.cursor, 20)
		return sessionMonitorSnapshot{page: page, readAt: time.Now().UTC()}, err
	}
	task, page, err := source.TaskPage(ctx, request.taskID, 0, 1)
	if err != nil {
		return previous, err
	}
	if len(page.Attempts) == 0 {
		return sessionMonitorSnapshot{task: task, readAt: time.Now().UTC()}, nil
	}
	attempt := page.Attempts[len(page.Attempts)-1]
	if attempt.ID != previous.attempt.ID {
		previous = sessionMonitorSnapshot{}
	}
	previous.task, previous.attempt = task, attempt
	events, err := source.AttemptEvents(ctx, attempt.ID, previous.sequence, 100)
	if err != nil {
		return previous, err
	}
	for _, event := range events {
		if event.AttemptID != attempt.ID || event.TaskID != task.ID || event.Sequence != previous.sequence+1 {
			return previous, core.Problem("monitor_event_identity", "Saved progress did not match this attempt. Refresh or inspect the saved task.", 9)
		}
		previous.sequence, previous.eventAt = event.Sequence, event.At
		switch event.Type {
		case "message_delta":
			text, clipped := sessionOutputTail(previous.output + event.Text)
			previous.output, previous.clipped = text, previous.clipped || clipped
		case "status", "warning", "error", "tool_activity", "input_required":
			previous.status = event.Text
		}
	}
	previous.more = len(events) == 100
	if attempt.State != "running" {
		// A terminal receipt supplies the whole persisted result independently of
		// how many event pages the monitor has caught up with.
		previous.output, previous.clipped = sessionOutputTail(attempt.Output)
		previous.more = false
	}
	previous.readAt = time.Now().UTC()
	return previous, nil
}

func sessionOutputTail(text string) (string, bool) {
	if len(text) <= sessionMonitorOutputBytes {
		return text, false
	}
	start := len(text) - sessionMonitorOutputBytes
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:], true
}

func (s *session) monitorSessions(ctx context.Context) error {
	if s.terminal == nil {
		page, err := s.app.TaskSummaries(ctx, "", "", 20)
		if err != nil {
			return err
		}
		if err = s.out.text("Sessions (saved tasks in this UCLI home; current snapshot)\n"); err != nil {
			return err
		}
		for _, task := range page.Tasks {
			if err = s.out.text(fmt.Sprintf("%s | %s | %s\n", task.Title, task.Status, task.ID)); err != nil {
				return err
			}
		}
		if len(page.Tasks) == 0 {
			_ = s.out.text("No saved sessions yet.\n")
		}
		return s.out.text("Use /tasks to choose a session, then /result to inspect saved output. No work started.\n")
	}
	current := ""
	if s.task != nil {
		current = s.task.ID
	}
	defer s.terminal.paint(terminalFrame{CursorRow: -1})
	return inspectSessionsTerminal(ctx, s.terminal, s.app, current, 2*time.Second)
}

type sessionMonitorView struct {
	query                           textEditor
	cursor, focus, taskID, feedback string
	snapshot                        sessionMonitorSnapshot
	picker                          pickerState
	loading                         bool
	scroll, cacheWidth              int
	cache                           []string
}

func inspectSessionsTerminal(ctx context.Context, terminal taskPickerTerminal, source sessionMonitorSource, current string, interval time.Duration) (err error) {
	workerCtx, stop := context.WithCancel(ctx)
	worker := newSessionMonitorWorker(workerCtx, source, interval)
	var cancelRead context.CancelFunc
	defer func() {
		if cancelRead != nil {
			cancelRead()
		}
		stop()
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-worker.done:
		case <-timer.C:
			err = core.Problem("session_monitor_timeout", "Session monitor did not stop within its deadline. Leave this session before opening it again.", 9)
		}
	}()
	v := sessionMonitorView{picker: newPicker("Sessions · saved tasks", nil, current)}
	var generation uint64
	request := func() {
		if cancelRead != nil {
			cancelRead()
		}
		generation++
		readCtx, cancel := context.WithCancel(workerCtx)
		cancelRead = cancel
		v.loading, v.feedback = true, ""
		select {
		case <-worker.requests:
		default:
		}
		worker.requests <- sessionMonitorRequest{readCtx, generation, v.query.text, v.cursor, v.taskID}
	}
	request()
	for {
		width, height := 80, 24
		if sized, ok := terminal.(interface{ size() (int, int) }); ok {
			width, height = sized.size()
		}
		if err = terminal.paint(v.frame(width, height)); err != nil {
			return err
		}
		event, eventErr := terminal.nextOr(ctx, worker.wake)
		if eventErr != nil {
			return eventErr
		}
		switch event.kind {
		case keyEOF:
			return io.EOF
		case keyInterrupt:
			return context.Canceled
		case keyEscape:
			if v.taskID == "" {
				return nil
			}
			v.taskID, v.scroll, v.cache = "", 0, nil
			request()
		case keyTaskReady:
			select {
			case result := <-worker.results:
				if result.request.generation != generation {
					continue
				}
				v.loading = false
				if result.err != nil {
					v.feedback = "Refresh failed: " + problemFor(result.err).Message
					continue
				}
				v.snapshot, v.feedback, v.cache = result.snapshot, "", nil
				if v.taskID == "" {
					v.picker = taskPagePicker(v.snapshot.page, current, v.focus)
					v.picker.title = "Sessions · saved tasks"
					for i := range v.picker.options {
						for _, task := range v.snapshot.page.Tasks {
							if v.picker.options[i].Value == task.ID {
								v.picker.options[i].Label = task.Title + " · " + sessionStateLabel(task.Status)
								v.picker.options[i].Detail = "Location: " + task.Workspace.Dir
								if task.HasDraft {
									v.picker.options[i].Detail += " · draft saved"
								}
							}
						}
						switch v.picker.options[i].Value {
						case taskPickerOlder:
							v.picker.options[i].Label = "Older sessions"
						case taskPickerRecent:
							v.picker.options[i].Label = "Recent sessions / refresh"
						case taskPickerEmpty:
							v.picker.options[i].Label = "No matching sessions"
						}
					}
					v.focus = v.picker.focused
				}
			default:
			}
		default:
			if !pickerViewportUsable(width, height) {
				continue
			}
			if v.taskID != "" {
				switch event.kind {
				case keyLeft:
					v.taskID, v.scroll, v.cache = "", 0, nil
					request()
				case keyUp:
					v.scroll--
				case keyDown:
					v.scroll++
				case keyPageUp:
					v.scroll -= max(1, height-4)
				case keyPageDown:
					v.scroll += max(1, height-4)
				case keyHome:
					v.scroll = 0
				case keyEnd:
					v.scroll = len(v.cache)
				case keyText:
					if event.text == "r" {
						request()
					}
				}
				continue
			}
			switch event.kind {
			case keyUp, keyDown:
				direction := pickerMoveUp
				if event.kind == keyDown {
					direction = pickerMoveDown
				}
				v.picker, _ = v.picker.reduce(pickerAction{Kind: direction})
				v.focus = v.picker.focused
			case keyEnter:
				if v.loading || v.feedback != "" {
					continue
				}
				switch v.focus {
				case taskPickerOlder:
					v.cursor = v.snapshot.page.NextCursor
				case taskPickerRecent:
					v.cursor = ""
				case "", taskPickerEmpty:
					continue
				default:
					v.taskID, v.snapshot, v.scroll, v.cache = v.focus, sessionMonitorSnapshot{}, 0, nil
				}
				request()
			case keyText, keyPaste, keyBackspace, keyDelete, keyLeft, keyRight, keyHome, keyEnd, keyClear, keyKillEnd:
				if event.kind == keyPaste {
					event.text = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(event.text, "\r\n", " "), "\r", " "), "\n", " ")
				}
				candidate := v.query
				if applyErr := candidate.apply(event); applyErr != nil {
					v.feedback = problemFor(applyErr).Message
					continue
				}
				if len(candidate.text) > 256 {
					v.feedback = "Search is limited to 256 UTF-8 bytes. Clear or shorten it."
					continue
				}
				changed := candidate.text != v.query.text
				v.query = candidate
				if changed {
					v.cursor, v.focus = "", ""
					request()
				}
			}
		}
	}
}

func (v *sessionMonitorView) frame(width, height int) terminalFrame {
	if v.taskID == "" {
		view := taskPickerFrame(v.picker, v.query, width, height)
		if len(view.Lines) == 0 {
			return view
		}
		footer := "Enter inspect · Esc back · Up/Down · Type search"
		if width < 50 {
			footer = "Enter inspect · Esc back"
		}
		if v.loading {
			footer = "Reading saved sessions · Esc back"
		}
		if v.feedback != "" {
			footer = v.feedback + " · Esc back"
		}
		view.Lines[len(view.Lines)-1].Text = footer
		return view
	}
	view := terminalFrame{CursorRow: -1}
	budget := max(1, height-1)
	if width < 20 || height < 5 {
		view.Lines = []terminalLine{{Text: "Session · resize / Esc back"}}
		return view
	}
	title := "Session · " + v.snapshot.task.Title
	if v.snapshot.task.ID == "" {
		title = "Session · reading saved work"
	}
	view.Lines = append(view.Lines, terminalLine{Text: title, Tone: toneInfo})
	if v.cache == nil || v.cacheWidth != width {
		v.cache = nil
		for _, line := range sessionMonitorDetail(v.snapshot) {
			v.cache = append(v.cache, workflowWrap(line, max(1, width-1))...)
		}
		v.cacheWidth = width
	}
	status := "Refreshes every 2s · recorded state, not executor liveness"
	if v.loading {
		status = "Reading saved progress · Esc back"
	}
	if v.feedback != "" {
		status = v.feedback + " · previous read retained"
	}
	view.Lines = append(view.Lines, terminalLine{Text: status, Dim: true})
	rows := max(1, budget-len(view.Lines)-1)
	v.scroll = min(max(0, v.scroll), max(0, len(v.cache)-rows))
	for _, line := range v.cache[v.scroll:min(len(v.cache), v.scroll+rows)] {
		view.Lines = append(view.Lines, terminalLine{Text: line})
	}
	footer := "Esc/Left back · Up/Down scroll · End latest · r refresh"
	if width < 55 {
		footer = "Esc back · Up/Down scroll · r refresh"
	}
	view.Lines = append(view.Lines, terminalLine{Text: footer, Dim: true})
	return view
}

func sessionMonitorDetail(snapshot sessionMonitorSnapshot) []string {
	if snapshot.task.ID == "" {
		return []string{"No saved detail read yet."}
	}
	attempt := snapshot.attempt
	if attempt.ID == "" {
		return []string{"Draft saved · no run accepted", "Viewing does not change or submit the draft.", "Location: " + snapshot.task.Workspace.Dir}
	}
	lines := []string{"State: " + sessionStateLabel(attempt.State), "Model: " + attempt.Effective.Model + " · effort " + attempt.Effective.Effort,
		"Permission: " + attempt.Effective.Permission}
	if attempt.State == "running" {
		lines = append(lines, "Recorded running does not prove the process is alive.")
	}
	if attempt.State == "unknown" {
		lines = append(lines, "Outcome unknown · inspect before sending again.")
	}
	if !snapshot.eventAt.IsZero() {
		lines = append(lines, "Last saved event: "+snapshot.eventAt.Format(time.RFC3339))
	}
	if snapshot.status != "" {
		lines = append(lines, "Saved activity: "+snapshot.status)
	}
	if attempt.Error != "" {
		lines = append(lines, "Problem: "+attempt.Error)
	}
	for _, warning := range attempt.Warnings {
		lines = append(lines, "Warning: "+warning)
	}
	lines = append(lines, "", "Saved output")
	if snapshot.clipped {
		lines = append(lines, "Showing the last 64 KiB of this read. Use /result for the full saved result.")
	}
	if snapshot.more {
		lines = append(lines, "More saved events are waiting to be read; the next refresh continues.")
	}
	if snapshot.output == "" {
		lines = append(lines, "No output saved yet.")
	} else {
		lines = append(lines, strings.Split(snapshot.output, "\n")...)
	}
	return append(lines, "", "UCLI task: "+snapshot.task.ID, "Attempt: "+attempt.ID,
		"Location: "+snapshot.task.Workspace.Dir, "No detach, stop or retry is performed from this view.")
}

func sessionStateLabel(state string) string {
	switch state {
	case "running":
		return "Working (last recorded)"
	case "complete":
		return "Complete"
	case "failed":
		return "Failed"
	case "interrupted":
		return "Interrupted"
	case "unknown":
		return "Outcome unknown"
	case "draft":
		return "Draft"
	default:
		return "State unavailable"
	}
}
