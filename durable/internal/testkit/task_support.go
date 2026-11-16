// Ported from packages/durable/test/task-support.ts (pi v1.0.0).

package testkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/durable"
)

// Deferred is a one-shot futures primitive for coordinating test goroutines.
type Deferred[T any] struct {
	once sync.Once
	done chan struct{}
	val  T
	err  error
}

// NewDeferred returns a fresh unsettled Deferred[T].
func NewDeferred[T any]() *Deferred[T] {
	return &Deferred[T]{
		done: make(chan struct{}),
	}
}

// Resolve completes d with val if not already settled.
func (d *Deferred[T]) Resolve(val T) {
	d.once.Do(func() {
		d.val = val
		close(d.done)
	})
}

// Reject fails d with err if not already settled.
func (d *Deferred[T]) Reject(err error) {
	d.once.Do(func() {
		d.err = err
		close(d.done)
	})
}

// Done returns a channel closed once d settles.
func (d *Deferred[T]) Done() <-chan struct{} {
	return d.done
}

// Settled reports whether d has already resolved or rejected.
func (d *Deferred[T]) Settled() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}

// Wait blocks until d settles and returns its value or error.
func (d *Deferred[T]) Wait() (T, error) {
	<-d.done
	return d.val, d.err
}

// WaitContext blocks until either d settles or ctx is cancelled.
func (d *Deferred[T]) WaitContext(ctx context.Context) (T, error) {
	select {
	case <-d.done:
		return d.val, d.err
	case <-ctx.Done():
		var zero T
		return zero, context.Cause(ctx)
	}
}

// Aborted blocks until sig is cancelled and returns its cancellation cause or error.
func Aborted(sig context.Context) error {
	if sig == nil {
		return context.Canceled
	}
	<-sig.Done()
	if cause := context.Cause(sig); cause != nil {
		return cause
	}
	return sig.Err()
}

// Eventually polls check across Flush turns until it returns true, or returns an error on timeout.
func Eventually(check func() (bool, error)) error {
	for attempt := 0; attempt < 400; attempt++ {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		Flush()
	}
	return errors.New("Condition was not reached")
}

// SettledChannel flushes pending work and reports whether done has already closed.
func SettledChannel(done <-chan struct{}) bool {
	Flush()
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// OpenTasksOptions customizes OpenTasks.
type OpenTasksOptions struct {
	Registry durable.Registry
	Now      func() int64
	Settings *durable.HarnessSettings
}

// OpenTasksResult holds the opened Harness, registry, and collected OnReport errors.
type OpenTasksResult struct {
	Harness  durable.Harness
	Registry durable.Registry
	mu       sync.Mutex
	reports  []error
}

// Reports returns a snapshot copy of errors reported to HarnessOptions.OnReport.
func (r *OpenTasksResult) Reports() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]error, len(r.reports))
	copy(out, r.reports)
	return out
}

// OpenTasks opens a Harness whose registry contains tasks and records all OnReport errors.
func OpenTasks(st durable.Storage, tasks []durable.AnyTask, options ...OpenTasksOptions) (*OpenTasksResult, error) {
	var opt OpenTasksOptions
	if len(options) > 0 {
		opt = options[0]
	}
	registry := opt.Registry
	if registry == nil {
		registry = durable.CreateRegistry()
	}
	if len(tasks) > 0 {
		if err := registry.Install(durable.DefineExtension(&durable.Extension{
			Name:  "tasks",
			Tasks: tasks,
		})); err != nil {
			return nil, fmt.Errorf("install tasks extension: %w", err)
		}
	}
	res := &OpenTasksResult{Registry: registry}
	hOpts := durable.HarnessOptions{
		Models:   ai.CreateModels(nil),
		Registry: registry,
		OnReport: func(err error) {
			res.mu.Lock()
			res.reports = append(res.reports, err)
			res.mu.Unlock()
		},
		Now:            opt.Now,
		StaticSettings: opt.Settings,
	}
	harness, err := durable.HarnessOpen(st, hOpts, TestContext)
	if err != nil {
		return nil, err
	}
	res.Harness = harness
	return res, nil
}

// Completed returns a terminal completed NextTaskState for result.
func Completed[S, R any](checkpoint S, result R) durable.NextTaskState[S, R] {
	return durable.NextTaskState[S, R]{
		Status:     durable.TaskStateTerminal,
		Checkpoint: checkpoint,
		Outcome: &durable.TypedTaskOutcome[R]{
			Status: durable.TaskOutcomeCompleted,
			Result: result,
			HasRes: true,
		},
	}
}

// AbortedWith returns a terminal aborted NextTaskState with reason.
func AbortedWith[S, R any](checkpoint S, reason string) durable.NextTaskState[S, R] {
	r := reason
	return durable.NextTaskState[S, R]{
		Status:     durable.TaskStateTerminal,
		Checkpoint: checkpoint,
		Outcome: &durable.TypedTaskOutcome[R]{
			Status: durable.TaskOutcomeAborted,
			Reason: &r,
		},
	}
}

// CountingRegistryReader wraps a RegistryReader while counting active Subscribe listeners.
type CountingRegistryReader struct {
	registry durable.RegistryReader
	mu       sync.Mutex
	count    int
}

// NewCountingReader wraps registry and tracks active subscriptions.
func NewCountingReader(registry durable.RegistryReader) *CountingRegistryReader {
	return &CountingRegistryReader{registry: registry}
}

// Snapshot delegates to the wrapped RegistryReader.
func (c *CountingRegistryReader) Snapshot() durable.RegistrySnapshot {
	return c.registry.Snapshot()
}

// Subscribe wraps registry.Subscribe and tracks live unclosed subscriptions.
func (c *CountingRegistryReader) Subscribe(listener func()) func() {
	c.mu.Lock()
	c.count++
	c.mu.Unlock()
	unsub := c.registry.Subscribe(listener)
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			c.count--
			c.mu.Unlock()
			if unsub != nil {
				unsub()
			}
		})
	}
}

// Subscriptions returns the number of currently active subscriptions.
func (c *CountingRegistryReader) Subscriptions() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

var _ durable.RegistryReader = (*CountingRegistryReader)(nil)

// SyncWriter wraps an io.Writer with a mutex and Close fence so asynchronous
// callbacks stop writing cleanly before the caller inspects the target buffer.
type SyncWriter struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
}

// NewSyncWriter wraps w for concurrent writes. When w is nil, io.Discard is used.
func NewSyncWriter(w io.Writer) *SyncWriter {
	if w == nil {
		w = io.Discard
	}
	return &SyncWriter{w: w}
}

// Write forwards p to the wrapped writer under the mutex until Close is called.
func (s *SyncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return len(p), nil
	}
	return s.w.Write(p)
}

// Close fences any future Write calls from touching the underlying writer.
func (s *SyncWriter) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
