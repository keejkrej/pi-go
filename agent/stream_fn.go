// Ported from packages/agent/src/stream-fn.ts (pi v1.5.2).

package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/ai"
)

var (
	sfMu              sync.RWMutex
	sfDefaultStreamFn StreamFn = sfUnconfiguredStreamFn
)

func sfUnconfiguredStreamFn(
	_ context.Context,
	model *ai.Model,
	_ *ai.TranscriptContext,
	_ *ai.SimpleStreamOptions,
) (*ai.AssistantMessageEventStream, error) {
	provider := ""
	id := ""
	if model != nil {
		provider = string(model.Provider)
		id = model.Id
	}
	return nil, fmt.Errorf("no default StreamFn configured for %s/%s; call agent.SetDefaultStreamFn or pass StreamFn", provider, id)
}

// SetDefaultStreamFn sets the package-wide fallback StreamFn used when an Agent
// or AgentLoop caller does not specify one. Passing nil restores the built-in
// unconfigured fallback.
func SetDefaultStreamFn(fn StreamFn) {
	sfMu.Lock()
	defer sfMu.Unlock()
	if fn == nil {
		sfDefaultStreamFn = sfUnconfiguredStreamFn
		return
	}
	sfDefaultStreamFn = fn
}

// GetDefaultStreamFn returns the current package-wide default StreamFn.
func GetDefaultStreamFn() StreamFn {
	sfMu.RLock()
	defer sfMu.RUnlock()
	return sfDefaultStreamFn
}
