package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf16"

	xwindows "github.com/charmbracelet/x/windows"
	"golang.org/x/sys/windows"
	"ucli.local/ucli/internal/core"
)

// The pinned framework's Windows VT record path emitted modifier NULs and
// damaged surrogate pairs in real ConPTY paste. Keep its portable VT decoder
// and mode restoration, but serialize this owned console input explicitly.
// This wrapper intentionally has no Name method: that selects the framework's
// generic byte decoder rather than its defective native-record reader.
type vtConsoleInput struct {
	file    *os.File
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	active  sync.WaitGroup
	pending string
	high    uint16
	failure func(error)
}

func terminalTransportInput(in io.Reader, failure func(error)) (io.Reader, func() error) {
	file, ok := in.(*os.File)
	if !ok || !isTerminal(file) {
		return in, func() error { return nil }
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader := &vtConsoleInput{file: file, ctx: ctx, cancel: cancel, failure: failure}
	return reader, reader.Close
}

func (r *vtConsoleInput) Fd() uintptr               { return r.file.Fd() }
func (r *vtConsoleInput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (r *vtConsoleInput) Close() error {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.active.Wait()
	return nil // The borrowed console handle is never closed.
}
func (r *vtConsoleInput) Read(data []byte) (int, error) {
	n, err := r.readConsole(data)
	if err != nil && !errors.Is(err, io.EOF) && r.failure != nil {
		// Keep the renderer alive while the application reports the original
		// typed failure. Returning it to the framework tears output down first.
		r.failure(err)
		return n, io.EOF
	}
	return n, err
}

func (r *vtConsoleInput) readConsole(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, io.EOF
	}
	r.active.Add(1)
	r.mu.Unlock()
	defer r.active.Done()
	for r.pending == "" {
		if r.ctx.Err() != nil {
			return 0, io.EOF
		}
		state, err := windows.WaitForSingleObject(windows.Handle(r.Fd()), 20)
		if err != nil {
			return 0, err
		}
		if state == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		if state != windows.WAIT_OBJECT_0 {
			return 0, fmt.Errorf("console input wait failed")
		}
		if r.ctx.Err() != nil {
			return 0, io.EOF
		}
		var records [256]xwindows.InputRecord
		var count uint32
		if err := xwindows.ReadConsoleInput(windows.Handle(r.Fd()), &records[0], uint32(len(records)), &count); err != nil {
			return 0, err
		}
		var text strings.Builder
		for _, record := range records[:count] {
			switch record.EventType {
			case xwindows.KEY_EVENT:
				event := record.KeyEvent()
				// Windows commits Alt+numpad/ConPTY fallback Unicode on Alt
				// release, including individual UTF-16 surrogate halves.
				if !event.KeyDown && !(event.VirtualKeyCode == 0x12 && event.Char != 0) {
					continue
				}
				character := uint16(event.Char)
				if character == 0 {
					if consoleNULKey(event.VirtualKeyCode, event.ControlKeyState) {
						return 0, core.Problem("invalid_input", "Terminal input must be valid UTF-8 without NUL; this input was not submitted.", 2)
					}
					if consoleModifier(event.VirtualKeyCode) {
						continue
					}
					if event.ControlKeyState&(xwindows.LEFT_ALT_PRESSED|xwindows.RIGHT_ALT_PRESSED) != 0 && event.VirtualKeyCode >= 0x60 && event.VirtualKeyCode <= 0x69 {
						continue
					}
					// Preserve nontext keys, including a real Ctrl+Space/NUL,
					// through the decoder's Win32 input protocol.
					fmt.Fprintf(&text, "\x1b[%d;%d;0;1;%d;%d_", event.VirtualKeyCode, event.VirtualScanCode, event.ControlKeyState, event.RepeatCount)
					continue
				}
				for range max(1, int(event.RepeatCount)) {
					if err := r.consoleRune(&text, character); err != nil {
						return 0, err
					}
					if text.Len() > core.MaxInputBytes {
						return 0, core.Problem("input_overflow", "Console input exceeded the 1 MiB input bound.", 2)
					}
				}
			case xwindows.WINDOW_BUFFER_SIZE_EVENT:
				size := record.WindowBufferSizeEvent().Size
				fmt.Fprintf(&text, "\x1b[8;%d;%dt", size.Y, size.X)
			}
		}
		r.pending = text.String()
	}
	n := copy(data, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func consoleNULKey(key uint16, modifiers uint32) bool {
	// Real ConPTY maps a pasted NUL to Ctrl+Shift+2 with Char==0. The VT
	// decoder otherwise treats this as a shortcut and silently drops it from
	// paste. Ctrl+Space is the other standard NUL spelling. Neither is a UCLI
	// shortcut; reject them before serializing meaningful nontext keys.
	return (key == 0x32 || key == 0x20) && modifiers&(xwindows.LEFT_CTRL_PRESSED|xwindows.RIGHT_CTRL_PRESSED) != 0
}

func consoleModifier(key uint16) bool {
	switch key {
	case 0x10, 0x11, 0x12, 0x14, 0x90, 0x91, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5:
		return true
	}
	return false
}
func (r *vtConsoleInput) consoleRune(text *strings.Builder, character uint16) error {
	// The pinned VT library drops U+FFFD at the end of bracketed paste, even
	// when it was valid UTF-8. Fail explicitly rather than save a changed prompt.
	// Remove this ceiling after a library upgrade passes the native round trip.
	if character == 0xfffd {
		return core.Problem("invalid_input", "This terminal input path cannot preserve U+FFFD. Use ucli run - with redirected UTF-8 text; this input was not submitted.", 2)
	}
	if r.high != 0 {
		if character >= 0xdc00 && character <= 0xdfff {
			text.WriteRune(utf16.DecodeRune(rune(r.high), rune(character)))
			r.high = 0
			return nil
		}
		return core.Problem("invalid_input", "Console input contains an incomplete UTF-16 character; no input from this read was accepted.", 2)
	}
	if character >= 0xd800 && character <= 0xdbff {
		r.high = character
		return nil
	}
	if character >= 0xdc00 && character <= 0xdfff {
		return core.Problem("invalid_input", "Console input contains an incomplete UTF-16 character; no input from this read was accepted.", 2)
	}
	text.WriteRune(rune(character))
	return nil
}
