package cli

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTranscriptFormattingGroupsTypedRecordsWithoutGuessingSpeakers(t *testing.T) {
	var shell terminalShell
	shell.append("Welcome to this saved session.")
	shell.append("Browse models without sending anything.")
	shell.message(terminalMessageMsg{Role: transcriptPrompt, Start: true, Text: "Explain the change."})
	shell.message(terminalMessageMsg{Role: transcriptAssistant, Start: true, Text: "UCLI\nThis is provider prose, not a status."})
	shell.append("The result is saved.")
	rows := shell.transcriptRows("", 80)
	var captions []string
	for _, row := range rows {
		if row.anchor.Offset == -1 {
			captions = append(captions, row.Text)
			if !row.Bold {
				t.Fatal("a typed block caption lost its visual emphasis")
			}
		}
	}
	if !reflect.DeepEqual(captions, []string{"UCLI", "  Your prompt", "Assistant", "UCLI"}) {
		t.Fatalf("system lines repeated captions or prose acquired a role: %q", captions)
	}
	if rows[1].Text != "  Welcome to this saved session." || rows[2].Text != "  Browse models without sending anything." || rows[3].Text != "" {
		t.Fatal("adjacent system lines did not form one compact group")
	}
	for index, row := range rows {
		if row.anchor.Offset == -1 && index > 0 && row.Text != "UCLI" && strings.TrimSpace(rows[index-1].Text) != "" {
			t.Fatal("distinct message blocks have no breathing room")
		}
	}
	if len(shell.transcript) != 5 || shell.transcript[3].Text != "UCLI\nThis is provider prose, not a status." || shell.transcript[3].Role != transcriptAssistant {
		t.Fatal("formatting rewrote a typed record or inferred a system event")
	}
}

func TestTranscriptProseWrapKeepsWordsAndExactSource(t *testing.T) {
	const source = "Readable prose keeps whole words together."
	var shell terminalShell
	shell.message(terminalMessageMsg{Role: transcriptAssistant, Start: true, Text: source})
	rows := shell.transcriptRows("", 26)
	want := []string{"Readable prose keeps ", "whole words together."}
	var got []string
	for _, row := range rows[1:] {
		if !strings.HasPrefix(row.Text, "  ") || cellWidth(row.Text) > 25 {
			t.Fatalf("assistant gutter or row width failed: %q", row.Text)
		}
		got = append(got, strings.TrimPrefix(row.Text, "  "))
	}
	if !reflect.DeepEqual(got, want) || strings.Join(got, "") != source || shell.transcript[0].Text != source {
		t.Fatalf("words split or stored source changed: %q", got)
	}
	if rows[1].anchor.Offset != 0 || rows[2].anchor.Offset != len(want[0]) {
		t.Fatal("soft wrapping replaced source byte anchors with display coordinates")
	}
}

func TestTranscriptParagraphsListsAndCodeKeepLiteralContent(t *testing.T) {
	const code = "    do_work(  alpha, beta, gamma  )"
	source := "## Summary\n\n- Keep the readable explanation with its list item.\n\n```go\n" + code + "\n```\n\nEnd."
	var shell terminalShell
	shell.message(terminalMessageMsg{Role: transcriptAssistant, Start: true, Text: source})
	rows := shell.transcriptRows("", 29)
	if rows[1].Text != "  ## Summary" || !rows[1].Bold || rows[2].Text != "  " {
		t.Fatal("literal heading or paragraph gap lost its presentation")
	}
	codeStart := strings.Index(source, code)
	var codeParts []string
	listContinuation := false
	for _, row := range rows {
		if row.anchor.Offset >= codeStart && row.anchor.Offset < codeStart+len(code) {
			codeParts = append(codeParts, strings.TrimPrefix(row.Text, "  "))
			if row.Bold || row.Band {
				t.Fatal("literal code was promoted into a heading or prompt")
			}
		}
		if strings.Contains(row.Text, "explanation") {
			listContinuation = strings.HasPrefix(row.Text, "    ")
		}
	}
	if strings.Join(codeParts, "") != code || !listContinuation || shell.transcript[0].Text != source {
		t.Fatalf("code indentation/source or hanging list presentation changed: code=%q list=%v", codeParts, listContinuation)
	}
	for _, fence := range []string{"  ```go", "  ```"} {
		found := false
		for _, row := range rows {
			found = found || row.Text == fence
		}
		if !found {
			t.Fatalf("literal fence was removed: %q", fence)
		}
	}
}

func TestTranscriptFormattingFragmentationAndScopeRemainStable(t *testing.T) {
	chunks := []string{"## A short", " heading\n\n", "Words with ", "日本語 and e", "\u0301 plus 👩", "\u200d💻.\n```\n", "  literal text\n```"}
	var streamed, whole terminalShell
	for _, chunk := range chunks {
		streamed.message(terminalMessageMsg{Role: transcriptAssistant, TaskID: "task_one", AttemptID: "attempt_one", Text: chunk})
	}
	whole.message(terminalMessageMsg{Role: transcriptAssistant, TaskID: "task_one", AttemptID: "attempt_one", Text: strings.Join(chunks, "")})
	if len(streamed.transcript) != 1 || streamed.transcript[0].Text != whole.transcript[0].Text {
		t.Fatal("fragment arrivals inserted a paragraph or split the assistant record")
	}
	for _, width := range []int{1, 3, 18, 60, 100} {
		a, b := streamed.transcriptRows("", width), whole.transcriptRows("", width)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("streaming arrival changed final formatting at width%d", width)
		}
		for _, row := range a[1:] {
			if !utf8.ValidString(row.Text) || cellWidth(row.Text) > max(1, width-1) {
				t.Fatalf("Unicode/gutter overflow at width%d: %q", width, row.Text)
			}
		}
	}
	streamed.message(terminalMessageMsg{Role: transcriptAssistant, TaskID: "task_two", AttemptID: "attempt_two", Text: "Another worker"})
	streamed.message(terminalMessageMsg{Role: transcriptAssistant, TaskID: "task_one", AttemptID: "attempt_one", Text: "\nStill first worker"})
	if len(streamed.transcript) != 2 || !strings.HasSuffix(streamed.transcript[0].Text, "Still first worker") || streamed.transcript[1].Text != "Another worker" {
		t.Fatal("formatting retargeted a task-scoped reply")
	}
	for _, row := range streamed.transcriptRows("", 60) {
		if row.anchor.Offset == -1 {
			want := jobCaptionAction("task_one", "attempt_one")
			if row.anchor.ID == streamed.transcript[1].ID {
				want = jobCaptionAction("task_two", "attempt_two")
			}
			if row.Action != want {
				t.Fatal("assistant caption lost its trusted job target")
			}
		} else if row.Action != "" {
			t.Fatal("provider prose acquired a job action")
		}
	}
}

func TestTranscriptFormatterKeepsLiteralControlsAndPartialCodeTail(t *testing.T) {
	var shell terminalShell
	shell.message(terminalMessageMsg{Role: transcriptAssistant, Start: true, Text: "\x1b[31mred\r\n\tindent"})
	if shell.transcript[0].Text != "\\u001b[31mred\\u000d\n\tindent" {
		t.Fatal("formatting changed the existing control-character escape contract")
	}
	for _, row := range shell.transcriptRows("", 80) {
		if strings.ContainsRune(row.Text, '\x1b') || strings.ContainsRune(row.Text, '\r') {
			t.Fatal("untrusted terminal control escaped the literal renderer")
		}
	}
	var rows transcriptRowsBuilder
	appendRecordRows(transcriptRecord{ID: 3, Role: transcriptAssistant, Base: 200, Text: "## not a known heading\n- not a known list"}, 16, &rows)
	for _, row := range rows.finish()[1:] {
		if row.Bold || row.anchor.Offset < 200 {
			t.Fatal("a retained middle-of-code tail acquired invented block semantics")
		}
	}
}

func TestTranscriptSpacingAnchorSurvivesNewSourceParagraph(t *testing.T) {
	var shell terminalShell
	shell.message(terminalMessageMsg{Role: transcriptAssistant, TaskID: "task_first", AttemptID: "attempt_first", Text: "First answer"})
	shell.message(terminalMessageMsg{Role: transcriptPrompt, Start: true, Text: "Next prompt"})
	rows := shell.transcriptRows("", 80)
	anchor := transcriptAnchor{ID: shell.transcript[1].ID, Offset: -2}
	if row := rows[anchorIndex(rows, anchor)]; row.anchor != anchor || row.Text != "" {
		t.Fatal("fixture did not select the actual block separator")
	}
	shell.message(terminalMessageMsg{Role: transcriptAssistant, TaskID: "task_first", AttemptID: "attempt_first", Text: "\n"})
	rows = shell.transcriptRows("", 60)
	if row := rows[anchorIndex(rows, anchor)]; row.Text != "  Your prompt" || row.anchor.ID != anchor.ID {
		t.Fatal("a source paragraph replacing display spacing lost the reader's message anchor")
	}
}
