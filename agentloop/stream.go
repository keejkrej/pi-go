package agentloop

import "sync"

// EventStream is an unbounded, single-producer/single-consumer event stream
// with a terminal result. Events are buffered without bound so a slow or absent
// consumer never blocks the producer (matching the TS EventStream queue
// semantics).
type EventStream[T any, R any] struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buffer []T
	done   bool

	isComplete func(T) bool
	extract    func(T) R

	result      R
	resultReady chan struct{}
	resultOnce  sync.Once

	out      chan T
	outStart sync.Once
}

// NewEventStream creates a stream. isComplete detects the terminal event;
// extract pulls the result from it.
func NewEventStream[T any, R any](isComplete func(T) bool, extract func(T) R) *EventStream[T, R] {
	s := &EventStream[T, R]{
		isComplete:  isComplete,
		extract:     extract,
		resultReady: make(chan struct{}),
		out:         make(chan T),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Push appends an event. Events pushed after End or after a terminal event are
// ignored.
func (s *EventStream[T, R]) Push(ev T) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	if s.isComplete != nil && s.isComplete(ev) {
		r := s.extract(ev)
		s.resolve(r)
	}
	s.buffer = append(s.buffer, ev)
	s.cond.Broadcast()
	s.mu.Unlock()
}

// End closes the stream. An optional explicit result resolves Result() if no
// terminal event was seen.
func (s *EventStream[T, R]) End(result ...R) {
	s.mu.Lock()
	if len(result) > 0 {
		s.resolve(result[0])
	} else {
		var zero R
		s.resolve(zero)
	}
	s.done = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

// resolve records the terminal result exactly once. Caller must hold s.mu.
func (s *EventStream[T, R]) resolve(r R) {
	s.resultOnce.Do(func() {
		s.result = r
		close(s.resultReady)
	})
}

// Events returns the same channel each call; it is closed once the buffer is
// drained and the stream is done.
func (s *EventStream[T, R]) Events() <-chan T {
	s.outStart.Do(func() {
		go s.drain()
	})
	return s.out
}

func (s *EventStream[T, R]) drain() {
	for {
		s.mu.Lock()
		for len(s.buffer) == 0 && !s.done {
			s.cond.Wait()
		}
		if len(s.buffer) > 0 {
			ev := s.buffer[0]
			s.buffer = s.buffer[1:]
			s.mu.Unlock()
			s.out <- ev
			continue
		}
		// buffer empty and done
		s.mu.Unlock()
		close(s.out)
		return
	}
}

// Result blocks until the terminal event or End. It returns the zero value if
// the stream ended without a terminal event and no explicit result.
func (s *EventStream[T, R]) Result() R {
	<-s.resultReady
	s.mu.Lock()
	r := s.result
	s.mu.Unlock()
	return r
}

// AssistantMessageEventStream specializes EventStream for StreamFn: the
// terminal event is DoneEvent or ErrorEvent and the result is the contained
// *AssistantMessage.
type AssistantMessageEventStream = EventStream[AssistantMessageEvent, *AssistantMessage]

// NewAssistantMessageEventStream creates an AssistantMessageEventStream.
func NewAssistantMessageEventStream() *AssistantMessageEventStream {
	return NewEventStream[AssistantMessageEvent, *AssistantMessage](
		func(ev AssistantMessageEvent) bool {
			switch ev.(type) {
			case *DoneEvent, *ErrorEvent:
				return true
			default:
				return false
			}
		},
		func(ev AssistantMessageEvent) *AssistantMessage {
			switch e := ev.(type) {
			case *DoneEvent:
				return e.Message
			case *ErrorEvent:
				return e.Error
			default:
				return nil
			}
		},
	)
}
