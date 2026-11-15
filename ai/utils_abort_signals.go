// Ported from packages/ai/src/utils/abort-signals.ts (pi v1.5.2).

package ai

import (
	"context"
	"sync"
	"time"
)

// CombinedAbortSignal merges zero or more contexts into a single cancelable signal with cleanup.
type CombinedAbortSignal struct {
	Ctx     context.Context
	Cleanup func()
}

type uasMergedContext struct {
	mu   sync.Mutex
	done chan struct{}
	err  error
}

func (m *uasMergedContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (m *uasMergedContext) Value(any) any               { return nil }
func (m *uasMergedContext) Done() <-chan struct{}       { return m.done }

func (m *uasMergedContext) Err() error {
	m.mu.Lock()
	err := m.err
	m.mu.Unlock()
	return err
}

func (m *uasMergedContext) abort(cause error) {
	if cause == nil {
		cause = AbortNewError("The operation was aborted")
	}
	m.mu.Lock()
	if m.err != nil {
		m.mu.Unlock()
		return
	}
	m.err = cause
	close(m.done)
	m.mu.Unlock()
}

// CombineAbortSignals mirrors TS `combineAbortSignals(signals)`.
// Nil contexts are ignored. Zero non-nil contexts yields a nil Ctx and no-op Cleanup;
// one non-nil context returns it directly with a no-op Cleanup; multiple contexts derive
// a merged context canceled as soon as any input context completes, and Cleanup detaches listeners.
func CombineAbortSignals(signals ...context.Context) CombinedAbortSignal {
	active := make([]context.Context, 0, len(signals))
	for _, s := range signals {
		if s != nil {
			active = append(active, s)
		}
	}
	if len(active) == 0 {
		return CombinedAbortSignal{Cleanup: func() {}}
	}
	if len(active) == 1 {
		return CombinedAbortSignal{Ctx: active[0], Cleanup: func() {}}
	}

	merged := &uasMergedContext{done: make(chan struct{})}
	stops := make([]func() bool, 0, len(active))
	for _, sig := range active {
		s := sig
		if s.Err() != nil {
			merged.abort(uaAbortReason(s))
			break
		}
		stop := context.AfterFunc(s, func() {
			merged.abort(uaAbortReason(s))
		})
		stops = append(stops, stop)
		if s.Err() != nil {
			merged.abort(uaAbortReason(s))
			break
		}
	}
	return CombinedAbortSignal{
		Ctx: merged,
		Cleanup: func() {
			for _, stop := range stops {
				stop()
			}
		},
	}
}
