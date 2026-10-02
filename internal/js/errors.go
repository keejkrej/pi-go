package js

import (
	"context"
)

// JSError mirrors a built-in JS error object (TypeError, RangeError,
// URIError, ...) thrown by the JS runtime itself. Error returns the message
// and Name the constructor name, so ErrorString renders "<Name>: <message>".
type JSError struct {
	// ErrName is the JS error name ("TypeError").
	ErrName string
	// Message is the JS error message.
	Message string
	// Code is the Node error code (err.code), "" when Node sets none.
	Code string
}

func (e *JSError) Error() string { return e.Message }

// Name returns the JS error name.
func (e *JSError) Name() string { return e.ErrName }

// NewTypeError returns new TypeError(message).
func NewTypeError(message string) *JSError {
	return &JSError{ErrName: "TypeError", Message: message}
}

// NewRangeError returns new RangeError(message).
func NewRangeError(message string) *JSError {
	return &JSError{ErrName: "RangeError", Message: message}
}

// NewURIError returns new URIError(message).
func NewURIError(message string) *JSError {
	return &JSError{ErrName: "URIError", Message: message}
}

// NewSyntaxError returns new SyntaxError(message).
func NewSyntaxError(message string) *JSError {
	return &JSError{ErrName: "SyntaxError", Message: message}
}

// ErrorString returns String(err) / `${err}` (Error.prototype.toString): the
// name, ": ", and the message, or just one of them when the other is empty.
// The name is err's Name() when err implements it, else "Error". The bare
// context.Canceled and context.DeadlineExceeded sentinels render as the
// DOMException a default AbortSignal reason stringifies to. A nil err renders
// as "undefined".
func ErrorString(err error) string {
	if err == nil {
		return "undefined"
	}
	switch err {
	case context.Canceled:
		return "AbortError: This operation was aborted"
	case context.DeadlineExceeded:
		return "TimeoutError: The operation was aborted due to timeout"
	}
	name := "Error"
	if n, ok := err.(interface{ Name() string }); ok {
		name = n.Name()
	}
	msg := err.Error()
	if name == "" {
		return msg
	}
	if msg == "" {
		return name
	}
	return name + ": " + msg
}
