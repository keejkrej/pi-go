// Ported from packages/coding-agent/src/experimental/session-worker.ts (pi v1.0.0).

package experimental

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/experimental/durableagent"
	"github.com/keejkrej/pi-go/codingagent/experimental/plugins"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/lockfile"
)

const (
	// SessionWorkerControlAddressEnv specifies the coordinator control Unix socket path for a session worker.
	SessionWorkerControlAddressEnv = "PI_SESSION_WORKER_CONTROL_ADDRESS"
	// SessionWorkerControlTokenEnv holds the per-worker authorization token expected by the spawning server.
	SessionWorkerControlTokenEnv = "PI_SESSION_WORKER_CONTROL_TOKEN"
	// SessionWorkerSessionKeyEnv carries the base64url-encoded canonical session storage key.
	SessionWorkerSessionKeyEnv = "PI_SESSION_WORKER_SESSION_KEY_BASE64"
	// SessionWorkerPeerIdEnv identifies this session worker peer to the coordinator.
	SessionWorkerPeerIdEnv = "PI_SESSION_WORKER_PEER_ID"

	// SessionWorkerInitialDemandGraceEnv overrides initial attachment demand grace duration (ms) in tests.
	SessionWorkerInitialDemandGraceEnv = "__PI_SESSION_WORKER_INITIAL_DEMAND_GRACE_MS"
	// SessionWorkerOrphanDemandGraceEnv overrides orphaned attachment demand grace duration (ms) in tests.
	SessionWorkerOrphanDemandGraceEnv = "__PI_SESSION_WORKER_ORPHAN_DEMAND_GRACE_MS"

	swDefaultInitialDemandGraceMs = 10_000
	swDefaultOrphanDemandGraceMs  = 30_000
)

// SessionWorkerOptions defines startup arguments passed to an internal session worker process.
type SessionWorkerOptions struct {
	SessionDir          string                 `json:"sessionDir"`
	Metadata            SessionCatalogMetadata `json:"metadata"`
	Provider            string                 `json:"provider,omitzero"`
	Model               string                 `json:"model,omitzero"`
	PluginManifestPaths []string               `json:"pluginManifestPaths"`
}

// WorkerOperationScope identifies the server connection and attachment owning a worker service call or subscription.
type WorkerOperationScope = services.WorkerServiceScope

// CreateSessionWorkerHarness opens the durable Harness and root conversation under an active session lock.
type CreateSessionWorkerHarness func(databasePath string, options SessionWorkerOptions) (*services.SessionWorkerRuntime, error)

// WorkerLifecycleOptions configures WorkerLifecycle grace timers and retirement notification.
type WorkerLifecycleOptions struct {
	InitialServerConnectionId string
	InitialDemandGrace        time.Duration
	OrphanDemandGrace         time.Duration
	OnRetire                  func()
	AfterFunc                 func(d time.Duration, fn func()) func() bool
}

type swDemandRecord struct {
	serverConnectionId string
	attachmentId       string
	timer              func() bool
	timerGen           uint64
}

// WorkerLifecycle reconciles server-generation attachment demand and live Harness activity.
type WorkerLifecycle struct {
	initialDemandGrace time.Duration
	orphanDemandGrace  time.Duration
	onRetire           func()
	afterFunc          func(d time.Duration, fn func()) func() bool

	mu                        sync.Mutex
	demands                   map[string]*swDemandRecord
	harnessActive             bool
	currentServerConnectionId string
	hasCurrentServer          bool
	initialTimer              func() bool
	initialTimerGen           uint64
	demandInitialized         bool
	retirementHolds           int
	retiring                  bool
	closed                    bool
}

// NewWorkerLifecycle initializes a WorkerLifecycle and starts the initial demand grace timer.
func NewWorkerLifecycle(options WorkerLifecycleOptions) *WorkerLifecycle {
	afterFn := options.AfterFunc
	if afterFn == nil {
		afterFn = func(d time.Duration, fn func()) func() bool {
			return time.AfterFunc(d, fn).Stop
		}
	}
	wl := &WorkerLifecycle{
		initialDemandGrace:        options.InitialDemandGrace,
		orphanDemandGrace:         options.OrphanDemandGrace,
		onRetire:                  options.OnRetire,
		afterFunc:                 afterFn,
		demands:                   make(map[string]*swDemandRecord),
		currentServerConnectionId: options.InitialServerConnectionId,
		hasCurrentServer:          options.InitialServerConnectionId != "",
	}
	wl.mu.Lock()
	wl.initialTimerGen++
	gen := wl.initialTimerGen
	wl.initialTimer = wl.afterFunc(wl.initialDemandGrace, func() {
		wl.mu.Lock()
		if wl.closed || wl.initialTimerGen != gen {
			wl.mu.Unlock()
			return
		}
		wl.initialTimer = nil
		wl.demandInitialized = true
		retire := wl.reconcileLocked()
		wl.mu.Unlock()
		if retire != nil {
			retire()
		}
	})
	wl.mu.Unlock()
	return wl
}

// ServerConnected records a newly active server generation and clears orphan timers for matching demands.
func (wl *WorkerLifecycle) ServerConnected(serverConnectionId string) {
	wl.mu.Lock()
	wl.currentServerConnectionId = serverConnectionId
	wl.hasCurrentServer = true
	for _, demand := range wl.demands {
		if demand.serverConnectionId == serverConnectionId && demand.timer != nil {
			demand.timerGen++
			demand.timer()
			demand.timer = nil
		}
	}
	wl.mu.Unlock()
}

// ServerDisconnected starts orphan grace timers for demands tied to the disconnected server generation.
func (wl *WorkerLifecycle) ServerDisconnected(serverConnectionId string) {
	wl.mu.Lock()
	if wl.hasCurrentServer && wl.currentServerConnectionId == serverConnectionId {
		wl.currentServerConnectionId = ""
		wl.hasCurrentServer = false
	}
	for key, demand := range wl.demands {
		if demand.serverConnectionId != serverConnectionId || demand.timer != nil || wl.closed {
			continue
		}
		demand.timerGen++
		k := key
		d := demand
		gen := demand.timerGen
		demand.timer = wl.afterFunc(wl.orphanDemandGrace, func() {
			wl.mu.Lock()
			if wl.closed || wl.demands[k] != d || d.timerGen != gen {
				wl.mu.Unlock()
				return
			}
			delete(wl.demands, k)
			retire := wl.reconcileLocked()
			wl.mu.Unlock()
			if retire != nil {
				retire()
			}
		})
	}
	wl.mu.Unlock()
}

// BeginRequest verifies active server/attachment demand and holds retirement until the returned release callback runs.
func (wl *WorkerLifecycle) BeginRequest(serverConnectionId string, attachmentId string) (func(), error) {
	wl.mu.Lock()
	if wl.retiring || wl.closed {
		wl.mu.Unlock()
		return nil, errors.New("Session worker is retiring")
	}
	if !wl.hasCurrentServer || serverConnectionId != wl.currentServerConnectionId {
		wl.mu.Unlock()
		return nil, errors.New("Session worker received a request from a stale server generation")
	}
	demand := wl.demands[swDemandKey(serverConnectionId, attachmentId)]
	if demand == nil || demand.timer != nil {
		wl.mu.Unlock()
		return nil, errors.New("Session worker request does not match the active attachment")
	}
	release := wl.holdRetirementLocked()
	wl.mu.Unlock()
	return release, nil
}

// HoldRetirement prevents idle worker retirement until the returned release callback is invoked once.
func (wl *WorkerLifecycle) HoldRetirement() func() {
	wl.mu.Lock()
	release := wl.holdRetirementLocked()
	wl.mu.Unlock()
	return release
}

func (wl *WorkerLifecycle) holdRetirementLocked() func() {
	wl.retirementHolds++
	var released bool
	return func() {
		wl.mu.Lock()
		if released {
			wl.mu.Unlock()
			return
		}
		released = true
		wl.retirementHolds--
		retire := wl.reconcileLocked()
		wl.mu.Unlock()
		if retire != nil {
			retire()
		}
	}
}

// SetDemand applies or clears attachment demand for the active server generation.
func (wl *WorkerLifecycle) SetDemand(serverConnectionId string, attachmentId string, attached bool) error {
	wl.mu.Lock()
	if wl.retiring || wl.closed {
		wl.mu.Unlock()
		return errors.New("Session worker is retiring")
	}
	if !wl.hasCurrentServer || serverConnectionId != wl.currentServerConnectionId {
		wl.mu.Unlock()
		return errors.New("Session worker received demand from a stale server generation")
	}
	wl.demandInitialized = true
	if wl.initialTimer != nil {
		wl.initialTimerGen++
		wl.initialTimer()
		wl.initialTimer = nil
	}
	key := swDemandKey(serverConnectionId, attachmentId)
	if prev := wl.demands[key]; prev != nil && prev.timer != nil {
		prev.timerGen++
		prev.timer()
		prev.timer = nil
	}
	if attached {
		wl.demands[key] = &swDemandRecord{
			serverConnectionId: serverConnectionId,
			attachmentId:       attachmentId,
		}
	} else {
		delete(wl.demands, key)
	}
	retire := wl.reconcileLocked()
	wl.mu.Unlock()
	if retire != nil {
		retire()
	}
	return nil
}

// SetHarnessActive marks whether active Durable Harness tasks keep the worker alive.
func (wl *WorkerLifecycle) SetHarnessActive(active bool) {
	wl.mu.Lock()
	wl.harnessActive = active
	var retire func()
	if !active {
		retire = wl.reconcileLocked()
	}
	wl.mu.Unlock()
	if retire != nil {
		retire()
	}
}

// Close stops all pending timers and disables further retirement triggers.
func (wl *WorkerLifecycle) Close() {
	wl.mu.Lock()
	wl.closed = true
	if wl.initialTimer != nil {
		wl.initialTimerGen++
		wl.initialTimer()
		wl.initialTimer = nil
	}
	for _, demand := range wl.demands {
		if demand.timer != nil {
			demand.timerGen++
			demand.timer()
			demand.timer = nil
		}
	}
	clear(wl.demands)
	wl.mu.Unlock()
}

func (wl *WorkerLifecycle) reconcileLocked() func() {
	if wl.closed ||
		wl.retiring ||
		!wl.demandInitialized ||
		wl.retirementHolds != 0 ||
		wl.harnessActive ||
		len(wl.demands) != 0 {
		return nil
	}
	wl.retiring = true
	return wl.onRetire
}

type swControl struct {
	initialServerConnectionId string
	conn                      net.Conn
	reader                    *bufio.Reader
	writeMu                   sync.Mutex
	queued                    []chord.JsonValue
	msgCh                     chan chord.JsonValue
	errCh                     chan error
}

func swConnectControl() (*swControl, error) {
	address := os.Getenv(SessionWorkerControlAddressEnv)
	token := os.Getenv(SessionWorkerControlTokenEnv)
	encodedSessionKey := os.Getenv(SessionWorkerSessionKeyEnv)
	if address == "" || token == "" || encodedSessionKey == "" {
		return nil, errors.New("Session worker requires a control address")
	}
	peerId := os.Getenv(SessionWorkerPeerIdEnv)
	if peerId == "" {
		return nil, errors.New("Session worker requires a peer ID")
	}

	conn, err := net.Dial("unix", address)
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(conn, 64*1024)
	ctrl := &swControl{
		conn:   conn,
		reader: reader,
		msgCh:  make(chan chord.JsonValue, 64),
		errCh:  make(chan error, 1),
	}

	regPayload := jsonx.ObjectOf(
		"type", "register_peer",
		"protocol", CoordinatorProtocolVersion,
		"peerId", peerId,
	)
	if err := ctrl.writeRaw(regPayload); err != nil {
		_ = conn.Close()
		return nil, err
	}

	line, err := swReadLine(reader)
	if err != nil {
		_ = conn.Close()
		return nil, errors.New("Coordinator rejected the session worker registration")
	}
	val, err := jsonx.Parse(line)
	if err != nil {
		_ = conn.Close()
		return nil, errors.New("Coordinator rejected the session worker registration")
	}
	obj, ok := val.(*jsonx.Object)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("Coordinator rejected the session worker registration")
	}
	typVal, _ := obj.Get("type")
	peerVal, _ := obj.Get("peerId")
	if typStr, _ := typVal.(string); typStr != "peer_registered" {
		_ = conn.Close()
		return nil, errors.New("Coordinator rejected the session worker registration")
	}
	if _, ok := peerVal.(string); !ok {
		_ = conn.Close()
		return nil, errors.New("Coordinator rejected the session worker registration")
	}
	if srvConnVal, hasSrv := obj.Get("serverConnectionId"); hasSrv && srvConnVal != nil {
		srvStr, ok := srvConnVal.(string)
		if !ok {
			_ = conn.Close()
			return nil, errors.New("Coordinator rejected the session worker registration")
		}
		ctrl.initialServerConnectionId = srvStr
	}

	go ctrl.readLoop()
	return ctrl, nil
}

func (c *swControl) readLoop() {
	for {
		line, err := swReadLine(c.reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.errCh <- io.EOF
			} else {
				_ = c.conn.Close()
				c.errCh <- err
			}
			close(c.msgCh)
			return
		}
		val, err := jsonx.Parse(line)
		if err != nil {
			_ = c.conn.Close()
			c.errCh <- errors.New("Session worker received invalid control JSON")
			close(c.msgCh)
			return
		}
		c.msgCh <- val
	}
}

func (c *swControl) writeRaw(message any) error {
	line, err := EncodeControlLine(message)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	_, err = c.conn.Write(line)
	c.writeMu.Unlock()
	return err
}

func (c *swControl) sendEvent(event any) error {
	return c.writeRaw(jsonx.ObjectOf(
		"type", "send",
		"to", "server",
		"payload", event,
	))
}

func (c *swControl) close() {
	_ = c.conn.Close()
}

type swActiveRequest struct {
	scope  WorkerOperationScope
	cancel chord.CancelContext
}

type swRunner struct {
	options       SessionWorkerOptions
	createHarness CreateSessionWorkerHarness
	control       *swControl
	token         string
	sessionKey    string

	releaseOwnership func() error
	runtime          *services.SessionWorkerRuntime
	workerServices   services.SessionWorkerServices
	taskGraph        chord.AttachedReplicatedState[durable.TaskGraph]
	stopActivity     func()
	lifecycle        *WorkerLifecycle

	mu             sync.Mutex
	ready          bool
	closing        bool
	closeOnce      sync.Once
	closeErr       error
	closeDone      chan struct{}
	activeRequests map[string]swActiveRequest
	reqWg          sync.WaitGroup
}

func (r *swRunner) closeResources() error {
	var errs []error
	if r.workerServices != nil {
		if err := r.workerServices.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.runtime != nil {
		if r.runtime.Harness != nil {
			if err := r.runtime.Harness.Close(chord.BackgroundContext); err != nil {
				errs = append(errs, err)
			}
		}
		if r.runtime.ModelRuntime != nil {
			r.runtime.ModelRuntime.Close()
		}
		if r.runtime.Cleanup != nil {
			if err := r.runtime.Cleanup(chord.BackgroundContext); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if r.releaseOwnership != nil {
		if err := r.releaseOwnership(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *swRunner) triggerClose() error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closing = true
		r.ready = false
		reqs := make([]swActiveRequest, 0, len(r.activeRequests))
		for _, req := range r.activeRequests {
			reqs = append(reqs, req)
		}
		clear(r.activeRequests)
		r.mu.Unlock()

		if r.lifecycle != nil {
			r.lifecycle.Close()
		}
		if r.stopActivity != nil {
			r.stopActivity()
		}
		if r.taskGraph != nil {
			r.taskGraph.Dispose()
		}
		if r.workerServices != nil {
			r.workerServices.RemoveSubscriptions(func(WorkerOperationScope) bool { return true })
		}
		cancelErr := errors.New("Session worker is closing")
		for _, req := range reqs {
			req.cancel.Cancel(cancelErr)
		}
		r.reqWg.Wait()
		if r.control != nil {
			r.control.close()
		}
		r.closeErr = r.closeResources()
		close(r.closeDone)
	})
	<-r.closeDone
	return r.closeErr
}

func (r *swRunner) announce() {
	r.mu.Lock()
	ready := r.ready && !r.closing
	r.mu.Unlock()
	if !ready {
		return
	}
	manifests := r.options.PluginManifestPaths
	if manifests == nil {
		manifests = []string{}
	}
	err := r.control.sendEvent(jsonx.ObjectOf(
		"type", "worker_ready",
		"token", r.token,
		"sessionKey", r.sessionKey,
		"sessionId", r.options.Metadata.Id,
		"pid", os.Getpid(),
		"metadata", r.options.Metadata,
		"pluginManifestPaths", manifests,
	))
	if err != nil {
		go func() { _ = r.triggerClose() }()
	}
}

func (r *swRunner) handleCoordinatorInput(val chord.JsonValue) error {
	obj, ok := val.(*jsonx.Object)
	if !ok {
		return errors.New("Coordinator sent an invalid worker message")
	}
	typVal, _ := obj.Get("type")
	typ, ok := typVal.(string)
	if !ok {
		return errors.New("Coordinator sent an invalid worker message")
	}

	switch typ {
	case "peer_registered":
		return nil

	case "server_connected":
		srvVal, _ := obj.Get("serverConnectionId")
		srvId, ok := srvVal.(string)
		if !ok {
			return errors.New("Coordinator sent an invalid worker message")
		}
		r.lifecycle.ServerConnected(srvId)
		return nil

	case "server_disconnected":
		srvVal, _ := obj.Get("serverConnectionId")
		srvId, ok := srvVal.(string)
		if !ok {
			return errors.New("Coordinator sent an invalid worker message")
		}
		matches := func(scope WorkerOperationScope) bool {
			return scope.ServerConnectionId == srvId
		}
		r.workerServices.RemoveSubscriptions(matches)
		r.mu.Lock()
		var toCancel []swActiveRequest
		for _, req := range r.activeRequests {
			if matches(req.scope) {
				toCancel = append(toCancel, req)
			}
		}
		r.mu.Unlock()
		discErr := errors.New("Server disconnected")
		for _, req := range toCancel {
			req.cancel.Cancel(discErr)
		}
		r.lifecycle.ServerDisconnected(srvId)
		return nil

	case "message":
		fromVal, _ := obj.Get("from")
		fromStr, ok := fromVal.(string)
		if !ok || fromStr != "server" {
			return errors.New("Coordinator sent an invalid worker message")
		}
		payload, hasPayload := obj.Get("payload")
		if !hasPayload {
			return nil
		}
		r.handleCommand(payload)
		return nil

	default:
		return errors.New("Coordinator sent an invalid worker message")
	}
}

func (r *swRunner) handleCommand(payload chord.JsonValue) {
	obj, ok := payload.(*jsonx.Object)
	if !ok {
		return
	}
	typVal, _ := obj.Get("type")
	typ, ok := typVal.(string)
	if !ok {
		return
	}

	switch typ {
	case "shutdown":
		go func() { _ = r.triggerClose() }()

	case "discover_workers":
		r.announce()

	case "session_demand":
		srvVal, _ := obj.Get("serverConnectionId")
		reqVal, _ := obj.Get("requestId")
		attVal, _ := obj.Get("attachmentId")
		attachedVal, _ := obj.Get("attached")
		srvId, ok1 := srvVal.(string)
		reqId, ok2 := reqVal.(string)
		attId, ok3 := attVal.(string)
		attached, ok4 := attachedVal.(bool)
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return
		}
		releaseHold := r.lifecycle.HoldRetirement()
		if !attached {
			r.workerServices.RemoveSubscriptions(func(scope WorkerOperationScope) bool {
				return scope.ServerConnectionId == srvId && scope.AttachmentId == attId
			})
		}
		err := r.lifecycle.SetDemand(srvId, attId, attached)
		if err != nil {
			_ = r.control.sendEvent(jsonx.ObjectOf(
				"type", "demand_rejected",
				"token", r.token,
				"sessionKey", r.sessionKey,
				"requestId", reqId,
				"message", err.Error(),
			))
			releaseHold()
			return
		}
		_ = r.control.sendEvent(jsonx.ObjectOf(
			"type", "demand_applied",
			"token", r.token,
			"sessionKey", r.sessionKey,
			"requestId", reqId,
			"attachmentId", attId,
			"attached", attached,
		))
		releaseHold()

	case "operation_cancel":
		reqVal, _ := obj.Get("requestId")
		reqId, ok1 := reqVal.(string)
		scopeVal, _ := obj.Get("scope")
		scope, ok2 := swParseScope(scopeVal)
		if !ok1 || reqId == "" || !ok2 {
			return
		}
		r.mu.Lock()
		active, exists := r.activeRequests[reqId]
		r.mu.Unlock()
		if exists && swSameScope(active.scope, scope) {
			active.cancel.Cancel(context.Canceled)
		}

	case "operation":
		reqVal, _ := obj.Get("requestId")
		reqId, ok1 := reqVal.(string)
		scopeVal, _ := obj.Get("scope")
		scope, ok2 := swParseScope(scopeVal)
		callVal, hasCall := obj.Get("call")
		if !ok1 || reqId == "" || !ok2 || !hasCall {
			return
		}
		call, err := chord.ParseServiceCall(callVal)
		if err != nil {
			if sendErr := r.sendOperationError(reqId, scope, err); sendErr != nil {
				go func() { _ = r.triggerClose() }()
			}
			return
		}
		r.reqWg.Add(1)
		go func() {
			defer r.reqWg.Done()
			r.handleOperation(reqId, scope, call)
		}()
	}
}

func (r *swRunner) handleOperation(requestId string, scope WorkerOperationScope, call chord.ServiceCall) {
	cancellable := chord.WithCancel(chord.BackgroundContext)
	releaseRequest, err := r.lifecycle.BeginRequest(scope.ServerConnectionId, scope.AttachmentId)
	if err != nil {
		_ = r.sendOperationError(requestId, scope, err)
		return
	}

	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		releaseRequest()
		_ = r.sendOperationError(requestId, scope, errors.New("Session worker is closing"))
		return
	}
	r.activeRequests[requestId] = swActiveRequest{
		scope:  scope,
		cancel: cancellable,
	}
	r.mu.Unlock()

	result, hasResult, invokeErr := r.workerServices.Invoke(call, scope, cancellable.Context)

	r.mu.Lock()
	delete(r.activeRequests, requestId)
	r.mu.Unlock()
	releaseRequest()

	if invokeErr != nil {
		if sendErr := r.sendOperationError(requestId, scope, invokeErr); sendErr != nil {
			go func() { _ = r.triggerClose() }()
		}
		return
	}
	var encodedResult chord.JsonValue
	if hasResult {
		var encErr error
		encodedResult, encErr = swEncodeOperationResult(result)
		if encErr != nil {
			if sendErr := r.sendOperationError(requestId, scope, encErr); sendErr != nil {
				go func() { _ = r.triggerClose() }()
			}
			return
		}
	}

	var resp *jsonx.Object
	if hasResult {
		resp = jsonx.ObjectOf(
			"type", "operation_result",
			"requestId", requestId,
			"scope", swScopeObject(scope),
			"result", encodedResult,
		)
	} else {
		resp = jsonx.ObjectOf(
			"type", "operation_result",
			"requestId", requestId,
			"scope", swScopeObject(scope),
		)
	}
	if err := r.control.sendEvent(jsonx.ObjectOf(
		"type", "operation_response",
		"token", r.token,
		"sessionKey", r.sessionKey,
		"response", resp,
	)); err != nil {
		go func() { _ = r.triggerClose() }()
	}
}

func (r *swRunner) sendOperationError(requestId string, scope WorkerOperationScope, err error) error {
	var code *chord.RemoteServiceErrorCode
	var rse *chord.RemoteServiceError
	if errors.As(err, &rse) && rse != nil && chord.IsRemoteServiceErrorCode(string(rse.Code)) {
		c := rse.Code
		code = &c
	}
	var resp *jsonx.Object
	if code != nil {
		resp = jsonx.ObjectOf(
			"type", "operation_error",
			"requestId", requestId,
			"scope", swScopeObject(scope),
			"code", string(*code),
			"message", err.Error(),
		)
	} else {
		resp = jsonx.ObjectOf(
			"type", "operation_error",
			"requestId", requestId,
			"scope", swScopeObject(scope),
			"message", err.Error(),
		)
	}
	return r.control.sendEvent(jsonx.ObjectOf(
		"type", "operation_response",
		"token", r.token,
		"sessionKey", r.sessionKey,
		"response", resp,
	))
}

// RunSessionWorkerWithHarness runs one session worker lifecycle using a custom harness constructor.
func RunSessionWorkerWithHarness(args []string, createHarness CreateSessionWorkerHarness) error {
	if createHarness == nil {
		createHarness = swCreateCodingAgentHarness
	}
	var failureControl *swControl
	sendFailure := func(err error) {
		if failureControl == nil {
			return
		}
		token := os.Getenv(SessionWorkerControlTokenEnv)
		encodedKey := os.Getenv(SessionWorkerSessionKeyEnv)
		if token != "" && encodedKey != "" {
			if decoded, decErr := base64.RawURLEncoding.DecodeString(encodedKey); decErr == nil {
				_ = failureControl.sendEvent(jsonx.ObjectOf(
					"type", "worker_failed",
					"token", token,
					"sessionKey", string(decoded),
					"message", err.Error(),
				))
			}
		}
	}

	if len(args) != 1 {
		err := errors.New("Session worker requires one options argument")
		return err
	}
	var options SessionWorkerOptions
	if err := jsonx.Unmarshal([]byte(args[0]), &options); err != nil {
		return fmt.Errorf("Session worker received invalid options: %w", err)
	}
	if err := swValidateWorkerOptions(options); err != nil {
		return err
	}

	control, err := swConnectControl()
	if err != nil {
		return err
	}
	failureControl = control
	token := os.Getenv(SessionWorkerControlTokenEnv)
	encodedSessionKey := os.Getenv(SessionWorkerSessionKeyEnv)
	sessionKeyBytes, err := base64.RawURLEncoding.DecodeString(encodedSessionKey)
	if err != nil {
		sendFailure(err)
		control.close()
		return err
	}
	sessionKey := string(sessionKeyBytes)

	staleMs := int64(2_000)
	updateMs := int64(1_000)
	realpath := true
	maxRetries := 320
	factor := 1.0
	minTimeout := int64(25)
	maxTimeout := int64(25)
	maxRetryTime := int64(8_000)
	releaseOwnership, err := lockfile.Lock(context.Background(), options.Metadata.Path, &lockfile.Options{
		Realpath: &realpath,
		Stale:    &staleMs,
		Update:   &updateMs,
		Retries: &lockfile.RetryOptions{
			Retries:      &maxRetries,
			Factor:       &factor,
			MinTimeout:   &minTimeout,
			MaxTimeout:   &maxTimeout,
			MaxRetryTime: &maxRetryTime,
		},
	})
	if err != nil {
		sendFailure(err)
		control.close()
		return err
	}

	runner := &swRunner{
		options:          options,
		createHarness:    createHarness,
		control:          control,
		token:            token,
		sessionKey:       sessionKey,
		releaseOwnership: releaseOwnership,
		closeDone:        make(chan struct{}),
		activeRequests:   make(map[string]swActiveRequest),
	}

	runtime, err := createHarness(SessionStoragePath(options.Metadata), options)
	if err != nil {
		cleanupErr := runner.closeResources()
		if cleanupErr != nil {
			combined := fmt.Errorf("Session worker startup and cleanup failed: %w", errors.Join(err, cleanupErr))
			sendFailure(combined)
			control.close()
			return combined
		}
		sendFailure(err)
		control.close()
		return err
	}
	runner.runtime = runtime

	taskGraph, err := runtime.Harness.TaskGraph(chord.BackgroundContext)
	if err != nil {
		cleanupErr := runner.closeResources()
		if cleanupErr != nil {
			combined := fmt.Errorf("Session worker startup and cleanup failed: %w", errors.Join(err, cleanupErr))
			sendFailure(combined)
			control.close()
			return combined
		}
		sendFailure(err)
		control.close()
		return err
	}
	runner.taskGraph = taskGraph

	workerServices, err := services.CreateSessionWorkerServices(services.CreateSessionWorkerServicesOptions{
		Harness:         runtime.Harness,
		Conversation:    runtime.Conversation,
		ModelRuntime:    runtime.ModelRuntime,
		SettingsManager: runtime.SettingsManager,
		FacetLoader:     runtime.FacetLoader,
		Publish: func(scope services.WorkerServiceScope, subscriptionId string, update chord.ServiceProviderUpdate, ctx chord.Context) error {
			updateJson, encErr := swEncodeServiceProviderUpdate(update)
			if encErr != nil {
				return encErr
			}
			return control.sendEvent(jsonx.ObjectOf(
				"type", "service_update",
				"token", token,
				"sessionKey", sessionKey,
				"scope", swScopeObject(scope),
				"subscriptionId", subscriptionId,
				"update", updateJson,
			))
		},
	})
	if err != nil {
		taskGraph.Dispose()
		cleanupErr := runner.closeResources()
		if cleanupErr != nil {
			combined := fmt.Errorf("Session worker startup and cleanup failed: %w", errors.Join(err, cleanupErr))
			sendFailure(combined)
			control.close()
			return combined
		}
		sendFailure(err)
		control.close()
		return err
	}
	runner.workerServices = workerServices

	initGrace, err := swLifecycleDelay(SessionWorkerInitialDemandGraceEnv, swDefaultInitialDemandGraceMs)
	if err != nil {
		taskGraph.Dispose()
		cleanupErr := runner.closeResources()
		_ = cleanupErr
		sendFailure(err)
		control.close()
		return err
	}
	orphanGrace, err := swLifecycleDelay(SessionWorkerOrphanDemandGraceEnv, swDefaultOrphanDemandGraceMs)
	if err != nil {
		taskGraph.Dispose()
		cleanupErr := runner.closeResources()
		_ = cleanupErr
		sendFailure(err)
		control.close()
		return err
	}

	runner.lifecycle = NewWorkerLifecycle(WorkerLifecycleOptions{
		InitialServerConnectionId: control.initialServerConnectionId,
		InitialDemandGrace:        initGrace,
		OrphanDemandGrace:         orphanGrace,
		OnRetire: func() {
			go func() { _ = runner.triggerClose() }()
		},
	})

	runner.stopActivity = taskGraph.Subscribe(func(graph durable.TaskGraph, call chord.Context, delivery chord.ReplicatedStateDelivery) error {
		runner.lifecycle.SetHarnessActive(len(graph.Tasks) > 0)
		return nil
	})
	if err := runtime.Harness.Resume(); err != nil {
		cleanupErr := runner.triggerClose()
		_ = cleanupErr
		sendFailure(err)
		return err
	}

	sigCtx, stopSig := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopSig()

	go func() {
		for {
			select {
			case <-runner.closeDone:
				return
			case <-sigCtx.Done():
				_ = runner.triggerClose()
				return
			case err := <-control.errCh:
				if err != nil && !errors.Is(err, io.EOF) {
					_ = runner.triggerClose()
					return
				}
				_ = runner.triggerClose()
				return
			case msg, ok := <-control.msgCh:
				if !ok {
					_ = runner.triggerClose()
					return
				}
				if err := runner.handleCoordinatorInput(msg); err != nil {
					_ = runner.triggerClose()
					return
				}
			}
		}
	}()

	runner.mu.Lock()
	runner.ready = true
	runner.mu.Unlock()

	manifests := options.PluginManifestPaths
	if manifests == nil {
		manifests = []string{}
	}
	if err := control.sendEvent(jsonx.ObjectOf(
		"type", "worker_ready",
		"token", token,
		"sessionKey", sessionKey,
		"sessionId", options.Metadata.Id,
		"pid", os.Getpid(),
		"metadata", options.Metadata,
		"pluginManifestPaths", manifests,
	)); err != nil {
		_ = runner.triggerClose()
		sendFailure(err)
		return err
	}

	<-runner.closeDone
	return runner.closeErr
}

// RunSessionWorkerProcess executes the built-in coding-agent session worker daemon.
func RunSessionWorkerProcess(args []string) error {
	return RunSessionWorkerWithHarness(args, swCreateCodingAgentHarness)
}

func swCreateCodingAgentHarness(databasePath string, options SessionWorkerOptions) (*services.SessionWorkerRuntime, error) {
	cwd := options.Metadata.Cwd
	agentDir := utils.GetAgentDir()
	ctx := context.Background()

	modelRuntime, err := core.CreateModelRuntime(ctx, nil)
	if err != nil {
		return nil, err
	}
	settingsManager := core.SettingsManagerCreate(cwd, agentDir)
	if _, err := durableagent.ApplyHttpSettings(settingsManager); err != nil {
		modelRuntime.Close()
		return nil, err
	}

	storage, err := sqlite.OpenNodeSqliteStorage(databasePath)
	if err != nil {
		modelRuntime.Close()
		return nil, err
	}

	promptExt := durableagent.CreateDurablePromptExtension(durableagent.DurablePromptExtensionOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
	})
	subagentExt := durableagent.CreateSubagentExtension()
	extensions := []*durable.Extension{
		tools.CodingTools,
		promptExt,
		subagentExt,
	}
	registry := durable.CreateRegistry()
	for _, ext := range extensions {
		if extErr := registry.Install(ext); extErr != nil {
			_ = storage.Close(chord.BackgroundContext)
			modelRuntime.Close()
			return nil, fmt.Errorf("install extension %q: %w", ext.Name, extErr)
		}
	}
	harnessSettings, err := durableagent.BuildHarnessSettings(settingsManager, extensions)
	if err != nil {
		_ = storage.Close(chord.BackgroundContext)
		modelRuntime.Close()
		return nil, err
	}

	harness, err := durable.HarnessOpen(storage, durable.HarnessOptions{
		Models:         modelRuntime,
		Registry:       registry,
		StaticSettings: &harnessSettings,
		Env:            durableagent.CreateHarnessEnv(cwd, settingsManager.GetShellPath()),
	}, chord.BackgroundContext)
	if err != nil {
		_ = storage.Close(chord.BackgroundContext)
		modelRuntime.Close()
		return nil, err
	}

	existingConv, err := harness.Conversation(durable.RootConversationId, chord.BackgroundContext)
	if err != nil {
		_ = harness.Close(chord.BackgroundContext)
		modelRuntime.Close()
		return nil, err
	}

	agentChange := durable.AgentChange{
		Cwd: jsonx.Some(cwd),
	}
	if existingConv == nil {
		var cliProvider, cliModel *string
		if options.Model != "" {
			m := options.Model
			cliModel = &m
			if options.Provider != "" {
				p := options.Provider
				cliProvider = &p
			}
		}
		initial, err := durableagent.ResolveHarnessModel(ctx, modelRuntime, settingsManager, cliProvider, cliModel, nil)
		if err != nil {
			_ = harness.Close(chord.BackgroundContext)
			modelRuntime.Close()
			return nil, err
		}
		agentChange.Model = jsonx.Some(initial.ModelRef)
		if initial.ThinkingLevel != "" {
			agentChange.ThinkingLevel = jsonx.Some(initial.ThinkingLevel)
		}
	}

	conversation, err := harness.Root(chord.BackgroundContext, durable.RootOptions{
		Agent: &agentChange,
	})
	if err != nil {
		_ = harness.Close(chord.BackgroundContext)
		modelRuntime.Close()
		return nil, err
	}

	facetLoader, err := plugins.CreateSessionPluginFacetLoader(options.PluginManifestPaths)
	if err != nil {
		_ = harness.Close(chord.BackgroundContext)
		modelRuntime.Close()
		return nil, err
	}

	return &services.SessionWorkerRuntime{
		Harness:         harness,
		Conversation:    conversation,
		ModelRuntime:    modelRuntime,
		SettingsManager: settingsManager,
		FacetLoader:     facetLoader,
	}, nil
}

func swValidateWorkerOptions(options SessionWorkerOptions) error {
	if options.SessionDir == "" ||
		options.Metadata.Id == "" ||
		!filepath.IsAbs(options.Metadata.Cwd) ||
		!filepath.IsAbs(options.Metadata.Path) {
		return errors.New("Session worker received invalid options")
	}
	if options.Provider != "" && options.Model == "" {
		return errors.New("Session worker received invalid options")
	}
	for _, p := range options.PluginManifestPaths {
		if p == "" {
			return errors.New("Session worker received invalid options")
		}
	}
	return nil
}

func swReadLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		frag, err := reader.ReadSlice('\n')
		line = append(line, frag...)
		if len(line) > MaxControlLineBytes {
			return nil, errors.New("Session worker control message is too large")
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
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}
}

func swEncodeOperationResult(result any) (chord.JsonValue, error) {
	if chord.IsJsonValue(result) {
		return result, nil
	}
	if _, isFloat64 := result.(float64); isFloat64 {
		return nil, errors.New("Service produced a non-JSON result")
	}
	if f32, isFloat32 := result.(float32); isFloat32 && (math.IsNaN(float64(f32)) || math.IsInf(float64(f32), 0)) {
		return nil, errors.New("Service produced a non-JSON result")
	}
	raw, err := jsonx.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("Service produced a non-JSON result: %w", err)
	}
	if raw == nil {
		return nil, errors.New("Service produced a non-JSON result")
	}
	val, err := jsonx.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("Service produced a non-JSON result: %w", err)
	}
	if !chord.IsJsonValue(val) {
		return nil, errors.New("Service produced a non-JSON result")
	}
	return val, nil
}

func swEncodeServiceProviderUpdate(update chord.ServiceProviderUpdate) (chord.JsonValue, error) {
	raw, err := jsonx.Marshal(update)
	if err != nil {
		return nil, err
	}
	val, err := jsonx.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !chord.IsJsonValue(val) {
		return nil, errors.New("Service produced a non-JSON update")
	}
	if _, err := chord.ParseServiceProviderUpdate(val); err != nil {
		return nil, err
	}
	return val, nil
}

func swParseScope(v chord.JsonValue) (WorkerOperationScope, bool) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return WorkerOperationScope{}, false
	}
	srvVal, ok1 := obj.Get("serverConnectionId")
	attVal, ok2 := obj.Get("attachmentId")
	srvStr, ok3 := srvVal.(string)
	attStr, ok4 := attVal.(string)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return WorkerOperationScope{}, false
	}
	return WorkerOperationScope{
		ServerConnectionId: srvStr,
		AttachmentId:       attStr,
	}, true
}

func swScopeObject(scope WorkerOperationScope) *jsonx.Object {
	return jsonx.ObjectOf(
		"serverConnectionId", scope.ServerConnectionId,
		"attachmentId", scope.AttachmentId,
	)
}

func swDemandKey(serverConnectionId string, attachmentId string) string {
	return serverConnectionId + "\x00" + attachmentId
}

func swSameScope(left WorkerOperationScope, right WorkerOperationScope) bool {
	return left.ServerConnectionId == right.ServerConnectionId && left.AttachmentId == right.AttachmentId
}

func swLifecycleDelay(name string, fallbackMs int) (time.Duration, error) {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return time.Duration(fallbackMs) * time.Millisecond, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative safe integer", name)
	}
	return time.Duration(parsed) * time.Millisecond, nil
}
