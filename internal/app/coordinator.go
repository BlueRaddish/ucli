package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"ucli.local/ucli/internal/adapters"
	"ucli.local/ucli/internal/core"
)

const maxExecutions = 8
const maxAdmissions = 16
const maxObservers = 16
const observerDeadline = 500 * time.Millisecond
const shutdownDeadline = 10 * time.Second

type preparedRun struct {
	attempt    core.Attempt
	executable string
	driver     adapters.Driver
	binding    *adapters.NativeBinding
	replay     bool
}

// The host owns work only while this App's process remains alive. No detached
// process, persistent provider conversation or automatic orphan takeover exists.
type executionHost struct {
	mu             sync.Mutex
	closing        bool
	next           uint64
	admissions     map[uint64]context.CancelFunc
	active         map[string]*executionWork
	changed        chan struct{}
	slots          chan struct{}
	admissionSlots chan struct{}
	observers      chan struct{}
	joined         sync.WaitGroup
	closeOnce      sync.Once
	closed         chan struct{}
	closeErr       error
}

type executionWork struct {
	done   chan struct{}
	cancel context.CancelFunc
	result core.Attempt // written before done closes; immutable afterwards
	err    error
}

// Execution separates a captured attempt from an observer's lifetime. A replay
// of another host's work exposes its receipt but cannot cancel or adopt it.
type Execution struct {
	app      *App
	accepted core.Attempt
	work     *executionWork
	replayed bool
	done     chan struct{}
}

func newExecutionHost() *executionHost {
	return &executionHost{admissions: make(map[uint64]context.CancelFunc), active: make(map[string]*executionWork),
		changed: make(chan struct{}), slots: make(chan struct{}, maxExecutions), admissionSlots: make(chan struct{}, maxAdmissions),
		observers: make(chan struct{}, maxObservers), closed: make(chan struct{})}
}

func copyAttempt(a core.Attempt) core.Attempt {
	a.Effective.Reasons = append([]string(nil), a.Effective.Reasons...)
	a.Warnings = append([]string(nil), a.Warnings...)
	if a.NativeContext != nil {
		v := *a.NativeContext
		a.NativeContext = &v
	}
	if a.Failure != nil {
		v := *a.Failure
		a.Failure = &v
	}
	return a
}

func (e *Execution) Accepted() core.Attempt { return copyAttempt(e.accepted) }
func (e *Execution) Replayed() bool         { return e.replayed }

// Done means the locally owned worker returned, or this is a receipt-only
// replay. It does not certify that foreign/unknown provider work has stopped.
func (e *Execution) Done() <-chan struct{} {
	if e.work != nil {
		return e.work.done
	}
	return e.done
}

func (e *Execution) Wait(ctx context.Context) (core.Attempt, error) {
	if e.work == nil {
		return e.Accepted(), resultError(e.accepted)
	}
	select {
	case <-e.work.done:
		return copyAttempt(e.work.result), e.work.err
	default:
	}
	select {
	case <-e.work.done:
		return copyAttempt(e.work.result), e.work.err
	case <-ctx.Done():
		return e.Accepted(), ctx.Err()
	}
}

// Cancel requests cancellation of this exact local worker. The adapter's later
// terminal evidence still decides complete/interrupted/unknown and lease release.
func (e *Execution) Cancel() error {
	if e.work == nil {
		return core.Problem("execution_not_owned", "This handle does not own an active worker. Inspect the saved attempt; no cancellation was sent.", 7)
	}
	e.work.cancel()
	return nil
}

// Start completes admission and durable acceptance before returning. Its context
// governs admission only; an explicit deadline is also retained by accepted work.
// Cancellation after acceptance is explicit through Cancel or App.Close.
func (a *App) Start(ctx context.Context, opt Options) (*Execution, error) {
	return a.start(ctx, opt, nil)
}

// driverOverride is an internal fixture seam; production always selects the
// admitted driver in prepareRun, including its fresh native account/config checks.
func (a *App) start(ctx context.Context, opt Options, driverOverride adapters.Driver) (*Execution, error) {
	if opt.ExpectedRevision != nil {
		revision := *opt.ExpectedRevision
		opt.ExpectedRevision = &revision
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h := a.host
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return nil, core.Problem("app_closing", "This UCLI host is closing; no new work was accepted.", 9)
	}
	select {
	case h.admissionSlots <- struct{}{}:
	default:
		h.mu.Unlock()
		return nil, core.Problem("admission_limit", "This UCLI host has sixteen admission checks in progress. Wait for one to finish.", 4)
	}
	admission, cancelAdmission := context.WithCancel(ctx)
	h.next++
	key := h.next
	h.admissions[key] = cancelAdmission
	h.joined.Add(1)
	h.mu.Unlock()
	transferred := false
	reserved := false
	defer func() {
		cancelAdmission()
		<-h.admissionSlots
		if !transferred {
			h.mu.Lock()
			delete(h.admissions, key)
			h.mu.Unlock()
			if reserved {
				<-h.slots
			}
			h.joined.Done()
		}
	}()
	p, err := a.prepareRun(admission, opt, func() error {
		select {
		case h.slots <- struct{}{}:
			reserved = true
			return nil
		default:
			return core.Problem("execution_limit", "This UCLI host has eight preparing or active workers. Wait for one to finish.", 4)
		}
	})
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if p.replay {
		e := &Execution{app: a, accepted: copyAttempt(p.attempt), work: h.active[p.attempt.ID], replayed: true, done: make(chan struct{})}
		close(e.done)
		h.mu.Unlock()
		return e, nil
	}
	// The acceptance may commit concurrently with close/caller cancellation.
	// Register even then, under the same gate, and start it already canceled so
	// the shared executor durably settles before Store.Close can run.
	lifetime := context.WithoutCancel(ctx)
	var cancelDeadline context.CancelFunc = func() {}
	if deadline, ok := ctx.Deadline(); ok {
		lifetime, cancelDeadline = context.WithDeadline(lifetime, deadline)
	}
	lifetime, cancelWork := context.WithCancel(lifetime)
	w := &executionWork{done: make(chan struct{}), cancel: func() { cancelWork(); cancelDeadline() }}
	h.active[p.attempt.ID] = w
	delete(h.admissions, key)
	if h.closing || admission.Err() != nil {
		w.cancel()
	}
	transferred = true
	h.mu.Unlock()
	if driverOverride != nil {
		p.driver = driverOverride
	}
	e := &Execution{app: a, accepted: copyAttempt(p.attempt), work: w}
	go func() {
		defer h.joined.Done()
		defer func() { <-h.slots }()
		defer w.cancel()
		w.result, w.err = a.runAccepted(lifetime, p.attempt, p.executable, p.driver, a.notifyExecution, p.binding)
		close(w.done)
		h.mu.Lock()
		delete(h.active, p.attempt.ID)
		h.mu.Unlock()
		a.notifyExecution()
	}()
	return e, nil
}

func (a *App) notifyExecution() {
	a.host.mu.Lock()
	close(a.host.changed)
	a.host.changed = make(chan struct{})
	a.host.mu.Unlock()
}

// Close forbids admission, requests stop, and waits before closing SQLite. If a
// driver ignores cancellation, return a bounded error and retain the open store;
// the one joiner closes it only when every owned admission/worker finally exits.
func (a *App) Close() error { return a.closeWithin(shutdownDeadline) }
func (a *App) closeWithin(limit time.Duration) error {
	h := a.host
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closing = true
		for _, cancel := range h.admissions {
			cancel()
		}
		for _, work := range h.active {
			work.cancel()
		}
		h.mu.Unlock()
		go func() {
			h.joined.Wait()
			h.closeErr = a.Store.Close()
			close(h.closed)
		}()
	})
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-h.closed:
		return h.closeErr
	case <-timer.C:
		return core.Problem("execution_shutdown_pending", "An owned worker has not stopped. Its state remains open for completion; do not assume remote cancellation.", 9)
	}
}

// Run keeps the foreground API. Explicit caller cancellation requests a stop;
// an observer error only detaches presentation, then waits for the owned result.
func (a *App) Run(ctx context.Context, opt Options, emit func(core.Event) error) (core.Attempt, error) {
	e, err := a.Start(ctx, opt)
	if err != nil {
		var p *core.Error
		if errors.As(err, &p) {
			return core.Attempt{TaskID: p.TaskID}, err
		}
		return core.Attempt{}, err
	}
	if e.Replayed() {
		if emit != nil {
			send, stop, observerErr := a.newObserver(emit)
			if observerErr != nil {
				return e.Accepted(), observerErr
			}
			defer stop()
			if observerErr = send(ctx, core.Event{Version: 1, TaskID: e.accepted.TaskID, AttemptID: e.accepted.ID, Type: "command_replayed", At: time.Now().UTC(), State: e.accepted.State}); observerErr != nil {
				return e.Accepted(), observerErr
			}
		}
		return e.Accepted(), resultError(e.accepted)
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = e.Cancel() })
	defer stopCancellation()
	var observed error
	if emit != nil {
		observed = e.Follow(ctx, 0, emit)
	}
	if ctx.Err() != nil {
		_ = e.Cancel()
	}
	result, runErr := waitForeground(ctx, e, shutdownDeadline)
	if runErr != nil {
		return result, runErr
	}
	return result, observed
}

func waitForeground(ctx context.Context, e *Execution, grace time.Duration) (core.Attempt, error) {
	result, err := e.Wait(ctx)
	if ctx.Err() == nil || !errors.Is(err, ctx.Err()) {
		return result, err
	}
	_ = e.Cancel()
	joinCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	result, err = e.Wait(joinCtx)
	if errors.Is(err, context.DeadlineExceeded) && joinCtx.Err() != nil {
		return result, core.Problem("execution_shutdown_pending", "The owned worker has not confirmed stopping. Saved state remains unsettled; inspect it before sending again.", 9)
	}
	return result, err
}
