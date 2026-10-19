//go:build windows

package process

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcess struct{ job, process windows.Handle }

func startNative(s Spec, in, out, diagnostic *os.File) (_ nativeProcess, resultErr error) {
	p := &windowsProcess{}
	var err error
	if p.job, err = windows.CreateJobObject(nil, nil); err != nil {
		return nil, err
	}
	started, assigned := false, false
	defer func() {
		if !started {
			if p.process != 0 {
				if assigned {
					resultErr = errors.Join(resultErr, p.stop())
				} else {
					resultErr = errors.Join(resultErr, windows.TerminateProcess(p.process, 1))
				}
				state, waitErr := windows.WaitForSingleObject(p.process, 1000)
				resultErr = errors.Join(resultErr, waitErr)
				if state == uint32(windows.WAIT_TIMEOUT) {
					resultErr = errors.Join(resultErr, errors.New("failed-start child termination unconfirmed"))
				}
			}
			resultErr = errors.Join(resultErr, p.close())
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(p.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, err
	}
	handles := []windows.Handle{windows.Handle(in.Fd()), windows.Handle(out.Fd()), windows.Handle(diagnostic.Fd())}
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
	exe, err := windows.UTF16PtrFromString(s.Executable)
	if err != nil {
		return nil, err
	}
	argv, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{s.Executable}, s.Args...)))
	if err != nil {
		return nil, err
	}
	dir, err := windows.UTF16PtrFromString(s.Dir)
	if err != nil {
		return nil, err
	}
	// A nonnil double-NUL block means empty environment; nil would inherit ambient
	// credentials. Windows expects case-insensitively sorted environment entries.
	sort.Slice(s.Env, func(i, j int) bool { return strings.ToUpper(s.Env[i]) < strings.ToUpper(s.Env[j]) })
	environment := utf16.Encode([]rune(strings.Join(s.Env, "\x00") + "\x00\x00"))
	var pi windows.ProcessInformation
	if err = windows.CreateProcess(exe, argv, nil, nil, true,
		windows.CREATE_NO_WINDOW|windows.CREATE_SUSPENDED|windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		&environment[0], dir, &si.StartupInfo, &pi); err != nil {
		return nil, err
	}
	p.process = pi.Process
	thread := pi.Thread
	defer func() {
		if thread != 0 {
			resultErr = errors.Join(resultErr, windows.CloseHandle(thread))
		}
	}()
	if err = windows.AssignProcessToJobObject(p.job, p.process); err != nil {
		return nil, err
	}
	assigned = true
	// No primary-thread scan and no child code can run before job assignment.
	if _, err = windows.ResumeThread(thread); err != nil {
		return nil, err
	}
	if err = windows.CloseHandle(thread); err != nil {
		return nil, err
	}
	thread = 0
	started = true
	return p, nil
}
func (p *windowsProcess) wait() (int, error) {
	state, err := windows.WaitForSingleObject(p.process, windows.INFINITE)
	if err != nil {
		return -1, err
	}
	if state != windows.WAIT_OBJECT_0 {
		return -1, fmt.Errorf("unexpected process wait status %d", state)
	}
	var code uint32
	if err = windows.GetExitCodeProcess(p.process, &code); err != nil {
		return -1, err
	}
	return int(code), nil
}
func (p *windowsProcess) stop() error {
	if err := windows.TerminateJobObject(p.job, 1); err != nil {
		return fmt.Errorf("terminate owned job: %w", err)
	}
	return nil
}
func (p *windowsProcess) close() error {
	var err error
	if p.job != 0 {
		err = errors.Join(err, windows.CloseHandle(p.job))
		p.job = 0
	}
	if p.process != 0 {
		err = errors.Join(err, windows.CloseHandle(p.process))
		p.process = 0
	}
	return err
}
func alive(pid int) (bool, error) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(h)
	state, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return false, err
	}
	if state == uint32(windows.WAIT_TIMEOUT) {
		return true, nil
	}
	if state == windows.WAIT_OBJECT_0 {
		return false, nil
	}
	return false, fmt.Errorf("unexpected liveness wait status %d", state)
}
