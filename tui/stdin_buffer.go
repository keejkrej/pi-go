// Ported from packages/tui/src/stdin-buffer.ts (pi v1.0.0).

package tui

import (
	"sync"
	"time"
)

const (
	sbEsc                      = "\x1b"
	sbDefaultSequenceTimeoutMs = 50
	sbDefaultEscapeTimeoutMs   = 10
	sbBracketedPasteStart      = "\x1b[200~"
	sbBracketedPasteEnd        = "\x1b[201~"
)

// StdinBufferOptions configures how long StdinBuffer waits for a partial sequence.
type StdinBufferOptions struct {
	// Timeout is the maximum wait, in milliseconds, for an incomplete sequence
	// such as CSI or mouse. Nil uses 50.
	Timeout *int64 `json:"timeout,omitzero"`
	// EscapeTimeout is the maximum wait, in milliseconds, after a lone ESC
	// before treating it as Escape. Nil uses 10. Increase for high-latency Alt+key (SSH).
	EscapeTimeout *int64 `json:"escapeTimeout,omitzero"`
}

// StdinBufferEventMap is the EventEmitter map: data and paste each pass one string.
// Listen with OnData and OnPaste. This type is not constructed.
type StdinBufferEventMap struct {
	Data  [1]string
	Paste [1]string
}

// StdinBuffer buffers stdin and emits complete sequences.
// Partial escape sequences that arrive across chunks stay buffered until complete or timed out.
// mu guards the fields. Do not hold it across listener calls.
type StdinBuffer struct {
	mu                             sync.Mutex
	buffer                         string
	timeout                        *time.Timer
	timeoutMs                      int64
	escapeTimeoutMs                int64
	pasteMode                      bool
	pasteBuffer                    string
	pendingKittyPrintableCodepoint *int
	dataListeners                  []func(string)
	pasteListeners                 []func(string)
}

// NewStdinBuffer constructs a buffer. Nil options use the defaults.
func NewStdinBuffer(options *StdinBufferOptions) *StdinBuffer {
	panic("unported: NewStdinBuffer")
}

// Process feeds a string chunk (TS process string overload).
func (b *StdinBuffer) Process(data string) {
	panic("unported: StdinBuffer.Process")
}

// ProcessBytes feeds a raw byte chunk (TS process Buffer overload).
// A one-byte chunk above 127 is ESC plus (byte - 128).
func (b *StdinBuffer) ProcessBytes(data []byte) {
	panic("unported: StdinBuffer.ProcessBytes")
}

// OnData registers a listener for complete sequences (TS "data").
// The returned function unsubscribes.
func (b *StdinBuffer) OnData(listener func(sequence string)) (unsubscribe func()) {
	panic("unported: StdinBuffer.OnData")
}

// OnPaste registers a listener for bracketed-paste content (TS "paste").
// The returned function unsubscribes. Content does not include the paste markers.
func (b *StdinBuffer) OnPaste(listener func(content string)) (unsubscribe func()) {
	panic("unported: StdinBuffer.OnPaste")
}

// Flush stops the pending timer and returns any incomplete buffer as one sequence.
func (b *StdinBuffer) Flush() []string {
	panic("unported: StdinBuffer.Flush")
}

// Clear drops the buffer, paste state, and pending timer.
func (b *StdinBuffer) Clear() {
	panic("unported: StdinBuffer.Clear")
}

// GetBuffer returns the incomplete input still buffered.
func (b *StdinBuffer) GetBuffer() string {
	panic("unported: StdinBuffer.GetBuffer")
}

// Destroy clears the buffer and cancels the pending timer.
func (b *StdinBuffer) Destroy() {
	panic("unported: StdinBuffer.Destroy")
}

func (b *StdinBuffer) emitDataSequence(sequence string) {
	panic("unported: StdinBuffer.emitDataSequence")
}
