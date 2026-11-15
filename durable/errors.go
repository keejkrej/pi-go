// Ported from packages/durable/src/errors.ts (pi v1.0.0).

package durable

import "fmt"

// ReadAfterWrite indicates a transaction read a table after its first table write.
// Read every required row before writing.
type ReadAfterWrite struct {
	message string
}

// NewReadAfterWrite creates a ReadAfterWrite error for the named Tx method.
func NewReadAfterWrite(method string) *ReadAfterWrite {
	return &ReadAfterWrite{
		message: fmt.Sprintf("Tx.%s() cannot read tables after the first table write", method),
	}
}

func (e *ReadAfterWrite) Error() string { return e.message }

// Name returns the error class name matching TypeScript.
func (e *ReadAfterWrite) Name() string { return "ReadAfterWrite" }

// StorageRejected indicates storage rejected a batch before any durable effect;
// the owning Session may continue safely.
type StorageRejected struct {
	message string
	cause   error
}

// NewStorageRejected creates a StorageRejected error with an optional underlying cause.
func NewStorageRejected(message string, cause error) *StorageRejected {
	return &StorageRejected{
		message: message,
		cause:   cause,
	}
}

func (e *StorageRejected) Error() string { return e.message }

// Name returns the error class name matching TypeScript.
func (e *StorageRejected) Name() string { return "StorageRejected" }

func (e *StorageRejected) Unwrap() error { return e.cause }

// ConversationBusy indicates a submission reached a busy conversation and was not admitted.
type ConversationBusy struct {
	ConversationId ConversationId
	message        string
}

// NewConversationBusy creates a ConversationBusy error for conversationId.
func NewConversationBusy(conversationId ConversationId) *ConversationBusy {
	return &ConversationBusy{
		ConversationId: conversationId,
		message:        fmt.Sprintf("Conversation %d is busy", conversationId),
	}
}

func (e *ConversationBusy) Error() string { return e.message }

// Name returns the error class name matching TypeScript.
func (e *ConversationBusy) Name() string { return "ConversationBusy" }
