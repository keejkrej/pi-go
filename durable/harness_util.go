// Ported from packages/durable/src/harness/util.ts (pi v1.0.0).

package durable

import (
	"context"
	"errors"
	"sync"

	"github.com/keejkrej/pi-go/chord"
)

type hutWaitOutcome[T any] struct {
	value T
	err   error
}

type hutWaitEntry[T any] struct {
	ch chan hutWaitOutcome[T]
}

// hutWaitHandle waits on a registered key waiter while observing cancellation.
type hutWaitHandle[K comparable, T any] struct {
	waiters *hutWaiters[K, T]
	key     K
	entry   *hutWaitEntry[T]
}

// Await blocks until the waiter resolves, rejects, or ctx is cancelled.
func (h *hutWaitHandle[K, T]) Await(ctx chord.Context) (T, error) {
	sig := hutAbortSignal(ctx)
	if sig == nil || sig.Done() == nil {
		out := <-h.entry.ch
		return out.value, out.err
	}
	select {
	case out := <-h.entry.ch:
		return out.value, out.err
	case <-sig.Done():
		if h.waiters.remove(h.key, h.entry) {
			var zero T
			return zero, hutSignalErr(sig)
		}
		out := <-h.entry.ch
		return out.value, out.err
	}
}

// hutWaiters manages pending waits by key; each waiter settles once via Resolve, RejectAll, or context abort.
type hutWaiters[K comparable, T any] struct {
	mu   sync.Mutex
	sets map[K]map[*hutWaitEntry[T]]struct{}
}

func hutNewWaiters[K comparable, T any]() *hutWaiters[K, T] {
	return &hutWaiters[K, T]{
		sets: make(map[K]map[*hutWaitEntry[T]]struct{}),
	}
}

// Register creates and records a pending waiter synchronously. Returns early if ctx is already cancelled.
func (w *hutWaiters[K, T]) Register(key K, ctx chord.Context) (*hutWaitHandle[K, T], error) {
	if sig := hutAbortSignal(ctx); sig != nil && sig.Err() != nil {
		return nil, hutSignalErr(sig)
	}
	entry := &hutWaitEntry[T]{
		ch: make(chan hutWaitOutcome[T], 1),
	}
	w.mu.Lock()
	set := w.sets[key]
	if set == nil {
		set = make(map[*hutWaitEntry[T]]struct{})
		w.sets[key] = set
	}
	set[entry] = struct{}{}
	w.mu.Unlock()
	return &hutWaitHandle[K, T]{
		waiters: w,
		key:     key,
		entry:   entry,
	}, nil
}

func (w *hutWaiters[K, T]) remove(key K, entry *hutWaitEntry[T]) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	set := w.sets[key]
	if set == nil {
		return false
	}
	if _, ok := set[entry]; !ok {
		return false
	}
	delete(set, entry)
	if len(set) == 0 {
		delete(w.sets, key)
	}
	return true
}

// Keys returns the keys that currently have at least one pending waiter.
func (w *hutWaiters[K, T]) Keys() []K {
	w.mu.Lock()
	defer w.mu.Unlock()
	keys := make([]K, 0, len(w.sets))
	for k := range w.sets {
		keys = append(keys, k)
	}
	return keys
}

// Resolve delivers value to all waiters registered for key.
func (w *hutWaiters[K, T]) Resolve(key K, value T) {
	w.mu.Lock()
	set := w.sets[key]
	if set != nil {
		delete(w.sets, key)
	}
	entries := make([]*hutWaitEntry[T], 0, len(set))
	for e := range set {
		entries = append(entries, e)
	}
	w.mu.Unlock()

	for _, e := range entries {
		e.ch <- hutWaitOutcome[T]{value: value}
	}
}

// RejectAll delivers err to every pending waiter and clears all registrations.
func (w *hutWaiters[K, T]) RejectAll(err error) {
	w.mu.Lock()
	var entries []*hutWaitEntry[T]
	for _, set := range w.sets {
		for e := range set {
			entries = append(entries, e)
		}
	}
	clear(w.sets)
	w.mu.Unlock()

	for _, e := range entries {
		e.ch <- hutWaitOutcome[T]{err: err}
	}
}

func hutAbortSignal(ctx chord.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return ctx.AbortSignal()
}

func hutSignalErr(sig context.Context) error {
	if cause := context.Cause(sig); cause != nil {
		return cause
	}
	return sig.Err()
}

func hutCheckAborted(ctx chord.Context) error {
	sig := hutAbortSignal(ctx)
	if sig != nil && sig.Err() != nil {
		return hutSignalErr(sig)
	}
	return nil
}

func hutIsContextAborted(ctx chord.Context) bool {
	sig := hutAbortSignal(ctx)
	return sig != nil && sig.Err() != nil
}

// hutScanAll collects every item of a paginated scan in page order.
func hutScanAll[T any](scan func(cursor Cursor) (Page[T, Cursor], error)) ([]T, error) {
	var items []T
	var cursor Cursor
	for {
		page, err := scan(cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if page.Next == nil {
			return items, nil
		}
		cursor = page.Next
	}
}

func hutClosedError() error {
	return errors.New("Harness is closed")
}
