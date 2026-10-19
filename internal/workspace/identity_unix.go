//go:build linux || darwin

package workspace

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func physicalIdentity(root string) (string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("filesystem identity unavailable")
	}
	return fmt.Sprintf("unix:%d:%d", stat.Dev, stat.Ino), nil
}
