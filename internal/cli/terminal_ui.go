package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"ucli.local/ucli/internal/core"
)

type keyKind uint8

const (
	keyText keyKind = iota
	keyPaste
	keyEnter
	keyEscape
	keyUp
	keyDown
	keyHome
	keyEnd
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyTab
	keyBackTab
	keyAltEnter
	keyEOF
	keyInterrupt
	keyResize
	keyClear
	keyKillEnd
	keyTaskReady
	keyPageUp
	keyPageDown
	keyLatestPrompt
	keyLatestOutput
)

type keyEvent struct {
	kind keyKind
	text string
}

type terminalLine struct {
	Text         string
	Focused, Dim bool
	Band         bool
	Tone         terminalTone
}

type terminalFrame struct {
	Lines                []terminalLine
	CursorRow, CursorCol int // zero based; a negative row hides the cursor
	Completion           completionResult
	CompletionQuery      string
}

type terminalProgram interface {
	Send(tea.Msg)
	Quit()
}

type terminalUI struct {
	program       terminalProgram
	events        chan keyEvent
	done          chan struct{}
	cancel        context.CancelFunc
	stopInput     func() error
	interrupt     context.CancelFunc
	width, height atomic.Int64
	queuedBytes   atomic.Int64
	liveFragment  atomic.Bool
	shellEnabled  atomic.Bool
	panelSurface  atomic.Bool
	composerRows  atomic.Int64
	mu            sync.Mutex
	err           error
	writer        *terminalWriter
	finalOutput   io.Writer
	finalText     string
}

type terminalFrameMsg terminalFrame
type terminalFragmentMsg string

type terminalModel struct {
	ui                  *terminalUI
	frame               terminalFrame
	fragment            string
	color               bool
	shell               terminalShell
	theme               terminalTheme
	options             terminalOptions
	mascot              terminalMascot
	interruptUntil      time.Time
	interruptGeneration uint64
}

func (m terminalModel) Init() tea.Cmd {
	if m.color {
		return tea.RequestBackgroundColor
	}
	return nil
}

func (m terminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var event keyEvent
	emit := false
	switch value := msg.(type) {
	case interruptConfirmExpiredMsg:
		if value.Generation == m.interruptGeneration {
			m.clearInterruptConfirmation()
		}
		return m, nil
	case terminalActivityMsg:
		command := m.startMascot(value, time.Now())
		return m, command
	case mascotTickMsg:
		if value.Generation == m.mascot.generation {
			command := m.advanceMascot(value.At)
			return m, command
		}
	case tea.BackgroundColorMsg:
		if value.Color != nil {
			m.theme = themeLight
			if value.IsDark() {
				m.theme = themeDark
			}
		}
	case terminalExitMsg:
		m.stopMascot()
		m.mascot.closed = true
		if m.shell.enabled {
			var final []string
			tail := strings.Split(m.shell.transcriptText(), "\n")
			for _, text := range tail[max(0, len(tail)-2):] {
				final = append(final, safe(streamFragment(text)))
			}
			if m.shell.notice.Text != "" {
				notice := safe(noticeLine(m.shell.notice).Text)
				if len(final) == 0 || final[len(final)-1] != notice {
					final = append(final, notice)
				}
			}
			m.ui.mu.Lock()
			if len(final) > 0 {
				m.ui.finalText = strings.Join(final, "\n") + "\n"
			}
			m.ui.mu.Unlock()
			m.shell.enabled = false
			m.fragment = ""
		}
		m.frame = terminalFrame{CursorRow: -1}
	case terminalChromeMsg:
		m.shell.enabled = true
		m.shell.chrome = terminalChrome(value)
	case terminalComposerMsg:
		m.shell.composer = terminalFrame(value)
		m.shell.composerFocus = true
		m.frame = terminalFrame{CursorRow: -1}
		if !m.shell.enabled {
			m.frame = terminalFrame(value)
		}
	case terminalNoticeMsg:
		if value.Kind == toneError || value.Kind == toneWarning || m.mascot.id == "wave" || m.mascot.id == "done" {
			m.stopMascot()
		}
		if terminalNotice(value) != m.shell.notice && (value.Kind == toneError || value.Kind == toneWarning) {
			text := noticeLine(terminalNotice(value)).Text
			// Progress errors already write their full diagnostic to the transcript.
			// Keep notice-only errors in history without repeating that diagnostic.
			logged := strings.TrimSpace(m.shell.transcriptText())
			diagnostic := strings.TrimSpace(safe(value.Text))
			if logged != diagnostic && !strings.HasSuffix(logged, "\n"+diagnostic) {
				m.shell.append(text)
			}
		}
		m.shell.notice = terminalNotice(value)
	case terminalMessageMsg:
		m.shell.message(value)
	case terminalTranscriptMsg:
		m.shell.append(string(value))
		m.fragment = ""
	case terminalFrameMsg:
		m.frame = terminalFrame(value)
		m.shell.composerFocus = len(m.frame.Lines) == 0
	case terminalFragmentMsg:
		m.fragment = string(value)
		m.ui.liveFragment.Store(m.fragment != "")
	case tea.WindowSizeMsg:
		m.ui.width.Store(int64(max(1, value.Width)))
		m.ui.height.Store(int64(max(1, value.Height)))
		event, emit = keyEvent{kind: keyResize}, true
	case tea.PasteMsg:
		m.clearInterruptConfirmation()
		if len(value.Content) > core.MaxInputBytes || !utf8.ValidString(value.Content) || strings.ContainsRune(value.Content, 0) {
			m.ui.fail(core.Problem("invalid_input", "Pasted input must be valid UTF-8 without NUL, at most 1 MiB.", 2))
			break
		}
		event, emit = keyEvent{kind: keyPaste, text: value.Content}, true
	case tea.KeyPressMsg:
		event, emit = terminalKey(value)
		if emit && event.kind != keyInterrupt {
			m.clearInterruptConfirmation()
		}
		if m.shell.enabled && m.shell.composerFocus && !m.shell.composer.Completion.Visible && (event.kind == keyPageUp || event.kind == keyPageDown || event.kind == keyLatestPrompt || event.kind == keyLatestOutput) {
			budget := terminalShellBudget(int(m.ui.height.Load()), len(m.shell.composer.Lines), m.shell.chrome.Panel)
			m.shell.navigate(event.kind, m.fragment, int(m.ui.width.Load()), budget.body)
			emit = false
		}
	}
	if emit {
		if event.kind == keyInterrupt {
			command := m.confirmInterrupt(time.Now())
			return m, command
		} else {
			m.ui.enqueue(event)
		}
	}
	return m, nil
}

func terminalKey(value tea.KeyPressMsg) (keyEvent, bool) {
	switch value.Keystroke() {
	case "enter":
		return keyEvent{kind: keyEnter}, true
	case "alt+enter", "shift+enter", "ctrl+j":
		return keyEvent{kind: keyAltEnter}, true
	case "esc", "escape":
		return keyEvent{kind: keyEscape}, true
	case "up":
		return keyEvent{kind: keyUp}, true
	case "down":
		return keyEvent{kind: keyDown}, true
	case "left":
		return keyEvent{kind: keyLeft}, true
	case "right":
		return keyEvent{kind: keyRight}, true
	case "home", "ctrl+a":
		return keyEvent{kind: keyHome}, true
	case "end", "ctrl+e":
		return keyEvent{kind: keyEnd}, true
	case "ctrl+up":
		return keyEvent{kind: keyLatestPrompt}, true
	case "ctrl+end":
		return keyEvent{kind: keyLatestOutput}, true
	case "pgup", "pageup":
		return keyEvent{kind: keyPageUp}, true
	case "pgdown", "pagedown":
		return keyEvent{kind: keyPageDown}, true
	case "backspace", "ctrl+h":
		return keyEvent{kind: keyBackspace}, true
	case "delete":
		return keyEvent{kind: keyDelete}, true
	case "tab":
		return keyEvent{kind: keyTab}, true
	case "shift+tab":
		return keyEvent{kind: keyBackTab}, true
	case "ctrl+d", "ctrl+z":
		return keyEvent{kind: keyEOF}, true
	case "ctrl+c":
		return keyEvent{kind: keyInterrupt}, true
	case "ctrl+u":
		return keyEvent{kind: keyClear}, true
	case "ctrl+k":
		return keyEvent{kind: keyKillEnd}, true
	}
	key := value.Key()
	if key.Text != "" && key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta) == 0 {
		return keyEvent{kind: keyText, text: key.Text}, true
	}
	return keyEvent{}, false
}

func (m terminalModel) View() tea.View {
	width, height := int(m.ui.width.Load()), int(m.ui.height.Load())
	if m.shell.enabled {
		return m.shellView(max(1, width), max(1, height))
	}
	var lines []string
	offset := 0
	if m.fragment != "" {
		lines = append(lines, clipCells(inlineText(m.fragment), max(1, width-1)))
		offset = 1
	}
	for _, line := range m.frame.Lines {
		if len(lines) >= max(1, height-1) {
			break
		}
		text := terminalStyle(line, width, m.color, m.theme)
		lines = append(lines, text)
	}
	if !m.interruptUntil.IsZero() {
		notice := terminalStyle(m.interruptNotice(width), width, m.color, m.theme)
		if len(lines) > 0 && m.frame.CursorRow+offset != len(lines)-1 {
			lines[len(lines)-1] = notice
		} else if len(lines) < max(1, height-1) {
			lines = append(lines, notice)
		}
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	if row := m.frame.CursorRow + offset; m.frame.CursorRow >= 0 && row < len(lines) {
		view.Cursor = tea.NewCursor(min(max(0, m.frame.CursorCol), max(0, width-2)), row)
	}
	// Inline rendering preserves terminal scrollback. Clipboard/mouse/focus
	// reporting, external windows, and terminal title changes are never enabled.
	return view
}

func newTerminalUI(in io.Reader, out io.Writer, interrupt context.CancelFunc, color bool, options ...terminalOptions) *terminalUI {
	ctx, cancel := context.WithCancel(context.Background())
	u := &terminalUI{events: make(chan keyEvent, 128), done: make(chan struct{}), cancel: cancel, interrupt: interrupt, finalOutput: out}
	in, releaseInput := terminalTransportInput(in, u.fail)
	u.stopInput = sync.OnceValue(releaseInput)
	u.width.Store(80)
	u.height.Store(24)
	u.writer = &terminalWriter{ui: u}
	var configured terminalOptions
	if len(options) > 0 {
		configured = options[0]
	}
	program := tea.NewProgram(terminalModel{ui: u, color: color, options: configured, frame: terminalFrame{CursorRow: -1}}, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignalHandler(), tea.WithFPS(60))
	u.program = program
	go func() {
		_, err := program.Run()
		if releaseErr := u.stopInput(); err == nil {
			err = releaseErr
		}
		if err != nil && !(errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil) {
			u.fail(err)
		}
		close(u.done)
	}()
	return u
}

func (u *terminalUI) fail(err error) {
	u.mu.Lock()
	if u.err == nil {
		u.err = err
	}
	u.mu.Unlock()
	u.interrupt()
}
func (u *terminalUI) failure() error { u.mu.Lock(); defer u.mu.Unlock(); return u.err }

func (u *terminalUI) size() (int, int) {
	height := int(u.height.Load())
	if u.shellEnabled.Load() {
		return int(u.width.Load()), terminalShellBudget(height, int(u.composerRows.Load()), u.panelSurface.Load()).body
	}
	if u.liveFragment.Load() {
		height--
	}
	return int(u.width.Load()), max(1, height)
}

func (u *terminalUI) enqueue(event keyEvent) {
	if u.failure() != nil {
		return
	}
	// Bounded foreground typeahead. No discarded prefix is presented as a full
	// accepted prompt. Larger queues/steering need the future async task service.
	bytes := int64(len(event.text) + 32)
	if u.queuedBytes.Add(bytes) > 2*core.MaxInputBytes {
		u.queuedBytes.Add(-bytes)
		u.fail(core.Problem("input_overflow", "Terminal input exceeded its bounded queue. Inspect the saved draft before resubmitting.", 2))
		return
	}
	select {
	case u.events <- event:
	default:
		u.queuedBytes.Add(-bytes)
		if event.kind != keyResize {
			u.fail(core.Problem("input_overflow", "Terminal input arrived faster than it could be handled. Inspect the saved draft before resubmitting.", 2))
		}
	}
}

func (u *terminalUI) next(ctx context.Context) (keyEvent, error) {
	return u.nextOr(ctx, nil)
}

func (u *terminalUI) nextOr(ctx context.Context, wake <-chan struct{}) (keyEvent, error) {
	if err := u.failure(); err != nil {
		return keyEvent{}, err
	}
	select {
	case <-wake:
		return keyEvent{kind: keyTaskReady}, nil
	case event := <-u.events:
		u.queuedBytes.Add(-int64(len(event.text) + 32))
		return event, nil
	case <-ctx.Done():
		if err := u.failure(); err != nil {
			return keyEvent{}, err
		}
		return keyEvent{}, ctx.Err()
	case <-u.done:
		if err := u.failure(); err != nil {
			return keyEvent{}, err
		}
		return keyEvent{}, io.EOF
	}
}

func (u *terminalUI) paint(frame terminalFrame) error {
	if err := u.failure(); err != nil {
		return err
	}
	u.program.Send(terminalFrameMsg(frame))
	return u.failure()
}

func (u *terminalUI) close() error {
	// Stop our input reader independently of renderer completion. The framework's
	// fallback CancelReader cannot interrupt it; delaying this until Run returns
	// could consume later shell input when output is blocked. The stop/join shares
	// the deadline below and never closes the borrowed console handle.
	go u.stopInput()
	// Include final message delivery in the deadline: Send may wait for an
	// unresponsive renderer. Cancellation releases pending framework sends.
	finished := make(chan error, 1)
	abandon := make(chan struct{})
	defer close(abandon)
	go func() {
		u.writer.flush()
		u.program.Send(terminalExitMsg{})
		u.program.Quit()
		select {
		case <-u.done:
		case <-abandon:
			return
		}
		u.mu.Lock()
		text := u.finalText
		u.mu.Unlock()
		var err error
		if text != "" && u.finalOutput != nil {
			var n int
			n, err = io.WriteString(u.finalOutput, text)
			if err == nil && n != len(text) {
				err = io.ErrShortWrite
			}
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		u.cancel()
		return errors.Join(u.failure(), err)
	case <-time.After(2 * time.Second):
		u.cancel()
		return errors.New("terminal shutdown did not finish within its deadline")
	}
}

// Complete transcript lines go above the inline view. An incomplete streaming
// line remains live in the view, so small deltas do not add spurious newlines.
type terminalWriter struct {
	ui      *terminalUI
	mu      sync.Mutex
	pending strings.Builder
}

func (w *terminalWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Input failure must still allow its final diagnostic through the renderer.
	// Only an exited renderer closes this output transport.
	select {
	case <-w.ui.done:
		return 0, io.ErrClosedPipe
	default:
	}
	text := string(data)
	for {
		part, rest, found := strings.Cut(text, "\n")
		w.pending.WriteString(part)
		if !found {
			break
		}
		w.complete(w.pending.String())
		w.pending.Reset()
		text = rest
	}
	if w.ui.shellEnabled.Load() && w.pending.Len() > 4096 {
		tail := streamFragment(w.pending.String())
		w.pending.Reset()
		w.pending.WriteString(tail)
	}
	w.ui.program.Send(terminalFragmentMsg(streamFragment(w.pending.String())))
	select {
	case <-w.ui.done:
		return 0, io.ErrClosedPipe
	default:
		return len(data), nil
	}
}

func streamFragment(text string) string {
	// Only a bounded tail is repainted for a growing, newline-free stream. The
	// full line still goes to scrollback on completion and to durable app state.
	const maxFragment = 4096
	if len(text) <= maxFragment {
		return text
	}
	start := len(text) - maxFragment
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return "… " + text[start:]
}
func (w *terminalWriter) flush() {
	// output.finish may have timed out while Write is still blocked in Send.
	// Serialize the buffer; terminal cancellation still releases a blocked Send.
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending.Len() > 0 {
		w.complete(w.pending.String())
		w.pending.Reset()
		w.ui.program.Send(terminalFragmentMsg(""))
	}
}

func (w *terminalWriter) complete(text string) {
	if w.ui.shellEnabled.Load() {
		w.ui.program.Send(terminalTranscriptMsg(text))
	} else {
		w.ui.program.Send(tea.Println(text)())
	}
}
