// Ported from packages/chord/src/context/index.ts (pi v1.0.0).

package chord

import (
	"context"
	"errors"
	"fmt"

	"github.com/keejkrej/pi-go/internal/js"
)

// ciAbortSignalContextKey stores the caller cancellation signal.
// A present nil value means the signal was explicitly cleared.
var ciAbortSignalContextKey = &ContextKey[context.Context]{
	Token: &typesSymbol{description: "chord.abortSignal"},
}

// ciAbortReasonError wraps a non-error cancellation reason passed to Cancel.
type ciAbortReasonError struct {
	reason any
}

func (e *ciAbortReasonError) Error() string {
	return fmt.Sprint(e.reason)
}

// Reason returns the raw cancellation reason supplied to Cancel.
func (e *ciAbortReasonError) Reason() any {
	return e.reason
}

// ciEmptyContext is a context with a fixed name and no values.
type ciEmptyContext struct {
	name string
}

func (c *ciEmptyContext) Value(key any) (any, bool) { return nil, false }

func (c *ciEmptyContext) AbortSignal() context.Context { return nil }

func (c *ciEmptyContext) String() string { return c.name }

// ciContextValue is a context with one additional or replaced value.
type ciContextValue[T any] struct {
	parent Context
	key    *ContextKey[T]
	value  T
}

func (c *ciContextValue[T]) Value(key any) (any, bool) {
	if k, ok := key.(*ContextKey[T]); ok && k != nil && c.key != nil && k.Token == c.key.Token {
		return c.value, true
	}
	return c.parent.Value(key)
}

func (c *ciContextValue[T]) AbortSignal() context.Context {
	val, ok := c.Value(ciAbortSignalContextKey)
	if !ok {
		return nil
	}
	sig, _ := val.(context.Context)
	return sig
}

func (c *ciContextValue[T]) String() string {
	desc := "anonymous"
	if c.key != nil && c.key.Token != nil && c.key.Token.description != "" {
		desc = c.key.Token.description
	}
	return fmt.Sprintf("%s.WithValue(%s)", c.parent.String(), desc)
}

// BackgroundContext is the empty root context named "[Context BACKGROUND_CONTEXT]".
var BackgroundContext Context = &ciEmptyContext{name: "[Context BACKGROUND_CONTEXT]"}

// TodoContext is the empty placeholder context named "[Context TODO_CONTEXT]".
var TodoContext Context = &ciEmptyContext{name: "[Context TODO_CONTEXT]"}

// CreateContextKey returns a new key. Keys compare by token identity, not by description.
func CreateContextKey[T any](description string) *ContextKey[T] {
	return &ContextKey[T]{
		Token: &typesSymbol{description: description},
	}
}

// WithContextValue derives a context containing one additional or replaced value.
func WithContextValue[T any](key *ContextKey[T], value T, parent Context) Context {
	if parent == nil {
		parent = BackgroundContext
	}
	return &ciContextValue[T]{
		parent: parent,
		key:    key,
		value:  value,
	}
}

// WithAbortSignal derives a context cancelled by either the parent signal or signal.
// The parent context remains unchanged. A nil signal is the absent AbortSignal.
func WithAbortSignal(signal context.Context, parent Context) Context {
	if parent == nil {
		parent = BackgroundContext
	}
	if signal == nil {
		return parent
	}
	parentSignal := parent.AbortSignal()
	if parentSignal == nil {
		return WithContextValue(ciAbortSignalContextKey, signal, parent)
	}
	if signal.Done() == nil {
		return WithContextValue(ciAbortSignalContextKey, parentSignal, parent)
	}
	combined, cancel := context.WithCancelCause(parentSignal)
	if signal.Err() != nil {
		cancel(ciSignalCause(signal))
		return WithContextValue(ciAbortSignalContextKey, combined, parent)
	}
	stop := context.AfterFunc(signal, func() {
		cancel(ciSignalCause(signal))
	})
	context.AfterFunc(combined, func() {
		stop()
	})
	return WithContextValue(ciAbortSignalContextKey, combined, parent)
}

// WithoutAbortSignal derives a context retaining all values except caller cancellation.
// It is intended for mandatory cleanup only.
func WithoutAbortSignal(parent Context) Context {
	return WithContextValue(ciAbortSignalContextKey, nil, parent)
}

// CancelContext is the independently cancellable child returned by WithCancel.
// Key order: context, cancel. Cancel aborts only the child.
type CancelContext struct {
	Context Context          `json:"context"`
	Cancel  func(reason any) `json:"cancel"`
}

// WithCancel derives an independently cancellable child context.
func WithCancel(parent Context) CancelContext {
	if parent == nil {
		parent = BackgroundContext
	}
	base := context.Background()
	if parentSignal := parent.AbortSignal(); parentSignal != nil {
		base = parentSignal
	}
	ctx, cancelCause := context.WithCancelCause(base)
	return CancelContext{
		Context: WithContextValue(ciAbortSignalContextKey, ctx, parent),
		Cancel: func(reason any) {
			switch r := reason.(type) {
			case nil:
				cancelCause(nil)
			case error:
				cancelCause(r)
			default:
				cancelCause(&ciAbortReasonError{reason: r})
			}
		},
	}
}

// AwaitWithContext waits until fn returns or parent is cancelled.
// Cancellation fails only this waiter; it does not cancel fn.
// fn stands in for an already-started Promise: the waiter must not abort the underlying work.
func AwaitWithContext[T any](fn func() (T, error), parent Context) (T, error) {
	if parent == nil {
		return fn()
	}
	signal := parent.AbortSignal()
	if signal == nil {
		return fn()
	}
	if signal.Err() != nil {
		var zero T
		return zero, ciAbortError(signal)
	}
	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		val, err := fn()
		ch <- result{val: val, err: err}
	}()
	select {
	case res := <-ch:
		return res.val, res.err
	case <-signal.Done():
		var zero T
		return zero, ciAbortError(signal)
	}
}

func ciSignalCause(signal context.Context) error {
	if cause := context.Cause(signal); cause != nil {
		return cause
	}
	return signal.Err()
}

func ciAbortError(signal context.Context) error {
	cause := ciSignalCause(signal)
	if cause == nil {
		return &js.JSError{ErrName: "AbortError", Message: "The operation was aborted"}
	}
	var reasonErr *ciAbortReasonError
	if errors.As(cause, &reasonErr) {
		return &js.JSError{ErrName: "AbortError", Message: "The operation was aborted"}
	}
	return cause
}

var (
	_ Context = (*ciEmptyContext)(nil)
	_ Context = (*ciContextValue[struct{}])(nil)
	_         = ciAbortSignalContextKey
)
