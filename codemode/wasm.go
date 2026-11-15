// Ported from packages/codemode/src/wasm.ts (pi v1.0.0).

package codemode

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/keejkrej/pi-go/internal/quickjs"
)

// CodemodeWasmModule is a compiled quickjs-wasi module.
// TS types it as an opaque WebAssembly.Module.
type CodemodeWasmModule = *quickjs.Module

var (
	wasmCacheMu sync.Mutex
	wasmCache   = make(map[string]CodemodeWasmModule)
)

// LoadQuickJSWasm reads and compiles the QuickJS wasm once per path.
// A nil path uses the embedded quickjs.wasm (an empty path means the same).
// Pass a path when that file lives elsewhere. A failed load is retried on the next call.
func LoadQuickJSWasm(path *string) (CodemodeWasmModule, error) {
	resolved := ""
	key := ""
	if path != nil && *path != "" {
		resolved = *path
		if abs, err := filepath.Abs(resolved); err == nil {
			key = abs
		} else {
			key = resolved
		}
	}

	wasmCacheMu.Lock()
	defer wasmCacheMu.Unlock()

	if cached, ok := wasmCache[key]; ok {
		return cached, nil
	}

	var (
		mod CodemodeWasmModule
		err error
	)
	if resolved == "" {
		mod, err = quickjs.EmbeddedModule(context.Background())
	} else {
		var data []byte
		data, err = os.ReadFile(resolved)
		if err == nil {
			mod, err = quickjs.CompileModule(context.Background(), data)
		}
	}
	if err != nil {
		return nil, err
	}
	wasmCache[key] = mod
	return mod, nil
}
