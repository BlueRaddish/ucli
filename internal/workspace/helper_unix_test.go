//go:build linux || darwin

package workspace

import (
	"os"
	"os/exec"
)

func hideTest(cmd *exec.Cmd)               {}
func makeAlias(alias, target string) error { return os.Symlink(target, alias) }
