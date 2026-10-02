// Ported from packages/protocol/src/cbor/options.ts (pi v1.0.0).

package cbor

import (
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
)

// Uint32Base is 2^32, the split between the high and low halves of a 64-bit CBOR argument.
const Uint32Base = 0x1_0000_0000

// MaxUint32 is the maximum unsigned 32-bit integer.
const MaxUint32 = 0xFFFF_FFFF

// optiMaxConfiguredDepth is the maximum maxDepth a caller may configure.
const optiMaxConfiguredDepth = 512

// Safe defaults for untrusted protocol payloads.
const (
	// DefaultMaxCborByteLength is the default maximum encoded size and byte or text string length.
	DefaultMaxCborByteLength = 16 * 1024 * 1024
	// DefaultMaxCborContainerLength is the default maximum array or map length.
	DefaultMaxCborContainerLength = 1_000_000
	// DefaultMaxCborDepth is the default maximum recursive item depth.
	DefaultMaxCborDepth = 64
)

// CborOptions limits decoding and encoding.
// A nil *CborOptions means the defaults.
// Field order is maxByteLength, maxContainerLength, maxDepth.
type CborOptions struct {
	// MaxByteLength is the maximum encoded input or output size and the maximum byte or text string length.
	MaxByteLength *int `json:"maxByteLength,omitzero"`
	// MaxContainerLength is the maximum number of elements in an array or entries in a map.
	MaxContainerLength *int `json:"maxContainerLength,omitzero"`
	// MaxDepth is the maximum recursive item depth.
	MaxDepth *int `json:"maxDepth,omitzero"`
}

// ResolvedCborOptions is CborOptions with defaults applied.
// Field order is maxByteLength, maxContainerLength, maxDepth.
type ResolvedCborOptions struct {
	MaxByteLength      int `json:"maxByteLength"`
	MaxContainerLength int `json:"maxContainerLength"`
	MaxDepth           int `json:"maxDepth"`
}

// CborError is a CBOR encode or decode failure. Name is "CborError".
type CborError struct {
	Message string
}

// NewCborError returns an error with the given message.
func NewCborError(message string) *CborError {
	panic("unported: NewCborError")
}

func (e *CborError) Error() string { return e.Message }

func (e *CborError) Name() string { return "CborError" }

var _ error = (*CborError)(nil)

// optiTextEncoder is new TextEncoder().
type optiTextEncoder struct{}

// Encode is textEncoder.encode. Lone surrogates become U+FFFD.
func (e *optiTextEncoder) Encode(value string) []byte {
	panic("unported: TextEncoder.Encode")
}

// TextEncoder is new TextEncoder().
var TextEncoder = &optiTextEncoder{}

// optiTextDecoder is new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).
// mu guards dec. The TS decoder is one shared object; Go calls may overlap.
type optiTextDecoder struct {
	mu  sync.Mutex
	dec *js.UTF8StreamDecoder
}

// Decode is textDecoder.decode(bytes). Fatal UTF-8 errors are returned.
func (d *optiTextDecoder) Decode(b []byte) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dec.DecodeChunk(b, false)
}

// TextDecoder is new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).
var TextDecoder = &optiTextDecoder{dec: js.NewTextDecoder(&js.TextDecoderOptions{Fatal: true, IgnoreBOM: true})}

// optiResolveLimit checks one configured limit.
// The error message is "<name> must be an integer between 0 and <maximum>" (TS RangeError).
func optiResolveLimit(name string, value int, maximum int) (int, error) {
	panic("unported: optiResolveLimit")
}

// ResolveOptions fills defaults and checks each limit.
// A nil options pointer means the defaults.
// A limit that is not an integer in range returns an error whose message is
// "<name> must be an integer between 0 and <maximum>" (TS RangeError).
func ResolveOptions(options *CborOptions) (ResolvedCborOptions, error) {
	panic("unported: ResolveOptions")
}
