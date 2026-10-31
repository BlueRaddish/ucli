package cli

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestReplyCaptionOpensOnlyItsExactRetainedAttempt(t *testing.T) {
	_, program := shellHarness(t, 80, 24, false)
	model := &program.model
	const task, oldAttempt, newAttempt = "task:one", "attempt:old", "attempt:new"
	program.Send(terminalJobMsg{TaskID: task, AttemptID: oldAttempt, Phase: jobAccepted})
	var rows transcriptRowsBuilder
	appendRecordRows(transcriptRecord{ID: 1, Role: transcriptAssistant, TaskID: task, AttemptID: oldAttempt, Text: "Earlier reply"}, 80, &rows)
	caption := rows.finish()[0]
	if caption.Action != jobCaptionAction(task, oldAttempt) {
		t.Fatal("caption lost the displayed reply's exact attempt identity")
	}
	model.activate(caption.Action)
	if !model.jobInspector || model.jobs.focus != task {
		t.Fatal("matching retained reply could not open its job")
	}
	model.jobInspector = false
	program.Send(terminalJobMsg{TaskID: task, Phase: jobPreparing})
	program.Send(terminalJobMsg{TaskID: task, AttemptID: newAttempt, Phase: jobAccepted, Bind: true})
	model.jobScroll = 7
	model.activate(caption.Action)
	if model.jobInspector || model.jobScroll != 7 {
		t.Fatal("an old caption opened the new attempt or changed navigation")
	}
	model.activate(jobCaptionAction(task, newAttempt))
	if !model.jobInspector || model.jobs.focus != task || model.jobScroll != 0 {
		t.Fatal("exact current reply could not open its retained job")
	}
	program.Send(terminalJobMsg{TaskID: "other-task", AttemptID: "other-attempt", Phase: jobAccepted})
	model.activate(jobCaptionAction("other-task", "other-attempt"))
	model.jobScroll = 3
	model.activate(caption.Action)
	model.activate("job:task:" + task) // The old task-only action has no fallback.
	if !model.jobInspector || model.jobs.focus != "other-task" || model.jobScroll != 3 {
		t.Fatal("a stale caption retargeted an already open inspector")
	}
}

func TestJobCaptionActionHasBoundedUnambiguousShape(t *testing.T) {
	for _, pair := range [][2]string{
		{"task", "attempt"},
		{"task:with:colon", "attempt:with:colon"},
		{"task/with space", "attempt\"punctuation"},
		{"task-\u754c", "attempt-\u0628"},
		{strings.Repeat("t", 256), strings.Repeat("a", 256)},
	} {
		action := jobCaptionAction(pair[0], pair[1])
		task, attempt, ok := parseJobCaptionAction(action)
		if !ok || task != pair[0] || attempt != pair[1] {
			t.Fatalf("scope changed on round trip: %q -> %q, %q", action, task, attempt)
		}
	}
	for _, pair := range [][2]string{{"", "a"}, {"t", ""}, {"t\n", "a"}, {"t", "a\x00"}, {strings.Repeat("t", 257), "a"}, {"t", string([]byte{0xff})}} {
		if jobCaptionAction(pair[0], pair[1]) != "" {
			t.Fatal("invalid scope produced a clickable action")
		}
	}
	valid := jobCaptionAction("task", "attempt")
	for _, action := range []string{
		"", "job:task:task", "job:attempt:", "job:attempt:dGFzaw", valid + ":suffix", valid + "=",
		"job:attempt:" + strings.Repeat("A", 343) + ":YQ",
		"job:attempt:dGFzaw:YQ\n", // Go's base64 decoder otherwise ignores newlines.
		"job:attempt:dGFzaw:" + base64.RawURLEncoding.EncodeToString([]byte("bad\x00scope")),
		"job:attempt:dGFzaw:/w", // Non-URL alphabet is refused.
		"job:attempt:dGFzaw:_w", // Decoded invalid UTF-8 is refused.
	} {
		if _, _, ok := parseJobCaptionAction(action); ok {
			t.Fatalf("malformed caption action accepted: %q", action)
		}
	}
}

func TestProviderTextCannotManufactureJobCaptionActions(t *testing.T) {
	for _, role := range []transcriptRole{transcriptSystem, transcriptPrompt, transcriptAssistant} {
		var rows transcriptRowsBuilder
		text := jobCaptionAction("foreign-task", "foreign-attempt")
		appendRecordRows(transcriptRecord{ID: 1, Role: role, Text: text}, 200, &rows)
		for _, row := range rows.finish() {
			if row.Action != "" || len(row.Spans) != 0 {
				t.Fatal("unscoped transcript text manufactured a control")
			}
		}
	}
	var rows transcriptRowsBuilder
	appendRecordRows(transcriptRecord{ID: 1, Role: transcriptAssistant, TaskID: "own-task", AttemptID: "own-attempt", Text: jobCaptionAction("foreign-task", "foreign-attempt")}, 200, &rows)
	for i, row := range rows.finish() {
		if i == 0 {
			if row.Action != jobCaptionAction("own-task", "own-attempt") {
				t.Fatal("trusted caption was replaced by provider prose")
			}
		} else if row.Action != "" || len(row.Spans) != 0 {
			t.Fatal("provider body acquired the caption's navigation authority")
		}
	}
}
