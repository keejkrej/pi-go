// Ported from packages/ai/src/utils/abort.ts (pi v1.5.2).

package ai

import "context"

type uaAbortError struct {
	message string
}

func (e *uaAbortError) Error() string { return e.message }
func (e *uaAbortError) Name() string  { return "AbortError" }
func (e *uaAbortError) Unwrap() error { return context.Canceled }

// AbortNewError creates an error whose message is msg, whose JS error name is "AbortError",
// and which matches errors.Is(err, context.Canceled) (PORTING.md §7.1 & §8).
func AbortNewError(msg string) error {
	if msg == "" {
		msg = "The operation was aborted"
	}
	return &uaAbortError{message: msg}
}

func uaAbortReason(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil && cause != ctx.Err() {
		return cause
	}
	if ctx.Err() == context.DeadlineExceeded {
		return ctx.Err()
	}
	return AbortNewError("The operation was aborted")
}

// OperationSignal returns a non-nil context (ctx when non-nil, otherwise context.Background()).
func OperationSignal(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

// RaceWithAbortSignal stops waiting for operation when ctx aborts, returning the abort error
// while continuing to observe the abandoned goroutine so any completion/error is consumed cleanly.
func RaceWithAbortSignal[T any](ctx context.Context, operation func() (T, error)) (T, error) {
	var zero T
	if ctx != nil && ctx.Err() != nil {
		go func() {
			_, _ = operation()
		}()
		return zero, uaAbortReason(ctx)
	}

	type uaResult struct {
		val T
		err error
	}
	ch := make(chan uaResult, 1)
	go func() {
		v, err := operation()
		ch <- uaResult{val: v, err: err}
	}()

	if ctx == nil {
		res := <-ch
		return res.val, res.err
	}
	select {
	case res := <-ch:
		return res.val, res.err
	case <-ctx.Done():
		return zero, uaAbortReason(ctx)
	}
}
