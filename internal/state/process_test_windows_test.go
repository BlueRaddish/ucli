//go:build windows

package state

import (
	"os/exec"
	"syscall"
)

func hideTestChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
