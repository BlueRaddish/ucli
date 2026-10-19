//go:build !windows

package artifact

import "os"

func secureCreatedPath(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return os.Chmod(path, mode)
}
func localPath(string) error { return nil }
func isReparse(string) bool  { return false }
