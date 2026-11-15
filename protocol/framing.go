// Ported from packages/protocol/src/framing.ts (pi v1.0.0).

package protocol

import (
	"encoding/binary"
	"fmt"

	"github.com/keejkrej/pi-go/internal/js"
)

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
	return &FrameError{Message: message}
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
	value := DefaultMaxFrameLength
	if options != nil && options.MaxFrameLength != nil {
		value = *options.MaxFrameLength
	}
	if value < 0 || value > framMaxUint32 {
		return 0, js.NewRangeError(fmt.Sprintf("maxFrameLength must be an integer between 0 and %d", framMaxUint32))
	}
	return value, nil
}

// EncodeFrame prefixes a payload with its unsigned 32-bit big-endian byte length.
// A payload longer than framMaxUint32 returns an error whose message is
// "Frame payload exceeds the unsigned 32-bit length limit" (TS RangeError).
func EncodeFrame(payload []byte) ([]byte, error) {
	if len(payload) > framMaxUint32 {
		return nil, js.NewRangeError("Frame payload exceeds the unsigned 32-bit length limit")
	}
	frame := make([]byte, framFrameHeaderLength+len(payload))
	binary.BigEndian.PutUint32(frame[:framFrameHeaderLength], uint32(len(payload)))
	copy(frame[framFrameHeaderLength:], payload)
	return frame, nil
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
	maxFrameLength, err := framResolveMaxFrameLength(options)
	if err != nil {
		return nil, err
	}
	return &FrameDecoder{
		maxFrameLength: maxFrameLength,
		state:          framDecoderStateOpen,
	}, nil
}

// Push appends one chunk and returns every payload completed by it.
// After end, the error message is "Frame decoder has ended".
// After failure, the error message is "Frame decoder has failed".
func (d *FrameDecoder) Push(chunk []byte) ([][]byte, error) {
	if d.state == framDecoderStateEnded {
		return nil, NewFrameError("Frame decoder has ended")
	}
	if d.state == framDecoderStateFailed {
		return nil, NewFrameError("Frame decoder has failed")
	}

	frames := [][]byte{}
	chunkOffset := 0
	for chunkOffset < len(chunk) {
		if d.expectedPayloadLength == nil {
			headerBytes := framFrameHeaderLength - d.headerLength
			if rem := len(chunk) - chunkOffset; rem < headerBytes {
				headerBytes = rem
			}
			copy(d.header[d.headerLength:], chunk[chunkOffset:chunkOffset+headerBytes])
			d.headerLength += headerBytes
			chunkOffset += headerBytes
			if d.headerLength < framFrameHeaderLength {
				continue
			}

			frameLength := int(binary.BigEndian.Uint32(d.header[:]))
			d.headerLength = 0
			if frameLength > d.maxFrameLength {
				return nil, d.fail(fmt.Sprintf("Frame length %d exceeds configured limit of %d", frameLength, d.maxFrameLength))
			}
			if frameLength == 0 {
				frames = append(frames, make([]byte, 0))
				continue
			}
			d.expectedPayloadLength = &frameLength
			d.payloadBlocks = nil
			d.currentPayloadBlock = nil
			d.currentPayloadBlockLength = 0
			d.payloadLength = 0
		}

		if d.expectedPayloadLength == nil {
			continue
		}
		expectedPayloadLength := *d.expectedPayloadLength
		for chunkOffset < len(chunk) && d.payloadLength < expectedPayloadLength {
			block := d.currentPayloadBlock
			if block == nil || d.currentPayloadBlockLength == len(block) {
				blockSize := framPayloadBlockSize
				if rem := expectedPayloadLength - d.payloadLength; rem < blockSize {
					blockSize = rem
				}
				block = make([]byte, blockSize)
				d.payloadBlocks = append(d.payloadBlocks, block)
				d.currentPayloadBlock = block
				d.currentPayloadBlockLength = 0
			}
			payloadBytes := len(block) - d.currentPayloadBlockLength
			if rem := len(chunk) - chunkOffset; rem < payloadBytes {
				payloadBytes = rem
			}
			copy(block[d.currentPayloadBlockLength:], chunk[chunkOffset:chunkOffset+payloadBytes])
			d.currentPayloadBlockLength += payloadBytes
			d.payloadLength += payloadBytes
			chunkOffset += payloadBytes
		}
		if d.payloadLength == expectedPayloadLength {
			if len(d.payloadBlocks) == 1 {
				frames = append(frames, d.payloadBlocks[0])
			} else {
				payload := make([]byte, expectedPayloadLength)
				offset := 0
				for _, payloadBlock := range d.payloadBlocks {
					copy(payload[offset:], payloadBlock)
					offset += len(payloadBlock)
				}
				frames = append(frames, payload)
			}
			d.payloadBlocks = nil
			d.currentPayloadBlock = nil
			d.currentPayloadBlockLength = 0
			d.expectedPayloadLength = nil
			d.payloadLength = 0
		}
	}
	return frames, nil
}

// End marks the stream finished.
// A partial header or payload returns FrameError "Truncated frame at end of stream".
// After end, the error message is "Frame decoder has ended".
// After failure, the error message is "Frame decoder has failed".
func (d *FrameDecoder) End() error {
	if d.state == framDecoderStateEnded {
		return NewFrameError("Frame decoder has ended")
	}
	if d.state == framDecoderStateFailed {
		return NewFrameError("Frame decoder has failed")
	}
	if d.headerLength != 0 || d.expectedPayloadLength != nil {
		return d.fail("Truncated frame at end of stream")
	}
	d.state = framDecoderStateEnded
	return nil
}

func (d *FrameDecoder) fail(message string) error {
	d.state = framDecoderStateFailed
	d.headerLength = 0
	d.payloadBlocks = nil
	d.currentPayloadBlock = nil
	d.currentPayloadBlockLength = 0
	d.expectedPayloadLength = nil
	d.payloadLength = 0
	return NewFrameError(message)
}
