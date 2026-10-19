package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func terminalInput(reader io.Reader) io.Reader {
	file, ok := reader.(*os.File)
	if !ok || !isTerminal(file) {
		return reader
	}
	return &consoleReader{handle: windows.Handle(file.Fd())}
}

// os.File's cooked Windows console path maps both an interrupted zero-character
// read and Ctrl+Z to EOF. Keep the native line editor, but distinguish the actual
// Ctrl+Z character from an interrupted read before os.File loses that information.
// No console modes or signal handlers are changed.
type consoleReader struct {
	handle  windows.Handle
	pending []byte
	tail    uint16
	eof     bool
}

func (r *consoleReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if r.eof {
			return 0, io.EOF
		}
		var buffer [4096]uint16
		var count uint32
		start := 0
		if r.tail != 0 {
			buffer[0] = r.tail
			start = 1
			r.tail = 0
		}
		err := windows.ReadConsole(r.handle, &buffer[start], uint32(len(buffer)-start), &count, nil)
		if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
			return 0, context.Canceled
		}
		if err != nil {
			return 0, err
		}
		if count == 0 {
			return 0, context.Canceled
		}
		characters := buffer[:start+int(count)]
		for i, c := range characters {
			if c == 0x1a {
				characters = characters[:i]
				r.eof = true
				break
			}
		}
		if n := len(characters); n > 0 && !r.eof && characters[n-1] >= 0xd800 && characters[n-1] <= 0xdbff {
			r.tail = characters[n-1]
			characters = characters[:n-1]
		}
		r.pending = []byte(string(utf16.Decode(characters)))
	}
	n := copy(dst, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}
