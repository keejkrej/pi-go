// Ported from packages/client/src/errors.ts (pi v1.0.0).

package client

import (
	"errors"
	"fmt"

	"github.com/keejkrej/pi-go/protocol"
)

const errorsDefaultDisconnectedMessage = "Client is disconnected"

// ServerError wraps a protocol-level error returned by a remote server.
type ServerError struct {
	Code    protocol.ProtocolErrorCode
	Message string
}

// NewServerError constructs a ServerError from a protocol error payload.
func NewServerError(err protocol.ProtocolError) *ServerError {
	return &ServerError{
		Code:    err.Code,
		Message: err.Message,
	}
}

func (e *ServerError) Error() string { return e.Message }

// Name returns "ServerError".
func (e *ServerError) Name() string { return "ServerError" }

// DisconnectedError indicates the client transport is disconnected.
type DisconnectedError struct {
	Message string
	Cause   error
}

// NewDisconnectedError constructs a DisconnectedError. An empty message with nil
// cause defaults to "Client is disconnected".
func NewDisconnectedError(message string, cause error) *DisconnectedError {
	if message == "" && cause == nil {
		message = errorsDefaultDisconnectedMessage
	}
	return &DisconnectedError{
		Message: message,
		Cause:   cause,
	}
}

func (e *DisconnectedError) Error() string { return e.Message }

// Name returns "DisconnectedError".
func (e *DisconnectedError) Name() string { return "DisconnectedError" }

// Unwrap returns the underlying cause, if any.
func (e *DisconnectedError) Unwrap() error { return e.Cause }

// ClientDisposedError indicates an operation was attempted after Client.Dispose.
type ClientDisposedError struct {
	Message string
}

// NewClientDisposedError returns a ClientDisposedError.
func NewClientDisposedError() *ClientDisposedError {
	return &ClientDisposedError{Message: "Client is disposed"}
}

func (e *ClientDisposedError) Error() string { return e.Message }

// Name returns "ClientDisposedError".
func (e *ClientDisposedError) Name() string { return "ClientDisposedError" }

// ToError normalizes an arbitrary failure value into a non-nil error.
func ToError(value any) error {
	if err, ok := value.(error); ok && err != nil {
		return err
	}
	return errors.New(fmt.Sprint(value))
}

// ToDisconnectedError converts an arbitrary error into a DisconnectedError,
// preserving existing DisconnectedError instances.
func ToDisconnectedError(value any) *DisconnectedError {
	cause := ToError(value)
	var disconnected *DisconnectedError
	if errors.As(cause, &disconnected) {
		return disconnected
	}
	return NewDisconnectedError(cause.Error(), cause)
}

var (
	_ error = (*ServerError)(nil)
	_ error = (*DisconnectedError)(nil)
	_ error = (*ClientDisposedError)(nil)
)
