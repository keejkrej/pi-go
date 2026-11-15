// Ported from packages/protocol/src/cbor/encoder.ts (pi v1.0.0).

package cbor

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

const encoMaxSafeInteger = 9007199254740991 // (1 << 53) - 1

type encoSliceKey struct {
	ptr *any
	len int
}

// encoAncestors is the encode-time cycle set (TS Set<object>).
type encoAncestors struct {
	objects map[*jsonx.Object]struct{}
	slices  map[encoSliceKey]struct{}
}

// encoCborWriter is a growing definite-length CBOR buffer.
type encoCborWriter struct {
	buffer        []byte
	offset        int
	maxByteLength int
}

func encoNewCborWriter(maxByteLength int) *encoCborWriter {
	initCap := 256
	if maxByteLength < initCap {
		initCap = maxByteLength
	}
	if initCap < 0 {
		initCap = 0
	}
	return &encoCborWriter{
		buffer:        make([]byte, initCap),
		maxByteLength: maxByteLength,
	}
}

func (w *encoCborWriter) writeByte(value int) error {
	if err := w.ensureCapacity(1); err != nil {
		return err
	}
	w.buffer[w.offset] = byte(value)
	w.offset++
	return nil
}

func (w *encoCborWriter) WriteBytes(b []byte) error {
	if err := w.ensureCapacity(len(b)); err != nil {
		return err
	}
	copy(w.buffer[w.offset:], b)
	w.offset += len(b)
	return nil
}

func (w *encoCborWriter) WriteUint16(value int) error {
	if err := w.ensureCapacity(2); err != nil {
		return err
	}
	binary.BigEndian.PutUint16(w.buffer[w.offset:], uint16(value))
	w.offset += 2
	return nil
}

func (w *encoCborWriter) WriteUint32(value int) error {
	if err := w.ensureCapacity(4); err != nil {
		return err
	}
	binary.BigEndian.PutUint32(w.buffer[w.offset:], uint32(value))
	w.offset += 4
	return nil
}

func (w *encoCborWriter) WriteUint64(value int) error {
	high := value / Uint32Base
	low := value - high*Uint32Base
	if err := w.WriteUint32(high); err != nil {
		return err
	}
	return w.WriteUint32(low)
}

func (w *encoCborWriter) WriteFloat64(value float64) error {
	if err := w.ensureCapacity(9); err != nil {
		return err
	}
	w.buffer[w.offset] = 0xfb
	binary.BigEndian.PutUint64(w.buffer[w.offset+1:], math.Float64bits(value))
	w.offset += 9
	return nil
}

func (w *encoCborWriter) Finish() []byte {
	out := make([]byte, w.offset)
	copy(out, w.buffer[:w.offset])
	return out
}

func (w *encoCborWriter) ensureCapacity(additionalBytes int) error {
	if additionalBytes > w.maxByteLength-w.offset {
		return NewCborError(fmt.Sprintf("CBOR byte length exceeds configured limit of %d", w.maxByteLength))
	}
	required := w.offset + additionalBytes
	if required <= len(w.buffer) {
		return nil
	}
	capacity := len(w.buffer)
	if capacity < 1 {
		capacity = 1
	}
	for capacity < required {
		doubled := capacity * 2
		next := required
		if doubled > next {
			next = doubled
		}
		if next > w.maxByteLength {
			next = w.maxByteLength
		}
		capacity = next
	}
	expanded := make([]byte, capacity)
	copy(expanded, w.buffer[:w.offset])
	w.buffer = expanded
	return nil
}

func encoWriteArgument(writer *encoCborWriter, majorType int, value int) error {
	prefix := majorType << 5
	if value < 24 {
		return writer.writeByte(prefix | value)
	}
	if value <= 0xff {
		if err := writer.writeByte(prefix | 24); err != nil {
			return err
		}
		return writer.writeByte(value)
	}
	if value <= 0xffff {
		if err := writer.writeByte(prefix | 25); err != nil {
			return err
		}
		return writer.WriteUint16(value)
	}
	if value <= MaxUint32 {
		if err := writer.writeByte(prefix | 26); err != nil {
			return err
		}
		return writer.WriteUint32(value)
	}
	if err := writer.writeByte(prefix | 27); err != nil {
		return err
	}
	return writer.WriteUint64(value)
}

func encoIsPlainObject(value any) bool {
	obj, ok := value.(*jsonx.Object)
	return ok && obj != nil
}

func encoEncodeText(writer *encoCborWriter, value string, options ResolvedCborOptions) error {
	encoded := TextEncoder.Encode(value)
	if len(encoded) > options.MaxByteLength {
		return NewCborError(fmt.Sprintf("CBOR text string length exceeds configured limit of %d", options.MaxByteLength))
	}
	decoded, err := TextDecoder.Decode(encoded)
	if err != nil || decoded != value {
		return NewCborError("CBOR text strings must contain valid Unicode scalar values")
	}
	if err := encoWriteArgument(writer, 3, len(encoded)); err != nil {
		return err
	}
	return writer.WriteBytes(encoded)
}

func encoEncodeValue(writer *encoCborWriter, value any, options ResolvedCborOptions, depth int, ancestors *encoAncestors) error {
	if depth > options.MaxDepth {
		return NewCborError(fmt.Sprintf("CBOR nesting depth exceeds configured limit of %d", options.MaxDepth))
	}
	if value == nil {
		return writer.writeByte(0xf6)
	}
	switch v := value.(type) {
	case bool:
		if v {
			return writer.writeByte(0xf5)
		}
		return writer.writeByte(0xf4)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return NewCborError("CBOR numbers must be finite")
		}
		isNegZero := v == 0 && math.Signbit(v)
		if math.Trunc(v) == v && !isNegZero {
			if math.Abs(v) > encoMaxSafeInteger {
				return NewCborError("CBOR integers must be safe JavaScript integers")
			}
			if v >= 0 {
				return encoWriteArgument(writer, 0, int(v))
			}
			return encoWriteArgument(writer, 1, int(-1-int64(v)))
		}
		return writer.WriteFloat64(v)
	case string:
		return encoEncodeText(writer, v, options)
	case []byte:
		if len(v) > options.MaxByteLength {
			return NewCborError(fmt.Sprintf("CBOR byte string length exceeds configured limit of %d", options.MaxByteLength))
		}
		if err := encoWriteArgument(writer, 2, len(v)); err != nil {
			return err
		}
		return writer.WriteBytes(v)
	case []any:
		if v == nil {
			return NewCborError("Unsupported CBOR value type: []any(nil)")
		}
		var sliceKey encoSliceKey
		if len(v) > 0 {
			if ancestors.slices == nil {
				ancestors.slices = make(map[encoSliceKey]struct{})
			}
			sliceKey = encoSliceKey{ptr: unsafe.SliceData(v), len: len(v)}
			if _, seen := ancestors.slices[sliceKey]; seen {
				return NewCborError("CBOR values must not contain cycles")
			}
		}
		if len(v) > options.MaxContainerLength {
			return NewCborError(fmt.Sprintf("CBOR array length exceeds configured limit of %d", options.MaxContainerLength))
		}
		if len(v) > 0 {
			ancestors.slices[sliceKey] = struct{}{}
			defer delete(ancestors.slices, sliceKey)
		}
		if err := encoWriteArgument(writer, 4, len(v)); err != nil {
			return err
		}
		for _, elem := range v {
			if err := encoEncodeValue(writer, elem, options, depth+1, ancestors); err != nil {
				return err
			}
		}
		return nil
	case *jsonx.Object:
		if !encoIsPlainObject(v) {
			return NewCborError("Unsupported CBOR value type: object")
		}
		if ancestors.objects == nil {
			ancestors.objects = make(map[*jsonx.Object]struct{})
		}
		if _, seen := ancestors.objects[v]; seen {
			return NewCborError("CBOR values must not contain cycles")
		}
		if v.Len() > options.MaxContainerLength {
			return NewCborError(fmt.Sprintf("CBOR map length exceeds configured limit of %d", options.MaxContainerLength))
		}
		ancestors.objects[v] = struct{}{}
		defer delete(ancestors.objects, v)
		if err := encoWriteArgument(writer, 5, v.Len()); err != nil {
			return err
		}
		for key, entryVal := range v.All() {
			if err := encoEncodeText(writer, key, options); err != nil {
				return err
			}
			if err := encoEncodeValue(writer, entryVal, options, depth+1, ancestors); err != nil {
				return err
			}
		}
		return nil
	default:
		return NewCborError(fmt.Sprintf("Unsupported CBOR value type: %T", value))
	}
}

// EncodeCbor encodes the protocol's strict, definite-length RFC 8949 subset.
// A nil options pointer means the defaults.
// Accepted values are nil, bool, float64 (finite; integers are float64, and -0 is preserved),
// string, []byte, []any, and *jsonx.Object. Other types return CborError
// "Unsupported CBOR value type: <typeof>".
func EncodeCbor(value any, options *CborOptions) ([]byte, error) {
	resolved, err := ResolveOptions(options)
	if err != nil {
		return nil, err
	}
	writer := encoNewCborWriter(resolved.MaxByteLength)
	if err := encoEncodeValue(writer, value, resolved, 0, &encoAncestors{}); err != nil {
		return nil, err
	}
	return writer.Finish(), nil
}
