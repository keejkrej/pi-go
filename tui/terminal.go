// Ported from packages/tui/src/terminal.ts (pi v1.0.0).

package tui

import (
	"sync"
	"time"
)

const (
	termTerminalProgressKeepaliveMs               int64 = 1000
	termKeyboardProtocolResponseFragmentTimeoutMs int64 = 150
	termDefaultEscapeTimeoutMs                    int64 = 10
	termDefaultSshEscapeTimeoutMs                 int64 = 100
	termDesiredKittyKeyboardProtocolFlags               = 7
	termTerminalProgressActiveSequence                  = "\x1b]9;4;3\x07"
	termTerminalProgressClearSequence                   = "\x1b]9;4;0\x07"
	termNativeShiftEnterSequence                        = "\x1b[13;2u"
	termKittyKeyboardProtocolQuery                      = "\x1b[>7u\x1b[?u\x1b[c" // embeds termDesiredKittyKeyboardProtocolFlags (7)
	termKeyboardProtocolTypeKittyFlags                  = "kitty-flags"
	termKeyboardProtocolTypeDeviceAttributes            = "device-attributes"
)

// KeyboardProtocolNegotiationSequence is a Kitty flags reply or a primary device-attributes reply.
type KeyboardProtocolNegotiationSequence interface {
	isKeyboardProtocolNegotiationSequence()
}

// KeyboardProtocolNegotiationSequenceKittyFlags is a Kitty keyboard-protocol flags reply.
// Type is always "kitty-flags".
type KeyboardProtocolNegotiationSequenceKittyFlags struct {
	Type  string `json:"type"`
	Flags int    `json:"flags"`
}

func (*KeyboardProtocolNegotiationSequenceKittyFlags) isKeyboardProtocolNegotiationSequence() {}

// KeyboardProtocolNegotiationSequenceDeviceAttributes is a DA1 reply.
// Type is always "device-attributes".
type KeyboardProtocolNegotiationSequenceDeviceAttributes struct {
	Type string `json:"type"`
}

func (*KeyboardProtocolNegotiationSequenceDeviceAttributes) isKeyboardProtocolNegotiationSequence() {}

// ParseKeyboardProtocolNegotiationSequence parses one complete negotiation reply.
// A nil result is not a negotiation sequence.
func ParseKeyboardProtocolNegotiationSequence(sequence string) KeyboardProtocolNegotiationSequence {
	panic("unported: ParseKeyboardProtocolNegotiationSequence")
}

func termIsKeyboardProtocolNegotiationSequencePrefix(sequence string) bool {
	panic("unported: termIsKeyboardProtocolNegotiationSequencePrefix")
}

// IsAppleTerminalSession reports whether this process is Apple Terminal on darwin.
func IsAppleTerminalSession() bool {
	panic("unported: IsAppleTerminalSession")
}

// RefreshTerminalDimensions asks a POSIX terminal to refresh its size by sending SIGWINCH
// to this process. Windows and a non-positive pid do nothing. EACCES and other signal
// errors are ignored.
func RefreshTerminalDimensions() {
	panic("unported: RefreshTerminalDimensions")
}

// NormalizeNativeShiftEnterInput rewrites a bare CR to the CSI-u Shift+Enter sequence
// when native Shift detection is enabled and Shift is pressed.
func NormalizeNativeShiftEnterInput(data string, shouldDetectNativeShiftEnter bool, isShiftPressed bool) string {
	panic("unported: NormalizeNativeShiftEnterInput")
}

// NormalizeAppleTerminalInput applies native Shift+Enter rewriting for an Apple Terminal session.
func NormalizeAppleTerminalInput(data string, isAppleTerminal bool, isShiftPressed bool) string {
	return NormalizeNativeShiftEnterInput(data, isAppleTerminal, isShiftPressed)
}

// Terminal is the minimal terminal surface a TUI drives.
type Terminal interface {
	// Start starts input and resize handling.
	Start(onInput func(data string), onResize func())
	// Stop restores terminal state.
	Stop()
	// DrainInput drains stdin before exit so Kitty key releases do not leak to the parent shell.
	// Nil maxMs is 1000. Nil idleMs is 50. Drain ends early when input is idle for idleMs.
	DrainInput(maxMs *int64, idleMs *int64) error
	// Write writes output to the terminal.
	Write(data string)
	// Columns is the terminal width in columns.
	Columns() int
	// Rows is the terminal height in rows.
	Rows() int
	// KittyProtocolActive reports whether the Kitty keyboard protocol is active.
	KittyProtocolActive() bool
	// MoveBy moves the cursor down by lines, or up when lines is negative.
	MoveBy(lines int)
	// HideCursor hides the cursor.
	HideCursor()
	// ShowCursor shows the cursor.
	ShowCursor()
	// ClearLine clears the current line.
	ClearLine()
	// ClearFromCursor clears from the cursor to the end of the screen.
	ClearFromCursor()
	// ClearScreen clears the screen and moves the cursor to the origin.
	ClearScreen()
	// SetTitle sets the terminal window title.
	SetTitle(title string)
	// SetProgress sets or clears the OSC 9;4 progress indicator.
	SetProgress(active bool)
}

// ResolveEscapeTimeoutMs returns how long to wait for the rest of an escape sequence
// before a lone ESC is the Escape key. A nil env reads the process environment; an empty
// map does not. PI_TUI_ESC_TIMEOUT wins when it is a finite number greater than zero.
// SSH_CONNECTION or SSH_TTY selects 100ms. Otherwise the timeout is 10ms.
func ResolveEscapeTimeoutMs(env map[string]string) int64 {
	panic("unported: ResolveEscapeTimeoutMs")
}

// ProcessTerminal is the real terminal on process stdin and stdout.
// mu guards state shared by the caller, the stdin reader, and timers.
type ProcessTerminal struct {
	mu                                      sync.Mutex
	wasRaw                                  bool
	inputHandler                            func(data string)
	resizeHandler                           func()
	kittyProtocolActive                     bool
	modifyOtherKeysActive                   bool
	keyboardProtocolPushed                  bool
	pendingKeyboardProtocolDeviceAttributes int
	keyboardProtocolNegotiationBuffer       string
	keyboardProtocolBufferFlushTimer        *time.Timer
	stdinBuffer                             *StdinBuffer
	stdinDataHandler                        func(data string)
	progressInterval                        *time.Ticker
	writeLogPath                            string
}

// NewProcessTerminal returns a process terminal.
// PI_TUI_WRITE_LOG, when set, is a write-log file, or a directory that receives a timestamped log.
func NewProcessTerminal() *ProcessTerminal {
	panic("unported: NewProcessTerminal")
}

// KittyProtocolActive reports whether the Kitty keyboard protocol is active.
func (t *ProcessTerminal) KittyProtocolActive() bool {
	return t.kittyProtocolActive
}

// ModifyOtherKeysActive reports whether the modifyOtherKeys fallback is active.
func (t *ProcessTerminal) ModifyOtherKeysActive() bool {
	return t.modifyOtherKeysActive
}

// Start implements Terminal.
func (t *ProcessTerminal) Start(onInput func(data string), onResize func()) {
	panic("unported: ProcessTerminal.Start")
}

func (t *ProcessTerminal) setupStdinBuffer() {
	panic("unported: ProcessTerminal.setupStdinBuffer")
}

func (t *ProcessTerminal) queryAndEnableKittyProtocol() {
	panic("unported: ProcessTerminal.queryAndEnableKittyProtocol")
}

func (t *ProcessTerminal) handleKeyboardProtocolNegotiationSequence(negotiationSequence KeyboardProtocolNegotiationSequence) bool {
	panic("unported: ProcessTerminal.handleKeyboardProtocolNegotiationSequence")
}

// termKeyboardProtocolNegotiationRead is one read of a negotiation reply.
// A nil pointer is not a negotiation sequence.
// Pending is the incomplete-fragment sentinel; Parsed and Sequence are then unset.
// Otherwise Parsed is the reply and Sequence is the full, possibly reassembled, sequence.
type termKeyboardProtocolNegotiationRead struct {
	Parsed   KeyboardProtocolNegotiationSequence
	Sequence string
	Pending  bool
}

func (t *ProcessTerminal) readKeyboardProtocolNegotiationSequence(sequence string) *termKeyboardProtocolNegotiationRead {
	panic("unported: ProcessTerminal.readKeyboardProtocolNegotiationSequence")
}

func (t *ProcessTerminal) setKeyboardProtocolNegotiationBuffer(sequence string) {
	panic("unported: ProcessTerminal.setKeyboardProtocolNegotiationBuffer")
}

func (t *ProcessTerminal) clearKeyboardProtocolNegotiationBuffer() {
	panic("unported: ProcessTerminal.clearKeyboardProtocolNegotiationBuffer")
}

func (t *ProcessTerminal) flushKeyboardProtocolNegotiationBufferAsInput() {
	panic("unported: ProcessTerminal.flushKeyboardProtocolNegotiationBufferAsInput")
}

func (t *ProcessTerminal) scheduleKeyboardProtocolNegotiationBufferFlush() {
	panic("unported: ProcessTerminal.scheduleKeyboardProtocolNegotiationBufferFlush")
}

func (t *ProcessTerminal) clearKeyboardProtocolNegotiationBufferFlushTimer() {
	panic("unported: ProcessTerminal.clearKeyboardProtocolNegotiationBufferFlushTimer")
}

func (t *ProcessTerminal) forwardInputSequence(sequence string) {
	panic("unported: ProcessTerminal.forwardInputSequence")
}

func (t *ProcessTerminal) enableModifyOtherKeys() {
	panic("unported: ProcessTerminal.enableModifyOtherKeys")
}

func (t *ProcessTerminal) disableModifyOtherKeys() {
	panic("unported: ProcessTerminal.disableModifyOtherKeys")
}

func (t *ProcessTerminal) enableWindowsVTInput() {
	panic("unported: ProcessTerminal.enableWindowsVTInput")
}

// DrainInput implements Terminal.
func (t *ProcessTerminal) DrainInput(maxMs *int64, idleMs *int64) error {
	panic("unported: ProcessTerminal.DrainInput")
}

// Stop implements Terminal.
func (t *ProcessTerminal) Stop() {
	panic("unported: ProcessTerminal.Stop")
}

// Write implements Terminal.
func (t *ProcessTerminal) Write(data string) {
	panic("unported: ProcessTerminal.Write")
}

// Columns implements Terminal.
func (t *ProcessTerminal) Columns() int {
	panic("unported: ProcessTerminal.Columns")
}

// Rows implements Terminal.
func (t *ProcessTerminal) Rows() int {
	panic("unported: ProcessTerminal.Rows")
}

// MoveBy implements Terminal.
func (t *ProcessTerminal) MoveBy(lines int) {
	panic("unported: ProcessTerminal.MoveBy")
}

// HideCursor implements Terminal.
func (t *ProcessTerminal) HideCursor() {
	panic("unported: ProcessTerminal.HideCursor")
}

// ShowCursor implements Terminal.
func (t *ProcessTerminal) ShowCursor() {
	panic("unported: ProcessTerminal.ShowCursor")
}

// ClearLine implements Terminal.
func (t *ProcessTerminal) ClearLine() {
	panic("unported: ProcessTerminal.ClearLine")
}

// ClearFromCursor implements Terminal.
func (t *ProcessTerminal) ClearFromCursor() {
	panic("unported: ProcessTerminal.ClearFromCursor")
}

// ClearScreen implements Terminal.
func (t *ProcessTerminal) ClearScreen() {
	panic("unported: ProcessTerminal.ClearScreen")
}

// SetTitle implements Terminal.
func (t *ProcessTerminal) SetTitle(title string) {
	panic("unported: ProcessTerminal.SetTitle")
}

// SetProgress implements Terminal.
func (t *ProcessTerminal) SetProgress(active bool) {
	panic("unported: ProcessTerminal.SetProgress")
}

func (t *ProcessTerminal) clearProgressInterval() bool {
	panic("unported: ProcessTerminal.clearProgressInterval")
}

var _ Terminal = (*ProcessTerminal)(nil)
