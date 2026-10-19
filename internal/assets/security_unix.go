//go:build !windows

package assets

import "os"

func secureCreated(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return os.Chmod(path, mode)
}

func localPath(string) error { return nil }
func isReparse(string) bool  { return false }
