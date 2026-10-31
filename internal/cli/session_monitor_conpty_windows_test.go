package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"ucli.local/ucli/internal/app"
	"ucli.local/ucli/internal/core"
	"ucli.local/ucli/internal/workspace"
)

// The saved running attempt is synthetic: no executor or native agent is started.
// Raw-byte deadlines isolate page catch-up from the slower screen QA helper.
func TestPackagedConPTYSessionMonitorBurstAndRefresh(t *testing.T) {
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
	w, err := workspace.Resolve(cwd)
	if err != nil {
		t.Fatal(err)
	}
	owner := core.Owner{ID: "monitor-fixture-owner", PID: os.Getpid(), StartedAt: time.Now().UTC()}
	cmd := core.RunCommand{ID: "monitor-fixture-command", Input: "Burst monitor fixture", Workspace: w, Owner: owner,
		Requested: core.Defaults(), Effective: core.Route{Runtime: "demo", Provider: "local", Model: "demo/fast", Account: "demo",
			Effort: "low", Permission: "read-only", Mode: "demo"}}
	attempt, fresh, err := a.Store.Accept(ctx, cmd)
	if err != nil || !fresh {
		t.Fatalf("seed running attempt: fresh=%v err=%v", fresh, err)
	}
	appendEvent := func(kind, text string) {
		t.Helper()
		if _, err := a.Store.AppendEvent(ctx, core.Event{TaskID: attempt.TaskID, AttemptID: attempt.ID, Type: kind, Text: text}, owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 250; i++ {
		appendEvent("status", "Synthetic saved progress")
	}
	const burst = "MONITOR_BURST_EDGE"
	appendEvent("message_delta", burst)
	const draft = "Keep this unsent monitor draft"
	seed, err := a.Draft(ctx, "", draft, cwd, core.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	p := startConPTYOptions(t, binary, home, cwd, false, "--no-motion", "--ascii")
	p.awaitVisible(t, 0, draft)
	mark := p.send(t, "\t")
	p.awaitVisible(t, mark, "Actions")
	mark = p.send(t, "Sessions")
	p.awaitVisible(t, mark, "Search: Sessions")
	mark = p.send(t, "\r")
	p.awaitVisible(t, mark, "Sessions")
	mark = p.send(t, "Burst monitor")
	p.awaitVisible(t, mark, "Search: Burst monitor")
	// Catch-up timing requires the saved-output rows to be visible. The richer
	// footer leaves the initial 80x24 detail page above those rows; that is
	// ordinary scrolling, not delayed event delivery. Prove the taller redraw
	// before starting either unchanged latency deadline below.
	mark = p.resize(t, 80, 32)
	awaitShellFrame(t, p, p.resizes, "monitor-timing-viewport", func(f shellCapturedFrame) bool {
		return f.RawBytes > mark && shellFrameBase(f, 80, 32) &&
			strings.Contains(strings.Join(f.Rows, "\n"), "Search: Burst monitor") && strings.Contains(f.Rows[31], "Draft kept")
	})
	catchUp := func(start time.Time, mark int, marker string) {
		t.Helper()
		p.await(t, mark, marker)
		elapsed := time.Since(start)
		t.Logf("Saved-event fixture %s visible in raw output after %s; not physical paint latency", marker, elapsed)
		if elapsed >= 3*time.Second {
			t.Fatalf("saved burst still waited between event pages: %s", elapsed)
		}
	}
	start := time.Now()
	mark = p.send(t, "\r")
	catchUp(start, mark, burst)
	p.awaitVisible(t, 0, "State: Working (last recorded)")
	p.awaitVisible(t, 0, burst)
	const refreshed = "MONITOR_REFRESH_EDGE"
	// A periodic read may observe the event before r is processed. Capture the
	// baseline first so that valid delivery cannot become a false timeout.
	mark, start = len(p.snapshot()), time.Now()
	appendEvent("message_delta", "\n"+refreshed)
	p.send(t, "r")
	catchUp(start, mark, refreshed)
	awaitShellFrame(t, p, p.resizes, "monitor-refreshed", func(f shellCapturedFrame) bool {
		text := strings.Join(f.Rows, "\n")
		return shellFrameBase(f, 80, 32) && strings.Contains(text, burst) && strings.Contains(text, refreshed) && strings.Contains(text, "Esc")
	})
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "Search: Burst monitor")
	mark = p.send(t, "\x1b")
	p.awaitVisible(t, mark, "? shortcuts")
	p.awaitVisible(t, mark, draft)
	mark = p.resize(t, 80, 24)
	awaitShellFrame(t, p, p.resizes, "monitor-restored-draft", func(f shellCapturedFrame) bool {
		return f.RawBytes > mark && shellFramePrompt(f, 80, 24, "", draft)
	})
	p.send(t, "\x04")
	if code := p.wait(t); code != 0 {
		t.Fatalf("monitor EOF exit=%d", code)
	}
	got, draftAttempts, err := a.Task(ctx, seed.ID)
	if err != nil || got.Draft != draft || got.Revision != seed.Revision || got.Selection != seed.Selection || len(draftAttempts) != 0 {
		t.Fatalf("viewing changed or sent draft: %+v attempts=%d err=%v", got, len(draftAttempts), err)
	}
	_, attempts, err := a.Task(ctx, attempt.TaskID)
	if err != nil || len(attempts) != 1 || attempts[0].State != "running" || attempts[0].Owner != owner {
		t.Fatalf("monitor changed running attempt: %+v err=%v", attempts, err)
	}
	events, err := a.Store.Events(ctx, attempt.ID, 0, 1000)
	if err != nil || len(events) != 253 || events[len(events)-1].Text != "\n"+refreshed {
		t.Fatalf("monitor rewrote saved progress: events=%d err=%v", len(events), err)
	}
}
