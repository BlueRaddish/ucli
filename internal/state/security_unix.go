//go:build !windows

package state

import "os"

func securePath(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return os.Chmod(path, mode)
}

func localPath(path string) error { return nil }
