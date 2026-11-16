// Ported from packages/coding-agent/src/experimental/coordinator.ts (pi v1.0.0).

package experimental

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// CoordinatorProtocolVersion is the control protocol version spoken between coordinators, servers, and session workers.
const CoordinatorProtocolVersion = 3

const (
	coorStartTimeout       = 10 * time.Second
	coorRetryInterval      = 10 * time.Millisecond
	coorEmptyStartupGrace  = 30 * time.Second
	coorEmptyShutdownGrace = 250 * time.Millisecond
)

// CoordinatorConnectionEvent describes peer membership or routed payloads received from a coordinator.
type CoordinatorConnectionEvent struct {
	Type    string          `json:"type"`
	PeerId  string          `json:"peerId,omitzero"`
	From    string          `json:"from,omitzero"`
	Payload chord.JsonValue `json:"payload,omitzero"`
}

// CoordinatorConnectionOptions configures a CoordinatorConnection.
type CoordinatorConnectionOptions struct {
	ControlPath        string
	Endpoint           string
	ServerConnectionId string
}

type coorInboundMessage struct {
	Type               string          `json:"type"`
	ServerConnectionId *string         `json:"serverConnectionId,omitzero"`
	Peers              []string        `json:"peers,omitzero"`
	PeerId             *string         `json:"peerId,omitzero"`
	From               *string         `json:"from,omitzero"`
	Payload            chord.JsonValue `json:"payload,omitzero"`
}

type coorConnListener struct {
	id uint64
	fn func(CoordinatorConnectionEvent)
}

// CoordinatorConnection is the server-side endpoint of the coordinator's opaque message router.
type CoordinatorConnection struct {
	serverConnectionId string
	controlPath        string
	endpoint           string

	replacedCh   chan struct{}
	replacedOnce sync.Once

	mu           sync.Mutex
	nextListenId uint64
	listeners    []coorConnListener
	peerIds      map[string]struct{}
	conn         net.Conn
	writeMu      sync.Mutex
	registered   bool
	closed       bool
	replacedVal  bool
	regCh        chan error
}

// NewCoordinatorConnection constructs an unconnected CoordinatorConnection.
func NewCoordinatorConnection(options CoordinatorConnectionOptions) *CoordinatorConnection {
	serverConnId := options.ServerConnectionId
	if serverConnId == "" {
		serverConnId = uuid.NewString()
	}
	return &CoordinatorConnection{
		serverConnectionId: serverConnId,
		controlPath:        options.ControlPath,
		endpoint:           options.Endpoint,
		replacedCh:         make(chan struct{}),
		peerIds:            make(map[string]struct{}),
	}
}

// ServerConnectionId returns this connection's stable server generation identifier.
func (c *CoordinatorConnection) ServerConnectionId() string {
	return c.serverConnectionId
}

// ControlPath returns the coordinator control Unix socket path.
func (c *CoordinatorConnection) ControlPath() string {
	return c.controlPath
}

// Replaced returns a channel closed when this server connection is superseded or disconnected.
func (c *CoordinatorConnection) Replaced() <-chan struct{} {
	return c.replacedCh
}

// WasReplaced reports whether this server connection has been superseded or disconnected unexpectedly.
func (c *CoordinatorConnection) WasReplaced() bool {
	c.mu.Lock()
	replaced := c.replacedVal
	c.mu.Unlock()
	return replaced
}

// PeerIds returns a snapshot of currently connected coordinator peer IDs.
func (c *CoordinatorConnection) PeerIds() []string {
	c.mu.Lock()
	out := make([]string, 0, len(c.peerIds))
	for id := range c.peerIds {
		out = append(out, id)
	}
	c.mu.Unlock()
	return out
}

// OnEvent registers a listener for peer lifecycle and routed payload events.
func (c *CoordinatorConnection) OnEvent(listener func(CoordinatorConnectionEvent)) func() {
	c.mu.Lock()
	c.nextListenId++
	id := c.nextListenId
	c.listeners = append(c.listeners, coorConnListener{id: id, fn: listener})
	c.mu.Unlock()

	return func() {
		c.mu.Lock()
		for i, entry := range c.listeners {
			if entry.id == id {
				c.listeners = append(c.listeners[:i], c.listeners[i+1:]...)
				break
			}
		}
		c.mu.Unlock()
	}
}

// Connect establishes the control connection and registers this server with the coordinator.
func (c *CoordinatorConnection) Connect() error {
	c.mu.Lock()
	if c.conn != nil {
		c.mu.Unlock()
		return errors.New("Coordinator server is already connected")
	}
	if c.closed {
		c.mu.Unlock()
		return errors.New("Coordinator server closed")
	}
	regCh := make(chan error, 1)
	c.regCh = regCh
	c.mu.Unlock()

	conn, err := coorDialSocket(c.controlPath)
	if err != nil {
		c.failRegistration(err)
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return errors.New("Coordinator server closed")
	}
	c.conn = conn
	c.mu.Unlock()

	go c.readLoop(conn)

	regMsg := jsonx.ObjectOf(
		"type", "register_server",
		"protocol", CoordinatorProtocolVersion,
		"serverConnectionId", c.serverConnectionId,
		"endpoint", c.endpoint,
	)
	if err := c.writeLineRaw(conn, regMsg); err != nil {
		_ = conn.Close()
		c.disconnected(err)
		return err
	}

	return <-regCh
}

// Send transmits an opaque JSON payload to a connected peer.
func (c *CoordinatorConnection) Send(peerId string, payload any) error {
	return c.write(jsonx.ObjectOf(
		"type", "send",
		"to", peerId,
		"payload", payload,
	))
}

// Broadcast transmits an opaque JSON payload from the current server to all connected peers.
func (c *CoordinatorConnection) Broadcast(payload any) error {
	return c.write(jsonx.ObjectOf(
		"type", "broadcast",
		"payload", payload,
	))
}

// Close closes the coordinator control connection and clears tracked peers.
func (c *CoordinatorConnection) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	c.conn = nil
	c.registered = false
	clear(c.peerIds)
	regCh := c.regCh
	c.regCh = nil
	c.mu.Unlock()

	if regCh != nil {
		coorSendErrNonBlocking(regCh, errors.New("Coordinator server closed"))
	}
	if conn != nil {
		_ = conn.Close()
	}
	return nil
}

func (c *CoordinatorConnection) write(message any) error {
	c.mu.Lock()
	conn := c.conn
	ok := c.registered && conn != nil && !c.closed
	c.mu.Unlock()
	if !ok {
		return errors.New("Coordinator server is not connected")
	}
	return c.writeLineRaw(conn, message)
}

func (c *CoordinatorConnection) writeLineRaw(conn net.Conn, message any) error {
	line, err := EncodeControlLine(message)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	_, err = conn.Write(line)
	c.writeMu.Unlock()
	return err
}

func (c *CoordinatorConnection) failRegistration(err error) {
	c.mu.Lock()
	regCh := c.regCh
	c.regCh = nil
	c.mu.Unlock()
	if regCh != nil {
		coorSendErrNonBlocking(regCh, err)
	}
}

func (c *CoordinatorConnection) readLoop(conn net.Conn) {
	reader := bufio.NewReaderSize(conn, 64*1024)
	for {
		line, err := coorReadLine(reader, MaxControlLineBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.disconnected(errors.New("Coordinator connection closed"))
			} else {
				_ = conn.Close()
				c.disconnected(err)
			}
			return
		}
		var raw any
		raw, err = jsonx.Parse(line)
		if err != nil {
			_ = conn.Close()
			c.disconnected(errors.New("Coordinator sent invalid JSON"))
			return
		}
		if err := c.handleMessage(conn, raw); err != nil {
			_ = conn.Close()
			c.disconnected(err)
			return
		}
	}
}

func (c *CoordinatorConnection) handleMessage(conn net.Conn, raw any) error {
	obj, ok := raw.(*jsonx.Object)
	if !ok {
		return errors.New("Coordinator sent an invalid message")
	}
	rawBytes, err := jsonx.Marshal(obj)
	if err != nil {
		return errors.New("Coordinator sent an invalid message")
	}
	var msg coorInboundMessage
	if err := jsonx.Unmarshal(rawBytes, &msg); err != nil || msg.Type == "" {
		return errors.New("Coordinator sent an invalid message")
	}

	switch msg.Type {
	case "server_registered":
		if msg.ServerConnectionId == nil || msg.Peers == nil {
			return errors.New("Coordinator sent an invalid message")
		}
		if *msg.ServerConnectionId != c.serverConnectionId {
			return errors.New("Coordinator returned an invalid server registration")
		}
		c.mu.Lock()
		for _, peerId := range msg.Peers {
			c.peerIds[peerId] = struct{}{}
		}
		c.registered = true
		regCh := c.regCh
		c.regCh = nil
		c.mu.Unlock()
		if regCh != nil {
			coorSendErrNonBlocking(regCh, nil)
		}
		return nil

	case "server_replaced":
		c.markReplaced()
		return nil

	case "peer_connected":
		if msg.PeerId == nil {
			return errors.New("Coordinator sent an invalid message")
		}
		c.mu.Lock()
		c.peerIds[*msg.PeerId] = struct{}{}
		c.mu.Unlock()
		c.emit(CoordinatorConnectionEvent{Type: "peer_connected", PeerId: *msg.PeerId})
		return nil

	case "peer_disconnected":
		if msg.PeerId == nil {
			return errors.New("Coordinator sent an invalid message")
		}
		c.mu.Lock()
		delete(c.peerIds, *msg.PeerId)
		c.mu.Unlock()
		c.emit(CoordinatorConnectionEvent{Type: "peer_disconnected", PeerId: *msg.PeerId})
		return nil

	case "message":
		if msg.From == nil {
			return errors.New("Coordinator sent an invalid message")
		}
		c.emit(CoordinatorConnectionEvent{Type: "message", From: *msg.From, Payload: msg.Payload})
		return nil

	default:
		return errors.New("Coordinator sent an unsupported message")
	}
}

func (c *CoordinatorConnection) emit(event CoordinatorConnectionEvent) {
	c.mu.Lock()
	snapshot := make([]coorConnListener, len(c.listeners))
	copy(snapshot, c.listeners)
	c.mu.Unlock()

	for _, entry := range snapshot {
		entry.fn(event)
	}
}

func (c *CoordinatorConnection) disconnected(err error) {
	c.mu.Lock()
	c.conn = nil
	closed := c.closed
	regCh := c.regCh
	c.regCh = nil
	c.mu.Unlock()

	if closed {
		return
	}
	if regCh != nil {
		coorSendErrNonBlocking(regCh, err)
	}
	c.markReplaced()
}

func (c *CoordinatorConnection) markReplaced() {
	c.mu.Lock()
	if c.replacedVal {
		c.mu.Unlock()
		return
	}
	c.replacedVal = true
	c.mu.Unlock()
	c.replacedOnce.Do(func() {
		close(c.replacedCh)
	})
}

// CoordinatorStartupLease holds an open control connection that keeps a freshly launched coordinator alive until registration completes.
type CoordinatorStartupLease struct {
	mu   sync.Mutex
	conn net.Conn
}

// Close releases the startup lease connection.
func (l *CoordinatorStartupLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	conn := l.conn
	l.conn = nil
	l.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

// EnsureCoordinator connects to an existing coordinator control socket or launches a background coordinator process until it is accepting connections.
func EnsureCoordinator(publicPath string, controlPath string) (*CoordinatorStartupLease, error) {
	existing, err := coorTryConnect(controlPath)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return &CoordinatorStartupLease{conn: existing}, nil
	}

	child, err := SpawnInternalProcess(InternalRoleCoordinator, []string{publicPath, controlPath}, nil)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(coorStartTimeout)
	for {
		conn, err := coorTryConnect(controlPath)
		if err != nil {
			return nil, err
		}
		if conn != nil {
			return &CoordinatorStartupLease{conn: conn}, nil
		}
		if child.Exited() {
			return nil, errors.New("Coordinator exited during startup")
		}
		if !time.Now().Before(deadline) {
			return nil, errors.New("Timed out waiting for coordinator startup")
		}
		time.Sleep(coorRetryInterval)
	}
}

func coorDialSocket(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}

func coorTryConnect(path string) (net.Conn, error) {
	conn, err := coorDialSocket(path)
	if err == nil {
		return conn, nil
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil, nil
	}
	return nil, err
}

func coorReadLine(reader *bufio.Reader, maxBytes int) ([]byte, error) {
	var line []byte
	for {
		frag, err := reader.ReadSlice('\n')
		line = append(line, frag...)
		if len(line) > maxBytes {
			return nil, errors.New("Coordinator message is too large")
		}
		if err == nil {
			if len(line) > 0 && line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return nil, io.EOF
		}
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}
}

func coorSendErrNonBlocking(ch chan error, err error) {
	select {
	case ch <- err:
	default:
	}
}

// Coordinator process implementation (intentionally transport-only shim).

type coorControlConn struct {
	conn     net.Conn
	sendCh   chan []byte
	closeMu  sync.Mutex
	closed   bool
	peerId   string
	isServer bool
	serverId string
	endpoint string
}

func coorNewControlConn(conn net.Conn) *coorControlConn {
	c := &coorControlConn{
		conn:   conn,
		sendCh: make(chan []byte, 256),
	}
	go c.writeLoop()
	return c
}

func (c *coorControlConn) writeLoop() {
	for line := range c.sendCh {
		if _, err := c.conn.Write(line); err != nil {
			_ = c.conn.Close()
			return
		}
	}
}

func (c *coorControlConn) writeLine(message any) {
	line, err := EncodeControlLine(message)
	if err != nil {
		return
	}
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return
	}
	select {
	case c.sendCh <- line:
		c.closeMu.Unlock()
	default:
		c.closed = true
		close(c.sendCh)
		c.closeMu.Unlock()
		_ = c.conn.Close()
	}
}

func (c *coorControlConn) close() {
	c.closeMu.Lock()
	if !c.closed {
		c.closed = true
		close(c.sendCh)
	}
	c.closeMu.Unlock()
	_ = c.conn.Close()
}

type coorDaemonControlMessage struct {
	Type               string          `json:"type"`
	Protocol           any             `json:"protocol,omitzero"`
	ServerConnectionId any             `json:"serverConnectionId,omitzero"`
	Endpoint           any             `json:"endpoint,omitzero"`
	PeerId             any             `json:"peerId,omitzero"`
	To                 any             `json:"to,omitzero"`
	Payload            chord.JsonValue `json:"payload,omitzero"`
}

type coorPublicPair struct {
	client   net.Conn
	upstream net.Conn
}

type coorDaemon struct {
	publicPath  string
	controlPath string

	publicListener  net.Listener
	controlListener net.Listener
	doneCh          chan struct{}

	mu            sync.Mutex
	peers         map[string]*coorControlConn
	controlConns  map[*coorControlConn]struct{}
	publicConns   map[*coorPublicPair]struct{}
	currentServer *coorControlConn
	shuttingDown  bool
	emptyTimer    *time.Timer
	emptyTimerGen uint64
}

func coorNewDaemon(publicPath string, controlPath string) *coorDaemon {
	return &coorDaemon{
		publicPath:   publicPath,
		controlPath:  controlPath,
		doneCh:       make(chan struct{}),
		peers:        make(map[string]*coorControlConn),
		controlConns: make(map[*coorControlConn]struct{}),
		publicConns:  make(map[*coorPublicPair]struct{}),
	}
}

// RunCoordinatorProcess runs the coordinator routing daemon until shutdown or idle retirement.
func RunCoordinatorProcess(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return RunCoordinatorProcessContext(ctx, args)
}

// RunCoordinatorProcessContext runs the coordinator routing daemon until ctx is cancelled, shutdown occurs, or idle retirement fires.
func RunCoordinatorProcessContext(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] == "" || args[1] == "" {
		return errors.New("Coordinator requires public and control socket paths")
	}
	d := coorNewDaemon(args[0], args[1])
	if err := d.start(); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		d.shutdown()
	}()

	<-d.doneCh
	return nil
}

func (d *coorDaemon) start() error {
	if err := coorRemoveStaleSocket(d.controlPath); err != nil {
		return err
	}
	if err := coorRemoveStaleSocket(d.publicPath); err != nil {
		return err
	}

	ctrlLn, err := net.Listen("unix", d.controlPath)
	if err != nil {
		_ = coorCleanupSocket(d.controlPath)
		_ = coorCleanupSocket(d.publicPath)
		return err
	}
	if err := os.Chmod(d.controlPath, 0o600); err != nil {
		_ = ctrlLn.Close()
		_ = coorCleanupSocket(d.controlPath)
		_ = coorCleanupSocket(d.publicPath)
		return err
	}

	pubLn, err := net.Listen("unix", d.publicPath)
	if err != nil {
		_ = ctrlLn.Close()
		_ = coorCleanupSocket(d.controlPath)
		_ = coorCleanupSocket(d.publicPath)
		return err
	}
	if err := os.Chmod(d.publicPath, 0o600); err != nil {
		_ = pubLn.Close()
		_ = ctrlLn.Close()
		_ = coorCleanupSocket(d.controlPath)
		_ = coorCleanupSocket(d.publicPath)
		return err
	}

	d.mu.Lock()
	d.controlListener = ctrlLn
	d.publicListener = pubLn
	d.scheduleEmptyShutdownLocked(coorEmptyStartupGrace)
	d.mu.Unlock()

	go d.acceptLoop(ctrlLn, d.handleControlConn)
	go d.acceptLoop(pubLn, d.handlePublicConn)
	return nil
}

func (d *coorDaemon) acceptLoop(ln net.Listener, handler func(net.Conn)) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handler(conn)
	}
}

func (d *coorDaemon) handleControlConn(rawConn net.Conn) {
	cc := coorNewControlConn(rawConn)
	d.mu.Lock()
	if d.shuttingDown {
		d.mu.Unlock()
		cc.close()
		return
	}
	d.controlConns[cc] = struct{}{}
	d.mu.Unlock()

	defer func() {
		cc.close()
		var notifyPeersMsg any
		var notifyServerMsg any
		var serverTarget *coorControlConn
		var peerTargets []*coorControlConn

		d.mu.Lock()
		delete(d.controlConns, cc)
		if cc.isServer && d.currentServer == cc {
			d.currentServer = nil
			notifyPeersMsg = jsonx.ObjectOf("type", "server_disconnected", "serverConnectionId", cc.serverId)
			peerTargets = make([]*coorControlConn, 0, len(d.peers))
			for _, p := range d.peers {
				peerTargets = append(peerTargets, p)
			}
		}
		if cc.peerId != "" && d.peers[cc.peerId] == cc {
			delete(d.peers, cc.peerId)
			if d.currentServer != nil {
				serverTarget = d.currentServer
				notifyServerMsg = jsonx.ObjectOf("type", "peer_disconnected", "peerId", cc.peerId)
			}
		}
		d.checkEmptyLocked()
		d.mu.Unlock()

		for _, p := range peerTargets {
			p.writeLine(notifyPeersMsg)
		}
		if serverTarget != nil && notifyServerMsg != nil {
			serverTarget.writeLine(notifyServerMsg)
		}
	}()

	reader := bufio.NewReaderSize(rawConn, 64*1024)
	for {
		line, err := coorReadLine(reader, MaxControlLineBytes)
		if err != nil {
			return
		}
		var msg coorDaemonControlMessage
		if err := jsonx.Unmarshal(line, &msg); err != nil || msg.Type == "" {
			return
		}

		if !cc.isServer && cc.peerId == "" {
			switch msg.Type {
			case "register_server":
				if err := d.registerServer(cc, msg); err != nil {
					return
				}
			case "register_peer":
				if err := d.registerPeer(cc, msg); err != nil {
					return
				}
			default:
				return
			}
			continue
		}

		if cc.isServer {
			d.mu.Lock()
			isCurrent := d.currentServer == cc
			d.mu.Unlock()
			if isCurrent {
				if err := d.handleRoutedMessage("server", msg); err != nil {
					return
				}
			}
			continue
		}

		if err := d.handleRoutedMessage(cc.peerId, msg); err != nil {
			return
		}
	}
}

func (d *coorDaemon) registerServer(cc *coorControlConn, msg coorDaemonControlMessage) error {
	proto, ok := coorAsInt(msg.Protocol)
	if !ok || proto != CoordinatorProtocolVersion {
		return errors.New("Unsupported coordinator protocol")
	}
	serverConnId, ok := msg.ServerConnectionId.(string)
	if !ok || serverConnId == "" {
		return errors.New("Coordinator serverConnectionId must be a string")
	}
	endpoint, ok := msg.Endpoint.(string)
	if !ok || endpoint == "" {
		return errors.New("Coordinator endpoint must be a string")
	}

	d.mu.Lock()
	cc.isServer = true
	cc.serverId = serverConnId
	cc.endpoint = endpoint
	previous := d.currentServer
	d.currentServer = cc
	d.cancelEmptyShutdownLocked()
	peersList := make([]string, 0, len(d.peers))
	peerTargets := make([]*coorControlConn, 0, len(d.peers))
	for id, p := range d.peers {
		peersList = append(peersList, id)
		peerTargets = append(peerTargets, p)
	}
	var pairs []*coorPublicPair
	if previous != nil && previous != cc {
		pairs = make([]*coorPublicPair, 0, len(d.publicConns))
		for pair := range d.publicConns {
			pairs = append(pairs, pair)
		}
		clear(d.publicConns)
	}
	d.mu.Unlock()

	cc.writeLine(jsonx.ObjectOf(
		"type", "server_registered",
		"serverConnectionId", serverConnId,
		"peers", peersList,
	))
	if previous != nil && previous != cc {
		for _, pair := range pairs {
			_ = pair.client.Close()
			if pair.upstream != nil {
				_ = pair.upstream.Close()
			}
		}
		prevDisc := jsonx.ObjectOf("type", "server_disconnected", "serverConnectionId", previous.serverId)
		for _, p := range peerTargets {
			p.writeLine(prevDisc)
		}
		previous.writeLine(jsonx.ObjectOf("type", "server_replaced"))
	}
	connMsg := jsonx.ObjectOf("type", "server_connected", "serverConnectionId", serverConnId)
	for _, p := range peerTargets {
		p.writeLine(connMsg)
	}
	return nil
}

func (d *coorDaemon) registerPeer(cc *coorControlConn, msg coorDaemonControlMessage) error {
	proto, ok := coorAsInt(msg.Protocol)
	if !ok || proto != CoordinatorProtocolVersion {
		return errors.New("Unsupported coordinator protocol")
	}
	peerId, ok := msg.PeerId.(string)
	if !ok || peerId == "" {
		return errors.New("Coordinator peerId must be a string")
	}

	d.mu.Lock()
	if peerId == "server" || d.peers[peerId] != nil {
		d.mu.Unlock()
		return fmt.Errorf("Coordinator peer is already connected: %s", peerId)
	}
	cc.peerId = peerId
	d.peers[peerId] = cc
	d.cancelEmptyShutdownLocked()
	cur := d.currentServer
	d.mu.Unlock()

	if cur == nil {
		cc.writeLine(jsonx.ObjectOf("type", "peer_registered", "peerId", peerId))
	} else {
		cc.writeLine(jsonx.ObjectOf(
			"type", "peer_registered",
			"peerId", peerId,
			"serverConnectionId", cur.serverId,
		))
		cur.writeLine(jsonx.ObjectOf("type", "peer_connected", "peerId", peerId))
	}
	return nil
}

func (d *coorDaemon) handleRoutedMessage(from string, msg coorDaemonControlMessage) error {
	switch msg.Type {
	case "send":
		to, ok := msg.To.(string)
		if !ok || to == "" {
			return errors.New("Coordinator message target must be a string")
		}
		d.mu.Lock()
		var target *coorControlConn
		if to == "server" {
			target = d.currentServer
		} else {
			target = d.peers[to]
		}
		d.mu.Unlock()
		if target != nil {
			target.writeLine(jsonx.ObjectOf("type", "message", "from", from, "payload", msg.Payload))
		}
		return nil

	case "broadcast":
		if from != "server" {
			return errors.New("Only the current server may broadcast")
		}
		d.mu.Lock()
		targets := make([]*coorControlConn, 0, len(d.peers))
		for _, p := range d.peers {
			targets = append(targets, p)
		}
		d.mu.Unlock()
		routed := jsonx.ObjectOf("type", "message", "from", from, "payload", msg.Payload)
		for _, p := range targets {
			p.writeLine(routed)
		}
		return nil

	default:
		return fmt.Errorf("Unknown coordinator routing message: %s", msg.Type)
	}
}

func (d *coorDaemon) handlePublicConn(clientConn net.Conn) {
	d.mu.Lock()
	if d.shuttingDown || d.currentServer == nil {
		d.mu.Unlock()
		_ = clientConn.Close()
		return
	}
	endpoint := d.currentServer.endpoint
	d.cancelEmptyShutdownLocked()
	pair := &coorPublicPair{client: clientConn}
	d.publicConns[pair] = struct{}{}
	d.mu.Unlock()

	finalize := func() {
		_ = clientConn.Close()
		if pair.upstream != nil {
			_ = pair.upstream.Close()
		}
		d.mu.Lock()
		delete(d.publicConns, pair)
		d.checkEmptyLocked()
		d.mu.Unlock()
	}

	upstream, err := net.Dial("unix", endpoint)
	if err != nil {
		finalize()
		return
	}
	d.mu.Lock()
	if d.shuttingDown {
		d.mu.Unlock()
		_ = upstream.Close()
		finalize()
		return
	}
	pair.upstream = upstream
	d.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, clientConn)
		_ = upstream.Close()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, upstream)
		_ = clientConn.Close()
	}()
	wg.Wait()
	finalize()
}

func (d *coorDaemon) checkEmptyLocked() {
	if d.shuttingDown || d.currentServer != nil || len(d.peers) > 0 || len(d.publicConns) > 0 || len(d.controlConns) > 0 {
		d.cancelEmptyShutdownLocked()
		return
	}
	d.scheduleEmptyShutdownLocked(coorEmptyShutdownGrace)
}

func (d *coorDaemon) scheduleEmptyShutdownLocked(delay time.Duration) {
	if d.emptyTimer != nil || d.shuttingDown {
		return
	}
	d.emptyTimerGen++
	gen := d.emptyTimerGen
	d.emptyTimer = time.AfterFunc(delay, func() {
		d.mu.Lock()
		if d.emptyTimerGen != gen || d.shuttingDown {
			d.mu.Unlock()
			return
		}
		d.emptyTimer = nil
		empty := d.currentServer == nil && len(d.peers) == 0 && len(d.publicConns) == 0 && len(d.controlConns) == 0
		d.mu.Unlock()
		if empty {
			d.shutdown()
		}
	})
}

func (d *coorDaemon) cancelEmptyShutdownLocked() {
	d.emptyTimerGen++
	if d.emptyTimer != nil {
		d.emptyTimer.Stop()
		d.emptyTimer = nil
	}
}

func (d *coorDaemon) shutdown() {
	d.mu.Lock()
	if d.shuttingDown {
		d.mu.Unlock()
		return
	}
	d.shuttingDown = true
	d.cancelEmptyShutdownLocked()

	pairs := make([]*coorPublicPair, 0, len(d.publicConns))
	for pair := range d.publicConns {
		pairs = append(pairs, pair)
	}
	clear(d.publicConns)

	controls := make([]*coorControlConn, 0, len(d.controlConns))
	for cc := range d.controlConns {
		controls = append(controls, cc)
	}
	clear(d.controlConns)

	pubLn := d.publicListener
	ctrlLn := d.controlListener
	d.publicListener = nil
	d.controlListener = nil
	d.mu.Unlock()

	for _, pair := range pairs {
		_ = pair.client.Close()
		if pair.upstream != nil {
			_ = pair.upstream.Close()
		}
	}
	for _, cc := range controls {
		cc.close()
	}
	if pubLn != nil {
		_ = pubLn.Close()
	}
	if ctrlLn != nil {
		_ = ctrlLn.Close()
	}
	_ = coorCleanupSocket(d.publicPath)
	_ = coorCleanupSocket(d.controlPath)
	close(d.doneCh)
}

func coorAsInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		i := int(n)
		return i, float64(i) == n
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

func coorRemoveStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("Coordinator path is not a socket: %s", path)
	}
	conn, err := net.Dial("unix", path)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("Coordinator socket is already active: %s", path)
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) || os.IsNotExist(err) {
		return coorCleanupSocket(path)
	}
	return err
}

func coorCleanupSocket(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
