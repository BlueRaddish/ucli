package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"ucli.local/ucli/internal/core"
)

type pickerTestTerminal struct {
	events            []keyEvent
	frames            []terminalFrame
	readErr, paintErr error
	width, height     int
}

func (t *pickerTestTerminal) size() (int, int) {
	if t.width == 0 || t.height == 0 {
		return 80, 24
	}
	return t.width, t.height
}

func (t *pickerTestTerminal) next(ctx context.Context) (keyEvent, error) {
	if err := ctx.Err(); err != nil {
		return keyEvent{}, err
	}
	if len(t.events) == 0 {
		if t.readErr != nil {
			return keyEvent{}, t.readErr
		}
		return keyEvent{}, io.EOF
	}
	event := t.events[0]
	t.events = t.events[1:]
	return event, nil
}
func (t *pickerTestTerminal) paint(frame terminalFrame) error {
	t.frames = append(t.frames, frame)
	return t.paintErr
}
func terminalPickerText(frame terminalFrame) string {
	var lines []string
	for _, line := range frame.Lines {
		lines = append(lines, line.Text)
	}
	return strings.Join(lines, "\n")
}

func TestTerminalPickerFocusIsPreviewUntilExplicitConfirmation(t *testing.T) {
	rows := []choice{{"auto", "auto - Choose eligible routes"}, {"demo/model", "demo/model - Model | demo | ready"}, {"future/model", "future/model - Future model | not admitted"}}
	terminal := &pickerTestTerminal{events: []keyEvent{{kind: keyDown}, {kind: keyEnter}}}
	value, err := pickTerminal(context.Background(), terminal, "Model for next submission", rows, "demo/model")
	if err != nil || value != "future/model" || len(terminal.frames) != 2 {
		t.Fatalf("confirmation = %q %v, frames %d", value, err, len(terminal.frames))
	}
	initial, preview := terminalPickerText(terminal.frames[0]), terminalPickerText(terminal.frames[1])
	for _, want := range []string{"Current: demo/model", "> demo/model (current)", "Model | demo | ready", "Enter select", "Esc cancel"} {
		if !strings.Contains(initial, want) {
			t.Fatalf("initial picker missing %q: %s", want, initial)
		}
	}
	for _, want := range []string{"Current: demo/model", "  demo/model (current)", "> future/model", "Future model | not admitted"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q: %s", want, preview)
		}
	}
	focused := 0
	for _, line := range terminal.frames[1].Lines {
		if line.Focused {
			focused++
			if line.Text != "> future/model" {
				t.Fatal("highlight does not identify candidate")
			}
		}
	}
	if focused != 1 || terminal.frames[0].CursorRow != 2 || terminal.frames[0].CursorCol != 8 {
		t.Fatal("focus or search caret unavailable")
	}
	terminal = &pickerTestTerminal{events: []keyEvent{{kind: keyDown}, {kind: keyText, text: "future"}, {kind: keyEscape}}}
	value, err = pickTerminal(context.Background(), terminal, "Model", rows, "demo/model")
	if value != "" || err != nil || rows[1].value != "demo/model" {
		t.Fatalf("cancel returned a selection: %q %v", value, err)
	}
}

func TestTerminalPickerPasteIsLiteralAndUnicodeEditingKeepsCaret(t *testing.T) {
	terminal := &pickerTestTerminal{events: []keyEvent{{kind: keyPaste, text: "0\r\n/older\n/send"}, {kind: keyEnter}}}
	value, err := pickTerminal(context.Background(), terminal, "Model", []choice{{"literal", "0 /older /send"}, {"other", "Other"}}, "other")
	if err != nil || value != "literal" || !strings.Contains(terminalPickerText(terminal.frames[1]), "Search: 0 /older /send") {
		t.Fatalf("paste executed a command or lost literal search: %q %v", value, err)
	}
	terminal = &pickerTestTerminal{events: []keyEvent{
		{kind: keyPaste, text: "한👩‍💻e\u0301"}, {kind: keyLeft}, {kind: keyBackspace},
		{kind: keyHome}, {kind: keyDelete}, {kind: keyEnd}, {kind: keyText, text: "x"}, {kind: keyEnter},
	}}
	value, err = pickTerminal(context.Background(), terminal, "Model", []choice{{"unicode", "e\u0301x"}}, "unicode")
	if err != nil || value != "unicode" {
		t.Fatalf("grapheme editing failed: %q %v", value, err)
	}
	// The shared safe renderer spells ZWJ as \\u200d; caret cells match that
	// displayed form while editing still deletes the whole original grapheme.
	if terminal.frames[1].CursorCol != 21 || terminal.frames[2].CursorCol != 20 || terminal.frames[3].CursorCol != 10 || terminal.frames[4].CursorCol != 8 {
		t.Fatalf("caret split a grapheme or counted bytes: %d, %d, %d, %d", terminal.frames[1].CursorCol, terminal.frames[2].CursorCol, terminal.frames[3].CursorCol, terminal.frames[4].CursorCol)
	}
	if !strings.Contains(terminalPickerText(terminal.frames[len(terminal.frames)-1]), "Search: e\u0301x") {
		t.Fatal("Home/End edited list focus instead of the query caret")
	}
}

func TestTerminalPickerNoMatchesAndResizeRemainRecoverable(t *testing.T) {
	terminal := &pickerTestTerminal{events: []keyEvent{
		{kind: keyText, text: "no-match"}, {kind: keyEnter}, {kind: keyResize}, {kind: keyTab},
		{kind: keyClear}, {kind: keyUp}, {kind: keyEnter},
	}}
	value, err := pickTerminal(context.Background(), terminal, "Effort", []choice{{"auto", "Auto"}, {"high", "High"}}, "auto")
	if err != nil || value != "high" || len(terminal.frames) != 7 {
		t.Fatalf("empty result recovery = %q %v, frames %d", value, err, len(terminal.frames))
	}
	for _, index := range []int{1, 2, 3, 4} {
		if !strings.Contains(terminalPickerText(terminal.frames[index]), "No matches. Edit the search") {
			t.Fatal("Enter/resize/tab accepted stale focus")
		}
	}
}

func TestTerminalPickerPropagatesClosedAndInvalidInputWithoutSelection(t *testing.T) {
	sentinel := errors.New("terminal unavailable")
	for _, tc := range []struct {
		name     string
		terminal pickerTestTerminal
		want     error
	}{
		{"EOF event", pickerTestTerminal{events: []keyEvent{{kind: keyEOF}}}, io.EOF},
		{"interruption", pickerTestTerminal{events: []keyEvent{{kind: keyInterrupt}}}, context.Canceled},
		{"read failure", pickerTestTerminal{readErr: sentinel}, sentinel},
		{"paint failure", pickerTestTerminal{paintErr: sentinel}, sentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := pickTerminal(context.Background(), &tc.terminal, "Model", []choice{{"auto", "Auto"}}, "auto")
			if value != "" || !errors.Is(err, tc.want) {
				t.Fatalf("closed input selected %q or hid error: %v", value, err)
			}
		})
	}
	for _, text := range []string{"invalid\x00query", string([]byte{0xff}), strings.Repeat("x", core.MaxInputBytes+1)} {
		terminal := &pickerTestTerminal{events: []keyEvent{{kind: keyPaste, text: text}, {kind: keyEnter}}}
		value, err := pickTerminal(context.Background(), terminal, "Model", nil, "auto")
		var problem *core.Error
		if value != "" || !errors.As(err, &problem) || problem.Code != "invalid_input" {
			t.Fatalf("invalid search accepted %q: %v", value, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	terminal := &pickerTestTerminal{events: []keyEvent{{kind: keyEnter}}}
	value, err := pickTerminal(ctx, terminal, "Model", []choice{{"auto", "Auto"}}, "auto")
	if value != "" || !errors.Is(err, context.Canceled) || len(terminal.frames) != 0 {
		t.Fatal("canceled picker painted or confirmed a selection")
	}
}

func TestTerminalPickerSmallViewportKeepsFocusSearchAndExitVisible(t *testing.T) {
	rows := []choice{{"one", "One - First detail"}, {"two", "Two - Second detail"}, {"three", "Three - Third detail"}}
	for _, height := range []int{4, 5, 6, 7, 8, 12} {
		terminal := &pickerTestTerminal{width: 40, height: height, events: []keyEvent{{kind: keyResize}, {kind: keyEscape}}}
		value, err := pickTerminal(context.Background(), terminal, "Model", rows, "three")
		if err != nil || value != "" {
			t.Fatalf("height %d cancel = %q %v", height, value, err)
		}
		for _, frame := range terminal.frames {
			text := terminalPickerText(frame)
			if len(frame.Lines) > height-1 || frame.CursorRow >= len(frame.Lines) {
				t.Fatalf("height %d overflows visible budget: %+v", height, frame)
			}
			for _, want := range []string{"Search: ", "> Three (current)", "Enter select | Esc cancel"} {
				if !strings.Contains(text, want) {
					t.Fatalf("height %d hides %q: %s", height, want, text)
				}
			}
		}
	}
}

func TestTerminalPickerLongQueryPansWithoutChangingItsValue(t *testing.T) {
	query := strings.Repeat("한", 30) + "tail"
	terminal := &pickerTestTerminal{width: 30, height: 6, events: []keyEvent{
		{kind: keyPaste, text: query}, {kind: keyHome}, {kind: keyEnd}, {kind: keyEnter},
	}}
	value, err := pickTerminal(context.Background(), terminal, "Model", []choice{{"long", "Long"}, {"match", query}}, "long")
	if value != "match" || err != nil {
		t.Fatalf("panning changed query or selected stale current: %q %v", value, err)
	}
	for _, index := range []int{1, 3} {
		frame := terminal.frames[index]
		text := frame.Lines[frame.CursorRow].Text
		if !strings.HasPrefix(text, "Search: ‹") || !strings.HasSuffix(text, "tail") || frame.CursorCol >= 29 {
			t.Fatalf("end caret/query not panned together: %+v", frame)
		}
	}
	if frame := terminal.frames[2]; !strings.HasPrefix(frame.Lines[frame.CursorRow].Text, "Search: 한") || frame.CursorCol != 8 {
		t.Fatal("Home failed to reveal the original query start")
	}
}

type resizingPickerTerminal struct{ pickerTestTerminal }

func (t *resizingPickerTerminal) next(ctx context.Context) (keyEvent, error) {
	event, err := t.pickerTestTerminal.next(ctx)
	if event.kind == keyResize {
		t.width, t.height = 40, 8
	}
	return event, err
}

func TestTerminalPickerCannotConfirmAnInvisibleOption(t *testing.T) {
	for _, size := range [][2]int{{80, 3}, {15, 12}} {
		terminal := &resizingPickerTerminal{pickerTestTerminal{width: size[0], height: size[1], events: []keyEvent{{kind: keyEnter}, {kind: keyResize}, {kind: keyEnter}}}}
		value, err := pickTerminal(context.Background(), terminal, "Model", []choice{{"a", "Alpha"}}, "a")
		if err != nil || value != "a" || len(terminal.frames) != 3 {
			t.Fatalf("size %v confirmed hidden choice: %q %v", size, value, err)
		}
		for _, index := range []int{0, 1} {
			text := terminalPickerText(terminal.frames[index])
			if !strings.Contains(text, "Resize") || !strings.Contains(text, "Esc") || strings.Contains(text, "> Alpha") {
				t.Fatalf("size guard failed: %s", text)
			}
		}
		if !strings.Contains(terminalPickerText(terminal.frames[2]), "> Alpha") {
			t.Fatal("resize did not restore focused option")
		}
	}
}
