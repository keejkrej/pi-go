// Ported from packages/protocol/src/codec.ts (pi v1.0.0).

package protocol

// ProtocolValidationError is a protocol schema or framing failure. Name is "ProtocolValidationError".
type ProtocolValidationError struct {
	Message string
}

// NewProtocolValidationError returns an error with the given message.
func NewProtocolValidationError(message string) *ProtocolValidationError {
	panic("unported: NewProtocolValidationError")
}

func (e *ProtocolValidationError) Error() string { return e.Message }

func (e *ProtocolValidationError) Name() string { return "ProtocolValidationError" }

var _ error = (*ProtocolValidationError)(nil)

// ParseClientMessage validates value against ClientMessageSchema and chord.IsJsonValue.
// Failure is ProtocolValidationError "Invalid client protocol message".
func ParseClientMessage(value any) (ClientMessage, error) {
	panic("unported: ParseClientMessage")
}

// ParseServerMessage validates value against ServerMessageSchema and chord.IsJsonValue.
// Failure is ProtocolValidationError "Invalid server protocol message".
func ParseServerMessage(value any) (ServerMessage, error) {
	panic("unported: ParseServerMessage")
}

// codecBoundedErrorMessage returns an Error message capped at 500 characters,
// or "Unknown codec error" when err is not an error.
func codecBoundedErrorMessage(err any) string {
	panic("unported: codecBoundedErrorMessage")
}

// codecEncodeProtocolMessage validates value and encodes one length-prefixed CBOR frame.
// CBOR failures become ProtocolValidationError
// "Unable to encode <kind> protocol message: <bounded message>".
func codecEncodeProtocolMessage[T any](value T, parse func(candidate any) (T, error), kind string, options *FrameDecoderOptions) ([]byte, error) {
	panic("unported: codecEncodeProtocolMessage")
}

// EncodeClientMessage validates and encodes one complete length-prefixed client message.
// A nil options pointer means the defaults.
func EncodeClientMessage(message ClientMessage, options *FrameDecoderOptions) ([]byte, error) {
	panic("unported: EncodeClientMessage")
}

// EncodeServerMessage validates and encodes one complete length-prefixed server message.
// A nil options pointer means the defaults.
func EncodeServerMessage(message ServerMessage, options *FrameDecoderOptions) ([]byte, error) {
	panic("unported: EncodeServerMessage")
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
	panic("unported: codecNewValidatedMessageDecoder")
}

// Push decodes every frame completed by chunk.
// After failure, the error message is "<kind> message decoder has failed".
// Other failures become ProtocolValidationError "Invalid <kind> protocol frame: <bounded message>".
func (d *codecValidatedMessageDecoder[T]) Push(chunk []byte) ([]T, error) {
	panic("unported: codecValidatedMessageDecoder.Push")
}

// End finishes the underlying frame decoder.
// After failure, the error message is "<kind> message decoder has failed".
// A framing failure becomes ProtocolValidationError "Invalid <kind> protocol framing: <bounded message>".
func (d *codecValidatedMessageDecoder[T]) End() error {
	panic("unported: codecValidatedMessageDecoder.End")
}

// ClientMessageDecoder incrementally decodes and validates framed client messages.
type ClientMessageDecoder struct {
	decoder *codecValidatedMessageDecoder[ClientMessage]
}

// NewClientMessageDecoder returns a decoder. A nil options pointer means the defaults.
func NewClientMessageDecoder(options *FrameDecoderOptions) (*ClientMessageDecoder, error) {
	panic("unported: NewClientMessageDecoder")
}

// Push decodes every client message completed by chunk.
func (d *ClientMessageDecoder) Push(chunk []byte) ([]ClientMessage, error) {
	panic("unported: ClientMessageDecoder.Push")
}

// End finishes the client decoder.
func (d *ClientMessageDecoder) End() error {
	panic("unported: ClientMessageDecoder.End")
}

// ServerMessageDecoder incrementally decodes and validates framed server messages.
type ServerMessageDecoder struct {
	decoder *codecValidatedMessageDecoder[ServerMessage]
}

// NewServerMessageDecoder returns a decoder. A nil options pointer means the defaults.
func NewServerMessageDecoder(options *FrameDecoderOptions) (*ServerMessageDecoder, error) {
	panic("unported: NewServerMessageDecoder")
}

// Push decodes every server message completed by chunk.
func (d *ServerMessageDecoder) Push(chunk []byte) ([]ServerMessage, error) {
	panic("unported: ServerMessageDecoder.Push")
}

// End finishes the server decoder.
func (d *ServerMessageDecoder) End() error {
	panic("unported: ServerMessageDecoder.End")
}

// IsSupportedProtocolVersion reports whether version is the integer ProtocolVersion.
// Non-integers, including 8.5, are false.
func IsSupportedProtocolVersion(version float64) bool {
	panic("unported: IsSupportedProtocolVersion")
}
