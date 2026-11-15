// Ported from packages/client/src/unix.ts (pi v1.0.0).

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	sysunix "golang.org/x/sys/unix"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/protocol"
)

const (
	unixDefaultDiscoveryTimeoutMs    = 1_000
	unixMaxTimerDelayMs              = 2_147_483_647
	unixSocketSuffix                 = ".sock"
	unixMaxConcurrentDiscoveryProbes = 16
	unixReadBufferSize               = 32 * 1024
)

var unixErrTransportClosed = errors.New("Unix transport is closed")

// UnixTransportOptions configures client connections to a local Unix socket server.
type UnixTransportOptions struct {
	Path            string `json:"path"`
	MaxPendingBytes *int   `json:"maxPendingBytes,omitzero"`
}

// UnixServerRoute describes one live server discovered on a Unix domain socket.
type UnixServerRoute struct {
	ServerId protocol.ServerId `json:"serverId"`
	Path     string            `json:"path"`
}

// DiscoverUnixServersOptions configures Unix server socket discovery.
type DiscoverUnixServersOptions struct {
	// Directory containing server-addressed Unix sockets.
	Directory string `json:"directory"`
	// TimeoutMs is the maximum time for each connection and handshake. Defaults to 1,000 ms.
	TimeoutMs *int `json:"timeoutMs,omitzero"`
}

type unixDiscoveryTimeoutError struct{}

func (e *unixDiscoveryTimeoutError) Error() string { return "Unix discovery timed out" }

// DiscoverUnixServers discovers reachable local servers by probing server-addressed Unix sockets.
func DiscoverUnixServers(options DiscoverUnixServersOptions) ([]UnixServerRoute, error) {
	if runtime.GOOS == "windows" {
		return nil, errors.New("Unix transport is not supported on Windows")
	}
	timeoutMs := unixDefaultDiscoveryTimeoutMs
	if options.TimeoutMs != nil {
		timeoutMs = *options.TimeoutMs
	}
	if timeoutMs <= 0 || timeoutMs > unixMaxTimerDelayMs {
		return nil, js.NewTypeError(fmt.Sprintf("Unix discovery timeoutMs must be an integer between 1 and %d", unixMaxTimerDelayMs))
	}

	entries, err := os.ReadDir(options.Directory)
	if err != nil {
		if errors.Is(err, sysunix.ENOENT) || errors.Is(err, os.ErrNotExist) {
			return []UnixServerRoute{}, nil
		}
		return nil, err
	}

	candidates := make([]UnixServerRoute, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, unixSocketSuffix) {
			continue
		}
		serverId := strings.TrimSuffix(name, unixSocketSuffix)
		if !protocol.IsServerId(serverId) {
			continue
		}
		candidates = append(candidates, UnixServerRoute{
			ServerId: protocol.ServerId(serverId),
			Path:     filepath.Join(options.Directory, name),
		})
	}

	if len(candidates) == 0 {
		return []UnixServerRoute{}, nil
	}

	workerCount := len(candidates)
	if workerCount > unixMaxConcurrentDiscoveryProbes {
		workerCount = unixMaxConcurrentDiscoveryProbes
	}

	var (
		mu        sync.Mutex
		nextIndex int
		failure   error
		routes    []UnixServerRoute
		wg        sync.WaitGroup
	)

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if failure != nil || nextIndex >= len(candidates) {
					mu.Unlock()
					return
				}
				candidate := candidates[nextIndex]
				nextIndex++
				mu.Unlock()

				var stat sysunix.Stat_t
				if err := sysunix.Lstat(candidate.Path, &stat); err != nil {
					// A socket can disappear between ReadDir and Lstat during normal server shutdown.
					if errors.Is(err, sysunix.ENOENT) || errors.Is(err, os.ErrNotExist) {
						continue
					}
					mu.Lock()
					if failure == nil {
						failure = err
					}
					mu.Unlock()
					return
				}
				if (stat.Mode & sysunix.S_IFMT) != sysunix.S_IFSOCK {
					continue
				}

				route, ok, err := unixProbeServer(candidate, timeoutMs)
				if err != nil {
					mu.Lock()
					if failure == nil {
						failure = err
					}
					mu.Unlock()
					return
				}
				if ok {
					mu.Lock()
					routes = append(routes, route)
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()
	if failure != nil {
		return nil, failure
	}

	if routes == nil {
		routes = []UnixServerRoute{}
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].ServerId < routes[j].ServerId
	})
	return routes, nil
}

// CreateUnixTransportFactory creates fresh Unix-domain socket transports for Client connection attempts.
func CreateUnixTransportFactory(options UnixTransportOptions) (ByteTransportFactory, error) {
	maxPendingBytes, err := unixValidateTransportOptions(options)
	if err != nil {
		return nil, err
	}
	return func(handlers ByteTransportHandlers) (ByteTransport, error) {
		transport, _, err := unixConnectSocket(context.Background(), options.Path, maxPendingBytes, handlers)
		return transport, err
	}, nil
}

func unixValidateTransportOptions(options UnixTransportOptions) (int, error) {
	if len(options.Path) == 0 {
		return 0, js.NewTypeError("Unix transport path must not be empty")
	}
	maxPendingBytes := protocol.DefaultMaxFrameLength * 4
	if options.MaxPendingBytes != nil {
		maxPendingBytes = *options.MaxPendingBytes
	}
	if maxPendingBytes <= 0 {
		return 0, js.NewTypeError("Unix transport maxPendingBytes must be a positive safe integer")
	}
	if runtime.GOOS == "windows" {
		return 0, errors.New("Unix transport is not supported on Windows")
	}
	return maxPendingBytes, nil
}

func unixConnectSocket(
	ctx context.Context,
	socketPath string,
	maxPendingBytes int,
	handlers ByteTransportHandlers,
) (*unixByteTransport, net.Conn, error) {
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, nil, err
	}
	transport := unixNewByteTransport(conn, maxPendingBytes, handlers)
	return transport, conn, nil
}

type unixWriteReq struct {
	data []byte
	done chan error
}

type unixByteTransport struct {
	conn            net.Conn
	maxPendingBytes int
	handlers        ByteTransportHandlers

	mu           sync.Mutex
	closed       bool
	terminal     bool
	pendingBytes int
	queue        []*unixWriteReq
	notify       chan struct{}
	wg           sync.WaitGroup
}

func unixNewByteTransport(conn net.Conn, maxPendingBytes int, handlers ByteTransportHandlers) *unixByteTransport {
	t := &unixByteTransport{
		conn:            conn,
		maxPendingBytes: maxPendingBytes,
		handlers:        handlers,
		notify:          make(chan struct{}, 1),
	}
	t.wg.Add(2)
	go t.readLoop()
	go t.writeLoop()
	return t
}

func (t *unixByteTransport) Send(chunk []byte) error {
	if chunk == nil {
		return js.NewTypeError("Unix transport chunks must be byte slices")
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return unixErrTransportClosed
	}
	if t.pendingBytes+len(chunk) > t.maxPendingBytes {
		t.mu.Unlock()
		return errors.New("Unix transport exceeded its pending byte limit")
	}
	copied := slices.Clone(chunk)
	t.pendingBytes += len(copied)
	req := &unixWriteReq{
		data: copied,
		done: make(chan error, 1),
	}
	t.queue = append(t.queue, req)
	t.mu.Unlock()
	select {
	case t.notify <- struct{}{}:
	default:
	}

	return <-req.done
}

func (t *unixByteTransport) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.terminal = true
	t.mu.Unlock()
	select {
	case t.notify <- struct{}{}:
	default:
	}
	_ = t.conn.Close()
}

func (t *unixByteTransport) waitClosed() {
	t.wg.Wait()
}

func (t *unixByteTransport) readLoop() {
	defer t.wg.Done()
	buf := make([]byte, unixReadBufferSize)
	for {
		n, err := t.conn.Read(buf)
		if n > 0 {
			chunk := slices.Clone(buf[:n])
			t.mu.Lock()
			terminal := t.terminal
			t.mu.Unlock()
			if !terminal && t.handlers.OnData != nil {
				t.handlers.OnData(chunk)
			}
		}
		if err != nil {
			t.mu.Lock()
			if t.terminal {
				t.mu.Unlock()
				return
			}
			t.terminal = true
			t.closed = true
			t.mu.Unlock()
			select {
			case t.notify <- struct{}{}:
			default:
			}
			_ = t.conn.Close()

			if errors.Is(err, io.EOF) {
				if t.handlers.OnClose != nil {
					t.handlers.OnClose()
				}
			} else {
				if t.handlers.OnError != nil {
					t.handlers.OnError(err)
				}
			}
			return
		}
	}
}

func (t *unixByteTransport) writeLoop() {
	defer t.wg.Done()
	for {
		t.mu.Lock()
		for len(t.queue) == 0 && !t.closed {
			t.mu.Unlock()
			<-t.notify
			t.mu.Lock()
		}
		if t.closed {
			remaining := t.queue
			t.queue = nil
			t.pendingBytes = 0
			t.mu.Unlock()
			for _, req := range remaining {
				req.done <- unixErrTransportClosed
			}
			return
		}
		req := t.queue[0]
		t.queue[0] = nil
		t.queue = t.queue[1:]
		if len(t.queue) == 0 {
			t.queue = nil
		}
		t.mu.Unlock()

		writeErr := unixWriteAll(t.conn, req.data)

		t.mu.Lock()
		t.pendingBytes -= len(req.data)
		t.mu.Unlock()

		req.done <- writeErr
	}
}

func unixWriteAll(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func unixProbeServer(route UnixServerRoute, timeoutMs int) (UnixServerRoute, bool, error) {
	maxPendingBytes, err := unixValidateTransportOptions(UnixTransportOptions{Path: route.Path})
	if err != nil {
		return UnixServerRoute{}, false, err
	}

	probeCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	var (
		transportMu sync.Mutex
		transport   *unixByteTransport
		conn        net.Conn
		timedOut    bool
	)

	stopCancel := context.AfterFunc(probeCtx, func() {
		transportMu.Lock()
		timedOut = true
		c := conn
		transportMu.Unlock()
		if c != nil {
			_ = c.Close()
		}
	})
	defer stopCancel()

	c, err := NewClient(ClientOptions{
		ServerId: route.ServerId,
		TransportFactory: func(handlers ByteTransportHandlers) (ByteTransport, error) {
			dialer := &net.Dialer{}
			rawConn, dialErr := dialer.DialContext(probeCtx, "unix", route.Path)
			if dialErr != nil {
				return nil, dialErr
			}
			transportMu.Lock()
			alreadyTimedOut := timedOut
			if alreadyTimedOut {
				transportMu.Unlock()
				_ = rawConn.Close()
				return nil, context.DeadlineExceeded
			}
			t := unixNewByteTransport(rawConn, maxPendingBytes, handlers)
			transport = t
			conn = rawConn
			transportMu.Unlock()
			return t, nil
		},
	})
	if err != nil {
		return UnixServerRoute{}, false, err
	}

	type connectResult struct {
		err error
	}
	resCh := make(chan connectResult, 1)
	go func() {
		_, connectErr := c.Connect()
		resCh <- connectResult{err: connectErr}
	}()

	var connectErr error
	select {
	case res := <-resCh:
		connectErr = res.err
	case <-probeCtx.Done():
		connectErr = &unixDiscoveryTimeoutError{}
		_ = c.Dispose()
		<-resCh
	}

	_ = c.Dispose()
	transportMu.Lock()
	activeTransport := transport
	activeConn := conn
	transportMu.Unlock()
	if activeConn != nil {
		_ = activeConn.Close()
	}
	if activeTransport != nil {
		activeTransport.Close()
		activeTransport.waitClosed()
	}

	if connectErr == nil {
		return route, true, nil
	}

	// Missing/refused sockets are stale or shutting down. Protocol failures mean
	// the endpoint is not the advertised server. Both are safe to omit.
	var (
		timeoutErr    *unixDiscoveryTimeoutError
		validationErr *protocol.ProtocolValidationError
		disconnected  *DisconnectedError
		serverErr     *ServerError
	)
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) ||
		errors.As(connectErr, &timeoutErr) ||
		errors.As(connectErr, &validationErr) ||
		(errors.As(connectErr, &disconnected) && disconnected.Cause == nil) ||
		(errors.As(connectErr, &serverErr) && serverErr.Code == "version") ||
		errors.Is(connectErr, unixErrTransportClosed) ||
		errors.Is(connectErr, io.EOF) ||
		errors.Is(connectErr, net.ErrClosed) ||
		errors.Is(connectErr, sysunix.ENOENT) ||
		errors.Is(connectErr, sysunix.ECONNREFUSED) ||
		errors.Is(connectErr, sysunix.ECONNRESET) ||
		errors.Is(connectErr, sysunix.EPIPE) ||
		errors.Is(connectErr, sysunix.ETIMEDOUT) ||
		errors.Is(connectErr, os.ErrNotExist) ||
		errors.Is(connectErr, context.DeadlineExceeded) {
		return UnixServerRoute{}, false, nil
	}
	return UnixServerRoute{}, false, connectErr
}
