// Ported from packages/durable/src/session/observation.ts (pi v1.0.0).

package durable

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const soMaxPendingWatchFrames = 100

// soRetirementOperations is the canonical terminal update for a retired document incarnation.
var soRetirementOperations = []chord.Op{chord.OpReplace{Value: nil}}

func soIsNil[T any](v T) bool {
	if any(v) == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return rv.IsNil()
	}
	return false
}

// soCommittedStateSource is a Session-to-Chord bridge owned one-to-one by one attached state.
type soCommittedStateSource[T any] struct {
	mu          sync.Mutex
	attachments map[*soSourceAttachment[T]]struct{}
	release     func()
	value       T
	cursor      int
	retired     bool
	closed      bool
}

func soNewCommittedStateSource[T any](value T, release func()) *soCommittedStateSource[T] {
	return &soCommittedStateSource[T]{
		attachments: make(map[*soSourceAttachment[T]]struct{}),
		release:     release,
		value:       value,
	}
}

func (s *soCommittedStateSource[T]) Attach() chord.ReplicatedStateSourceAttachment[T] {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		panic("State source is closed")
	}
	snap := chord.ReplicatedStateSourceSnapshot[T]{
		Value:  s.value,
		Cursor: s.cursor,
	}
	var att *soSourceAttachment[T]
	att = soNewSourceAttachment(snap, func() {
		var rel func()
		s.mu.Lock()
		delete(s.attachments, att)
		if len(s.attachments) == 0 && !s.closed {
			s.closed = true
			var zero T
			s.value = zero
			rel = s.release
			s.release = nil
		}
		s.mu.Unlock()
		if rel != nil {
			rel()
		}
	})
	s.attachments[att] = struct{}{}
	s.mu.Unlock()
	return att
}

func (s *soCommittedStateSource[T]) Advance(value T, ops []chord.Op, ctx chord.Context) {
	s.mu.Lock()
	if s.closed || s.retired {
		s.mu.Unlock()
		return
	}
	s.value = value
	s.cursor++
	if soIsNil(value) {
		s.retired = true
	}
	frame := chord.ReplicatedStateSourceFrame[T]{
		Cursor:  s.cursor,
		Value:   value,
		Ops:     ops,
		Context: ctx,
	}
	attachments := make([]*soSourceAttachment[T], 0, len(s.attachments))
	for att := range s.attachments {
		attachments = append(attachments, att)
	}
	s.mu.Unlock()

	for _, att := range attachments {
		att.publish(frame)
	}
}

func (s *soCommittedStateSource[T]) CloseSession() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	var zero T
	s.value = zero
	attachments := make([]*soSourceAttachment[T], 0, len(s.attachments))
	for att := range s.attachments {
		attachments = append(attachments, att)
	}
	s.attachments = make(map[*soSourceAttachment[T]]struct{})
	rel := s.release
	s.release = nil
	s.mu.Unlock()

	for _, att := range attachments {
		att.Dispose()
	}
	if rel != nil {
		rel()
	}
}

type soSourceAttachment[T any] struct {
	mu         sync.Mutex
	snapshot   chord.ReplicatedStateSourceSnapshot[T]
	release    func()
	frames     []chord.ReplicatedStateSourceFrame[T]
	listener   func(frame chord.ReplicatedStateSourceFrame[T])
	activated  bool
	disposed   bool
	delivering bool
}

func soNewSourceAttachment[T any](snap chord.ReplicatedStateSourceSnapshot[T], release func()) *soSourceAttachment[T] {
	return &soSourceAttachment[T]{
		snapshot: snap,
		release:  release,
	}
}

func (a *soSourceAttachment[T]) Snapshot() chord.ReplicatedStateSourceSnapshot[T] {
	return a.snapshot
}

func (a *soSourceAttachment[T]) Activate(listener func(frame chord.ReplicatedStateSourceFrame[T])) {
	a.mu.Lock()
	if a.activated {
		a.mu.Unlock()
		panic("State attachment is already active")
	}
	if a.disposed {
		a.mu.Unlock()
		panic("State attachment is disposed")
	}
	a.activated = true
	a.listener = listener
	shouldDrain := len(a.frames) > 0 && !a.delivering
	a.mu.Unlock()
	if shouldDrain {
		a.drain()
	}
}

func (a *soSourceAttachment[T]) publish(frame chord.ReplicatedStateSourceFrame[T]) {
	a.mu.Lock()
	if a.disposed {
		a.mu.Unlock()
		return
	}
	a.frames = append(a.frames, frame)
	if !a.activated || a.delivering {
		a.mu.Unlock()
		return
	}
	a.delivering = true
	a.mu.Unlock()
	go a.drainLoop()
}

func (a *soSourceAttachment[T]) drain() {
	a.mu.Lock()
	if a.disposed || !a.activated || a.delivering || len(a.frames) == 0 {
		a.mu.Unlock()
		return
	}
	a.delivering = true
	a.mu.Unlock()
	a.drainLoop()
}

func (a *soSourceAttachment[T]) drainLoop() {
	for {
		a.mu.Lock()
		if a.disposed || a.listener == nil || len(a.frames) == 0 {
			a.delivering = false
			a.mu.Unlock()
			return
		}
		frame := a.frames[0]
		a.frames[0] = chord.ReplicatedStateSourceFrame[T]{}
		a.frames = a.frames[1:]
		listener := a.listener
		a.mu.Unlock()

		func() {
			defer func() {
				if r := recover(); r != nil {
					a.Dispose()
				}
			}()
			listener(frame)
		}()
	}
}

func (a *soSourceAttachment[T]) Dispose() {
	a.mu.Lock()
	if a.disposed {
		a.mu.Unlock()
		return
	}
	a.disposed = true
	a.frames = nil
	a.listener = nil
	rel := a.release
	a.release = nil
	a.mu.Unlock()
	if rel != nil {
		rel()
	}
}

type soWatchFrame[T any] struct {
	value T
	ops   []chord.Op
	ctx   chord.Context
}

// soCommittedWatch provides serialized exact-frame observation of one document incarnation or conversation view.
type soCommittedWatch[T any] struct {
	mu            sync.Mutex
	detach        func()
	replace       func() T
	done          chan struct{}
	closeOnce     sync.Once
	pending       []soWatchFrame[T]
	value         T
	listener      func(value T, ops []chord.Op, ctx chord.Context) error
	started       bool
	running       bool
	detached      bool
	retired       bool
	end           *WatchEnd
	stopAbort     func() bool
	abortObserved bool
}

func soNewCommittedWatch[T any](value T, detach func(), replace func() T) *soCommittedWatch[T] {
	return &soCommittedWatch[T]{
		value:   value,
		detach:  detach,
		replace: replace,
		done:    make(chan struct{}),
	}
}

func (w *soCommittedWatch[T]) Value() T {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.value
}

func (w *soCommittedWatch[T]) Done() <-chan struct{} {
	return w.done
}

func (w *soCommittedWatch[T]) Closed() WatchEnd {
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return *w.end
}

func (w *soCommittedWatch[T]) Start(listener func(value T, ops []chord.Op, ctx chord.Context) error) error {
	if listener == nil {
		return fmt.Errorf("Watch listener is required")
	}
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return fmt.Errorf("Watch is already started")
	}
	if w.end != nil {
		w.mu.Unlock()
		return fmt.Errorf("Watch is stopped")
	}
	w.started = true
	w.listener = listener
	shouldDrain := len(w.pending) > 0 && !w.running
	if shouldDrain {
		w.running = true
	}
	w.mu.Unlock()

	if shouldDrain {
		go w.drain()
	}
	return nil
}

func (w *soCommittedWatch[T]) Stop() WatchEnd {
	w.terminate(WatchEnd{Reason: WatchEndStopped})
	return w.Closed()
}

func (w *soCommittedWatch[T]) ObserveCancellation(sig context.Context) error {
	if sig == nil {
		return nil
	}
	w.mu.Lock()
	if w.abortObserved {
		w.mu.Unlock()
		return fmt.Errorf("Watch cancellation is already installed")
	}
	if w.end != nil {
		w.mu.Unlock()
		return nil
	}
	w.abortObserved = true
	stop := context.AfterFunc(sig, func() {
		w.Cancel()
	})
	w.stopAbort = stop
	aborted := sig.Err() != nil
	w.mu.Unlock()

	if aborted {
		w.Cancel()
	}
	return nil
}

func (w *soCommittedWatch[T]) Cancel() {
	w.terminate(WatchEnd{Reason: WatchEndCancelled})
}

func (w *soCommittedWatch[T]) CloseSession() {
	w.terminate(WatchEnd{Reason: WatchEndSessionClosed})
}

func (w *soCommittedWatch[T]) Advance(value T, ops []chord.Op, ctx chord.Context) {
	w.mu.Lock()
	if w.end != nil || w.retired {
		w.mu.Unlock()
		return
	}
	if soIsNil(value) {
		w.retired = true
	}
	if len(w.pending) >= soMaxPendingWatchFrames {
		w.pending = nil
		replacement := value
		if w.replace != nil {
			replacement = w.replace()
		}
		var replaceJson any = replacement
		if soIsNil(replacement) {
			replaceJson = nil
		} else if encoded, err := jsonx.Encode(replacement); err == nil {
			replaceJson = encoded
		}
		w.pending = append(w.pending, soWatchFrame[T]{
			value: replacement,
			ops:   []chord.Op{chord.OpReplace{Value: replaceJson}},
			ctx:   ctx,
		})
	} else {
		w.pending = append(w.pending, soWatchFrame[T]{
			value: value,
			ops:   ops,
			ctx:   ctx,
		})
	}
	shouldDrain := w.started && !w.running && w.end == nil
	if shouldDrain {
		w.running = true
	}
	w.mu.Unlock()

	if shouldDrain {
		go w.drain()
	}
}

func (w *soCommittedWatch[T]) drain() {
	for {
		w.mu.Lock()
		if w.end != nil || !w.started || len(w.pending) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		frame := w.pending[0]
		w.pending[0] = soWatchFrame[T]{}
		w.pending = w.pending[1:]
		w.value = frame.value
		listener := w.listener
		deliveryCtx := chord.WithoutAbortSignal(frame.ctx)
		w.mu.Unlock()

		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = soToError(r)
				}
			}()
			err = listener(frame.value, frame.ops, deliveryCtx)
		}()
		if err != nil {
			w.terminate(WatchEnd{Reason: WatchEndListenerError, Error: err})
			return
		}
		if soIsNil(frame.value) {
			w.terminate(WatchEnd{Reason: WatchEndRetired})
			return
		}
	}
}

func (w *soCommittedWatch[T]) terminate(end WatchEnd) {
	w.mu.Lock()
	if w.end != nil {
		w.mu.Unlock()
		return
	}
	w.end = &end
	w.pending = nil
	var detach func()
	if !w.detached {
		w.detached = true
		detach = w.detach
		w.detach = nil
	}
	stopAbort := w.stopAbort
	w.stopAbort = nil
	w.mu.Unlock()

	if stopAbort != nil {
		stopAbort()
	}
	if detach != nil {
		detach()
	}
	w.closeOnce.Do(func() {
		close(w.done)
	})
}

func soToError(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return errors.New(fmt.Sprint(value))
}
