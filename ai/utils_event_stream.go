// Ported from packages/ai/src/utils/event-stream.ts (pi v1.5.2).

package ai

import (
	"context"
	"iter"
	"math"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type eventStreamItem[T any] struct {
	val T
	ok  bool
}

type eventStreamWaiter[T any] struct {
	ch        chan eventStreamItem[T]
	delivered bool
	canceled  bool
}

// EventStream is an unbounded FIFO stream for consumers with final result resolution.
type EventStream[T, R any] struct {
	mu            sync.Mutex
	queue         []T
	head          int
	waiters       []*eventStreamWaiter[T]
	waitersHead   int
	done          bool
	hasResult     bool
	result        R
	resultCh      chan struct{}
	isComplete    func(T) bool
	extractResult func(T) R
	onPush        func(T) T
	onEnd         func(R) R
}

// NewEventStream constructs an EventStream with completion and result extraction predicates.
func NewEventStream[T, R any](isComplete func(T) bool, extractResult func(T) R) *EventStream[T, R] {
	return &EventStream[T, R]{
		resultCh:      make(chan struct{}),
		isComplete:    isComplete,
		extractResult: extractResult,
	}
}

func (s *EventStream[T, R]) popWaiterLocked() *eventStreamWaiter[T] {
	for s.waitersHead < len(s.waiters) {
		w := s.waiters[s.waitersHead]
		s.waiters[s.waitersHead] = nil
		s.waitersHead++
		if !w.canceled {
			if s.waitersHead == len(s.waiters) {
				s.waiters = s.waiters[:0]
				s.waitersHead = 0
			}
			return w
		}
	}
	s.waiters = s.waiters[:0]
	s.waitersHead = 0
	return nil
}

func (s *EventStream[T, R]) closeWaitersLocked() {
	var zero T
	for {
		w := s.popWaiterLocked()
		if w == nil {
			return
		}
		w.delivered = true
		w.ch <- eventStreamItem[T]{val: zero, ok: false}
	}
}

// Push appends event to the stream without blocking. It is a no-op once the stream is done.
func (s *EventStream[T, R]) Push(event T) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	if s.onPush != nil {
		event = s.onPush(event)
	}
	if s.isComplete != nil && s.isComplete(event) {
		s.done = true
		if s.extractResult != nil && !s.hasResult {
			s.result = s.extractResult(event)
			s.hasResult = true
			close(s.resultCh)
		}
	}
	if w := s.popWaiterLocked(); w != nil {
		w.delivered = true
		w.ch <- eventStreamItem[T]{val: event, ok: true}
	} else {
		s.queue = append(s.queue, event)
	}
	if s.done {
		s.closeWaitersLocked()
	}
	s.mu.Unlock()
}

// End closes the stream, optionally supplying the final result.
func (s *EventStream[T, R]) End(result ...R) {
	s.mu.Lock()
	var hasArg bool
	var r R
	if len(result) > 0 {
		hasArg = true
		r = result[0]
		if !s.done && s.onEnd != nil {
			r = s.onEnd(r)
		}
	}
	s.done = true
	if hasArg && !s.hasResult {
		s.result = r
		s.hasResult = true
		close(s.resultCh)
	}
	s.closeWaitersLocked()
	s.mu.Unlock()
}

// Next returns the next queued event, blocking until one arrives, the stream ends, or ctx cancels.
// Returns (zero, false, nil) once drained and done.
func (s *EventStream[T, R]) Next(ctx context.Context) (T, bool, error) {
	s.mu.Lock()
	if s.head < len(s.queue) {
		ev := s.queue[s.head]
		var zero T
		s.queue[s.head] = zero
		s.head++
		if s.head == len(s.queue) {
			s.queue = s.queue[:0]
			s.head = 0
		}
		s.mu.Unlock()
		return ev, true, nil
	}
	if s.done {
		s.mu.Unlock()
		var zero T
		return zero, false, nil
	}
	if ctx != nil && ctx.Err() != nil {
		s.mu.Unlock()
		var zero T
		return zero, false, ctx.Err()
	}
	w := &eventStreamWaiter[T]{ch: make(chan eventStreamItem[T], 1)}
	s.waiters = append(s.waiters, w)
	s.mu.Unlock()

	if ctx == nil {
		res := <-w.ch
		return res.val, res.ok, nil
	}
	select {
	case res := <-w.ch:
		return res.val, res.ok, nil
	case <-ctx.Done():
		s.mu.Lock()
		if !w.delivered {
			w.canceled = true
			for s.waitersHead < len(s.waiters) && s.waiters[s.waitersHead].canceled {
				s.waiters[s.waitersHead] = nil
				s.waitersHead++
			}
			if s.waitersHead == len(s.waiters) {
				s.waiters = s.waiters[:0]
				s.waitersHead = 0
			}
			s.mu.Unlock()
			var zero T
			return zero, false, ctx.Err()
		}
		s.mu.Unlock()
		res := <-w.ch
		return res.val, res.ok, nil
	}
}

// All yields queued and incoming events until the stream ends or the caller breaks.
func (s *EventStream[T, R]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		for {
			ev, ok, err := s.Next(context.Background())
			if err != nil || !ok {
				return
			}
			if !yield(ev) {
				return
			}
		}
	}
}

// Result blocks until the stream's final result is resolved or ctx is canceled.
func (s *EventStream[T, R]) Result(ctx context.Context) (R, error) {
	s.mu.Lock()
	if s.hasResult {
		r := s.result
		s.mu.Unlock()
		return r, nil
	}
	ch := s.resultCh
	s.mu.Unlock()

	if ctx == nil {
		<-ch
		s.mu.Lock()
		r := s.result
		s.mu.Unlock()
		return r, nil
	}
	select {
	case <-ctx.Done():
		var zero R
		return zero, ctx.Err()
	case <-ch:
		s.mu.Lock()
		r := s.result
		s.mu.Unlock()
		return r, nil
	}
}

// AssistantMessageEventStream is the specialized EventStream for assistant responses.
// When an AssistantMessage finishes normally or with an error via Push or End,
// NewAssistantMessageEventStream stamps DurationMs with a monotonic clock unless already set
// or its timestamp predates the stream.
type AssistantMessageEventStream = EventStream[AssistantMessageEvent, *AssistantMessage]

// NewAssistantMessageEventStream creates a timed AssistantMessageEventStream.
func NewAssistantMessageEventStream() *AssistantMessageEventStream {
	startedAt := js.DateNow()
	startedMono := time.Now()
	stampDuration := func(msg *AssistantMessage) {
		if msg == nil || msg.DurationMs != nil || msg.Timestamp < startedAt {
			return
		}
		ms := int64(math.Round(time.Since(startedMono).Seconds() * 1000))
		if ms < 0 {
			ms = 0
		}
		msg.DurationMs = &ms
	}

	s := NewEventStream(
		func(event AssistantMessageEvent) bool {
			switch event.(type) {
			case *AssistantMessageEventDone, *AssistantMessageEventError:
				return true
			default:
				return false
			}
		},
		func(event AssistantMessageEvent) *AssistantMessage {
			switch ev := event.(type) {
			case *AssistantMessageEventDone:
				return ev.Message
			case *AssistantMessageEventError:
				return ev.Error
			default:
				panic("Unexpected event type for final result")
			}
		},
	)
	s.onPush = func(event AssistantMessageEvent) AssistantMessageEvent {
		return eventStreamPreparePushEvent(event, stampDuration)
	}
	s.onEnd = func(msg *AssistantMessage) *AssistantMessage {
		stampDuration(msg)
		return eventStreamCloneAssistantMessage(msg)
	}
	return s
}

func eventStreamClonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func eventStreamCloneToolCall(tc *ToolCall) *ToolCall {
	if tc == nil {
		return nil
	}
	cp := *tc
	if tc.Arguments != nil {
		cp.Arguments = tc.Arguments.Clone()
	}
	cp.ThoughtSignature = eventStreamClonePtr(tc.ThoughtSignature)
	cp.Namespace = eventStreamClonePtr(tc.Namespace)
	cp.ArgumentsError = eventStreamClonePtr(tc.ArgumentsError)
	return &cp
}

func eventStreamCloneContent(c Content) Content {
	switch v := c.(type) {
	case nil:
		return nil
	case *TextContent:
		if v == nil {
			return nil
		}
		cp := *v
		cp.TextSignature = eventStreamClonePtr(v.TextSignature)
		return &cp
	case *ThinkingContent:
		if v == nil {
			return nil
		}
		cp := *v
		cp.ThinkingSignature = eventStreamClonePtr(v.ThinkingSignature)
		cp.Redacted = eventStreamClonePtr(v.Redacted)
		return &cp
	case *ImageContent:
		if v == nil {
			return nil
		}
		cp := *v
		return &cp
	case *ToolCall:
		if v == nil {
			return nil
		}
		return eventStreamCloneToolCall(v)
	case *UnknownContent:
		if v == nil {
			return nil
		}
		var raw *jsonx.Object
		if v.Raw != nil {
			raw = v.Raw.Clone()
		}
		return &UnknownContent{Raw: raw}
	default:
		return c
	}
}

func eventStreamCloneDiagnostics(src []AssistantMessageDiagnostic) []AssistantMessageDiagnostic {
	if src == nil {
		return nil
	}
	out := make([]AssistantMessageDiagnostic, len(src))
	for i, d := range src {
		diag := d
		if d.Error != nil {
			errCopy := *d.Error
			errCopy.Name = eventStreamClonePtr(d.Error.Name)
			errCopy.Stack = eventStreamClonePtr(d.Error.Stack)
			errCopy.Code = jsonx.CloneValue(d.Error.Code)
			diag.Error = &errCopy
		}
		if d.Details != nil {
			diag.Details = d.Details.Clone()
		}
		out[i] = diag
	}
	return out
}

func eventStreamCloneDeferredHandle(h *DeferredHandle) *DeferredHandle {
	if h == nil {
		return nil
	}
	cp := *h
	cp.ExpiresAt = eventStreamClonePtr(h.ExpiresAt)
	cp.PollAfterMs = eventStreamClonePtr(h.PollAfterMs)
	if val, ok := h.Data.Get(); ok {
		cp.Data = jsonx.Some(jsonx.CloneValue(val))
	}
	return &cp
}

func eventStreamCloneAssistantMessage(src *AssistantMessage) *AssistantMessage {
	if src == nil {
		return nil
	}
	var content ContentList
	if src.Content != nil {
		content = make(ContentList, len(src.Content))
		for i, c := range src.Content {
			content[i] = eventStreamCloneContent(c)
		}
	}
	usage := src.Usage
	usage.CacheWrite1h = eventStreamClonePtr(src.Usage.CacheWrite1h)
	usage.Reasoning = eventStreamClonePtr(src.Usage.Reasoning)
	usage.Cost.TotalFromProvider = eventStreamClonePtr(src.Usage.Cost.TotalFromProvider)
	return &AssistantMessage{
		Role:                  src.Role,
		Content:               content,
		Api:                   src.Api,
		Provider:              src.Provider,
		Model:                 src.Model,
		ResponseModel:         eventStreamClonePtr(src.ResponseModel),
		ResponseId:            eventStreamClonePtr(src.ResponseId),
		ProviderThinkingLevel: eventStreamClonePtr(src.ProviderThinkingLevel),
		ThinkingLevel:         eventStreamClonePtr(src.ThinkingLevel),
		Diagnostics:           eventStreamCloneDiagnostics(src.Diagnostics),
		Usage:                 usage,
		StopReason:            src.StopReason,
		Deferred:              eventStreamCloneDeferredHandle(src.Deferred),
		ErrorMessage:          eventStreamClonePtr(src.ErrorMessage),
		RawStopReason:         eventStreamClonePtr(src.RawStopReason),
		EndTurn:               eventStreamClonePtr(src.EndTurn),
		Timestamp:             src.Timestamp,
		DurationMs:            eventStreamClonePtr(src.DurationMs),
	}
}

func eventStreamPreparePushEvent(event AssistantMessageEvent, stampDuration func(*AssistantMessage)) AssistantMessageEvent {
	switch ev := event.(type) {
	case *AssistantMessageEventStart:
		if ev == nil {
			return nil
		}
		cp := *ev
		cp.Partial = eventStreamCloneAssistantMessage(ev.Partial)
		return &cp
	case *AssistantMessageEventTextStart:
		if ev == nil {
			return nil
		}
		cp := *ev
		cp.Partial = eventStreamCloneAssistantMessage(ev.Partial)
		return &cp
	case *AssistantMessageEventTextDelta:
		if ev == nil {
			return nil
		}
		cp := *ev
		cp.Partial = eventStreamCloneAssistantMessage(ev.Partial)
		return &cp
	case *AssistantMessageEventTextEnd:
		if ev == nil {
			return nil
		}
		cp := *ev
		cp.Partial = eventStreamCloneAssistantMessage(ev.Partial)
		return &cp
	case *AssistantMessageEventThinkingStart:
		if ev == nil {
			return nil
		}
		cp := *ev
		cp.Partial = eventStreamCloneAssistantMessage(ev.Partial)
		return &cp
	case *AssistantMessageEventThinkingDelta:
		if ev == nil {
			return nil
		}
		cp := *ev
		cp.Partial = eventStreamCloneAssistantMessage(ev.Partial)
		return &cp
	case *AssistantMessageEventThinkingEnd:
		if ev == nil {
			return nil
		}
		cp := *ev
		cp.Partial = eventStreamCloneAssistantMessage(ev.Partial)
		return &cp
	case *AssistantMessageEventToolcallStart:
		if ev == nil {
			return nil
		}
		cp := *ev
		cp.Partial = eventStreamCloneAssistantMessage(ev.Partial)
		return &cp
	case *AssistantMessageEventToolcallDelta:
		if ev == nil {
			return nil
		}
		cp := *ev
		cp.Partial = eventStreamCloneAssistantMessage(ev.Partial)
		return &cp
	case *AssistantMessageEventToolcallEnd:
		if ev == nil {
			return nil
		}
		cp := *ev
		cp.ToolCall = eventStreamCloneToolCall(ev.ToolCall)
		cp.Partial = eventStreamCloneAssistantMessage(ev.Partial)
		return &cp
	case *AssistantMessageEventDone:
		if ev == nil {
			return nil
		}
		stampDuration(ev.Message)
		cp := *ev
		cp.Message = eventStreamCloneAssistantMessage(ev.Message)
		return &cp
	case *AssistantMessageEventError:
		if ev == nil {
			return nil
		}
		stampDuration(ev.Error)
		cp := *ev
		cp.Error = eventStreamCloneAssistantMessage(ev.Error)
		return &cp
	case *UnknownAssistantMessageEvent:
		if ev == nil {
			return nil
		}
		var raw *jsonx.Object
		if ev.Raw != nil {
			raw = ev.Raw.Clone()
		}
		return &UnknownAssistantMessageEvent{Raw: raw}
	default:
		return event
	}
}

// CreateAssistantMessageEventStream is the factory alias matching TS createAssistantMessageEventStream.
func CreateAssistantMessageEventStream() *AssistantMessageEventStream {
	return NewAssistantMessageEventStream()
}
