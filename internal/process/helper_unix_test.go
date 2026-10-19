//go:build linux || darwin

package process

import "os/exec"

func hideTestChild(cmd *exec.Cmd) {}
