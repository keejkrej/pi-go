// Ported from packages/protocol/src/codec.ts (pi v1.0.0).

package protocol

import (
	"errors"
	"fmt"
	"math"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/protocol/cbor"
)

// ProtocolValidationError is a protocol schema or framing failure. Name is "ProtocolValidationError".
type ProtocolValidationError struct {
	Message string
}

// NewProtocolValidationError returns an error with the given message.
func NewProtocolValidationError(message string) *ProtocolValidationError {
	return &ProtocolValidationError{Message: message}
}

func (e *ProtocolValidationError) Error() string { return e.Message }

func (e *ProtocolValidationError) Name() string { return "ProtocolValidationError" }

var _ error = (*ProtocolValidationError)(nil)

// ParseClientMessage validates value against ClientMessageSchema and chord.IsJsonValue.
// Failure is ProtocolValidationError "Invalid client protocol message".
func ParseClientMessage(value any) (ClientMessage, error) {
	candidate := value
	if cm, ok := value.(ClientMessage); ok {
		candidate = protClientMessageToValue(cm)
	}
	if !typebox.Check(ClientMessageSchema, candidate) || !chord.IsJsonValue(candidate) {
		return nil, NewProtocolValidationError("Invalid client protocol message")
	}
	if cm, ok := value.(ClientMessage); ok {
		if _, isUnknown := cm.(*UnknownClientMessage); !isUnknown {
			return cm, nil
		}
	}
	decoded, err := DecodeClientMessage(candidate)
	if err != nil {
		return nil, NewProtocolValidationError("Invalid client protocol message")
	}
	return decoded, nil
}

// ParseServerMessage validates value against ServerMessageSchema and chord.IsJsonValue.
// Failure is ProtocolValidationError "Invalid server protocol message".
func ParseServerMessage(value any) (ServerMessage, error) {
	candidate := value
	if sm, ok := value.(ServerMessage); ok {
		candidate = protServerMessageToValue(sm)
	}
	if !typebox.Check(ServerMessageSchema, candidate) || !chord.IsJsonValue(candidate) {
		return nil, NewProtocolValidationError("Invalid server protocol message")
	}
	if sm, ok := value.(ServerMessage); ok {
		if _, isUnknown := sm.(*UnknownServerMessage); !isUnknown {
			if _, isUnknownResp := sm.(*UnknownResponseEnvelope); !isUnknownResp {
				return sm, nil
			}
		}
	}
	decoded, err := DecodeServerMessage(candidate)
	if err != nil {
		return nil, NewProtocolValidationError("Invalid server protocol message")
	}
	return decoded, nil
}

// codecBoundedErrorMessage returns an Error message capped at 500 characters,
// or "Unknown codec error" when err is not an error.
func codecBoundedErrorMessage(err any) string {
	e, ok := err.(error)
	if !ok || e == nil {
		return "Unknown codec error"
	}
	msg := e.Error()
	if js.Len(msg) <= 500 {
		return msg
	}
	return js.Slice(msg, 0, 497) + "..."
}

// codecEncodeProtocolMessage validates value and encodes one length-prefixed CBOR frame.
// CBOR failures become ProtocolValidationError
// "Unable to encode <kind> protocol message: <bounded message>".
func codecEncodeProtocolMessage[T any](
	value T,
	toJsonx func(T) any,
	parse func(candidate any) (T, error),
	kind string,
	options *FrameDecoderOptions,
) ([]byte, error) {
	validated, err := parse(value)
	if err != nil {
		return nil, err
	}
	maxFrameLength := DefaultMaxFrameLength
	if options != nil && options.MaxFrameLength != nil {
		maxFrameLength = *options.MaxFrameLength
	}
	cborBytes, err := cbor.EncodeCbor(toJsonx(validated), &cbor.CborOptions{MaxByteLength: &maxFrameLength})
	if err != nil {
		var pve *ProtocolValidationError
		if errors.As(err, &pve) {
			return nil, err
		}
		return nil, NewProtocolValidationError(fmt.Sprintf("Unable to encode %s protocol message: %s", kind, codecBoundedErrorMessage(err)))
	}
	frame, err := EncodeFrame(cborBytes)
	if err != nil {
		var pve *ProtocolValidationError
		if errors.As(err, &pve) {
			return nil, err
		}
		return nil, NewProtocolValidationError(fmt.Sprintf("Unable to encode %s protocol message: %s", kind, codecBoundedErrorMessage(err)))
	}
	return frame, nil
}

// EncodeClientMessage validates and encodes one complete length-prefixed client message.
// A nil options pointer means the defaults.
func EncodeClientMessage(message ClientMessage, options *FrameDecoderOptions) ([]byte, error) {
	return codecEncodeProtocolMessage(message, protClientMessageToValue, ParseClientMessage, "client", options)
}

// EncodeServerMessage validates and encodes one complete length-prefixed server message.
// A nil options pointer means the defaults.
func EncodeServerMessage(message ServerMessage, options *FrameDecoderOptions) ([]byte, error) {
	return codecEncodeProtocolMessage(message, protServerMessageToValue, ParseServerMessage, "server", options)
}

// codecValidatedMessageDecoder is a framed, schema-checked message decoder.
type codecValidatedMessageDecoder[T any] struct {
	failed         bool
	frames         *FrameDecoder
	kind           string
	maxFrameLength int
	parse          func(candidate any) (T, error)
}

func codecNewValidatedMessageDecoder[T any](kind string, parse func(candidate any) (T, error), options *FrameDecoderOptions) (*codecValidatedMessageDecoder[T], error) {
	frames, err := NewFrameDecoder(options)
	if err != nil {
		return nil, err
	}
	maxFrameLength := DefaultMaxFrameLength
	if options != nil && options.MaxFrameLength != nil {
		maxFrameLength = *options.MaxFrameLength
	}
	return &codecValidatedMessageDecoder[T]{
		frames:         frames,
		kind:           kind,
		maxFrameLength: maxFrameLength,
		parse:          parse,
	}, nil
}

// Push decodes every frame completed by chunk.
// After failure, the error message is "<kind> message decoder has failed".
// Other failures become ProtocolValidationError "Invalid <kind> protocol frame: <bounded message>".
func (d *codecValidatedMessageDecoder[T]) Push(chunk []byte) ([]T, error) {
	if d.failed {
		return nil, NewProtocolValidationError(fmt.Sprintf("%s message decoder has failed", d.kind))
	}
	frames, err := d.frames.Push(chunk)
	if err != nil {
		d.failed = true
		var pve *ProtocolValidationError
		if errors.As(err, &pve) {
			return nil, err
		}
		return nil, NewProtocolValidationError(fmt.Sprintf("Invalid %s protocol frame: %s", d.kind, codecBoundedErrorMessage(err)))
	}
	messages := make([]T, 0, len(frames))
	for _, frame := range frames {
		maxLen := d.maxFrameLength
		decoded, err := cbor.DecodeCbor(frame, &cbor.CborOptions{MaxByteLength: &maxLen})
		if err != nil {
			d.failed = true
			return nil, NewProtocolValidationError(fmt.Sprintf("Invalid %s protocol frame: %s", d.kind, codecBoundedErrorMessage(err)))
		}
		msg, err := d.parse(decoded)
		if err != nil {
			d.failed = true
			var pve *ProtocolValidationError
			if errors.As(err, &pve) {
				return nil, err
			}
			return nil, NewProtocolValidationError(fmt.Sprintf("Invalid %s protocol frame: %s", d.kind, codecBoundedErrorMessage(err)))
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// End finishes the underlying frame decoder.
// After failure, the error message is "<kind> message decoder has failed".
// A framing failure becomes ProtocolValidationError "Invalid <kind> protocol framing: <bounded message>".
func (d *codecValidatedMessageDecoder[T]) End() error {
	if d.failed {
		return NewProtocolValidationError(fmt.Sprintf("%s message decoder has failed", d.kind))
	}
	if err := d.frames.End(); err != nil {
		d.failed = true
		return NewProtocolValidationError(fmt.Sprintf("Invalid %s protocol framing: %s", d.kind, codecBoundedErrorMessage(err)))
	}
	return nil
}

// ClientMessageDecoder incrementally decodes and validates framed client messages.
type ClientMessageDecoder struct {
	decoder *codecValidatedMessageDecoder[ClientMessage]
}

// NewClientMessageDecoder returns a decoder. A nil options pointer means the defaults.
func NewClientMessageDecoder(options *FrameDecoderOptions) (*ClientMessageDecoder, error) {
	dec, err := codecNewValidatedMessageDecoder("client", ParseClientMessage, options)
	if err != nil {
		return nil, err
	}
	return &ClientMessageDecoder{decoder: dec}, nil
}

// Push decodes every client message completed by chunk.
func (d *ClientMessageDecoder) Push(chunk []byte) ([]ClientMessage, error) {
	return d.decoder.Push(chunk)
}

// End finishes the client decoder.
func (d *ClientMessageDecoder) End() error {
	return d.decoder.End()
}

// ServerMessageDecoder incrementally decodes and validates framed server messages.
type ServerMessageDecoder struct {
	decoder *codecValidatedMessageDecoder[ServerMessage]
}

// NewServerMessageDecoder returns a decoder. A nil options pointer means the defaults.
func NewServerMessageDecoder(options *FrameDecoderOptions) (*ServerMessageDecoder, error) {
	dec, err := codecNewValidatedMessageDecoder("server", ParseServerMessage, options)
	if err != nil {
		return nil, err
	}
	return &ServerMessageDecoder{decoder: dec}, nil
}

// Push decodes every server message completed by chunk.
func (d *ServerMessageDecoder) Push(chunk []byte) ([]ServerMessage, error) {
	return d.decoder.Push(chunk)
}

// End finishes the server decoder.
func (d *ServerMessageDecoder) End() error {
	return d.decoder.End()
}

// IsSupportedProtocolVersion reports whether version is the integer ProtocolVersion.
// Non-integers, including 8.5, are false.
func IsSupportedProtocolVersion(version float64) bool {
	return !math.IsNaN(version) && !math.IsInf(version, 0) && math.Trunc(version) == version && version == float64(ProtocolVersion)
}
