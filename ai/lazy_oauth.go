// Ported from packages/ai/src/lazy-oauth.ts (pi v1.5.2).

package ai

import "sync"

var (
	loMu   sync.RWMutex
	loHook func()
)

// SetLazyOAuthFlowsHook registers the implementation callback invoked by
// RegisterLazyOAuthFlows. Higher layers (such as ai/oauth) wire it without
// creating an upward import from ai.
func SetLazyOAuthFlowsHook(fn func()) {
	loMu.Lock()
	defer loMu.Unlock()
	loHook = fn
}

// RegisterLazyOAuthFlows invokes the registered OAuth lazy-loader hook, if any.
func RegisterLazyOAuthFlows() {
	loMu.RLock()
	hook := loHook
	loMu.RUnlock()
	if hook != nil {
		hook()
	}
}
