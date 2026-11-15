// Ported from packages/client/test/unix-transport.test.ts (pi v1.0.0).

package client_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
)

func makeShortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pct-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "pi.sock")
}

func startRawUnixListener(t *testing.T, sockPath string, handle func(net.Conn)) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen unix %s failed: %v", sockPath, err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		copied := append([]net.Conn(nil), conns...)
		mu.Unlock()
		for _, conn := range copied {
			_ = conn.Close()
		}
		wg.Wait()
	})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer func() { _ = c.Close() }()
				handle(c)
			}(conn)
		}
	}()
	return ln
}

func TestRejectsInvalidUnixTransportOptions(t *testing.T) {
	if _, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: ""}); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("expected empty path error, got %v", err)
	}
	zero := 0
	if _, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: "/tmp/pi.sock", MaxPendingBytes: &zero}); err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("expected positive maxPendingBytes error, got %v", err)
	}
}

func TestCreateUnixTransportFactoryRealUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix domain socket tests skipped on windows")
	}

	t.Run("carries a complete Client handshake and request over a real Unix socket", func(t *testing.T) {
		sockPath := makeShortSocketPath(t)
		var mu sync.Mutex
		var receivedMembers []string

		startRawUnixListener(t, sockPath, func(conn net.Conn) {
			decoder, err := protocol.NewClientMessageDecoder(nil)
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			for {
				n, err := conn.Read(buf)
				if n > 0 {
					msgs, decErr := decoder.Push(buf[:n])
					if decErr != nil {
						return
					}
					for _, msg := range msgs {
						switch m := msg.(type) {
						case *protocol.ClientHello:
							frame, err := protocol.EncodeServerMessage(&protocol.ServerHello{
								Type:     "hello",
								Version:  protocol.ProtocolVersion,
								ServerId: testServerId,
							}, nil)
							if err != nil {
								return
							}
							for i := 0; i < len(frame); i++ {
								if _, err := conn.Write(frame[i : i+1]); err != nil {
									return
								}
							}
						case *protocol.CancelEnvelope:
							continue
						case *protocol.RequestEnvelope:
							call, err := chord.ParseServiceCall(m.Call)
							if err != nil {
								return
							}
							mu.Lock()
							receivedMembers = append(receivedMembers, fmt.Sprintf("%s.%s", call.ServiceId, call.Member))
							mu.Unlock()
							frame, err := protocol.EncodeServerMessage(&protocol.ResponseSuccess{
								Type:   "response",
								Id:     m.Id,
								Ok:     true,
								Result: jsonx.Some[chord.JsonValue]([]any{}),
							}, nil)
							if err != nil {
								return
							}
							split := len(frame) / 2
							if _, err := conn.Write(frame[:split]); err != nil {
								return
							}
							if _, err := conn.Write(frame[split:]); err != nil {
								return
							}
						}
					}
				}
				if err != nil {
					return
				}
			}
		})

		factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: sockPath})
		if err != nil {
			t.Fatalf("CreateUnixTransportFactory failed: %v", err)
		}
		c, err := client.NewClient(client.ClientOptions{
			ServerId:         testServerId,
			TransportFactory: factory,
		})
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}
		defer func() { _ = c.Dispose() }()

		hello, err := c.Connect()
		if err != nil || hello.ServerId != testServerId {
			t.Fatalf("Connect failed: hello=%+v err=%v", hello, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		val, ok, err := c.Request(ctx, testServerTarget, chord.ServiceCall{
			ServiceId: "test.server",
			Member:    "list",
			Args:      []chord.JsonValue{},
		})
		if err != nil || !ok || !reflect.DeepEqual(val, []any{}) {
			t.Fatalf("Request failed: val=%#v ok=%v err=%v", val, ok, err)
		}
		mu.Lock()
		got := append([]string(nil), receivedMembers...)
		mu.Unlock()
		if !reflect.DeepEqual(got, []string{"test.server.list"}) {
			t.Fatalf("unexpected receivedMembers: %v", got)
		}
	})

	t.Run("reports truncated final frames through Client", func(t *testing.T) {
		sockPath := makeShortSocketPath(t)
		startRawUnixListener(t, sockPath, func(conn net.Conn) {
			decoder, err := protocol.NewClientMessageDecoder(nil)
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			for {
				n, err := conn.Read(buf)
				if n > 0 {
					msgs, decErr := decoder.Push(buf[:n])
					if decErr != nil {
						return
					}
					for _, msg := range msgs {
						if _, isHello := msg.(*protocol.ClientHello); isHello {
							frame, err := protocol.EncodeServerMessage(&protocol.ServerHello{
								Type:     "hello",
								Version:  protocol.ProtocolVersion,
								ServerId: testServerId,
							}, nil)
							if err != nil {
								return
							}
							if _, err := conn.Write(frame); err != nil {
								return
							}
						} else {
							_, _ = conn.Write([]byte{0, 0, 0, 2, 1})
							if uc, ok := conn.(*net.UnixConn); ok {
								_ = uc.CloseWrite()
							}
							return
						}
					}
				}
				if err != nil {
					return
				}
			}
		})

		factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: sockPath})
		if err != nil {
			t.Fatalf("CreateUnixTransportFactory failed: %v", err)
		}
		c, err := client.NewClient(client.ClientOptions{
			ServerId:         testServerId,
			TransportFactory: factory,
		})
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}
		defer func() { _ = c.Dispose() }()

		if _, err := c.Connect(); err != nil {
			t.Fatalf("Connect failed: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		_, _, err = c.Request(ctx, testServerTarget, chord.ServiceCall{
			ServiceId: "test.server",
			Member:    "list",
			Args:      []chord.JsonValue{},
		})
		var pve *protocol.ProtocolValidationError
		if !errors.As(err, &pve) || !regexp.MustCompile(`(?i)truncated`).MatchString(pve.Message) {
			t.Fatalf("expected ProtocolValidationError mentioning truncated, got %T (%v)", err, err)
		}
		if c.ConnectionState() != client.ConnectionStateDisconnected {
			t.Fatalf("expected disconnected state, got %s", c.ConnectionState())
		}
	})

	t.Run("rejects connection attempts to missing sockets", func(t *testing.T) {
		sockPath := makeShortSocketPath(t)
		factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: sockPath})
		if err != nil {
			t.Fatalf("CreateUnixTransportFactory failed: %v", err)
		}
		_, err = factory(client.ByteTransportHandlers{
			OnData:  func([]byte) {},
			OnClose: func() {},
			OnError: func(error) {},
		})
		if !errors.Is(err, syscall.ENOENT) {
			t.Fatalf("expected ENOENT error, got %T (%v)", err, err)
		}
	})
}
