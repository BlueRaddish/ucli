package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ucli.local/ucli/internal/core"
)

func TestScopedOutputInterleavesAttemptsWithoutChangingForegroundReply(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	o := newOutput(u.writer, u.writer, false, func() {})
	o.terminal = u
	send := func(task, attempt, text string) {
		t.Helper()
		if err := o.attemptEvent(core.Event{Type: "message_delta", TaskID: task, AttemptID: attempt, Text: text}, "demo/fast"); err != nil {
			t.Fatal(err)
		}
	}
	_ = o.prompt("First prompt")
	send("task_aaaaaaaa", "attempt_a", "A1 ")
	send("task_bbbbbbbb", "attempt_b", "B1 ")
	_ = o.prompt("New prompt while A still runs")
	_ = o.beginReply("saved/result")
	_ = o.assistant("Foreground ")
	send("task_aaaaaaaa", "attempt_a", "A2")
	send("task_bbbbbbbb", "attempt_b", "B2")
	send("task_aaaaaaaa", "attempt_c", "New attempt on A")
	send("task_bbbbbbbb", "attempt_a", "Different task, same attempt spelling")
	_ = o.assistant("tail")
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	records := p.model.shell.transcript
	if len(records) != 7 || records[1].Text != "A1 A2" || records[2].Text != "B1 B2" || records[4].Text != "Foreground tail" || records[5].Text != "New attempt on A" || records[6].Text != "Different task, same attempt spelling" {
		t.Fatalf("interleaved output changed ownership: %#v", records)
	}
	if records[1].TaskID != "task_aaaaaaaa" || records[1].AttemptID != "attempt_a" || records[2].TaskID != "task_bbbbbbbb" || records[4].TaskID != "" || records[4].AttemptID != "" {
		t.Fatalf("scope was lost or applied to legacy output: %#v", records)
	}
	if records[1].Label == records[2].Label || !strings.Contains(records[1].Label, "demo/fast") || !strings.Contains(records[1].Label, "aaaaaaaa") || !strings.Contains(records[2].Label, "bbbbbbbb") || records[4].Label != "Assistant · saved/result" {
		t.Fatalf("same-model tasks or the legacy reply lost their labels: %#v", records)
	}
}

func TestScopedOutputKeepsTaskIdentityBeforeLongModelAtNarrowWidths(t *testing.T) {
	for _, width := range []int{60, 80} {
		u, p := shellHarness(t, width, 24, false)
		o := newOutput(u.writer, u.writer, false, func() {})
		o.terminal = u
		id := "task_0123456789abcdef0123456789abcdef"
		if err := o.attemptEvent(core.Event{Type: "message_delta", TaskID: id, AttemptID: "attempt_one", Text: "Saved output"}, strings.Repeat("long-model-name", 12)); err != nil {
			t.Fatal(err)
		}
		if err := o.finish(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(p.model.View().Content, "task "+shortExecutionID(id)) {
			t.Fatalf("%d columns hid the task behind the long model label", width)
		}
	}
}

func TestScopedOutputPreservesPlainJSONAndOtherEvents(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonMode), func(t *testing.T) {
			var actual, expected bytes.Buffer
			scoped := newOutput(&actual, &actual, jsonMode, func() {})
			legacy := newOutput(&expected, &expected, jsonMode, func() {})
			for _, event := range []core.Event{
				{Type: "accepted", TaskID: "task_a", AttemptID: "attempt_a"},
				{Type: "message_delta", TaskID: "task_a", AttemptID: "attempt_a", Text: "literal\n  code\x1b[2J"},
				{Type: "status", Text: "status"},
				{Type: "warning", Text: "warning"},
				{Type: "result_persisted", State: "complete"},
			} {
				if err := scoped.attemptEvent(event, "demo/fast"); err != nil {
					t.Fatal(err)
				}
				if err := legacy.event(event); err != nil {
					t.Fatal(err)
				}
			}
			if err := scoped.finish(); err != nil {
				t.Fatal(err)
			}
			if err := legacy.finish(); err != nil {
				t.Fatal(err)
			}
			if actual.String() != expected.String() {
				t.Fatalf("non-rich output changed: %q != %q", actual.String(), expected.String())
			}
			if jsonMode {
				var event core.Event
				if err := json.Unmarshal(bytes.Split(actual.Bytes(), []byte("\n"))[1], &event); err != nil || event.TaskID != "task_a" || event.AttemptID != "attempt_a" || event.Text != "literal\n  code\x1b[2J" {
					t.Fatalf("JSON event changed: %#v, %v", event, err)
				}
			}
		})
	}
	u, p := shellHarness(t, 80, 24, false)
	o := newOutput(u.writer, u.writer, false, func() {})
	o.terminal = u
	if err := o.attemptEvent(core.Event{Type: "status", Text: "Scoped status still uses notice"}, "demo/fast"); err != nil {
		t.Fatal(err)
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.model.View().Content, "Scoped status still uses notice") {
		t.Fatal("other rich events no longer use the existing notice path")
	}
}

func TestScopedOutputRejectsPartialOrOversizedIdentityWithoutLegacyFallback(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	o := newOutput(u.writer, u.writer, false, func() {})
	o.terminal = u
	_ = o.beginReply("legacy")
	_ = o.assistant("unchanged")
	for _, pair := range [][2]string{{"", "attempt_a"}, {"task_a", ""}, {strings.Repeat("x", 257), "attempt_a"}, {"task_a", "bad\nID"}, {"task_a", string([]byte{0xff})}} {
		if err := o.attemptEvent(core.Event{Type: "message_delta", TaskID: pair[0], AttemptID: pair[1], Text: "WRONG"}, "demo/fast"); err == nil {
			t.Fatalf("invalid scope was accepted: %q", pair)
		}
	}
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	// The reducer also refuses malformed typed messages that bypass the writer.
	p.Send(terminalMessageMsg{Role: transcriptAssistant, TaskID: "task_a", Text: "WRONG"})
	if records := p.model.shell.transcript; len(records) != 1 || records[0].Text != "unchanged" {
		t.Fatalf("invalid scope fell through to the foreground reply: %#v", records)
	}
}

func TestScopedTranscriptEvictionAndLateOutputKeepBoundedIdentity(t *testing.T) {
	var shell terminalShell
	message := func(task, attempt, text string) {
		shell.message(terminalMessageMsg{Role: transcriptAssistant, TaskID: task, AttemptID: attempt, Text: text, Label: "Assistant"})
	}
	message("task_first", "attempt_first", "first")
	firstID := shell.transcript[0].ID
	for i := range shellTranscriptLines + 10 {
		message(fmt.Sprintf("task_%d", i), fmt.Sprintf("attempt_%d", i), "x")
	}
	if len(shell.transcript) != shellTranscriptLines || shell.transcript[0].ID == firstID || !shell.dropped || shell.replyID != 0 {
		t.Fatal("scoped records exceeded bounds or changed foreground ownership")
	}
	shell.paused, shell.anchor = true, transcriptAnchor{shell.transcript[0].ID, 0}
	anchor := shell.anchor
	message("task_first", "attempt_first", "late tail")
	last := shell.transcript[len(shell.transcript)-1]
	if last.ID == firstID || last.Text != "late tail" || last.TaskID != "task_first" || !shell.paused || shell.anchor != anchor || len(shell.transcript) > shellTranscriptLines {
		t.Fatal("late output reused an evicted anchor or moved the reader")
	}
	message("task_first", "attempt_first", strings.Repeat("x", shellTranscriptBytes+1))
	if shell.bytes > shellTranscriptBytes || len(shell.transcript) != 1 || shell.transcript[0].Base == 0 || shell.transcript[0].AttemptID != "attempt_first" {
		t.Fatal("scoped long reply escaped the existing text/identity bounds")
	}
}
