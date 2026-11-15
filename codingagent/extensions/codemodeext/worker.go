// Ported from packages/coding-agent/src/extensions/codemode/worker.ts (pi v1.0.0).

// Worker execution for codemode in Go runs in-process inside the wazero
// QuickJS WebAssembly runtime (package codemode/runtime) rather than spawning
// an external Node worker_threads or Bun worker entrypoint.
package codemodeext

const workerInProcess = true
