//go:build windows

package workspace

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
)

func hideTest(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}
func makeAlias(alias, target string) error {
	// Fixed test-owned paths contain no command-shell metacharacters.
	cmd := exec.Command("cmd.exe", "/c", "mklink", "/J", alias, target)
	hideTest(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("junction fixture: %w: %s", err, out)
	}
	return nil
}
