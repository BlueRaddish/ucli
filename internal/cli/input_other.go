//go:build !windows

package cli

import "io"

func terminalInput(reader io.Reader) io.Reader { return reader }
