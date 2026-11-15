// Ported from packages/coding-agent/test/model-catalog-refresh.test.ts (pi v1.5.2).

package components

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/omap"
)

type tmcrMockRuntime struct {
	fn func(ctx context.Context, options *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error)
}

func (m *tmcrMockRuntime) Refresh(ctx context.Context, options *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error) {
	return m.fn(ctx, options)
}

func tmcrSuccessfulRefresh() ai.ModelsRefreshResult {
	return ai.ModelsRefreshResult{
		Aborted: false,
		Errors:  omap.NewMap[string, error](),
	}
}

func TestRefreshModelCatalogs(t *testing.T) {
	t.Run("shares one runtime refresh between concurrent callers", func(t *testing.T) {
		var calls atomic.Int32
		gate := make(chan struct{})
		runtime := &tmcrMockRuntime{
			fn: func(ctx context.Context, _ *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error) {
				calls.Add(1)
				<-gate
				return tmcrSuccessfulRefresh(), nil
			},
		}

		ctx1, cancel1 := context.WithCancel(context.Background())
		defer cancel1()
		ctx2, cancel2 := context.WithCancel(context.Background())
		defer cancel2()

		type resPair struct {
			res ai.ModelsRefreshResult
			err error
		}
		ch1 := make(chan resPair, 1)
		ch2 := make(chan resPair, 1)

		go func() {
			r, err := RefreshModelCatalogs(ctx1, runtime)
			ch1 <- resPair{r, err}
		}()
		go func() {
			r, err := RefreshModelCatalogs(ctx2, runtime)
			ch2 <- resPair{r, err}
		}()

		// Give both goroutines a moment to join the active call before releasing gate.
		time.Sleep(10 * time.Millisecond)
		if got := calls.Load(); got != 1 {
			t.Fatalf("calls = %d, want 1", got)
		}
		close(gate)

		r1 := <-ch1
		r2 := <-ch2
		if r1.err != nil || r2.err != nil {
			t.Fatalf("unexpected errors: %v, %v", r1.err, r2.err)
		}
		if r1.res.Aborted || r2.res.Aborted {
			t.Fatalf("expected non-aborted results")
		}
	})

	t.Run("keeps the shared refresh alive when one caller stops waiting", func(t *testing.T) {
		gate := make(chan struct{})
		started := make(chan context.Context, 1)
		runtime := &tmcrMockRuntime{
			fn: func(ctx context.Context, _ *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error) {
				started <- ctx
				<-gate
				return tmcrSuccessfulRefresh(), nil
			},
		}

		ctx1, cancel1 := context.WithCancel(context.Background())
		ctx2, cancel2 := context.WithCancel(context.Background())
		defer cancel2()

		errCh1 := make(chan error, 1)
		resCh2 := make(chan error, 1)

		go func() {
			_, err := RefreshModelCatalogs(ctx1, runtime)
			errCh1 <- err
		}()
		refreshCtx := <-started
		go func() {
			_, err := RefreshModelCatalogs(ctx2, runtime)
			resCh2 <- err
		}()
		time.Sleep(10 * time.Millisecond)

		cancel1()
		if err := <-errCh1; !errors.Is(err, context.Canceled) {
			t.Fatalf("caller1 err = %v, want context.Canceled", err)
		}
		if refreshCtx.Err() != nil {
			t.Fatalf("expected shared refresh context not aborted yet")
		}

		close(gate)
		if err := <-resCh2; err != nil {
			t.Fatalf("caller2 err = %v, want nil", err)
		}
	})

	t.Run("aborts an abandoned refresh and allows a later refresh to start", func(t *testing.T) {
		var calls atomic.Int32
		ctxCh := make(chan context.Context, 2)
		runtime := &tmcrMockRuntime{
			fn: func(ctx context.Context, _ *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error) {
				calls.Add(1)
				ctxCh <- ctx
				<-ctx.Done()
				return ai.ModelsRefreshResult{Aborted: true, Errors: omap.NewMap[string, error]()}, nil
			},
		}

		ctx1, cancel1 := context.WithCancel(context.Background())
		errCh1 := make(chan error, 1)
		go func() {
			_, err := RefreshModelCatalogs(ctx1, runtime)
			errCh1 <- err
		}()

		refreshCtx1 := <-ctxCh
		cancel1()
		if err := <-errCh1; !errors.Is(err, context.Canceled) {
			t.Fatalf("caller1 err = %v, want context.Canceled", err)
		}
		select {
		case <-refreshCtx1.Done():
		case <-time.After(time.Second):
			t.Fatalf("expected first refresh context to abort")
		}

		ctx2, cancel2 := context.WithCancel(context.Background())
		errCh2 := make(chan error, 1)
		go func() {
			_, err := RefreshModelCatalogs(ctx2, runtime)
			errCh2 <- err
		}()

		<-ctxCh
		if got := calls.Load(); got != 2 {
			t.Fatalf("calls = %d, want 2", got)
		}
		cancel2()
		if err := <-errCh2; !errors.Is(err, context.Canceled) {
			t.Fatalf("caller2 err = %v, want context.Canceled", err)
		}
	})
}
