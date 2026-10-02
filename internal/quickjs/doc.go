// Ported from node_modules/quickjs-wasi/dist/index.js (quickjs-wasi 3.6.2).

// Package quickjs is a Go port of the quickjs-wasi 3.6.2 JavaScript glue
// (dist/index.js, dist/wasi-shim.js) that runs the embedded, unmodified
// quickjs-wasi quickjs.wasm (QuickJS-NG compiled to WASI) on wazero.
//
// One [QuickJS] value owns one wasm module instance with its own linear memory,
// runtime and context, exactly like QuickJS.create() in TS. Values inside the VM
// are reached through [JSValueHandle]s. Script exceptions surface as
// [*JSException] errors.
//
// Threading: a VM is not safe for concurrent use. All calls must come from one
// goroutine at a time. Host callbacks run on the calling goroutine, nested inside
// the wasm call that triggered them, and may re-enter the VM. The only
// cross-goroutine controls are the context passed to [QuickJSCreate] (cancelling
// it aborts a running call and closes the instance) and an
// [QuickJSOptions.InterruptHandler] that polls shared state.
//
// Strings: Go strings cross the boundary as raw bytes. Well-formed UTF-8 is
// UTF-8; guest strings that contain lone UTF-16 surrogates come back as WTF-8
// (each lone surrogate as its 3-byte generalized UTF-8 sequence), and passing
// such a string back in restores the original guest string exactly. This is the
// Go analog of the TS glue's WTF-8 encode/decode.
//
// Not ported: native extensions (ExtensionDescriptor, .so dynamic linking),
// because wazero cannot share an indirect function table with host-created
// globals; and host Promise conversion in hostToHandle, because Go has no
// Promise type. Snapshots still carry and validate extension metadata.
package quickjs
