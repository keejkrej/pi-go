package sse

import (
	"io"
	"strings"
	"unicode/utf8"
)

// StreamOptions are the options of NewReaderWithOptions
// (EventSourceParserStream options).
type StreamOptions struct {
	// OnError is called with every parse error.
	OnError func(err error)
	// Terminate ends the stream with the first parse error (TS onError:
	// "terminate"). Otherwise invalid retry values and unknown fields are
	// ignored. A buffer overflow always ends the stream.
	Terminate bool
	// OnRetry is called with the reconnection time of a valid retry field.
	OnRetry func(retry int64)
	// OnComment is called with the text of comment lines.
	OnComment func(comment string)
	// MaxBufferSize is ParserConfig.MaxBufferSize.
	MaxBufferSize *int
}

// Reader reads events from a byte stream, like an EventSourceParserStream
// fed by a TextDecoderStream: the bytes are decoded as UTF-8 (a leading BOM
// is dropped, invalid sequences become U+FFFD) and parsed incrementally.
// An unterminated event at the end of the stream is dropped. It is not safe
// for concurrent use.
type Reader struct {
	r      io.Reader
	buf    []byte
	dec    utf8Decoder
	parser *Parser
	opts   StreamOptions
	queue  []Event
	head   int
	err    error
}

// NewReader returns a Reader with default options.
func NewReader(r io.Reader) *Reader { return NewReaderWithOptions(r, nil) }

// NewReaderWithOptions returns a Reader; opts may be nil.
func NewReaderWithOptions(r io.Reader, opts *StreamOptions) *Reader {
	rd := &Reader{r: r, buf: make([]byte, 32*1024)}
	if opts != nil {
		rd.opts = *opts
	}
	rd.parser = CreateParser(ParserConfig{
		OnEvent: func(ev Event) {
			// Events after the stream failed are not delivered.
			if rd.err == nil {
				rd.queue = append(rd.queue, ev)
			}
		},
		OnError: func(err *ParseError) {
			if rd.opts.OnError != nil {
				rd.opts.OnError(err)
			}
			// A buffer overflow is fatal: the parser is unusable until reset.
			if (rd.opts.Terminate || err.Type == ErrorTypeMaxBufferSizeExceeded) && rd.err == nil {
				rd.err = err
			}
		},
		OnRetry:       rd.opts.OnRetry,
		OnComment:     rd.opts.OnComment,
		MaxBufferSize: rd.opts.MaxBufferSize,
	})
	return rd
}

// Next returns the next event. At the end of the stream it returns io.EOF;
// a read error or a terminating *ParseError is returned after the events
// parsed before it. Once Next fails it keeps returning the same error.
func (r *Reader) Next() (Event, error) {
	for r.head == len(r.queue) {
		if r.err != nil {
			return Event{}, r.err
		}
		r.queue, r.head = r.queue[:0], 0
		r.fill()
	}
	ev := r.queue[r.head]
	r.queue[r.head] = Event{}
	r.head++
	return ev, nil
}

// fill reads and parses one chunk of input.
func (r *Reader) fill() {
	n, err := r.r.Read(r.buf)
	if n > 0 {
		r.feed(r.dec.decode(r.buf[:n], false))
	}
	if err == nil {
		return
	}
	if err == io.EOF {
		r.feed(r.dec.decode(nil, true))
	}
	if r.err == nil {
		r.err = err
	}
}

func (r *Reader) feed(s string) {
	// TextDecoderStream does not enqueue empty chunks.
	if s == "" || r.err != nil {
		return
	}
	if err := r.parser.Feed(s); err != nil && r.err == nil {
		r.err = err
	}
}

// utf8Decoder is a streaming UTF-8 decoder with the WHATWG Encoding
// Standard's error handling (TextDecoder with fatal false): each maximal
// invalid subsequence becomes one U+FFFD, and a U+FEFF at the very start of
// the stream is dropped.
type utf8Decoder struct {
	cp           rune
	needed, seen int
	lower, upper byte
	started      bool // a code point was decoded (BOM seen)
}

func (d *utf8Decoder) emit(sb *strings.Builder, r rune) {
	if !d.started {
		d.started = true
		if r == 0xFEFF {
			return
		}
	}
	sb.WriteRune(r)
}

// decode decodes p; flush ends the stream (an incomplete sequence becomes
// U+FFFD).
func (d *utf8Decoder) decode(p []byte, flush bool) string {
	var sb strings.Builder
	sb.Grow(len(p))
	for i := 0; i < len(p); {
		b := p[i]
		if d.needed == 0 {
			if b < utf8.RuneSelf {
				if !d.started {
					d.started = true
				}
				// Copy the ASCII run at once.
				j := i + 1
				for j < len(p) && p[j] < utf8.RuneSelf {
					j++
				}
				sb.Write(p[i:j])
				i = j
				continue
			}
			i++
			switch {
			case b >= 0xC2 && b <= 0xDF:
				d.needed, d.cp = 1, rune(b&0x1F)
			case b >= 0xE0 && b <= 0xEF:
				if b == 0xE0 {
					d.lower = 0xA0
				}
				if b == 0xED {
					d.upper = 0x9F
				}
				d.needed, d.cp = 2, rune(b&0xF)
			case b >= 0xF0 && b <= 0xF4:
				if b == 0xF0 {
					d.lower = 0x90
				}
				if b == 0xF4 {
					d.upper = 0x8F
				}
				d.needed, d.cp = 3, rune(b&0x7)
			default:
				d.emit(&sb, utf8.RuneError)
			}
			continue
		}
		lower, upper := d.lower, d.upper
		if lower == 0 {
			lower = 0x80
		}
		if upper == 0 {
			upper = 0xBF
		}
		if b < lower || b > upper {
			// The byte is not consumed: it starts the next sequence.
			d.reset()
			d.emit(&sb, utf8.RuneError)
			continue
		}
		i++
		d.lower, d.upper = 0, 0
		d.cp = d.cp<<6 | rune(b&0x3F)
		d.seen++
		if d.seen == d.needed {
			r := d.cp
			d.reset()
			d.emit(&sb, r)
		}
	}
	if flush && d.needed != 0 {
		d.reset()
		d.emit(&sb, utf8.RuneError)
	}
	return sb.String()
}

func (d *utf8Decoder) reset() {
	d.cp, d.needed, d.seen, d.lower, d.upper = 0, 0, 0, 0, 0
}
