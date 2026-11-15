// Ported from packages/coding-agent/test/experimental-session-worker-manager.test.ts (pi v1.0.0).

package experimental_test

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/experimental"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/server"
)

var teswmMetadata = experimental.SessionCatalogMetadata{
	Id:        "session-1",
	CreatedAt: 1,
	Cwd:       "/tmp",
	Path:      "/tmp/session-1",
}

type teswmSentMessage struct {
	peerId  string
	payload chord.JsonValue
}

type teswmFakeCoordinator struct {
	mu        sync.Mutex
	listeners map[int]func(experimental.CoordinatorConnectionEvent)
	nextId    int
	sent      []teswmSentMessage
	onSend    func(peerId string, payload *jsonx.Object)
}

func teswmNewFakeCoordinator() *teswmFakeCoordinator {
	return &teswmFakeCoordinator{
		listeners: make(map[int]func(experimental.CoordinatorConnectionEvent)),
	}
}

func (c *teswmFakeCoordinator) ControlPath() string {
	return "/tmp/control.sock"
}

func (c *teswmFakeCoordinator) ServerConnectionId() string {
	return "server-generation-1"
}

func (c *teswmFakeCoordinator) WasReplaced() bool {
	return false
}

func (c *teswmFakeCoordinator) OnEvent(listener func(experimental.CoordinatorConnectionEvent)) func() {
	c.mu.Lock()
	id := c.nextId
	c.nextId++
	c.listeners[id] = listener
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.listeners, id)
		c.mu.Unlock()
	}
}

func (c *teswmFakeCoordinator) Send(peerId string, payload any) error {
	norm, err := teswmNormalizePayload(payload)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.sent = append(c.sent, teswmSentMessage{peerId: peerId, payload: norm})
	handler := c.onSend
	c.mu.Unlock()
	if handler != nil {
		if obj, ok := norm.(*jsonx.Object); ok {
			handler(peerId, obj)
		}
	}
	return nil
}

func (c *teswmFakeCoordinator) Broadcast(payload any) error {
	norm, err := teswmNormalizePayload(payload)
	if err != nil {
		return err
	}
	obj, ok := norm.(*jsonx.Object)
	if !ok {
		return nil
	}
	typ, _ := obj.Get("type")
	if typ != "discover_workers" {
		return nil
	}
	c.emit(experimental.CoordinatorConnectionEvent{
		Type: "message",
		From: "worker-1",
		Payload: jsonx.ObjectOf(
			"type", "worker_ready",
			"token", "worker-token",
			"sessionKey", teswmMetadata.Path,
			"sessionId", teswmMetadata.Id,
			"pid", 123,
			"metadata", jsonx.ObjectOf(
				"id", teswmMetadata.Id,
				"createdAt", teswmMetadata.CreatedAt,
				"cwd", teswmMetadata.Cwd,
				"path", teswmMetadata.Path,
			),
			"pluginManifestPaths", []any{},
		),
	})
	return nil
}

func (c *teswmFakeCoordinator) setOnSend(fn func(peerId string, payload *jsonx.Object)) {
	c.mu.Lock()
	c.onSend = fn
	c.mu.Unlock()
}

func (c *teswmFakeCoordinator) sentCopy() []teswmSentMessage {
	c.mu.Lock()
	out := slices.Clone(c.sent)
	c.mu.Unlock()
	return out
}

func (c *teswmFakeCoordinator) emit(event experimental.CoordinatorConnectionEvent) {
	c.mu.Lock()
	list := make([]func(experimental.CoordinatorConnectionEvent), 0, len(c.listeners))
	for _, fn := range c.listeners {
		list = append(list, fn)
	}
	c.mu.Unlock()
	for _, fn := range list {
		fn(event)
	}
}

func teswmNormalizePayload(payload any) (chord.JsonValue, error) {
	raw, err := jsonx.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return jsonx.Parse(raw)
}

func teswmCreateAttachedWorker(t *testing.T, opts ...experimental.SessionWorkerManagerOptions) (*teswmFakeCoordinator, *experimental.SessionWorkerManager, server.RoutedSessionHandle, server.RoutedSessionAttachment) {
	t.Helper()
	coordinator := teswmNewFakeCoordinator()
	workers := experimental.NewSessionWorkerManager(coordinator, "/tmp", nil, nil, opts...)
	t.Cleanup(func() {
		workers.Detach()
	})
	if err := workers.Discover([]string{"worker-1"}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	handle, err := workers.OpenSession(teswmMetadata, chord.BackgroundContext, nil)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	coordinator.setOnSend(func(peerId string, payload *jsonx.Object) {
		typ, _ := payload.Get("type")
		if typ != "session_demand" {
			return
		}
		reqId, _ := payload.Get("requestId")
		attId, _ := payload.Get("attachmentId")
		attached, _ := payload.Get("attached")
		go coordinator.emit(experimental.CoordinatorConnectionEvent{
			Type: "message",
			From: peerId,
			Payload: jsonx.ObjectOf(
				"type", "demand_applied",
				"token", "worker-token",
				"sessionKey", teswmMetadata.Path,
				"requestId", reqId,
				"attachmentId", attId,
				"attached", attached,
			),
		})
	})
	attachment, err := handle.AttachClient(chord.BackgroundContext)
	if err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	return coordinator, workers, handle, attachment
}

func TestExperimentalSessionWorkerManagerAdoptsDiscoveredWorkerPluginSelection(t *testing.T) {
	coordinator := teswmNewFakeCoordinator()
	workers := experimental.NewSessionWorkerManager(coordinator, "/tmp", nil, nil)
	defer workers.Detach()

	if err := workers.Discover([]string{"worker-1"}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, msg := range coordinator.sentCopy() {
		if obj, ok := msg.payload.(*jsonx.Object); ok {
			if typ, _ := obj.Get("type"); typ == "shutdown" && msg.peerId == "worker-1" {
				t.Fatalf("did not expect shutdown sent to discovered worker-1")
			}
		}
	}
	if got := len(workers.WorkerPids()); got != 1 {
		t.Fatalf("len(WorkerPids) = %d, want 1", got)
	}
	if err := workers.AssertSessionPluginManifestPaths(teswmMetadata, nil); err != nil {
		t.Fatalf("AssertSessionPluginManifestPaths(empty): %v", err)
	}
}

func TestExperimentalSessionWorkerManagerRejectsDifferentPluginSelectionWithoutStopping(t *testing.T) {
	_, workers, _, _ := teswmCreateAttachedWorker(t)
	err := workers.AssertSessionPluginManifestPaths(teswmMetadata, []string{"/tmp/plugin/chord-facets.json"})
	if err == nil || !strings.Contains(err.Error(), "active with a different plugin selection") {
		t.Fatalf("expected different plugin selection error, got %v", err)
	}
	if got := len(workers.WorkerPids()); got != 1 {
		t.Fatalf("len(WorkerPids) = %d, want 1", got)
	}
}

func TestExperimentalSessionWorkerManagerCompensatesTimedOutAttachment(t *testing.T) {
	coordinator := teswmNewFakeCoordinator()
	workers := experimental.NewSessionWorkerManager(coordinator, "/tmp", nil, nil, experimental.SessionWorkerManagerOptions{
		DemandTimeout: 25 * time.Millisecond,
	})
	defer workers.Detach()

	if err := workers.Discover([]string{"worker-1"}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	handle, err := workers.OpenSession(teswmMetadata, chord.BackgroundContext, nil)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	type demandEntry struct {
		attachmentId string
		attached     bool
	}
	var demandsMu sync.Mutex
	var demands []demandEntry
	coordinator.setOnSend(func(peerId string, payload *jsonx.Object) {
		typ, _ := payload.Get("type")
		if typ != "session_demand" {
			return
		}
		attId, ok1 := payload.Get("attachmentId")
		attached, ok2 := payload.Get("attached")
		attStr, ok1s := attId.(string)
		attBool, ok2b := attached.(bool)
		if !ok1 || !ok2 || !ok1s || !ok2b {
			return
		}
		demandsMu.Lock()
		demands = append(demands, demandEntry{attachmentId: attStr, attached: attBool})
		demandsMu.Unlock()
		if attBool {
			return
		}
		reqId, _ := payload.Get("requestId")
		go coordinator.emit(experimental.CoordinatorConnectionEvent{
			Type: "message",
			From: peerId,
			Payload: jsonx.ObjectOf(
				"type", "demand_applied",
				"token", "worker-token",
				"sessionKey", teswmMetadata.Path,
				"requestId", reqId,
				"attachmentId", attStr,
				"attached", false,
			),
		})
	})

	_, err = handle.AttachClient(chord.BackgroundContext)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timed out error from AttachClient, got %v", err)
	}

	demandsMu.Lock()
	defer demandsMu.Unlock()
	if len(demands) != 2 {
		t.Fatalf("len(demands) = %d, want 2: %+v", len(demands), demands)
	}
	if !demands[0].attached || demands[0].attachmentId == "" {
		t.Fatalf("expected first demand attached=true with non-empty attachmentId, got %+v", demands[0])
	}
	if demands[1].attached || demands[1].attachmentId != demands[0].attachmentId {
		t.Fatalf("expected second demand attached=false matching %q, got %+v", demands[0].attachmentId, demands[1])
	}
}

func TestExperimentalSessionWorkerManagerKillsWorkerWhenTimedOutDemandUnreconciled(t *testing.T) {
	var killedMu sync.Mutex
	var killed []int
	coordinator := teswmNewFakeCoordinator()
	workers := experimental.NewSessionWorkerManager(coordinator, "/tmp", nil, nil, experimental.SessionWorkerManagerOptions{
		DemandTimeout:   20 * time.Millisecond,
		ShutdownTimeout: 20 * time.Millisecond,
		KillPid: func(pid int) error {
			killedMu.Lock()
			killed = append(killed, pid)
			killedMu.Unlock()
			return nil
		},
	})
	defer workers.Detach()

	if err := workers.Discover([]string{"worker-1"}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	handle, err := workers.OpenSession(teswmMetadata, chord.BackgroundContext, nil)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	coordinator.setOnSend(nil)

	_, err = handle.AttachClient(chord.BackgroundContext)
	if err == nil || !strings.Contains(err.Error(), "worker was terminated") {
		t.Fatalf("expected worker was terminated error, got %v", err)
	}
	killedMu.Lock()
	defer killedMu.Unlock()
	if !slices.Equal(killed, []int{123}) {
		t.Fatalf("killed PIDs = %v, want [123]", killed)
	}
	if got := len(workers.WorkerPids()); got != 0 {
		t.Fatalf("len(WorkerPids) = %d, want 0", got)
	}
}

func TestExperimentalSessionWorkerManagerBoundsHarnessDrivenShutdown(t *testing.T) {
	var killedMu sync.Mutex
	var killed []int
	coordinator, workers, handle, attachment := teswmCreateAttachedWorker(t, experimental.SessionWorkerManagerOptions{
		DemandTimeout:   20 * time.Millisecond,
		ShutdownTimeout: 20 * time.Millisecond,
		KillPid: func(pid int) error {
			killedMu.Lock()
			killed = append(killed, pid)
			killedMu.Unlock()
			return nil
		},
	})
	if err := attachment.Release(chord.BackgroundContext); err != nil {
		t.Fatalf("Release: %v", err)
	}
	coordinator.setOnSend(nil)

	if err := handle.Close(chord.BackgroundContext); err != nil {
		t.Fatalf("handle.Close: %v", err)
	}
	killedMu.Lock()
	defer killedMu.Unlock()
	if !slices.Equal(killed, []int{123}) {
		t.Fatalf("killed PIDs = %v, want [123]", killed)
	}
	if got := len(workers.WorkerPids()); got != 0 {
		t.Fatalf("len(WorkerPids) = %d, want 0", got)
	}
}

func TestExperimentalSessionWorkerOperations(t *testing.T) {
	serviceCall := chord.ServiceCall{
		ServiceId: "test.session",
		Member:    "run",
		Args:      []chord.JsonValue{"Hello"},
	}
	noopPublish := func(string, chord.ServiceProviderUpdate, chord.Context) error { return nil }

	t.Run("correlates service results to the worker generation and attachment", func(t *testing.T) {
		coordinator, workers, _, attachment := teswmCreateAttachedWorker(t)
		coordinator.setOnSend(func(peerId string, payload *jsonx.Object) {
			if typ, _ := payload.Get("type"); typ != "operation" {
				return
			}
			reqId, _ := payload.Get("requestId")
			scope, _ := payload.Get("scope")
			go coordinator.emit(experimental.CoordinatorConnectionEvent{
				Type: "message",
				From: peerId,
				Payload: jsonx.ObjectOf(
					"type", "operation_response",
					"token", "worker-token",
					"sessionKey", teswmMetadata.Path,
					"response", jsonx.ObjectOf(
						"type", "operation_result",
						"requestId", reqId,
						"scope", scope,
						"result", jsonx.ObjectOf("accepted", true),
					),
				),
			})
		})

		res, hasVal, err := attachment.InvokeService(serviceCall, noopPublish, chord.BackgroundContext)
		if err != nil {
			t.Fatalf("InvokeService: %v", err)
		}
		if !hasVal {
			t.Fatalf("expected hasVal=true")
		}
		resObj, ok := res.(*jsonx.Object)
		if !ok {
			t.Fatalf("expected *jsonx.Object result, got %T", res)
		}
		if acc, _ := resObj.Get("accepted"); acc != true {
			t.Fatalf("accepted = %v, want true", acc)
		}

		var foundOp *jsonx.Object
		for _, msg := range coordinator.sentCopy() {
			if obj, ok := msg.payload.(*jsonx.Object); ok {
				if typ, _ := obj.Get("type"); typ == "operation" {
					foundOp = obj
				}
			}
		}
		if foundOp == nil {
			t.Fatalf("expected sent operation payload")
		}
		scopeRaw, _ := foundOp.Get("scope")
		scopeObj, ok := scopeRaw.(*jsonx.Object)
		if !ok {
			t.Fatalf("expected operation scope object, got %T", scopeRaw)
		}
		if sid, _ := scopeObj.Get("serverConnectionId"); sid != "server-generation-1" {
			t.Fatalf("scope.serverConnectionId = %v, want server-generation-1", sid)
		}
		if attId, _ := scopeObj.Get("attachmentId"); attId == "" || attId == nil {
			t.Fatalf("expected non-empty scope.attachmentId, got %v", attId)
		}

		workers.Detach()
		_ = attachment.Release(chord.BackgroundContext)
	})

	t.Run("rejects a correlated response with mismatched worker identity", func(t *testing.T) {
		coordinator, workers, _, attachment := teswmCreateAttachedWorker(t)
		coordinator.setOnSend(func(peerId string, payload *jsonx.Object) {
			if typ, _ := payload.Get("type"); typ != "operation" {
				return
			}
			reqId, _ := payload.Get("requestId")
			scope, _ := payload.Get("scope")
			go coordinator.emit(experimental.CoordinatorConnectionEvent{
				Type: "message",
				From: peerId,
				Payload: jsonx.ObjectOf(
					"type", "operation_response",
					"token", "wrong-token",
					"sessionKey", teswmMetadata.Path,
					"response", jsonx.ObjectOf(
						"type", "operation_result",
						"requestId", reqId,
						"scope", scope,
						"result", jsonx.ObjectOf("accepted", true),
					),
				),
			})
		})

		_, _, err := attachment.InvokeService(serviceCall, noopPublish, chord.BackgroundContext)
		if err == nil || !strings.Contains(err.Error(), "mismatched operation response") {
			t.Fatalf("expected mismatched operation response error, got %v", err)
		}
		workers.Detach()
		_ = attachment.Release(chord.BackgroundContext)
	})

	t.Run("rejects a null request scope", func(t *testing.T) {
		coordinator, workers, _, attachment := teswmCreateAttachedWorker(t)
		coordinator.setOnSend(func(peerId string, payload *jsonx.Object) {
			if typ, _ := payload.Get("type"); typ != "operation" {
				return
			}
			reqId, _ := payload.Get("requestId")
			go coordinator.emit(experimental.CoordinatorConnectionEvent{
				Type: "message",
				From: peerId,
				Payload: jsonx.ObjectOf(
					"type", "operation_response",
					"token", "worker-token",
					"sessionKey", teswmMetadata.Path,
					"response", jsonx.ObjectOf(
						"type", "operation_result",
						"requestId", reqId,
						"scope", nil,
						"result", jsonx.ObjectOf("accepted", true),
					),
				),
			})
		})

		_, _, err := attachment.InvokeService(serviceCall, noopPublish, chord.BackgroundContext)
		if err == nil || !strings.Contains(err.Error(), "invalid operation response") {
			t.Fatalf("expected invalid operation response error, got %v", err)
		}
		workers.Detach()
		_ = attachment.Release(chord.BackgroundContext)
	})

	t.Run("rejects pending service calls on replacement without stopping the worker", func(t *testing.T) {
		coordinator, workers, _, attachment := teswmCreateAttachedWorker(t)
		coordinator.setOnSend(nil)

		errCh := make(chan error, 1)
		go func() {
			_, _, err := attachment.InvokeService(serviceCall, noopPublish, chord.BackgroundContext)
			errCh <- err
		}()

		// Give InvokeService a moment to register the pending operation, then detach to simulate server replacement.
		deadline := time.Now().Add(2 * time.Second)
		for {
			foundOp := false
			for _, msg := range coordinator.sentCopy() {
				if obj, ok := msg.payload.(*jsonx.Object); ok {
					if typ, _ := obj.Get("type"); typ == "operation" {
						foundOp = true
						break
					}
				}
			}
			if foundOp || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		workers.Detach()

		err := <-errCh
		if err == nil || !strings.Contains(err.Error(), "replaced during a worker operation") {
			t.Fatalf("expected replaced during a worker operation error, got %v", err)
		}
		for _, msg := range coordinator.sentCopy() {
			if obj, ok := msg.payload.(*jsonx.Object); ok {
				if typ, _ := obj.Get("type"); typ == "shutdown" {
					t.Fatalf("did not expect shutdown message on detach")
				}
			}
		}
		if got := len(workers.WorkerPids()); got != 0 {
			t.Fatalf("len(WorkerPids) = %d, want 0", got)
		}
	})
}
