// Ported from packages/chord/test/context.test.ts (pi v1.0.0).

package chord

import (
	"context"
	"errors"
	"testing"
)

func TestContext(t *testing.T) {
	t.Run("provides distinct empty root contexts", func(t *testing.T) {
		key := CreateContextKey[string]("value")

		if TodoContext == BackgroundContext {
			t.Fatalf("expected TodoContext and BackgroundContext to be distinct")
		}
		if TodoContext.AbortSignal() != nil {
			t.Fatalf("expected TodoContext.AbortSignal() to be nil")
		}
		if v, ok := TodoContext.Value(key); ok || v != nil {
			t.Fatalf("expected TodoContext.Value(key) to be absent, got (%#v, %v)", v, ok)
		}
		if got := BackgroundContext.String(); got != "[Context BACKGROUND_CONTEXT]" {
			t.Fatalf("got %q, want %q", got, "[Context BACKGROUND_CONTEXT]")
		}
		if got := TodoContext.String(); got != "[Context TODO_CONTEXT]" {
			t.Fatalf("got %q, want %q", got, "[Context TODO_CONTEXT]")
		}
	})

	t.Run("layers typed values without modifying parents", func(t *testing.T) {
		firstKey := CreateContextKey[string]("first")
		secondKey := CreateContextKey[float64]("second")
		dupFirstKey := CreateContextKey[string]("first")

		first := WithContextValue(firstKey, "one", BackgroundContext)
		second := WithContextValue(secondKey, 2, first)
		replaced := WithContextValue(firstKey, "updated", second)

		if _, ok := BackgroundContext.Value(firstKey); ok {
			t.Fatalf("expected BackgroundContext not to have firstKey")
		}
		if v, ok := first.Value(firstKey); !ok || v != "one" {
			t.Fatalf("expected firstKey = one, got (%#v, %v)", v, ok)
		}
		if _, ok := first.Value(dupFirstKey); ok {
			t.Fatalf("expected distinct key with same description not to match")
		}
		if _, ok := first.Value(secondKey); ok {
			t.Fatalf("expected first not to have secondKey")
		}
		if v, ok := second.Value(firstKey); !ok || v != "one" {
			t.Fatalf("expected second.Value(firstKey) = one, got (%#v, %v)", v, ok)
		}
		if v, ok := second.Value(secondKey); !ok || v != float64(2) {
			t.Fatalf("expected second.Value(secondKey) = 2, got (%#v, %v)", v, ok)
		}
		if v, ok := replaced.Value(firstKey); !ok || v != "updated" {
			t.Fatalf("expected replaced.Value(firstKey) = updated, got (%#v, %v)", v, ok)
		}
		if v, ok := second.Value(firstKey); !ok || v != "one" {
			t.Fatalf("expected second parent to remain unchanged, got (%#v, %v)", v, ok)
		}
		wantStr := "[Context BACKGROUND_CONTEXT].WithValue(first).WithValue(second).WithValue(first)"
		if got := replaced.String(); got != wantStr {
			t.Fatalf("got %q, want %q", got, wantStr)
		}
	})

	t.Run("inherits parent cancellation and isolates child cancellation", func(t *testing.T) {
		parentCtx, parentCancel := context.WithCancelCause(context.Background())
		defer parentCancel(nil)

		parent := WithAbortSignal(parentCtx, BackgroundContext)
		child := WithCancel(parent)
		sibling := WithCancel(parent)

		childListenerDone := make(chan struct{})
		stop := context.AfterFunc(child.Context.AbortSignal(), func() {
			close(childListenerDone)
		})
		defer stop()

		child.Cancel("child")
		if child.Context.AbortSignal().Err() == nil {
			t.Fatalf("expected child signal to be cancelled")
		}
		if cause := context.Cause(child.Context.AbortSignal()); cause == nil || cause.Error() != "child" {
			t.Fatalf("expected child cause 'child', got %v", cause)
		}
		if sibling.Context.AbortSignal().Err() != nil {
			t.Fatalf("expected sibling not to be cancelled")
		}
		if parent.AbortSignal().Err() != nil {
			t.Fatalf("expected parent not to be cancelled")
		}
		<-childListenerDone

		parentCancel(&ciAbortReasonError{reason: "parent"})
		if sibling.Context.AbortSignal().Err() == nil {
			t.Fatalf("expected sibling signal to be cancelled when parent cancels")
		}
		if cause := context.Cause(sibling.Context.AbortSignal()); cause == nil || cause.Error() != "parent" {
			t.Fatalf("expected sibling cause 'parent', got %v", cause)
		}
	})

	t.Run("masks caller cancellation for mandatory cleanup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		key := CreateContextKey[string]("value")
		callCtx := WithContextValue(key, "preserved", WithAbortSignal(ctx, BackgroundContext))
		cleanup := WithoutAbortSignal(callCtx)

		cancel()
		if callCtx.AbortSignal() == nil || callCtx.AbortSignal().Err() == nil {
			t.Fatalf("expected callCtx to be cancelled")
		}
		if cleanup.AbortSignal() != nil {
			t.Fatalf("expected cleanup AbortSignal to be nil")
		}
		if v, ok := cleanup.Value(key); !ok || v != "preserved" {
			t.Fatalf("expected preserved value, got (%#v, %v)", v, ok)
		}
	})

	t.Run("stops waiting when the invocation is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		callCtx := WithAbortSignal(ctx, BackgroundContext)

		releaseWork := make(chan struct{})
		workStarted := make(chan struct{})
		workResult := make(chan string, 1)
		go func() {
			<-releaseWork
			workResult <- "completed later"
		}()

		waitingDone := make(chan error, 1)
		go func() {
			_, err := AwaitWithContext(func() (string, error) {
				close(workStarted)
				v := <-workResult
				workResult <- v
				return v, nil
			}, callCtx)
			waitingDone <- err
		}()

		<-workStarted
		cancellation := errors.New("cancelled")
		cancel(cancellation)

		if err := <-waitingDone; err != cancellation {
			t.Fatalf("expected cancellation error %v, got %v", cancellation, err)
		}
		close(releaseWork)
		if got := <-workResult; got != "completed later" {
			t.Fatalf("expected underlying work to still complete, got %q", got)
		}

		res, err := AwaitWithContext(func() (string, error) {
			return "completed", nil
		}, BackgroundContext)
		if err != nil || res != "completed" {
			t.Fatalf("expected completed without error, got (%q, %v)", res, err)
		}
	})
}
