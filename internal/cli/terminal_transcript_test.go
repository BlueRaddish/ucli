package cli

import (
	"context"
	"image/color"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"ucli.local/ucli/internal/core"
)

func TestTranscriptConsumesTypedPromptsAndStreamingWithoutInferringRoles(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	o := newOutput(u.writer, u.writer, false, func() {})
	o.terminal = u
	_ = o.text("Your prompt\nAssistant · fake heading\n")
	_ = o.prompt("Explain this:\n  You\n  Assistant")
	_ = o.beginReply("demo/fast")
	chunks := []string{"A reply.\n\x60\x60\x60go\n", "  fmt.", "Println(\"日本語\")\n", "\x60\x60\x60\nDone."}
	for _, chunk := range chunks {
		if err := o.event(core.Event{Type: "message_delta", Text: chunk}); err != nil {
			t.Fatal(err)
		}
	}
	_ = o.progress("Actual provider status\n")
	if err := o.finish(); err != nil {
		t.Fatal(err)
	}
	records := p.model.shell.transcript
	if len(records) != 4 || records[0].Role != transcriptSystem || records[1].Role != transcriptSystem || records[2].Role != transcriptPrompt || records[3].Role != transcriptAssistant {
		t.Fatalf("formatted strings changed roles or deltas split the reply: %#v", records)
	}
	if records[3].Text != strings.Join(chunks, "") || records[3].Label != "Assistant · demo/fast" {
		t.Fatalf("stream added text, lost code indentation, or repeated headings: %#v", records[3])
	}
	view := p.model.View().Content
	if !strings.Contains(view, "  Your prompt") || !strings.Contains(view, "  fmt.Println") || strings.Contains(view, "\x1b") || strings.Count(view, "Actual provider status") != 1 {
		t.Fatalf("role/code/notice presentation regressed: %q", view)
	}
}

func transcriptFixture(t *testing.T) (*terminalUI, *shellTestProgram) {
	t.Helper()
	u, p := shellHarness(t, 60, 20, false)
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> untouched draft"}, {Text: "Tab actions"}}, CursorRow: 0, CursorCol: 8})
	p.Send(terminalMessageMsg{Role: transcriptPrompt, Start: true, Text: "OLDER PROMPT"})
	p.Send(terminalMessageMsg{Role: transcriptAssistant, Start: true, Text: "Earlier reply"})
	p.Send(terminalMessageMsg{Role: transcriptPrompt, Start: true, Text: "LATEST PROMPT 日本語"})
	p.Send(terminalMessageMsg{Role: transcriptAssistant, Start: true, Text: strings.Repeat("A paragraph with preserved indentation.\n", 35)})
	return u, p
}

func TestTranscriptLatestPromptAndOutputAreSeparateStableTargets(t *testing.T) {
	u, p := transcriptFixture(t)
	before := p.model.View()
	if !strings.Contains(before.Content, "Latest prompt · Ctrl+Up · LATEST PROMPT") {
		t.Fatalf("offscreen latest prompt has no jump affordance: %q", before.Content)
	}
	p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp, Mod: tea.ModCtrl}))
	jump := p.model.View()
	if !strings.Contains(jump.Content, "> LATEST PROMPT 日本語") || strings.Contains(jump.Content, "> OLDER PROMPT") || strings.Contains(jump.Content, "Latest prompt · Ctrl+Up") || !strings.Contains(jump.Content, "Following paused") {
		t.Fatalf("jump selected the wrong prompt or left its heading hidden: %q", jump.Content)
	}
	if jump.Cursor == nil || *jump.Cursor != *before.Cursor || len(u.events) != 0 || p.model.shell.composer.Lines[0].Text != "> untouched draft" {
		t.Fatal("transcript navigation changed editor state or leaked a key")
	}
	anchor := p.model.shell.anchor
	p.Send(terminalMessageMsg{Role: transcriptAssistant, Text: "NEWEST OUTPUT"})
	if p.model.shell.anchor != anchor || strings.Contains(p.model.View().Content, "NEWEST OUTPUT") {
		t.Fatal("new output moved the reader away from the prompt")
	}
	p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd, Mod: tea.ModCtrl}))
	if p.model.shell.paused || !strings.Contains(p.model.View().Content, "NEWEST OUTPUT") {
		t.Fatal("Ctrl+End failed to resume following")
	}
	_ = u.paint(terminalFrame{Lines: []terminalLine{{Text: "Choose a model"}}, CursorRow: -1})
	p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp, Mod: tea.ModCtrl}))
	if event, err := u.next(context.Background()); err != nil || event.kind != keyLatestPrompt || p.model.shell.paused {
		t.Fatal("transcript navigation stole an overlay key")
	}
}

func TestTranscriptReaderAnchorSurvivesStreamingResizeAndEviction(t *testing.T) {
	u, p := transcriptFixture(t)
	p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	anchor := p.model.shell.anchor
	for range 10 {
		p.Send(terminalMessageMsg{Role: transcriptAssistant, Text: "A further streamed line.\n"})
	}
	p.Send(tea.WindowSizeMsg{Width: 33, Height: 16})
	w := p.model.shell.transcriptWindow(p.model.fragment, 33, 7)
	if p.model.shell.anchor != anchor || w.rows[w.start].anchor.ID != anchor.ID || w.rows[w.start].anchor.Offset > anchor.Offset {
		t.Fatal("resize/streaming replaced the original byte anchor with a tail offset")
	}
	p.Send(terminalMessageMsg{Role: transcriptAssistant, Text: strings.Repeat("日本語", 20000)})
	if p.model.shell.bytes > shellTranscriptBytes || !p.model.shell.dropped {
		t.Fatal("the display cache exceeded its byte bound")
	}
	if _, found := p.model.shell.latestPrompt(); found {
		t.Fatal("evicted prompt remained jumpable")
	}
	p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp, Mod: tea.ModCtrl}))
	if !strings.Contains(p.model.View().Content, "Earlier display omitted") || strings.Contains(p.model.View().Content, "Latest prompt ·") {
		t.Fatal("missing history was presented as a valid navigation target")
	}
	if !utf8.ValidString(p.model.shell.transcriptText()) || len(u.events) != 1 { // resize only
		t.Fatal("eviction broke Unicode or navigation leaked input")
	}
}

func TestTranscriptWrappedCacheBoundsNewlineAndNarrowOutput(t *testing.T) {
	_, p := transcriptFixture(t)
	p.Send(terminalMessageMsg{Role: transcriptAssistant, Text: strings.Repeat("\n", shellTranscriptBytes-100)})
	for _, width := range []int{1, 3, 60} {
		rows := p.model.shell.transcriptRows("", width)
		if len(rows) > transcriptDisplayRows || !strings.Contains(rows[0].Text, "Earlier display omitted") {
			t.Fatalf("width %d created unbounded or undisclosed wrapped rows: %d", width, len(rows))
		}
		if prompt, ok := p.model.shell.latestPrompt(); ok && promptRowIndex(rows, prompt.ID) != -1 {
			t.Fatal("a pruned wrapped prompt retained a false jump target")
		}
	}
}

func TestTranscriptPromptBandUsesPairedColorsAndMonochromeStructure(t *testing.T) {
	_, p := shellHarness(t, 60, 20, true)
	p.Send(terminalMessageMsg{Role: transcriptPrompt, Start: true, Text: "A real prompt\n  code indentation"})
	for _, theme := range []struct {
		background color.Color
		paired     string
	}{
		{color.Black, "38;2;230;237;243;48;2;35;47;61"},
		{color.White, "38;2;31;41;55;48;2;229;237;245"},
		{nil, "7"},
	} {
		p.model.theme = themeUnknown
		if theme.background != nil {
			p.Send(tea.BackgroundColorMsg{Color: theme.background})
		}
		view := p.model.View().Content
		if !strings.Contains(view, "\x1b["+theme.paired+"m  Your prompt") || !strings.Contains(view, "\x1b["+theme.paired+"m> A real prompt") {
			t.Fatal("prompt band lost the exact foreground/background pair or structural caption")
		}
	}
	p.model.color = false
	for _, size := range [][2]int{{100, 30}, {60, 20}, {18, 8}, {3, 2}, {1, 1}} {
		p.Send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := p.model.View().Content
		lines := strings.Split(view, "\n")
		if len(lines) != size[1] || strings.Contains(view, "\x1b") {
			t.Fatalf("NO_COLOR/narrow shell changed geometry: %v %q", size, view)
		}
		for _, line := range lines {
			if cellWidth(line) > max(1, size[0]-1) {
				t.Fatalf("transcript wrapped outside the viewport at %v: %q", size, line)
			}
		}
	}
}

func TestTranscriptPromptArrowMarksOnlyFirstSourceRow(t *testing.T) {
	record := transcriptRecord{ID: 42, Role: transcriptPrompt, Text: "Long first prompt line\n  code\n| literal content"}
	for _, width := range []int{12, 80} {
		var builder transcriptRowsBuilder
		appendRecordRows(record, width, &builder)
		rows := builder.finish()
		if rows[0].Text != "  Your prompt" || rows[0].anchor != (transcriptAnchor{42, -1}) {
			t.Fatalf("caption or jump anchor changed: %+v", rows[0])
		}
		arrows := 0
		for i, row := range rows {
			if !row.Band {
				t.Fatal("prompt continuation lost its highlight")
			}
			if i == 0 {
				continue
			}
			prefix := "  "
			if i == 1 {
				prefix = "> "
			}
			if !strings.HasPrefix(row.Text, prefix) {
				t.Fatalf("repeated gutter/marker at width %d: %q", width, row.Text)
			}
			if strings.HasPrefix(row.Text, "> ") {
				arrows++
			}
			if row.anchor.ID != record.ID || row.anchor.Offset < 0 {
				t.Fatal("marker changed source identity")
			}
		}
		if arrows != 1 || rows[1].anchor.Offset != 0 {
			t.Fatal("one submitted prompt needs one source-start arrow")
		}
		if width == 80 && (rows[2].Text != "    code" || rows[3].Text != "  | literal content") {
			t.Fatal("continuation indentation or literal input changed")
		}
	}
	// A source tail retained after pruning must not acquire a fresh-start arrow.
	var builder transcriptRowsBuilder
	appendRecordRows(transcriptRecord{ID: 42, Role: transcriptPrompt, Base: 20, Text: "tail"}, 80, &builder)
	if row := builder.finish()[1]; row.Text != "  tail" || row.anchor.Offset != 20 {
		t.Fatal("pruned source appeared as a new prompt")
	}
}

func TestTranscriptExitKeepsBoundedDiagnosticTail(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	p.Send(terminalMessageMsg{Role: transcriptAssistant, Start: true, Text: strings.Repeat("Saved body\n", 1000) + "Final reply line"})
	_ = u.notice(terminalNotice{Text: "Result saved", Kind: toneSuccess})
	p.Send(terminalExitMsg{})
	if strings.Count(u.finalText, "\n") > 3 || !strings.Contains(u.finalText, "Final reply line") || !strings.Contains(u.finalText, "Result saved") {
		t.Fatalf("exit dumped the whole message or lost its diagnostic: %q", u.finalText)
	}
}

func TestTranscriptExplicitNewPromptResumesFollowing(t *testing.T) {
	_, p := transcriptFixture(t)
	p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp, Mod: tea.ModCtrl}))
	p.Send(terminalMessageMsg{Role: transcriptPrompt, Start: true, Text: "A new explicit submission"})
	if p.model.shell.paused || !strings.Contains(p.model.View().Content, "> A new explicit submission") {
		t.Fatal("a new submission stayed hidden behind an old reading anchor")
	}
}

func TestTranscriptPartialEvictionPreservesOnlyRetainedSourceAnchors(t *testing.T) {
	_, p := shellHarness(t, 2000, 30, false)
	p.Send(terminalMessageMsg{Role: transcriptAssistant, Start: true, Text: strings.Repeat("x", shellTranscriptBytes)})
	id := p.model.shell.replyID
	p.model.shell.paused = true
	p.model.shell.anchor = transcriptAnchor{id, 5000}
	p.Send(terminalMessageMsg{Role: transcriptAssistant, Text: strings.Repeat("y", 512)})
	w := p.model.shell.transcriptWindow("", 2000, 20)
	if w.rows[w.start].anchor.ID != id || w.rows[w.start].anchor.Offset > 5000 || w.rows[w.start].anchor.Offset < 512 {
		t.Fatal("retained source anchor was lost during partial record eviction")
	}
	p.model.shell.anchor = transcriptAnchor{id, 20}
	w = p.model.shell.transcriptWindow("", 2000, 20)
	if w.start != 0 || !strings.Contains(w.rows[w.start].Text, "Earlier display omitted") {
		t.Fatal("a pruned byte anchor silently landed after its omission notice")
	}
}
