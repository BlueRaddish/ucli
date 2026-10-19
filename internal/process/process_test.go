package process

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const helperMarker = "__ucli_process_helper__"

// This test executable is the actual child, with no providers or credentials.
func TestProcessHelper(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == helperMarker {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	args := os.Args[index+1:]
	switch args[0] {
	case "report":
		cwd, _ := os.Getwd()
		json.NewEncoder(os.Stdout).Encode(map[string]any{"cwd": cwd, "env": os.Environ(), "args": args[1:]})
		fmt.Fprint(os.Stderr, "diagnostic marker")
		os.Exit(7)
	case "echo":
		scan := bufio.NewScanner(os.Stdin)
		for scan.Scan() {
			if scan.Text() == "quit" {
				os.Exit(0)
			}
			fmt.Fprintln(os.Stdout, scan.Text())
		}
	case "flood":
		data := bytes.Repeat([]byte("x"), 4096)
		for i := 0; i < 1024*16; i++ {
			if _, err := os.Stdout.Write(data); err != nil {
				break
			}
		}
	case "stall":
		fmt.Fprintln(os.Stdout, "ready")
		time.Sleep(30 * time.Second)
	case "descendant":
		exe, _ := os.Executable()
		cmd := exec.Command(exe, "-test.run=^TestProcessHelper$", "--", helperMarker, "leaf", args[1])
		cmd.Env = []string{}
		hideTestChild(cmd)
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, cmd.Process.Pid)
		time.Sleep(30 * time.Second)
	case "leaf":
		for i := 0; i < 1500; i++ {
			if _, err := os.Stat(filepath.Join(args[1], "stop")); err == nil {
				break
			}
			if err := os.WriteFile(filepath.Join(args[1], "heartbeat"), []byte(strconv.Itoa(i)), 0600); err != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	default:
		os.Exit(3)
	}
	os.Exit(0)
}

func helperSpec(t *testing.T, mode string, args ...string) Spec {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("macOS owned process launch is explicitly unsupported pending a verified non-reaping observer")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Spec{Executable: exe, Args: append([]string{"-test.run=^TestProcessHelper$", "--", helperMarker, mode}, args...), Env: []string{}, Dir: t.TempDir()}
}
func waitBounded(t *testing.T, c *Child) (Exit, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	exit, err := c.Wait(ctx)
	if ctx.Err() != nil {
		t.Fatalf("lifecycle exceeded deadline: %+v %v", exit, err)
	}
	return exit, err
}
func readLine(t *testing.T, c *Child) string {
	t.Helper()
	var data []byte
	deadline := time.After(4 * time.Second)
	for {
		select {
		case chunk, ok := <-c.Events():
			if !ok {
				t.Fatal("events ended before fixture ready")
			}
			if chunk.Stream == "stdout" {
				data = append(data, chunk.Data...)
				if bytes.Contains(data, []byte{'\n'}) {
					return strings.TrimSpace(string(data))
				}
			}
		case <-deadline:
			t.Fatal("fixture did not become ready")
		}
	}
}

func TestCloseInputAllowsNaturalExitWithoutStoppingOutput(t *testing.T) {
	c, err := Start(context.Background(), helperSpec(t, "echo"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Send(context.Background(), []byte("final line\n")); err != nil {
		t.Fatal(err)
	}
	if err = c.CloseInput(); err != nil {
		t.Fatal(err)
	}
	if err = c.CloseInput(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(c.Send(context.Background(), []byte("late\n")), ErrClosed) {
		t.Fatal("write after EOF was accepted")
	}
	var output bytes.Buffer
	for event := range c.Events() {
		if event.Stream == "stdout" {
			output.Write(event.Data)
		}
	}
	exit, err := waitBounded(t, c)
	if err != nil || exit.Code != 0 || exit.Stopped || output.String() != "final line\n" {
		t.Fatalf("EOF did not preserve natural exit/output: %+v %v %q", exit, err, output.String())
	}
}

func TestExplicitEnvironmentDirectoryArgumentsAndCachedWait(t *testing.T) {
	t.Setenv("UCLI_AMBIENT_TEST", "must-not-copy")
	s := helperSpec(t, "report", "space value", "ü 한글", `literal"quote`, `tail\`, "")
	s.Dir = filepath.Join(s.Dir, "cwd space ü")
	if err := os.Mkdir(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	s.Env = []string{"ONLY_MARKER=fixture value"}
	c, err := Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exit, err := waitBounded(t, c)
	if err != nil || exit.Code != 7 || exit.Stopped {
		t.Fatalf("exit %+v %v", exit, err)
	}
	var out, diagnostic bytes.Buffer
	for chunk := range c.Events() {
		if chunk.Stream == "stdout" {
			out.Write(chunk.Data)
		} else {
			diagnostic.Write(chunk.Data)
		}
	}
	var report struct {
		CWD  string   `json:"cwd"`
		Env  []string `json:"env"`
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err, out.String())
	}
	want, _ := filepath.EvalSymlinks(s.Dir)
	got, _ := filepath.EvalSymlinks(report.CWD)
	if got != want {
		t.Fatalf("cwd %q != %q", got, want)
	}
	if strings.Join(report.Env, "|") != "ONLY_MARKER=fixture value" {
		t.Fatalf("ambient environment leaked: %v", report.Env)
	}
	if fmt.Sprint(report.Args) != fmt.Sprint(s.Args[4:]) || diagnostic.String() != "diagnostic marker" {
		t.Fatalf("transport mismatch: %+v %q", report.Args, diagnostic.String())
	}
	for range 3 {
		again, e := c.Wait(context.Background())
		if again != exit || e != nil {
			t.Fatalf("wait not cached: %+v %v", again, e)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if again, err := c.Wait(canceled); again != exit || err != nil {
		t.Fatalf("caller deadline replaced terminal result: %+v %v", again, err)
	}
}

func TestPersistentStdinAndRawChunks(t *testing.T) {
	s := helperSpec(t, "echo")
	s.FrameBytes = 8
	s.QueueBytes = 128
	c, err := Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, line := range []string{"ü\n", "two\n", "quit\n"} {
		if err := c.Send(context.Background(), []byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	exit, err := waitBounded(t, c)
	if err != nil || exit.Code != 0 {
		t.Fatalf("%+v %v", exit, err)
	}
	var all []byte
	for chunk := range c.Events() {
		if len(chunk.Data) > s.FrameBytes {
			t.Fatal("oversized output chunk")
		}
		all = append(all, chunk.Data...)
	}
	if string(all) != "ü\ntwo\n" {
		t.Fatalf("lost bytes %q", all)
	}
	if err := c.Send(context.Background(), []byte("later")); !errors.Is(err, ErrClosed) {
		t.Fatalf("send after exit: %v", err)
	}
}

func TestUnreadConsumerCannotBlockLifecycle(t *testing.T) {
	s := helperSpec(t, "flood")
	s.QueueBytes = 8192
	s.FrameBytes = 4096
	c, err := Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exit, err := waitBounded(t, c) // Deliberately never consume Events before exit.
	if !errors.Is(err, ErrBackpressure) || !exit.Stopped {
		t.Fatalf("backpressure was hidden: %+v %v", exit, err)
	}
	retained := 0
	for chunk := range c.Events() {
		retained += len(chunk.Data)
	}
	if retained > s.QueueBytes {
		t.Fatalf("queue retained %d > %d", retained, s.QueueBytes)
	}
}

func TestStalledStdinStopsOnWriteDeadline(t *testing.T) {
	s := helperSpec(t, "stall")
	s.FrameBytes = 1024 * 1024
	c, err := Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if readLine(t, c) != "ready" {
		t.Fatal("bad fixture readiness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = c.Send(ctx, bytes.Repeat([]byte("x"), s.FrameBytes))
	if !errors.Is(err, ErrInputBlocked) || time.Since(start) > 2*time.Second {
		t.Fatalf("write not bounded: %v %s", err, time.Since(start))
	}
	exit, err := waitBounded(t, c)
	if !exit.Stopped || !errors.Is(err, ErrInputBlocked) {
		t.Fatalf("write failure lost: %+v %v", exit, err)
	}
}

func TestWaitDeadlineDoesNotStopChild(t *testing.T) {
	c, err := Start(context.Background(), helperSpec(t, "stall"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	readLine(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	exit, err := c.Wait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || exit.Stopped {
		t.Fatalf("wait changed lifetime: %+v %v", exit, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	exit, err = waitBounded(t, c)
	if err != nil || !exit.Stopped {
		t.Fatalf("close: %+v %v", exit, err)
	}
}

func TestCancellationOwnsDescendant(t *testing.T) {
	root := t.TempDir()
	defer os.WriteFile(filepath.Join(root, "stop"), []byte("cleanup"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := Start(ctx, helperSpec(t, "descendant", root))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pid, err := strconv.Atoi(readLine(t, c))
	if err != nil {
		t.Fatal(err)
	}
	if yes, err := Alive(pid); err != nil || !yes {
		t.Fatalf("descendant was not alive: %v %v", yes, err)
	}
	cancel()
	exit, err := waitBounded(t, c)
	if !errors.Is(err, context.Canceled) || !exit.Stopped {
		t.Fatalf("cancel: %+v %v", exit, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		yes, err := Alive(pid)
		if err != nil {
			t.Fatal(err)
		}
		if !yes {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned descendant remains alive")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRejectMalformedSpecAndInput(t *testing.T) {
	valid := helperSpec(t, "echo")
	for name, change := range map[string]func(*Spec){
		"ambient": func(s *Spec) { s.Env = nil }, "relative-exe": func(s *Spec) { s.Executable = "go" },
		"relative-dir": func(s *Spec) { s.Dir = "." }, "bad-env": func(s *Spec) { s.Env = []string{"MISSING"} },
		"duplicate-env": func(s *Spec) { s.Env = []string{"A=1", "A=2"} }, "nul": func(s *Spec) { s.Args = []string{"x\x00"} },
		"negative": func(s *Spec) { s.QueueBytes = -1 }, "queue-small": func(s *Spec) { s.QueueBytes = 1 },
		"frame-huge": func(s *Spec) { s.FrameBytes = 2 * 1024 * 1024 },
	} {
		t.Run(name, func(t *testing.T) {
			s := valid
			change(&s)
			if c, err := Start(context.Background(), s); err == nil {
				c.Close()
				t.Fatal("invalid spec accepted")
			}
		})
	}
	c, err := Start(context.Background(), valid)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Send(context.Background(), make([]byte, defaultFrame+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatal(err)
	}
	for _, pid := range []int{0, -1, int(int64(1)<<32 + 1)} {
		if _, err := Alive(pid); err == nil {
			t.Fatal("invalid pid accepted")
		}
	}
}

type failedCleanup struct{ failure error }

func (p failedCleanup) wait() (int, error) { return 0, nil }
func (p failedCleanup) stop() error        { return p.failure }
func (p failedCleanup) close() error       { return p.failure }
func TestCleanupErrorsRemainObservable(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	want := errors.New("injected OS cleanup failure")
	c := &Child{owner: failedCleanup{want}, input: w, done: make(chan struct{}), exit: Exit{Code: -1}}
	if c.Settled() {
		t.Fatal("unreaped child reported settlement")
	}
	drained := make(chan struct{})
	close(drained)
	go c.reap(drained)
	if _, err := c.Wait(context.Background()); !errors.Is(err, want) {
		t.Fatalf("lost cleanup error: %v", err)
	}
	if err := c.Close(); !errors.Is(err, want) {
		t.Fatalf("Close hid cleanup error: %v", err)
	}
	if !c.Settled() {
		t.Fatal("cached terminal outcome missing settlement")
	}
}
