// Package sse parses Server-Sent Events streams with the semantics of the
// npm package eventsource-parser 3.1.1 (createParser and
// EventSourceParserStream behind a TextDecoderStream).
//
// The parser follows the WHATWG event stream grammar: lines end with "\r\n",
// "\r" or "\n"; "data" lines are joined with "\n"; "event" sets the event
// type; "id" sets the event id unless it contains U+0000; "retry" reports a
// reconnection time; lines starting with ':' are comments; a blank line
// dispatches the event if it has at least one data line. Unlike the browser
// EventSource, the event type is not defaulted to "message" and the id is
// not carried over to later events.
package sse

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Event is a parsed event (EventSourceMessage).
type Event struct {
	// ID is the event id, or nil when the event had no id field.
	// An empty id is a non-nil pointer to "".
	ID *string
	// Event is the event type, or "" when none was set. An empty event field
	// also means "no type", so "" never names a type.
	Event string
	// Data is the event data: the data lines joined with "\n".
	Data string
	// Retry is the reconnection time in milliseconds from a retry field in
	// this event's block, or nil when the block had none. A retry field with
	// no data line does not produce an Event; it is reported through
	// ParserConfig.OnRetry only.
	Retry *int64
}

// ParserConfig holds the parser callbacks and options. Nil callbacks are
// no-ops.
type ParserConfig struct {
	// OnEvent is called for each dispatched event.
	OnEvent func(event Event)
	// OnError is called for invalid retry values, unknown fields and buffer
	// overflows. Parsing continues after the first two.
	OnError func(err *ParseError)
	// OnRetry is called with the reconnection time in milliseconds of a
	// valid retry field (values beyond int64 saturate).
	OnRetry func(retry int64)
	// OnComment is called with the text of comment lines (after ':' and one
	// optional space).
	OnComment func(comment string)
	// MaxBufferSize limits, in UTF-16 code units, the unterminated line plus
	// the data of the pending event. When the limit is exceeded the parser
	// reports ErrorTypeMaxBufferSizeExceeded and Feed fails until Reset. Nil
	// means unbounded.
	MaxBufferSize *int
}

// ResetOptions are the options of Parser.Reset.
type ResetOptions struct {
	// Consume parses a buffered unterminated line as if it ended with a
	// newline before resetting.
	Consume bool
}

// errTerminated is returned by Feed after a buffer overflow.
var errTerminated = errors.New("Cannot feed parser: it was terminated after exceeding the configured max buffer size. Call `reset()` to resume parsing.")

// latin1BOM is "ï»¿": eventsource-parser strips these three
// characters (the UTF-8 BOM bytes read as Latin-1) from the start of the
// first chunk. A real U+FEFF BOM is removed earlier by the UTF-8 decoder.
const latin1BOM = "\xc3\xaf\xc2\xbb\xc2\xbf"

// Parser is an incremental event stream parser (createParser). It is not
// safe for concurrent use.
type Parser struct {
	config ParserConfig

	// pending holds the fragments of an unterminated line from earlier Feed
	// calls; pendingLen is their total UTF-16 length.
	pending    []string
	pendingLen int

	isFirstChunk bool
	id           *string
	data         []string // data lines of the pending event
	dataLen      int      // UTF-16 length of the joined data (only with MaxBufferSize)
	eventType    string
	retry        *int64
	terminated   bool
}

// CreateParser returns a new parser. It needs to be reset (or replaced)
// between reconnections.
func CreateParser(config ParserConfig) *Parser {
	return &Parser{config: config, isFirstChunk: true}
}

// Feed parses the next chunk of the stream. Chunks may split lines (and
// "\r\n" pairs) anywhere: an unterminated line is kept for the next call.
// It returns an error only after a buffer overflow, until Reset is called.
func (p *Parser) Feed(chunk string) error {
	if p.terminated {
		return errTerminated
	}
	if p.isFirstChunk {
		p.isFirstChunk = false
		chunk = strings.TrimPrefix(chunk, latin1BOM)
	}
	if len(p.pending) == 0 {
		if trailing := p.processLines(chunk); trailing != "" {
			p.pending = append(p.pending, trailing)
			p.pendingLen = len16(trailing)
		}
		p.checkBufferSize()
		return nil
	}
	// Buffer fragments until a line terminator arrives instead of
	// concatenating on every call.
	if !strings.ContainsAny(chunk, "\r\n") {
		p.pending = append(p.pending, chunk)
		p.pendingLen += len16(chunk)
		p.checkBufferSize()
		return nil
	}
	p.pending = append(p.pending, chunk)
	input := strings.Join(p.pending, "")
	clear(p.pending)
	p.pending = p.pending[:0]
	p.pendingLen = 0
	if trailing := p.processLines(input); trailing != "" {
		p.pending = append(p.pending, trailing)
		p.pendingLen = len16(trailing)
	}
	p.checkBufferSize()
	return nil
}

func (p *Parser) checkBufferSize() {
	limit := p.config.MaxBufferSize
	if limit == nil || p.pendingLen+p.dataLen <= *limit {
		return
	}
	p.terminated = true
	clear(p.pending)
	p.pending = p.pending[:0]
	p.pendingLen = 0
	p.clearEvent()
	p.onError(NewParseError("Buffered data exceeded max buffer size of "+strconv.Itoa(*limit)+" characters",
		ParseErrorOptions{Type: ErrorTypeMaxBufferSizeExceeded}))
}

// processLines parses the complete lines of chunk and returns the trailing
// unterminated part. A '\r' at the very end of the chunk is kept back, since
// it may be the first half of a "\r\n" split across chunks.
func (p *Parser) processLines(chunk string) string {
	i := 0
	for i < len(chunk) {
		n := strings.IndexAny(chunk[i:], "\r\n")
		if n < 0 {
			break
		}
		end := i + n
		if chunk[end] == '\r' && end == len(chunk)-1 {
			break
		}
		p.parseLine(chunk[i:end])
		i = end + 1
		if chunk[end] == '\r' && i < len(chunk) && chunk[i] == '\n' {
			i++
		}
	}
	return chunk[i:]
}

// valueAfter returns line[n:] without one leading space.
func valueAfter(line string, n int) string {
	if n < len(line) && line[n] == ' ' {
		return line[n+1:]
	}
	return line[n:]
}

func (p *Parser) parseLine(line string) {
	if line == "" {
		p.dispatchEvent()
		return
	}
	switch {
	case strings.HasPrefix(line, "data:"):
		p.addData(valueAfter(line, 5))
		return
	case strings.HasPrefix(line, "event:"):
		p.eventType = valueAfter(line, 6)
		return
	case strings.HasPrefix(line, "id:"):
		p.setID(valueAfter(line, 3))
		return
	case line[0] == ':':
		if p.config.OnComment != nil {
			p.config.OnComment(valueAfter(line, 1))
		}
		return
	}
	sep := strings.IndexByte(line, ':')
	if sep < 0 {
		p.processField(line, "", line)
		return
	}
	p.processField(line[:sep], valueAfter(line, sep+1), line)
}

func (p *Parser) addData(value string) {
	if p.config.MaxBufferSize != nil {
		if len(p.data) > 0 {
			p.dataLen++
		}
		p.dataLen += len16(value)
	}
	p.data = append(p.data, value)
}

func (p *Parser) setID(value string) {
	// An id containing U+0000 is ignored.
	if !strings.Contains(value, "\x00") {
		p.id = &value
	}
}

func (p *Parser) processField(field, value, line string) {
	// Field names are compared literally, without case folding.
	switch field {
	case "event":
		p.eventType = value
	case "data":
		p.addData(value)
	case "id":
		p.setID(value)
	case "retry":
		if isASCIIDigits(value) {
			v := parseRetry(value)
			p.retry = &v
			if p.config.OnRetry != nil {
				p.config.OnRetry(v)
			}
			return
		}
		p.onError(NewParseError(`Invalid `+"`retry`"+` value: "`+value+`"`,
			ParseErrorOptions{Type: ErrorTypeInvalidRetry, Value: &value, Line: &line}))
	default:
		shown := field
		if len16(field) > 20 {
			shown = slice16(field, 20) + "…"
		}
		p.onError(NewParseError(`Unknown field "`+shown+`"`,
			ParseErrorOptions{Type: ErrorTypeUnknownField, Field: &field, Value: &value, Line: &line}))
	}
}

func (p *Parser) onError(err *ParseError) {
	if p.config.OnError != nil {
		p.config.OnError(err)
	}
}

func (p *Parser) dispatchEvent() {
	if len(p.data) > 0 && p.config.OnEvent != nil {
		data := p.data[0]
		if len(p.data) > 1 {
			data = strings.Join(p.data, "\n")
		}
		p.config.OnEvent(Event{ID: p.id, Event: p.eventType, Data: data, Retry: p.retry})
	}
	p.clearEvent()
}

// Finish ends a stream. A trailing unterminated line is parsed as if it
// ended at EOF, and a pending event that has data but no blank line is
// dispatched. eventsource-parser's TransformStream drops that tail; the
// decoders pi actually uses (anthropic iterateSseMessages and MCP
// consumeSseStream) deliver it. An event type with no data line is still
// not dispatched.
func (p *Parser) Finish() {
	if p.terminated {
		return
	}
	if len(p.pending) > 0 {
		input := strings.Join(p.pending, "")
		clear(p.pending)
		p.pending = p.pending[:0]
		p.pendingLen = 0
		// A CR held back at the end of the last chunk is a terminator once
		// no more bytes will arrive. Appending LF makes a bare CR a CRLF
		// and a line with no terminator a complete line.
		p.processLines(input + "\n")
	}
	if len(p.data) > 0 {
		p.dispatchEvent()
	}
}

func (p *Parser) clearEvent() {
	p.id = nil
	clear(p.data)
	p.data = p.data[:0]
	p.dataLen = 0
	p.eventType = ""
	p.retry = nil
}

// Reset clears the parser state, for a new stream (after a reconnection,
// for example). A buffered unterminated line is dropped unless
// opts.Consume is set, in which case it is parsed first. Data of an event
// that was not dispatched yet is always dropped.
func (p *Parser) Reset(opts *ResetOptions) {
	if opts != nil && opts.Consume && len(p.pending) > 0 {
		p.parseLine(strings.Join(p.pending, ""))
	}
	p.isFirstChunk = true
	p.clearEvent()
	clear(p.pending)
	p.pending = p.pending[:0]
	p.pendingLen = 0
	p.terminated = false
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseRetry converts a digit string like parseInt(value, 10): to the
// nearest float64, then to int64, saturating.
func parseRetry(digits string) int64 {
	f, _ := strconv.ParseFloat(digits, 64)
	if f >= math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(f)
}

// len16 returns the length of s in UTF-16 code units (JS string length).
func len16(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			n++
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}

// slice16 returns the first n UTF-16 code units of s (s.slice(0, n)). A
// surrogate pair cut in half leaves U+FFFD.
func slice16(s string, n int) string {
	units := 0
	for i := 0; i < len(s); {
		if units >= n {
			return s[:i]
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r > 0xFFFF {
			if units+2 > n {
				return s[:i] + string(utf8.RuneError)
			}
			units += 2
		} else {
			units++
		}
		i += size
	}
	return s
}
