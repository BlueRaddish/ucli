package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	xwindows "github.com/charmbracelet/x/windows"
)

func TestConsoleUTF16BoundariesAndModifierSuppression(t *testing.T) {
	r := &vtConsoleInput{}
	var text strings.Builder
	for _, unit := range []uint16{'e', 0x301, 0xd83d, 0xdc69, 0xd83c, 0xdffd, 0x200d, 0xd83d, 0xdcbb} {
		if err := r.consoleRune(&text, unit); err != nil {
			t.Fatal(err)
		}
	}
	if text.String() != "e\u0301👩🏽‍💻" || r.high != 0 {
		t.Fatalf("surrogate/combining input changed: %q", text.String())
	}
	for _, bad := range [][]uint16{{0xdc69}, {0xd83d, 'x'}} {
		r = &vtConsoleInput{}
		var err error
		for _, unit := range bad {
			err = r.consoleRune(&text, unit)
			if err != nil {
				break
			}
		}
		if err == nil {
			t.Fatal("invalid surrogate silently replaced")
		}
	}
	if err := r.consoleRune(&text, 0xfffd); err == nil {
		t.Fatal("known lossy library paste character was silently accepted")
	}
	for _, key := range []uint16{0x10, 0x11, 0x12, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5} {
		if !consoleModifier(key) {
			t.Fatal("modifier may insert NUL")
		}
	}
	for _, key := range []uint16{0x20, 0x25, 0x0d, 0x09} {
		if consoleModifier(key) {
			t.Fatal("meaningful control key discarded")
		}
	}
}

func TestConsoleNULKeysCannotBecomeDroppedPasteShortcuts(t *testing.T) {
	for _, key := range []uint16{0x32, 0x20} {
		for _, ctrl := range []uint32{xwindows.LEFT_CTRL_PRESSED, xwindows.RIGHT_CTRL_PRESSED} {
			if !consoleNULKey(key, ctrl|xwindows.SHIFT_PRESSED) {
				t.Fatal("NUL control spelling was accepted")
			}
		}
		if consoleNULKey(key, xwindows.SHIFT_PRESSED) {
			t.Fatal("ordinary text key was mistaken for NUL")
		}
	}
	if consoleNULKey(0x25, xwindows.LEFT_CTRL_PRESSED) || consoleNULKey(0x11, xwindows.LEFT_CTRL_PRESSED) {
		t.Fatal("navigation/modifier key was mistaken for NUL")
	}
}

func TestConsoleTransportClosedReaderNeverConsumesAnotherKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &vtConsoleInput{ctx: ctx, cancel: cancel, pending: "unread"}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if n, err := r.Read(make([]byte, 20)); n != 0 || err == nil {
			t.Errorf("closed reader consumed typeahead: %d %v", n, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("closed input reader blocked")
	}
}
