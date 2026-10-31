package cli

import (
	"reflect"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	xwindows "github.com/charmbracelet/x/windows"
	"golang.org/x/sys/windows"
)

func mouseRecord(buttons, flags, modifiers uint32) xwindows.MouseEventRecord {
	return xwindows.MouseEventRecord{
		MousePositon: windows.Coord{X: 7, Y: 12},
		ButtonState:  buttons, EventFlags: flags, ControlKeyState: modifiers,
	}
}

func mouseBytes(t *testing.T, state *consoleMouseState, event xwindows.MouseEventRecord, want string) string {
	t.Helper()
	var text strings.Builder
	state.writeSGR(&text, event)
	got := text.String()
	if got != want {
		t.Fatalf("mouse record serialized as %q, want %q", got, want)
	}
	for _, value := range []byte(got) {
		if value == 0 || value > 0x7e || (value < 0x20 && value != 0x1b) {
			t.Fatalf("mouse record introduced a text/control byte: %q", got)
		}
	}
	return got
}

func TestConsoleMouseButtonPressReleaseAndModifiers(t *testing.T) {
	for _, tc := range []struct {
		name           string
		mask           uint32
		press, release string
	}{
		{"left", xwindows.FROM_LEFT_1ST_BUTTON_PRESSED, "\x1b[<0;8;13M", "\x1b[<0;8;13m"},
		{"middle", xwindows.FROM_LEFT_2ND_BUTTON_PRESSED, "\x1b[<1;8;13M", "\x1b[<1;8;13m"},
		{"right", xwindows.RIGHTMOST_BUTTON_PRESSED, "\x1b[<2;8;13M", "\x1b[<2;8;13m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state consoleMouseState
			mouseBytes(t, &state, mouseRecord(tc.mask, 0, 0), tc.press)
			mouseBytes(t, &state, mouseRecord(0, 0, 0), tc.release)
			mouseBytes(t, &state, mouseRecord(0, 0, 0), "")
		})
	}
	for _, tc := range []struct {
		name           string
		modifiers      uint32
		press, release string
	}{
		{"shift", xwindows.SHIFT_PRESSED, "\x1b[<4;8;13M", "\x1b[<4;8;13m"},
		{"left-alt", xwindows.LEFT_ALT_PRESSED, "\x1b[<8;8;13M", "\x1b[<8;8;13m"},
		{"right-alt", xwindows.RIGHT_ALT_PRESSED, "\x1b[<8;8;13M", "\x1b[<8;8;13m"},
		{"left-control", xwindows.LEFT_CTRL_PRESSED, "\x1b[<16;8;13M", "\x1b[<16;8;13m"},
		{"right-control", xwindows.RIGHT_CTRL_PRESSED, "\x1b[<16;8;13M", "\x1b[<16;8;13m"},
		{"combined", xwindows.SHIFT_PRESSED | xwindows.LEFT_ALT_PRESSED | xwindows.RIGHT_CTRL_PRESSED, "\x1b[<28;8;13M", "\x1b[<28;8;13m"},
		{"locks-ignored", xwindows.CAPSLOCK_ON | xwindows.NUMLOCK_ON | xwindows.SCROLLLOCK_ON | xwindows.ENHANCED_KEY, "\x1b[<0;8;13M", "\x1b[<0;8;13m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state consoleMouseState
			mouseBytes(t, &state, mouseRecord(xwindows.FROM_LEFT_1ST_BUTTON_PRESSED, 0, tc.modifiers), tc.press)
			mouseBytes(t, &state, mouseRecord(0, 0, tc.modifiers), tc.release)
		})
	}
}

func TestConsoleMouseMotionAndDuplicateRecordsNeverActivate(t *testing.T) {
	var state consoleMouseState
	left := uint32(xwindows.FROM_LEFT_1ST_BUTTON_PRESSED)
	mouseBytes(t, &state, mouseRecord(0, xwindows.MOUSE_MOVED, 0), "")
	mouseBytes(t, &state, mouseRecord(left, xwindows.MOUSE_MOVED, 0), "")
	mouseBytes(t, &state, mouseRecord(left, 0, 0), "\x1b[<0;8;13M")
	mouseBytes(t, &state, mouseRecord(left, 0, 0), "")
	mouseBytes(t, &state, mouseRecord(left, xwindows.DOUBLE_CLICK, 0), "")
	mouseBytes(t, &state, mouseRecord(left, xwindows.MOUSE_MOVED, 0), "")
	mouseBytes(t, &state, mouseRecord(0, 0, 0), "\x1b[<0;8;13m")
	// DOUBLE_CLICK replaces the second actual press. It is one transition,
	// not two activations, and another copy of that record remains inert.
	mouseBytes(t, &state, mouseRecord(left, xwindows.DOUBLE_CLICK, 0), "\x1b[<0;8;13M")
	mouseBytes(t, &state, mouseRecord(left, xwindows.DOUBLE_CLICK, 0), "")
	mouseBytes(t, &state, mouseRecord(0, 0, 0), "\x1b[<0;8;13m")
	chord := left | xwindows.FROM_LEFT_2ND_BUTTON_PRESSED | xwindows.RIGHTMOST_BUTTON_PRESSED
	mouseBytes(t, &state, mouseRecord(chord, 0, 0), "\x1b[<0;8;13M\x1b[<1;8;13M\x1b[<2;8;13M")
	mouseBytes(t, &state, mouseRecord(xwindows.RIGHTMOST_BUTTON_PRESSED, 0, 0), "\x1b[<0;8;13m\x1b[<1;8;13m")
	mouseBytes(t, &state, mouseRecord(left, 0, 0), "\x1b[<2;8;13m\x1b[<0;8;13M")
}

func TestConsoleMouseWheelDirectionDoesNotChangeButtons(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags uint32
		delta int16
		want  string
	}{
		{"up", xwindows.MOUSE_WHEELED, 120, "\x1b[<64;8;13M"},
		{"down", xwindows.MOUSE_WHEELED, -120, "\x1b[<65;8;13M"},
		{"right", xwindows.MOUSE_HWHEELED, 120, "\x1b[<67;8;13M"},
		{"left", xwindows.MOUSE_HWHEELED, -120, "\x1b[<66;8;13M"},
		{"high-resolution", xwindows.MOUSE_WHEELED, 1, "\x1b[<64;8;13M"},
		{"bounded-large", xwindows.MOUSE_WHEELED, 32767, "\x1b[<64;8;13M"},
		{"bounded-negative", xwindows.MOUSE_WHEELED, -32768, "\x1b[<65;8;13M"},
		{"zero", xwindows.MOUSE_WHEELED, 0, ""},
		{"horizontal-zero", xwindows.MOUSE_HWHEELED, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state consoleMouseState
			left := uint32(xwindows.FROM_LEFT_1ST_BUTTON_PRESSED)
			mouseBytes(t, &state, mouseRecord(left, 0, 0), "\x1b[<0;8;13M")
			buttons := uint32(uint16(tc.delta))<<16 | left
			mouseBytes(t, &state, mouseRecord(buttons, tc.flags, 0), tc.want)
			mouseBytes(t, &state, mouseRecord(left, 0, 0), "")
			mouseBytes(t, &state, mouseRecord(0, 0, 0), "\x1b[<0;8;13m")
		})
	}
	var state consoleMouseState
	mouseBytes(t, &state, mouseRecord(120<<16, xwindows.MOUSE_WHEELED, xwindows.SHIFT_PRESSED|xwindows.RIGHT_CTRL_PRESSED), "\x1b[<84;8;13M")
}

func TestConsoleMouseInvalidRecordsAndCoordinateBounds(t *testing.T) {
	var state consoleMouseState
	left := uint32(xwindows.FROM_LEFT_1ST_BUTTON_PRESSED)
	for _, flags := range []uint32{0x10, xwindows.MOUSE_MOVED | xwindows.MOUSE_WHEELED} {
		mouseBytes(t, &state, mouseRecord(left, flags, 0), "")
	}
	mouseBytes(t, &state, mouseRecord(xwindows.FROM_LEFT_3RD_BUTTON_PRESSED|xwindows.FROM_LEFT_4TH_BUTTON_PRESSED, 0, 0), "")
	for _, coordinate := range []windows.Coord{{X: -1}, {Y: -1}} {
		event := mouseRecord(left, 0, 0)
		event.MousePositon = coordinate
		mouseBytes(t, &state, event, "")
	}
	event := mouseRecord(left, 0, 0)
	event.MousePositon = windows.Coord{}
	mouseBytes(t, &state, event, "\x1b[<0;1;1M")
	event.ButtonState = 0
	event.MousePositon = windows.Coord{X: 32767, Y: 32767}
	mouseBytes(t, &state, event, "\x1b[<0;32768;32768m")
}

func TestConsoleMouseSGRDecodedAsMouseNotPromptText(t *testing.T) {
	var state consoleMouseState
	var decoder uv.EventDecoder
	left := uint32(xwindows.FROM_LEFT_1ST_BUTTON_PRESSED)
	for _, tc := range []struct {
		record xwindows.MouseEventRecord
		bytes  string
		want   uv.Event
	}{
		{mouseRecord(left, 0, xwindows.SHIFT_PRESSED), "\x1b[<4;8;13M", uv.MouseClickEvent{X: 7, Y: 12, Button: uv.MouseLeft, Mod: uv.ModShift}},
		{mouseRecord(0, 0, xwindows.LEFT_CTRL_PRESSED), "\x1b[<16;8;13m", uv.MouseReleaseEvent{X: 7, Y: 12, Button: uv.MouseLeft, Mod: uv.ModCtrl}},
		{mouseRecord(120<<16, xwindows.MOUSE_HWHEELED, xwindows.LEFT_ALT_PRESSED), "\x1b[<75;8;13M", uv.MouseWheelEvent{X: 7, Y: 12, Button: uv.MouseWheelRight, Mod: uv.ModAlt}},
	} {
		encoded := mouseBytes(t, &state, tc.record, tc.bytes)
		n, event := decoder.Decode([]byte(encoded))
		if n != len(encoded) || !reflect.DeepEqual(event, tc.want) {
			t.Fatalf("pinned VT decoder returned %T %#v after %d bytes, want %#v", event, event, n, tc.want)
		}
	}
}
