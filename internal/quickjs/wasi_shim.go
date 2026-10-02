// Ported from node_modules/quickjs-wasi/dist/wasi-shim.js (quickjs-wasi 3.6.2).

// Minimal WASI shim for running QuickJS on wazero.
//
// Implements the subset of WASI snapshot_preview1 that QuickJS needs:
//   - clock_time_get (for Date.now() and Math.random() PRNG seeding)
//   - fd_write (for QuickJS runtime internal logging)
//   - fd_close (stub)
//   - fd_fdstat_get (stub)
//   - fd_seek (stub)
//   - random_get (for WASI libc init)
//
// Users can override any of these by providing their own implementations
// in the Wasi option when creating a VM.

package quickjs

import (
	"crypto/rand"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/tetratelabs/wazero/api"
)

// WASI error codes.
const (
	wasiErrnoSuccess = 0
	wasiErrnoBadf    = 8
	wasiErrnoNosys   = 52
)

// WASI clock IDs.
const (
	wasiClockRealtime  = 0
	wasiClockMonotonic = 1
)

// Memory gives WASI overrides access to the VM's linear memory
// (TS: the WebAssembly.Memory proxy passed to the WasiOptions factory). It is
// usable once the instance exists, i.e. from inside the WASI functions.
type Memory struct {
	mem api.Memory
}

// Buffer returns the current linear memory contents (TS: memory.buffer). The
// slice aliases the memory and becomes stale when the memory grows; fetch it
// again on every use. nil before instantiation.
func (m *Memory) Buffer() []byte {
	if m.mem == nil {
		return nil
	}
	buf, _ := m.mem.Read(0, m.mem.Size())
	return buf
}

// API returns the underlying wazero memory, nil before instantiation.
func (m *Memory) API() api.Memory {
	return m.mem
}

// WasiImports holds wasi_snapshot_preview1 function implementations. Nil
// fields keep the built-in implementation. Each returns a WASI errno.
type WasiImports struct {
	ClockTimeGet func(clockID uint32, precision uint64, resultPtr uint32) uint32
	FdWrite      func(fd, iovsPtr, iovsLen, nwrittenPtr uint32) uint32
	FdClose      func(fd uint32) uint32
	FdFdstatGet  func(fd, statPtr uint32) uint32
	FdSeek       func(fd uint32, offset int64, whence, resultPtr uint32) uint32
	RandomGet    func(bufPtr, bufLen uint32) uint32
}

// WasiOptions builds custom WASI function implementations for a VM. It receives
// the VM's memory (usable once the functions run) and returns the overrides.
type WasiOptions func(memory *Memory) *WasiImports

// merge returns the receiver with every non-nil function of overrides applied
// (TS: { ...wasiBuiltins, ...wasiUserOverrides }).
func (w *WasiImports) merge(overrides *WasiImports) *WasiImports {
	merged := *w
	if overrides == nil {
		return &merged
	}
	if overrides.ClockTimeGet != nil {
		merged.ClockTimeGet = overrides.ClockTimeGet
	}
	if overrides.FdWrite != nil {
		merged.FdWrite = overrides.FdWrite
	}
	if overrides.FdClose != nil {
		merged.FdClose = overrides.FdClose
	}
	if overrides.FdFdstatGet != nil {
		merged.FdFdstatGet = overrides.FdFdstatGet
	}
	if overrides.FdSeek != nil {
		merged.FdSeek = overrides.FdSeek
	}
	if overrides.RandomGet != nil {
		merged.RandomGet = overrides.RandomGet
	}
	return &merged
}

// wasiStdout and wasiStderr are where the built-in fd_write sends fds 1 and 2.
var (
	wasiStdout io.Writer = os.Stdout
	wasiStderr io.Writer = os.Stderr
)

// createWasiShim returns the wasi_snapshot_preview1 imports with built-in
// defaults. Memory accesses that fall outside linear memory panic with a
// RangeError, like the DataView accesses in TS; the panic aborts the wasm call.
func createWasiShim(memoryAccessor func() api.Memory) *WasiImports {
	return &WasiImports{
		ClockTimeGet: func(clockID uint32, _ uint64, resultPtr uint32) uint32 {
			mem := memoryAccessor()
			if clockID == wasiClockRealtime || clockID == wasiClockMonotonic {
				timeNs := uint64(time.Now().UnixMilli()) * 1000000
				if !mem.WriteUint64Le(resultPtr, timeNs) {
					panic(rangeErrorOutOfBounds())
				}
				return wasiErrnoSuccess
			}
			return wasiErrnoNosys
		},
		FdWrite: func(fd, iovsPtr, iovsLen, nwrittenPtr uint32) uint32 {
			mem := memoryAccessor()
			var totalWritten uint32
			if fd != 1 && fd != 2 {
				return wasiErrnoBadf
			}
			for i := range iovsLen {
				bufPtr, ok1 := mem.ReadUint32Le(iovsPtr + i*8)
				bufLen, ok2 := mem.ReadUint32Le(iovsPtr + i*8 + 4)
				if !ok1 || !ok2 {
					panic(rangeErrorOutOfBounds())
				}
				// TS: bytes.slice(bufPtr, bufPtr + bufLen), clamped to the buffer.
				size := mem.Size()
				start := min(bufPtr, size)
				end := uint32(min(uint64(bufPtr)+uint64(bufLen), uint64(size)))
				chunk, _ := mem.Read(start, end-start)
				text := textDecode(chunk)
				if fd == 1 {
					_, _ = io.WriteString(wasiStdout, text)
				} else {
					_, _ = io.WriteString(wasiStderr, text)
				}
				totalWritten += bufLen
			}
			if !mem.WriteUint32Le(nwrittenPtr, totalWritten) {
				panic(rangeErrorOutOfBounds())
			}
			return wasiErrnoSuccess
		},
		FdClose: func(uint32) uint32 {
			return wasiErrnoNosys
		},
		FdFdstatGet: func(fd, statPtr uint32) uint32 {
			mem := memoryAccessor()
			if fd == 1 || fd == 2 {
				var stat [24]byte
				stat[0] = 2 // fs_filetype = CHARACTER_DEVICE
				// fs_flags (u16 at +2), fs_rights_base (u64 at +8) and
				// fs_rights_inheriting (u64 at +16) are all zero.
				if !mem.WriteByte(statPtr, stat[0]) ||
					!mem.Write(statPtr+2, stat[2:4]) ||
					!mem.Write(statPtr+8, stat[8:24]) {
					panic(rangeErrorOutOfBounds())
				}
				return wasiErrnoSuccess
			}
			return wasiErrnoBadf
		},
		FdSeek: func(uint32, int64, uint32, uint32) uint32 {
			return wasiErrnoNosys
		},
		RandomGet: func(bufPtr, bufLen uint32) uint32 {
			mem := memoryAccessor()
			buf, ok := mem.Read(bufPtr, bufLen)
			if !ok {
				panic(&HostError{name: "RangeError", message: "Invalid typed array length: " + strconv.FormatUint(uint64(bufLen), 10)})
			}
			// crypto.getRandomValues refuses more than 65536 bytes (message
			// text from node v24).
			if bufLen > 65536 {
				panic(&HostError{name: "QuotaExceededError", message: "The requested length exceeds 65,536 bytes"})
			}
			_, _ = rand.Read(buf)
			return wasiErrnoSuccess
		},
	}
}
