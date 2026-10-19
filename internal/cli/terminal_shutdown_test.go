package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Gate the actual writer's Send boundary, not a parallel implementation of its
// buffering. This also models a renderer that cannot finish until canceled.
type shutdownGateProgram struct {
	ctx     context.Context
	entered chan tea.Msg
	release chan struct{}
}

func (p *shutdownGateProgram) Send(msg tea.Msg) {
	p.entered <- msg
	select {
	case <-p.release:
	case <-p.ctx.Done():
	}
}
func (p *shutdownGateProgram) Quit() { p.Send(tea.Quit()) }

func TestTerminalShutdownSerializesWriteAndFlush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &shutdownGateProgram{ctx: ctx, entered: make(chan tea.Msg, 16), release: make(chan struct{})}
	u := &terminalUI{program: p, done: make(chan struct{})}
	w := &terminalWriter{ui: u}
	written, flushed := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(written)
		_, _ = w.Write([]byte("first line\nremaining text"))
	}()
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not reach the blocked send")
	}
	go func() { defer close(flushed); w.flush() }()
	select {
	case <-p.entered:
		t.Error("flush accessed pending output while Write still owned it")
	case <-time.After(100 * time.Millisecond):
	}
	close(p.release)
	for _, done := range []<-chan struct{}{written, flushed} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("writer/flush did not finish after the send was released")
		}
	}
}

func TestTerminalShutdownStopsInputDespiteBlockedFinalOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &shutdownGateProgram{ctx: ctx, entered: make(chan tea.Msg, 16), release: make(chan struct{})}
	stopped := make(chan struct{})
	u := &terminalUI{program: p, done: make(chan struct{}), cancel: cancel,
		stopInput: sync.OnceValue(func() error { close(stopped); return nil })}
	u.writer = &terminalWriter{ui: u}
	u.writer.pending.WriteString("final diagnostic")
	result := make(chan error, 1)
	start := time.Now()
	go func() { result <- u.close() }()
	select {
	case <-stopped:
	case <-time.After(300 * time.Millisecond):
		t.Error("input was not stopped independently of blocked final output")
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "deadline") {
			t.Fatalf("blocked renderer reported success: %v", err)
		}
		if time.Since(start) > 3*time.Second || ctx.Err() == nil {
			t.Fatal("renderer shutdown did not cancel within its bound")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked renderer held shutdown indefinitely")
	}
}

func TestTerminalShutdownBoundsInputJoin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &shutdownGateProgram{ctx: ctx, entered: make(chan tea.Msg, 16), release: make(chan struct{})}
	close(p.release)
	stopped, join := make(chan struct{}), make(chan struct{})
	defer close(join)
	u := &terminalUI{program: p, done: make(chan struct{}), cancel: cancel,
		stopInput: sync.OnceValue(func() error { close(stopped); <-join; return nil })}
	u.writer = &terminalWriter{ui: u}
	result := make(chan error, 1)
	go func() { result <- u.close() }()
	select {
	case <-stopped:
	case <-time.After(300 * time.Millisecond):
		t.Error("input stop hook was not invoked")
	}
	select {
	case err := <-result:
		if err == nil || ctx.Err() == nil {
			t.Fatalf("blocked input join reported clean shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("input join held shutdown indefinitely")
	}
}

func TestTerminalShutdownPreservesNormalFinalDiagnostic(t *testing.T) {
	var output bytes.Buffer
	u := newTerminalUI(strings.NewReader(""), &output, func() {}, false)
	if _, err := u.writer.Write([]byte("Final saved draft diagnostic")); err != nil {
		t.Fatal(err)
	}
	if err := u.close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Final saved draft diagnostic") {
		t.Fatalf("normal shutdown lost its final diagnostic: %q", output.String())
	}
}

func TestTerminalShutdownPreservesInputFailureDiagnostic(t *testing.T) {
	var output bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := newTerminalUI(strings.NewReader(""), &output, cancel, false)
	want := errors.New("input could not be preserved")
	u.fail(want)
	if ctx.Err() == nil {
		t.Fatal("input failure did not cancel the application")
	}
	if _, err := u.writer.Write([]byte("Input not accepted. Use redirected UTF-8 input.\n")); err != nil {
		t.Fatal(err)
	}
	if err := u.close(); !errors.Is(err, want) {
		t.Fatalf("lost original input failure: %v", err)
	}
	if !strings.Contains(output.String(), "Use redirected UTF-8 input.") {
		t.Fatalf("input failure hid its actionable diagnostic: %q", output.String())
	}
}
