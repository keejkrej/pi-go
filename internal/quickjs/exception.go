// Ported from node_modules/quickjs-wasi/dist/index.js (quickjs-wasi 3.6.2).

package quickjs

// JSException is an exception thrown from QuickJS code. It is a Go error whose
// Error() is the JS message, with the JS name and stack available, and it also
// exposes Handle, a live handle to the QuickJS exception value, allowing
// direct inspection of custom properties.
//
// The Handle must be disposed when you're done with it. If the error
// propagates uncaught, the handle is released with the VM.
type JSException struct {
	// Handle is a live handle to the QuickJS exception value. You can read
	// custom properties, call methods, etc. Must be disposed when done.
	Handle *JSValueHandle
	// Cached values so they survive handle disposal / VM teardown.
	name    string
	message string
	stack   *string
}

// newJSException reads the error properties eagerly and caches them.
func newJSException(handle *JSValueHandle) *JSException {
	e := &JSException{Handle: handle}
	msgHandle := handle.GetProp("message")
	defer msgHandle.Dispose()
	e.name = ConsumeHandle(handle.GetProp("name"), func(h *JSValueHandle) string {
		if h.IsUndefined() {
			return "Error"
		}
		return h.ToString()
	})
	if msgHandle.IsUndefined() {
		e.message = handle.ToString()
	} else {
		e.message = msgHandle.ToString()
	}
	e.stack = ConsumeHandle(handle.GetProp("stack"), func(h *JSValueHandle) *string {
		if h.IsUndefined() {
			return nil
		}
		stack := h.ToString()
		return &stack
	})
	return e
}

// Error returns the JS message (TS: error.message).
func (e *JSException) Error() string { return e.message }

// Name returns the JS error name ("Error" when the thrown value has none).
func (e *JSException) Name() string { return e.name }

// SetName overrides the cached name.
func (e *JSException) SetName(name string) { e.name = name }

// Message returns the JS message. For a thrown value without a message
// property it is the value's string conversion.
func (e *JSException) Message() string { return e.message }

// SetMessage overrides the cached message.
func (e *JSException) SetMessage(message string) { e.message = message }

// Stack returns the guest stack, nil when the thrown value has none.
func (e *JSException) Stack() *string { return e.stack }

// SetStack overrides the cached stack.
func (e *JSException) SetStack(stack *string) { e.stack = stack }

// Dispose disposes the exception handle.
func (e *JSException) Dispose() {
	e.Handle.Dispose()
}
