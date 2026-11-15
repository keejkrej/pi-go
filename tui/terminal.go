// Ported from packages/tui/src/terminal.ts (pi v1.5.2).

package tui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
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
	termKittyKeyboardFlagsQuery                         = "\x1b[>7u\x1b[?u"
	termDeviceAttributesQuery                           = "\x1b[c"
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
	if !strings.HasPrefix(sequence, "\x1b[?") {
		return nil
	}
	body := sequence[3:]
	if len(body) >= 2 && body[len(body)-1] == 'u' {
		digits := body[:len(body)-1]
		if termIsDigitsOnly(digits) {
			if flags, err := strconv.Atoi(digits); err == nil {
				return &KeyboardProtocolNegotiationSequenceKittyFlags{
					Type:  termKeyboardProtocolTypeKittyFlags,
					Flags: flags,
				}
			}
		}
	}
	if len(body) >= 1 && body[len(body)-1] == 'c' {
		params := body[:len(body)-1]
		if termIsDigitsAndSemicolonsOnly(params) {
			return &KeyboardProtocolNegotiationSequenceDeviceAttributes{
				Type: termKeyboardProtocolTypeDeviceAttributes,
			}
		}
	}
	return nil
}

func termIsDigitsOnly(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func termIsDigitsAndSemicolonsOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if (b < '0' || b > '9') && b != ';' {
			return false
		}
	}
	return true
}

func termIsKeyboardProtocolNegotiationSequencePrefix(sequence string) bool {
	if sequence == "\x1b[" {
		return true
	}
	if strings.HasPrefix(sequence, "\x1b[?") {
		return termIsDigitsAndSemicolonsOnly(sequence[3:])
	}
	return false
}

// IsAppleTerminalSession reports whether this process is Apple Terminal on darwin.
func IsAppleTerminalSession() bool {
	return runtime.GOOS == "darwin" && os.Getenv("TERM_PROGRAM") == "Apple_Terminal"
}

// RefreshTerminalDimensions asks a POSIX terminal to refresh its size by sending SIGWINCH
// to this process. Windows and a non-positive pid do nothing. EACCES and other signal
// errors are ignored.
func RefreshTerminalDimensions() {
	termRefreshTerminalDimensionsFor(os.Getpid(), termSendSigwinch)
}

func termRefreshTerminalDimensionsFor(pid int, sendSigwinch func(int) error) {
	if pid <= 0 || sendSigwinch == nil {
		return
	}
	_ = sendSigwinch(pid)
}

func termIsNativeShiftEnterCandidate(data string) bool {
	return data == "\r" || data == "\x1b\r"
}

// NormalizeNativeShiftEnterInput rewrites a bare CR (or ESC+CR) to the CSI-u Shift+Enter
// sequence when native Shift detection is enabled and Shift is pressed.
func NormalizeNativeShiftEnterInput(data string, shouldDetectNativeShiftEnter bool, isShiftPressed bool) string {
	if shouldDetectNativeShiftEnter && termIsNativeShiftEnterCandidate(data) && isShiftPressed {
		return termNativeShiftEnterSequence
	}
	return data
}

// NormalizeAppleTerminalInput applies native Shift+Enter rewriting for an Apple Terminal session.
func NormalizeAppleTerminalInput(data string, isAppleTerminal bool, isShiftPressed bool) string {
	return NormalizeNativeShiftEnterInput(data, isAppleTerminal, isShiftPressed)
}

func termDecodeUTF8Chunk(chunk []byte, pending *[]byte) string {
	data := chunk
	if len(*pending) > 0 {
		combined := make([]byte, len(*pending)+len(chunk))
		copy(combined, *pending)
		copy(combined[len(*pending):], chunk)
		data = combined
		*pending = (*pending)[:0]
	}
	tailStart := len(data)
	for i := 1; i <= 3 && i <= len(data); i++ {
		b := data[len(data)-i]
		if b < 0x80 {
			break
		}
		if utf8.RuneStart(b) {
			if !utf8.FullRune(data[len(data)-i:]) {
				tailStart = len(data) - i
			}
			break
		}
	}
	if tailStart < len(data) {
		*pending = append(*pending, data[tailStart:]...)
		data = data[:tailStart]
	}
	return string(data)
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
	// SetProgramStatus reports program activity via OSC 7501 when supported.
	SetProgramStatus(status ProgramStatus)
}

// ExitRestoreTerminal is an optional interface implemented by terminals that can
// synchronously reset terminal modes during exit when Stop did not run.
type ExitRestoreTerminal interface {
	RestoreOnExit(before string, after string)
}

// ResolveEscapeTimeoutMs returns how long to wait for the rest of an escape sequence
// before a lone ESC is the Escape key. A nil env reads the process environment; an empty
// map does not. PI_TUI_ESC_TIMEOUT wins when it is a finite number greater than zero.
// SSH_CONNECTION or SSH_TTY selects 100ms. Otherwise the timeout is 10ms.
func ResolveEscapeTimeoutMs(env map[string]string) int64 {
	lookup := os.Getenv
	if env != nil {
		lookup = func(k string) string { return env[k] }
	}
	if raw := strings.TrimSpace(lookup("PI_TUI_ESC_TIMEOUT")); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && parsed > 0 {
			return int64(parsed)
		}
	}
	if lookup("SSH_CONNECTION") != "" || lookup("SSH_TTY") != "" {
		return termDefaultSshEscapeTimeoutMs
	}
	return termDefaultEscapeTimeoutMs
}

// ProcessTerminal is the real terminal on process stdin and stdout.
// mu guards state shared by the caller, the stdin reader, and timers.
type ProcessTerminal struct {
	mu                                      sync.Mutex
	wasRaw                                  bool
	prevTermState                           *term.State
	inputHandler                            func(data string)
	resizeHandler                           func()
	kittyProtocolActive                     bool
	modifyOtherKeysActive                   bool
	keyboardProtocolPushed                  bool
	pendingKeyboardProtocolDeviceAttributes int
	keyboardProtocolNegotiationBuffer       string
	keyboardProtocolBufferFlushTimer        *time.Timer
	flushTimerGen                           uint64
	stdinBuffer                             *StdinBuffer
	stdinDataHandler                        func(data string)
	progressInterval                        *time.Ticker
	progressDone                            chan struct{}
	programStatus                           *ProgramStatus
	programStatusSupported                  bool
	programStatusQueryPending               bool
	writeLogPath                            string
	stopStdinFn                             func()
	stopResizeFn                            func()
	drainDataCount                          atomic.Int64
	lastDrainDataNs                         atomic.Int64

	// Optional test hooks; zero values use real OS stdin/stdout/env/native helpers.
	skipOSIO        bool
	writeOutFn      func(string)
	writeSyncFn     func(string)
	getenvFn        func(string) string
	getSizeFn       func() (cols, rows int, ok bool)
	isNativeShiftFn func() bool
	enableWinVTFn   func() (bool, error)
	platformOS      string
}

// NewProcessTerminal returns a process terminal.
// PI_TUI_WRITE_LOG, when set, is a write-log file, or a directory that receives a timestamped log.
func NewProcessTerminal() *ProcessTerminal {
	return &ProcessTerminal{
		writeLogPath: termResolveWriteLogPath(os.Getenv("PI_TUI_WRITE_LOG")),
	}
}

func termResolveWriteLogPath(env string) string {
	if env == "" {
		return ""
	}
	if info, err := os.Stat(env); err == nil && info.IsDir() {
		ts := time.Now().Format("2006-01-02_15-04-05")
		return filepath.Join(env, fmt.Sprintf("tui-%s-%d.log", ts, os.Getpid()))
	}
	return env
}

func (t *ProcessTerminal) getenv(key string) string {
	if t.getenvFn != nil {
		return t.getenvFn(key)
	}
	return os.Getenv(key)
}

func (t *ProcessTerminal) outWrite(data string) {
	if data == "" {
		return
	}
	if t.writeOutFn != nil {
		t.writeOutFn(data)
		return
	}
	_, _ = os.Stdout.WriteString(data)
}

func (t *ProcessTerminal) outSyncWrite(data string) {
	if data == "" {
		return
	}
	if t.writeSyncFn != nil {
		t.writeSyncFn(data)
		return
	}
	_, _ = os.Stdout.WriteString(data)
}

func (t *ProcessTerminal) platformOSName() string {
	if t.platformOS != "" {
		return t.platformOS
	}
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

func (t *ProcessTerminal) isAppleTerminalSession() bool {
	return t.platformOSName() == "darwin" && t.getenv("TERM_PROGRAM") == "Apple_Terminal"
}

func (t *ProcessTerminal) isNativeShiftPressed() bool {
	if t.isNativeShiftFn != nil {
		return t.isNativeShiftFn()
	}
	return IsNativeModifierPressed(ModifierKeyShift)
}

func (t *ProcessTerminal) envForTimeoutLocked() map[string]string {
	if t.getenvFn == nil {
		return nil
	}
	return map[string]string{
		"PI_TUI_ESC_TIMEOUT": t.getenv("PI_TUI_ESC_TIMEOUT"),
		"SSH_CONNECTION":     t.getenv("SSH_CONNECTION"),
		"SSH_TTY":            t.getenv("SSH_TTY"),
	}
}

// KittyProtocolActive reports whether the Kitty keyboard protocol is active.
func (t *ProcessTerminal) KittyProtocolActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kittyProtocolActive
}

// ModifyOtherKeysActive reports whether the modifyOtherKeys fallback is active.
func (t *ProcessTerminal) ModifyOtherKeysActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.modifyOtherKeysActive
}

// Start implements Terminal.
func (t *ProcessTerminal) Start(onInput func(data string), onResize func()) {
	t.mu.Lock()
	prevSB := t.stdinBuffer
	prevStopStdin := t.stopStdinFn
	prevStopResize := t.stopResizeFn
	prevState := t.prevTermState
	t.stdinBuffer = nil
	t.stopStdinFn = nil
	t.stopResizeFn = nil
	t.prevTermState = nil
	t.inputHandler = onInput
	t.resizeHandler = onResize
	skipOS := t.skipOSIO
	t.mu.Unlock()

	if prevStopStdin != nil {
		prevStopStdin()
	}
	if prevSB != nil {
		prevSB.Destroy()
	}
	if prevStopResize != nil {
		prevStopResize()
	}
	if prevState != nil {
		termRestoreRaw(os.Stdin, prevState)
	}

	if !skipOS {
		oldState, wasRaw, _ := termMakeRaw(os.Stdin)
		t.mu.Lock()
		t.prevTermState = oldState
		t.wasRaw = wasRaw
		t.mu.Unlock()
	}

	// Enable bracketed paste mode - terminal wraps pastes in \x1b[200~ ... \x1b[201~
	t.outWrite("\x1b[?2004h")

	if !skipOS {
		stopResize := termWatchResize(func() {
			t.mu.Lock()
			h := t.resizeHandler
			t.mu.Unlock()
			if h != nil {
				h()
			}
		})
		t.mu.Lock()
		t.stopResizeFn = stopResize
		t.mu.Unlock()

		RefreshTerminalDimensions()
		t.enableWindowsVTInput()
	} else if t.platformOSName() == "win32" {
		t.enableWindowsVTInput()
	}

	t.queryAndEnableKittyProtocol()

	if !skipOS {
		stopStdin := termStartStdinReader(os.Stdin, func(chunk string) {
			t.mu.Lock()
			handler := t.stdinDataHandler
			t.mu.Unlock()
			if handler != nil {
				handler(chunk)
			}
		})
		t.mu.Lock()
		if t.stdinDataHandler != nil {
			t.stopStdinFn = stopStdin
			stopStdin = nil
		}
		t.mu.Unlock()
		if stopStdin != nil {
			stopStdin()
		}
	}
}

// feedStdinChunk simulates raw stdin bytes/text arriving on stdin (for tests or embedded transports).
func (t *ProcessTerminal) feedStdinChunk(data string) {
	t.mu.Lock()
	handler := t.stdinDataHandler
	t.mu.Unlock()
	if handler != nil {
		handler(data)
	} else {
		t.drainDataCount.Add(1)
		t.lastDrainDataNs.Store(time.Now().UnixNano())
	}
}

func (t *ProcessTerminal) setupStdinBuffer() {
	t.mu.Lock()
	escapeTimeout := ResolveEscapeTimeoutMs(t.envForTimeoutLocked())
	oldSB := t.stdinBuffer
	sb := NewStdinBuffer(&StdinBufferOptions{
		EscapeTimeout: &escapeTimeout,
	})
	t.stdinBuffer = sb
	t.stdinDataHandler = func(data string) {
		t.drainDataCount.Add(1)
		t.lastDrainDataNs.Store(time.Now().UnixNano())
		sb.Process(data)
	}
	t.mu.Unlock()

	if oldSB != nil {
		oldSB.Destroy()
	}

	sb.OnData(func(sequence string) {
		t.handleStdinDataSequence(sequence)
	})
	sb.OnPaste(func(content string) {
		t.mu.Lock()
		h := t.inputHandler
		t.mu.Unlock()
		if h != nil {
			h("\x1b[200~" + content + "\x1b[201~")
		}
	})
}

func (t *ProcessTerminal) handleStdinDataSequence(sequence string) {
	if IsProgramStatusReply(sequence) {
		var statusWrite string
		t.mu.Lock()
		if t.programStatusQueryPending {
			t.programStatusQueryPending = false
			t.programStatusSupported = true
			statusWrite = t.programStatusWriteLocked()
		}
		t.mu.Unlock()
		if statusWrite != "" {
			t.outWrite(statusWrite)
		}
		return
	}

	t.mu.Lock()
	negotiation, flushedPrev := t.readKeyboardProtocolNegotiationSequenceLocked(sequence)
	var writes []string
	kittyChanged := false
	kittyActive := false
	forwardSeq := ""

	if negotiation != nil && negotiation.Pending {
		t.scheduleKeyboardProtocolNegotiationBufferFlushLocked()
	} else if negotiation != nil {
		consumed, changed, active, w := t.handleKeyboardProtocolNegotiationSequenceLocked(negotiation.Parsed)
		writes = w
		kittyChanged = changed
		kittyActive = active
		if !consumed {
			forwardSeq = negotiation.Sequence
		}
	} else {
		forwardSeq = sequence
	}
	handler := t.inputHandler
	t.mu.Unlock()

	if flushedPrev != "" {
		t.forwardInputWithHandler(handler, flushedPrev)
	}
	for _, w := range writes {
		t.outWrite(w)
	}
	if kittyChanged {
		SetKittyProtocolActive(kittyActive)
	}
	if forwardSeq != "" {
		t.forwardInputWithHandler(handler, forwardSeq)
	}
}

func (t *ProcessTerminal) queryAndEnableKittyProtocol() {
	t.setupStdinBuffer()

	t.mu.Lock()
	t.keyboardProtocolPushed = true
	t.pendingKeyboardProtocolDeviceAttributes += 1
	t.clearKeyboardProtocolNegotiationBufferLocked()
	override := t.getenv("PI_PROGRAM_STATUS")
	t.programStatusSupported = override == "1"
	t.programStatusQueryPending = override != "1" && override != "0"
	programStatusQuery := ""
	if t.programStatusQueryPending {
		programStatusQuery = ProgramStatusQuery
	}
	query := termKittyKeyboardFlagsQuery + programStatusQuery + termDeviceAttributesQuery
	statusReplay := t.programStatusWriteLocked()
	t.mu.Unlock()

	t.outWrite(query)
	if statusReplay != "" {
		t.outWrite(statusReplay)
	}
}

func (t *ProcessTerminal) programStatusWriteLocked() string {
	if t.programStatusSupported && t.programStatus != nil {
		return FormatProgramStatus(*t.programStatus)
	}
	return ""
}

func (t *ProcessTerminal) handleKeyboardProtocolNegotiationSequence(negotiationSequence KeyboardProtocolNegotiationSequence) bool {
	t.mu.Lock()
	consumed, changed, active, writes := t.handleKeyboardProtocolNegotiationSequenceLocked(negotiationSequence)
	t.mu.Unlock()
	for _, w := range writes {
		t.outWrite(w)
	}
	if changed {
		SetKittyProtocolActive(active)
	}
	return consumed
}

func (t *ProcessTerminal) handleKeyboardProtocolNegotiationSequenceLocked(negotiationSequence KeyboardProtocolNegotiationSequence) (consumed bool, kittyChanged bool, kittyActive bool, writes []string) {
	t.clearKeyboardProtocolNegotiationBufferLocked()
	switch s := negotiationSequence.(type) {
	case *KeyboardProtocolNegotiationSequenceDeviceAttributes:
		if t.pendingKeyboardProtocolDeviceAttributes == 0 {
			return false, false, false, nil
		}
		t.pendingKeyboardProtocolDeviceAttributes -= 1
		if t.pendingKeyboardProtocolDeviceAttributes == 0 {
			t.programStatusQueryPending = false
		}
		if !t.kittyProtocolActive {
			if w := t.enableModifyOtherKeysLocked(); w != "" {
				writes = append(writes, w)
			}
		}
		return true, false, false, writes
	case *KeyboardProtocolNegotiationSequenceKittyFlags:
		if s.Flags != 0 {
			if w := t.disableModifyOtherKeysLocked(); w != "" {
				writes = append(writes, w)
			}
			if !t.kittyProtocolActive {
				t.kittyProtocolActive = true
				kittyChanged = true
				kittyActive = true
			}
		} else {
			if w := t.enableModifyOtherKeysLocked(); w != "" {
				writes = append(writes, w)
			}
		}
		return true, kittyChanged, kittyActive, writes
	}
	if !t.kittyProtocolActive {
		if w := t.enableModifyOtherKeysLocked(); w != "" {
			writes = append(writes, w)
		}
	}
	return true, false, false, writes
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
	t.mu.Lock()
	res, flushed := t.readKeyboardProtocolNegotiationSequenceLocked(sequence)
	handler := t.inputHandler
	t.mu.Unlock()
	if flushed != "" {
		t.forwardInputWithHandler(handler, flushed)
	}
	return res
}

func (t *ProcessTerminal) readKeyboardProtocolNegotiationSequenceLocked(sequence string) (*termKeyboardProtocolNegotiationRead, string) {
	flushed := ""
	if t.keyboardProtocolNegotiationBuffer != "" {
		bufferedSequence := t.keyboardProtocolNegotiationBuffer + sequence
		if parsed := ParseKeyboardProtocolNegotiationSequence(bufferedSequence); parsed != nil {
			t.clearKeyboardProtocolNegotiationBufferLocked()
			return &termKeyboardProtocolNegotiationRead{
				Parsed:   parsed,
				Sequence: bufferedSequence,
			}, flushed
		}
		if termIsKeyboardProtocolNegotiationSequencePrefix(bufferedSequence) {
			t.setKeyboardProtocolNegotiationBufferLocked(bufferedSequence)
			return &termKeyboardProtocolNegotiationRead{Pending: true}, flushed
		}
		flushed = t.takeKeyboardProtocolNegotiationBufferLocked()
	}

	if parsed := ParseKeyboardProtocolNegotiationSequence(sequence); parsed != nil {
		return &termKeyboardProtocolNegotiationRead{
			Parsed:   parsed,
			Sequence: sequence,
		}, flushed
	}
	if termIsKeyboardProtocolNegotiationSequencePrefix(sequence) {
		t.setKeyboardProtocolNegotiationBufferLocked(sequence)
		return &termKeyboardProtocolNegotiationRead{Pending: true}, flushed
	}
	return nil, flushed
}

func (t *ProcessTerminal) setKeyboardProtocolNegotiationBuffer(sequence string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setKeyboardProtocolNegotiationBufferLocked(sequence)
}

func (t *ProcessTerminal) setKeyboardProtocolNegotiationBufferLocked(sequence string) {
	t.clearKeyboardProtocolNegotiationBufferFlushTimerLocked()
	t.keyboardProtocolNegotiationBuffer = sequence
}

func (t *ProcessTerminal) clearKeyboardProtocolNegotiationBuffer() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.clearKeyboardProtocolNegotiationBufferLocked()
}

func (t *ProcessTerminal) clearKeyboardProtocolNegotiationBufferLocked() {
	t.clearKeyboardProtocolNegotiationBufferFlushTimerLocked()
	t.keyboardProtocolNegotiationBuffer = ""
}

func (t *ProcessTerminal) takeKeyboardProtocolNegotiationBufferLocked() string {
	seq := t.keyboardProtocolNegotiationBuffer
	t.clearKeyboardProtocolNegotiationBufferLocked()
	return seq
}

func (t *ProcessTerminal) flushKeyboardProtocolNegotiationBufferAsInput() {
	t.mu.Lock()
	if t.keyboardProtocolNegotiationBuffer == "" {
		t.mu.Unlock()
		return
	}
	seq := t.takeKeyboardProtocolNegotiationBufferLocked()
	handler := t.inputHandler
	t.mu.Unlock()
	t.forwardInputWithHandler(handler, seq)
}

func (t *ProcessTerminal) scheduleKeyboardProtocolNegotiationBufferFlush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scheduleKeyboardProtocolNegotiationBufferFlushLocked()
}

func (t *ProcessTerminal) scheduleKeyboardProtocolNegotiationBufferFlushLocked() {
	if t.keyboardProtocolNegotiationBuffer == "" || t.keyboardProtocolBufferFlushTimer != nil {
		return
	}
	t.flushTimerGen++
	gen := t.flushTimerGen
	t.keyboardProtocolBufferFlushTimer = time.AfterFunc(
		time.Duration(termKeyboardProtocolResponseFragmentTimeoutMs)*time.Millisecond,
		func() {
			t.mu.Lock()
			if t.flushTimerGen != gen || t.keyboardProtocolNegotiationBuffer == "" {
				t.mu.Unlock()
				return
			}
			seq := t.keyboardProtocolNegotiationBuffer
			t.keyboardProtocolNegotiationBuffer = ""
			t.keyboardProtocolBufferFlushTimer = nil
			t.flushTimerGen++
			handler := t.inputHandler
			t.mu.Unlock()
			t.forwardInputWithHandler(handler, seq)
		},
	)
}

func (t *ProcessTerminal) clearKeyboardProtocolNegotiationBufferFlushTimer() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.clearKeyboardProtocolNegotiationBufferFlushTimerLocked()
}

func (t *ProcessTerminal) clearKeyboardProtocolNegotiationBufferFlushTimerLocked() {
	t.flushTimerGen++
	if t.keyboardProtocolBufferFlushTimer == nil {
		return
	}
	t.keyboardProtocolBufferFlushTimer.Stop()
	t.keyboardProtocolBufferFlushTimer = nil
}

func (t *ProcessTerminal) forwardInputSequence(sequence string) {
	t.mu.Lock()
	handler := t.inputHandler
	t.mu.Unlock()
	t.forwardInputWithHandler(handler, sequence)
}

func (t *ProcessTerminal) forwardInputWithHandler(handler func(string), sequence string) {
	if handler == nil {
		return
	}
	shouldDetect := termIsNativeShiftEnterCandidate(sequence) &&
		(t.isAppleTerminalSession() || t.platformOSName() == "win32")
	isShift := shouldDetect && t.isNativeShiftPressed()
	input := NormalizeNativeShiftEnterInput(sequence, shouldDetect, isShift)
	handler(input)
}

func (t *ProcessTerminal) enableModifyOtherKeys() {
	t.mu.Lock()
	w := t.enableModifyOtherKeysLocked()
	t.mu.Unlock()
	if w != "" {
		t.outWrite(w)
	}
}

func (t *ProcessTerminal) enableModifyOtherKeysLocked() string {
	if t.kittyProtocolActive || t.modifyOtherKeysActive {
		return ""
	}
	t.modifyOtherKeysActive = true
	return "\x1b[>4;2m"
}

func (t *ProcessTerminal) disableModifyOtherKeys() {
	t.mu.Lock()
	w := t.disableModifyOtherKeysLocked()
	t.mu.Unlock()
	if w != "" {
		t.outWrite(w)
	}
}

func (t *ProcessTerminal) disableModifyOtherKeysLocked() string {
	if !t.modifyOtherKeysActive {
		return ""
	}
	t.modifyOtherKeysActive = false
	return "\x1b[>4;0m"
}

func (t *ProcessTerminal) enableWindowsVTInput() {
	if t.platformOSName() != "win32" {
		return
	}
	if t.enableWinVTFn != nil {
		_, _ = t.enableWinVTFn()
		return
	}
	_, _ = termEnableWindowsVTInput()
}

// DrainInput implements Terminal.
func (t *ProcessTerminal) DrainInput(maxMs *int64, idleMs *int64) error {
	maxWait := int64(1000)
	if maxMs != nil {
		maxWait = *maxMs
	}
	idleWait := int64(50)
	if idleMs != nil {
		idleWait = *idleMs
	}

	t.mu.Lock()
	shouldDisableKitty := t.keyboardProtocolPushed || t.kittyProtocolActive
	t.clearKeyboardProtocolNegotiationBufferLocked()
	var writes []string
	kittyReset := false
	if shouldDisableKitty {
		writes = append(writes, "\x1b[<u")
		t.keyboardProtocolPushed = false
		if t.kittyProtocolActive {
			t.kittyProtocolActive = false
		}
		kittyReset = true
	}
	if w := t.disableModifyOtherKeysLocked(); w != "" {
		writes = append(writes, w)
	}
	previousHandler := t.inputHandler
	t.inputHandler = nil
	readerRunning := t.stopStdinFn != nil
	skipOS := t.skipOSIO
	t.mu.Unlock()

	for _, w := range writes {
		t.outWrite(w)
	}
	if kittyReset {
		SetKittyProtocolActive(false)
	}

	defer func() {
		t.mu.Lock()
		if t.inputHandler == nil {
			t.inputHandler = previousHandler
		}
		t.mu.Unlock()
	}()

	if !skipOS && !readerRunning && termIsStdinTerminal(os.Stdin) {
		termDrainStdinDirect(os.Stdin, maxWait, idleWait)
		return nil
	}

	now := time.Now()
	t.lastDrainDataNs.Store(now.UnixNano())
	endTime := now.Add(time.Duration(maxWait) * time.Millisecond)
	idleDur := time.Duration(idleWait) * time.Millisecond

	for {
		cur := time.Now()
		timeLeft := endTime.Sub(cur)
		if timeLeft <= 0 {
			break
		}
		lastNs := t.lastDrainDataNs.Load()
		if cur.Sub(time.Unix(0, lastNs)) >= idleDur {
			break
		}
		sleepDur := idleDur
		if timeLeft < sleepDur {
			sleepDur = timeLeft
		}
		if sleepDur <= 0 {
			break
		}
		time.Sleep(sleepDur)
	}
	return nil
}

// Stop implements Terminal.
func (t *ProcessTerminal) Stop() {
	t.mu.Lock()
	var writes []string
	if t.clearProgressIntervalLocked() {
		writes = append(writes, termTerminalProgressClearSequence)
	}
	if t.programStatusSupported && t.programStatus != nil {
		writes = append(writes, FormatProgramStatus(ProgramStatus{State: ProgramStatusStateClear}))
	}
	t.programStatusSupported = false
	t.programStatusQueryPending = false

	writes = append(writes, "\x1b[?2004l")

	shouldDisableKitty := t.keyboardProtocolPushed || t.kittyProtocolActive
	t.clearKeyboardProtocolNegotiationBufferLocked()
	kittyReset := shouldDisableKitty
	if shouldDisableKitty {
		writes = append(writes, "\x1b[<u")
		t.keyboardProtocolPushed = false
		t.kittyProtocolActive = false
	}
	if w := t.disableModifyOtherKeysLocked(); w != "" {
		writes = append(writes, w)
	}

	sb := t.stdinBuffer
	t.stdinBuffer = nil
	t.stdinDataHandler = nil
	t.inputHandler = nil
	t.resizeHandler = nil
	stopStdin := t.stopStdinFn
	t.stopStdinFn = nil
	stopResize := t.stopResizeFn
	t.stopResizeFn = nil
	prevState := t.prevTermState
	t.prevTermState = nil
	t.mu.Unlock()

	for _, w := range writes {
		t.outWrite(w)
	}
	if kittyReset {
		SetKittyProtocolActive(false)
	}
	if stopStdin != nil {
		stopStdin()
	}
	if sb != nil {
		sb.Destroy()
	}
	if stopResize != nil {
		stopResize()
	}
	if prevState != nil {
		termRestoreRaw(os.Stdin, prevState)
	}
}

// RestoreOnExit synchronously restores terminal modes during process exit if Stop did not run.
func (t *ProcessTerminal) RestoreOnExit(before string, after string) {
	t.mu.Lock()
	var seq strings.Builder
	seq.WriteString(before)
	if t.progressInterval != nil {
		seq.WriteString(termTerminalProgressClearSequence)
	}
	seq.WriteString("\x1b[?2004l")
	if t.keyboardProtocolPushed || t.kittyProtocolActive {
		seq.WriteString("\x1b[<u")
	}
	if t.modifyOtherKeysActive {
		seq.WriteString("\x1b[>4;0m")
	}
	seq.WriteString(after)
	out := seq.String()
	t.mu.Unlock()

	t.outSyncWrite(out)
}

// Write implements Terminal.
func (t *ProcessTerminal) Write(data string) {
	t.outWrite(data)
	t.mu.Lock()
	logPath := t.writeLogPath
	t.mu.Unlock()
	if logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			_, _ = f.WriteString(data)
			_ = f.Close()
		}
	}
}

// Columns implements Terminal.
func (t *ProcessTerminal) Columns() int {
	if t.getSizeFn != nil {
		if c, _, ok := t.getSizeFn(); ok && c > 0 {
			return c
		}
	} else if c, _, ok := termGetWindowSize(os.Stdout); ok && c > 0 {
		return c
	}
	if c, err := strconv.Atoi(t.getenv("COLUMNS")); err == nil && c > 0 {
		return c
	}
	return 80
}

// Rows implements Terminal.
func (t *ProcessTerminal) Rows() int {
	if t.getSizeFn != nil {
		if _, r, ok := t.getSizeFn(); ok && r > 0 {
			return r
		}
	} else if _, r, ok := termGetWindowSize(os.Stdout); ok && r > 0 {
		return r
	}
	if r, err := strconv.Atoi(t.getenv("LINES")); err == nil && r > 0 {
		return r
	}
	return 24
}

// MoveBy implements Terminal.
func (t *ProcessTerminal) MoveBy(lines int) {
	if lines > 0 {
		t.outWrite(fmt.Sprintf("\x1b[%dB", lines))
	} else if lines < 0 {
		t.outWrite(fmt.Sprintf("\x1b[%dA", -lines))
	}
}

// HideCursor implements Terminal.
func (t *ProcessTerminal) HideCursor() {
	t.outWrite("\x1b[?25l")
}

// ShowCursor implements Terminal.
func (t *ProcessTerminal) ShowCursor() {
	t.outWrite("\x1b[?25h")
}

// ClearLine implements Terminal.
func (t *ProcessTerminal) ClearLine() {
	t.outWrite("\x1b[K")
}

// ClearFromCursor implements Terminal.
func (t *ProcessTerminal) ClearFromCursor() {
	t.outWrite("\x1b[J")
}

// ClearScreen implements Terminal.
func (t *ProcessTerminal) ClearScreen() {
	t.outWrite("\x1b[2J\x1b[H")
}

// SetTitle implements Terminal.
func (t *ProcessTerminal) SetTitle(title string) {
	t.outWrite("\x1b]0;" + ReplaceControlCharacters(title) + "\x07")
}

// SetProgramStatus implements Terminal.
func (t *ProcessTerminal) SetProgramStatus(status ProgramStatus) {
	t.mu.Lock()
	if status.State == ProgramStatusStateClear {
		t.programStatus = nil
	} else {
		copyStatus := status
		t.programStatus = &copyStatus
	}
	supported := t.programStatusSupported
	t.mu.Unlock()
	if supported {
		t.outWrite(FormatProgramStatus(status))
	}
}

// SetProgress implements Terminal.
func (t *ProcessTerminal) SetProgress(active bool) {
	if active {
		t.outWrite(termTerminalProgressActiveSequence)
		t.mu.Lock()
		if t.progressInterval == nil {
			ticker := time.NewTicker(time.Duration(termTerminalProgressKeepaliveMs) * time.Millisecond)
			done := make(chan struct{})
			t.progressInterval = ticker
			t.progressDone = done
			t.mu.Unlock()

			go func(ch <-chan time.Time, stop <-chan struct{}) {
				for {
					select {
					case <-ch:
						t.outWrite(termTerminalProgressActiveSequence)
					case <-stop:
						return
					}
				}
			}(ticker.C, done)
			return
		}
		t.mu.Unlock()
		return
	}

	t.clearProgressInterval()
	t.outWrite(termTerminalProgressClearSequence)
}

func (t *ProcessTerminal) clearProgressInterval() bool {
	t.mu.Lock()
	cleared := t.clearProgressIntervalLocked()
	t.mu.Unlock()
	return cleared
}

func (t *ProcessTerminal) clearProgressIntervalLocked() bool {
	if t.progressInterval == nil {
		return false
	}
	t.progressInterval.Stop()
	t.progressInterval = nil
	if t.progressDone != nil {
		close(t.progressDone)
		t.progressDone = nil
	}
	return true
}

var (
	_ Terminal            = (*ProcessTerminal)(nil)
	_ ExitRestoreTerminal = (*ProcessTerminal)(nil)
)
