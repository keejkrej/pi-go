// Ported from packages/client/src/promise.ts (pi v1.0.0).

package client

import (
	"context"
	"errors"
	"sync"
)

// PromiseResolvers holds a thread-safe one-shot deferred result with its settlement channel.
type PromiseResolvers[T any] struct {
	mu      sync.Mutex
	settled bool
	value   T
	err     error
	done    chan struct{}
}

// CreatePromiseResolvers allocates a fresh unsettled PromiseResolvers.
func CreatePromiseResolvers[T any]() *PromiseResolvers[T] {
	return &PromiseResolvers[T]{
		done: make(chan struct{}),
	}
}

// Resolve settles the promise with value if not already settled.
func (p *PromiseResolvers[T]) Resolve(value T) {
	p.mu.Lock()
	if p.settled {
		p.mu.Unlock()
		return
	}
	p.settled = true
	p.value = value
	close(p.done)
	p.mu.Unlock()
}

// Reject settles the promise with err if not already settled.
func (p *PromiseResolvers[T]) Reject(err error) {
	if err == nil {
		err = errors.New("rejected")
	}
	p.mu.Lock()
	if p.settled {
		p.mu.Unlock()
		return
	}
	p.settled = true
	p.err = err
	close(p.done)
	p.mu.Unlock()
}

// Done returns a channel that closes when the promise settles.
func (p *PromiseResolvers[T]) Done() <-chan struct{} {
	return p.done
}

// Settled reports whether the promise has already been settled.
func (p *PromiseResolvers[T]) Settled() bool {
	p.mu.Lock()
	settled := p.settled
	p.mu.Unlock()
	return settled
}

// Await blocks until the promise settles and returns its outcome.
func (p *PromiseResolvers[T]) Await() (T, error) {
	<-p.done
	return p.value, p.err
}

// AwaitContext blocks until the promise settles or ctx is cancelled.
func (p *PromiseResolvers[T]) AwaitContext(ctx context.Context) (T, error) {
	if ctx == nil {
		return p.Await()
	}
	select {
	case <-p.done:
		return p.value, p.err
	case <-ctx.Done():
		var zero T
		if cause := context.Cause(ctx); cause != nil {
			return zero, cause
		}
		return zero, ctx.Err()
	}
}
