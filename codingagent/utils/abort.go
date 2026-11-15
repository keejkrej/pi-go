// Ported from packages/coding-agent/src/utils/abort.ts (pi v1.5.2).

package utils

import (
	"context"
	"errors"
)

// AbortError represents an aborted operation.
type AbortError struct {
	Message string
}

func (e *AbortError) Error() string {
	if e != nil && e.Message != "" {
		return e.Message
	}
	return "The operation was aborted"
}

// Name returns the JS error name for abort errors.
func (e *AbortError) Name() string {
	return "AbortError"
}

func abortReason(ctx context.Context) error {
	if ctx == nil {
		return &AbortError{Message: "The operation was aborted"}
	}
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return &AbortError{Message: "The operation was aborted"}
}

// OperationSignal normalizes an optional context without imposing a deadline.
func OperationSignal(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

type abortRaceResult[T any] struct {
	val T
	err error
}

// RaceWithAbortSignal stops waiting on ctx cancellation while allowing an in-flight
// operation to settle without leaking goroutines or unhandled errors.
func RaceWithAbortSignal[T any](ctx context.Context, operation func() (T, error)) (T, error) {
	if ctx == nil {
		return operation()
	}
	if ctx.Err() != nil {
		go func() {
			_, _ = operation()
		}()
		var zero T
		return zero, abortReason(ctx)
	}

	ch := make(chan abortRaceResult[T], 1)
	go func() {
		val, err := operation()
		ch <- abortRaceResult[T]{val: val, err: err}
	}()

	select {
	case <-ctx.Done():
		var zero T
		return zero, abortReason(ctx)
	case res := <-ch:
		return res.val, res.err
	}
}
