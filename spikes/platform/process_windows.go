//go:build windows

package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

type processOwner struct {
	job, process windows.Handle
	readers      []*os.File
	streams      chan error
	stages       map[string]float64
}

func configureHidden(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}
func startOwned(cmd *exec.Cmd) (*processOwner, error) {
	// Deliberately limited spike launcher: same executable and inherited cwd/env.
	// P09 must add explicit environment, directory and shell-specific validation.
	input, ok := cmd.Stdin.(*os.File)
	if !ok || cmd.Env != nil || cmd.Dir != "" || len(cmd.ExtraFiles) != 0 {
		return nil, errors.New("unsupported fixture process configuration")
	}
	defer input.Close()
	stageStart := time.Now()
	stages := map[string]float64{}
	mark := func(name string) {
		stages[name] = float64(time.Since(stageStart).Microseconds()) / 1000
		stageStart = time.Now()
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	owner := &processOwner{job: job, stages: stages, streams: make(chan error, 2)}
	started := false
	defer func() {
		if !started {
			owner.Close()
			for _, r := range owner.readers {
				r.Close()
			}
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, err
	}
	outputs := []io.Writer{cmd.Stdout, cmd.Stderr}
	handles := []windows.Handle{windows.Handle(input.Fd())}
	for range outputs {
		r, w, pipeErr := os.Pipe()
		if pipeErr != nil {
			return nil, pipeErr
		}
		owner.readers = append(owner.readers, r)
		defer w.Close()
		handles = append(handles, windows.Handle(w.Fd()))
	}
	for _, h := range handles {
		if err = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return nil, err
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	if err = attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return nil, err
	}
	si := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{
		Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW,
		ShowWindow: windows.SW_HIDE, StdInput: handles[0], StdOutput: handles[1], StdErr: handles[2],
	}, ProcThreadAttributeList: attributes.List()}
	exe, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return nil, err
	}
	argv, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmd.Args))
	if err != nil {
		return nil, err
	}
	var pi windows.ProcessInformation
	mark("job_setup_ms")
	if err = windows.CreateProcess(exe, argv, nil, nil, true,
		windows.CREATE_NO_WINDOW|windows.CREATE_SUSPENDED|windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		nil, nil, &si.StartupInfo, &pi); err != nil {
		return nil, err
	}
	owner.process = pi.Process
	defer windows.CloseHandle(pi.Thread)
	mark("start_suspended_ms")
	if err = windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		windows.TerminateProcess(pi.Process, 1) // Not yet owned by the job.
		return nil, err
	}
	mark("job_assignment_ms")
	// Retain CreateProcess's primary-thread handle instead of scanning every
	// system thread. The child remains suspended until job assignment succeeds.
	if _, err = windows.ResumeThread(pi.Thread); err != nil {
		return nil, err
	}
	mark("thread_resume_ms")
	for i, reader := range owner.readers {
		go func(r *os.File, dst io.Writer) {
			_, copyErr := io.Copy(dst, r)
			if copyErr != nil {
				io.Copy(io.Discard, r)
			} // Still drain a rejected frame.
			r.Close()
			owner.streams <- copyErr
		}(reader, outputs[i])
	}
	started = true
	return owner, nil
}
func (o *processOwner) Wait() error {
	_, err := windows.WaitForSingleObject(o.process, windows.INFINITE)
	var code uint32
	if e := windows.GetExitCodeProcess(o.process, &code); e != nil {
		err = errors.Join(err, e)
	}
	if code != 0 {
		err = errors.Join(err, fmt.Errorf("child exit %d", code))
	}
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	for range 2 {
		select {
		case e := <-o.streams:
			err = errors.Join(err, e)
		case <-timer.C:
			for _, r := range o.readers {
				r.Close()
			}
			// Spike ceiling: a downstream dst.Write that never returns can still
			// block this drain. P09 needs a bounded output queue and independent
			// lifecycle cancellation; this is not a slow-consumer safety proof.
			err = errors.Join(err, errors.New("child output EOF deadline exceeded"), <-o.streams)
		}
	}
	return err
}
func (o *processOwner) Stop() { windows.TerminateJobObject(o.job, 1) }
func (o *processOwner) Close() {
	if o.job != 0 {
		o.Stop()
		windows.CloseHandle(o.job)
		o.job = 0
	}
	if o.process != 0 {
		windows.CloseHandle(o.process)
		o.process = 0
	}
}
func physicalIdentity(root string) (string, error) {
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
func processAlive(pid int) (bool, error) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(h)
	state, err := windows.WaitForSingleObject(h, 0)
	return state == uint32(windows.WAIT_TIMEOUT), err
}
