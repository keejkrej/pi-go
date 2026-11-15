// Ported from packages/ai/src/lazy-bedrock-provider.ts (pi v1.5.2).

package ai

import "sync"

var (
	lbpMu   sync.RWMutex
	lbpHook func()
)

// SetLazyBedrockProviderHook registers the implementation callback invoked by
// RegisterLazyBedrockProvider. Higher layers (such as ai/api) wire it without
// creating an upward import from ai.
func SetLazyBedrockProviderHook(fn func()) {
	lbpMu.Lock()
	defer lbpMu.Unlock()
	lbpHook = fn
}

// RegisterLazyBedrockProvider invokes the registered Bedrock lazy-provider loader hook, if any.
func RegisterLazyBedrockProvider() {
	lbpMu.RLock()
	hook := lbpHook
	lbpMu.RUnlock()
	if hook != nil {
		hook()
	}
}
