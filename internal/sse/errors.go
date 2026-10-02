package sse

// ErrorType is the kind of a ParseError.
type ErrorType string

const (
	ErrorTypeInvalidRetry          ErrorType = "invalid-retry"
	ErrorTypeUnknownField          ErrorType = "unknown-field"
	ErrorTypeMaxBufferSizeExceeded ErrorType = "max-buffer-size-exceeded"
)

// ParseError is reported for problems found while parsing a stream: an
// unknown field, an invalid retry value, or a buffer overflow.
type ParseError struct {
	Message string
	// Type is the kind of error.
	Type ErrorType
	// Field is the field name of an unknown field.
	Field *string
	// Value is the field value of an unknown field or invalid retry.
	Value *string
	// Line is the line that caused the error, if available.
	Line *string
}

// ParseErrorOptions holds the ParseError properties besides the message.
type ParseErrorOptions struct {
	Type  ErrorType
	Field *string
	Value *string
	Line  *string
}

// NewParseError returns a ParseError with the given message and properties.
func NewParseError(message string, options ParseErrorOptions) *ParseError {
	return &ParseError{Message: message, Type: options.Type, Field: options.Field, Value: options.Value, Line: options.Line}
}

func (e *ParseError) Error() string { return e.Message }

// Name returns the JS error name, "ParseError".
func (e *ParseError) Name() string { return "ParseError" }
