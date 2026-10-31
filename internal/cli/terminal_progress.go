package cli

import (
	"io"
	"strings"
	"time"

	"ucli.local/ucli/internal/core"
)

// Progress is a display projection. It never decides acceptance, cancellation,
// native identity, or whether a result was durably saved.
type jobPhase uint8

const (
	jobPreparing jobPhase = iota + 1
	jobAccepted
	jobClientRunning
	jobReceiving
	jobSaved
	jobBlocked
	jobViewPaused
)

type terminalJobMsg struct {
	TaskID, AttemptID, Model, Effort, Permission, Title string
	Phase                                               jobPhase
	State, Detail                                       string
	At                                                  time.Time
	Bind                                                bool // only the UI submission owner binds a newly accepted attempt
}

type terminalJob struct {
	terminalJobMsg
	Started time.Time
	Events  []string
}

type terminalJobs struct {
	items []terminalJob // oldest first, at most sixteen; no native transcript copy
	focus string
}

type terminalOpenJobMsg struct{}

func (j *terminalJobs) update(msg terminalJobMsg) {
	if !validTranscriptScopeID(msg.TaskID) || msg.AttemptID != "" && !validTranscriptScopeID(msg.AttemptID) {
		return
	}
	if msg.At.IsZero() {
		msg.At = time.Now().UTC()
	}
	index := -1
	for i := range j.items {
		if j.items[i].TaskID == msg.TaskID {
			index = i
			break
		}
	}
	fresh := index < 0
	if fresh {
		j.items = append(j.items, terminalJob{terminalJobMsg: terminalJobMsg{TaskID: msg.TaskID}, Started: msg.At})
		index = len(j.items) - 1
	}
	job := &j.items[index]
	if msg.Phase == jobPreparing {
		*job = terminalJob{terminalJobMsg: msg, Started: msg.At}
	} else if !fresh {
		// Worker completion can precede observer drain. During a new preparation,
		// every old observer is excluded until the submission owner supplies the
		// exact newly accepted handle. Durable 'accepted' alone cannot rebind it.
		if job.AttemptID == "" && msg.AttemptID != "" && !msg.Bind {
			return
		}
		if job.AttemptID != "" && msg.AttemptID != "" && msg.AttemptID != job.AttemptID {
			return
		}
	}
	// A late observation cannot turn an already saved result back into work.
	if job.Phase == jobSaved && msg.Phase != jobSaved && msg.Phase != jobPreparing {
		return
	}
	if msg.AttemptID != "" {
		job.AttemptID = msg.AttemptID
	}
	for _, pair := range []struct {
		dst *string
		src string
	}{{&job.Model, msg.Model}, {&job.Effort, msg.Effort}, {&job.Permission, msg.Permission}, {&job.Title, msg.Title}} {
		if pair.src != "" {
			*pair.dst = clipCells(inlineText(pair.src), 160)
		}
	}
	previous := job.Phase
	if msg.Phase != 0 {
		job.Phase = msg.Phase
	}
	if msg.State != "" {
		job.State = msg.State
	}
	job.At = msg.At
	if msg.Detail != "" || previous != job.Phase || len(job.Events) == 0 {
		detail := clipCells(inlineText(msg.Detail), 240)
		if detail == "" {
			detail = job.label()
		}
		line := msg.At.Local().Format("15:04:05") + "  " + detail
		if len(job.Events) == 0 || !strings.HasSuffix(job.Events[len(job.Events)-1], "  "+detail) {
			job.Events = append(job.Events, line)
			if len(job.Events) > 32 {
				job.Events = append([]string(nil), job.Events[len(job.Events)-32:]...)
			}
		}
	}
	if len(j.items) > 16 {
		j.items = append([]terminalJob(nil), j.items[len(j.items)-16:]...)
	}
	if j.focus == "" || msg.Phase == jobPreparing {
		j.focus = msg.TaskID
	}
}

func (j terminalJobs) selected(task string) (terminalJob, bool) {
	for _, key := range []string{task, j.focus} {
		for i := len(j.items) - 1; i >= 0; i-- {
			if j.items[i].TaskID == key {
				return j.items[i], true
			}
		}
	}
	if len(j.items) > 0 {
		return j.items[len(j.items)-1], true
	}
	return terminalJob{}, false
}

func (j terminalJob) label() string {
	switch j.Phase {
	case jobPreparing:
		return "Preparing request"
	case jobAccepted:
		return "Request accepted · waiting for response"
	case jobClientRunning:
		return "Client started · waiting for response"
	case jobReceiving:
		return "Receiving response"
	case jobSaved:
		if j.State == "complete" {
			return "Complete · response saved"
		}
		return sessionStateLabel(j.State) + " · outcome saved"
	case jobBlocked:
		return "Request did not start"
	case jobViewPaused:
		return "Live view paused · inspect saved session"
	}
	return "Waiting for progress"
}

func (m terminalModel) progressLine(width int) (terminalLine, bool) {
	job, ok := m.jobs.selected(m.shell.chrome.TaskID)
	if !ok {
		return terminalLine{}, false
	}
	text := job.label()
	if width >= 60 && job.Model != "" {
		text = job.Model + " · " + text
	}
	suffix := "  [Details · F3]"
	if width < 40 {
		suffix = " [F3]"
	}
	tone := toneInfo
	if job.Phase == jobSaved && job.State == "complete" {
		tone = toneSuccess
	} else if job.Phase == jobBlocked || job.Phase == jobViewPaused || job.Phase == jobSaved {
		tone = toneWarning
	}
	return terminalLine{Text: clipCells(text, max(1, width-1-cellWidth(suffix))) + suffix, Tone: tone, Action: "job:open"}, true
}

func (m terminalModel) inspectorFrame(width, height int) terminalFrame {
	frame := terminalFrame{CursorRow: -1}
	job, ok := m.jobs.selected(m.jobs.focus)
	if !ok {
		frame.Lines = []terminalLine{{Text: "Job details", Bold: true}, {Text: "No request has started in this UCLI session."}, {Text: "[Back]", Action: "job:close", Tone: toneInfo}}
		return frame
	}
	frame.Lines = append(frame.Lines, terminalLine{Text: "Job details  [Back · Esc]", Bold: true, Tone: toneInfo, Action: "job:close"})
	var content []terminalLine
	add := func(text string, tone terminalTone) {
		for _, line := range workflowWrap(safe(text), max(1, width-3)) {
			content = append(content, terminalLine{Text: "  " + line, Tone: tone})
		}
	}
	add(job.label(), toneInfo)
	if job.Title != "" {
		add(job.Title, toneNormal)
	}
	add("Model: "+job.Model+" · effort "+job.Effort, toneNormal)
	add("Permission: "+job.Permission, toneNormal)
	content = append(content, terminalLine{}, terminalLine{Text: "  Activity", Bold: true})
	for _, event := range job.Events {
		add(event, toneNormal)
	}
	content = append(content, terminalLine{}, terminalLine{Text: "  Response so far", Bold: true})
	output := ""
	for _, record := range m.shell.transcript {
		if record.Role == transcriptAssistant && record.TaskID == job.TaskID && record.AttemptID == job.AttemptID {
			output = record.Text
		}
	}
	if output == "" {
		add("No response received yet.", toneNormal)
	} else {
		if len(output) > 4096 {
			output = streamFragment(output)
			add("Recent response tail; the full saved result is in Sessions.", toneNormal)
		}
		for _, line := range strings.Split(output, "\n") {
			add(line, toneNormal)
		}
	}
	content = append(content, terminalLine{})
	add("Task: "+job.TaskID, toneNormal)
	if job.AttemptID != "" {
		add("Attempt: "+job.AttemptID, toneNormal)
	}
	add("This view shows UCLI events, not the native client's private reasoning or terminal.", toneNormal)
	rows := max(1, height-2)
	start := min(max(0, m.jobScroll), max(0, len(content)-rows))
	frame.Lines = append(frame.Lines, content[start:min(len(content), start+rows)]...)
	for len(frame.Lines) < height-1 {
		frame.Lines = append(frame.Lines, terminalLine{})
	}
	frame.Lines = append(frame.Lines, terminalLine{Text: "Esc back · Up/Down scroll · Left/Right jobs · F2 mouse", Dim: true, Action: "job:close"})
	return frame
}

func (o *output) job(msg terminalJobMsg) error {
	if o.terminal == nil || o.json {
		return nil
	}
	return o.write(terminalJobWriter{o.terminal, msg}, "")
}

type terminalJobWriter struct {
	terminal *terminalUI
	message  terminalJobMsg
}

func (w terminalJobWriter) Write(data []byte) (int, error) {
	select {
	case <-w.terminal.done:
		return 0, io.ErrClosedPipe
	default:
	}
	w.terminal.program.Send(w.message)
	return len(data), nil
}

func (o *output) jobEvent(event core.Event, model string) error {
	msg := terminalJobMsg{TaskID: event.TaskID, AttemptID: event.AttemptID, Model: model, At: event.At}
	switch event.Type {
	case "accepted":
		msg.Phase = jobAccepted
	case "status":
		// Only our typed execution hook proves that the owned child started.
		if event.Text == core.StatusClientStarted {
			msg.Phase = jobClientRunning
		}
		msg.Detail = event.Text
	case "warning", "error", "input_required", "tool_activity":
		msg.Detail = event.Text
	case "result_persisted":
		msg.Phase, msg.State = jobSaved, event.State
	default:
		return nil // Internal native context/identity is never diagnostic prose.
	}
	return o.job(msg)
}

func (m *terminalModel) moveJob(direction int) {
	for i := range m.jobs.items {
		if m.jobs.items[i].TaskID == m.jobs.focus {
			next := (i + direction + len(m.jobs.items)) % len(m.jobs.items)
			m.jobs.focus, m.jobScroll = m.jobs.items[next].TaskID, 0
			return
		}
	}
}
