// Ported from packages/codemode/src/wasm.ts (pi v1.0.0).

package codemode

import "github.com/keejkrej/pi-go/internal/quickjs"

// CodemodeWasmModule is a compiled quickjs-wasi module.
// TS types it as an opaque WebAssembly.Module.
type CodemodeWasmModule = *quickjs.Module

// LoadQuickJSWasm reads and compiles the QuickJS wasm once per path.
// A nil path uses the embedded quickjs.wasm (an empty path means the same).
// Pass a path when that file lives elsewhere. A failed load is retried on the next call.
func LoadQuickJSWasm(path *string) (CodemodeWasmModule, error) {
	panic("unported: LoadQuickJSWasm")
}
