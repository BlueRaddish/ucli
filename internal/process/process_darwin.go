//go:build darwin

package process

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func startNative(_ Spec, _, _, _ *os.File) (nativeProcess, error) {
	// A Darwin NOTE_EXIT watch alone can miss an exit before registration.
	// Do not restore numeric group signalling after cmd.Wait: the PGID may be
	// reused. Enable only after a non-reaping observer is verified on a Mac host.
	return nil, fmt.Errorf("%w: macOS child ownership awaits a verified non-reaping exit observer", ErrUnsupportedPlatform)
}

func alive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}
