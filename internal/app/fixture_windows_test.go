//go:build windows

package app

import (
	"os/exec"
	"syscall"
)

func hideFixture(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}
