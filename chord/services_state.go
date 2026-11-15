// Ported from packages/chord/src/services/state.ts (pi v1.0.0).

package chord

import (
	"errors"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/omap"
)

const ssPendingCap = 100

var ssAsyncErrorHook func(error)

// ssStateListener is one subscriber callback. A non-nil error is a thrown listener failure.
// Synchronous callbacks that return an ignored value in TS are plain callbacks here.
type ssStateListener[T any] func(value T, call Context, delivery ReplicatedStateDelivery) error

type ssStateDelivery[T any] struct {
	value    T
	context  Context
	delivery ReplicatedStateDelivery
}

// ssStateSubscriber is one public subscription, independent of producer and other subscriber progress.
type ssStateSubscriber[T any] struct {
	mu          sync.Mutex
	listener    ssStateListener[T]
	reportError func(error)
	pending     []ssStateDelivery[T]
	running     bool
	started     bool
	closed      bool
}

func ssNewStateSubscriber[T any](listener ssStateListener[T], reportError func(error)) *ssStateSubscriber[T] {
	return &ssStateSubscriber[T]{
		listener:    listener,
		reportError: reportError,
	}
}

func (s *ssStateSubscriber[T]) push(frame ssStateDelivery[T]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if len(s.pending) == ssPendingCap {
		// A cold replica can queue updates reentrantly before this subscriber's first hydration starts.
		if !s.started {
			hydration := s.pending[0]
			s.pending = []ssStateDelivery[T]{hydration, frame}
			return
		}
		s.pending = []ssStateDelivery[T]{frame}
		return
	}
	s.pending = append(s.pending, frame)
}

func (s *ssStateSubscriber[T]) drain() {
	s.mu.Lock()
	if s.running || s.closed {
		s.mu.Unlock()
		return
	}
	s.running = true
	for {
		if s.closed || len(s.pending) == 0 {
			s.running = false
			s.mu.Unlock()
			return
		}
		frame := s.pending[0]
		copy(s.pending, s.pending[1:])
		s.pending[len(s.pending)-1] = ssStateDelivery[T]{}
		s.pending = s.pending[:len(s.pending)-1]
		s.started = true
		listener := s.listener
		s.mu.Unlock()

		if listener != nil {
			if err := listener(frame.value, frame.context, frame.delivery); err != nil {
				s.report(err)
			}
		}
		s.mu.Lock()
	}
}

func (s *ssStateSubscriber[T]) clear() {
	s.mu.Lock()
	s.pending = nil
	s.mu.Unlock()
}

func (s *ssStateSubscriber[T]) close() {
	s.mu.Lock()
	s.closed = true
	s.pending = nil
	s.mu.Unlock()
}

func (s *ssStateSubscriber[T]) resume() {
	s.drain()
}

func (s *ssStateSubscriber[T]) report(value any) {
	if value == nil {
		return
	}
	err := ssToError(value)
	if s.reportError != nil {
		s.reportError(err)
		return
	}
	ssReportErrorAsync(err)
}

// ssSourceListener receives source operations. Function values are not comparable.
type ssSourceListener func(ops OpList, sequence int, call Context) error

type ssSourceEntry struct {
	id uint64
	fn ssSourceListener
}

type ssPublication[T any] struct {
	value    T
	ops      OpList
	sequence int
	context  Context
}

// ssReplicatedStatePublisher maintains local publication order independently of how revisions are produced.
type ssReplicatedStatePublisher[T any] struct {
	mu          sync.Mutex
	listeners   *omap.Map[*ssStateSubscriber[T], int]
	reportError func(error)
	// sourceEntries is insertion-ordered. Func values cannot be omap.Set keys.
	sourceEntries []ssSourceEntry
	nextSourceID  uint64
	publications  []ssPublication[T]
	value         T
	sequence      int
	delivering    bool
}

func ssNewReplicatedStatePublisher[T any](initial T, reportError func(error)) *ssReplicatedStatePublisher[T] {
	if reportError == nil {
		reportError = ssReportErrorAsync
	}
	return &ssReplicatedStatePublisher[T]{
		listeners:   omap.NewMap[*ssStateSubscriber[T], int](),
		reportError: reportError,
		value:       initial,
	}
}

func (p *ssReplicatedStatePublisher[T]) Value() T {
	p.mu.Lock()
	v := p.value
	p.mu.Unlock()
	return v
}

func (p *ssReplicatedStatePublisher[T]) snapshot() (value T, sequence int) {
	p.mu.Lock()
	value = p.value
	sequence = p.sequence
	p.mu.Unlock()
	return value, sequence
}

func (p *ssReplicatedStatePublisher[T]) subscribe(listener ssStateListener[T]) func() {
	sub := ssNewStateSubscriber(listener, p.reportError)
	p.mu.Lock()
	value := p.value
	sequence := p.sequence
	p.listeners.Set(sub, sequence)
	sub.push(ssStateDelivery[T]{
		value:    value,
		context:  ServiceDeliveryContext(),
		delivery: ReplicatedStateDelivery{Kind: ReplicatedStateDeliveryKindHydrate, Sequence: sequence},
	})
	p.mu.Unlock()

	sub.drain()

	return func() {
		sub.close()
		p.mu.Lock()
		p.listeners.Delete(sub)
		p.mu.Unlock()
	}
}

func (p *ssReplicatedStatePublisher[T]) subscribeSource(listener ssSourceListener) func() {
	p.mu.Lock()
	p.nextSourceID++
	id := p.nextSourceID
	p.sourceEntries = append(p.sourceEntries, ssSourceEntry{id: id, fn: listener})
	p.mu.Unlock()

	return func() {
		p.mu.Lock()
		for i, entry := range p.sourceEntries {
			if entry.id == id {
				copy(p.sourceEntries[i:], p.sourceEntries[i+1:])
				p.sourceEntries[len(p.sourceEntries)-1] = ssSourceEntry{}
				p.sourceEntries = p.sourceEntries[:len(p.sourceEntries)-1]
				break
			}
		}
		p.mu.Unlock()
	}
}

// enqueue appends a prepared revision and returns true if this caller became the active deliverer.
func (p *ssReplicatedStatePublisher[T]) enqueue(value T, ops OpList, call Context) bool {
	p.mu.Lock()
	p.value = value
	p.sequence++
	p.publications = append(p.publications, ssPublication[T]{
		value:    value,
		ops:      ops,
		sequence: p.sequence,
		context:  call,
	})
	if p.delivering {
		p.mu.Unlock()
		return false
	}
	p.delivering = true
	p.mu.Unlock()
	return true
}

func (p *ssReplicatedStatePublisher[T]) drainPublications() []any {
	var errs []any
	deliveringCleared := false
	defer func() {
		if !deliveringCleared {
			p.mu.Lock()
			p.delivering = false
			p.mu.Unlock()
		}
	}()

	var toDrain []*ssStateSubscriber[T]
	seenDrain := map[*ssStateSubscriber[T]]struct{}{}

	for {
		p.mu.Lock()
		if len(p.publications) == 0 {
			p.delivering = false
			deliveringCleared = true
			p.mu.Unlock()
			break
		}
		pub := p.publications[0]
		copy(p.publications, p.publications[1:])
		p.publications[len(p.publications)-1] = ssPublication[T]{}
		p.publications = p.publications[:len(p.publications)-1]

		srcSnap := make([]ssSourceListener, len(p.sourceEntries))
		for i, entry := range p.sourceEntries {
			srcSnap[i] = entry.fn
		}
		type subEntry struct {
			sub *ssStateSubscriber[T]
			seq int
		}
		subSnap := make([]subEntry, 0, p.listeners.Len())
		for sub, hydratedSeq := range p.listeners.All() {
			subSnap = append(subSnap, subEntry{sub: sub, seq: hydratedSeq})
		}
		p.mu.Unlock()

		for _, listener := range srcSnap {
			if listener == nil {
				continue
			}
			if err := listener(pub.ops, pub.sequence, pub.context); err != nil {
				errs = append(errs, err)
			}
		}
		delivery := ReplicatedStateDelivery{
			Kind:     ReplicatedStateDeliveryKindUpdate,
			Sequence: pub.sequence,
		}
		for _, entry := range subSnap {
			if pub.sequence <= entry.seq {
				continue
			}
			entry.sub.push(ssStateDelivery[T]{
				value:    pub.value,
				context:  pub.context,
				delivery: delivery,
			})
			if _, ok := seenDrain[entry.sub]; !ok {
				seenDrain[entry.sub] = struct{}{}
				toDrain = append(toDrain, entry.sub)
			}
		}
	}

	for _, sub := range toDrain {
		sub.drain()
	}
	return errs
}

// publish publishes an already-prepared immutable revision and returns isolated listener failures.
func (p *ssReplicatedStatePublisher[T]) publish(value T, ops OpList, call Context) []any {
	if !p.enqueue(value, ops, call) {
		return nil
	}
	return p.drainPublications()
}

type ssPublisherInternals[T any] struct {
	pub *ssReplicatedStatePublisher[T]
}

func (i *ssPublisherInternals[T]) Snapshot() (any, int) {
	return i.pub.snapshot()
}

func (i *ssPublisherInternals[T]) Subscribe(listener func(ops OpList, sequence int, call Context) error) func() {
	return i.pub.subscribeSource(listener)
}

// MutableReplicatedStateImpl is the authoritative mutable replicated state.
type MutableReplicatedStateImpl[T any] struct {
	mu        sync.Mutex
	tracker   Tracker[T]
	publisher *ssReplicatedStatePublisher[T]
	changing  bool
}

// NewMutableReplicatedStateImpl takes ownership of an alias-free strict-JSON root.
func NewMutableReplicatedStateImpl[T any](initial T) *MutableReplicatedStateImpl[T] {
	tracker := Track(initial)
	pub := ssNewReplicatedStatePublisher(tracker.Value(), ssReportErrorAsync)
	impl := &MutableReplicatedStateImpl[T]{
		tracker:   tracker,
		publisher: pub,
	}
	RegisterReplicatedStateInternals(impl, &ssPublisherInternals[T]{pub: pub})
	return impl
}

func (m *MutableReplicatedStateImpl[T]) Value() (T, bool) {
	m.mu.Lock()
	v := m.tracker.Value()
	m.mu.Unlock()
	return v, true
}

func (m *MutableReplicatedStateImpl[T]) Change(call Context, mutate func(draft Draft[T]) error) error {
	m.mu.Lock()
	if m.changing {
		m.mu.Unlock()
		return errors.New("Replicated state cannot be changed reentrantly from a change callback")
	}
	m.changing = true
	change := m.tracker.BeginChange()
	draft, err := change.State()
	if err != nil {
		change.Abort()
		m.changing = false
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	if mutate == nil {
		m.mu.Lock()
		change.Abort()
		m.changing = false
		m.mu.Unlock()
		return js.NewTypeError("Replicated state change callback must be a function")
	}

	mutateErr := mutate(draft)

	m.mu.Lock()
	defer func() {
		if m.changing {
			m.changing = false
		}
	}()
	if mutateErr != nil {
		change.Abort()
		m.changing = false
		m.mu.Unlock()
		return mutateErr
	}
	prepared, err := change.Prepare()
	if err != nil {
		change.Abort()
		m.changing = false
		m.mu.Unlock()
		return err
	}
	if err := m.tracker.Adopt(prepared); err != nil {
		m.changing = false
		m.mu.Unlock()
		return err
	}
	m.changing = false
	m.mu.Unlock()

	ops := OpList(prepared.Ops())
	if len(ops) == 0 {
		return nil
	}
	errs := m.publisher.publish(prepared.Value(), ops, call)
	return ssThrowCollectedErrors(errs, "Replicated state listeners failed")
}

func (m *MutableReplicatedStateImpl[T]) Replace(call Context, value T) error {
	m.mu.Lock()
	if m.changing {
		m.mu.Unlock()
		return errors.New("Replicated state cannot be replaced from a change callback")
	}
	prepared, err := m.tracker.PrepareReplace(value)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if err := m.tracker.Adopt(prepared); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	ops := OpList(prepared.Ops())
	if len(ops) == 0 {
		return nil
	}
	errs := m.publisher.publish(prepared.Value(), ops, call)
	return ssThrowCollectedErrors(errs, "Replicated state listeners failed")
}

func (m *MutableReplicatedStateImpl[T]) Subscribe(listener func(value T, call Context, delivery ReplicatedStateDelivery) error) (unsubscribe func()) {
	return m.publisher.subscribe(listener)
}

// ssAttachedReplicatedStateImpl is publication-only state backed by one source attachment.
type ssAttachedReplicatedStateImpl[T any] struct {
	mu          sync.Mutex
	publisher   *ssReplicatedStatePublisher[T]
	attachment  ReplicatedStateSourceAttachment[T]
	reportError func(error)
	cursor      int
	disposed    bool
}

func ssNewAttachedReplicatedStateImpl[T any](attachment ReplicatedStateSourceAttachment[T], options *ReplicatedStateSourceOptions) *ssAttachedReplicatedStateImpl[T] {
	snap := attachment.Snapshot()
	var reportError func(error)
	if options != nil && options.OnError != nil {
		reportError = options.OnError
	} else {
		reportError = ssReportErrorAsync
	}
	impl := &ssAttachedReplicatedStateImpl[T]{
		attachment:  attachment,
		reportError: reportError,
		cursor:      snap.Cursor,
	}
	impl.publisher = ssNewReplicatedStatePublisher(snap.Value, func(err error) {
		impl.report(err)
	})
	RegisterReplicatedStateInternals(impl, &ssPublisherInternals[T]{pub: impl.publisher})
	return impl
}

func (s *ssAttachedReplicatedStateImpl[T]) Value() (T, bool) {
	return s.publisher.Value(), true
}

func (s *ssAttachedReplicatedStateImpl[T]) Subscribe(listener func(value T, call Context, delivery ReplicatedStateDelivery) error) (unsubscribe func()) {
	return s.publisher.subscribe(listener)
}

func (s *ssAttachedReplicatedStateImpl[T]) activate() {
	s.attachment.Activate(func(frame ReplicatedStateSourceFrame[T]) {
		s.receive(frame)
	})
}

func (s *ssAttachedReplicatedStateImpl[T]) Dispose() {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return
	}
	s.disposed = true
	att := s.attachment
	s.mu.Unlock()
	att.Dispose()
}

func (s *ssAttachedReplicatedStateImpl[T]) receive(frame ReplicatedStateSourceFrame[T]) {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return
	}
	if err := ssAssertCursor(frame.Cursor, "frame"); err != nil {
		s.failLocked(err)
		return
	}
	expected := s.cursor + 1
	if frame.Cursor != expected {
		s.failLocked(fmt.Errorf("Replicated state source cursor has a gap: expected %d, received %d", expected, frame.Cursor))
		return
	}
	s.cursor = frame.Cursor
	becameDeliverer := s.publisher.enqueue(frame.Value, frame.Ops, frame.Context)
	s.mu.Unlock()

	if !becameDeliverer {
		return
	}
	errs := s.publisher.drainPublications()
	if len(errs) == 1 {
		s.report(errs[0])
	} else if len(errs) > 1 {
		s.report(ssJoinErrors("Replicated state listeners failed", errs))
	}
}

func (s *ssAttachedReplicatedStateImpl[T]) failLocked(err error) {
	if s.disposed {
		s.mu.Unlock()
		return
	}
	s.disposed = true
	att := s.attachment
	s.mu.Unlock()
	att.Dispose()
	s.report(err)
}

func (s *ssAttachedReplicatedStateImpl[T]) fail(err error) {
	s.mu.Lock()
	s.failLocked(err)
}

func (s *ssAttachedReplicatedStateImpl[T]) report(value any) {
	if value == nil || s.reportError == nil {
		return
	}
	s.reportError(ssToError(value))
}

// AttachReplicatedStateSource attaches a publication-only replicated state to one authoritative immutable source stream.
// A nil options pointer means the TS default {}.
func AttachReplicatedStateSource[T any](source ReplicatedStateSource[T], options *ReplicatedStateSourceOptions) (AttachedReplicatedState[T], error) {
	attachment := source.Attach()
	snap := attachment.Snapshot()
	if err := ssAssertCursor(snap.Cursor, "snapshot"); err != nil {
		attachment.Dispose()
		return nil, err
	}
	state := ssNewAttachedReplicatedStateImpl(attachment, options)
	state.activate()
	return state, nil
}

// ReplicatedStateReplica is a cold read-only state used by service consumers until a complete snapshot arrives.
// T is a JsonValue. Value's boolean is false until hydration, distinct from a present JSON null.
type ReplicatedStateReplica[T any] struct {
	mu          sync.Mutex
	listeners   *omap.Set[*ssStateSubscriber[T]]
	reportError func(error)
	validator   *JsonRevisionValidator
	value       T
	ready       bool
	sequence    *int
}

// NewReplicatedStateReplica builds an empty replica. reportError receives listener and validation failures.
func NewReplicatedStateReplica[T any](reportError func(error)) *ReplicatedStateReplica[T] {
	return &ReplicatedStateReplica[T]{
		listeners:   omap.NewSet[*ssStateSubscriber[T]](),
		reportError: reportError,
		validator:   NewJsonRevisionValidator(),
	}
}

func (r *ReplicatedStateReplica[T]) Value() (T, bool) {
	r.mu.Lock()
	v, ok := r.value, r.ready
	r.mu.Unlock()
	return v, ok
}

func (r *ReplicatedStateReplica[T]) Subscribe(listener func(value T, call Context, delivery ReplicatedStateDelivery) error) (unsubscribe func()) {
	sub := ssNewStateSubscriber(listener, r.reportError)
	r.mu.Lock()
	r.listeners.Add(sub)
	hydrated := r.ready && r.sequence != nil
	if hydrated {
		sub.push(ssStateDelivery[T]{
			value:    r.value,
			context:  ServiceDeliveryContext(),
			delivery: ReplicatedStateDelivery{Kind: ReplicatedStateDeliveryKindHydrate, Sequence: *r.sequence},
		})
	}
	r.mu.Unlock()

	if hydrated {
		sub.drain()
	}

	return func() {
		sub.close()
		r.mu.Lock()
		r.listeners.Delete(sub)
		r.mu.Unlock()
	}
}

func (r *ReplicatedStateReplica[T]) Hydrate(sequence int, ops OpList, call Context) error {
	r.mu.Lock()
	if !IsBase(ops) {
		r.clearLocked()
		r.mu.Unlock()
		return errors.New("Replicated state snapshot is not a base operation batch")
	}
	applied, err := ApplyImmutable[any](nil, ops)
	if err != nil {
		r.clearLocked()
		r.mu.Unlock()
		return err
	}
	validated, err := r.validator.Validate(applied)
	if err != nil {
		r.clearLocked()
		r.mu.Unlock()
		return err
	}
	var next T
	if validated != nil {
		typed, ok := validated.(T)
		if !ok {
			r.clearLocked()
			r.mu.Unlock()
			return js.NewTypeError("replicated state value has incompatible type")
		}
		next = typed
	}
	seq := sequence
	r.sequence = &seq
	r.value = next
	r.ready = true
	subs := r.enqueueAllLocked(call, ReplicatedStateDelivery{Kind: ReplicatedStateDeliveryKindHydrate, Sequence: sequence})
	r.mu.Unlock()

	for _, sub := range subs {
		sub.drain()
	}
	return nil
}

func (r *ReplicatedStateReplica[T]) Update(sequence int, ops OpList, call Context) error {
	r.mu.Lock()
	if !r.ready || r.sequence == nil {
		r.mu.Unlock()
		return errors.New("Replicated state received an update before hydration")
	}
	if sequence != *r.sequence+1 {
		r.clearLocked()
		r.mu.Unlock()
		return errors.New("Replicated state update sequence has a gap")
	}
	applied, err := ApplyImmutable[any](r.value, ops)
	if err != nil {
		r.clearLocked()
		r.mu.Unlock()
		return err
	}
	validated, err := r.validator.Validate(applied)
	if err != nil {
		r.clearLocked()
		r.mu.Unlock()
		return err
	}
	var next T
	if validated != nil {
		typed, ok := validated.(T)
		if !ok {
			r.clearLocked()
			r.mu.Unlock()
			return js.NewTypeError("replicated state value has incompatible type")
		}
		next = typed
	}
	seq := sequence
	r.sequence = &seq
	r.value = next
	subs := r.enqueueAllLocked(call, ReplicatedStateDelivery{Kind: ReplicatedStateDeliveryKindUpdate, Sequence: sequence})
	r.mu.Unlock()

	for _, sub := range subs {
		sub.drain()
	}
	return nil
}

func (r *ReplicatedStateReplica[T]) Clear() {
	r.mu.Lock()
	r.clearLocked()
	r.mu.Unlock()
}

func (r *ReplicatedStateReplica[T]) clearLocked() {
	var zero T
	r.value = zero
	r.ready = false
	r.sequence = nil
	for sub := range r.listeners.All() {
		sub.clear()
	}
}

func (r *ReplicatedStateReplica[T]) enqueueAllLocked(context Context, delivery ReplicatedStateDelivery) []*ssStateSubscriber[T] {
	if !r.ready {
		return nil
	}
	frame := ssStateDelivery[T]{
		value:    r.value,
		context:  context,
		delivery: delivery,
	}
	subs := make([]*ssStateSubscriber[T], 0, r.listeners.Len())
	for sub := range r.listeners.All() {
		subs = append(subs, sub)
		sub.push(frame)
	}
	return subs
}

func (r *ReplicatedStateReplica[T]) deliverAll(context Context, delivery ReplicatedStateDelivery) {
	r.mu.Lock()
	subs := r.enqueueAllLocked(context, delivery)
	r.mu.Unlock()
	for _, sub := range subs {
		sub.drain()
	}
}

// ServiceDeliveryContext is the context for synthetic service deliveries without a caller.
func ServiceDeliveryContext() Context {
	return BackgroundContext
}

func ssAssertCursor(cursor int, kind string) error {
	if cursor < -9007199254740991 || cursor > 9007199254740991 {
		return js.NewTypeError(fmt.Sprintf("Replicated state source %s cursor must be a safe integer", kind))
	}
	return nil
}

func ssIsPromiseLike(value any) bool {
	return false
}

func ssJoinErrors(message string, errs []any) error {
	converted := make([]error, len(errs))
	for i, e := range errs {
		converted[i] = ssToError(e)
	}
	return fmt.Errorf("%s: %w", message, errors.Join(converted...))
}

func ssThrowCollectedErrors(errs []any, message string) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return ssToError(errs[0])
	}
	return ssJoinErrors(message, errs)
}

func ssReportErrorAsync(err error) {
	if ssAsyncErrorHook != nil && err != nil {
		ssAsyncErrorHook(err)
	}
}

func ssToError(value any) error {
	if err, ok := value.(error); ok && err != nil {
		return err
	}
	return fmt.Errorf("%v", value)
}
