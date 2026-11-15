// Ported from packages/server/src/testing/host.ts (pi v1.0.0).

package servertest

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/server"
)

// Deferred is a single-assignment synchronization primitive with a completion channel.
type Deferred[T any] struct {
	once  sync.Once
	done  chan struct{}
	value T
}

// NewDeferred allocates an unresolved Deferred.
func NewDeferred[T any]() *Deferred[T] {
	return &Deferred[T]{
		done: make(chan struct{}),
	}
}

// Resolve completes the Deferred once with value.
func (d *Deferred[T]) Resolve(value T) {
	d.once.Do(func() {
		d.value = value
		close(d.done)
	})
}

// Done returns a channel that closes when Resolve has completed.
func (d *Deferred[T]) Done() <-chan struct{} {
	return d.done
}

// Await blocks until Resolve is called and returns the resolved value.
func (d *Deferred[T]) Await() T {
	<-d.done
	return d.value
}

// OpenGate pauses a test hook until Release is resolved.
type OpenGate struct {
	Entered *Deferred[struct{}]
	Release *Deferred[struct{}]
}

func hostNewGate() *OpenGate {
	return &OpenGate{
		Entered: NewDeferred[struct{}](),
		Release: NewDeferred[struct{}](),
	}
}

// TestHarness is a controllable in-memory RoutedSessionHandle for server tests.
type TestHarness struct {
	mu                     sync.Mutex
	metadata               server.SessionMetadata
	closed                 *Deferred[struct{}]
	terminatedCh           chan error
	terminateOnce          sync.Once
	attachedClients        int
	attachmentReleaseCount int
	closeCount             int
	serviceCalls           []chord.ServiceCall
	failAttachmentRelease  error
	failClose              error
	nextServiceError       error
	nextServiceResult      chord.JsonValue
	nextServiceResultOk    bool
	nextCloseGate          *OpenGate
	nextServiceGate        *OpenGate
}

// NewTestHarness constructs a TestHarness bound to metadata.
func NewTestHarness(metadata server.SessionMetadata) *TestHarness {
	return &TestHarness{
		metadata:            metadata,
		closed:              NewDeferred[struct{}](),
		terminatedCh:        make(chan error, 1),
		nextServiceResult:   jsonx.ObjectOf("ok", true),
		nextServiceResultOk: true,
	}
}

// Metadata returns the session metadata passed to OpenSession.
func (h *TestHarness) Metadata() server.SessionMetadata { return h.metadata }

// Closed returns a Deferred resolved when Close completes without error.
func (h *TestHarness) Closed() *Deferred[struct{}] { return h.closed }

// Terminated implements server.RoutedSessionHandle.
func (h *TestHarness) Terminated() <-chan error { return h.terminatedCh }

// AttachedClients returns the current active client attachment count.
func (h *TestHarness) AttachedClients() int {
	h.mu.Lock()
	n := h.attachedClients
	h.mu.Unlock()
	return n
}

// AttachmentReleaseCount returns total calls to attachment Release.
func (h *TestHarness) AttachmentReleaseCount() int {
	h.mu.Lock()
	n := h.attachmentReleaseCount
	h.mu.Unlock()
	return n
}

// CloseCount returns total calls to Close.
func (h *TestHarness) CloseCount() int {
	h.mu.Lock()
	n := h.closeCount
	h.mu.Unlock()
	return n
}

// ServiceCalls returns a snapshot copy of recorded InvokeService calls.
func (h *TestHarness) ServiceCalls() []chord.ServiceCall {
	h.mu.Lock()
	copied := slices.Clone(h.serviceCalls)
	h.mu.Unlock()
	return copied
}

// SetFailAttachmentRelease configures an error to return from future attachment Releases.
func (h *TestHarness) SetFailAttachmentRelease(err error) {
	h.mu.Lock()
	h.failAttachmentRelease = err
	h.mu.Unlock()
}

// SetFailClose configures an error to return from the next Close call.
func (h *TestHarness) SetFailClose(err error) {
	h.mu.Lock()
	h.failClose = err
	h.mu.Unlock()
}

// SetNextServiceError configures an error to return from the next InvokeService call.
func (h *TestHarness) SetNextServiceError(err error) {
	h.mu.Lock()
	h.nextServiceError = err
	h.mu.Unlock()
}

// SetNextServiceResult configures the result tuple returned by the next InvokeService call.
func (h *TestHarness) SetNextServiceResult(value chord.JsonValue, ok bool) {
	h.mu.Lock()
	h.nextServiceResult = value
	h.nextServiceResultOk = ok
	h.mu.Unlock()
}

// GateNextClose installs a gate that pauses the next Close call.
func (h *TestHarness) GateNextClose() *OpenGate {
	gate := hostNewGate()
	h.mu.Lock()
	h.nextCloseGate = gate
	h.mu.Unlock()
	return gate
}

// GateNextServiceCall installs a gate that pauses the next InvokeService call.
func (h *TestHarness) GateNextServiceCall() *OpenGate {
	gate := hostNewGate()
	h.mu.Lock()
	h.nextServiceGate = gate
	h.mu.Unlock()
	return gate
}

// AttachClient implements server.RoutedSessionHandle.
func (h *TestHarness) AttachClient(_ chord.Context) (server.RoutedSessionAttachment, error) {
	h.mu.Lock()
	h.attachedClients++
	h.mu.Unlock()
	return &hostHarnessAttachment{harness: h}, nil
}

// InvokeService executes and records one service call on the test harness.
func (h *TestHarness) InvokeService(call chord.ServiceCall, _ chord.ServiceUpdatePublisher, _ chord.Context) (chord.JsonValue, bool, error) {
	h.mu.Lock()
	h.serviceCalls = append(h.serviceCalls, call)
	if h.nextServiceError != nil {
		err := h.nextServiceError
		h.nextServiceError = nil
		h.mu.Unlock()
		return nil, false, err
	}
	gate := h.nextServiceGate
	h.nextServiceGate = nil
	h.mu.Unlock()

	if gate != nil {
		gate.Entered.Resolve(struct{}{})
		gate.Release.Await()
	}

	h.mu.Lock()
	result := h.nextServiceResult
	ok := h.nextServiceResultOk
	h.nextServiceResult = jsonx.ObjectOf("ok", true)
	h.nextServiceResultOk = true
	h.mu.Unlock()

	return result, ok, nil
}

// Close implements server.RoutedSessionHandle.
func (h *TestHarness) Close(_ chord.Context) error {
	h.mu.Lock()
	h.closeCount++
	gate := h.nextCloseGate
	h.nextCloseGate = nil
	h.mu.Unlock()

	if gate != nil {
		gate.Entered.Resolve(struct{}{})
		gate.Release.Await()
	}

	h.mu.Lock()
	if h.failClose != nil {
		err := h.failClose
		h.failClose = nil
		h.mu.Unlock()
		return err
	}
	h.mu.Unlock()

	h.closed.Resolve(struct{}{})
	h.terminateOnce.Do(func() {
		h.terminatedCh <- nil
	})
	return nil
}

// Terminate resolves the harness termination signal with err.
func (h *TestHarness) Terminate(err error) {
	h.terminateOnce.Do(func() {
		h.terminatedCh <- err
	})
}

type hostHarnessAttachment struct {
	mu       sync.Mutex
	harness  *TestHarness
	released bool
}

func (a *hostHarnessAttachment) InvokeService(
	call chord.ServiceCall,
	publish chord.ServiceUpdatePublisher,
	callContext chord.Context,
) (chord.JsonValue, bool, error) {
	return a.harness.InvokeService(call, publish, callContext)
}

func (a *hostHarnessAttachment) Release(_ chord.Context) error {
	a.mu.Lock()
	if a.released {
		a.mu.Unlock()
		return nil
	}
	a.harness.mu.Lock()
	a.harness.attachmentReleaseCount++
	failErr := a.harness.failAttachmentRelease
	if failErr != nil {
		a.harness.mu.Unlock()
		a.mu.Unlock()
		return failErr
	}
	a.released = true
	a.harness.attachedClients--
	a.harness.mu.Unlock()
	a.mu.Unlock()
	return nil
}

// CreateTestServerServices returns a RoutedServerServiceHost handling pi.session-management.
func CreateTestServerServices() server.RoutedServerServiceHost {
	return &hostTestServerServices{}
}

type hostTestServerServices struct{}

func (s *hostTestServerServices) AttachClient(presentation server.RoutedServerPresentation, _ chord.Context) (server.RoutedServerServiceAttachment, error) {
	return &hostTestServerServiceAttachment{presentation: presentation}, nil
}

type hostTestServerServiceAttachment struct {
	presentation server.RoutedServerPresentation
}

func (a *hostTestServerServiceAttachment) InvokeService(
	call chord.ServiceCall,
	_ chord.ServiceUpdatePublisher,
	callContext chord.Context,
) (chord.JsonValue, bool, error) {
	if call.Instance == nil &&
		call.ServiceId == "pi.session-management" &&
		call.Member == "attach" &&
		len(call.Args) == 1 {
		if sessionId, ok := call.Args[0].(string); ok {
			if err := a.presentation.AttachSession(sessionId, callContext); err != nil {
				return nil, false, err
			}
			return nil, true, nil
		}
	}
	if call.Instance == nil &&
		call.ServiceId == "pi.session-management" &&
		call.Member == "detach" &&
		len(call.Args) == 0 {
		if err := a.presentation.DetachSession(callContext); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}
	return nil, false, fmt.Errorf("Unsupported test server service %s.%s", call.ServiceId, call.Member)
}

func (a *hostTestServerServiceAttachment) Release(_ chord.Context) error { return nil }

// TestServerHost is an in-memory ServerHost implementation for conformance and transport tests.
type TestServerHost struct {
	mu                   sync.Mutex
	serverServices       server.RoutedServerServiceHost
	sessions             map[string]server.SessionMetadata
	harnesses            map[string][]*TestHarness
	openSessionCount     int
	nextOpenSessionError error
	nextHarnessCloseErr  error
	nextOpenSessionGate  *OpenGate
}

// NewTestServerHost constructs a fresh TestServerHost.
func NewTestServerHost() *TestServerHost {
	return &TestServerHost{
		serverServices: CreateTestServerServices(),
		sessions:       make(map[string]server.SessionMetadata),
		harnesses:      make(map[string][]*TestHarness),
	}
}

// ServerServices implements server.ServerHost.
func (h *TestServerHost) ServerServices() server.RoutedServerServiceHost {
	return h.serverServices
}

// OpenSessionCount returns the number of OpenSession invocations.
func (h *TestServerHost) OpenSessionCount() int {
	h.mu.Lock()
	n := h.openSessionCount
	h.mu.Unlock()
	return n
}

// HarnessCount returns the number of session keys with created TestHarness entries.
func (h *TestServerHost) HarnessCount() int {
	h.mu.Lock()
	n := len(h.harnesses)
	h.mu.Unlock()
	return n
}

// Harnesses returns a copy of all created TestHarnesses for id.
func (h *TestServerHost) Harnesses(id string) []*TestHarness {
	h.mu.Lock()
	copied := slices.Clone(h.harnesses[id])
	h.mu.Unlock()
	return copied
}

// SetNextOpenSessionError configures an error for the next OpenSession call.
func (h *TestServerHost) SetNextOpenSessionError(err error) {
	h.mu.Lock()
	h.nextOpenSessionError = err
	h.mu.Unlock()
}

// SetNextHarnessCloseError configures a close error on the next opened TestHarness.
func (h *TestServerHost) SetNextHarnessCloseError(err error) {
	h.mu.Lock()
	h.nextHarnessCloseErr = err
	h.mu.Unlock()
}

// ResolveSession implements server.ServerHost.
func (h *TestServerHost) ResolveSession(sessionId string, _ chord.Context) (server.SessionMetadata, error) {
	h.mu.Lock()
	metadata, ok := h.sessions[sessionId]
	h.mu.Unlock()
	if !ok {
		return nil, server.NewSessionNotFoundError(fmt.Sprintf("Unknown session: %s", sessionId))
	}
	return metadata, nil
}

// OpenSession implements server.ServerHost.
func (h *TestServerHost) OpenSession(metadata server.SessionMetadata, _ chord.Context) (server.RoutedSessionHandle, error) {
	h.mu.Lock()
	h.openSessionCount++
	gate := h.nextOpenSessionGate
	h.nextOpenSessionGate = nil
	h.mu.Unlock()

	if gate != nil {
		gate.Entered.Resolve(struct{}{})
		gate.Release.Await()
	}

	h.mu.Lock()
	_, seeded := h.sessions[metadata.SessionId()]
	if !seeded {
		h.mu.Unlock()
		return nil, server.NewSessionNotFoundError(fmt.Sprintf("Unknown session: %s", metadata.SessionId()))
	}
	if h.nextOpenSessionError != nil {
		err := h.nextOpenSessionError
		h.nextOpenSessionError = nil
		h.mu.Unlock()
		return nil, err
	}
	harness := NewTestHarness(metadata)
	if h.nextHarnessCloseErr != nil {
		harness.failClose = h.nextHarnessCloseErr
		h.nextHarnessCloseErr = nil
	}
	h.harnesses[metadata.SessionId()] = append(h.harnesses[metadata.SessionId()], harness)
	h.mu.Unlock()

	return harness, nil
}

// Seed adds durable session metadata for id (default "session-1").
func (h *TestServerHost) Seed(id ...string) server.SessionMetadata {
	sessionId := "session-1"
	if len(id) > 0 && id[0] != "" {
		sessionId = id[0]
	}
	metadata := server.BasicSessionMetadata{Id: sessionId}
	h.mu.Lock()
	h.sessions[sessionId] = metadata
	h.mu.Unlock()
	return metadata
}

// GateNextOpenSession pauses the next OpenSession call.
func (h *TestServerHost) GateNextOpenSession() *OpenGate {
	gate := hostNewGate()
	h.mu.Lock()
	h.nextOpenSessionGate = gate
	h.mu.Unlock()
	return gate
}

// LatestHarness returns the most recently created TestHarness for id.
func (h *TestServerHost) LatestHarness(id string) (*TestHarness, error) {
	h.mu.Lock()
	list := h.harnesses[id]
	var latest *TestHarness
	if len(list) > 0 {
		latest = list[len(list)-1]
	}
	h.mu.Unlock()
	if latest == nil {
		return nil, errors.New("No harness for " + id)
	}
	return latest, nil
}
