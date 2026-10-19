//go:build !windows

package state

import "os/exec"

func hideTestChild(cmd *exec.Cmd) {}
