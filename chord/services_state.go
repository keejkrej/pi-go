// Ported from packages/chord/src/services/state.ts (pi v1.0.0).

package chord

import (
	"sync"

	"github.com/keejkrej/pi-go/internal/omap"
)

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
	panic("unported: ssNewStateSubscriber")
}

func (s *ssStateSubscriber[T]) push(frame ssStateDelivery[T]) {
	panic("unported: push")
}

func (s *ssStateSubscriber[T]) drain() {
	panic("unported: drain")
}

func (s *ssStateSubscriber[T]) clear() {
	panic("unported: clear")
}

func (s *ssStateSubscriber[T]) close() {
	panic("unported: close")
}

func (s *ssStateSubscriber[T]) resume() {
	panic("unported: resume")
}

func (s *ssStateSubscriber[T]) report(value any) {
	panic("unported: report")
}

// ssSourceListener receives source operations. Function values are not comparable.
type ssSourceListener func(ops OpList, sequence int, call Context) error

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
	// sourceListeners is insertion-ordered. Func values cannot be omap.Set keys.
	sourceListeners []ssSourceListener
	publications    []ssPublication[T]
	value           T
	sequence        int
	delivering      bool
}

func ssNewReplicatedStatePublisher[T any](initial T, reportError func(error)) *ssReplicatedStatePublisher[T] {
	panic("unported: ssNewReplicatedStatePublisher")
}

func (p *ssReplicatedStatePublisher[T]) Value() T {
	return p.value
}

func (p *ssReplicatedStatePublisher[T]) snapshot() (value T, sequence int) {
	panic("unported: snapshot")
}

func (p *ssReplicatedStatePublisher[T]) subscribe(listener ssStateListener[T]) func() {
	panic("unported: subscribe")
}

func (p *ssReplicatedStatePublisher[T]) subscribeSource(listener ssSourceListener) func() {
	panic("unported: subscribeSource")
}

// publish publishes an already-prepared immutable revision and returns isolated listener failures.
func (p *ssReplicatedStatePublisher[T]) publish(value T, ops OpList, call Context) []any {
	panic("unported: publish")
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
	panic("unported: NewMutableReplicatedStateImpl")
}

func (m *MutableReplicatedStateImpl[T]) Value() (T, bool) {
	return m.tracker.Value(), true
}

func (m *MutableReplicatedStateImpl[T]) Change(call Context, mutate func(draft Draft[T]) error) error {
	panic("unported: Change")
}

func (m *MutableReplicatedStateImpl[T]) Replace(call Context, value T) error {
	panic("unported: Replace")
}

func (m *MutableReplicatedStateImpl[T]) Subscribe(listener func(value T, call Context, delivery ReplicatedStateDelivery) error) (unsubscribe func()) {
	panic("unported: Subscribe")
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
	panic("unported: ssNewAttachedReplicatedStateImpl")
}

func (s *ssAttachedReplicatedStateImpl[T]) Value() (T, bool) {
	return s.publisher.Value(), true
}

func (s *ssAttachedReplicatedStateImpl[T]) Subscribe(listener func(value T, call Context, delivery ReplicatedStateDelivery) error) (unsubscribe func()) {
	panic("unported: Subscribe")
}

func (s *ssAttachedReplicatedStateImpl[T]) activate() {
	panic("unported: activate")
}

func (s *ssAttachedReplicatedStateImpl[T]) Dispose() {
	panic("unported: Dispose")
}

func (s *ssAttachedReplicatedStateImpl[T]) receive(frame ReplicatedStateSourceFrame[T]) {
	panic("unported: receive")
}

func (s *ssAttachedReplicatedStateImpl[T]) fail(err error) {
	panic("unported: fail")
}

func (s *ssAttachedReplicatedStateImpl[T]) report(value any) {
	panic("unported: report")
}

// AttachReplicatedStateSource attaches a publication-only replicated state to one authoritative immutable source stream.
// A nil options pointer means the TS default {}.
func AttachReplicatedStateSource[T any](source ReplicatedStateSource[T], options *ReplicatedStateSourceOptions) (AttachedReplicatedState[T], error) {
	panic("unported: AttachReplicatedStateSource")
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
	panic("unported: NewReplicatedStateReplica")
}

func (r *ReplicatedStateReplica[T]) Value() (T, bool) {
	return r.value, r.ready
}

func (r *ReplicatedStateReplica[T]) Subscribe(listener func(value T, call Context, delivery ReplicatedStateDelivery) error) (unsubscribe func()) {
	panic("unported: Subscribe")
}

func (r *ReplicatedStateReplica[T]) Hydrate(sequence int, ops OpList, call Context) error {
	panic("unported: Hydrate")
}

func (r *ReplicatedStateReplica[T]) Update(sequence int, ops OpList, call Context) error {
	panic("unported: Update")
}

func (r *ReplicatedStateReplica[T]) Clear() {
	panic("unported: Clear")
}

func (r *ReplicatedStateReplica[T]) deliverAll(context Context, delivery ReplicatedStateDelivery) {
	panic("unported: deliverAll")
}

// ServiceDeliveryContext is the context for synthetic service deliveries without a caller.
func ServiceDeliveryContext() Context {
	return BackgroundContext
}

func ssAssertCursor(cursor int, kind string) error {
	panic("unported: ssAssertCursor")
}

func ssIsPromiseLike(value any) bool {
	panic("unported: ssIsPromiseLike")
}

func ssThrowCollectedErrors(errs []any, message string) error {
	panic("unported: ssThrowCollectedErrors")
}

func ssReportErrorAsync(err error) {
	panic("unported: ssReportErrorAsync")
}

func ssToError(value any) error {
	panic("unported: ssToError")
}
