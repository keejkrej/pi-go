// Ported from packages/ai/src/session-resources.ts (pi v1.5.2).

package ai

import "sync"

// SessionResourceCleanup releases resources tied to one session.
// A nil sessionId means every session, matching an omitted argument.
type SessionResourceCleanup func(sessionId *string)

type srRegisteredCleanup struct {
	id      uint64
	cleanup SessionResourceCleanup
}

// AggregateError collects multiple failures thrown by CleanupSessionResources.
type AggregateError struct {
	Message string
	Errors  []error
}

func (e *AggregateError) Error() string   { return e.Message }
func (e *AggregateError) Name() string    { return "AggregateError" }
func (e *AggregateError) Unwrap() []error { return e.Errors }

var (
	srMu       sync.Mutex
	srNextID   uint64
	srCleanups []srRegisteredCleanup
)

// RegisterSessionResourceCleanup registers cleanup and returns an unsubscribe
// function. Cleanups run in registration order.
func RegisterSessionResourceCleanup(cleanup SessionResourceCleanup) func() {
	if cleanup == nil {
		return func() {}
	}
	srMu.Lock()
	srNextID++
	id := srNextID
	srCleanups = append(srCleanups, srRegisteredCleanup{id: id, cleanup: cleanup})
	srMu.Unlock()

	return func() {
		srMu.Lock()
		for i, entry := range srCleanups {
			if entry.id == id {
				srCleanups = append(srCleanups[:i], srCleanups[i+1:]...)
				break
			}
		}
		srMu.Unlock()
	}
}

// CleanupSessionResources runs every registered cleanup.
// Failures are collected; when any cleanup throws, the result is an error
// whose message is "Failed to cleanup session resources".
func CleanupSessionResources(sessionId *string) error {
	srMu.Lock()
	snapshot := make([]SessionResourceCleanup, len(srCleanups))
	for i, entry := range srCleanups {
		snapshot[i] = entry.cleanup
	}
	srMu.Unlock()

	var errs []error
	for _, fn := range snapshot {
		func() {
			defer func() {
				if r := recover(); r != nil {
					if err, ok := r.(error); ok {
						errs = append(errs, err)
					} else {
						errs = append(errs, &srPanicError{value: r})
					}
				}
			}()
			fn(sessionId)
		}()
	}
	if len(errs) > 0 {
		return &AggregateError{
			Message: "Failed to cleanup session resources",
			Errors:  errs,
		}
	}
	return nil
}

type srPanicError struct {
	value any
}

func (e *srPanicError) Error() string { return FormatThrownValue(e.value) }
