package cli

import (
	"bytes"
	"context"
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// Consume the real terminalUI/writer message path synchronously. Assertions
// inspect resulting screen placement and user key routing, not duplicate layout.
type shellTestProgram struct {
	model terminalModel
	msgs  []tea.Msg
}

func (p *shellTestProgram) Send(msg tea.Msg) {
	p.msgs = append(p.msgs, msg)
	next, _ := p.model.Update(msg)
	p.model = next.(terminalModel)
}
func (p *shellTestProgram) Quit() {}

func TestFixedShellNoticesKeepOneCopyOfLoggedErrors(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	const diagnostic = "Cannot open plan.\nCheck its path."
	_, _ = u.writer.Write([]byte(diagnostic + "\n"))
	_ = u.notice(terminalNotice{Text: diagnostic, Kind: toneError})
	_ = u.notice(terminalNotice{Text: "A later action succeeded", Kind: toneSuccess})
	history := p.model.shell.transcriptText()
	if strings.Count(history, "Cannot open plan.") != 1 || !strings.Contains(history, "Check its path.") {
		t.Fatalf("notice duplicated or lost the logged diagnostic: %q", history)
	}
	_ = u.notice(terminalNotice{Text: "path.", Kind: toneWarning})
	if !strings.Contains(p.model.shell.transcriptText(), "[Warning] path.") {
		t.Fatal("an unrelated suffix was mistaken for an already logged warning")
	}
	_ = u.notice(terminalNotice{Text: "Connection unavailable", Kind: toneWarning})
	if !strings.Contains(p.model.shell.transcriptText(), "[Warning] Connection unavailable") {
		t.Fatal("notice-only warning disappeared from history")
	}
}

func TestStandaloneShellFooterDoesNotImplyAnEditableDraft(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	_ = u.setChrome(terminalChrome{Version: "0.1-test", Location: "/work/ucli", Model: "Workflow plan", Panel: true})
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "Workflow plan · Esc back · Ctrl+C close", Dim: true}}, CursorRow: -1})
	_ = u.paint(terminalFrame{Lines: []terminalLine{{Text: "Plan · not executed"}}, CursorRow: -1})
	view := p.model.View()
	if strings.Contains(view.Content, "Draft kept") || strings.Contains(view.Content, "> ") || !strings.Contains(view.Content, "Workflow plan · Esc back") || view.Cursor != nil {
		t.Fatalf("standalone panel implies an editor: %#v", view)
	}
	if strings.Contains(view.Content, "Model Workflow plan") || !strings.Contains(view.Content, "Location /work/ucli") {
		t.Fatalf("panel title was presented as a selected model: %q", view.Content)
	}
}

func shellHarness(t *testing.T, width, height int, color bool) (*terminalUI, *shellTestProgram) {
	t.Helper()
	u := &terminalUI{events: make(chan keyEvent, 128), done: make(chan struct{}), interrupt: func() {}}
	u.width.Store(int64(width))
	u.height.Store(int64(height))
	p := &shellTestProgram{model: terminalModel{ui: u, color: color, frame: terminalFrame{CursorRow: -1}}}
	u.program, u.writer = p, &terminalWriter{ui: u}
	if err := u.setChrome(terminalChrome{Version: "0.1-test", Location: "/work/ucli", Model: "demo/fast", Context: "read-only"}); err != nil {
		t.Fatal(err)
	}
	return u, p
}

func TestFixedShellConsumesOutputMenusAndFailureWithoutMovingComposer(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	composer := terminalFrame{Lines: []terminalLine{{Text: "> draft stays"}, {Text: "Tab actions · Enter send", Dim: true}}, CursorRow: 0, CursorCol: 13}
	if err := u.paintComposer(composer); err != nil {
		t.Fatal(err)
	}
	initial := p.model.View()
	if !initial.AltScreen || initial.Cursor.Y != 20 {
		t.Fatalf("composer is not bottom anchored: %#v", initial)
	}
	for i := range 100 {
		if _, err := fmt.Fprintf(u.writer, "line %03d\n", i); err != nil {
			t.Fatal(err)
		}
	}
	_ = u.notice(terminalNotice{Text: "Cannot connect; draft is kept.", Kind: toneError})
	view := p.model.View()
	lines := strings.Split(view.Content, "\n")
	if len(lines) != 24 || !strings.Contains(lines[0], "UCLI 0.1-test") || lines[1] != "read-only" || lines[2] != strings.Repeat("─", 79) {
		t.Fatalf("header moved after output: %q", view.Content)
	}
	if !strings.Contains(lines[18], "[Error] Cannot connect") || lines[20] != "> draft stays" || view.Cursor.Y != initial.Cursor.Y {
		t.Fatalf("failure displaced input or notice: %q", view.Content)
	}
	if lines[19] != strings.Repeat("─", 79) || lines[21] != lines[19] || lines[22] != "Model demo/fast · /work/ucli" || !strings.Contains(lines[23], "Tab actions") {
		t.Fatalf("prompt rules, metadata, or hints moved: %q", view.Content)
	}
	_ = u.paint(terminalFrame{Lines: []terminalLine{{Text: "Choose model"}, {Text: "Search: deep"}, {Text: "> demo/deep", Focused: true}}, CursorRow: 1, CursorCol: 12})
	menu := p.model.View()
	if menu.Cursor.Y != 4 || !strings.Contains(menu.Content, "> draft stays") || !strings.Contains(menu.Content, "Draft kept · finish this panel") {
		t.Fatalf("menu lost composition or active cursor: %#v", menu)
	}
	_ = u.paintComposer(composer) // Escape returns to the unchanged editor.
	returned := p.model.View()
	if returned.Cursor.Y != 20 || p.model.shell.composer.Lines[0].Text != composer.Lines[0].Text {
		t.Fatal("return changed draft or cursor")
	}
	for _, msg := range p.msgs {
		if strings.Contains(fmt.Sprintf("%T", msg), "printLineMessage") {
			t.Fatal("fullscreen output escaped into terminal scrollback")
		}
	}
}

func TestFramedComposerMultilineAndLongMetadataPreserveContext(t *testing.T) {
	u, p := shellHarness(t, 60, 20, false)
	_ = u.setChrome(terminalChrome{Version: "test", Location: "/a/very/long/日本語/parent/directory/ucli", Model: strings.Repeat("provider/", 8) + "fast", Context: "sandbox | effort auto"})
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> first"}, {Text: "  second"}, {Text: "  third"}, {Text: "Tab actions"}}, CursorRow: 2, CursorCol: 7})
	v := p.model.View()
	lines := strings.Split(v.Content, "\n")
	if len(lines) != 20 || v.Cursor == nil || v.Cursor.Y != 16 || lines[14] != "> first" || lines[16] != "  third" || lines[17] != strings.Repeat("─", 59) || !strings.HasSuffix(lines[18], "/directory/ucli") || lines[19] != "Tab actions" {
		t.Fatalf("multiline composer displaced footer or lost path suffix: %q cursor=%#v", v.Content, v.Cursor)
	}
	if strings.Contains(strings.Join(lines[:3], "\n"), "provider/") || strings.Contains(strings.Join(lines[:3], "\n"), "/directory/ucli") {
		t.Fatal("model/path metadata leaked back into the header")
	}
	for _, line := range lines {
		if cellWidth(line) > 59 {
			t.Fatalf("metadata or rules wrapped: %q", line)
		}
	}
}

func TestFramedComposerActualEditorKeepsCursorOnInputAtEveryHeight(t *testing.T) {
	u, p := shellHarness(t, 40, 24, false)
	s := &session{terminal: u}
	editor := textEditor{text: "first\nsecond", cursor: len("first\nsecond")}
	for height := 1; height <= 24; height++ {
		p.Send(tea.WindowSizeMsg{Width: 40, Height: height})
		_ = u.paintComposer(s.composerFrame(editor))
		view := p.model.View()
		lines := strings.Split(view.Content, "\n")
		if len(lines) != height || view.Cursor == nil || view.Cursor.Y >= height || !strings.Contains(lines[view.Cursor.Y], "second") {
			t.Fatalf("height %d left the cursor outside the actual editor: %q cursor=%#v", height, view.Content, view.Cursor)
		}
	}
}

func TestFramedComposerBusyFooterKeepsInterruptHint(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> "}, {Text: "Ctrl+C interrupts", Dim: true}}, CursorRow: -1})
	_ = u.paint(terminalFrame{CursorRow: -1}) // execution has no menu overlay
	view := p.model.View()
	lines := strings.Split(view.Content, "\n")
	if view.Cursor != nil || lines[23] != "Ctrl+C interrupts" || strings.Contains(view.Content, "finish this panel") || strings.Contains(view.Content, "Working") {
		t.Fatalf("busy footer falsely describes an editable panel: %q", view.Content)
	}
}

func TestFixedShellScrollKeysPreserveDraftAndOverlayRouting(t *testing.T) {
	u, p := shellHarness(t, 60, 16, false)
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> untouched"}, {Text: "Tab actions"}}, CursorRow: 0})
	for i := range 40 {
		_, _ = fmt.Fprintf(u.writer, "output %02d\n", i)
	}
	before := p.model.View().Content
	p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	after := p.model.View().Content
	if before == after || !strings.Contains(after, "> untouched") || len(u.events) != 0 {
		t.Fatal("PageUp failed to scroll locally or reached composer input")
	}
	// New output must not pull the scrolled view back to the tail.
	_, _ = u.writer.Write([]byte("new output\n"))
	if strings.Contains(p.model.View().Content, "new output") {
		t.Fatal("streaming displaced a manually scrolled view")
	}
	for range 100 {
		p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	}
	for range 10 {
		p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	}
	if !strings.Contains(p.model.View().Content, "new output") {
		t.Fatal("repeated paging at the top accumulated invisible overscroll")
	}
	_ = u.paint(terminalFrame{Lines: []terminalLine{{Text: "Workflow steps"}}, CursorRow: -1})
	p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	event, err := u.next(context.Background())
	if err != nil || event.kind != keyPageDown {
		t.Fatalf("overlay lost its own paging keys: %v %v", event, err)
	}
}

func TestFixedShellResizeAndNoColorKeepCursorVisible(t *testing.T) {
	u, p := shellHarness(t, 80, 24, false)
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> first"}, {Text: "  👩‍💻 日本語"}, {Text: "Tab actions"}}, CursorRow: 1, CursorCol: 9})
	_ = u.notice(terminalNotice{Text: "Usage unknown", Kind: toneWarning})
	for _, size := range [][2]int{{80, 24}, {18, 8}, {8, 4}, {3, 2}, {1, 1}, {80, 24}} {
		p.Send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		v := p.model.View()
		lines := strings.Split(v.Content, "\n")
		if len(lines) != size[1] || strings.Contains(v.Content, "\x1b") || !utf8.ValidString(v.Content) {
			t.Fatalf("unbounded/uncolored resize %v: %q", size, v.Content)
		}
		for _, line := range lines {
			if cellWidth(line) > max(1, size[0]-1) {
				t.Fatalf("line wraps at %v: %q", size, line)
			}
		}
		if v.Cursor == nil || v.Cursor.Y >= size[1] || v.Cursor.X >= size[0] {
			t.Fatalf("cursor is not reachable at %v: %#v", size, v.Cursor)
		}
	}
	if !strings.Contains(p.model.View().Content, "日本語") || p.model.shell.composer.Lines[1].Text != "  👩‍💻 日本語" {
		t.Fatal("resize changed retained composition")
	}
}

func TestFixedShellSeverityEscapesUntrustedControlsAndBoundsHistory(t *testing.T) {
	u, p := shellHarness(t, 80, 24, true)
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> "}, {Text: "Tab actions"}}, CursorRow: 0})
	_ = u.notice(terminalNotice{Text: "Provider \x1b[2J error", Kind: toneError})
	_, _ = u.writer.Write([]byte("\x1b]52;c;clipboard\a\n"))
	v := p.model.View()
	if !strings.Contains(v.Content, "\x1b[31m[Error]") || strings.Contains(v.Content, "\x1b[2J") || strings.Contains(v.Content, "\x1b]52;") {
		t.Fatalf("unsafe or unlabelled error rendering: %q", v.Content)
	}
	_ = u.notice(terminalNotice{Text: "Unknown quota", Kind: toneWarning})
	if !strings.Contains(p.model.View().Content, "\x1b[33m[Warning]") {
		t.Fatal("warning lost semantic color/label")
	}
	for range 1000 {
		_, _ = u.writer.Write([]byte(strings.Repeat("x", 1000) + "\n"))
	}
	if p.model.shell.bytes > shellTranscriptBytes || len(p.model.shell.transcript) > shellTranscriptLines || !p.model.shell.dropped {
		t.Fatal("display transcript exceeded bounds")
	}
	_, _ = u.writer.Write(bytes.Repeat([]byte("x"), 10000))
	if u.writer.pending.Len() > 4100 {
		t.Fatal("fullscreen incomplete line grew without bound")
	}
}

func TestFixedShellObservedBackgroundChangesPaletteNotGeometry(t *testing.T) {
	u, p := shellHarness(t, 80, 24, true)
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> draft"}, {Text: "Tab actions"}}, CursorRow: 0})
	_ = u.notice(terminalNotice{Text: "Quota is unknown", Kind: toneWarning})
	p.Send(tea.BackgroundColorMsg{Color: color.White})
	light := p.model.View()
	if !strings.Contains(light.Content, "\x1b[38;2;138;88;0m[Warning]") {
		t.Fatal("light background did not get the darker warning label")
	}
	p.Send(tea.BackgroundColorMsg{Color: color.Black})
	dark := p.model.View()
	if !strings.Contains(dark.Content, "\x1b[38;2;255;209;102m[Warning]") || light.Cursor.Y != dark.Cursor.Y || light.Cursor.X != dark.Cursor.X {
		t.Fatal("theme response changed geometry or lost dark warning styling")
	}
	p.model.color = false
	plain := p.model.View()
	if strings.Contains(plain.Content, "\x1b") || !strings.Contains(plain.Content, "[Warning]") || plain.Cursor.Y != dark.Cursor.Y {
		t.Fatal("no-color changed meaning or cursor geometry")
	}
}

func TestFixedShellActualRendererClosesAfterFinalNotice(t *testing.T) {
	var output bytes.Buffer
	u := newTerminalUI(strings.NewReader(""), &output, func() {}, false)
	if err := u.setChrome(terminalChrome{Version: "test", Location: "/fixture", Model: "demo/fast"}); err != nil {
		t.Fatal(err)
	}
	_ = u.paintComposer(terminalFrame{Lines: []terminalLine{{Text: "> kept"}, {Text: "Tab actions"}}, CursorRow: 0})
	_, _ = u.writer.Write([]byte("Final result is saved.\n"))
	_ = u.notice(terminalNotice{Text: "Draft kept", Kind: toneSuccess})
	if err := u.close(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.HasSuffix(text, "Final result is saved.\n[OK] Draft kept\n") {
		t.Fatalf("actual renderer lost final diagnostic after shutdown: %q", text)
	}
}

func TestFixedShellFinalDiagnosticCannotBlockShutdownForever(t *testing.T) {
	u, _ := shellHarness(t, 80, 24, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u.cancel, u.stopInput = cancel, func() error { return nil }
	w := &heldWriter{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(w.release)
	u.finalOutput = w
	_ = u.notice(terminalNotice{Text: "Draft preserved", Kind: toneError})
	close(u.done) // model is settled; only the final original output is blocked
	result := make(chan error, 1)
	go func() { result <- u.close() }()
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("did not consume the post-renderer output path")
	}
	select {
	case err := <-result:
		if err == nil || ctx.Err() == nil {
			t.Fatal("blocked final output reported clean shutdown")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("final diagnostic exceeded the shutdown bound")
	}
}
