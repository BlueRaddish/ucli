package cli

import (
	"context"
	"os"
	"testing"

	"golang.org/x/sys/windows"
	"ucli.local/ucli/internal/app"
)

// The real packaged UI uses only its explicit local demo executor. It proves
// input/receipt continuity, not provider behavior or human usability.
func TestPackagedConPTYEnterSendAndInterruptGuard(t *testing.T) {
	requireVisibleQA(t)
	binary := os.Getenv("UCLI_CONPTY_BINARY")
	if binary == "" {
		t.Skip("set UCLI_CONPTY_BINARY to a freshly built Windows UCLI executable")
	}
	t.Setenv("TERM", "xterm-256color")
	home, cwd := t.TempDir(), t.TempDir()
	a, err := app.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	p := startConPTYOptions(t, binary, home, cwd, false, "--no-motion", "--ascii")
	p.awaitVisible(t, 0, "? shortcuts")
	mark := p.send(t, "One Enter creates one attempt\r")
	p.awaitVisible(t, mark, "Ready for your next prompt")
	tasks, err := a.Tasks(ctx)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("first Enter did not create one task: %+v %v", tasks, err)
	}
	id := tasks[0].ID
	assertAttempts := func(want ...string) {
		t.Helper()
		task, attempts, err := a.Task(ctx, id)
		if err != nil || len(attempts) != len(want) || task.Draft != "" {
			t.Fatalf("wrong durable result: %+v %+v %v", task, attempts, err)
		}
		for i, input := range want {
			if attempts[i].State != "complete" || attempts[i].Input != input {
				t.Fatalf("attempt %d changed input or did not complete: %+v", i, attempts[i])
			}
		}
	}
	assertAttempts("One Enter creates one attempt")
	// Empty Enter is harmless; the next visible edit establishes ordering.
	p.send(t, "\r")
	const multiline = "/send is literal\nSecond pasted line"
	mark = p.send(t, "\x1b[200~"+multiline+"\x1b[201~")
	p.awaitVisible(t, mark, "Second pasted line")
	assertAttempts("One Enter creates one attempt")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Ready for your next prompt")
	assertAttempts("One Enter creates one attempt", multiline)

	// Inspecting another view keeps the unsent editor as well as its saved task.
	const partial = "Keep this message through accidental copy"
	mark = p.send(t, partial)
	p.awaitVisible(t, mark, partial)
	mark = p.send(t, "\t")
	p.awaitVisible(t, mark, "Actions")
	mark = p.send(t, "Sessions")
	p.awaitVisible(t, mark, "Search: Sessions")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Sessions")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "State: Complete")
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "Sessions")
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "? shortcuts")
	p.awaitVisible(t, mark, partial)
	assertAttempts("One Enter creates one attempt", multiline)

	mark = p.send(t, "\x03")
	// Use raw incremental feedback only for this short confirmation window;
	// screen reconstruction can itself take longer than the two-second window.
	p.await(t, mark, "Press Ctrl+C again")
	if state, err := windows.WaitForSingleObject(p.process, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("first Ctrl+C closed the UI: state=%d err=%v", state, err)
	}
	const edit = " plus edit"
	mark = p.send(t, edit)
	p.awaitVisible(t, mark, partial+edit)
	assertAttempts("One Enter creates one attempt", multiline)
	mark = p.send(t, "\x03")
	p.await(t, mark, "Press Ctrl+C again")
	if state, err := windows.WaitForSingleObject(p.process, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("typing did not disarm prior confirmation: state=%d err=%v", state, err)
	}
	p.send(t, "\x03")
	if code := p.wait(t); code != 130 {
		t.Fatalf("confirmed Ctrl+C exit=%d", code)
	}
	stored, attempts, err := a.Task(ctx, id)
	if err != nil || stored.Draft != partial+edit || len(attempts) != 2 {
		t.Fatalf("confirmed interruption lost or submitted composition: %+v %+v %v", stored, attempts, err)
	}
	revision := stored.Revision
	p = startConPTYOptions(t, binary, home, cwd, false, "--no-motion", "--ascii")
	p.awaitVisible(t, 0, partial+edit)
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("restored message EOF exit=%d", code)
	}
	stored, attempts, err = a.Task(ctx, id)
	if err != nil || stored.Draft != partial+edit || stored.Revision != revision || len(attempts) != 2 {
		t.Fatalf("restoring/exiting rewrote or dispatched input: %+v %+v %v", stored, attempts, err)
	}
}
