//go:build linux || darwin

package app

import "os/exec"

func hideFixture(cmd *exec.Cmd) {}
