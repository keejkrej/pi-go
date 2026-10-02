// Ported from node_modules/quickjs-wasi/dist/index.js (quickjs-wasi 3.6.2).

package quickjs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"

	"github.com/tetratelabs/wazero/api"
)

// SnapshotExtension is the metadata of a native extension loaded into a
// snapshotted VM (TS: SnapshotExtension). This port does not load extensions,
// so its own snapshots never carry any; the type exists for format
// compatibility with snapshots produced by the TS glue.
type SnapshotExtension struct {
	Name       string
	MemoryBase int
	TableBase  int
	InitFn     string
}

// Snapshot is the complete VM state: the WASM linear memory plus the pointers
// needed to resume (TS: Snapshot).
type Snapshot struct {
	Memory       []byte
	StackPointer int
	RuntimePtr   int
	ContextPtr   int
	Extensions   []SnapshotExtension
}

// Snapshot snapshots the entire VM state.
//
// It returns a snapshot containing a copy of the full WASM linear memory. Use
// QuickJSSerializeSnapshot() to convert it to a versioned binary buffer for
// persistent storage.
func (vm *QuickJS) Snapshot() (*Snapshot, error) {
	if err := vm.assertNotDisposed(); err != nil {
		return nil, err
	}
	if vm.failure != nil {
		return nil, vm.failure
	}
	memory := vm.readBytes(0, vm.memory.Size())
	var stackPointer int
	if g := vm.mod.ExportedGlobal("__stack_pointer"); g != nil {
		stackPointer = int(api.DecodeI32(g.Get()))
	}
	runtimePtr := int(int32(vm.call(exQjsGetRuntimePtr)))
	contextPtr := int(int32(vm.call(exQjsGetContextPtr)))
	if vm.failure != nil {
		return nil, vm.failure
	}
	return &Snapshot{
		Memory:       memory,
		StackPointer: stackPointer,
		RuntimePtr:   runtimePtr,
		ContextPtr:   contextPtr,
		Extensions:   []SnapshotExtension{},
	}, nil
}

// QuickJSSerializeSnapshot serializes a snapshot to a binary buffer for
// persistent storage (TS: QuickJS.serializeSnapshot).
//
// The format includes a versioned header followed by the raw memory. Apply
// your own compression (gzip, zstd, etc.) on top for smaller storage. The
// memory compresses well due to its large zero regions. See snapshotHeaderSize
// for the layout.
func QuickJSSerializeSnapshot(snapshot *Snapshot) []byte {
	// Calculate the extension metadata size.
	extMetaSize := 4 // extCount (u32)
	for _, ext := range snapshot.Extensions {
		extMetaSize += 4 + len(ext.Name) + 4 + 4 + 4 + len(ext.InitFn)
	}
	totalSize := snapshotHeaderSize + extMetaSize + len(snapshot.Memory)
	bytes := make([]byte, totalSize)
	// Header.
	binary.BigEndian.PutUint32(bytes[0:], snapshotMagic) // big-endian for readability in hex
	bytes[4] = snapshotVersion
	// bytes 5-7 are reserved (already zero)
	binary.LittleEndian.PutUint32(bytes[8:], uint32(len(snapshot.Memory)))
	binary.LittleEndian.PutUint32(bytes[12:], uint32(snapshot.StackPointer))
	binary.LittleEndian.PutUint32(bytes[16:], uint32(snapshot.RuntimePtr))
	binary.LittleEndian.PutUint32(bytes[20:], uint32(snapshot.ContextPtr))
	// Extension metadata (version 2).
	offset := snapshotHeaderSize
	binary.LittleEndian.PutUint32(bytes[offset:], uint32(len(snapshot.Extensions)))
	offset += 4
	for _, ext := range snapshot.Extensions {
		binary.LittleEndian.PutUint32(bytes[offset:], uint32(len(ext.Name)))
		offset += 4
		offset += copy(bytes[offset:], ext.Name)
		binary.LittleEndian.PutUint32(bytes[offset:], uint32(ext.MemoryBase))
		offset += 4
		binary.LittleEndian.PutUint32(bytes[offset:], uint32(ext.TableBase))
		offset += 4
		binary.LittleEndian.PutUint32(bytes[offset:], uint32(len(ext.InitFn)))
		offset += 4
		offset += copy(bytes[offset:], ext.InitFn)
	}
	// Memory data.
	copy(bytes[offset:], snapshot.Memory)
	return bytes
}

// errDataViewBounds is the RangeError a DataView read past the end raises.
var errDataViewBounds = &HostError{name: "RangeError", message: "Offset is outside the bounds of the DataView"}

// QuickJSDeserializeSnapshot deserializes a snapshot from a binary buffer
// produced by QuickJSSerializeSnapshot() (TS: QuickJS.deserializeSnapshot).
// The returned memory is a copy.
func QuickJSDeserializeSnapshot(data []byte) (*Snapshot, error) {
	if len(data) < snapshotHeaderSize {
		return nil, errors.New("Invalid snapshot: too small")
	}
	getUint32 := func(offset int) (uint32, error) {
		if offset < 0 || offset+4 > len(data) {
			return 0, errDataViewBounds
		}
		return binary.LittleEndian.Uint32(data[offset:]), nil
	}
	// slice is data.slice(start, end) (clamped).
	slice := func(start, end int) []byte {
		start = min(start, len(data))
		end = max(min(end, len(data)), start)
		return data[start:end]
	}
	// Validate magic.
	magic := binary.BigEndian.Uint32(data[0:])
	if magic != snapshotMagic {
		return nil, fmt.Errorf("Invalid snapshot: bad magic (expected 0x%s, got 0x%s)",
			strconv.FormatUint(snapshotMagic, 16), strconv.FormatUint(uint64(magic), 16))
	}
	// Validate version.
	version := data[4]
	if version != snapshotVersion && version != 1 {
		return nil, fmt.Errorf("Unsupported snapshot version: %d (expected %d)", version, snapshotVersion)
	}
	memorySize := binary.LittleEndian.Uint32(data[8:])
	stackPointer := binary.LittleEndian.Uint32(data[12:])
	runtimePtr := binary.LittleEndian.Uint32(data[16:])
	contextPtr := binary.LittleEndian.Uint32(data[20:])
	extensions := []SnapshotExtension{}
	memoryOffset := snapshotHeaderSize
	if version >= 2 {
		// Version 2 adds extension metadata between the header and the memory data.
		extCount, err := getUint32(24)
		if err != nil {
			return nil, err
		}
		offset := 28
		for range extCount {
			// name length (u32) + name (utf8) + memoryBase (u32) + tableBase (u32) + initFn length (u32) + initFn (utf8)
			nameLen, err := getUint32(offset)
			if err != nil {
				return nil, err
			}
			offset += 4
			name := textDecode(slice(offset, offset+int(nameLen)))
			offset += int(nameLen)
			memBase, err := getUint32(offset)
			if err != nil {
				return nil, err
			}
			offset += 4
			tblBase, err := getUint32(offset)
			if err != nil {
				return nil, err
			}
			offset += 4
			initFnLen, err := getUint32(offset)
			if err != nil {
				return nil, err
			}
			offset += 4
			initFn := textDecode(slice(offset, offset+int(initFnLen)))
			offset += int(initFnLen)
			extensions = append(extensions, SnapshotExtension{Name: name, MemoryBase: int(memBase), TableBase: int(tblBase), InitFn: initFn})
		}
		memoryOffset = offset
	}
	expectedSize := memoryOffset + int(memorySize)
	if len(data) < expectedSize {
		return nil, fmt.Errorf("Invalid snapshot: expected %d bytes, got %d", expectedSize, len(data))
	}
	memory := append([]byte(nil), data[memoryOffset:memoryOffset+int(memorySize)]...)
	return &Snapshot{
		Memory:       memory,
		StackPointer: int(stackPointer),
		RuntimePtr:   int(runtimePtr),
		ContextPtr:   int(contextPtr),
		Extensions:   extensions,
	}, nil
}
