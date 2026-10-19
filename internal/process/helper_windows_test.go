//go:build windows

package process

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
)

func hideTestChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}
