package process

import (
	"errors"
	"sync"
)

// groupGuard serializes POSIX signalling with relinquishing leader identity.
// It is platform-neutral so the ownership ordering can be tested on every host;
// only the Linux native owner calls it in production. The caller alone reaps.
type groupGuard struct {
	mu              sync.Mutex
	closed, stopped bool
	stopErr         error
}

func (g *groupGuard) stop(kill func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	return g.stopLocked(kill)
}

func (g *groupGuard) stopLocked(kill func() error) error {
	if !g.stopped {
		g.stopped = true
		g.stopErr = kill()
	}
	return g.stopErr
}

func (g *groupGuard) finish(kill func() error, reap func() (int, error)) (int, error) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return -1, errors.New("process group identity already released")
	}
	stopErr := g.stopLocked(kill)
	g.closed = true // Forbid all future signals BEFORE reap can release the PID.
	g.mu.Unlock()
	// Never hold mu across a potentially blocking reap on an observation/error
	// path: Close must still return its own bounded cleanup uncertainty.
	code, err := reap()
	return code, errors.Join(stopErr, err)
}

func (g *groupGuard) abandon() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}
