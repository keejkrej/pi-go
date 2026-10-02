// Ported from packages/chord/src/context/index.ts (pi v1.0.0).

package chord

import "context"

// ciAbortSignalContextKey stores the caller cancellation signal.
// A present nil value means the signal was explicitly cleared.
var ciAbortSignalContextKey = &ContextKey[context.Context]{
	Token: &typesSymbol{description: "chord.abortSignal"},
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
	panic("unported: Context.Value")
}

func (c *ciContextValue[T]) AbortSignal() context.Context {
	panic("unported: Context.AbortSignal")
}

func (c *ciContextValue[T]) String() string {
	panic("unported: Context.String")
}

// BackgroundContext is the empty root context named "[Context BACKGROUND_CONTEXT]".
var BackgroundContext Context = &ciEmptyContext{name: "[Context BACKGROUND_CONTEXT]"}

// TodoContext is the empty placeholder context named "[Context TODO_CONTEXT]".
var TodoContext Context = &ciEmptyContext{name: "[Context TODO_CONTEXT]"}

// CreateContextKey returns a new key. Keys compare by token identity, not by description.
func CreateContextKey[T any](description string) *ContextKey[T] {
	panic("unported: CreateContextKey")
}

// WithContextValue derives a context containing one additional or replaced value.
func WithContextValue[T any](key *ContextKey[T], value T, parent Context) Context {
	panic("unported: WithContextValue")
}

// WithAbortSignal derives a context cancelled by either the parent signal or signal.
// The parent context remains unchanged. A nil signal is the absent AbortSignal.
func WithAbortSignal(signal context.Context, parent Context) Context {
	panic("unported: WithAbortSignal")
}

// WithoutAbortSignal derives a context retaining all values except caller cancellation.
// It is intended for mandatory cleanup only.
func WithoutAbortSignal(parent Context) Context {
	panic("unported: WithoutAbortSignal")
}

// CancelContext is the independently cancellable child returned by WithCancel.
// Key order: context, cancel. Cancel aborts only the child.
type CancelContext struct {
	Context Context          `json:"context"`
	Cancel  func(reason any) `json:"cancel"`
}

// WithCancel derives an independently cancellable child context.
func WithCancel(parent Context) CancelContext {
	panic("unported: WithCancel")
}

// AwaitWithContext waits until fn returns or parent is cancelled.
// Cancellation fails only this waiter; it does not cancel fn.
// fn stands in for an already-started Promise: the waiter must not abort the underlying work.
func AwaitWithContext[T any](fn func() (T, error), parent Context) (T, error) {
	panic("unported: AwaitWithContext")
}

var (
	_ Context = (*ciEmptyContext)(nil)
	_ Context = (*ciContextValue[struct{}])(nil)
	_         = ciAbortSignalContextKey
)
