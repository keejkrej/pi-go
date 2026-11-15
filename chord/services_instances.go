// Ported from packages/chord/src/services/instances.ts (pi v1.0.0).

package chord

import (
	"fmt"
	"iter"
	"reflect"
	"sync"

	"github.com/keejkrej/pi-go/internal/omap"
)

type InstanceDirectoryEntry interface {
	Key() string
	Generation() int
	Service() any
	Deactivate()
}

type InstanceDirectoryOptions struct {
	Ready   bool        `json:"ready"`
	OnError func(error) `json:"onError"`
}

type siObserverTask struct {
	cancel func(reason any)
}

type siObserver struct {
	handler func(service any, context Context) error
	// tasks is keyed by InstanceDirectoryEntry.Key. A live key belongs to one entry.
	tasks  *omap.Map[string, siObserverTask]
	closed bool
}

type siPendingStart struct {
	handler func(service any, context Context) error
	service any
	context Context
}

// InstanceDirectory owns keyed instance lifetime and the cancellable tasks observing those instances.
type InstanceDirectory[TEntry InstanceDirectoryEntry] struct {
	mu          sync.Mutex
	entries     *omap.Map[string, TEntry]
	observers   *omap.Set[*siObserver]
	reportError func(error)
	ready       bool
	disposed    bool
}

func NewInstanceDirectory[TEntry InstanceDirectoryEntry](options *InstanceDirectoryOptions) *InstanceDirectory[TEntry] {
	var ready bool
	var onError func(error)
	if options != nil {
		ready = options.Ready
		onError = options.OnError
	}
	return &InstanceDirectory[TEntry]{
		entries:     omap.NewMap[string, TEntry](),
		observers:   omap.NewSet[*siObserver](),
		reportError: onError,
		ready:       ready,
	}
}

func (d *InstanceDirectory[TEntry]) ObserverCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.observers.Len()
}

func (d *InstanceDirectory[TEntry]) Values() iter.Seq[TEntry] {
	d.mu.Lock()
	snapshot := make([]TEntry, 0, d.entries.Len())
	for _, entry := range d.entries.All() {
		snapshot = append(snapshot, entry)
	}
	d.mu.Unlock()
	return func(yield func(TEntry) bool) {
		for _, entry := range snapshot {
			if !yield(entry) {
				return
			}
		}
	}
}

func (d *InstanceDirectory[TEntry]) Get(key string) (TEntry, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.entries.Get(key)
}

func (d *InstanceDirectory[TEntry]) Insert(entry TEntry) error {
	d.mu.Lock()
	if err := d.assertActive(); err != nil {
		d.mu.Unlock()
		return err
	}
	if d.entries.Has(entry.Key()) {
		d.mu.Unlock()
		return fmt.Errorf("Keyed service already has a live instance with key %s", entry.Key())
	}
	d.entries.Set(entry.Key(), entry)
	var starts []siPendingStart
	if d.ready {
		starts = d.prepareStartAllLocked(entry)
	}
	d.mu.Unlock()

	d.runStarts(starts)
	return nil
}

func (d *InstanceDirectory[TEntry]) Replace(entry TEntry) error {
	d.mu.Lock()
	if err := d.assertActive(); err != nil {
		d.mu.Unlock()
		return err
	}
	var deactivate func()
	var cancels []func(any)
	if previous, ok := d.entries.Get(entry.Key()); ok {
		if previous.Generation() == entry.Generation() {
			d.mu.Unlock()
			return fmt.Errorf("Keyed service repeated a live generation")
		}
		deactivate, cancels = d.removeLocked(previous)
	}
	d.entries.Set(entry.Key(), entry)
	var starts []siPendingStart
	if d.ready {
		starts = d.prepareStartAllLocked(entry)
	}
	d.mu.Unlock()

	if deactivate != nil {
		deactivate()
	}
	for _, cancel := range cancels {
		cancel(nil)
	}
	d.runStarts(starts)
	return nil
}

func (d *InstanceDirectory[TEntry]) Remove(entry TEntry) {
	d.remove(entry)
}

func (d *InstanceDirectory[TEntry]) Ready() error {
	d.mu.Lock()
	if err := d.assertActive(); err != nil {
		d.mu.Unlock()
		return err
	}
	if d.ready {
		d.mu.Unlock()
		return nil
	}
	d.ready = true
	var starts []siPendingStart
	for _, entry := range d.entries.All() {
		starts = append(starts, d.prepareStartAllLocked(entry)...)
	}
	d.mu.Unlock()

	d.runStarts(starts)
	return nil
}

func (d *InstanceDirectory[TEntry]) Reset() {
	d.mu.Lock()
	if d.disposed {
		d.mu.Unlock()
		return
	}
	d.ready = false
	var deactivates []func()
	var cancels []func(any)
	entries := make([]TEntry, 0, d.entries.Len())
	for _, entry := range d.entries.All() {
		entries = append(entries, entry)
	}
	for _, entry := range entries {
		deact, cs := d.removeLocked(entry)
		if deact != nil {
			deactivates = append(deactivates, deact)
		}
		cancels = append(cancels, cs...)
	}
	d.mu.Unlock()

	for _, deact := range deactivates {
		deact()
	}
	for _, cancel := range cancels {
		cancel(nil)
	}
}

func (d *InstanceDirectory[TEntry]) Observe(handler func(service any, context Context) error) (func(), error) {
	d.mu.Lock()
	if err := d.assertActive(); err != nil {
		d.mu.Unlock()
		return nil, err
	}
	observer := &siObserver{
		handler: handler,
		tasks:   omap.NewMap[string, siObserverTask](),
	}
	d.observers.Add(observer)
	var starts []siPendingStart
	if d.ready {
		for _, entry := range d.entries.All() {
			if st, ok := d.prepareStartLocked(observer, entry); ok {
				starts = append(starts, st)
			}
		}
	}
	d.mu.Unlock()

	d.runStarts(starts)

	return func() {
		d.mu.Lock()
		if observer.closed {
			d.mu.Unlock()
			return
		}
		observer.closed = true
		cancels := make([]func(any), 0, observer.tasks.Len())
		for _, task := range observer.tasks.All() {
			if task.cancel != nil {
				cancels = append(cancels, task.cancel)
			}
		}
		observer.tasks.Clear()
		d.observers.Delete(observer)
		d.mu.Unlock()

		for _, cancel := range cancels {
			cancel(nil)
		}
	}, nil
}

func (d *InstanceDirectory[TEntry]) Dispose() error {
	d.mu.Lock()
	if d.disposed {
		d.mu.Unlock()
		return nil
	}
	d.disposed = true
	var cancels []func(any)
	for observer := range d.observers.All() {
		observer.closed = true
		for _, task := range observer.tasks.All() {
			if task.cancel != nil {
				cancels = append(cancels, task.cancel)
			}
		}
		observer.tasks.Clear()
	}
	d.observers.Clear()
	deactivates := make([]func(), 0, d.entries.Len())
	for _, entry := range d.entries.All() {
		deactivates = append(deactivates, entry.Deactivate)
	}
	d.entries.Clear()
	d.mu.Unlock()

	for _, cancel := range cancels {
		cancel(nil)
	}
	for _, deact := range deactivates {
		deact()
	}
	return nil
}

func (d *InstanceDirectory[TEntry]) remove(entry TEntry) {
	d.mu.Lock()
	deactivate, cancels := d.removeLocked(entry)
	d.mu.Unlock()

	if deactivate != nil {
		deactivate()
	}
	for _, cancel := range cancels {
		cancel(nil)
	}
}

func (d *InstanceDirectory[TEntry]) removeLocked(entry TEntry) (func(), []func(any)) {
	current, ok := d.entries.Get(entry.Key())
	if !ok || !siSameEntry(current, entry) {
		return nil, nil
	}
	d.entries.Delete(entry.Key())
	var cancels []func(any)
	for observer := range d.observers.All() {
		if task, hasTask := observer.tasks.Get(entry.Key()); hasTask {
			if task.cancel != nil {
				cancels = append(cancels, task.cancel)
			}
			observer.tasks.Delete(entry.Key())
		}
	}
	return entry.Deactivate, cancels
}

func (d *InstanceDirectory[TEntry]) startAll(entry TEntry) {
	d.mu.Lock()
	starts := d.prepareStartAllLocked(entry)
	d.mu.Unlock()
	d.runStarts(starts)
}

func (d *InstanceDirectory[TEntry]) start(observer *siObserver, entry TEntry) {
	d.mu.Lock()
	st, ok := d.prepareStartLocked(observer, entry)
	d.mu.Unlock()
	if ok {
		d.runStarts([]siPendingStart{st})
	}
}

func (d *InstanceDirectory[TEntry]) prepareStartAllLocked(entry TEntry) []siPendingStart {
	var starts []siPendingStart
	for observer := range d.observers.All() {
		if st, ok := d.prepareStartLocked(observer, entry); ok {
			starts = append(starts, st)
		}
	}
	return starts
}

func (d *InstanceDirectory[TEntry]) prepareStartLocked(observer *siObserver, entry TEntry) (siPendingStart, bool) {
	key := entry.Key()
	if observer.closed || observer.tasks.Has(key) {
		return siPendingStart{}, false
	}
	cc := WithCancel(BackgroundContext)
	observer.tasks.Set(key, siObserverTask{cancel: cc.Cancel})
	return siPendingStart{
		handler: observer.handler,
		service: entry.Service(),
		context: cc.Context,
	}, true
}

func (d *InstanceDirectory[TEntry]) runStarts(starts []siPendingStart) {
	for _, st := range starts {
		if st.handler == nil {
			continue
		}
		err := siInvokeObserver(st.handler, st.service, st.context)
		if err != nil {
			sig := st.context.AbortSignal()
			if sig == nil || sig.Err() == nil {
				if d.reportError != nil {
					d.reportError(err)
				}
			}
		}
	}
}

func (d *InstanceDirectory[TEntry]) assertActive() error {
	if d.disposed {
		return fmt.Errorf("Keyed service directory is disposed")
	}
	return nil
}

func siInvokeObserver(handler func(service any, context Context) error, service any, call Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = siToError(r)
		}
	}()
	return handler(service, call)
}

func siSameEntry[TEntry InstanceDirectoryEntry](left, right TEntry) bool {
	la := any(left)
	ra := any(right)
	if la == nil || ra == nil {
		return la == nil && ra == nil
	}
	lt := reflect.TypeOf(la)
	rt := reflect.TypeOf(ra)
	if lt == rt && lt.Comparable() {
		return la == ra
	}
	return left.Key() == right.Key() && left.Generation() == right.Generation()
}

func siToError(value any) error {
	if value == nil {
		return nil
	}
	if err, ok := value.(error); ok {
		return err
	}
	return fmt.Errorf("%v", value)
}
