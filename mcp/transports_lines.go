// Ported from packages/mcp/src/transports/lines.ts (pi v1.5.2).

package mcp

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

const tlDefaultMaxStderrBytes = 64 * 1024

// JsonRpcLineReaderOptions configures a JsonRpcLineReader.
type JsonRpcLineReaderOptions struct {
	MaxMessageBytes *int
	MaxStderrBytes  *int
	// OnStderr receives every stderr chunk decoded as UTF-8.
	OnStderr  func(chunk string)
	OnMessage func(message protocol.JsonRpcMessage)
	OnError   func(err error)
}

// JsonRpcLineReader reads newline-delimited JSON-RPC messages from stdout
// and keeps a bounded tail of stderr for diagnostics.
type JsonRpcLineReader struct {
	mu                 sync.Mutex
	options            JsonRpcLineReaderOptions
	stdoutChunks       [][]byte
	stdoutPendingBytes int
	stderrBuffer       []byte
}

// NewJsonRpcLineReader returns a line reader with the given options.
func NewJsonRpcLineReader(options JsonRpcLineReaderOptions) *JsonRpcLineReader {
	return &JsonRpcLineReader{options: options}
}

// Stderr returns the retained tail of stderr (up to MaxStderrBytes).
func (r *JsonRpcLineReader) Stderr() string {
	r.mu.Lock()
	buf := append([]byte(nil), r.stderrBuffer...)
	r.mu.Unlock()
	return js.DecodeUTF8(buf)
}

func (r *JsonRpcLineReader) maxMessageBytes() int {
	if r.options.MaxMessageBytes != nil {
		return *r.options.MaxMessageBytes
	}
	return DefaultMaxMessageBytes
}

func (r *JsonRpcLineReader) maxStderrBytes() int {
	if r.options.MaxStderrBytes != nil {
		return *r.options.MaxStderrBytes
	}
	return tlDefaultMaxStderrBytes
}

// ReadStdout consumes the next stdout bytes and reports every complete message
// or parse error via Options callbacks.
func (r *JsonRpcLineReader) ReadStdout(chunk []byte) {
	maxBytes := r.maxMessageBytes()
	var events []tlStdoutEvent

	r.mu.Lock()
	start := 0
	for {
		rel := bytes.IndexByte(chunk[start:], '\n')
		if rel < 0 {
			if start < len(chunk) {
				rem := make([]byte, len(chunk)-start)
				copy(rem, chunk[start:])
				r.stdoutChunks = append(r.stdoutChunks, rem)
				r.stdoutPendingBytes += len(rem)
			}
			if r.stdoutPendingBytes > maxBytes {
				r.stdoutChunks = nil
				r.stdoutPendingBytes = 0
				events = append(events, tlStdoutEvent{
					err: fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes),
				})
			}
			break
		}
		end := start + rel
		line := chunk[start:end]
		if len(r.stdoutChunks) > 0 {
			total := r.stdoutPendingBytes + len(line)
			joined := make([]byte, 0, total)
			for _, c := range r.stdoutChunks {
				joined = append(joined, c...)
			}
			joined = append(joined, line...)
			line = joined
			r.stdoutChunks = nil
			r.stdoutPendingBytes = 0
		}
		start = end + 1
		if len(line) > maxBytes {
			events = append(events, tlStdoutEvent{
				err: fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes),
			})
			continue
		}
		text := strings.TrimSuffix(js.DecodeUTF8(line), "\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		events = append(events, tlStdoutEvent{line: text})
	}
	onMsg := r.options.OnMessage
	onErr := r.options.OnError
	r.mu.Unlock()

	for _, ev := range events {
		if ev.err != nil {
			if onErr != nil {
				onErr(ev.err)
			}
			continue
		}
		parsed, err := jsonx.ParseString(ev.line)
		if err != nil {
			if onErr != nil {
				onErr(err)
			}
			continue
		}
		msg, err := protocol.ParseJsonRpcMessage(parsed)
		if err != nil {
			if onErr != nil {
				onErr(err)
			}
			continue
		}
		if onMsg != nil {
			onMsg(msg)
		}
	}
}

type tlStdoutEvent struct {
	line string
	err  error
}

// ReadStderr appends chunk to the retained stderr tail and calls OnStderr.
func (r *JsonRpcLineReader) ReadStderr(chunk []byte) {
	maxBytes := r.maxStderrBytes()

	r.mu.Lock()
	if maxBytes <= 0 {
		r.stderrBuffer = nil
	} else {
		r.stderrBuffer = append(r.stderrBuffer, chunk...)
		if len(r.stderrBuffer) > maxBytes {
			excess := len(r.stderrBuffer) - maxBytes
			copy(r.stderrBuffer, r.stderrBuffer[excess:])
			r.stderrBuffer = r.stderrBuffer[:maxBytes]
		}
	}
	onStderr := r.options.OnStderr
	r.mu.Unlock()

	if onStderr != nil {
		onStderr(js.DecodeUTF8(chunk))
	}
}

// End finalizes stdout; any non-whitespace incomplete trailing message is
// reported as an error and dropped.
func (r *JsonRpcLineReader) End() {
	r.mu.Lock()
	var pending []byte
	if r.stdoutPendingBytes > 0 {
		pending = make([]byte, 0, r.stdoutPendingBytes)
		for _, c := range r.stdoutChunks {
			pending = append(pending, c...)
		}
	}
	r.stdoutChunks = nil
	r.stdoutPendingBytes = 0
	onErr := r.options.OnError
	r.mu.Unlock()

	if len(pending) > 0 && strings.TrimSpace(js.DecodeUTF8(pending)) != "" && onErr != nil {
		onErr(errors.New("MCP stdio server closed with an incomplete JSON-RPC message"))
	}
}
