package cli

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

func TestTerminalModelInputAndBounds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := &terminalUI{events: make(chan keyEvent, 128), done: make(chan struct{}), interrupt: cancel}
	m := terminalModel{ui: u, color: true}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 12, Height: 4})
	m = updated.(terminalModel)
	m.frame = terminalFrame{Lines: []terminalLine{{Text: "\x1b[2Junsafe long title", Focused: true}, {Text: "second"}, {Text: "third"}, {Text: "fourth"}}, CursorRow: 1, CursorCol: 80}
	v := m.View()
	if strings.Contains(v.Content, "\x1b[2J") || strings.Contains(v.Content, "fourth") || v.Cursor.X > 10 {
		t.Fatalf("unbounded view: %#v", v)
	}
	_, _ = m.Update(tea.PasteMsg{Content: "/send\n/exit"})
	if event, _ := u.next(ctx); event.kind != keyResize {
		t.Fatal(event)
	}
	if event, _ := u.next(ctx); event.kind != keyPaste || event.text != "/send\n/exit" {
		t.Fatal(event)
	}
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	m = updated.(terminalModel)
	if ctx.Err() != nil {
		t.Fatal("first copy/interrupt gesture canceled the application")
	}
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if ctx.Err() == nil {
		t.Fatal("confirmed interrupt did not reach application")
	}
}

func TestTerminalQueueNeverSilentlyTruncates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := &terminalUI{events: make(chan keyEvent, 1), interrupt: cancel}
	u.enqueue(keyEvent{kind: keyText, text: "first"})
	u.enqueue(keyEvent{kind: keyText, text: "second"})
	if u.failure() == nil || ctx.Err() == nil {
		t.Fatal("input overflow was silent")
	}
}

func TestStreamingRepaintIsBoundedWithoutDamagingUTF8(t *testing.T) {
	text := strings.Repeat("中", 10000)
	fragment := streamFragment(text)
	if len(fragment) > 4100 || !utf8.ValidString(fragment) || !strings.HasSuffix(text, strings.TrimPrefix(fragment, "… ")) {
		t.Fatal("invalid bounded live stream")
	}
	if got := streamFragment("small"); got != "small" {
		t.Fatal(got)
	}
}
