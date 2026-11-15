// Ported from packages/coding-agent/src/experimental/services/connection.ts (pi v1.0.0).

package services

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
)

// ServerConnectionState represents the client's server connection lifecycle state.
// Discriminator Status: "connecting" | "connected" | "disconnected".
type ServerConnectionState struct {
	Status  string  `json:"status"`
	Attempt *int    `json:"attempt,omitzero"`
	Since   *string `json:"since,omitzero"`
	Reason  *string `json:"reason,omitzero"`
	RetryAt *string `json:"retryAt,omitzero"`
}

// MarshalJSON serializes ServerConnectionState with variant-exact keys matching TS.
func (s ServerConnectionState) MarshalJSON() ([]byte, error) {
	switch s.Status {
	case "connecting":
		attempt := 0
		if s.Attempt != nil {
			attempt = *s.Attempt
		}
		return jsonx.Marshal(jsonx.ObjectOf("status", "connecting", "attempt", attempt))
	case "connected":
		since := ""
		if s.Since != nil {
			since = *s.Since
		}
		return jsonx.Marshal(jsonx.ObjectOf("status", "connected", "since", since))
	default:
		since := ""
		if s.Since != nil {
			since = *s.Since
		}
		reason := ""
		if s.Reason != nil {
			reason = *s.Reason
		}
		return jsonx.Marshal(jsonx.ObjectOf(
			"status", "disconnected",
			"since", since,
			"reason", reason,
			"retryAt", s.RetryAt,
		))
	}
}

// SessionAttachmentState represents the client's selected-Session attachment lifecycle state.
// Discriminator Status: "detached" | "attaching" | "attached" | "degraded".
type SessionAttachmentState struct {
	Status    string `json:"status"`
	SessionId string `json:"sessionId,omitzero"`
}

// ServerServiceSource supplies server-scoped remote services to a facet host.
type ServerServiceSource interface {
	chord.RemoteServiceSource
	Connection() chord.ReplicatedState[ServerConnectionState]
	Dispose(ctx chord.Context) error
}

// SessionServiceSource supplies selected-Session remote services to a facet host.
type SessionServiceSource interface {
	chord.RemoteServiceSource
	Attachment() chord.ReplicatedState[SessionAttachmentState]
	// WhenAttached waits for the exact current attachment generation to finish hydrating.
	WhenAttached(sessionId string, ctx chord.Context) error
	// WhenDetached waits for every binding to finish releasing the previous attachment.
	WhenDetached(ctx chord.Context) error
	Dispose(ctx chord.Context) error
}

// ServiceSourceOptions configures asynchronous service source error reporting.
type ServiceSourceOptions struct {
	OnError func(error)
}

type connServiceView interface {
	Call(member string, args ...any) (any, error)
	Get(property any) (any, error)
}

type connStateView interface {
	StateValue() (any, bool, error)
	Subscribe(listener func(value chord.JsonValue, call chord.Context, delivery chord.ReplicatedStateDelivery) error) (func(), error)
}

func connDefineService[T any](id string, local ...bool) *chord.Service[T] {
	var opts *chord.DefineServiceOptions
	if len(local) > 0 {
		l := local[0]
		opts = &chord.DefineServiceOptions{Local: &l}
	}
	svc, err := chord.DefineService[T](id, opts)
	if err != nil {
		panic(fmt.Sprintf("services: define service %s: %v", id, err))
	}
	return svc
}

func connEncodeJsonValue(v any) (chord.JsonValue, error) {
	return jsonx.Encode(v)
}

func connDecodeJson[T any](raw any) (T, error) {
	var zero T
	if typed, ok := raw.(T); ok {
		return typed, nil
	}
	if err := jsonx.Decode(raw, &zero); err != nil {
		return zero, err
	}
	return zero, nil
}

func connExtractArg[T any](args []any, index int) (T, error) {
	var zero T
	if index < 0 || index >= len(args) {
		return zero, fmt.Errorf("missing service argument at index %d", index)
	}
	return connDecodeJson[T](args[index])
}

func connExtractContext(args []any, index int) chord.Context {
	if index >= 0 && index < len(args) {
		if ctx, ok := args[index].(chord.Context); ok && ctx != nil {
			return ctx
		}
	}
	return chord.BackgroundContext
}

type connStateWrapper[T any] struct {
	resolve func() (any, error)
}

// WrapReplicatedState adapts a Chord state member view or JsonValue ReplicatedState to ReplicatedState[T].
func WrapReplicatedState[T any](source any) chord.ReplicatedState[T] {
	if typed, ok := source.(chord.ReplicatedState[T]); ok {
		return typed
	}
	return &connStateWrapper[T]{
		resolve: func() (any, error) {
			return source, nil
		},
	}
}

func connWrapServiceStateMember[T any](view connServiceView, member string) chord.ReplicatedState[T] {
	return &connStateWrapper[T]{
		resolve: func() (any, error) {
			if view == nil {
				return nil, errors.New("service view is nil")
			}
			return view.Get(member)
		},
	}
}

func (w *connStateWrapper[T]) Value() (T, bool) {
	var zero T
	if w == nil || w.resolve == nil {
		return zero, false
	}
	rawState, err := w.resolve()
	if err != nil || rawState == nil {
		return zero, false
	}
	switch s := rawState.(type) {
	case chord.ReplicatedState[T]:
		return s.Value()
	case chord.ReplicatedState[chord.JsonValue]:
		val, ok := s.Value()
		if !ok {
			return zero, false
		}
		decoded, err := connDecodeJson[T](val)
		if err != nil {
			return zero, false
		}
		return decoded, true
	case connStateView:
		val, ok, err := s.StateValue()
		if err != nil || !ok {
			return zero, false
		}
		decoded, err := connDecodeJson[T](val)
		if err != nil {
			return zero, false
		}
		return decoded, true
	default:
		return zero, false
	}
}

func (w *connStateWrapper[T]) Subscribe(listener func(value T, call chord.Context, delivery chord.ReplicatedStateDelivery) error) func() {
	if w == nil || w.resolve == nil || listener == nil {
		return func() {}
	}
	rawState, err := w.resolve()
	if err != nil || rawState == nil {
		return func() {}
	}
	adapt := func(raw any, call chord.Context, delivery chord.ReplicatedStateDelivery) error {
		decoded, err := connDecodeJson[T](raw)
		if err != nil {
			return err
		}
		return listener(decoded, call, delivery)
	}
	switch s := rawState.(type) {
	case chord.ReplicatedState[T]:
		return s.Subscribe(listener)
	case chord.ReplicatedState[chord.JsonValue]:
		return s.Subscribe(adapt)
	case connStateView:
		unsub, err := s.Subscribe(adapt)
		if err != nil || unsub == nil {
			return func() {}
		}
		return unsub
	default:
		return func() {}
	}
}

type connRoutedServiceBinding struct {
	mu                 sync.Mutex
	services           chord.RemoteServiceBinding
	getBound           func() bool
	onActivate         func(ctx chord.Context) error
	remove             func()
	activated          bool
	activationComplete bool
}

func connNewRoutedServiceBinding(
	services []chord.ServiceRef,
	transport chord.RemoteServiceTransport,
	getBound func() bool,
	assertAccess func(),
	onError func(error),
	onActivate func(ctx chord.Context) error,
	remove func(),
) (*connRoutedServiceBinding, error) {
	bound := false
	binding, err := chord.CreateRemoteServiceBinding(&chord.RemoteServiceBindingOptions{
		Services:     services,
		Transport:    transport,
		Bound:        &bound,
		AssertAccess: assertAccess,
		OnError:      onError,
	})
	if err != nil {
		return nil, err
	}
	if onActivate == nil {
		onActivate = func(chord.Context) error { return nil }
	}
	return &connRoutedServiceBinding{
		services:   binding,
		getBound:   getBound,
		onActivate: onActivate,
		remove:     remove,
	}, nil
}

func (b *connRoutedServiceBinding) Use(service any) any {
	return b.services.Use(service)
}

func (b *connRoutedServiceBinding) Observe(service any, handler func(service any, call chord.Context) error) func() {
	return b.services.Observe(service, handler)
}

func (b *connRoutedServiceBinding) Ready(ctx chord.Context) error {
	b.mu.Lock()
	needActivate := !b.activated
	if needActivate {
		b.activated = true
	}
	b.mu.Unlock()
	if needActivate {
		if err := b.services.Rebind(b.getBound(), ctx); err != nil {
			return err
		}
	}
	if err := b.ServiceReady(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	needOnActivate := !b.activationComplete
	b.mu.Unlock()
	if needOnActivate {
		if err := b.onActivate(ctx); err != nil {
			return err
		}
		b.mu.Lock()
		b.activationComplete = true
		b.mu.Unlock()
	}
	return nil
}

func (b *connRoutedServiceBinding) ServiceReady(ctx chord.Context) error {
	return b.services.Ready(ctx)
}

func (b *connRoutedServiceBinding) UpdateBound(bound bool, ctx chord.Context) error {
	b.mu.Lock()
	activated := b.activated
	b.mu.Unlock()
	if !activated {
		return nil
	}
	return b.services.Rebind(bound, ctx)
}

func (b *connRoutedServiceBinding) Dispose(ctx chord.Context) error {
	if b.remove != nil {
		b.remove()
	}
	return b.services.Dispose(ctx)
}

type connSerialQueue struct {
	mu   sync.Mutex
	tail chan struct{}
	wg   sync.WaitGroup
}

func (q *connSerialQueue) runSync(fn func()) {
	q.wg.Add(1)
	defer q.wg.Done()

	q.mu.Lock()
	prev := q.tail
	next := make(chan struct{})
	q.tail = next
	q.mu.Unlock()

	if prev != nil {
		<-prev
	}
	defer close(next)
	fn()
}

func (q *connSerialQueue) enqueueAsync(fn func()) {
	q.wg.Add(1)
	q.mu.Lock()
	prev := q.tail
	next := make(chan struct{})
	q.tail = next
	q.mu.Unlock()

	go func() {
		defer q.wg.Done()
		if prev != nil {
			<-prev
		}
		defer close(next)
		fn()
	}()
}

func (q *connSerialQueue) wait() {
	q.wg.Wait()
}

type connServerServiceSourceImpl struct {
	client            *client.Client
	transport         chord.RemoteServiceTransport
	connectionRaw     chord.MutableReplicatedState[chord.JsonValue]
	connectionTyped   chord.ReplicatedState[ServerConnectionState]
	removeConnListen  func()
	onError           func(error)
	transitions       connSerialQueue
	mu                sync.Mutex
	bindings          map[*connRoutedServiceBinding]struct{}
	connectionAttempt int
	disposed          bool
}

// CreateServerServiceSource creates the server-scoped remote service source for one presentation client.
func CreateServerServiceSource(c *client.Client, options ...ServiceSourceOptions) ServerServiceSource {
	var opts ServiceSourceOptions
	if len(options) > 0 {
		opts = options[0]
	}
	onError := opts.OnError
	if onError == nil {
		onError = func(error) {}
	}
	attempt := 0
	if c.ConnectionState() == client.ConnectionStateConnecting {
		attempt = 1
	}
	initialJson, err := connEncodeJsonValue(connToServerConnectionState(c, attempt, nil))
	if err != nil {
		panic(fmt.Sprintf("services: encode initial server connection state: %v", err))
	}
	connState, err := chord.NewReplicatedState(initialJson)
	if err != nil {
		panic(fmt.Sprintf("services: create server connection state: %v", err))
	}
	impl := &connServerServiceSourceImpl{
		client:            c,
		transport:         client.CreateClientServiceTransport(c, func() protocol.RpcTarget { return &protocol.ServerTarget{ServerId: c.ServerId()} }),
		connectionRaw:     connState,
		connectionTyped:   WrapReplicatedState[ServerConnectionState](connState),
		onError:           onError,
		bindings:          make(map[*connRoutedServiceBinding]struct{}),
		connectionAttempt: attempt,
	}
	unsub, _ := c.OnConnectionStateChange(func(change client.ConnectionStateChange) {
		impl.mu.Lock()
		if change.State == client.ConnectionStateConnecting {
			impl.connectionAttempt++
		}
		curAttempt := impl.connectionAttempt
		disposed := impl.disposed
		impl.mu.Unlock()
		if disposed {
			return
		}

		nextState := connToServerConnectionStateWithState(change.State, curAttempt, change.Error)
		_ = connPublishReplacement(impl.connectionRaw, nextState, chord.BackgroundContext)

		impl.transitions.enqueueAsync(func() {
			impl.mu.Lock()
			bindings := make([]*connRoutedServiceBinding, 0, len(impl.bindings))
			for b := range impl.bindings {
				bindings = append(bindings, b)
			}
			impl.mu.Unlock()

			var errs []error
			bound := change.State == client.ConnectionStateConnected
			for _, b := range bindings {
				if err := b.UpdateBound(bound, chord.BackgroundContext); err != nil {
					errs = append(errs, err)
				}
			}
			if len(errs) > 0 {
				impl.onError(errors.Join(errs...))
			}
		})
	})
	impl.removeConnListen = unsub
	return impl
}

func (s *connServerServiceSourceImpl) AcceptsUnavailableServices() bool {
	return false
}

func (s *connServerServiceSourceImpl) Connection() chord.ReplicatedState[ServerConnectionState] {
	return s.connectionTyped
}

func (s *connServerServiceSourceImpl) Catalogue(call chord.Context) ([]chord.ServiceCatalogueEntry, error) {
	var abortCtx = chord.BackgroundContext.AbortSignal()
	if call != nil {
		abortCtx = call.AbortSignal()
	}
	return s.client.ServiceCatalogue(abortCtx, &protocol.ServerTarget{ServerId: s.client.ServerId()})
}

func (s *connServerServiceSourceImpl) Open(options *chord.RemoteServiceSourceOpenOptions) chord.RemoteServices {
	s.mu.Lock()
	disposed := s.disposed
	s.mu.Unlock()
	if disposed {
		panic("Server service source is disposed")
	}
	var holder *connRoutedServiceBinding
	binding, err := connNewRoutedServiceBinding(
		options.Services,
		s.transport,
		func() bool { return s.client.Connected() },
		options.AssertAccess,
		options.OnError,
		nil,
		func() {
			s.mu.Lock()
			delete(s.bindings, holder)
			s.mu.Unlock()
		},
	)
	if err != nil {
		panic(fmt.Sprintf("services: open server service binding: %v", err))
	}
	holder = binding
	s.mu.Lock()
	s.bindings[binding] = struct{}{}
	s.mu.Unlock()
	return binding
}

func (s *connServerServiceSourceImpl) Dispose(ctx chord.Context) error {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return nil
	}
	s.disposed = true
	unsub := s.removeConnListen
	s.removeConnListen = nil
	s.mu.Unlock()

	if unsub != nil {
		unsub()
	}
	s.transitions.wait()

	s.mu.Lock()
	bindings := make([]*connRoutedServiceBinding, 0, len(s.bindings))
	for b := range s.bindings {
		bindings = append(bindings, b)
	}
	clear(s.bindings)
	s.mu.Unlock()

	var errs []error
	for _, b := range bindings {
		if err := b.Dispose(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("Failed to dispose server service source: %w", errors.Join(errs...))
	}
	return nil
}

type connSessionServiceSourceImpl struct {
	client             *client.Client
	transport          chord.RemoteServiceTransport
	attachmentRaw      chord.MutableReplicatedState[chord.JsonValue]
	attachmentTyped    chord.ReplicatedState[SessionAttachmentState]
	removeAttachListen func()
	onError            func(error)
	wg                 sync.WaitGroup
	mu                 sync.Mutex
	bindings           map[*connRoutedServiceBinding]struct{}
	catalogue          []chord.ServiceCatalogueEntry
	hasCatalogue       bool
	attachmentRevision int
	disposed           bool
}

// CreateSessionServiceSource creates the selected-Session remote service source for one presentation client.
func CreateSessionServiceSource(c *client.Client, options ...ServiceSourceOptions) SessionServiceSource {
	var opts ServiceSourceOptions
	if len(options) > 0 {
		opts = options[0]
	}
	onError := opts.OnError
	if onError == nil {
		onError = func(error) {}
	}
	initial := SessionAttachmentState{Status: "detached"}
	if att := c.Attachment(); att != nil {
		initial = SessionAttachmentState{Status: "attaching", SessionId: att.SessionId}
	}
	initialJson, err := connEncodeJsonValue(initial)
	if err != nil {
		panic(fmt.Sprintf("services: encode initial session attachment state: %v", err))
	}
	attachState, err := chord.NewReplicatedState(initialJson)
	if err != nil {
		panic(fmt.Sprintf("services: create session attachment state: %v", err))
	}
	impl := &connSessionServiceSourceImpl{
		client: c,
		transport: client.CreateClientServiceTransport(c, func() protocol.RpcTarget {
			if att := c.Attachment(); att != nil {
				return att
			}
			return nil
		}),
		attachmentRaw:   attachState,
		attachmentTyped: WrapReplicatedState[SessionAttachmentState](attachState),
		onError:         onError,
		bindings:        make(map[*connRoutedServiceBinding]struct{}),
	}
	unsub, _ := c.OnAttachmentChange(func(attachment *protocol.SessionTarget) {
		impl.mu.Lock()
		if impl.disposed {
			impl.mu.Unlock()
			return
		}
		impl.attachmentRevision++
		revision := impl.attachmentRevision
		impl.mu.Unlock()

		if attachment != nil {
			_ = connPublishReplacement(
				impl.attachmentRaw,
				SessionAttachmentState{Status: "attaching", SessionId: attachment.SessionId},
				chord.BackgroundContext,
			)
			impl.wg.Add(1)
			go func(att *protocol.SessionTarget, rev int) {
				defer impl.wg.Done()
				cat, err := impl.client.ServiceCatalogue(chord.BackgroundContext.AbortSignal(), att)
				if err != nil {
					impl.onError(err)
					return
				}
				impl.mu.Lock()
				if impl.attachmentRevision == rev && connSameAttachment(impl.client.Attachment(), att) {
					impl.catalogue = cat
					impl.hasCatalogue = true
				}
				impl.mu.Unlock()
			}(attachment, revision)
		}

		impl.wg.Add(1)
		go func(att *protocol.SessionTarget, rev int) {
			defer impl.wg.Done()
			err := impl.rebind(att != nil, chord.BackgroundContext)
			impl.mu.Lock()
			matches := impl.attachmentRevision == rev && connSameAttachment(impl.client.Attachment(), att)
			impl.mu.Unlock()
			if !matches {
				return
			}
			if err != nil {
				next := SessionAttachmentState{Status: "detached"}
				if att != nil {
					next = SessionAttachmentState{Status: "degraded", SessionId: att.SessionId}
				}
				_ = connPublishReplacement(impl.attachmentRaw, next, chord.BackgroundContext)
				impl.onError(err)
				return
			}
			next := SessionAttachmentState{Status: "detached"}
			if att != nil {
				next = SessionAttachmentState{Status: "attached", SessionId: att.SessionId}
			}
			_ = connPublishReplacement(impl.attachmentRaw, next, chord.BackgroundContext)
		}(attachment, revision)
	})
	impl.removeAttachListen = unsub
	return impl
}

func (s *connSessionServiceSourceImpl) AcceptsUnavailableServices() bool {
	s.mu.Lock()
	hasCatalogue := s.hasCatalogue
	s.mu.Unlock()
	return s.client.Attachment() == nil && !hasCatalogue
}

func (s *connSessionServiceSourceImpl) Attachment() chord.ReplicatedState[SessionAttachmentState] {
	return s.attachmentTyped
}

func (s *connSessionServiceSourceImpl) Catalogue(call chord.Context) ([]chord.ServiceCatalogueEntry, error) {
	target := s.client.Attachment()
	if target == nil {
		s.mu.Lock()
		cat := append([]chord.ServiceCatalogueEntry(nil), s.catalogue...)
		s.mu.Unlock()
		return cat, nil
	}
	abortCtx := chord.BackgroundContext.AbortSignal()
	if call != nil {
		abortCtx = call.AbortSignal()
	}
	cat, err := s.client.ServiceCatalogue(abortCtx, target)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.catalogue = append([]chord.ServiceCatalogueEntry(nil), cat...)
	s.hasCatalogue = true
	s.mu.Unlock()
	return cat, nil
}

func (s *connSessionServiceSourceImpl) Open(options *chord.RemoteServiceSourceOpenOptions) chord.RemoteServices {
	s.mu.Lock()
	disposed := s.disposed
	s.mu.Unlock()
	if disposed {
		panic("Session service source is disposed")
	}
	var holder *connRoutedServiceBinding
	binding, err := connNewRoutedServiceBinding(
		options.Services,
		s.transport,
		func() bool { return s.client.Attachment() != nil },
		options.AssertAccess,
		options.OnError,
		func(ctx chord.Context) error {
			att := s.client.Attachment()
			s.mu.Lock()
			rev := s.attachmentRevision
			s.mu.Unlock()
			if att == nil {
				return s.whenDetached(rev, ctx)
			}
			return s.whenAttached(att, rev, ctx)
		},
		func() {
			s.mu.Lock()
			delete(s.bindings, holder)
			s.mu.Unlock()
		},
	)
	if err != nil {
		panic(fmt.Sprintf("services: open session service binding: %v", err))
	}
	holder = binding
	s.mu.Lock()
	s.bindings[binding] = struct{}{}
	s.mu.Unlock()
	return binding
}

func (s *connSessionServiceSourceImpl) WhenAttached(sessionId string, ctx chord.Context) error {
	att := s.client.Attachment()
	if att == nil || att.SessionId != sessionId {
		return fmt.Errorf("Session %s is not the current attachment", sessionId)
	}
	s.mu.Lock()
	rev := s.attachmentRevision
	s.mu.Unlock()
	return s.whenAttached(att, rev, ctx)
}

func (s *connSessionServiceSourceImpl) WhenDetached(ctx chord.Context) error {
	if s.client.Attachment() != nil {
		return errors.New("A Session is still attached")
	}
	s.mu.Lock()
	rev := s.attachmentRevision
	s.mu.Unlock()
	return s.whenDetached(rev, ctx)
}

func (s *connSessionServiceSourceImpl) Dispose(ctx chord.Context) error {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return nil
	}
	s.disposed = true
	unsub := s.removeAttachListen
	s.removeAttachListen = nil
	s.mu.Unlock()

	if unsub != nil {
		unsub()
	}
	s.wg.Wait()

	s.mu.Lock()
	bindings := make([]*connRoutedServiceBinding, 0, len(s.bindings))
	for b := range s.bindings {
		bindings = append(bindings, b)
	}
	clear(s.bindings)
	s.mu.Unlock()

	var errs []error
	for _, b := range bindings {
		if err := b.Dispose(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("Failed to dispose Session service source: %w", errors.Join(errs...))
	}
	return nil
}

func (s *connSessionServiceSourceImpl) snapshotBindings() []*connRoutedServiceBinding {
	s.mu.Lock()
	out := make([]*connRoutedServiceBinding, 0, len(s.bindings))
	for b := range s.bindings {
		out = append(out, b)
	}
	s.mu.Unlock()
	return out
}

func (s *connSessionServiceSourceImpl) rebind(bound bool, ctx chord.Context) error {
	var errs []error
	for _, b := range s.snapshotBindings() {
		if err := b.UpdateBound(bound, ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("Failed to rebind Session services: %w", errors.Join(errs...))
	}
	return nil
}

func (s *connSessionServiceSourceImpl) whenAttached(attachment *protocol.SessionTarget, revision int, ctx chord.Context) error {
	bindings := s.snapshotBindings()
	var errs []error
	for _, b := range bindings {
		if err := b.ServiceReady(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		s.mu.Lock()
		matches := s.attachmentRevision == revision && connSameAttachment(s.client.Attachment(), attachment)
		s.mu.Unlock()
		if matches {
			_ = connPublishReplacement(s.attachmentRaw, SessionAttachmentState{Status: "degraded", SessionId: attachment.SessionId}, ctx)
		}
		return errors.Join(errs...)
	}
	s.mu.Lock()
	matches := s.attachmentRevision == revision && connSameAttachment(s.client.Attachment(), attachment)
	s.mu.Unlock()
	if !matches {
		return fmt.Errorf("Session %s was replaced while attaching", attachment.SessionId)
	}
	state, ok := s.attachmentTyped.Value()
	if !ok || state.Status != "attached" || state.SessionId != attachment.SessionId {
		_ = connPublishReplacement(s.attachmentRaw, SessionAttachmentState{Status: "attached", SessionId: attachment.SessionId}, ctx)
	}
	return nil
}

func (s *connSessionServiceSourceImpl) whenDetached(revision int, ctx chord.Context) error {
	for _, b := range s.snapshotBindings() {
		if err := b.ServiceReady(ctx); err != nil {
			return err
		}
	}
	s.mu.Lock()
	matches := s.attachmentRevision == revision && s.client.Attachment() == nil
	s.mu.Unlock()
	if !matches {
		return errors.New("The Session attachment changed while detaching")
	}
	state, ok := s.attachmentTyped.Value()
	if !ok || state.Status != "detached" {
		_ = connPublishReplacement(s.attachmentRaw, SessionAttachmentState{Status: "detached"}, ctx)
	}
	return nil
}

func connPublishReplacement(state chord.MutableReplicatedState[chord.JsonValue], value any, ctx chord.Context) error {
	encoded, err := connEncodeJsonValue(value)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = chord.BackgroundContext
	}
	return state.Replace(ctx, encoded)
}

func connToServerConnectionState(c *client.Client, attempt int, err error) ServerConnectionState {
	return connToServerConnectionStateWithState(c.ConnectionState(), attempt, err)
}

func connToServerConnectionStateWithState(st client.ConnectionState, attempt int, err error) ServerConnectionState {
	since := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	switch st {
	case client.ConnectionStateConnecting:
		att := attempt
		return ServerConnectionState{Status: "connecting", Attempt: &att}
	case client.ConnectionStateConnected:
		return ServerConnectionState{Status: "connected", Since: &since}
	default:
		reason := "Client is disconnected"
		if err != nil {
			reason = err.Error()
		}
		return ServerConnectionState{Status: "disconnected", Since: &since, Reason: &reason, RetryAt: nil}
	}
}

func connSameAttachment(left, right *protocol.SessionTarget) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ServerId == right.ServerId &&
		left.SessionId == right.SessionId &&
		left.AttachmentId == right.AttachmentId
}
