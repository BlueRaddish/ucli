package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"ucli.local/ucli/internal/core"
)

const renderDeadline = 500 * time.Millisecond

type outputItem struct {
	writer  io.Writer
	text    string
	barrier chan struct{}
}

// A blocked caller-owned writer cannot be forcibly interrupted in Go. This queue
// bounds memory and service wait, with at most one blocked goroutine per Run.
// A native terminal transport can later supply its own cancelable write boundary.
type output struct {
	out, errOut io.Writer
	json        bool
	queue       chan outputItem
	done, abort chan struct{}
	stop        sync.Once
	closing     atomic.Bool
	mu          sync.Mutex
	err         error
	cancel      context.CancelFunc
	terminal    *terminalUI // Run closes this after the output worker and app
}

func newOutput(out, errOut io.Writer, jsonMode bool, cancel context.CancelFunc) *output {
	o := &output{out: out, errOut: errOut, json: jsonMode, queue: make(chan outputItem, 32), done: make(chan struct{}), abort: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(o.done)
		for {
			var item outputItem
			select {
			case <-o.abort:
				return
			case item = <-o.queue:
			}
			// Leave the queue open: a timed-out app callback can still be sending.
			// A terminal barrier acknowledges every preceding write and exits.
			if item.barrier != nil {
				close(item.barrier)
				return
			}
			select {
			case <-o.abort:
				return
			default:
			}
			n, err := io.WriteString(item.writer, item.text)
			if err == nil && n != len(item.text) {
				err = io.ErrShortWrite
			}
			if err != nil {
				o.fail(err)
				return
			}
		}
	}()
	return o
}

func (o *output) fail(err error) error {
	o.mu.Lock()
	if o.err == nil {
		o.err = err
	}
	result := o.err
	o.mu.Unlock()
	o.cancel()
	return result
}

func (o *output) failure() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}

func (o *output) write(w io.Writer, text string) error {
	if o.closing.Load() {
		return io.ErrClosedPipe
	}
	if err := o.failure(); err != nil {
		return err
	}
	timer := time.NewTimer(renderDeadline)
	defer timer.Stop()
	select {
	case <-o.abort:
		return io.ErrClosedPipe
	case <-o.done:
		if err := o.failure(); err != nil {
			return err
		}
		return io.ErrClosedPipe
	case o.queue <- outputItem{writer: w, text: text}:
		if o.closing.Load() {
			return io.ErrClosedPipe
		}
		return nil
	case <-timer.C:
		return o.fail(errors.New("output reader is not keeping up; inspect the saved task after reconnecting"))
	}
}

func (o *output) finish() error {
	o.closing.Store(true)
	defer o.stop.Do(func() { close(o.abort) })
	timer := time.NewTimer(renderDeadline)
	defer timer.Stop()
	ack := make(chan struct{})
	select {
	case o.queue <- outputItem{barrier: ack}:
	case <-o.done:
		return o.failure()
	case <-timer.C:
		return o.fail(errors.New("output reader stopped responding; saved task state remains available"))
	}
	select {
	case <-ack:
		return o.failure()
	case <-o.done:
		return o.failure()
	case <-timer.C:
		return o.fail(errors.New("output reader stopped responding; saved task state remains available"))
	}
}

func (o *output) text(text string) error     { return o.write(o.out, safe(text)) }
func (o *output) progress(text string) error { return o.progressTone(text, toneInfo) }
func (o *output) progressTone(text string, tone terminalTone) error {
	if o.terminal != nil && !o.json && tone == toneInfo {
		return o.notice(text, tone)
	}
	if err := o.write(o.errOut, safe(text)); err != nil {
		return err
	}
	return o.notice(text, tone)
}
func (o *output) data(kind string, data any) error {
	return o.dataWithPagination(kind, data, nil)
}

func (o *output) dataWithPagination(kind string, data, pagination any) error {
	b, err := json.Marshal(struct {
		Version    int    `json:"version"`
		Type       string `json:"type"`
		Data       any    `json:"data"`
		Pagination any    `json:"pagination,omitempty"`
	}{1, kind, data, pagination})
	if err != nil {
		return err
	}
	return o.write(o.out, string(b)+"\n")
}

func (o *output) event(e core.Event) error {
	if o.json {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		return o.write(o.out, string(b)+"\n")
	}
	switch e.Type {
	case "message_delta":
		return o.assistant(e.Text)
	case "accepted":
		return o.progress("Accepted task " + e.TaskID + "\n")
	case "warning":
		return o.progressTone("Warning: "+e.Text+"\n", toneWarning)
	case "status":
		if e.Text != "" {
			return o.progress(e.Text + "\n")
		}
	case "result_persisted":
		if e.State == "complete" {
			return o.notice("Complete | result saved", toneSuccess)
		}
	}
	return nil
}

func (o *output) problem(err error) int {
	problem := problemFor(err)
	if o.json {
		_ = o.data("error", problem)
	} else {
		text := "ucli: " + problem.Message + "\n"
		if problem.TaskID != "" {
			text += "Saved task: " + problem.TaskID + "; inspect with ucli tasks show " + problem.TaskID + "\n"
		}
		tone := toneError
		if problem.Exit == 3 || problem.Exit == 4 || problem.Exit == 7 {
			tone = toneWarning
		}
		_ = o.progressTone(text, tone)
	}
	if problem.Exit == 0 {
		return 9
	}
	return problem.Exit
}

func problemFor(err error) *core.Error {
	var problem *core.Error
	if !errors.As(err, &problem) {
		switch {
		case errors.Is(err, context.Canceled):
			problem = core.Problem("interrupted", "interrupted; inspect ucli tasks for the recorded outcome", 130)
		case errors.Is(err, context.DeadlineExceeded):
			problem = core.Problem("deadline", "deadline exceeded; inspect ucli tasks before retrying", 124)
		default:
			problem = core.Problem("internal", err.Error(), 9)
		}
	}
	return problem
}

// Escape all terminal controls, including split ANSI/OSC sequences, rather than
// attempting to interpret provider output as terminal instructions.
func safe(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	return b.String()
}
