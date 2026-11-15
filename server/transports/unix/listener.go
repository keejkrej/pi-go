// Ported from packages/server/src/transports/unix/listener.ts (pi v1.0.0).

package unix

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	sysunix "golang.org/x/sys/unix"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server"
)

const (
	listDefaultSocketMode             = 0o600
	listDefaultGracefulCloseTimeoutMs = 5_000
	listMaxUint32                     = 0xffff_ffff
	listMaxTimerDelayMs               = 2_147_483_647
	listSocketProbeTimeoutMs          = 1_000
	listReadBufferSize                = 32 * 1024
)

// UnixSocket is the socket abstraction wrapped by UnixByteConnection.
// *net.UnixConn satisfies this interface directly.
type UnixSocket interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	CloseWrite() error
	Close() error
}

type listResolvedOptions struct {
	path                   string
	mode                   int
	gracefulCloseTimeoutMs int
	maxPendingBytes        int
	onError                func(err error)
}

type listFileIdentity struct {
	dev uint64
	ino uint64
}

type listUnixListener struct {
	mu             sync.Mutex
	options        listResolvedOptions
	path           string
	mode           int
	connections    map[*UnixByteConnection]struct{}
	listener       net.Listener
	socketIdentity *listFileIdentity
	ownedBindPath  string
	closing        bool
	closeStarted   bool
	closeDone      chan struct{}
	closeErr       error
	accept         server.ByteConnectionAcceptor
	wg             sync.WaitGroup
}

// CreateUnixListener builds a ServerListener binding a Unix-domain socket at options.Path.
func CreateUnixListener(options UnixListenerOptions) (server.ServerListener, error) {
	resolved, err := listResolveOptions(options)
	if err != nil {
		return nil, err
	}
	return &listUnixListener{
		options:     resolved,
		path:        resolved.path,
		mode:        resolved.mode,
		connections: make(map[*UnixByteConnection]struct{}),
	}, nil
}

func (l *listUnixListener) Start(accept server.ByteConnectionAcceptor) error {
	l.mu.Lock()
	if l.listener != nil {
		l.mu.Unlock()
		return errors.New("Unix listener is already started")
	}
	if l.closing {
		l.mu.Unlock()
		return errors.New("Unix listener is closing or closed")
	}
	l.accept = accept
	l.mu.Unlock()

	ownedBindPath := listGetOwnedBindPath(l.path)
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	if err := listRemoveStaleSocket(l.path); err != nil {
		return err
	}
	if err := listRemoveStaleSocket(ownedBindPath); err != nil {
		return err
	}

	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		return errors.New("Unix listener is closing or closed")
	}
	l.ownedBindPath = ownedBindPath
	l.mu.Unlock()

	netListener, err := net.Listen("unix", ownedBindPath)
	if err != nil {
		l.mu.Lock()
		ownedBind := l.ownedBindPath
		l.ownedBindPath = ""
		l.mu.Unlock()
		if ownedBind != "" {
			_ = listRemovePath(ownedBind)
		}
		return err
	}
	if ul, ok := netListener.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}

	l.mu.Lock()
	l.listener = netListener
	l.mu.Unlock()

	cleanupFailure := func(cause error) error {
		_ = l.closeListenerAndCleanup(netListener)
		l.mu.Lock()
		l.listener = nil
		l.mu.Unlock()
		return cause
	}

	var stat sysunix.Stat_t
	if err := sysunix.Lstat(ownedBindPath, &stat); err != nil {
		return cleanupFailure(err)
	}
	if (stat.Mode & sysunix.S_IFMT) != sysunix.S_IFSOCK {
		return cleanupFailure(fmt.Errorf("Unix listener path is not a socket after binding: %s", ownedBindPath))
	}

	l.mu.Lock()
	l.socketIdentity = &listFileIdentity{dev: uint64(stat.Dev), ino: stat.Ino}
	l.mu.Unlock()

	if err := os.Link(ownedBindPath, l.path); err != nil {
		return cleanupFailure(err)
	}
	if err := listSetSocketMode(l.path, l.mode); err != nil {
		return cleanupFailure(err)
	}
	if err := listRemovePath(ownedBindPath); err != nil {
		return cleanupFailure(err)
	}

	l.mu.Lock()
	l.ownedBindPath = ""
	alreadyClosing := l.closing
	l.mu.Unlock()
	if alreadyClosing {
		return cleanupFailure(errors.New("Unix listener is closing or closed"))
	}

	l.wg.Add(1)
	go l.acceptLoop(netListener, accept)
	return nil
}

func (l *listUnixListener) Close() error {
	l.mu.Lock()
	if l.closeStarted {
		done := l.closeDone
		l.mu.Unlock()
		<-done
		return l.closeErr
	}
	l.closing = true
	l.closeStarted = true
	done := make(chan struct{})
	l.closeDone = done
	netListener := l.listener
	conns := make([]*UnixByteConnection, 0, len(l.connections))
	for conn := range l.connections {
		conns = append(conns, conn)
	}
	l.mu.Unlock()

	var (
		closeWg    sync.WaitGroup
		cleanupErr error
	)

	if netListener != nil {
		_ = netListener.Close()
	}

	for _, conn := range conns {
		closeWg.Add(1)
		go func(c *UnixByteConnection) {
			defer closeWg.Done()
			_ = c.Close(nil)
		}(conn)
	}
	closeWg.Wait()

	l.wg.Wait()

	if netListener != nil {
		cleanupErr = l.closeListenerAndCleanup(netListener)
	} else {
		cleanupErr = l.cleanupOwnedSocket()
	}

	l.mu.Lock()
	ownedBind := l.ownedBindPath
	l.ownedBindPath = ""
	clear(l.connections)
	l.listener = nil
	l.mu.Unlock()

	if ownedBind != "" {
		if err := listRemovePath(ownedBind); err != nil && cleanupErr == nil {
			cleanupErr = err
		}
	}

	l.mu.Lock()
	l.closeErr = cleanupErr
	l.mu.Unlock()

	close(done)
	return cleanupErr
}

func (l *listUnixListener) acceptLoop(netListener net.Listener, accept server.ByteConnectionAcceptor) {
	defer l.wg.Done()
	for {
		rawConn, err := netListener.Accept()
		if err != nil {
			l.mu.Lock()
			closing := l.closing
			l.mu.Unlock()
			if closing || errors.Is(err, net.ErrClosed) {
				return
			}
			l.reportError(err)
			return
		}

		l.mu.Lock()
		closing := l.closing
		l.mu.Unlock()
		if closing {
			_ = rawConn.Close()
			continue
		}

		sock, ok := rawConn.(UnixSocket)
		if !ok {
			_ = rawConn.Close()
			l.reportError(errors.New("Unix listener accepted connection without UnixSocket support"))
			continue
		}

		conn := NewUnixByteConnection(sock, l.options.gracefulCloseTimeoutMs, l.options.maxPendingBytes)
		l.mu.Lock()
		if l.closing {
			l.mu.Unlock()
			_ = rawConn.Close()
			conn.MarkClosed()
			continue
		}
		l.connections[conn] = struct{}{}
		l.mu.Unlock()

		handler := accept(conn)
		l.wg.Add(1)
		go l.readConnection(sock, conn, handler)
	}
}

func (l *listUnixListener) readConnection(sock UnixSocket, conn *UnixByteConnection, handler server.ByteConnectionHandler) {
	defer l.wg.Done()
	buf := make([]byte, listReadBufferSize)
	for {
		n, err := sock.Read(buf)
		if n > 0 && handler.OnData != nil {
			handler.OnData(slices.Clone(buf[:n]))
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && !conn.Closed() {
				if handler.OnError != nil {
					handler.OnError(err)
				}
				_ = sock.Close()
			}
			conn.MarkClosed()
			l.mu.Lock()
			delete(l.connections, conn)
			l.mu.Unlock()
			if handler.OnClose != nil {
				handler.OnClose()
			}
			return
		}
	}
}

func (l *listUnixListener) closeListenerAndCleanup(netListener net.Listener) error {
	if err := netListener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		l.reportError(err)
	}
	l.mu.Lock()
	ownedBind := l.ownedBindPath
	l.ownedBindPath = ""
	l.mu.Unlock()
	if ownedBind != "" {
		if err := listRemovePath(ownedBind); err != nil {
			return err
		}
	}
	return l.cleanupOwnedSocket()
}

func (l *listUnixListener) cleanupOwnedSocket() error {
	l.mu.Lock()
	identity := l.socketIdentity
	l.socketIdentity = nil
	l.mu.Unlock()
	if identity == nil {
		return nil
	}

	var current sysunix.Stat_t
	if err := sysunix.Lstat(l.path, &current); err != nil {
		if errors.Is(err, sysunix.ENOENT) || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if (current.Mode&sysunix.S_IFMT) != sysunix.S_IFSOCK || uint64(current.Dev) != identity.dev || current.Ino != identity.ino {
		return nil
	}

	preserved := filepath.Join(filepath.Dir(l.path), "cleanup-"+uuid.NewString()[:6])
	if err := os.Rename(l.path, preserved); err != nil {
		if errors.Is(err, sysunix.ENOENT) || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var moved sysunix.Stat_t
	if err := sysunix.Lstat(preserved, &moved); err != nil {
		return err
	}
	if (moved.Mode&sysunix.S_IFMT) == sysunix.S_IFSOCK && uint64(moved.Dev) == identity.dev && moved.Ino == identity.ino {
		return listRemovePath(preserved)
	}

	var check sysunix.Stat_t
	if err := sysunix.Lstat(l.path, &check); err != nil {
		if errors.Is(err, sysunix.ENOENT) || errors.Is(err, os.ErrNotExist) {
			_ = os.Rename(preserved, l.path)
		} else {
			return err
		}
	}
	return fmt.Errorf("Unix listener path changed during cleanup; preserved replacement at %s", preserved)
}

func (l *listUnixListener) reportError(err error) {
	if err == nil || l.options.onError == nil {
		return
	}
	l.options.onError(err)
}

type listWriteReq struct {
	data []byte
	done chan error
}

// UnixByteConnection wraps a stream UnixSocket with bounded backpressure and ordered graceful shutdown.
// Exported for transport-level verification.
type UnixByteConnection struct {
	socket                 UnixSocket
	gracefulCloseTimeoutMs int
	maxPendingBytes        int

	mu           sync.Mutex
	pendingBytes int
	closed       bool
	closing      bool
	queue        []*listWriteReq
	finalChunk   *[]byte
	hasFinal     bool
	notify       chan struct{}
	closeDone    chan struct{}
	closeOnce    sync.Once
	closeTimer   *time.Timer
	writerDone   chan struct{}
}

// NewUnixByteConnection constructs a UnixByteConnection managing socket writes and shutdown.
func NewUnixByteConnection(socket UnixSocket, gracefulCloseTimeoutMs int, maxPendingBytes int) *UnixByteConnection {
	c := &UnixByteConnection{
		socket:                 socket,
		gracefulCloseTimeoutMs: gracefulCloseTimeoutMs,
		maxPendingBytes:        maxPendingBytes,
		notify:                 make(chan struct{}, 1),
		closeDone:              make(chan struct{}),
		writerDone:             make(chan struct{}),
	}
	go c.writeLoop()
	return c
}

// Closed reports whether the connection has reached terminal close.
func (c *UnixByteConnection) Closed() bool {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	return closed
}

// Send queues chunk for ordered delivery on socket, enforcing maxPendingBytes.
func (c *UnixByteConnection) Send(chunk []byte) error {
	if chunk == nil {
		return js.NewTypeError("Unix connection chunks must be byte slices")
	}
	c.mu.Lock()
	if c.closed || c.closing {
		c.mu.Unlock()
		return errors.New("Unix connection is closed")
	}
	if c.pendingBytes+len(chunk) > c.maxPendingBytes {
		c.mu.Unlock()
		return errors.New("Unix connection exceeded its pending byte limit")
	}
	copied := slices.Clone(chunk)
	c.pendingBytes += len(copied)
	req := &listWriteReq{
		data: copied,
		done: make(chan error, 1),
	}
	c.queue = append(c.queue, req)
	select {
	case c.notify <- struct{}{}:
	default:
	}
	c.mu.Unlock()

	return <-req.done
}

// Close flushes queued writes, writes finalChunk if non-nil, half-closes the socket, and awaits close.
func (c *UnixByteConnection) Close(finalChunk []byte) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.MarkClosed()
		<-c.writerDone
		return nil
	}
	if c.closing {
		closeDone := c.closeDone
		c.mu.Unlock()
		<-closeDone
		<-c.writerDone
		return nil
	}
	c.closing = true
	if finalChunk != nil {
		copied := slices.Clone(finalChunk)
		c.finalChunk = &copied
		c.hasFinal = true
	}
	c.closeTimer = time.AfterFunc(time.Duration(c.gracefulCloseTimeoutMs)*time.Millisecond, func() {
		_ = c.socket.Close()
		c.MarkClosed()
	})
	select {
	case c.notify <- struct{}{}:
	default:
	}
	closeDone := c.closeDone
	c.mu.Unlock()

	<-closeDone
	<-c.writerDone
	return nil
}

// MarkClosed records that the underlying socket has fully closed.
func (c *UnixByteConnection) MarkClosed() {
	c.mu.Lock()
	c.closed = true
	c.closing = true
	if c.closeTimer != nil {
		c.closeTimer.Stop()
		c.closeTimer = nil
	}
	select {
	case c.notify <- struct{}{}:
	default:
	}
	c.mu.Unlock()

	c.closeOnce.Do(func() {
		close(c.closeDone)
	})
}

func (c *UnixByteConnection) writeLoop() {
	defer close(c.writerDone)
	for {
		c.mu.Lock()
		for len(c.queue) == 0 && !c.closing && !c.closed {
			c.mu.Unlock()
			<-c.notify
			c.mu.Lock()
		}
		if len(c.queue) > 0 {
			req := c.queue[0]
			c.queue[0] = nil
			c.queue = c.queue[1:]
			if len(c.queue) == 0 {
				c.queue = nil
			}
			closed := c.closed
			c.mu.Unlock()

			var writeErr error
			if closed {
				writeErr = errors.New("Unix connection is closed")
			} else {
				writeErr = listWriteAll(c.socket, req.data)
			}

			c.mu.Lock()
			c.pendingBytes -= len(req.data)
			c.mu.Unlock()

			req.done <- writeErr
			continue
		}

		// Queue is empty and closing/closed is set.
		hasFinal := c.hasFinal
		finalChunk := c.finalChunk
		c.hasFinal = false
		c.finalChunk = nil
		closed := c.closed
		c.mu.Unlock()

		if !closed {
			if hasFinal && finalChunk != nil && len(*finalChunk) > 0 {
				if err := listWriteAll(c.socket, *finalChunk); err != nil {
					_ = c.socket.Close()
					c.MarkClosed()
					return
				}
			}
			if err := c.socket.CloseWrite(); err != nil {
				_ = c.socket.Close()
				c.MarkClosed()
				return
			}
		}

		if closed {
			return
		}

		// Wait until socket closes (MarkClosed), draining any cancelled state.
		for {
			c.mu.Lock()
			isClosed := c.closed
			c.mu.Unlock()
			if isClosed {
				return
			}
			<-c.notify
		}
	}
}

func listWriteAll(sock UnixSocket, data []byte) error {
	for len(data) > 0 {
		n, err := sock.Write(data)
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

func listGetOwnedBindPath(socketPath string) string {
	sum := sha256.Sum256([]byte(socketPath))
	suffix := hex.EncodeToString(sum[:])[:8]
	return filepath.Join(filepath.Dir(socketPath), "bind-"+suffix)
}

func listRemoveStaleSocket(socketPath string) error {
	var original sysunix.Stat_t
	if err := sysunix.Lstat(socketPath, &original); err != nil {
		if errors.Is(err, sysunix.ENOENT) || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if (original.Mode & sysunix.S_IFMT) != sysunix.S_IFSOCK {
		return fmt.Errorf("Refusing to remove non-socket Unix listener path: %s", socketPath)
	}
	live, err := listIsSocketLive(socketPath)
	if err != nil {
		return err
	}
	if live {
		return fmt.Errorf("Unix listener is already running: %s", socketPath)
	}

	preserved := filepath.Join(filepath.Dir(socketPath), "stale-"+uuid.NewString()[:6])
	if err := os.Rename(socketPath, preserved); err != nil {
		if errors.Is(err, sysunix.ENOENT) || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var current sysunix.Stat_t
	if err := sysunix.Lstat(preserved, &current); err != nil {
		return err
	}
	if (current.Mode&sysunix.S_IFMT) != sysunix.S_IFSOCK || uint64(current.Dev) != uint64(original.Dev) || current.Ino != original.Ino {
		var check sysunix.Stat_t
		if err := sysunix.Lstat(socketPath, &check); err != nil {
			if errors.Is(err, sysunix.ENOENT) || errors.Is(err, os.ErrNotExist) {
				_ = os.Rename(preserved, socketPath)
			} else {
				return err
			}
		}
		return fmt.Errorf("Unix listener path changed while checking for a stale socket: %s", socketPath)
	}
	return listRemovePath(preserved)
}

func listRemovePath(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, sysunix.ENOENT) && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func listIsSocketLive(socketPath string) (bool, error) {
	conn, err := net.DialTimeout("unix", socketPath, listSocketProbeTimeoutMs*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true, nil
	}
	if errors.Is(err, sysunix.ECONNREFUSED) ||
		errors.Is(err, sysunix.ENOENT) ||
		errors.Is(err, sysunix.EPIPE) ||
		errors.Is(err, sysunix.ECONNRESET) ||
		errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func listSetSocketMode(socketPath string, mode int) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	err := os.Chmod(socketPath, os.FileMode(mode))
	if err != nil && !errors.Is(err, sysunix.ENOSYS) && !errors.Is(err, sysunix.ENOTSUP) {
		return err
	}
	return nil
}

func listResolveOptions(options UnixListenerOptions) (listResolvedOptions, error) {
	if options.Path == "" {
		return listResolvedOptions{}, js.NewTypeError("Server Unix socket path must not be empty")
	}
	mode := listDefaultSocketMode
	if options.Mode != nil {
		mode = *options.Mode
	}
	if mode < 0 || mode > 0o777 {
		return listResolvedOptions{}, js.NewTypeError("Server Unix socket mode must be an integer between 0 and 0o777")
	}
	maxFrameLength := protocol.DefaultMaxFrameLength
	if options.MaxFrameLength != nil {
		maxFrameLength = *options.MaxFrameLength
	}
	if maxFrameLength <= 0 || maxFrameLength > listMaxUint32 {
		return listResolvedOptions{}, js.NewTypeError(fmt.Sprintf("Server maxFrameLength must be an integer between 1 and %d", listMaxUint32))
	}
	maxPendingBytes := maxFrameLength * 4
	if options.MaxPendingBytes != nil {
		maxPendingBytes = *options.MaxPendingBytes
	}
	if maxPendingBytes < maxFrameLength+4 {
		return listResolvedOptions{}, js.NewTypeError("Server maxPendingBytes must be a safe integer at least maxFrameLength + 4")
	}
	gracefulCloseTimeoutMs := listDefaultGracefulCloseTimeoutMs
	if options.GracefulCloseTimeoutMs != nil {
		gracefulCloseTimeoutMs = *options.GracefulCloseTimeoutMs
	}
	if gracefulCloseTimeoutMs <= 0 || gracefulCloseTimeoutMs > listMaxTimerDelayMs {
		return listResolvedOptions{}, js.NewTypeError(fmt.Sprintf("Server gracefulCloseTimeoutMs must be an integer between 1 and %d", listMaxTimerDelayMs))
	}
	return listResolvedOptions{
		path:                   options.Path,
		mode:                   mode,
		maxPendingBytes:        maxPendingBytes,
		gracefulCloseTimeoutMs: gracefulCloseTimeoutMs,
		onError:                options.OnError,
	}, nil
}
