// Ported from packages/protocol/src/framing.ts (pi v1.0.0).

package protocol

const (
	framFrameHeaderLength = 4
	framMaxUint32         = 0xFFFF_FFFF
	framPayloadBlockSize  = 64 * 1024
)

// DefaultMaxFrameLength is the default upper bound for one framed CBOR payload.
const DefaultMaxFrameLength = 16 * 1024 * 1024

// FrameDecoderOptions configures a frame decoder.
// A nil *FrameDecoderOptions means the defaults.
type FrameDecoderOptions struct {
	// MaxFrameLength is the maximum payload length, excluding the 4-byte header.
	MaxFrameLength *int `json:"maxFrameLength,omitzero"`
}

// FrameError is a length-prefix framing failure. Name is "FrameError".
type FrameError struct {
	Message string
}

// NewFrameError returns an error with the given message.
func NewFrameError(message string) *FrameError {
	panic("unported: NewFrameError")
}

func (e *FrameError) Error() string { return e.Message }

func (e *FrameError) Name() string { return "FrameError" }

var _ error = (*FrameError)(nil)

// framDecoderState is the frame decoder lifecycle.
type framDecoderState string

const (
	framDecoderStateOpen   framDecoderState = "open"
	framDecoderStateEnded  framDecoderState = "ended"
	framDecoderStateFailed framDecoderState = "failed"
)

// framResolveMaxFrameLength applies the default and checks the limit.
// The error message is "maxFrameLength must be an integer between 0 and <max>" (TS RangeError).
func framResolveMaxFrameLength(options *FrameDecoderOptions) (int, error) {
	panic("unported: framResolveMaxFrameLength")
}

// EncodeFrame prefixes a payload with its unsigned 32-bit big-endian byte length.
// A payload longer than framMaxUint32 returns an error whose message is
// "Frame payload exceeds the unsigned 32-bit length limit" (TS RangeError).
func EncodeFrame(payload []byte) ([]byte, error) {
	panic("unported: EncodeFrame")
}

// FrameDecoder incrementally splits arbitrary byte chunks into length-prefixed payloads.
type FrameDecoder struct {
	header                    [framFrameHeaderLength]byte
	headerLength              int
	maxFrameLength            int
	payloadBlocks             [][]byte
	currentPayloadBlock       []byte
	currentPayloadBlockLength int
	expectedPayloadLength     *int
	payloadLength             int
	state                     framDecoderState
}

// NewFrameDecoder returns a decoder. A nil options pointer means the defaults.
// An invalid maxFrameLength returns the RangeError from framResolveMaxFrameLength.
func NewFrameDecoder(options *FrameDecoderOptions) (*FrameDecoder, error) {
	panic("unported: NewFrameDecoder")
}

// Push appends one chunk and returns every payload completed by it.
// After end, the error message is "Frame decoder has ended".
// After failure, the error message is "Frame decoder has failed".
func (d *FrameDecoder) Push(chunk []byte) ([][]byte, error) {
	panic("unported: FrameDecoder.Push")
}

// End marks the stream finished.
// A partial header or payload returns FrameError "Truncated frame at end of stream".
// After end, the error message is "Frame decoder has ended".
// After failure, the error message is "Frame decoder has failed".
func (d *FrameDecoder) End() error {
	panic("unported: FrameDecoder.End")
}

func (d *FrameDecoder) fail(message string) error {
	panic("unported: FrameDecoder.fail")
}
