package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
)

// Explicit opt-in consumes the packaged entrypoint, not this test executable.
// ConPTY owns only an in-memory terminal; no desktop window or keyboard API is used.
func TestPackagedConPTY(t *testing.T) {
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	t.Logf("packaged binary %s sha256=%s", binary, hex.EncodeToString(hash[:]))
	home, cwd := t.TempDir(), t.TempDir()
	t.Run("no_window_negative_control", func(t *testing.T) {
		p := startConPTY(t, binary, home, cwd, true)
		// The initial experiment observed a stuck child, not a trustworthy exit.
		// Bound this deliberately incompatible route and retain that wait status.
		state, err := windows.WaitForSingleObject(p.process, 250)
		p.waitStatus = &state
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(p.snapshot(), "Enter sends your prompt") {
			t.Fatal("CREATE_NO_WINDOW unexpectedly reached interactive prompt")
		}
		if state != uint32(windows.WAIT_TIMEOUT) && state != windows.WAIT_OBJECT_0 {
			t.Fatalf("unexpected negative-control wait status%d", state)
		}
	})
	if !t.Run("task_loop_and_resize", func(t *testing.T) {
		p := startConPTY(t, binary, home, cwd, false)
		p.await(t, 0, "Enter sends your prompt")
		mark := p.send(t, "/models\r")
		p.await(t, mark, "Choose a CLI")
		mark = p.send(t, "Local demo\r")
		p.await(t, mark, "Local demo")
		mark = p.send(t, "1\r")
		p.await(t, mark, "Choose a model")
		mark = p.send(t, "deep\r")
		p.await(t, mark, "demo/deep")
		mark = p.send(t, "1\r")
		p.await(t, mark, "Choose effort")
		mark = p.send(t, "1\r")
		p.await(t, mark, "Model selected")
		if err := windows.ResizePseudoConsole(p.console, windows.Coord{X: 60, Y: 20}); err != nil {
			t.Fatal(err)
		}
		mark = p.send(t, "ConPTY task one 한글 😀\r")
		p.await(t, mark, "Ready for your next prompt")
		mark = p.send(t, "/new\r")
		p.await(t, mark, "New task selected")
		mark = p.send(t, "/draft Second unsent draft\r")
		p.await(t, mark, "Saved draft:")
		mark = p.send(t, "/tasks\r")
		p.await(t, mark, "Open a task")
		mark = p.send(t, "ConPTY task one\r")
		p.await(t, mark, "ConPTY task one")
		mark = p.send(t, "1\r")
		p.await(t, mark, "Opened task")
		mark = p.send(t, "/result\r")
		p.await(t, mark, "Attempt ")
		p.send(t, "/exit\r")
		if code := p.wait(t); code != 0 {
			t.Fatalf("exit=%d", code)
		}
	}) {
		return
	}
	t.Run("restart_and_etx", func(t *testing.T) {
		p := startConPTY(t, binary, home, cwd, false)
		p.await(t, 0, "Restored your unsent message")
		p.await(t, 0, "Second unsent draft")
		p.send(t, "\x03")
		if code := p.wait(t); code != 130 {
			t.Fatalf("ETX exit=%d, want130", code)
		}
	})
	t.Run("restart_and_eof", func(t *testing.T) {
		p := startConPTY(t, binary, home, cwd, false)
		p.await(t, 0, "Restored your unsent message")
		p.await(t, 0, "Enter sends your prompt")
		// Ctrl+Z followed by Enter is the Windows console's cooked EOF path.
		p.send(t, "\x1a\r")
		if code := p.wait(t); code != 0 {
			t.Fatalf("EOF exit=%d, want0", code)
		}
	})
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	tasks, err := a.Tasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("terminal loop created unexpected tasks: %+v", tasks)
	}
	for _, task := range tasks {
		_, attempts, err := a.Task(context.Background(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Draft == "Second unsent draft" {
			if len(attempts) != 0 {
				t.Fatal("ETX or EOF submitted draft")
			}
			continue
		}
		if len(attempts) != 1 || attempts[0].Input != "ConPTY task one 한글 😀" || attempts[0].Requested.Model != "demo/deep" {
			t.Fatalf("task/menu mismatch: %+v", attempts)
		}
	}
}

// This separate home exercises the paged finder without changing the original
// two-task terminal fixture. It proves platform behavior, not owner usability.
func TestPackagedConPTYTaskFinder(t *testing.T) {
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	t.Logf("packaged binary %s sha256=%s", binary, hex.EncodeToString(hash[:]))
	home, cwd, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	pins := core.Defaults()
	pins.Model, pins.Effort = "demo/deep", "high"
	oldest, err := a.Draft(ctx, "", "Finder oldest draft", cwd, pins)
	if err != nil {
		t.Fatal(err)
	}
	pins.Model, pins.Effort = "demo/fast", "low"
	literal, err := a.Draft(ctx, "", "0 /older", elsewhere, pins)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 21 {
		_, err := a.Store.SaveDraft(ctx, core.Task{Title: fmt.Sprintf("Finder filler %02d", i), Draft: "Unsent filler", Workspace: literal.Workspace, Selection: core.Defaults()}, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	p := startConPTY(t, binary, home, cwd, false)
	p.await(t, 0, "Restored your unsent message")
	p.await(t, 0, "Finder oldest draft")
	p.await(t, 0, "Next: model demo/deep | effort high")
	mark := p.send(t, "/tasks\r")
	p.await(t, mark, "1. Finder filler 20")
	p.await(t, mark, "/older Older tasks")
	mark = p.send(t, "/older\r")
	p.await(t, mark, "3. Finder oldest draft")
	mark = p.send(t, "0\r")
	p.await(t, mark, "Task "+oldest.ID+" | draft | revision 1")
	p.await(t, mark, "Next: model demo/deep | effort high")
	mark = p.send(t, "/tasks\r")
	p.await(t, mark, "3. Finder oldest draft")
	// An external edit must not refresh the draft/CAS revision just because the
	// user browses or leaves the finder. The live list may move this task upward.
	changed, err := a.Draft(ctx, oldest.ID, "Newer original from another connection", cwd, oldest.Selection)
	if err != nil {
		t.Fatal(err)
	}
	mark = p.send(t, "0\r")
	p.await(t, mark, "Task "+oldest.ID+" | draft | revision 1")
	p.await(t, mark, "Finder oldest draft")
	mark = p.send(t, "/tasks\r")
	p.await(t, mark, "1. Finder filler 00")
	mark = p.send(t, "/search 0 /older\r")
	p.await(t, mark, "1. 0 /older | draft")
	mark = p.send(t, "1\r")
	p.await(t, mark, "Opened task in ")
	p.await(t, mark, "Task "+literal.ID+" | draft | revision 1")
	p.await(t, mark, "Next: model demo/fast | effort low")
	mark = p.send(t, "/draft Terminal saved addition\r")
	p.await(t, mark, "Task "+literal.ID+" | draft | revision 2")
	p.await(t, mark, "Enter sends your prompt")
	p.send(t, "\x03")
	if code := p.wait(t); code != 130 {
		t.Fatalf("finder ETX exit=%d, want130", code)
	}
	tasks, err := a.Tasks(ctx)
	if err != nil || len(tasks) != 23 {
		t.Fatalf("finder changed task count: count%d err%v", len(tasks), err)
	}
	for _, task := range tasks {
		_, attempts, err := a.Task(ctx, task.ID)
		if err != nil || len(attempts) != 0 {
			t.Fatalf("finder/ETX submitted task %s: %+v %v", task.ID, attempts, err)
		}
		switch task.ID {
		case oldest.ID:
			if task.Draft != changed.Draft || task.Revision != changed.Revision || task.Selection != oldest.Selection {
				t.Fatalf("finder overwrote external edit or pins: %+v", task)
			}
		case literal.ID:
			if task.Draft != "Terminal saved addition" || task.Revision != literal.Revision+1 || task.Selection != literal.Selection || task.Workspace != literal.Workspace {
				t.Fatalf("explicit choice lost draft identity/pins: %+v", task)
			}
		default:
			if task.Revision != 1 || task.Draft != "Unsent filler" {
				t.Fatalf("finder changed an unselected task: %+v", task)
			}
		}
	}
	t.Log("23 fixture tasks retained; oldest draft restored; page/search/context checked; ETX130; zero attempts")
}

// This route stops at provider discovery: no provider is selected, no credential
// store is inspected, and no login or task is dispatched. It exercises actual
// terminal composition continuity and the explicit full-access choice only.
func TestPackagedConPTYConnectionDiscoveryAndSandbox(t *testing.T) {
	requireVisibleQA(t)
	t.Setenv("TERM", "xterm-256color")
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	selection := core.Selection{Model: "claude/fixture-model", Account: "synthetic-native-account", Effort: "medium", Permission: "read-only"}
	seed, err := a.Draft(ctx, "", "Saved account discovery draft", cwd, selection)
	if err != nil {
		t.Fatal(err)
	}
	p := startNativeConPTYOptions(t, binary, home, cwd, false)
	p.awaitVisible(t, 0, seed.Draft)
	const composition = "Unsent composition stays here"
	mark := p.send(t, "\x1b[200~\n"+composition+"\x1b[201~")
	p.awaitVisible(t, mark, composition)
	mark = p.send(t, "\t")
	p.awaitVisible(t, mark, "Actions")
	mark = p.send(t, "Connect account")
	p.awaitVisible(t, mark, "Connect account")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Connect an account")
	p.awaitVisible(t, mark, "Codex")
	p.awaitVisible(t, mark, "Claude")
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, composition)
	unchanged, attempts, err := a.Task(ctx, seed.ID)
	if err != nil || len(attempts) != 0 || unchanged.Draft != seed.Draft || unchanged.Revision != seed.Revision || unchanged.Selection != selection || unchanged.Workspace != seed.Workspace {
		t.Fatalf("account discovery/cancel changed draft context: %+v %+v %v", unchanged, attempts, err)
	}
	mark = p.send(t, "\t")
	p.awaitVisible(t, mark, "Actions")
	mark = p.send(t, "Permission")
	p.awaitVisible(t, mark, "Permission")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Choose permission")
	mark = p.send(t, "sandbox")
	p.awaitVisible(t, mark, "full local computer access")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Permission selected: sandbox")
	p.awaitVisible(t, mark, "prompt kept; Enter sends")
	p.awaitVisible(t, mark, composition)
	// Intentional EOF saves the partial composition without submitting it.
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("discovery/permission EOF exit=%d", code)
	}
	wantSelection := selection
	wantSelection.Permission = "sandbox"
	got, attempts, err := a.Task(ctx, seed.ID)
	if err != nil || len(attempts) != 0 || got.Draft != seed.Draft+"\n"+composition || got.Revision != seed.Revision+2 || got.Selection != wantSelection || got.Workspace != seed.Workspace {
		t.Fatalf("permission or EOF lost draft/pins: %+v %+v %v", got, attempts, err)
	}
	if _, err := os.Stat(filepath.Join(home, "connections-v1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("discovery chooser created connection state: %v", err)
	}
	t.Log("Account chooser canceled; saved and unsent draft retained; sandbox selected explicitly; zero attempts or native connections")
}

type pseudoTerminal struct {
	console, job, process windows.Handle
	input, output         *os.File
	mu                    sync.Mutex
	raw                   bytes.Buffer
	changed, drained      chan struct{}
	truncated             bool
	exit                  *uint32
	creationFlags         uint32
	waitStatus            *uint32
	forcedStop            bool
	terminationConfirmed  bool
	started               time.Time
	sent                  map[int]time.Time
	observations          []map[string]any
	resizes               []shellResize
	visibleSequence       int
}

func startConPTY(t *testing.T, binary, home, cwd string, noWindow bool) *pseudoTerminal {
	return startConPTYOptions(t, binary, home, cwd, noWindow, "--plain")
}

func startConPTYOptions(t *testing.T, binary, home, cwd string, noWindow bool, options ...string) *pseudoTerminal {
	t.Helper()
	return startConPTYMode(t, binary, home, cwd, noWindow, true, options...)
}

// Native mode is only for inspect/selection fixtures with an isolated UCLI home
// and no linked accounts. These journeys never submit a native task or login.
func startNativeConPTYOptions(t *testing.T, binary, home, cwd string, noWindow bool, options ...string) *pseudoTerminal {
	t.Helper()
	return startConPTYMode(t, binary, home, cwd, noWindow, false, options...)
}

func startConPTYMode(t *testing.T, binary, home, cwd string, noWindow, demo bool, options ...string) *pseudoTerminal {
	t.Helper()
	return startConPTYInvocation(t, binary, home, cwd, noWindow, demo, true, options...)
}

// Offline commands use the real process directory without an interactive --cwd
// flag. Keep their command-line contract separate from chat fixtures.
func startConPTYInvocation(t *testing.T, binary, home, cwd string, noWindow, demo, passCWD bool, options ...string) *pseudoTerminal {
	t.Helper()
	p := &pseudoTerminal{changed: make(chan struct{}, 1), drained: make(chan struct{}), sent: make(map[int]time.Time)}
	t.Cleanup(func() { p.close(t) })
	var inputRead, inputWrite, outputRead, outputWrite windows.Handle
	if err := windows.CreatePipe(&inputRead, &inputWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(inputRead)
	p.input = os.NewFile(uintptr(inputWrite), "conpty-input")
	if err := windows.CreatePipe(&outputRead, &outputWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(outputWrite)
	p.output = os.NewFile(uintptr(outputRead), "conpty-output")
	// Drain independently before creation and through teardown; ClosePseudoConsole
	// can emit a final frame and older Windows versions block if it is not read.
	go func() {
		defer close(p.drained)
		buffer := make([]byte, 8192)
		for {
			n, err := p.output.Read(buffer)
			if n > 0 {
				p.mu.Lock()
				if p.raw.Len()+n <= 2<<20 {
					p.raw.Write(buffer[:n])
				} else {
					p.truncated = true
				}
				p.mu.Unlock()
				select {
				case p.changed <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	if err := windows.CreatePseudoConsole(windows.Coord{X: 80, Y: 24}, inputRead, outputWrite, 0, &p.console); err != nil {
		t.Fatalf("CreatePseudoConsole: %v", err)
	}
	var err error
	p.job, err = windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(p.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatal(err)
	}
	defer attributes.Delete()
	// This attribute takes HPCON by value, unlike attributes taking a pointer to
	// a structure. Keep the OS handle as uintptr rather than inventing a Go pointer.
	update := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
	ok, _, callErr := update.Call(uintptr(unsafe.Pointer(attributes.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(p.console), unsafe.Sizeof(p.console), 0, 0)
	if ok == 0 {
		t.Fatalf("pseudoconsole process attribute: %v", callErr)
	}
	// Explicit NULL standard handles prevent Windows from duplicating the test
	// harness's redirected handles instead of initializing ConPTY console handles.
	// See microsoft/terminal discussion 15814 (maintainer diagnosis).
	si := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESHOWWINDOW | windows.STARTF_USESTDHANDLES, ShowWindow: windows.SW_HIDE}, ProcThreadAttributeList: attributes.List()}
	exe, err := windows.UTF16PtrFromString(binary)
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{binary, "--home", home}
	if passCWD {
		arguments = append(arguments, "--cwd", cwd)
	}
	if demo {
		arguments = append(arguments, "--demo")
	}
	args, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append(arguments, options...)))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := windows.UTF16PtrFromString(cwd)
	if err != nil {
		t.Fatal(err)
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.EXTENDED_STARTUPINFO_PRESENT)
	// CREATE_NO_WINDOW suppresses console handles; ConPTY supplies its own hidden
	// virtual console instead. Retain that flag only as the failing control route.
	if noWindow {
		flags |= windows.CREATE_NO_WINDOW
	}
	p.creationFlags = flags
	if err = windows.CreateProcess(exe, args, nil, nil, false, flags, nil, dir, &si.StartupInfo, &pi); err != nil {
		t.Fatalf("CreateProcess: %v", err)
	}
	p.process = pi.Process
	defer windows.CloseHandle(pi.Thread)
	if err = windows.AssignProcessToJobObject(p.job, p.process); err != nil {
		_ = windows.TerminateProcess(p.process, 1)
		t.Fatal(err)
	}
	p.started = time.Now()
	if _, err = windows.ResumeThread(pi.Thread); err != nil {
		t.Fatal(err)
	}
	return p
}

var terminalCSI = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
var terminalOSC = regexp.MustCompile("\x1b\\][^\a]*(?:\a|\x1b\\\\)")

func (p *pseudoTerminal) snapshot() string { p.mu.Lock(); defer p.mu.Unlock(); return p.raw.String() }

func requireVisibleQA(t *testing.T) {
	t.Helper()
	if os.Getenv("UCLI_TERMINAL_QA_DEPS") == "" {
		t.Skip("set UCLI_TERMINAL_QA_DEPS for optional packaged screen verification")
	}
}

// Rich terminals repaint only changed cells. Match the reconstructed visible
// screen after new output, not a contiguous substring of differential bytes.
// Keep this separate from await: plain output and timing checks need no emulator.
func (p *pseudoTerminal) awaitVisible(t *testing.T, mark int, needle string) {
	t.Helper()
	p.visibleSequence++
	label := fmt.Sprintf("visible-%d-%d", p.started.UnixNano(), p.visibleSequence)
	awaitShellFrame(t, p, p.resizes, label, func(f shellCapturedFrame) bool {
		return f.RawBytes > mark && strings.Contains(strings.Join(f.Rows, "\n"), needle)
	})
}

func (p *pseudoTerminal) resize(t *testing.T, width, height int) int {
	t.Helper()
	mark := len(p.snapshot())
	if err := windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(width), Y: int16(height)}); err != nil {
		t.Fatal(err)
	}
	p.resizes = append(p.resizes, shellResize{Offset: mark, Width: width, Height: height})
	return mark
}

func (p *pseudoTerminal) await(t *testing.T, mark int, needle string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		text := p.snapshot()
		if mark > len(text) {
			t.Fatal("invalid transcript mark")
		}
		plain := terminalCSI.ReplaceAllString(terminalOSC.ReplaceAllString(text[mark:], ""), "")
		if strings.Contains(plain, needle) {
			start := p.started
			if sent, ok := p.sent[mark]; ok {
				start = sent
			}
			p.observations = append(p.observations, map[string]any{"needle": needle, "from_offset": mark, "observed_offset": len(text), "elapsed_ms": float64(time.Since(start).Microseconds()) / 1000, "scope": "time to matching output bytes; not physical-screen paint or human latency"})
			return
		}
		select {
		case <-p.changed:
		case <-p.drained:
			t.Fatalf("terminal closed before %q: %q", needle, plain)
		case <-timer.C:
			t.Fatalf("terminal did not show %q: %q", needle, plain)
		}
	}
}
func (p *pseudoTerminal) send(t *testing.T, text string) int {
	t.Helper()
	mark := len(p.snapshot())
	p.sent[mark] = time.Now()
	done := make(chan error, 1)
	go func() { _, err := p.input.WriteString(text); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pseudoconsole input write timed out")
	}
	return mark
}
func (p *pseudoTerminal) wait(t *testing.T) uint32 {
	t.Helper()
	state, err := windows.WaitForSingleObject(p.process, 5000)
	p.waitStatus = &state
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("terminal child wait: state=%d err=%v", state, err)
	}
	var code uint32
	if err = windows.GetExitCodeProcess(p.process, &code); err != nil {
		t.Fatal(err)
	}
	p.exit = &code
	return code
}
func (p *pseudoTerminal) close(t *testing.T) {
	if p.process != 0 {
		state, _ := windows.WaitForSingleObject(p.process, 0)
		if state != windows.WAIT_OBJECT_0 {
			p.forcedStop = true
			if err := windows.TerminateJobObject(p.job, 1); err != nil {
				t.Error(err)
			}
			wait, err := windows.WaitForSingleObject(p.process, 1000)
			p.terminationConfirmed = err == nil && wait == windows.WAIT_OBJECT_0
			if !p.terminationConfirmed {
				t.Errorf("owned terminal child termination unconfirmed: wait=%d err=%v", wait, err)
			}
		} else if p.exit == nil {
			var code uint32
			if windows.GetExitCodeProcess(p.process, &code) == nil {
				p.exit = &code
			}
		}
		if state == windows.WAIT_OBJECT_0 {
			p.terminationConfirmed = true
		}
		_ = windows.CloseHandle(p.process)
	}
	if p.input != nil {
		_ = p.input.Close()
	}
	if p.console != 0 {
		done := make(chan struct{})
		go func() { windows.ClosePseudoConsole(p.console); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("ClosePseudoConsole timeout")
		}
	}
	if p.job != 0 {
		_ = windows.CloseHandle(p.job)
	}
	if p.output != nil {
		select {
		case <-p.drained:
		case <-time.After(time.Second):
			_ = p.output.Close()
			select {
			case <-p.drained:
			case <-time.After(time.Second):
				t.Error("terminal drain did not stop")
			}
		}
		_ = p.output.Close()
	}
	text := p.snapshot()
	p.mu.Lock()
	truncated := p.truncated
	p.mu.Unlock()
	if truncated {
		t.Error("terminal transcript exceeded 2 MiB; continued draining")
	}
	if evidence := os.Getenv("UCLI_CONPTY_EVIDENCE"); evidence != "" {
		name := strings.ReplaceAll(t.Name(), "/", "-")
		if err := os.MkdirAll(evidence, 0700); err != nil {
			t.Error(err)
			return
		}
		// A test can own more than one console. Retain every child transcript,
		// including restart/EOF proof, instead of overwriting at cleanup.
		base := name
		var raw *os.File
		for index := 1; ; index++ {
			if index > 1 {
				name = fmt.Sprintf("%s-%d", base, index)
			}
			var err error
			raw, err = os.OpenFile(filepath.Join(evidence, name+".raw.txt"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if errors.Is(err, os.ErrExist) {
				continue
			}
			if err != nil {
				t.Error(err)
				return
			}
			break
		}
		if _, err := raw.WriteString(text); err != nil {
			t.Error(err)
		}
		if err := raw.Close(); err != nil {
			t.Error(err)
		}
		b, _ := json.MarshalIndent(map[string]any{"test": t.Name(), "exit_code": p.exit, "wait_status": p.waitStatus, "forced_stop": p.forcedStop, "termination_confirmed": p.terminationConfirmed, "creation_flags": p.creationFlags, "failed": t.Failed(), "truncated": truncated, "terminal": "Windows ConPTY; hidden, no desktop surface", "transcript_bytes": len(text), "observations": p.observations}, "", "  ")
		if err := os.WriteFile(filepath.Join(evidence, name+".json"), append(b, '\n'), 0600); err != nil {
			t.Error(err)
		}
	}
	code := "unconfirmed"
	if p.exit != nil {
		code = fmt.Sprint(*p.exit)
	}
	t.Log(fmt.Sprintf("captured %d ConPTY bytes; exit=%s", len(text), code))
}
