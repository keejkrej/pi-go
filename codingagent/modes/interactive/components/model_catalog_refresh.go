// Ported from packages/coding-agent/src/modes/interactive/model-catalog-refresh.ts (pi v1.5.2).

package components

import (
	"context"
	"sync"

	"github.com/keejkrej/pi-go/ai"
)

// ModelCatalogRuntime abstracts ModelRuntime.Refresh for shared catalog refreshes.
type ModelCatalogRuntime interface {
	Refresh(ctx context.Context, options *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error)
}

type mcrActiveRefresh struct {
	cancel  context.CancelFunc
	done    chan struct{}
	waiters int
	res     ai.ModelsRefreshResult
	err     error
}

type mcrCoordinator struct {
	mu     sync.Mutex
	active map[ModelCatalogRuntime]*mcrActiveRefresh
}

var mcrGlobalCoordinator = &mcrCoordinator{
	active: make(map[ModelCatalogRuntime]*mcrActiveRefresh),
}

// RefreshModelCatalogs shares concurrent interactive all-catalog refreshes per runtime
// while keeping each caller's cancellation independent.
func RefreshModelCatalogs(ctx context.Context, modelRuntime ModelCatalogRuntime) (ai.ModelsRefreshResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ai.ModelsRefreshResult{}, err
	}

	c := mcrGlobalCoordinator
	c.mu.Lock()
	active := c.active[modelRuntime]
	if active == nil {
		refreshCtx, cancel := context.WithCancel(context.Background())
		created := &mcrActiveRefresh{
			cancel: cancel,
			done:   make(chan struct{}),
		}
		active = created
		c.active[modelRuntime] = active
		go func() {
			res, err := modelRuntime.Refresh(refreshCtx, &ai.ModelsRefreshOptions{})
			c.mu.Lock()
			if c.active[modelRuntime] == created {
				delete(c.active, modelRuntime)
			}
			created.res = res
			created.err = err
			close(created.done)
			c.mu.Unlock()
		}()
	}
	active.waiters++
	c.mu.Unlock()

	cleanupWaiter := func() {
		c.mu.Lock()
		active.waiters--
		if active.waiters == 0 && c.active[modelRuntime] == active {
			delete(c.active, modelRuntime)
			cancel := active.cancel
			c.mu.Unlock()
			cancel()
			return
		}
		c.mu.Unlock()
	}

	select {
	case <-active.done:
		cleanupWaiter()
		if err := ctx.Err(); err != nil {
			return ai.ModelsRefreshResult{}, err
		}
		return active.res, active.err
	case <-ctx.Done():
		cleanupWaiter()
		return ai.ModelsRefreshResult{}, ctx.Err()
	}
}
