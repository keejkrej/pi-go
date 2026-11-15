// Ported from packages/server/src/errors.ts (pi v1.0.0).

package server

import "github.com/keejkrej/pi-go/protocol"

// InternalServerErrorMessage is the bounded public error message for unexpected host failures.
const InternalServerErrorMessage = "Internal server error"

// ServerError is a host or lifecycle error that can safely cross the protocol boundary.
type ServerError struct {
	Code    protocol.ProtocolErrorCode
	Message string
}

// NewServerError constructs a ServerError with a protocol error code and message.
func NewServerError(code protocol.ProtocolErrorCode, message string) *ServerError {
	return &ServerError{
		Code:    code,
		Message: message,
	}
}

func (e *ServerError) Error() string { return e.Message }

// Name returns "ServerError".
func (e *ServerError) Name() string { return "ServerError" }

// WrongServerError indicates a request targeted a different logical server.
type WrongServerError struct {
	ServerError
}

// NewWrongServerError constructs a WrongServerError.
func NewWrongServerError() *WrongServerError {
	return &WrongServerError{
		ServerError: ServerError{
			Code:    "wrong_server",
			Message: "Request was addressed to another server",
		},
	}
}

// Name returns "WrongServerError".
func (e *WrongServerError) Name() string { return "WrongServerError" }

// Unwrap exposes the underlying *ServerError.
func (e *WrongServerError) Unwrap() error { return &e.ServerError }

// SessionNotFoundError indicates no durable session matched the requested ID.
type SessionNotFoundError struct {
	ServerError
}

// NewSessionNotFoundError constructs a SessionNotFoundError with an optional message.
func NewSessionNotFoundError(message ...string) *SessionNotFoundError {
	msg := "Session was not found"
	if len(message) > 0 && message[0] != "" {
		msg = message[0]
	}
	return &SessionNotFoundError{
		ServerError: ServerError{
			Code:    "session_not_found",
			Message: msg,
		},
	}
}

// Name returns "SessionNotFoundError".
func (e *SessionNotFoundError) Name() string { return "SessionNotFoundError" }

// Unwrap exposes the underlying *ServerError.
func (e *SessionNotFoundError) Unwrap() error { return &e.ServerError }

// SessionAmbiguousError indicates a session lookup matched multiple sessions.
type SessionAmbiguousError struct {
	ServerError
}

// NewSessionAmbiguousError constructs a SessionAmbiguousError.
func NewSessionAmbiguousError() *SessionAmbiguousError {
	return &SessionAmbiguousError{
		ServerError: ServerError{
			Code:    "session_ambiguous",
			Message: "Session ID matches more than one session",
		},
	}
}

// Name returns "SessionAmbiguousError".
func (e *SessionAmbiguousError) Name() string { return "SessionAmbiguousError" }

// Unwrap exposes the underlying *ServerError.
func (e *SessionAmbiguousError) Unwrap() error { return &e.ServerError }

// SessionNotAttachedError indicates the requesting client does not hold the target session attachment.
type SessionNotAttachedError struct {
	ServerError
}

// NewSessionNotAttachedError constructs a SessionNotAttachedError.
func NewSessionNotAttachedError() *SessionNotAttachedError {
	return &SessionNotAttachedError{
		ServerError: ServerError{
			Code:    "session_not_attached",
			Message: "Session is not attached to this client",
		},
	}
}

// Name returns "SessionNotAttachedError".
func (e *SessionNotAttachedError) Name() string { return "SessionNotAttachedError" }

// Unwrap exposes the underlying *ServerError.
func (e *SessionNotAttachedError) Unwrap() error { return &e.ServerError }

// ServerDrainingError indicates the server or attachment is shutting down.
type ServerDrainingError struct {
	ServerError
}

// NewServerDrainingError constructs a ServerDrainingError.
func NewServerDrainingError() *ServerDrainingError {
	return &ServerDrainingError{
		ServerError: ServerError{
			Code:    "server_draining",
			Message: "Server is draining",
		},
	}
}

// Name returns "ServerDrainingError".
func (e *ServerDrainingError) Name() string { return "ServerDrainingError" }

// Unwrap exposes the underlying *ServerError.
func (e *ServerDrainingError) Unwrap() error { return &e.ServerError }

var (
	_ error = (*ServerError)(nil)
	_ error = (*WrongServerError)(nil)
	_ error = (*SessionNotFoundError)(nil)
	_ error = (*SessionAmbiguousError)(nil)
	_ error = (*SessionNotAttachedError)(nil)
	_ error = (*ServerDrainingError)(nil)
)
