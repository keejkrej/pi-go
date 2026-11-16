// Ported from packages/coding-agent/src/experimental/session-worker-manager.ts (pi v1.0.0).

package experimental

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/server"
)

const (
	swmWorkerStartupTimeout   = 15 * time.Second
	swmWorkerShutdownTimeout  = 10 * time.Second
	swmWorkerDiscoveryTimeout = 5 * time.Second
	swmWorkerDemandTimeout    = 5 * time.Second
)

// SessionPluginSelectionConflictError reports that an open/running session already uses a different plugin selection.
type SessionPluginSelectionConflictError struct {
	Message string
}

// NewSessionPluginSelectionConflictError constructs a SessionPluginSelectionConflictError.
func NewSessionPluginSelectionConflictError(message string) *SessionPluginSelectionConflictError {
	return &SessionPluginSelectionConflictError{Message: message}
}

func (e *SessionPluginSelectionConflictError) Error() string {
	return e.Message
}

// Name returns "SessionPluginSelectionConflictError".
func (e *SessionPluginSelectionConflictError) Name() string {
	return "SessionPluginSelectionConflictError"
}

// SessionWorkerModel identifies an optional initial provider/model override for newly launched workers.
type SessionWorkerModel struct {
	Provider string
	Model    string
}

// SessionCoordinatorClient is the coordinator surface required by SessionWorkerManager.
type SessionCoordinatorClient interface {
	ControlPath() string
	ServerConnectionId() string
	WasReplaced() bool
	OnEvent(listener func(CoordinatorConnectionEvent)) func()
	Send(peerId string, payload any) error
	Broadcast(payload any) error
}

type swmWorkerRecord struct {
	peerId              string
	metadata            SessionCatalogMetadata
	pid                 int
	token               string
	pluginManifestPaths []string
	terminatedCh        chan error
	terminatedOnce      sync.Once
	mu                  sync.Mutex
	terminatedErr       error
	attachmentIds       map[string]struct{}
	expectedStop        bool
	stopOnce            sync.Once
	stopDone            chan struct{}
	stopping            bool
}

func swmNewWorkerRecord(peerId string, metadata SessionCatalogMetadata, pid int, token string, manifests []string) *swmWorkerRecord {
	cp := make([]string, len(manifests))
	copy(cp, manifests)
	return &swmWorkerRecord{
		peerId:              peerId,
		metadata:            metadata,
		pid:                 pid,
		token:               token,
		pluginManifestPaths: cp,
		terminatedCh:        make(chan error, 1),
		attachmentIds:       make(map[string]struct{}),
		stopDone:            make(chan struct{}),
	}
}

func (w *swmWorkerRecord) finishTerminated(err error) {
	w.terminatedOnce.Do(func() {
		w.mu.Lock()
		w.terminatedErr = err
		w.mu.Unlock()
		if err != nil {
			select {
			case w.terminatedCh <- err:
			default:
			}
		}
		close(w.terminatedCh)
	})
}

type swmPendingDemand struct {
	attachmentId string
	attached     bool
	requestId    string
	timer        *time.Timer
	worker       *swmWorkerRecord
	doneCh       chan error
	settled      bool
}

type swmOperationResult struct {
	hasValue bool
	value    chord.JsonValue
	err      error
}

type swmPendingOperation struct {
	worker  *swmWorkerRecord
	scope   WorkerOperationScope
	cleanup func()
	doneCh  chan swmOperationResult
}

type swmServiceSubscription struct {
	worker   *swmWorkerRecord
	scope    WorkerOperationScope
	publish  func(update chord.ServiceProviderUpdate, ctx chord.Context) error
	mu       sync.Mutex
	closed   bool
	queue    []chord.ServiceProviderUpdate
	notifyCh chan struct{}
}

func swmNewServiceSubscription(
	worker *swmWorkerRecord,
	scope WorkerOperationScope,
	publish func(update chord.ServiceProviderUpdate, ctx chord.Context) error,
) *swmServiceSubscription {
	s := &swmServiceSubscription{
		worker:   worker,
		scope:    scope,
		publish:  publish,
		notifyCh: make(chan struct{}, 1),
	}
	go s.deliverLoop()
	return s
}

func (s *swmServiceSubscription) enqueue(update chord.ServiceProviderUpdate) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.queue = append(s.queue, update)
	s.mu.Unlock()
	select {
	case s.notifyCh <- struct{}{}:
	default:
	}
}

func (s *swmServiceSubscription) stop() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.queue = nil
	}
	s.mu.Unlock()
	select {
	case s.notifyCh <- struct{}{}:
	default:
	}
}

func (s *swmServiceSubscription) deliverLoop() {
	for range s.notifyCh {
		for {
			s.mu.Lock()
			if s.closed || len(s.queue) == 0 {
				closed := s.closed
				s.mu.Unlock()
				if closed {
					return
				}
				break
			}
			next := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			_ = s.publish(next, chord.TodoContext)
		}
	}
}

type swmPendingLaunch struct {
	sessionKey          string
	peerId              string
	token               string
	pluginManifestPaths []string
	child               *InternalChildProcess
	timer               *time.Timer
	doneCh              chan struct{}
	worker              *swmWorkerRecord
	err                 error
	settled             bool
}

// SessionWorkerManagerOptions configures optional timeouts and test hooks for SessionWorkerManager.
type SessionWorkerManagerOptions struct {
	StartupTimeout   time.Duration
	ShutdownTimeout  time.Duration
	DiscoveryTimeout time.Duration
	DemandTimeout    time.Duration
	KillPid          func(pid int) error
}

// SessionWorkerManager tracks per-session worker processes and routes coordinator messages for one replaceable server.
type SessionWorkerManager struct {
	coordinator          SessionCoordinatorClient
	sessionDir           string
	model                *SessionWorkerModel
	onWorkerCountChanged func(count int)
	removeListener       func()
	startupTimeout       time.Duration
	shutdownTimeout      time.Duration
	discoveryTimeout     time.Duration
	demandTimeout        time.Duration
	killPid              func(pid int) error

	mu                   sync.Mutex
	workerPids           map[string]int
	workersBySession     map[string]*swmWorkerRecord
	workersByPeer        map[string]*swmWorkerRecord
	pending              map[string]*swmPendingLaunch
	pendingDemand        map[string]*swmPendingDemand
	pendingOperations    map[string]*swmPendingOperation
	serviceSubscriptions map[string]*swmServiceSubscription
	discoveryPeers       map[string]struct{}
	discoveryCh          chan struct{}
	detached             bool
	shuttingDown         bool
}

// NewSessionWorkerManager creates a SessionWorkerManager attached to coordinator.
func NewSessionWorkerManager(
	coordinator SessionCoordinatorClient,
	sessionDir string,
	model *SessionWorkerModel,
	onWorkerCountChanged func(count int),
	options ...SessionWorkerManagerOptions,
) *SessionWorkerManager {
	startupTimeout := swmWorkerStartupTimeout
	shutdownTimeout := swmWorkerShutdownTimeout
	discoveryTimeout := swmWorkerDiscoveryTimeout
	demandTimeout := swmWorkerDemandTimeout
	killPid := func(pid int) error {
		return syscall.Kill(pid, syscall.SIGKILL)
	}
	if len(options) > 0 {
		opts := options[0]
		if opts.StartupTimeout > 0 {
			startupTimeout = opts.StartupTimeout
		}
		if opts.ShutdownTimeout > 0 {
			shutdownTimeout = opts.ShutdownTimeout
		}
		if opts.DiscoveryTimeout > 0 {
			discoveryTimeout = opts.DiscoveryTimeout
		}
		if opts.DemandTimeout > 0 {
			demandTimeout = opts.DemandTimeout
		}
		if opts.KillPid != nil {
			killPid = opts.KillPid
		}
	}
	m := &SessionWorkerManager{
		coordinator:          coordinator,
		sessionDir:           sessionDir,
		model:                model,
		onWorkerCountChanged: onWorkerCountChanged,
		startupTimeout:       startupTimeout,
		shutdownTimeout:      shutdownTimeout,
		discoveryTimeout:     discoveryTimeout,
		demandTimeout:        demandTimeout,
		killPid:              killPid,
		workerPids:           make(map[string]int),
		workersBySession:     make(map[string]*swmWorkerRecord),
		workersByPeer:        make(map[string]*swmWorkerRecord),
		pending:              make(map[string]*swmPendingLaunch),
		pendingDemand:        make(map[string]*swmPendingDemand),
		pendingOperations:    make(map[string]*swmPendingOperation),
		serviceSubscriptions: make(map[string]*swmServiceSubscription),
	}
	if coordinator != nil {
		m.removeListener = coordinator.OnEvent(m.handleCoordinatorEvent)
	}
	return m
}

// TrackedSessions returns metadata for all currently ready session workers.
func (m *SessionWorkerManager) TrackedSessions() []SessionCatalogMetadata {
	m.mu.Lock()
	out := make([]SessionCatalogMetadata, 0, len(m.workersBySession))
	for _, w := range m.workersBySession {
		out = append(out, w.metadata)
	}
	m.mu.Unlock()
	return out
}

// WorkerPids returns a snapshot map of active sessionId -> OS process PID.
func (m *SessionWorkerManager) WorkerPids() map[string]int {
	m.mu.Lock()
	out := make(map[string]int, len(m.workerPids))
	for k, v := range m.workerPids {
		out[k] = v
	}
	m.mu.Unlock()
	return out
}

// WorkerPid returns the OS PID for sessionId if tracked.
func (m *SessionWorkerManager) WorkerPid(sessionId string) (int, bool) {
	m.mu.Lock()
	pid, ok := m.workerPids[sessionId]
	m.mu.Unlock()
	return pid, ok
}

// AssertSessionPluginManifestPaths fails with SessionPluginSelectionConflictError if session already has another plugin set.
func (m *SessionWorkerManager) AssertSessionPluginManifestPaths(metadata SessionCatalogMetadata, manifestPaths []string) error {
	m.mu.Lock()
	existing := m.workersBySession[metadata.Path]
	pending := m.pending[metadata.Path]
	m.mu.Unlock()

	if existing != nil && !swmSameStrings(existing.pluginManifestPaths, manifestPaths) {
		return NewSessionPluginSelectionConflictError(
			fmt.Sprintf("Session %s is active with a different plugin selection", metadata.Id),
		)
	}
	if pending != nil && !swmSameStrings(pending.pluginManifestPaths, manifestPaths) {
		return NewSessionPluginSelectionConflictError(
			fmt.Sprintf("Session %s is starting with a different plugin selection", metadata.Id),
		)
	}
	return nil
}

// Discover probes peerIds previously registered on the coordinator to adopt surviving workers.
func (m *SessionWorkerManager) Discover(peerIds []string) error {
	m.mu.Lock()
	if m.detached {
		m.mu.Unlock()
		return nil
	}
	undiscovered := make(map[string]struct{})
	for _, peerId := range peerIds {
		if m.workersByPeer[peerId] == nil && m.pendingPeerLocked(peerId) == nil {
			undiscovered[peerId] = struct{}{}
		}
	}
	if len(undiscovered) == 0 {
		m.mu.Unlock()
		return nil
	}
	discoveryCh := make(chan struct{})
	m.discoveryPeers = undiscovered
	m.discoveryCh = discoveryCh
	m.mu.Unlock()

	err := m.coordinator.Broadcast(jsonx.ObjectOf("type", "discover_workers"))
	if err != nil {
		m.mu.Lock()
		if m.discoveryCh == discoveryCh {
			m.discoveryPeers = nil
			m.discoveryCh = nil
			close(discoveryCh)
		}
		m.mu.Unlock()
		return err
	}

	timer := time.NewTimer(m.discoveryTimeout)
	defer timer.Stop()
	select {
	case <-discoveryCh:
	case <-timer.C:
	}

	m.mu.Lock()
	if m.discoveryCh == discoveryCh {
		m.discoveryPeers = nil
		m.discoveryCh = nil
		close(discoveryCh)
	}
	m.mu.Unlock()
	return nil
}

// OpenSession returns a RoutedSessionHandle for metadata, adopting an existing worker or launching one.
func (m *SessionWorkerManager) OpenSession(metadata SessionCatalogMetadata, ctx chord.Context, pluginManifestPaths []string) (server.RoutedSessionHandle, error) {
	m.mu.Lock()
	if m.detached || m.shuttingDown {
		m.mu.Unlock()
		return nil, errors.New("Experimental server is shutting down")
	}
	existing := m.workersBySession[metadata.Path]
	pending := m.pending[metadata.Path]
	m.mu.Unlock()

	if existing != nil {
		if !swmSameStrings(existing.pluginManifestPaths, pluginManifestPaths) {
			return nil, NewSessionPluginSelectionConflictError(
				fmt.Sprintf("Session %s is active with a different plugin selection", metadata.Id),
			)
		}
		return m.routedHandle(existing), nil
	}
	if pending != nil {
		if !swmSameStrings(pending.pluginManifestPaths, pluginManifestPaths) {
			return nil, NewSessionPluginSelectionConflictError(
				fmt.Sprintf("Session %s is starting with a different plugin selection", metadata.Id),
			)
		}
		<-pending.doneCh
		if pending.err != nil {
			return nil, pending.err
		}
		return m.routedHandle(pending.worker), nil
	}

	worker, err := m.launch(metadata, ctx, pluginManifestPaths)
	if err != nil {
		return nil, err
	}
	return m.routedHandle(worker), nil
}

// CloseSession gracefully stops any running or pending worker for metadata.
func (m *SessionWorkerManager) CloseSession(metadata SessionCatalogMetadata, ctx chord.Context) error {
	m.mu.Lock()
	worker := m.workersBySession[metadata.Path]
	pending := m.pending[metadata.Path]
	m.mu.Unlock()

	if worker == nil && pending != nil {
		<-pending.doneCh
		worker = pending.worker
	}
	if worker != nil {
		return m.stopWorker(worker, ctx)
	}
	return nil
}

type swmRoutedHandle struct {
	manager *SessionWorkerManager
	worker  *swmWorkerRecord
}

func (m *SessionWorkerManager) routedHandle(worker *swmWorkerRecord) server.RoutedSessionHandle {
	return &swmRoutedHandle{manager: m, worker: worker}
}

func (h *swmRoutedHandle) Terminated() <-chan error {
	return h.worker.terminatedCh
}

func (h *swmRoutedHandle) AttachClient(ctx chord.Context) (server.RoutedSessionAttachment, error) {
	return h.manager.attachClient(h.worker, ctx)
}

func (h *swmRoutedHandle) Close(ctx chord.Context) error {
	return h.manager.stopWorker(h.worker, ctx)
}

type swmRoutedAttachment struct {
	manager *SessionWorkerManager
	worker  *swmWorkerRecord
	scope   WorkerOperationScope
	mu      sync.Mutex
	rel     bool
}

func (m *SessionWorkerManager) attachClient(worker *swmWorkerRecord, ctx chord.Context) (server.RoutedSessionAttachment, error) {
	m.mu.Lock()
	if m.detached || m.shuttingDown {
		m.mu.Unlock()
		return nil, errors.New("Experimental server is shutting down")
	}
	m.mu.Unlock()

	worker.mu.Lock()
	stopping := worker.stopping
	worker.mu.Unlock()
	if stopping {
		return nil, errors.New("Experimental Session worker is stopping")
	}

	m.mu.Lock()
	curByPeer := m.workersByPeer[worker.peerId]
	if curByPeer != worker {
		m.mu.Unlock()
		return nil, errors.New("Experimental Session worker is no longer available")
	}
	attachmentId := uuid.NewString()
	worker.mu.Lock()
	worker.attachmentIds[attachmentId] = struct{}{}
	worker.mu.Unlock()
	m.mu.Unlock()

	if err := m.applyDemand(worker, attachmentId, true, true, ctx); err != nil {
		worker.mu.Lock()
		delete(worker.attachmentIds, attachmentId)
		worker.mu.Unlock()
		return nil, err
	}

	scope, err := m.operationScope(worker, attachmentId)
	if err != nil {
		_ = m.applyDemand(worker, attachmentId, false, true, chord.BackgroundContext)
		worker.mu.Lock()
		delete(worker.attachmentIds, attachmentId)
		worker.mu.Unlock()
		return nil, err
	}
	return &swmRoutedAttachment{
		manager: m,
		worker:  worker,
		scope:   scope,
	}, nil
}

func (a *swmRoutedAttachment) InvokeService(call chord.ServiceCall, publish chord.ServiceUpdatePublisher, ctx chord.Context) (chord.JsonValue, bool, error) {
	return a.manager.invokeService(a.worker, a.scope, call, publish, ctx)
}

func (a *swmRoutedAttachment) Release(ctx chord.Context) error {
	a.mu.Lock()
	if a.rel {
		a.mu.Unlock()
		return nil
	}
	a.rel = true
	a.mu.Unlock()

	a.manager.mu.Lock()
	detached := a.manager.detached
	a.manager.mu.Unlock()

	a.worker.mu.Lock()
	_, hasAtt := a.worker.attachmentIds[a.scope.AttachmentId]
	a.worker.mu.Unlock()
	if detached || !hasAtt {
		return nil
	}

	err := a.manager.applyDemand(a.worker, a.scope.AttachmentId, false, true, ctx)

	a.worker.mu.Lock()
	delete(a.worker.attachmentIds, a.scope.AttachmentId)
	a.worker.mu.Unlock()

	a.manager.mu.Lock()
	detachedNow := a.manager.detached
	a.manager.removeServiceSubscriptionsLocked(func(entry *swmServiceSubscription) bool {
		return entry.worker == a.worker && swmSameScope(entry.scope, a.scope)
	})
	a.manager.mu.Unlock()

	if err != nil && !detachedNow && !a.manager.coordinator.WasReplaced() {
		return err
	}
	return nil
}

func (m *SessionWorkerManager) invokeService(
	worker *swmWorkerRecord,
	scope WorkerOperationScope,
	call chord.ServiceCall,
	publish chord.ServiceUpdatePublisher,
	ctx chord.Context,
) (chord.JsonValue, bool, error) {
	control := chord.DecodeServiceControlCall(call)
	var addedSubKey string
	if subCall, isSub := control.(chord.ServiceControlCallSubscribe); isSub {
		key := swmScopedServiceSubscriptionKey(scope, subCall.SubscriptionId)
		m.mu.Lock()
		if m.serviceSubscriptions[key] != nil {
			m.mu.Unlock()
			return nil, false, errors.New("Service subscription ID is already active")
		}
		m.serviceSubscriptions[key] = swmNewServiceSubscription(worker, scope, func(update chord.ServiceProviderUpdate, updateCtx chord.Context) error {
			if publish == nil {
				return nil
			}
			return publish(subCall.SubscriptionId, update, updateCtx)
		})
		m.mu.Unlock()
		addedSubKey = key
	}

	resp, hasResult, err := m.invoke(worker, scope, call, ctx)
	if err != nil {
		if addedSubKey != "" {
			if subCall, isSub := control.(chord.ServiceControlCallSubscribe); isSub {
				m.mu.Lock()
				if sub := m.serviceSubscriptions[addedSubKey]; sub != nil {
					sub.stop()
					delete(m.serviceSubscriptions, addedSubKey)
				}
				m.mu.Unlock()
				go func() {
					_, _, _ = m.invoke(worker, scope, chord.CreateServiceUnsubscribeCall(subCall.SubscriptionId), chord.BackgroundContext)
				}()
			}
		}
		return nil, false, err
	}
	if unsubCall, isUnsub := control.(chord.ServiceControlCallUnsubscribe); isUnsub {
		key := swmScopedServiceSubscriptionKey(scope, unsubCall.SubscriptionId)
		m.mu.Lock()
		if sub := m.serviceSubscriptions[key]; sub != nil {
			sub.stop()
			delete(m.serviceSubscriptions, key)
		}
		m.mu.Unlock()
	}
	return resp, hasResult, nil
}

func (m *SessionWorkerManager) applyDemand(
	worker *swmWorkerRecord,
	attachmentId string,
	attached bool,
	compensateOnTimeout bool,
	_ chord.Context,
) error {
	worker.mu.Lock()
	stopping := worker.stopping
	worker.mu.Unlock()

	m.mu.Lock()
	activePeer := m.workersByPeer[worker.peerId] == worker
	if stopping || !activePeer {
		m.mu.Unlock()
		return errors.New("Experimental Session worker is stopping")
	}
	requestId := uuid.NewString()
	doneCh := make(chan error, 1)
	pend := &swmPendingDemand{
		attachmentId: attachmentId,
		attached:     attached,
		requestId:    requestId,
		worker:       worker,
		doneCh:       doneCh,
	}
	pend.timer = time.AfterFunc(m.demandTimeout, func() {
		if compensateOnTimeout {
			go m.reconcileDemandTimeout(requestId)
		} else {
			m.rejectDemand(requestId, errors.New("Session worker demand update timed out"))
		}
	})
	m.pendingDemand[requestId] = pend
	m.mu.Unlock()

	sendErr := m.coordinator.Send(worker.peerId, jsonx.ObjectOf(
		"type", "session_demand",
		"serverConnectionId", m.coordinator.ServerConnectionId(),
		"requestId", requestId,
		"attachmentId", attachmentId,
		"attached", attached,
	))
	if sendErr != nil {
		m.rejectDemand(requestId, sendErr)
	}
	return <-doneCh
}

func (m *SessionWorkerManager) invoke(worker *swmWorkerRecord, scope WorkerOperationScope, call chord.ServiceCall, ctx chord.Context) (chord.JsonValue, bool, error) {
	if _, err := m.operationScope(worker, scope.AttachmentId); err != nil {
		return nil, false, err
	}
	var abortSig context.Context
	if ctx != nil {
		abortSig = ctx.AbortSignal()
	}
	if abortSig != nil && abortSig.Err() != nil {
		return nil, false, abortSig.Err()
	}

	requestId := uuid.NewString()
	doneCh := make(chan swmOperationResult, 1)

	stopWatch := make(chan struct{})
	var stopOnce sync.Once
	cleanup := func() {
		stopOnce.Do(func() {
			close(stopWatch)
		})
	}

	m.mu.Lock()
	if m.detached || m.shuttingDown {
		m.mu.Unlock()
		return nil, false, errors.New("Experimental Session worker is stopping")
	}
	m.pendingOperations[requestId] = &swmPendingOperation{
		worker:  worker,
		scope:   scope,
		cleanup: cleanup,
		doneCh:  doneCh,
	}
	m.mu.Unlock()

	if abortSig != nil {
		go func() {
			select {
			case <-stopWatch:
				return
			case <-abortSig.Done():
				m.mu.Lock()
				_, exists := m.pendingOperations[requestId]
				m.mu.Unlock()
				if !exists {
					return
				}
				_ = m.coordinator.Send(worker.peerId, jsonx.ObjectOf(
					"type", "operation_cancel",
					"requestId", requestId,
					"scope", swScopeObject(scope),
				))
				err := abortSig.Err()
				if err == nil {
					err = context.Canceled
				}
				m.rejectOperation(requestId, err)
			}
		}()
	}

	if call.Args == nil {
		call.Args = []chord.JsonValue{}
	}
	if err := m.coordinator.Send(worker.peerId, jsonx.ObjectOf(
		"type", "operation",
		"requestId", requestId,
		"scope", swScopeObject(scope),
		"call", call,
	)); err != nil {
		m.rejectOperation(requestId, err)
	}

	res := <-doneCh
	return res.value, res.hasValue, res.err
}

func (m *SessionWorkerManager) operationScope(worker *swmWorkerRecord, attachmentId string) (WorkerOperationScope, error) {
	worker.mu.Lock()
	stopping := worker.stopping
	_, hasAtt := worker.attachmentIds[attachmentId]
	worker.mu.Unlock()

	m.mu.Lock()
	detached := m.detached
	shuttingDown := m.shuttingDown
	peerMatch := m.workersByPeer[worker.peerId] == worker
	m.mu.Unlock()

	if detached || shuttingDown || stopping {
		return WorkerOperationScope{}, errors.New("Experimental Session worker is stopping")
	}
	if !peerMatch || !hasAtt {
		return WorkerOperationScope{}, errors.New("Experimental Session worker has no active attachment")
	}
	return WorkerOperationScope{
		ServerConnectionId: m.coordinator.ServerConnectionId(),
		AttachmentId:       attachmentId,
	}, nil
}

func (m *SessionWorkerManager) stopWorker(worker *swmWorkerRecord, _ chord.Context) error {
	m.mu.Lock()
	detached := m.detached
	peerMatch := m.workersByPeer[worker.peerId] == worker
	m.mu.Unlock()
	if detached || !peerMatch {
		return nil
	}

	worker.stopOnce.Do(func() {
		defer close(worker.stopDone)
		m.mu.Lock()
		m.rejectWorkerOperationsLocked(worker, errors.New("Session worker is stopping"))
		m.mu.Unlock()

		worker.mu.Lock()
		worker.stopping = true
		worker.expectedStop = true
		worker.mu.Unlock()

		_ = m.coordinator.Send(worker.peerId, jsonx.ObjectOf("type", "shutdown"))

		timer := time.NewTimer(m.shutdownTimeout)
		defer timer.Stop()
		select {
		case <-worker.terminatedCh:
		case <-timer.C:
			if worker.pid > 0 {
				_ = m.killPid(worker.pid)
			}
			m.removeWorkerAndNotify(worker, nil)
			<-worker.terminatedCh
		}
	})
	<-worker.stopDone
	return nil
}

// Shutdown gracefully terminates all pending and active session workers owned by this server.
func (m *SessionWorkerManager) Shutdown() error {
	m.mu.Lock()
	if m.detached || m.shuttingDown {
		m.mu.Unlock()
		return nil
	}
	m.shuttingDown = true
	pendingList := make([]*swmPendingLaunch, 0, len(m.pending))
	for _, p := range m.pending {
		pendingList = append(pendingList, p)
	}
	workersList := make([]*swmWorkerRecord, 0, len(m.workersBySession))
	for _, w := range m.workersBySession {
		workersList = append(workersList, w)
	}
	m.mu.Unlock()

	for _, p := range pendingList {
		_ = m.coordinator.Send(p.peerId, jsonx.ObjectOf("type", "shutdown"))
	}

	var wg sync.WaitGroup
	pendingDone := make(chan struct{})
	go func() {
		for _, p := range pendingList {
			if p.child != nil {
				<-p.child.Done()
			}
		}
		close(pendingDone)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTimer(m.shutdownTimeout)
		defer timer.Stop()
		select {
		case <-pendingDone:
		case <-timer.C:
			for _, p := range pendingList {
				if p.child != nil {
					_ = TerminateInternalProcess(p.child)
				}
			}
			<-pendingDone
		}
	}()

	for _, w := range workersList {
		worker := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.stopWorker(worker, chord.BackgroundContext)
		}()
	}
	wg.Wait()
	m.detachState()
	return nil
}

// Detach forgets tracked workers without terminating them when this server is superseded.
func (m *SessionWorkerManager) Detach() {
	m.mu.Lock()
	if m.detached {
		m.mu.Unlock()
		return
	}
	m.detached = true
	for _, p := range m.pending {
		if !p.settled {
			p.settled = true
			if p.timer != nil {
				p.timer.Stop()
			}
			p.err = errors.New("Experimental server was replaced")
			close(p.doneCh)
		}
	}
	for reqId := range m.pendingOperations {
		m.rejectOperationLocked(reqId, errors.New("Experimental server was replaced during a worker operation"))
	}
	m.mu.Unlock()
	m.detachState()
}

func (m *SessionWorkerManager) launch(metadata SessionCatalogMetadata, _ chord.Context, pluginManifestPaths []string) (*swmWorkerRecord, error) {
	sessionKey := metadata.Path
	peerId := "worker-" + uuid.NewString()
	token := uuid.NewString()
	manifestsCopy := make([]string, len(pluginManifestPaths))
	copy(manifestsCopy, pluginManifestPaths)

	opts := SessionWorkerOptions{
		SessionDir:          m.sessionDir,
		Metadata:            metadata,
		PluginManifestPaths: manifestsCopy,
	}
	if opts.PluginManifestPaths == nil {
		opts.PluginManifestPaths = []string{}
	}
	if m.model != nil {
		opts.Provider = m.model.Provider
		opts.Model = m.model.Model
	}
	encodedOpts, err := jsonx.Marshal(opts)
	if err != nil {
		return nil, err
	}

	child, err := SpawnInternalProcess(InternalRoleSessionWorker, []string{string(encodedOpts)}, &InternalProcessSpawnOptions{
		Env: map[string]string{
			SessionWorkerControlAddressEnv: m.coordinator.ControlPath(),
			SessionWorkerControlTokenEnv:   token,
			SessionWorkerSessionKeyEnv:     base64.RawURLEncoding.EncodeToString([]byte(sessionKey)),
			SessionWorkerPeerIdEnv:         peerId,
		},
	})
	if err != nil {
		return nil, err
	}

	doneCh := make(chan struct{})
	pend := &swmPendingLaunch{
		sessionKey:          sessionKey,
		peerId:              peerId,
		token:               token,
		pluginManifestPaths: manifestsCopy,
		child:               child,
		doneCh:              doneCh,
	}

	m.mu.Lock()
	if m.detached || m.shuttingDown {
		m.mu.Unlock()
		_ = TerminateInternalProcess(child)
		return nil, errors.New("Experimental server is shutting down")
	}
	pend.timer = time.AfterFunc(m.startupTimeout, func() {
		m.failPending(sessionKey, errors.New("Session worker startup timed out"))
	})
	m.pending[sessionKey] = pend
	count := len(m.workersBySession) + len(m.pending)
	m.mu.Unlock()
	m.notifyWorkerCountChanged(count)

	go func() {
		<-child.Done()
		m.childExited(pend, child.ExitError())
	}()

	<-doneCh
	if pend.err != nil {
		return nil, pend.err
	}
	return pend.worker, nil
}

func (m *SessionWorkerManager) handleCoordinatorEvent(event CoordinatorConnectionEvent) {
	m.mu.Lock()
	if m.detached {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	if event.Type == "peer_disconnected" {
		m.mu.Lock()
		m.markDiscoveredLocked(event.PeerId)
		worker := m.workersByPeer[event.PeerId]
		pend := m.pendingPeerLocked(event.PeerId)
		m.mu.Unlock()

		if worker != nil {
			worker.mu.Lock()
			expected := worker.expectedStop
			worker.mu.Unlock()
			var discErr error
			if !expected {
				discErr = fmt.Errorf("Session worker %s disconnected unexpectedly", worker.metadata.Id)
			}
			m.removeWorkerAndNotify(worker, discErr)
		}
		if pend != nil {
			m.failPending(pend.sessionKey, errors.New("Session worker disconnected during startup"))
		}
		return
	}

	if event.Type != "message" {
		return
	}
	obj, ok := event.Payload.(*jsonx.Object)
	if !ok {
		return
	}
	typVal, _ := obj.Get("type")
	typ, ok := typVal.(string)
	if !ok {
		return
	}
	tokVal, _ := obj.Get("token")
	keyVal, _ := obj.Get("sessionKey")
	token, tokOk := tokVal.(string)
	sessionKey, keyOk := keyVal.(string)
	if !tokOk || !keyOk {
		if typ == "operation_response" {
			m.mu.Lock()
			worker := m.workersByPeer[event.From]
			if worker != nil {
				m.rejectWorkerOperationsLocked(worker, errors.New("Session worker returned an invalid operation response"))
			}
			m.mu.Unlock()
		}
		return
	}

	switch typ {
	case "worker_failed":
		msgVal, _ := obj.Get("message")
		msgStr, ok := msgVal.(string)
		if !ok {
			return
		}
		m.mu.Lock()
		pend := m.pending[sessionKey]
		match := pend != nil && pend.peerId == event.From && pend.token == token
		m.mu.Unlock()
		if match {
			m.failPending(sessionKey, fmt.Errorf("Session worker failed: %s", msgStr))
		}

	case "demand_applied", "demand_rejected":
		reqVal, _ := obj.Get("requestId")
		reqId, ok := reqVal.(string)
		if !ok {
			return
		}
		var attId string
		var attached bool
		var errMsg string
		if typ == "demand_applied" {
			attVal, _ := obj.Get("attachmentId")
			attachedVal, _ := obj.Get("attached")
			a1, ok1 := attVal.(string)
			a2, ok2 := attachedVal.(bool)
			if !ok1 || !ok2 {
				return
			}
			attId, attached = a1, a2
		} else {
			msgVal, _ := obj.Get("message")
			msgStr, ok1 := msgVal.(string)
			if !ok1 {
				return
			}
			errMsg = msgStr
		}
		m.mu.Lock()
		pend := m.pendingDemand[reqId]
		if pend == nil ||
			pend.worker.peerId != event.From ||
			pend.worker.token != token ||
			pend.worker.metadata.Path != sessionKey ||
			(typ == "demand_applied" && (pend.attachmentId != attId || pend.attached != attached)) {
			m.mu.Unlock()
			return
		}
		delete(m.pendingDemand, reqId)
		if pend.timer != nil {
			pend.timer.Stop()
		}
		var err error
		if typ == "demand_rejected" {
			err = fmt.Errorf("Session worker rejected demand: %s", errMsg)
		}
		if !pend.settled {
			pend.settled = true
			pend.doneCh <- err
		}
		m.mu.Unlock()

	case "operation_response":
		respVal, hasResp := obj.Get("response")
		if !hasResp {
			m.mu.Lock()
			worker := m.workersByPeer[event.From]
			if worker != nil {
				m.rejectWorkerOperationsLocked(worker, errors.New("Session worker returned an invalid operation response"))
			}
			m.mu.Unlock()
			return
		}
		m.handleOperationResponse(event.From, token, sessionKey, respVal)

	case "service_update":
		scopeVal, _ := obj.Get("scope")
		subVal, _ := obj.Get("subscriptionId")
		updateVal, hasUpd := obj.Get("update")
		scope, ok1 := swParseScope(scopeVal)
		subId, ok2 := subVal.(string)
		if !ok1 || !ok2 || subId == "" || !hasUpd {
			return
		}
		m.handleServiceEvent(event.From, token, sessionKey, scope, subId, updateVal)

	case "worker_ready":
		m.handleWorkerReady(event.From, token, sessionKey, obj)
	}
}

func (m *SessionWorkerManager) handleOperationResponse(peerId string, token string, sessionKey string, respVal chord.JsonValue) {
	rejectInvalid := func() {
		m.mu.Lock()
		worker := m.workersByPeer[peerId]
		if worker != nil {
			m.rejectWorkerOperationsLocked(worker, errors.New("Session worker returned an invalid operation response"))
		}
		m.mu.Unlock()
	}

	respObj, ok := respVal.(*jsonx.Object)
	if !ok {
		rejectInvalid()
		return
	}
	typVal, _ := respObj.Get("type")
	reqVal, _ := respObj.Get("requestId")
	scopeVal, _ := respObj.Get("scope")
	typ, ok1 := typVal.(string)
	reqId, ok2 := reqVal.(string)
	scope, ok3 := swParseScope(scopeVal)
	if !ok1 || !ok2 || reqId == "" || !ok3 || (typ != "operation_error" && typ != "operation_result") {
		rejectInvalid()
		return
	}

	m.mu.Lock()
	pend := m.pendingOperations[reqId]
	if pend == nil {
		m.mu.Unlock()
		return
	}
	if pend.worker.peerId != peerId ||
		pend.worker.token != token ||
		pend.worker.metadata.Path != sessionKey ||
		!swmSameScope(scope, pend.scope) {
		m.rejectOperationLocked(reqId, errors.New("Session worker returned a mismatched operation response"))
		m.mu.Unlock()
		return
	}

	switch typ {
	case "operation_error":
		msgVal, _ := respObj.Get("message")
		msgStr, okMsg := msgVal.(string)
		if !okMsg {
			m.rejectOperationLocked(reqId, errors.New("Session worker returned an invalid operation response"))
			m.mu.Unlock()
			return
		}
		delete(m.pendingOperations, reqId)
		m.mu.Unlock()
		pend.cleanup()

		codeVal, hasCode := respObj.Get("code")
		var retErr error
		if hasCode {
			if codeStr, okCode := codeVal.(string); okCode && chord.IsRemoteServiceErrorCode(codeStr) {
				retErr = chord.NewRemoteServiceError(chord.RemoteServiceErrorCode(codeStr), msgStr)
			}
		}
		if retErr == nil {
			retErr = fmt.Errorf("Session worker operation failed: %s", msgStr)
		}
		select {
		case pend.doneCh <- swmOperationResult{err: retErr}:
		default:
		}

	case "operation_result":
		resultVal, hasResult := respObj.Get("result")
		delete(m.pendingOperations, reqId)
		m.mu.Unlock()
		pend.cleanup()
		select {
		case pend.doneCh <- swmOperationResult{hasValue: hasResult, value: resultVal}:
		default:
		}
	}
}

func (m *SessionWorkerManager) handleServiceEvent(peerId string, token string, sessionKey string, scope WorkerOperationScope, subscriptionId string, update chord.JsonValue) {
	key := swmScopedServiceSubscriptionKey(scope, subscriptionId)
	m.mu.Lock()
	entry := m.serviceSubscriptions[key]
	m.mu.Unlock()

	if entry == nil ||
		entry.worker.peerId != peerId ||
		entry.worker.token != token ||
		entry.worker.metadata.Path != sessionKey ||
		!swmSameScope(entry.scope, scope) {
		return
	}
	parsed, err := chord.ParseServiceProviderUpdate(update)
	if err != nil {
		return
	}
	entry.enqueue(parsed)
}

func (m *SessionWorkerManager) handleWorkerReady(peerId string, token string, sessionKey string, obj *jsonx.Object) {
	sidVal, _ := obj.Get("sessionId")
	pidVal, _ := obj.Get("pid")
	metaVal, _ := obj.Get("metadata")
	manVal, _ := obj.Get("pluginManifestPaths")

	sessionId, ok1 := sidVal.(string)
	pidInt, ok2 := coorAsInt(pidVal)
	if !ok1 || !ok2 || pidInt < 1 {
		return
	}
	metaRaw, err := jsonx.Marshal(metaVal)
	if err != nil {
		return
	}
	var metadata SessionCatalogMetadata
	if err := jsonx.Unmarshal(metaRaw, &metadata); err != nil {
		return
	}
	manRaw, err := jsonx.Marshal(manVal)
	if err != nil {
		return
	}
	var manifests []string
	if err := jsonx.Unmarshal(manRaw, &manifests); err != nil {
		return
	}
	for _, p := range manifests {
		if p == "" {
			return
		}
	}
	if sessionKey != metadata.Path ||
		sessionId != metadata.Id ||
		!filepath.IsAbs(metadata.Cwd) ||
		!filepath.IsAbs(metadata.Path) {
		return
	}

	m.mu.Lock()
	m.markDiscoveredLocked(peerId)
	pend := m.pending[sessionKey]
	if pend != nil && pend.peerId == peerId && !swmSameStrings(manifests, pend.pluginManifestPaths) {
		m.mu.Unlock()
		m.failPending(sessionKey, errors.New("Session worker started with stale plugin packages"))
		return
	}
	existing := m.workersBySession[sessionKey]
	if existing != nil && existing.peerId != peerId {
		m.mu.Unlock()
		_ = m.coordinator.Send(peerId, jsonx.ObjectOf("type", "shutdown"))
		return
	}
	if pend != nil && (pend.peerId != peerId || pend.token != token || (pend.child != nil && pend.child.Pid() != pidInt)) {
		m.mu.Unlock()
		return
	}
	if existing != nil {
		m.mu.Unlock()
		return
	}

	worker := swmNewWorkerRecord(peerId, metadata, pidInt, token, manifests)
	m.workersBySession[sessionKey] = worker
	m.workersByPeer[peerId] = worker
	m.workerPids[sessionId] = pidInt
	if pend != nil {
		delete(m.pending, sessionKey)
		if !pend.settled {
			pend.settled = true
			if pend.timer != nil {
				pend.timer.Stop()
			}
			pend.worker = worker
			close(pend.doneCh)
		}
	}
	count := len(m.workersBySession) + len(m.pending)
	m.mu.Unlock()
	m.notifyWorkerCountChanged(count)
}

func (m *SessionWorkerManager) childExited(pend *swmPendingLaunch, exitErr error) {
	m.mu.Lock()
	stillPending := m.pending[pend.sessionKey] == pend
	worker := m.workersByPeer[pend.peerId]
	m.mu.Unlock()

	reason := "clean"
	if exitErr != nil {
		reason = exitErr.Error()
	}
	if stillPending {
		m.failPending(pend.sessionKey, fmt.Errorf("Session worker exited before readiness (%s)", reason))
		return
	}
	if worker != nil {
		worker.mu.Lock()
		expected := worker.expectedStop
		worker.mu.Unlock()
		var termErr error
		if !expected {
			termErr = fmt.Errorf("Session worker %s exited unexpectedly (%s)", worker.metadata.Id, reason)
		}
		m.removeWorkerAndNotify(worker, termErr)
	}
}

func (m *SessionWorkerManager) failPending(sessionKey string, err error) {
	m.mu.Lock()
	pend := m.pending[sessionKey]
	if pend == nil {
		m.mu.Unlock()
		return
	}
	delete(m.pending, sessionKey)
	if !pend.settled {
		pend.settled = true
		if pend.timer != nil {
			pend.timer.Stop()
		}
		pend.err = err
		close(pend.doneCh)
	}
	child := pend.child
	count := len(m.workersBySession) + len(m.pending)
	m.mu.Unlock()

	m.notifyWorkerCountChanged(count)
	if child != nil && !child.Exited() {
		_ = TerminateInternalProcess(child)
	}
}

func (m *SessionWorkerManager) rejectDemand(requestId string, err error) {
	m.mu.Lock()
	pend := m.pendingDemand[requestId]
	if pend == nil {
		m.mu.Unlock()
		return
	}
	delete(m.pendingDemand, requestId)
	if pend.timer != nil {
		pend.timer.Stop()
	}
	if !pend.settled {
		pend.settled = true
		pend.doneCh <- err
	}
	m.mu.Unlock()
}

func (m *SessionWorkerManager) rejectOperation(requestId string, err error) {
	m.mu.Lock()
	pend := m.rejectOperationLocked(requestId, err)
	m.mu.Unlock()
	if pend != nil {
		pend.cleanup()
	}
}

func (m *SessionWorkerManager) rejectOperationLocked(requestId string, err error) *swmPendingOperation {
	pend := m.pendingOperations[requestId]
	if pend == nil {
		return nil
	}
	delete(m.pendingOperations, requestId)
	select {
	case pend.doneCh <- swmOperationResult{err: err}:
	default:
	}
	return pend
}

func (m *SessionWorkerManager) rejectWorkerOperationsLocked(worker *swmWorkerRecord, err error) {
	var cleanups []func()
	for reqId, pend := range m.pendingOperations {
		if pend.worker == worker {
			delete(m.pendingOperations, reqId)
			select {
			case pend.doneCh <- swmOperationResult{err: err}:
			default:
			}
			cleanups = append(cleanups, pend.cleanup)
		}
	}
	if len(cleanups) > 0 {
		go func() {
			for _, fn := range cleanups {
				fn()
			}
		}()
	}
}

func (m *SessionWorkerManager) reconcileDemandTimeout(requestId string) {
	m.mu.Lock()
	pend := m.pendingDemand[requestId]
	if pend == nil {
		m.mu.Unlock()
		return
	}
	delete(m.pendingDemand, requestId)
	if pend.timer != nil {
		pend.timer.Stop()
	}
	m.mu.Unlock()

	timeoutErr := errors.New("Session worker demand update timed out")
	cleanupErr := m.applyDemand(pend.worker, pend.attachmentId, false, false, chord.BackgroundContext)
	if cleanupErr == nil {
		if !pend.settled {
			pend.settled = true
			if pend.attached {
				pend.doneCh <- timeoutErr
			} else {
				pend.doneCh <- nil
			}
		}
		return
	}

	if stopErr := m.stopWorker(pend.worker, chord.BackgroundContext); stopErr != nil {
		if !pend.settled {
			pend.settled = true
			pend.doneCh <- fmt.Errorf("Session worker demand reconciliation and termination failed: %w", errors.Join(timeoutErr, cleanupErr, stopErr))
		}
		return
	}
	if !pend.settled {
		pend.settled = true
		pend.doneCh <- fmt.Errorf("Session worker demand reconciliation failed; worker was terminated: %w", errors.Join(timeoutErr, cleanupErr))
	}
}

func (m *SessionWorkerManager) removeWorkerAndNotify(worker *swmWorkerRecord, termErr error) {
	m.mu.Lock()
	if m.workersByPeer[worker.peerId] != worker {
		m.mu.Unlock()
		return
	}
	m.rejectWorkerOperationsLocked(worker, errors.New("Session worker disconnected during an operation"))
	m.removeServiceSubscriptionsLocked(func(entry *swmServiceSubscription) bool {
		return entry.worker == worker
	})
	for reqId, pend := range m.pendingDemand {
		if pend.worker == worker {
			delete(m.pendingDemand, reqId)
			if pend.timer != nil {
				pend.timer.Stop()
			}
			if !pend.settled {
				pend.settled = true
				pend.doneCh <- errors.New("Session worker disconnected during demand update")
			}
		}
	}
	delete(m.workersByPeer, worker.peerId)
	delete(m.workersBySession, worker.metadata.Path)
	if m.workerPids[worker.metadata.Id] == worker.pid {
		delete(m.workerPids, worker.metadata.Id)
	}
	count := len(m.workersBySession) + len(m.pending)
	m.mu.Unlock()

	worker.finishTerminated(termErr)
	m.notifyWorkerCountChanged(count)
}

func (m *SessionWorkerManager) removeServiceSubscriptionsLocked(matches func(entry *swmServiceSubscription) bool) {
	for key, entry := range m.serviceSubscriptions {
		if matches(entry) {
			entry.stop()
			delete(m.serviceSubscriptions, key)
		}
	}
}

func (m *SessionWorkerManager) notifyWorkerCountChanged(count int) {
	if m.onWorkerCountChanged != nil {
		m.onWorkerCountChanged(count)
	}
}

func (m *SessionWorkerManager) pendingPeerLocked(peerId string) *swmPendingLaunch {
	for _, p := range m.pending {
		if p.peerId == peerId {
			return p
		}
	}
	return nil
}

func (m *SessionWorkerManager) markDiscoveredLocked(peerId string) {
	if m.discoveryPeers == nil {
		return
	}
	delete(m.discoveryPeers, peerId)
	if len(m.discoveryPeers) == 0 && m.discoveryCh != nil {
		ch := m.discoveryCh
		m.discoveryCh = nil
		close(ch)
	}
}

func (m *SessionWorkerManager) detachState() {
	if m.removeListener != nil {
		m.removeListener()
	}
	m.mu.Lock()
	for reqId := range m.pendingOperations {
		m.rejectOperationLocked(reqId, errors.New("Experimental server detached during a worker operation"))
	}
	for reqId, pend := range m.pendingDemand {
		delete(m.pendingDemand, reqId)
		if pend.timer != nil {
			pend.timer.Stop()
		}
		if !pend.settled {
			pend.settled = true
			pend.doneCh <- nil
		}
	}
	clear(m.pending)
	for key, sub := range m.serviceSubscriptions {
		sub.stop()
		delete(m.serviceSubscriptions, key)
	}
	clear(m.workersByPeer)
	clear(m.workersBySession)
	clear(m.workerPids)
	m.discoveryPeers = nil
	if m.discoveryCh != nil {
		close(m.discoveryCh)
		m.discoveryCh = nil
	}
	m.mu.Unlock()
}

func swmSameStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func swmSameScope(left WorkerOperationScope, right WorkerOperationScope) bool {
	return left.ServerConnectionId == right.ServerConnectionId && left.AttachmentId == right.AttachmentId
}

func swmScopedServiceSubscriptionKey(scope WorkerOperationScope, subscriptionId string) string {
	return scope.ServerConnectionId + "\x00" + scope.AttachmentId + "\x00" + subscriptionId
}
