// Ported from packages/coding-agent/test/model-runtime-test-utils.ts (pi v1.0.0).

package testkit

import (
	"context"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

var (
	mrtuMu       sync.RWMutex
	mrtuRuntimes = map[*core.ModelRegistry]*core.ModelRuntime{}
)

func mrtuWrap(runtime *core.ModelRuntime) *core.ModelRegistry {
	registry := core.NewModelRegistry(runtime)
	mrtuMu.Lock()
	mrtuRuntimes[registry] = runtime
	mrtuMu.Unlock()
	return registry
}

// CreateModelRegistry loads optional models.json configuration without introducing file-backed
// catalog locks into unit tests. Nil modelsPath uses defaults; &"" disables models.json.
func CreateModelRegistry(ctx context.Context, credentials ai.CredentialStore, modelsPath ...*string) (*core.ModelRegistry, error) {
	var pathPtr *string
	if len(modelsPath) > 0 {
		pathPtr = modelsPath[0]
	}
	runtime, err := core.CreateModelRuntime(ctx, &core.CreateModelRuntimeOptions{
		Credentials:       credentials,
		ModelsPath:        pathPtr,
		ModelsStore:       core.NewInMemoryCodingAgentModelsStore(),
		AllowModelNetwork: false,
	})
	if err != nil {
		return nil, err
	}
	return mrtuWrap(runtime), nil
}

// CreateInMemoryModelRegistry creates an in-memory ModelRegistry with no models.json file backing.
func CreateInMemoryModelRegistry(ctx context.Context, credentials ai.CredentialStore) (*core.ModelRegistry, error) {
	noModelsPath := ""
	runtime, err := core.CreateModelRuntime(ctx, &core.CreateModelRuntimeOptions{
		Credentials:       credentials,
		ModelsPath:        &noModelsPath,
		AllowModelNetwork: false,
	})
	if err != nil {
		return nil, err
	}
	return mrtuWrap(runtime), nil
}

// GetModelRuntime returns the underlying *core.ModelRuntime created for modelRegistry by test helpers.
func GetModelRuntime(modelRegistry *core.ModelRegistry) (*core.ModelRuntime, error) {
	if modelRegistry == nil {
		return nil, fmt.Errorf("ModelRegistry was not created by the test helper")
	}
	mrtuMu.RLock()
	runtime := mrtuRuntimes[modelRegistry]
	mrtuMu.RUnlock()
	if runtime == nil {
		return nil, fmt.Errorf("ModelRegistry was not created by the test helper")
	}
	return runtime, nil
}
