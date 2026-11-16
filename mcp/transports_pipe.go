// Ported from packages/mcp/src/transports/pipe.ts (pi v1.5.2).

package mcp

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

const tpDefaultCloseTimeoutMs int64 = 2_000

// McpPipe represents a running program's stdio streams (e.g. a remote process).
type McpPipe interface {
	Write(data []byte) error
	CloseStdin() error
	// Close kills the program (and any children it owns).
	Close() error
	// Wait blocks until the program exits, regardless of reason, including after Close.
	Wait() error
}

// McpPipeHandlers connects PipeTransport to the program's output.
type McpPipeHandlers struct {
	OnStdout func(bytes []byte)
	OnStderr func(bytes []byte)
}

// PipeTransportOptions configures a PipeTransport.
type PipeTransportOptions struct {
	// Open starts the program and feeds its output to handlers.
	Open            func(ctx context.Context, handlers McpPipeHandlers) (McpPipe, error)
	MaxMessageBytes *int
	MaxStderrBytes  *int
	OnStderr        func(chunk string)
	// CloseTimeoutMs is how long Close waits after CloseStdin before calling pipe.Close. Default 2000.
	CloseTimeoutMs *int64
}

// PipeTransport speaks MCP JSON-RPC over an externally managed McpPipe.
type PipeTransport struct {
	TransportEvents
	mu      sync.Mutex
	writeMu sync.Mutex
	Options PipeTransportOptions
	lines   *JsonRpcLineReader
	pipe    McpPipe
	started bool
	closed  bool
	ended   bool
	doneCh  chan struct{}
}

// NewPipeTransport returns a PipeTransport that has not been started.
func NewPipeTransport(options PipeTransportOptions) *PipeTransport {
	t := &PipeTransport{Options: options}
	t.lines = NewJsonRpcLineReader(JsonRpcLineReaderOptions{
		MaxMessageBytes: options.MaxMessageBytes,
		MaxStderrBytes:  options.MaxStderrBytes,
		OnStderr:        options.OnStderr,
		OnMessage:       func(msg protocol.JsonRpcMessage) { t.emitMessage(msg) },
		OnError:         func(err error) { t.emitError(err) },
	})
	return t
}

// Stderr returns the retained tail of stderr (up to MaxStderrBytes).
func (t *PipeTransport) Stderr() string {
	return t.lines.Stderr()
}

// Start opens the underlying pipe and monitors its lifecycle.
func (t *PipeTransport) Start() error {
	return t.StartContext(context.Background())
}

// StartContext opens the underlying pipe using ctx and monitors its lifecycle.
func (t *PipeTransport) StartContext(ctx context.Context) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return errors.New("MCP pipe transport already started")
	}
	if t.closed {
		t.mu.Unlock()
		return protocol.NewMcpConnectionClosedError()
	}
	t.started = true
	openFn := t.Options.Open
	t.mu.Unlock()

	if openFn == nil {
		return errors.New("MCP pipe transport missing Open handler")
	}
	pipe, err := openFn(ctx, McpPipeHandlers{
		OnStdout: func(b []byte) { t.lines.ReadStdout(b) },
		OnStderr: func(b []byte) { t.lines.ReadStderr(b) },
	})
	if err != nil {
		return err
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		go func() { _ = pipe.Wait() }()
		_ = pipe.Close()
		return protocol.NewMcpConnectionClosedError()
	}
	done := make(chan struct{})
	t.pipe = pipe
	t.doneCh = done
	t.mu.Unlock()

	go func() {
		waitErr := pipe.Wait()
		t.mu.Lock()
		wasClosed := t.closed
		t.ended = true
		t.mu.Unlock()
		if waitErr != nil && !wasClosed {
			t.emitError(waitErr)
		}
		t.lines.End()
		t.emitClose()
		close(done)
	}()
	return nil
}

// Send writes one newline-delimited JSON-RPC message to the pipe.
func (t *PipeTransport) Send(message protocol.JsonRpcMessage) error {
	t.mu.Lock()
	pipe := t.pipe
	closed := t.closed || t.ended
	t.mu.Unlock()
	if pipe == nil || closed {
		return protocol.NewMcpConnectionClosedError()
	}
	raw, err := jsonx.Marshal(message)
	if err != nil {
		return err
	}
	payload := append(raw, '\n')
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	t.mu.Lock()
	stillOpen := t.pipe != nil && !t.closed && !t.ended
	t.mu.Unlock()
	if !stillOpen {
		return protocol.NewMcpConnectionClosedError()
	}
	return pipe.Write(payload)
}

// Close closes stdin cleanly, waits up to CloseTimeoutMs, and kills the pipe if needed.
func (t *PipeTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		done := t.doneCh
		t.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil
	}
	t.closed = true
	pipe := t.pipe
	ended := t.ended
	done := t.doneCh
	timeoutMs := tpDefaultCloseTimeoutMs
	if t.Options.CloseTimeoutMs != nil {
		timeoutMs = *t.Options.CloseTimeoutMs
	}
	t.mu.Unlock()

	if pipe == nil {
		t.emitClose()
		return nil
	}
	if ended {
		if done != nil {
			<-done
		}
		return nil
	}

	_ = pipe.CloseStdin()
	if timeoutMs < 0 {
		timeoutMs = 0
	}
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		_ = pipe.Close()
		<-done
		return nil
	}
}

var _ McpTransport = (*PipeTransport)(nil)
