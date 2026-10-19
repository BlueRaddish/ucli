//go:build !windows

package cli

import "io"

func terminalTransportInput(in io.Reader, _ func(error)) (io.Reader, func() error) {
	return in, func() error { return nil }
}
