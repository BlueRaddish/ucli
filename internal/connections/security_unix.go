//go:build !windows

package connections

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func secureCreatedPath(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return os.Chmod(path, mode)
}
func localPath(string) error { return nil }
func isReparse(string) bool  { return false }
func tryLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
func unlockFile(file *os.File) { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }
