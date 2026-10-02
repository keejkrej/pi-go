// Ported from packages/codemode/src/runtime/worker.ts (pi v1.0.0).

package runtime

import (
	"context"

	"github.com/keejkrej/pi-go/internal/quickjs"
)

// workerRun runs one script inside a fresh QuickJS VM and relays tool calls and output
// to the host. One VM per run. The host cancels ctx when the script settles, times out,
// or is aborted; cancelling ctx closes the wasm instance. data.Interrupt is also polled
// by the VM interrupt handler.
//
// toHost receives worker messages. toWorker delivers host results. The VM is not safe
// for concurrent use: settle runs on this goroutine, not on a second one.
// A VM failure outside the script is posted as WorkerCrashMessage.
func workerRun(ctx context.Context, data *WorkerData, toHost chan<- WorkerToHostMessage, toWorker <-chan *HostToWorkerMessage) {
	panic("unported: workerRun")
}

// workerDiscardOutput drops QuickJS engine diagnostics on fd 1 and 2.
// Reporting every byte as written keeps libc from retrying. It is the Wasi option
// passed to quickjs.QuickJSCreate.
func workerDiscardOutput(memory *quickjs.Memory) *quickjs.WasiImports {
	panic("unported: workerDiscardOutput")
}

// workerDescribeException encodes a guest exception as ScriptErrorJson
// ({ name, message, stack }).
func workerDescribeException(err *quickjs.JSException) string {
	panic("unported: workerDescribeException")
}

// workerCrash posts a crash message. err is formatted as "Name: message" when it has a name.
func workerCrash(toHost chan<- WorkerToHostMessage, err error) {
	panic("unported: workerCrash")
}
