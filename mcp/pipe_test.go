// Ported from packages/mcp/test/pipe.test.ts (pi v1.5.2).

package mcp

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

type tpipeFakePipe struct {
	mu      sync.Mutex
	calls   []string
	written []string
	exitCh  chan error
	once    sync.Once
}

func (f *tpipeFakePipe) Write(data []byte) error {
	f.mu.Lock()
	f.written = append(f.written, string(data))
	f.mu.Unlock()
	return nil
}

func (f *tpipeFakePipe) CloseStdin() error {
	f.mu.Lock()
	f.calls = append(f.calls, "closeStdin")
	f.mu.Unlock()
	return nil
}

func (f *tpipeFakePipe) Close() error {
	f.mu.Lock()
	f.calls = append(f.calls, "close")
	f.mu.Unlock()
	f.once.Do(func() {
		f.exitCh <- errors.New("aborted")
		close(f.exitCh)
	})
	return nil
}

func (f *tpipeFakePipe) Wait() error {
	return <-f.exitCh
}

func (f *tpipeFakePipe) finish(err error) {
	f.once.Do(func() {
		f.exitCh <- err
		close(f.exitCh)
	})
}

func (f *tpipeFakePipe) snapshotCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *tpipeFakePipe) snapshotWritten() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.written))
	copy(out, f.written)
	return out
}

type tpipeFakeProgram struct {
	pipe      *tpipeFakePipe
	transport *PipeTransport
	handlers  McpPipeHandlers
	mu        sync.Mutex
	messages  []protocol.JsonRpcMessage
	errors    []error
	closed    bool
	closedCh  chan struct{}
}

func tpipeStartFake(t *testing.T, opts PipeTransportOptions) *tpipeFakeProgram {
	t.Helper()
	fake := &tpipeFakePipe{exitCh: make(chan error, 1)}
	prog := &tpipeFakeProgram{
		pipe:     fake,
		closedCh: make(chan struct{}),
	}
	opts.Open = func(_ context.Context, h McpPipeHandlers) (McpPipe, error) {
		prog.handlers = h
		return fake, nil
	}
	tr := NewPipeTransport(opts)
	prog.transport = tr
	tr.OnMessage(func(m protocol.JsonRpcMessage) {
		prog.mu.Lock()
		prog.messages = append(prog.messages, m)
		prog.mu.Unlock()
	})
	tr.OnError(func(err error) {
		prog.mu.Lock()
		prog.errors = append(prog.errors, err)
		prog.mu.Unlock()
	})
	tr.OnClose(func() {
		prog.mu.Lock()
		if !prog.closed {
			prog.closed = true
			close(prog.closedCh)
		}
		prog.mu.Unlock()
	})
	if err := tr.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	return prog
}

func (p *tpipeFakeProgram) pingMsg(id int64) protocol.JsonRpcMessage {
	return &protocol.JsonRpcRequest{
		Jsonrpc: "2.0",
		Id:      protocol.JsonRpcId{Number: float64(id)},
		Method:  "ping",
	}
}

func (p *tpipeFakeProgram) msgJSON(t *testing.T, id int64) string {
	t.Helper()
	b, err := jsonx.Marshal(p.pingMsg(id))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPipe_Framing(t *testing.T) {
	t.Run("joins split messages and splits multi-message chunks", func(t *testing.T) {
		p := tpipeStartFake(t, PipeTransportOptions{})
		m1, m2, m3 := p.msgJSON(t, 1), p.msgJSON(t, 2), p.msgJSON(t, 3)
		p.handlers.OnStdout([]byte(m1[:10]))
		p.handlers.OnStdout([]byte(m1[10:] + "\n" + m2))
		p.handlers.OnStdout([]byte("\n" + m3 + "\n"))

		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.messages) != 3 || len(p.errors) != 0 {
			t.Fatalf("messages=%d errors=%v", len(p.messages), p.errors)
		}
		for i, want := range []float64{1, 2, 3} {
			req := p.messages[i].(*protocol.JsonRpcRequest)
			if req.Id.Number != want {
				t.Fatalf("msg %d id = %v, want %v", i, req.Id.Number, want)
			}
		}
	})

	t.Run("strips CR and skips blank lines", func(t *testing.T) {
		p := tpipeStartFake(t, PipeTransportOptions{})
		p.handlers.OnStdout([]byte("\n\r\n   \n" + p.msgJSON(t, 1) + "\r\n\n"))
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.messages) != 1 || len(p.errors) != 0 {
			t.Fatalf("messages=%d errors=%v", len(p.messages), p.errors)
		}
	})

	t.Run("reports invalid messages and keeps reading", func(t *testing.T) {
		p := tpipeStartFake(t, PipeTransportOptions{})
		p.handlers.OnStdout([]byte("not json\n{\"jsonrpc\":\"1.0\"}\n" + p.msgJSON(t, 1) + "\n"))
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.messages) != 1 || len(p.errors) != 2 {
			t.Fatalf("messages=%d errors=%v", len(p.messages), p.errors)
		}
	})

	t.Run("drops messages over maxMessageBytes", func(t *testing.T) {
		maxBytes := 48
		p := tpipeStartFake(t, PipeTransportOptions{MaxMessageBytes: &maxBytes})
		longLine, _ := jsonx.Marshal(jsonx.ObjectOf("jsonrpc", "2.0", "method", strings.Repeat("x", 48)))
		p.handlers.OnStdout(append(longLine, []byte("\n"+p.msgJSON(t, 1)+"\n")...))
		p.handlers.OnStdout([]byte(strings.Repeat("y", 49)))
		p.handlers.OnStdout([]byte("\n" + p.msgJSON(t, 2) + "\n"))

		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.messages) != 2 || len(p.errors) != 2 {
			t.Fatalf("messages=%d errors=%v", len(p.messages), p.errors)
		}
		for i, e := range p.errors {
			if e.Error() != "MCP stdio message exceeds 48 bytes" {
				t.Fatalf("err[%d] = %q", i, e.Error())
			}
		}
	})

	t.Run("keeps bounded stderr tail and calls OnStderr", func(t *testing.T) {
		maxStderr := 8
		var chunks []string
		p := tpipeStartFake(t, PipeTransportOptions{
			MaxStderrBytes: &maxStderr,
			OnStderr:       func(c string) { chunks = append(chunks, c) },
		})
		p.handlers.OnStderr([]byte("starting\n"))
		p.handlers.OnStderr([]byte("ready\n"))
		if len(chunks) != 2 || chunks[0] != "starting\n" || chunks[1] != "ready\n" {
			t.Fatalf("chunks = %v", chunks)
		}
		if got := p.transport.Stderr(); got != "g\nready\n" {
			t.Fatalf("stderr = %q, want %q", got, "g\nready\n")
		}
	})
}

func TestPipe_Lifecycle(t *testing.T) {
	t.Run("writes each message as one line", func(t *testing.T) {
		p := tpipeStartFake(t, PipeTransportOptions{})
		if err := p.transport.Send(p.pingMsg(1)); err != nil {
			t.Fatal(err)
		}
		written := p.pipe.snapshotWritten()
		if len(written) != 1 || written[0] != p.msgJSON(t, 1)+"\n" {
			t.Fatalf("written = %v", written)
		}
	})

	t.Run("reports incomplete message on exit then closes", func(t *testing.T) {
		p := tpipeStartFake(t, PipeTransportOptions{})
		p.handlers.OnStdout([]byte(`{"jsonrpc":"2.0"`))
		p.pipe.finish(nil)
		<-p.closedCh
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.errors) != 1 || p.errors[0].Error() != "MCP stdio server closed with an incomplete JSON-RPC message" {
			t.Fatalf("errors = %v", p.errors)
		}
	})

	t.Run("reports failed exit it did not cause then closes", func(t *testing.T) {
		p := tpipeStartFake(t, PipeTransportOptions{})
		p.pipe.finish(errors.New("connection lost"))
		<-p.closedCh
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.errors) != 1 || p.errors[0].Error() != "connection lost" {
			t.Fatalf("errors = %v", p.errors)
		}
	})

	t.Run("rejects send after close and after program ended", func(t *testing.T) {
		closedByUs := tpipeStartFake(t, PipeTransportOptions{})
		closedByUs.pipe.finish(nil)
		_ = closedByUs.transport.Close()
		var ce *protocol.McpConnectionClosedError
		if err := closedByUs.transport.Send(closedByUs.pingMsg(1)); !errors.As(err, &ce) {
			t.Fatalf("expected McpConnectionClosedError, got %v", err)
		}

		ended := tpipeStartFake(t, PipeTransportOptions{})
		ended.pipe.finish(nil)
		<-ended.closedCh
		if err := ended.transport.Send(ended.pingMsg(1)); !errors.As(err, &ce) {
			t.Fatalf("expected McpConnectionClosedError, got %v", err)
		}
		if len(ended.pipe.snapshotWritten()) != 0 {
			t.Fatal("expected nothing written after exit")
		}
	})

	t.Run("closes stdin and does not kill program that exits in time", func(t *testing.T) {
		p := tpipeStartFake(t, PipeTransportOptions{})
		done := make(chan error, 1)
		go func() {
			done <- p.transport.Close()
		}()
		deadline := time.Now().Add(time.Second)
		for len(p.pipe.snapshotCalls()) == 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		var ce *protocol.McpConnectionClosedError
		if err := p.transport.Send(p.pingMsg(1)); !errors.As(err, &ce) {
			t.Fatalf("expected McpConnectionClosedError while closing, got %v", err)
		}
		p.pipe.finish(nil)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		calls := p.pipe.snapshotCalls()
		if len(calls) != 1 || calls[0] != "closeStdin" {
			t.Fatalf("calls = %v", calls)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if !p.closed || len(p.errors) != 0 {
			t.Fatalf("closed=%v errors=%v", p.closed, p.errors)
		}
	})

	t.Run("shuts down once when closed twice", func(t *testing.T) {
		p := tpipeStartFake(t, PipeTransportOptions{})
		c1 := make(chan error, 1)
		c2 := make(chan error, 1)
		go func() { c1 <- p.transport.Close() }()
		go func() { c2 <- p.transport.Close() }()
		time.Sleep(20 * time.Millisecond)
		p.pipe.finish(nil)
		<-c1
		<-c2
		_ = p.transport.Close()
		calls := p.pipe.snapshotCalls()
		if len(calls) != 1 || calls[0] != "closeStdin" {
			t.Fatalf("calls = %v", calls)
		}
	})

	t.Run("kills program only after closeTimeoutMs", func(t *testing.T) {
		timeoutMs := int64(80)
		p := tpipeStartFake(t, PipeTransportOptions{CloseTimeoutMs: &timeoutMs})
		start := time.Now()
		if err := p.transport.Close(); err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		if elapsed < 50*time.Millisecond {
			t.Fatalf("closed too early (%v), expected >= ~80ms timeout", elapsed)
		}
		calls := p.pipe.snapshotCalls()
		if len(calls) != 2 || calls[0] != "closeStdin" || calls[1] != "close" {
			t.Fatalf("calls = %v", calls)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if !p.closed || len(p.errors) != 0 {
			t.Fatalf("closed=%v errors=%v", p.closed, p.errors)
		}
	})

	t.Run("fails Start when Open fails", func(t *testing.T) {
		tr := NewPipeTransport(PipeTransportOptions{
			Open: func(context.Context, McpPipeHandlers) (McpPipe, error) {
				return nil, errors.New("not_supported")
			},
		})
		if err := tr.Start(); err == nil || !strings.Contains(err.Error(), "not_supported") {
			t.Fatalf("expected not_supported error, got %v", err)
		}
	})

	t.Run("kills program that finishes starting after Close", func(t *testing.T) {
		openReady := make(chan struct{})
		closeStarted := make(chan struct{})
		fake := &tpipeFakePipe{exitCh: make(chan error, 1)}
		tr := NewPipeTransport(PipeTransportOptions{
			Open: func(context.Context, McpPipeHandlers) (McpPipe, error) {
				close(closeStarted)
				<-openReady
				return fake, nil
			},
		})
		startErr := make(chan error, 1)
		go func() {
			startErr <- tr.Start()
		}()
		<-closeStarted
		if err := tr.Close(); err != nil {
			t.Fatal(err)
		}
		close(openReady)
		var ce *protocol.McpConnectionClosedError
		if err := <-startErr; !errors.As(err, &ce) {
			t.Fatalf("expected McpConnectionClosedError, got %v", err)
		}
		calls := fake.snapshotCalls()
		if len(calls) != 1 || calls[0] != "close" {
			t.Fatalf("calls = %v, want [close]", calls)
		}
	})
}

func tpipeSpawnSelfFixture(ctx context.Context, mode string, handlers McpPipeHandlers) (McpPipe, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), "PI_MCP_STDIO_FIXTURE="+mode)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var ioWg sync.WaitGroup
	ioWg.Add(2)
	go func() {
		defer ioWg.Done()
		buf := make([]byte, 4096)
		for {
			n, rerr := stdoutPipe.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				handlers.OnStdout(cp)
			}
			if rerr != nil {
				return
			}
		}
	}()
	go func() {
		defer ioWg.Done()
		buf := make([]byte, 4096)
		for {
			n, rerr := stderrPipe.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				handlers.OnStderr(cp)
			}
			if rerr != nil {
				return
			}
		}
	}()

	waitOnce := sync.OnceValue(func() error {
		ioWg.Wait()
		return cmd.Wait()
	})

	return &tpipeOsPipe{
		cmd:   cmd,
		stdin: stdinPipe,
		wait:  waitOnce,
	}, nil
}

type tpipeOsPipe struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	wait  func() error
}

func (p *tpipeOsPipe) Write(data []byte) error {
	_, err := p.stdin.Write(data)
	return err
}

func (p *tpipeOsPipe) CloseStdin() error {
	return p.stdin.Close()
}

func (p *tpipeOsPipe) Close() error {
	if p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}

func (p *tpipeOsPipe) Wait() error {
	return p.wait()
}

func TestPipe_WithRealProgram(t *testing.T) {
	tr := NewPipeTransport(PipeTransportOptions{
		Open: func(ctx context.Context, handlers McpPipeHandlers) (McpPipe, error) {
			return tpipeSpawnSelfFixture(ctx, "stdio", handlers)
		},
	})
	client := NewMcpClient(&McpClientOptions{
		Implementation: protocol.Implementation{Name: "pipe-test", Version: "1.0.0"},
	})
	res, err := client.Connect(tr)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if res.ServerInfo.Name != "stdio-fixture" || res.ServerInfo.Version != "1.0.0" {
		t.Fatalf("ServerInfo = %#v", res.ServerInfo)
	}
	tools, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}
	callRes, err := client.CallTool(t.Context(), "echo", jsonx.ObjectOf("text", "hello"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(callRes.Content) != 1 || callRes.Content[0].(*protocol.TextContent).Text != "hello" {
		t.Fatalf("callRes = %#v", callRes)
	}
	if !strings.Contains(tr.Stderr(), "stdio fixture ready") {
		t.Fatalf("stderr = %q", tr.Stderr())
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if client.ConnectionState() != ClientStateClosed {
		t.Fatalf("state = %q", client.ConnectionState())
	}
}
