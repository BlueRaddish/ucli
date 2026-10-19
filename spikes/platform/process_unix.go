//go:build linux || darwin

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

type processOwner struct {
	pid    int
	cmd    *exec.Cmd
	stages map[string]float64
}

func configureHidden(cmd *exec.Cmd) {}
func startOwned(cmd *exec.Cmd) (*processOwner, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &processOwner{pid: cmd.Process.Pid, cmd: cmd}, nil
}
func (o *processOwner) Wait() error { return o.cmd.Wait() }
func (o *processOwner) Stop()       { syscall.Kill(-o.pid, syscall.SIGKILL) }
func (o *processOwner) Close()      { o.Stop() }
func physicalIdentity(root string) (string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("missing filesystem identity")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
func processAlive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}
