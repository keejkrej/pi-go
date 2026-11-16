// Ported from packages/durable/src/harness/output.ts (pi v1.0.0).

package durable

import (
	"bytes"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/chord"
)

// OutputLimits configures retained bytes, lines, and retention direction for tool output.
type OutputLimits struct {
	MaxBytes int
	MaxLines int
	Retain   ToolOutputRetain
}

// BoundedOutput holds sanitized retained text and exact counts of dropped bytes and lines.
type BoundedOutput struct {
	Text         string
	DroppedBytes int
	DroppedLines int
}

// OutputSlice holds an exact slice within OutputLimits alongside byte and line drop counts.
type OutputSlice struct {
	Text         string
	Bytes        int
	DroppedBytes int
	DroppedLines int
}

type hoChunk struct {
	text     string
	bytes    int
	newlines int
}

// SanitizeOutput removes control characters other than tab and newline that break transcripts and display.
func SanitizeOutput(text string) string {
	var b strings.Builder
	cleaned := false
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if hoIsInvalidRune(r) {
			if !cleaned {
				b.Grow(len(text))
				b.WriteString(text[:i])
				cleaned = true
			}
		} else if cleaned {
			b.WriteRune(r)
		}
		i += size
	}
	if !cleaned {
		return text
	}
	return b.String()
}

func hoIsInvalidRune(r rune) bool {
	if r <= 0x08 || r == 0x0b || (r >= 0x0c && r <= 0x1f) {
		return true
	}
	return r >= 0xfff9 && r <= 0xfffb
}

// BoundOutput bounds text to whole lines within limits, keeping head or tail. A single line
// longer than MaxBytes is cut at the byte limit on a UTF-8 character boundary.
func BoundOutput(text string, limits OutputLimits) OutputSlice {
	b := []byte(text)
	var from, to int
	if limits.Retain == ToolOutputRetainHead {
		from, to = hoHeadRange(b, limits)
	} else {
		from, to = hoTailRange(b, limits)
	}
	kept := b[from:to]
	keptText := text
	if len(kept) != len(b) {
		keptText = string(kept)
	}
	return OutputSlice{
		Text:         keptText,
		Bytes:        len(kept),
		DroppedBytes: len(b) - len(kept),
		DroppedLines: hoLineCountBytes(b) - hoLineCountBytes(kept),
	}
}

func hoHeadRange(b []byte, limits OutputLimits) (int, int) {
	if limits.MaxLines == 0 || limits.MaxBytes == 0 {
		return 0, 0
	}
	end := len(b)
	lines := 0
	pos := 0
	for {
		idx := bytes.IndexByte(b[pos:], '\n')
		if idx == -1 {
			break
		}
		lines++
		abs := pos + idx
		if lines == limits.MaxLines {
			end = abs + 1
			break
		}
		pos = abs + 1
	}
	if end > limits.MaxBytes {
		newline := bytes.LastIndexByte(b[:limits.MaxBytes], '\n')
		if newline == -1 {
			end = CharacterEnd(b, limits.MaxBytes)
		} else {
			end = newline + 1
		}
	}
	return 0, end
}

func hoTailRange(b []byte, limits OutputLimits) (int, int) {
	if limits.MaxLines == 0 || limits.MaxBytes == 0 {
		return len(b), len(b)
	}
	last := len(b) - 1
	if last >= 0 && b[last] == '\n' {
		last--
	}
	start := 0
	lines := 1
	searchEnd := last + 1
	for searchEnd > 0 {
		idx := bytes.LastIndexByte(b[:searchEnd], '\n')
		if idx == -1 {
			break
		}
		if lines == limits.MaxLines {
			start = idx + 1
			break
		}
		lines++
		searchEnd = idx
	}
	if len(b)-start > limits.MaxBytes {
		from := len(b) - limits.MaxBytes
		if from > 0 {
			if idx := bytes.IndexByte(b[from-1:], '\n'); idx != -1 && from-1+idx+1 < len(b) {
				start = from - 1 + idx + 1
				return start, len(b)
			}
		} else if idx := bytes.IndexByte(b, '\n'); idx != -1 && idx+1 < len(b) {
			start = idx + 1
			return start, len(b)
		}
		start = hoCharacterStart(b, from)
	}
	return start, len(b)
}

// CharacterEnd returns the last UTF-8 character boundary at or before index.
func CharacterEnd(b []byte, index int) int {
	if index >= len(b) {
		return len(b)
	}
	if index <= 0 {
		return 0
	}
	end := index
	for end > 0 && (b[end]&0xc0) == 0x80 {
		end--
	}
	return end
}

func hoCharacterStart(b []byte, index int) int {
	start := index
	if start < 0 {
		start = 0
	}
	for start < len(b) && (b[start]&0xc0) == 0x80 {
		start++
	}
	return start
}

func hoLineCountBytes(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	newlines := bytes.Count(b, []byte{'\n'})
	if b[len(b)-1] == '\n' {
		return newlines
	}
	return newlines + 1
}

// OutputBuffer maintains bounded running tool output while tracking exact stream totals.
// It decodes incremental byte streams cleanly across UTF-8 rune boundaries.
type OutputBuffer struct {
	mu              sync.Mutex
	limits          OutputLimits
	pendingBytes    []byte
	chunks          []hoChunk
	storedBytes     int
	storedNewlines  int
	full            bool
	totalBytes      int
	totalNewlines   int
	endsWithNewline bool
}

// NewOutputBuffer creates a bounded OutputBuffer configured by limits.
func NewOutputBuffer(limits OutputLimits) *OutputBuffer {
	return &OutputBuffer{
		limits:          limits,
		endsWithNewline: true,
	}
}

// StoredBytes returns bytes currently buffered.
func (o *OutputBuffer) StoredBytes() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.storedBytes
}

// Push accepts a string chunk into the output buffer and reports whether anything was accepted.
func (o *OutputBuffer) Push(text string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	flushed := o.flushIncompleteBytesLocked()
	return o.acceptLocked(flushed + text)
}

// PushBytes accepts a raw UTF-8 byte slice into the output buffer, buffering trailing partial runes.
func (o *OutputBuffer) PushBytes(data []byte) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	var buf []byte
	if len(o.pendingBytes) > 0 {
		buf = make([]byte, len(o.pendingBytes)+len(data))
		copy(buf, o.pendingBytes)
		copy(buf[len(o.pendingBytes):], data)
		o.pendingBytes = nil
	} else {
		buf = data
	}
	if len(buf) == 0 {
		return false
	}
	// Hold back any incomplete trailing UTF-8 sequence (at most 3 bytes).
	holdStart := len(buf)
	for i := len(buf) - 1; i >= 0 && len(buf)-i <= 4; i-- {
		if utf8.RuneStart(buf[i]) {
			if !utf8.FullRune(buf[i:]) {
				holdStart = i
			}
			break
		}
	}
	if holdStart < len(buf) {
		o.pendingBytes = append([]byte(nil), buf[holdStart:]...)
		buf = buf[:holdStart]
	}
	if len(buf) == 0 {
		return false
	}
	return o.acceptLocked(hoDecodeValidUTF8(buf))
}

// End flushes any incomplete trailing UTF-8 bytes as replacement runes when the stream ends.
func (o *OutputBuffer) End() {
	o.mu.Lock()
	defer o.mu.Unlock()
	flushed := o.flushIncompleteBytesLocked()
	if flushed != "" {
		o.acceptLocked(flushed)
	}
}

func (o *OutputBuffer) flushIncompleteBytesLocked() string {
	if len(o.pendingBytes) == 0 {
		return ""
	}
	s := hoDecodeValidUTF8(o.pendingBytes)
	o.pendingBytes = nil
	return s
}

func hoDecodeValidUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			size = hoMaximalSubpart(b[i:])
		}
		sb.WriteRune(r)
		i += size
	}
	return sb.String()
}

// hoMaximalSubpart returns the WHATWG UTF-8 maximal subpart length for an
// ill-formed sequence starting at b[0] (when utf8.DecodeRune returned RuneError, 1).
func hoMaximalSubpart(b []byte) int {
	b0 := b[0]
	if len(b) < 2 {
		return 1
	}
	b1 := b[1]
	switch {
	case b0 == 0xE0:
		if b1 >= 0xA0 && b1 <= 0xBF {
			return 2
		}
	case b0 == 0xED:
		if b1 >= 0x80 && b1 <= 0x9F {
			return 2
		}
	case b0 >= 0xE1 && b0 <= 0xEF:
		if b1 >= 0x80 && b1 <= 0xBF {
			return 2
		}
	case b0 == 0xF0:
		if b1 >= 0x90 && b1 <= 0xBF {
			if len(b) >= 3 && b[2] >= 0x80 && b[2] <= 0xBF {
				return 3
			}
			return 2
		}
	case b0 >= 0xF1 && b0 <= 0xF3:
		if b1 >= 0x80 && b1 <= 0xBF {
			if len(b) >= 3 && b[2] >= 0x80 && b[2] <= 0xBF {
				return 3
			}
			return 2
		}
	case b0 == 0xF4:
		if b1 >= 0x80 && b1 <= 0x8F {
			if len(b) >= 3 && b[2] >= 0x80 && b[2] <= 0xBF {
				return 3
			}
			return 2
		}
	}
	return 1
}

func (o *OutputBuffer) acceptLocked(text string) bool {
	if len(text) == 0 {
		return false
	}
	nBytes := Utf8ByteLength(text)
	newlines := strings.Count(text, "\n")
	o.totalBytes += nBytes
	o.totalNewlines += newlines
	o.endsWithNewline = strings.HasSuffix(text, "\n")
	if o.full {
		return true
	}
	o.chunks = append(o.chunks, hoChunk{text: text, bytes: nBytes, newlines: newlines})
	o.storedBytes += nBytes
	o.storedNewlines += newlines
	if o.limits.Retain == ToolOutputRetainHead {
		o.full = o.storedBytes > o.limits.MaxBytes || o.storedNewlines >= o.limits.MaxLines
		return true
	}
	for len(o.chunks) > 1 {
		first := o.chunks[0]
		bytesAfter := o.storedBytes - first.bytes
		newlinesAfter := o.storedNewlines - first.newlines
		if bytesAfter <= o.limits.MaxBytes+1 && newlinesAfter <= o.limits.MaxLines+1 {
			break
		}
		o.chunks = o.chunks[1:]
		o.storedBytes = bytesAfter
		o.storedNewlines = newlinesAfter
	}
	return true
}

// Snapshot returns sanitized retained output alongside exact byte and line drop counts.
func (o *OutputBuffer) Snapshot() BoundedOutput {
	o.mu.Lock()
	defer o.mu.Unlock()
	var stored string
	if len(o.chunks) == 1 {
		stored = o.chunks[0].text
	} else if len(o.chunks) > 1 {
		var sb strings.Builder
		sb.Grow(o.storedBytes)
		for _, c := range o.chunks {
			sb.WriteString(c.text)
		}
		stored = sb.String()
	}
	kept := BoundOutput(stored, o.limits)
	storedLines := hoLines(o.storedNewlines, stored == "" || strings.HasSuffix(stored, "\n"))
	keptLines := storedLines - kept.DroppedLines
	if o.limits.Retain == ToolOutputRetainTail || len(o.chunks) > 1 {
		text := stored
		bytesCount := o.storedBytes
		if o.limits.Retain == ToolOutputRetainTail {
			text = kept.Text
			bytesCount = kept.Bytes
		}
		if text == "" {
			o.chunks = nil
			o.storedNewlines = 0
		} else {
			nl := strings.Count(text, "\n")
			o.chunks = []hoChunk{{text: text, bytes: bytesCount, newlines: nl}}
			o.storedNewlines = nl
		}
		o.storedBytes = bytesCount
	}
	return BoundedOutput{
		Text:         SanitizeOutput(kept.Text),
		DroppedBytes: o.totalBytes - kept.Bytes,
		DroppedLines: hoLines(o.totalNewlines, o.endsWithNewline) - keptLines,
	}
}

func hoLines(newlines int, terminated bool) int {
	if terminated {
		return newlines
	}
	return newlines + 1
}

const (
	hoMinProgressIntervalMs = int64(100)
	hoProgressBytesPerSec   = int64(100 * 1024)
)

type hoTimer interface {
	Stop() bool
}

// hoProgress schedules throttled asynchronous commits of tool progress updates.
type hoProgress struct {
	write     func() (int, error)
	onError   func(error)
	nowMs     func() int64
	afterFunc func(time.Duration, func()) hoTimer

	mu           sync.Mutex
	waiters      []chan error
	timer        hoTimer
	inFlight     bool
	inFlightDone chan struct{}
	nextAt       int64
	dirty        bool
	stopped      bool
}

func hoNewProgress(write func() (int, error), onError func(error)) *hoProgress {
	return &hoProgress{
		write:   write,
		onError: onError,
	}
}

func hoNewProgressWithClock(
	write func() (int, error),
	onError func(error),
	nowMs func() int64,
	afterFunc func(time.Duration, func()) hoTimer,
) *hoProgress {
	return &hoProgress{
		write:     write,
		onError:   onError,
		nowMs:     nowMs,
		afterFunc: afterFunc,
	}
}

func (p *hoProgress) now() int64 {
	if p.nowMs != nil {
		return p.nowMs()
	}
	return time.Now().UnixMilli()
}

func (p *hoProgress) after(d time.Duration, fn func()) hoTimer {
	if p.afterFunc != nil {
		return p.afterFunc(d, fn)
	}
	return time.AfterFunc(d, fn)
}

// Mark schedules a throttled progress commit.
func (p *hoProgress) Mark() {
	p.mu.Lock()
	p.dirty = true
	p.scheduleLocked()
	p.mu.Unlock()
}

// MarkAndWait schedules a progress commit and waits for the commit including this mark.
func (p *hoProgress) MarkAndWait(ctx chord.Context) error {
	if err := hutCheckAborted(ctx); err != nil {
		return err
	}
	ch := make(chan error, 1)
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return hutClosedError()
	}
	p.waiters = append(p.waiters, ch)
	p.dirty = true
	p.scheduleLocked()
	p.mu.Unlock()

	sig := hutAbortSignal(ctx)
	if sig == nil || sig.Done() == nil {
		return <-ch
	}
	select {
	case err := <-ch:
		return err
	case <-sig.Done():
		return hutSignalErr(sig)
	}
}

// Stop stops future scheduling, awaits any in-flight flush, and returns remaining pending waiters.
func (p *hoProgress) Stop() []chan error {
	p.mu.Lock()
	p.stopped = true
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	done := p.inFlightDone
	p.mu.Unlock()

	if done != nil {
		<-done
	}

	p.mu.Lock()
	waiters := p.waiters
	p.waiters = nil
	p.mu.Unlock()
	return waiters
}

func (p *hoProgress) scheduleLocked() {
	if p.stopped || p.timer != nil || p.inFlight {
		return
	}
	wait := p.nextAt - p.now()
	if wait <= 0 {
		p.startFlushLocked()
		return
	}
	p.timer = p.after(time.Duration(wait)*time.Millisecond, func() {
		p.mu.Lock()
		p.timer = nil
		if !p.stopped && !p.inFlight && p.dirty {
			p.startFlushLocked()
		}
		p.mu.Unlock()
	})
}

func (p *hoProgress) startFlushLocked() {
	if p.stopped || !p.dirty || p.inFlight {
		return
	}
	p.dirty = false
	waiters := p.waiters
	p.waiters = nil
	p.inFlight = true
	done := make(chan struct{})
	p.inFlightDone = done
	go p.runFlush(waiters, done)
}

func (p *hoProgress) runFlush(waiters []chan error, done chan struct{}) {
	defer close(done)

	started := p.now()
	writtenBytes, err := p.write()

	var delayMs int64
	if err != nil {
		delayMs = hoMinProgressIntervalMs
	} else {
		byRate := (int64(writtenBytes) * 1000) / hoProgressBytesPerSec
		delayMs = max(hoMinProgressIntervalMs, byRate)
	}

	for _, w := range waiters {
		w <- err
	}
	if err != nil && p.onError != nil {
		p.onError(err)
	}

	p.mu.Lock()
	p.nextAt = started + delayMs
	p.inFlight = false
	p.inFlightDone = nil
	if p.dirty && !p.stopped {
		p.scheduleLocked()
	}
	p.mu.Unlock()
}
