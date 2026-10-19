package process

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestGroupSignalPrecedesReapAndNeverRunsAfterRelease(t *testing.T) {
	var g groupGuard
	var mu sync.Mutex
	order := []string{}
	kill := func() error { mu.Lock(); defer mu.Unlock(); order = append(order, "signal"); return nil }
	reaping, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		close(release)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("reap did not finish")
		}
	}()
	go func() {
		defer close(done)
		g.finish(kill, func() (int, error) {
			mu.Lock()
			order = append(order, "reap")
			mu.Unlock()
			close(reaping)
			<-release
			return 0, nil
		})
	}()
	select {
	case <-reaping:
	case <-time.After(time.Second):
		t.Fatal("reap did not begin")
	}
	// Stop is nonblocking even while an error-path reap is still waiting. None
	// of these concurrent callers can signal a now-reusable numeric group ID.
	var callers sync.WaitGroup
	for i := 0; i < 32; i++ {
		callers.Add(1)
		go func() { defer callers.Done(); g.stop(kill) }()
	}
	stopped := make(chan struct{})
	go func() { callers.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked behind reap")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "signal" || order[1] != "reap" {
		t.Fatalf("unsafe lifecycle order %v", order)
	}
}

func TestGroupStopOnceErrorsAndLostOwnership(t *testing.T) {
	var g groupGuard
	want := errors.New("group termination failed")
	calls := 0
	kill := func() error { calls++; return want }
	if !errors.Is(g.stop(kill), want) {
		t.Fatal("termination failure hidden")
	}
	code, err := g.finish(kill, func() (int, error) { return 7, nil })
	if calls != 1 || code != 7 || !errors.Is(err, want) {
		t.Fatalf("calls=%d code=%d err=%v", calls, code, err)
	}
	var lost groupGuard
	lost.abandon()
	if err := lost.stop(func() error { t.Fatal("signal after lost ownership"); return nil }); err != nil {
		t.Fatal(err)
	}
	_, err = lost.finish(func() error { t.Fatal("signal after lost ownership"); return nil }, func() (int, error) { t.Fatal("reap after lost ownership"); return 0, nil })
	if err == nil {
		t.Fatal("lost ownership accepted a reap")
	}
}
