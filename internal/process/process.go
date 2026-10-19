// Package process runs explicitly configured children and owns their process tree.
// It transports raw bounded chunks; provider framing and permission policy belong
// above this package. Process ownership is not a security sandbox.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	ErrBackpressure        = errors.New("process output queue exhausted")
	ErrFrameTooLarge       = errors.New("process input exceeds frame limit")
	ErrInputBlocked        = errors.New("process stdin write deadline exceeded")
	ErrClosed              = errors.New("process is closed")
	ErrDrainTimeout        = errors.New("process pipe cleanup deadline exceeded")
	ErrUnsupportedPlatform = errors.New("owned process launch is unsupported on this platform")
)

const (
	defaultFrame = 64 * 1024
	defaultQueue = 256 * 1024
	writeTimeout = 2 * time.Second
	closeTimeout = 5 * time.Second
	drainTimeout = time.Second
)

type Spec struct {
	Executable string
	Args       []string
	// Env must be nonnil. An empty slice means an intentionally empty environment.
	Env        []string
	Dir        string
	QueueBytes int
	// FrameBytes bounds each raw output chunk and each Send payload, not JSON lines.
	FrameBytes int
}
type Chunk struct {
	Stream string
	Data   []byte
}
type Exit struct {
	Code    int
	Stopped bool
}

type nativeProcess interface {
	wait() (int, error)
	stop() error
	close() error
}

type Child struct {
	owner       nativeProcess
	input       *os.File
	outputs     []*os.File
	events      chan Chunk
	done        chan struct{}
	writeGate   chan struct{}
	inputClosed bool // guarded by writeGate; EOF does not stop the owned process
	frame       int
	readSize    int
	stopOnce    sync.Once
	mu          sync.Mutex
	finished    bool
	exit        Exit
	err         error
}

func validate(spec *Spec) error {
	if !filepath.IsAbs(spec.Executable) || strings.IndexByte(spec.Executable, 0) >= 0 {
		return errors.New("executable must be an absolute path without NUL")
	}
	if !filepath.IsAbs(spec.Dir) {
		return errors.New("working directory must be absolute")
	}
	info, err := os.Stat(spec.Dir)
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("working directory is not a directory")
	}
	if spec.Env == nil {
		return errors.New("explicit environment is required")
	}
	keys := map[string]bool{}
	for _, entry := range spec.Env {
		i := strings.IndexByte(entry, '=')
		if i <= 0 || strings.IndexByte(entry, 0) >= 0 {
			return errors.New("invalid environment entry")
		}
		key := entry[:i]
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		if keys[key] {
			return errors.New("duplicate environment key")
		}
		keys[key] = true
	}
	for _, arg := range spec.Args {
		if strings.IndexByte(arg, 0) >= 0 {
			return errors.New("argument contains NUL")
		}
	}
	if spec.FrameBytes == 0 {
		spec.FrameBytes = defaultFrame
	}
	if spec.QueueBytes == 0 {
		spec.QueueBytes = max(defaultQueue, spec.FrameBytes)
	}
	if spec.FrameBytes < 1 || spec.FrameBytes > 1024*1024 || spec.QueueBytes < spec.FrameBytes || spec.QueueBytes > 16*1024*1024 {
		return errors.New("invalid output limits: frame 1..1MiB, queue frame..16MiB")
	}
	return nil
}

func Start(ctx context.Context, spec Spec) (*Child, error) {
	if ctx == nil {
		return nil, errors.New("nil lifetime context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validate(&spec); err != nil {
		return nil, err
	}
	// Copy caller-owned slices before starting asynchronous work.
	spec.Args = append([]string{}, spec.Args...)
	spec.Env = append([]string{}, spec.Env...)
	var pipes []*os.File
	pipe := func() (*os.File, *os.File, error) {
		r, w, err := os.Pipe()
		if err == nil {
			pipes = append(pipes, r, w)
		}
		return r, w, err
	}
	keep := false
	defer func() {
		if !keep {
			for _, f := range pipes {
				f.Close()
			}
		}
	}()
	inR, inW, err := pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := pipe()
	if err != nil {
		return nil, err
	}
	owner, err := startNative(spec, inR, outW, errW)
	if err != nil {
		return nil, err
	}
	// Only the child-facing copies are inherited; the parent must release them.
	for _, f := range []*os.File{inR, outW, errW} {
		f.Close()
	}
	readSize := min(spec.FrameBytes, 4096)
	c := &Child{owner: owner, input: inW, outputs: []*os.File{outR, errR},
		events: make(chan Chunk, spec.QueueBytes/readSize), done: make(chan struct{}),
		writeGate: make(chan struct{}, 1), frame: spec.FrameBytes, readSize: readSize, exit: Exit{Code: -1}}
	c.writeGate <- struct{}{}
	keep = true
	var readers sync.WaitGroup
	readers.Add(2)
	for i, stream := range []string{"stdout", "stderr"} {
		go func(f *os.File, stream string) { defer readers.Done(); c.read(f, stream) }(c.outputs[i], stream)
	}
	readDone := make(chan struct{})
	go func() { readers.Wait(); close(c.events); close(readDone) }()
	go c.reap(readDone)
	go func() {
		select {
		case <-ctx.Done():
			c.stop(ctx.Err(), true)
		case <-c.done:
		}
	}()
	return c, nil
}

func (c *Child) addError(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.finished {
		c.err = errors.Join(c.err, err)
	}
}
func (c *Child) stop(reason error, requested bool) {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.exit.Stopped = c.exit.Stopped || requested
	c.err = errors.Join(c.err, reason)
	c.mu.Unlock()
	c.stopOnce.Do(func() { c.addError(c.owner.stop()) })
}

func (c *Child) read(f *os.File, stream string) {
	defer f.Close()
	for {
		data := make([]byte, c.readSize)
		n, err := f.Read(data)
		if n > 0 {
			// Never call user code or wait for a consumer in an OS pipe reader.
			// Capacity counts full-size chunks conservatively; memory remains
			// bounded by QueueBytes plus two readers' working chunks.
			select {
			case c.events <- Chunk{stream, data[:n]}:
			default:
				c.stop(ErrBackpressure, true)
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				c.stop(fmt.Errorf("%s read: %w", stream, err), true)
			}
			return
		}
	}
}

func (c *Child) reap(readDone <-chan struct{}) {
	code, err := c.owner.wait()
	c.addError(err)
	// A successful leader exit does not surrender ownership of its descendants.
	c.stop(nil, false)
	c.input.Close() // Unblocks a pending Send even if the child never read stdin.
	select {
	case <-readDone:
	case <-time.After(drainTimeout):
		c.addError(ErrDrainTimeout)
		for _, f := range c.outputs {
			f.Close()
		}
		// Wait is still bounded if the OS unexpectedly fails to cancel a read.
		// Only the reader-join goroutine closes Events, preventing send/close races.
		select {
		case <-readDone:
		case <-time.After(drainTimeout):
		}
	}
	c.addError(c.owner.close())
	c.mu.Lock()
	c.exit.Code = code
	c.finished = true
	c.mu.Unlock()
	close(c.done)
}

// Events delivers every read chunk or stops with ErrBackpressure. Data belongs to
// the receiver. Consumers must inspect Wait's error; EOF alone is not success.
func (c *Child) Events() <-chan Chunk { return c.events }

// Settled reports whether the terminal outcome is cached after owned teardown.
// It distinguishes a Close wait deadline from a cached lifetime deadline. Callers
// must still inspect Close/Wait errors: settlement alone is not successful cleanup.
func (c *Child) Settled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finished
}

// Send serializes writes and returns within the caller deadline or two seconds.
// A timeout during a write stops the tree: partial protocol input cannot be retried
// safely on the same process. A timeout while waiting for the write gate does not.
func (c *Child) Send(ctx context.Context, data []byte) error {
	if ctx == nil {
		return errors.New("nil send context")
	}
	if len(data) > c.frame {
		return ErrFrameTooLarge
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	case <-c.writeGate:
	}
	if err := ctx.Err(); err != nil {
		c.writeGate <- struct{}{}
		return err
	}
	select {
	case <-c.done:
		c.writeGate <- struct{}{}
		return ErrClosed
	default:
	}
	if c.inputClosed {
		c.writeGate <- struct{}{}
		return ErrClosed
	}
	data = append([]byte{}, data...)
	written := make(chan error, 1)
	go func() {
		_, err := c.input.Write(data)
		written <- err
		c.writeGate <- struct{}{}
	}()
	select {
	case err := <-written:
		if err != nil {
			c.stop(fmt.Errorf("stdin write: %w", err), true)
		}
		return err
	case <-ctx.Done():
		err := errors.Join(ErrInputBlocked, ctx.Err())
		c.stop(err, true)
		c.input.Close()
		return err
	case <-c.done:
		return ErrClosed
	}
}

// CloseInput sends EOF after preceding Send calls, while preserving output and
// process ownership. Repeated calls are safe; later Send calls return ErrClosed.
// A concurrent blocked writer bounds this wait to the normal write deadline.
func (c *Child) CloseInput() error {
	timer := time.NewTimer(writeTimeout)
	defer timer.Stop()
	select {
	case <-c.done:
		return nil
	case <-timer.C:
		return ErrInputBlocked
	case <-c.writeGate:
	}
	defer func() { c.writeGate <- struct{}{} }()
	if c.inputClosed {
		return nil
	}
	c.inputClosed = true
	err := c.input.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

// Wait returns the cached terminal result, and may be called concurrently/repeatedly.
// Its context only bounds this wait, never the child's lifetime. Native nonzero
// exits are reported through Code; errors describe transport, stop or cleanup.
func (c *Child) Wait(ctx context.Context) (Exit, error) {
	if ctx == nil {
		return Exit{Code: -1}, errors.New("nil wait context")
	}
	// Once terminal, a later caller deadline cannot replace the cached outcome.
	select {
	case <-c.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.exit, c.err
	default:
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.finished {
			return c.exit, c.err
		}
		return c.exit, errors.Join(ctx.Err(), c.err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exit, c.err
}

// Close requests owned-tree termination and waits at most five seconds. Errors
// remain observable; a deadline or stop failure is never proof of cleanup.
func (c *Child) Close() error {
	c.stop(nil, true)
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	_, err := c.Wait(ctx)
	return err
}

// Alive is conservative liveness only, never an ownership or PID-reuse proof.
func Alive(pid int) (bool, error) {
	if pid <= 0 || int64(pid) > 1<<31-1 {
		return false, errors.New("pid must be a positive signed 32-bit identifier")
	}
	return alive(pid)
}
