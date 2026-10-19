//go:build linux

package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

type unixProcess struct {
	cmd   *exec.Cmd
	group groupGuard
}

func startNative(s Spec, in, out, diagnostic *os.File) (nativeProcess, error) {
	cmd := exec.Command(s.Executable, s.Args...)
	cmd.Env, cmd.Dir = s.Env, s.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, diagnostic
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &unixProcess{cmd: cmd}, nil
}
func (p *unixProcess) wait() (int, error) {
	// WNOWAIT retains the zombie leader and therefore the PID/PGID identity.
	// A normal cmd.Wait first would release that identity before group cleanup.
	var info unix.Siginfo
	var observeErr error
	for {
		observeErr = unix.Waitid(unix.P_PID, p.cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(observeErr, syscall.EINTR) {
			break
		}
	}
	if errors.Is(observeErr, syscall.ECHILD) {
		// An unexpected external reaper/auto-reap means ownership is lost.
		// Neither signal nor wait again by numeric PID: it may have been reused.
		p.group.abandon()
		return -1, errors.Join(fmt.Errorf("owned child is no longer waitable: %w", observeErr), p.cmd.Process.Release())
	}
	code, err := p.group.finish(p.killGroup, p.reap)
	if observeErr != nil {
		err = errors.Join(fmt.Errorf("observe owned child before reaping: %w", observeErr), err)
	}
	return code, err
}
func (p *unixProcess) reap() (int, error) {
	err := p.cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
func (p *unixProcess) stop() error {
	return p.group.stop(p.killGroup)
}
func (p *unixProcess) killGroup() error {
	err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
func (p *unixProcess) close() error { return nil }
func alive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}
