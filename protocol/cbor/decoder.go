// Ported from packages/protocol/src/cbor/decoder.ts (pi v1.0.0).

package cbor

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

const decoMaxSafeInteger = 9007199254740991 // (1 << 53) - 1

// decoCborReader decodes one definite-length CBOR item.
// Field order follows the TS constructor fields.
type decoCborReader struct {
	bytes   []byte
	offset  int
	options ResolvedCborOptions
}

func decoNewCborReader(bytes []byte, options ResolvedCborOptions) *decoCborReader {
	return &decoCborReader{
		bytes:   bytes,
		options: options,
	}
}

func (r *decoCborReader) Decode() (any, error) {
	value, err := r.readItem(0)
	if err != nil {
		return nil, err
	}
	if r.offset != len(r.bytes) {
		return nil, NewCborError("CBOR payload contains trailing data")
	}
	return value, nil
}

func (r *decoCborReader) readItem(depth int) (any, error) {
	if depth > r.options.MaxDepth {
		return nil, NewCborError(fmt.Sprintf("CBOR nesting depth exceeds configured limit of %d", r.options.MaxDepth))
	}
	initial, err := r.readByte()
	if err != nil {
		return nil, err
	}
	majorType := initial >> 5
	additionalInformation := initial & 0x1f

	switch majorType {
	case 0:
		arg, err := r.readArgument(additionalInformation)
		if err != nil {
			return nil, err
		}
		return float64(arg), nil
	case 1:
		arg, err := r.readArgument(additionalInformation)
		if err != nil {
			return nil, err
		}
		if arg >= decoMaxSafeInteger {
			return nil, NewCborError("Decoded CBOR integer is outside the safe range")
		}
		return float64(-1 - int64(arg)), nil
	case 2:
		length, err := r.readLength(additionalInformation, "byte string", r.options.MaxByteLength)
		if err != nil {
			return nil, err
		}
		raw, err := r.readBytes(length)
		if err != nil {
			return nil, err
		}
		out := make([]byte, length)
		copy(out, raw)
		return out, nil
	case 3:
		length, err := r.readLength(additionalInformation, "text string", r.options.MaxByteLength)
		if err != nil {
			return nil, err
		}
		raw, err := r.readBytes(length)
		if err != nil {
			return nil, err
		}
		decoded, err := TextDecoder.Decode(raw)
		if err != nil {
			return nil, NewCborError("CBOR text string contains invalid UTF-8")
		}
		return decoded, nil
	case 4:
		length, err := r.readLength(additionalInformation, "array", r.options.MaxContainerLength)
		if err != nil {
			return nil, err
		}
		initCap := length
		if initCap > 1024 {
			initCap = 1024
		}
		result := make([]any, 0, initCap)
		for range length {
			item, err := r.readItem(depth + 1)
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
		return result, nil
	case 5:
		length, err := r.readLength(additionalInformation, "map", r.options.MaxContainerLength)
		if err != nil {
			return nil, err
		}
		result := jsonx.NewObject()
		for range length {
			keyAny, err := r.readItem(depth + 1)
			if err != nil {
				return nil, err
			}
			key, ok := keyAny.(string)
			if !ok {
				return nil, NewCborError("CBOR map keys must be strings")
			}
			if result.Has(key) {
				return nil, NewCborError("CBOR map contains a duplicate key")
			}
			val, err := r.readItem(depth + 1)
			if err != nil {
				return nil, err
			}
			result.Set(key, val)
		}
		return result, nil
	case 6:
		return nil, NewCborError("CBOR tags are not supported")
	case 7:
		return r.readSimple(additionalInformation)
	default:
		return nil, NewCborError("Malformed CBOR major type")
	}
}

func (r *decoCborReader) readSimple(additionalInformation int) (any, error) {
	switch additionalInformation {
	case 20:
		return false, nil
	case 21:
		return true, nil
	case 22:
		return nil, nil
	case 27:
		raw, err := r.readBytes(8)
		if err != nil {
			return nil, err
		}
		value := math.Float64frombits(binary.BigEndian.Uint64(raw))
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, NewCborError("Decoded CBOR number must be finite")
		}
		isNegZero := value == 0 && math.Signbit(value)
		if math.Trunc(value) == value && !isNegZero && math.Abs(value) > decoMaxSafeInteger {
			return nil, NewCborError("Decoded CBOR integer is outside the safe range")
		}
		return value, nil
	case 31:
		return nil, NewCborError("CBOR break marker is not supported")
	default:
		return nil, NewCborError("Unsupported CBOR simple value or floating-point width")
	}
}

func (r *decoCborReader) readLength(additionalInformation int, kind string, limit int) (int, error) {
	if additionalInformation == 31 {
		return 0, NewCborError(fmt.Sprintf("Indefinite-length CBOR %ss are not supported", kind))
	}
	length, err := r.readArgument(additionalInformation)
	if err != nil {
		return 0, err
	}
	if length > limit {
		return 0, NewCborError(fmt.Sprintf("CBOR %s length exceeds configured limit of %d", kind, limit))
	}
	return length, nil
}

func (r *decoCborReader) readArgument(additionalInformation int) (int, error) {
	if additionalInformation < 24 {
		return additionalInformation, nil
	}
	switch additionalInformation {
	case 24:
		return r.readByte()
	case 25:
		raw, err := r.readBytes(2)
		if err != nil {
			return 0, err
		}
		return int(binary.BigEndian.Uint16(raw)), nil
	case 26:
		raw, err := r.readBytes(4)
		if err != nil {
			return 0, err
		}
		return int(binary.BigEndian.Uint32(raw)), nil
	case 27:
		raw, err := r.readBytes(8)
		if err != nil {
			return 0, err
		}
		high := binary.BigEndian.Uint32(raw[:4])
		low := binary.BigEndian.Uint32(raw[4:])
		if high > 0x1f_ffff {
			return 0, NewCborError("Decoded CBOR integer or length is outside the safe range")
		}
		return int(uint64(high)<<32 | uint64(low)), nil
	case 31:
		return 0, NewCborError("Indefinite-length CBOR items are not supported")
	default:
		return 0, NewCborError("Malformed CBOR additional information")
	}
}

func (r *decoCborReader) readByte() (int, error) {
	if r.offset >= len(r.bytes) {
		return 0, NewCborError("Truncated CBOR payload")
	}
	value := r.bytes[r.offset]
	r.offset++
	return int(value), nil
}

// readBytes returns the next length bytes. TS returns a subarray view and advances offset by length.
func (r *decoCborReader) readBytes(length int) ([]byte, error) {
	if length > len(r.bytes)-r.offset {
		return nil, NewCborError("Truncated CBOR payload")
	}
	value := r.bytes[r.offset : r.offset+length]
	r.offset += length
	return value, nil
}

// DecodeCbor decodes exactly one item from the protocol's strict RFC 8949 subset.
// A nil options pointer means the defaults.
// Numbers are float64, including integers and -0. Maps are *jsonx.Object in insertion
// order with string keys. Arrays are []any. Byte strings are []byte. Null is nil.
// A non-empty tail returns CborError "CBOR payload contains trailing data".
func DecodeCbor(bytes []byte, options *CborOptions) (any, error) {
	resolved, err := ResolveOptions(options)
	if err != nil {
		return nil, err
	}
	if len(bytes) > resolved.MaxByteLength {
		return nil, NewCborError(fmt.Sprintf("CBOR byte length exceeds configured limit of %d", resolved.MaxByteLength))
	}
	return decoNewCborReader(bytes, resolved).Decode()
}
