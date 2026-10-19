// Package workspace resolves local physical checkout identity without executing
// Git or reading repository configuration. It does not enforce filesystem access.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ucli.local/ucli/internal/core"
)

// Resolve preserves the caller's absolute execution directory while resolving
// aliases for the root/lease identity. A linked worktree owns its own physical root.
func Resolve(cwd string) (core.Workspace, error) {
	if cwd == "" {
		return core.Workspace{}, errors.New("workspace directory is required")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return core.Workspace{}, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return core.Workspace{}, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return core.Workspace{}, err
	}
	if !info.IsDir() {
		return core.Workspace{}, errors.New("workspace is not a directory")
	}
	root := real
	for dir := real; ; dir = filepath.Dir(dir) {
		marker, statErr := os.Stat(filepath.Join(dir, ".git"))
		if statErr == nil {
			if !marker.IsDir() && !marker.Mode().IsRegular() {
				return core.Workspace{}, errors.New("invalid .git marker")
			}
			root = dir
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return core.Workspace{}, fmt.Errorf("inspect checkout root: %w", statErr)
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	id, err := physicalIdentity(root)
	if err != nil {
		return core.Workspace{}, err
	}
	return core.Workspace{ID: id, Root: root, Dir: abs}, nil
}
