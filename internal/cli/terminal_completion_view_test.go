package cli

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCompletionPopupPreservesShellGeometryAndReachability(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 20}} {
		width, height := size[0], size[1]
		u, p := shellHarness(t, width, height, false)
		_ = u.notice(terminalNotice{Text: "Draft is kept", Kind: toneInfo})
		frame := terminalFrame{Lines: []terminalLine{{Text: "> /"}, {Text: completionHint(width, false)}}, CursorRow: 0, CursorCol: 3}
		_ = u.paintComposer(frame)
		baseline := strings.Split(p.model.View().Content, "\n")
		var completion terminalCompletion
		completion.sync("/", 1, false)
		for range len(slashCommands) {
			frame.Completion, frame.CompletionQuery = completion.result(), "/"
			_ = u.paintComposer(frame)
			view := p.model.View()
			rows := strings.Split(view.Content, "\n")
			if len(rows) != height || view.Cursor == nil || view.Cursor.Y != height-4 || view.Cursor.X != 3 || strings.Contains(view.Content, "\x1b[") {
				t.Fatalf("%dx%d lost input geometry or no-color mode: %#v", width, height, view)
			}
			for _, index := range []int{0, 1, 2, height - 6, height - 5, height - 4, height - 3, height - 2, height - 1} {
				if rows[index] != baseline[index] {
					t.Fatalf("completion displaced fixed row %d: %q != %q", index, rows[index], baseline[index])
				}
			}
			selected := completion.result().Rows[completion.result().Focus].Name
			if !strings.Contains(strings.Join(rows[3:height-6], "\n"), "> "+selected+" ") {
				t.Fatalf("selected command %s is outside the bounded viewport", selected)
			}
			completion.move(1)
		}
		// Paging belongs to command selection while completion is active.
		p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
		if event, err := u.next(t.Context()); err != nil || event.kind != keyPageDown || p.model.shell.paused {
			t.Fatalf("popup paging reached transcript: %+v %v", event, err)
		}
	}
}

func TestCompletionRenderingBoundsAndEmptyRecovery(t *testing.T) {
	var completion terminalCompletion
	completion.sync("/", 1, false)
	for _, width := range []int{4, 12, 40, 80} {
		for rows := 0; rows <= 24; rows++ {
			lines := completionLines(completion.result(), "/", width, rows)
			if len(lines) > min(8, rows) {
				t.Fatalf("popup exceeded %d rows: %+v", rows, lines)
			}
			if completionCanAccept(completion.result(), width, rows) {
				focused := false
				for _, line := range lines {
					focused = focused || line.Focused && strings.Contains(line.Text, "/send")
				}
				if !focused {
					t.Fatal("an invisible command can be accepted")
				}
			}
		}
	}
	completion.sync("/zz", 3, false)
	lines := completionLines(completion.result(), "/zz", 80, 8)
	if len(lines) != 2 || !strings.Contains(lines[0].Text, "No commands match /zz") || !strings.Contains(lines[1].Text, "Esc close") || completionCanAccept(completion.result(), 80, 8) {
		t.Fatalf("no-match state advertises an action: %+v", lines)
	}
}
