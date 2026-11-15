// Ported from packages/tui/src/stdin-buffer.ts (pi v1.5.2).

package tui

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	sbEsc                      = "\x1b"
	sbDefaultSequenceTimeoutMs = 50
	sbDefaultEscapeTimeoutMs   = 10
	sbBracketedPasteStart      = "\x1b[200~"
	sbBracketedPasteEnd        = "\x1b[201~"
)

var (
	sbTerminalReplyHeaderPattern = regexp.MustCompile("^\x1b(?:\\](?:1[01];|4;\\d{1,3};|7501;\\?)|P>\\||_G(?:[a-z]=\\d+,)*[a-z]=\\d+;)")
	sbMouseSgrRegex              = regexp.MustCompile(`^<\d+;\d+;\d+[Mm]$`)
	sbDigitsOnlyRegex            = regexp.MustCompile(`^\d+$`)
	sbKittyUnmodPrintableRegex   = regexp.MustCompile("^\x1b\\[(\\d+)(?::\\d*)?(?::\\d+)?u$")
)

type sbSeqStatus int

const (
	sbSeqComplete sbSeqStatus = iota
	sbSeqIncomplete
	sbSeqNotEscape
)

func sbIsControlString(data string) bool {
	return strings.HasPrefix(data, sbEsc+"]") || strings.HasPrefix(data, sbEsc+"P") || strings.HasPrefix(data, sbEsc+"_")
}

func sbIsCompleteSequence(data string) sbSeqStatus {
	if !strings.HasPrefix(data, sbEsc) {
		return sbSeqNotEscape
	}
	if len(data) == 1 {
		return sbSeqIncomplete
	}
	afterEsc := data[1:]

	if strings.HasPrefix(afterEsc, "[") {
		if strings.HasPrefix(afterEsc, "[M") {
			if len(data) >= 6 {
				return sbSeqComplete
			}
			return sbSeqIncomplete
		}
		return sbIsCompleteCsiSequence(data)
	}
	if strings.HasPrefix(afterEsc, "]") {
		return sbIsCompleteOscSequence(data)
	}
	if strings.HasPrefix(afterEsc, "P") {
		return sbIsCompleteDcsSequence(data)
	}
	if strings.HasPrefix(afterEsc, "_") {
		return sbIsCompleteApcSequence(data)
	}
	if strings.HasPrefix(afterEsc, "O") {
		if len(afterEsc) >= 2 {
			return sbSeqComplete
		}
		return sbSeqIncomplete
	}
	_, rSize := utf8.DecodeRuneInString(afterEsc)
	if rSize > 0 && rSize == len(afterEsc) {
		return sbSeqComplete
	}
	return sbSeqComplete
}

func sbIsCompleteCsiSequence(data string) sbSeqStatus {
	if !strings.HasPrefix(data, sbEsc+"[") {
		return sbSeqComplete
	}
	if len(data) < 3 {
		return sbSeqIncomplete
	}
	payload := data[2:]
	lastChar := payload[len(payload)-1]
	if lastChar >= 0x40 && lastChar <= 0x7e {
		if strings.HasPrefix(payload, "<") {
			if sbMouseSgrRegex.MatchString(payload) {
				return sbSeqComplete
			}
			if lastChar == 'M' || lastChar == 'm' {
				parts := strings.Split(payload[1:len(payload)-1], ";")
				if len(parts) == 3 {
					allDigits := true
					for _, p := range parts {
						if !sbDigitsOnlyRegex.MatchString(p) {
							allDigits = false
							break
						}
					}
					if allDigits {
						return sbSeqComplete
					}
				}
			}
			return sbSeqIncomplete
		}
		return sbSeqComplete
	}
	return sbSeqIncomplete
}

func sbIsCompleteOscSequence(data string) sbSeqStatus {
	if !strings.HasPrefix(data, sbEsc+"]") {
		return sbSeqComplete
	}
	if strings.HasSuffix(data, sbEsc+"\\") || strings.HasSuffix(data, "\x07") {
		return sbSeqComplete
	}
	return sbSeqIncomplete
}

func sbIsCompleteDcsSequence(data string) sbSeqStatus {
	if !strings.HasPrefix(data, sbEsc+"P") {
		return sbSeqComplete
	}
	if strings.HasSuffix(data, sbEsc+"\\") {
		return sbSeqComplete
	}
	return sbSeqIncomplete
}

func sbIsCompleteApcSequence(data string) sbSeqStatus {
	if !strings.HasPrefix(data, sbEsc+"_") {
		return sbSeqComplete
	}
	if strings.HasSuffix(data, sbEsc+"\\") {
		return sbSeqComplete
	}
	return sbSeqIncomplete
}

func sbParseUnmodifiedKittyPrintableCodepoint(sequence string) *int {
	m := sbKittyUnmodPrintableRegex.FindStringSubmatch(sequence)
	if m == nil {
		return nil
	}
	cp, err := strconv.Atoi(m[1])
	if err != nil || cp < 32 {
		return nil
	}
	return &cp
}

type sbExtractResult struct {
	sequences []string
	remainder string
}

func sbExtractCompleteSequences(buffer string) sbExtractResult {
	var sequences []string
	pos := 0

	for pos < len(buffer) {
		remaining := buffer[pos:]
		if strings.HasPrefix(remaining, sbEsc) {
			terminalReply := sbIsControlString(remaining) && sbTerminalReplyHeaderPattern.MatchString(remaining)
			seqEnd := 1
			matched := false
			for seqEnd <= len(remaining) {
				if terminalReply && seqEnd > 2 {
					ch := remaining[seqEnd-1]
					if ch == 0x18 || ch == 0x1a {
						pos += seqEnd
						matched = true
						break
					}
					if ch == '\x1b' && seqEnd < len(remaining) && remaining[seqEnd] != '\\' {
						pos += seqEnd - 1
						matched = true
						break
					}
				}
				candidate := remaining[:seqEnd]
				status := sbIsCompleteSequence(candidate)
				if status == sbSeqComplete {
					if candidate == "\x1b\x1b" && seqEnd < len(remaining) {
						next := remaining[seqEnd]
						if next == '[' || next == ']' || next == 'O' || next == 'P' || next == '_' {
							sequences = append(sequences, sbEsc)
							pos++
							matched = true
							break
						}
					}
					sequences = append(sequences, candidate)
					pos += seqEnd
					matched = true
					break
				} else if status == sbSeqIncomplete {
					if seqEnd == len(remaining) {
						seqEnd++
						break
					}
					_, rSize := utf8.DecodeRuneInString(remaining[seqEnd:])
					if rSize <= 0 {
						rSize = 1
					}
					seqEnd += rSize
				} else {
					sequences = append(sequences, candidate)
					pos += seqEnd
					matched = true
					break
				}
			}
			if !matched {
				return sbExtractResult{sequences: sequences, remainder: remaining}
			}
		} else {
			_, rSize := utf8.DecodeRuneInString(remaining)
			if rSize <= 0 {
				rSize = 1
			}
			sequences = append(sequences, remaining[:rSize])
			pos += rSize
		}
	}

	return sbExtractResult{sequences: sequences, remainder: ""}
}

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

type sbStringSub struct {
	id int
	fn func(string)
}

// StdinBuffer buffers stdin and emits complete sequences.
// Partial escape sequences that arrive across chunks stay buffered until complete or timed out.
// mu guards the fields. Do not hold it across listener calls.
type StdinBuffer struct {
	mu                             sync.Mutex
	buffer                         string
	timeout                        *time.Timer
	timerGen                       uint64
	timeoutMs                      int64
	escapeTimeoutMs                int64
	pasteMode                      bool
	pasteBuffer                    string
	pendingKittyPrintableCodepoint *int
	nextSubID                      int
	dataListeners                  []sbStringSub
	pasteListeners                 []sbStringSub
}

// NewStdinBuffer constructs a buffer. Nil options use the defaults.
func NewStdinBuffer(options *StdinBufferOptions) *StdinBuffer {
	timeoutMs := int64(sbDefaultSequenceTimeoutMs)
	escapeTimeoutMs := int64(sbDefaultEscapeTimeoutMs)
	if options != nil {
		if options.Timeout != nil {
			timeoutMs = *options.Timeout
		}
		if options.EscapeTimeout != nil {
			escapeTimeoutMs = *options.EscapeTimeout
		}
	}
	return &StdinBuffer{
		timeoutMs:       timeoutMs,
		escapeTimeoutMs: escapeTimeoutMs,
	}
}

func (b *StdinBuffer) stopTimerLocked() {
	b.timerGen++
	if b.timeout != nil {
		b.timeout.Stop()
		b.timeout = nil
	}
}

func (b *StdinBuffer) snapshotDataListenersLocked() []func(string) {
	if len(b.dataListeners) == 0 {
		return nil
	}
	fns := make([]func(string), len(b.dataListeners))
	for i, s := range b.dataListeners {
		fns[i] = s.fn
	}
	return fns
}

func (b *StdinBuffer) snapshotPasteListenersLocked() []func(string) {
	if len(b.pasteListeners) == 0 {
		return nil
	}
	fns := make([]func(string), len(b.pasteListeners))
	for i, s := range b.pasteListeners {
		fns[i] = s.fn
	}
	return fns
}

func sbInvokeListeners(fns []func(string), payload string) {
	for _, fn := range fns {
		if fn != nil {
			fn(payload)
		}
	}
}

func (b *StdinBuffer) onTimeout(gen uint64) {
	b.mu.Lock()
	if gen != b.timerGen {
		b.mu.Unlock()
		return
	}
	b.timeout = nil
	flushed := b.flushLocked()
	b.mu.Unlock()

	for _, sequence := range flushed {
		if len(sequence) > 2 && sbIsControlString(sequence) {
			b.emitDataSequence(sequence[:2])
			b.Process(sequence[2:])
		} else {
			b.emitDataSequence(sequence)
		}
	}
}

// Process feeds a string chunk (TS process string overload).
func (b *StdinBuffer) Process(data string) {
	b.mu.Lock()
	b.stopTimerLocked()

	if len(data) == 0 && len(b.buffer) == 0 {
		var events []string
		b.emitDataSequenceLocked("", &events)
		subs := b.snapshotDataListenersLocked()
		b.mu.Unlock()
		for _, ev := range events {
			sbInvokeListeners(subs, ev)
		}
		return
	}

	b.buffer += data

	if b.pasteMode {
		b.pasteBuffer += b.buffer
		b.buffer = ""

		if endIndex := strings.Index(b.pasteBuffer, sbBracketedPasteEnd); endIndex != -1 {
			pastedContent := b.pasteBuffer[:endIndex]
			remaining := b.pasteBuffer[endIndex+len(sbBracketedPasteEnd):]

			b.pasteMode = false
			b.pasteBuffer = ""
			b.pendingKittyPrintableCodepoint = nil

			pasteSubs := b.snapshotPasteListenersLocked()
			b.mu.Unlock()
			sbInvokeListeners(pasteSubs, pastedContent)
			if len(remaining) > 0 {
				b.Process(remaining)
			}
			return
		}

		b.mu.Unlock()
		return
	}

	if startIndex := strings.Index(b.buffer, sbBracketedPasteStart); startIndex != -1 {
		var events []string
		if startIndex > 0 {
			beforePaste := b.buffer[:startIndex]
			res := sbExtractCompleteSequences(beforePaste)
			for _, seq := range res.sequences {
				b.emitDataSequenceLocked(seq, &events)
			}
		}

		b.pendingKittyPrintableCodepoint = nil
		b.buffer = b.buffer[startIndex+len(sbBracketedPasteStart):]
		b.pasteMode = true
		b.pasteBuffer = b.buffer
		b.buffer = ""

		var hasPaste bool
		var pastedContent, remaining string
		var pasteSubs []func(string)
		if endIndex := strings.Index(b.pasteBuffer, sbBracketedPasteEnd); endIndex != -1 {
			pastedContent = b.pasteBuffer[:endIndex]
			remaining = b.pasteBuffer[endIndex+len(sbBracketedPasteEnd):]

			b.pasteMode = false
			b.pasteBuffer = ""
			b.pendingKittyPrintableCodepoint = nil

			hasPaste = true
			pasteSubs = b.snapshotPasteListenersLocked()
		}

		dataSubs := b.snapshotDataListenersLocked()
		b.mu.Unlock()

		for _, ev := range events {
			sbInvokeListeners(dataSubs, ev)
		}
		if hasPaste {
			sbInvokeListeners(pasteSubs, pastedContent)
			if len(remaining) > 0 {
				b.Process(remaining)
			}
		}
		return
	}

	res := sbExtractCompleteSequences(b.buffer)
	b.buffer = res.remainder

	var events []string
	for _, seq := range res.sequences {
		b.emitDataSequenceLocked(seq, &events)
	}

	if len(b.buffer) > 0 && !sbTerminalReplyHeaderPattern.MatchString(b.buffer) {
		delayMs := b.timeoutMs
		if b.buffer == sbEsc {
			delayMs = b.escapeTimeoutMs
		}
		gen := b.timerGen
		b.timeout = time.AfterFunc(time.Duration(delayMs)*time.Millisecond, func() {
			b.onTimeout(gen)
		})
	}

	dataSubs := b.snapshotDataListenersLocked()
	b.mu.Unlock()

	for _, ev := range events {
		sbInvokeListeners(dataSubs, ev)
	}
}

// ProcessBytes feeds a raw byte chunk (TS process Buffer overload).
// A one-byte chunk above 127 is ESC plus (byte - 128).
func (b *StdinBuffer) ProcessBytes(data []byte) {
	var str string
	if len(data) == 1 && data[0] > 127 {
		str = string([]byte{'\x1b', data[0] - 128})
	} else {
		str = string(data)
	}
	b.Process(str)
}

// OnData registers a listener for complete sequences (TS "data").
// The returned function unsubscribes.
func (b *StdinBuffer) OnData(listener func(sequence string)) (unsubscribe func()) {
	b.mu.Lock()
	b.nextSubID++
	id := b.nextSubID
	b.dataListeners = append(b.dataListeners, sbStringSub{id: id, fn: listener})
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		for i, s := range b.dataListeners {
			if s.id == id {
				b.dataListeners = append(b.dataListeners[:i], b.dataListeners[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}
}

// OnPaste registers a listener for bracketed-paste content (TS "paste").
// The returned function unsubscribes. Content does not include the paste markers.
func (b *StdinBuffer) OnPaste(listener func(content string)) (unsubscribe func()) {
	b.mu.Lock()
	b.nextSubID++
	id := b.nextSubID
	b.pasteListeners = append(b.pasteListeners, sbStringSub{id: id, fn: listener})
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		for i, s := range b.pasteListeners {
			if s.id == id {
				b.pasteListeners = append(b.pasteListeners[:i], b.pasteListeners[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}
}

func (b *StdinBuffer) flushLocked() []string {
	b.stopTimerLocked()
	if len(b.buffer) == 0 {
		return []string{}
	}
	sequences := []string{b.buffer}
	b.buffer = ""
	b.pendingKittyPrintableCodepoint = nil
	return sequences
}

// Flush stops the pending timer and returns any incomplete buffer as one sequence.
func (b *StdinBuffer) Flush() []string {
	b.mu.Lock()
	sequences := b.flushLocked()
	b.mu.Unlock()
	return sequences
}

// Clear drops the buffer, paste state, and pending timer.
func (b *StdinBuffer) Clear() {
	b.mu.Lock()
	b.stopTimerLocked()
	b.buffer = ""
	b.pasteMode = false
	b.pasteBuffer = ""
	b.pendingKittyPrintableCodepoint = nil
	b.mu.Unlock()
}

// GetBuffer returns the incomplete input still buffered.
func (b *StdinBuffer) GetBuffer() string {
	b.mu.Lock()
	buf := b.buffer
	b.mu.Unlock()
	return buf
}

// Destroy clears the buffer and cancels the pending timer.
func (b *StdinBuffer) Destroy() {
	b.Clear()
}

func (b *StdinBuffer) emitDataSequenceLocked(sequence string, out *[]string) {
	if r, size := utf8.DecodeRuneInString(sequence); len(sequence) > 0 && size == len(sequence) && r <= 0xffff && b.pendingKittyPrintableCodepoint != nil && int(r) == *b.pendingKittyPrintableCodepoint {
		b.pendingKittyPrintableCodepoint = nil
		return
	}
	b.pendingKittyPrintableCodepoint = sbParseUnmodifiedKittyPrintableCodepoint(sequence)
	*out = append(*out, sequence)
}

func (b *StdinBuffer) emitDataSequence(sequence string) {
	b.mu.Lock()
	var out []string
	b.emitDataSequenceLocked(sequence, &out)
	subs := b.snapshotDataListenersLocked()
	b.mu.Unlock()
	for _, ev := range out {
		sbInvokeListeners(subs, ev)
	}
}
