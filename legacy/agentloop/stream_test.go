package agentloop

import (
	"sync"
	"testing"
)

func newIntStream() *EventStream[int, int] {
	return NewEventStream[int, int](
		func(ev int) bool { return ev < 0 },
		func(ev int) int { return -ev },
	)
}

func TestEventStreamOrderAndResult(t *testing.T) {
	s := newIntStream()
	go func() {
		s.Push(1)
		s.Push(2)
		s.Push(3)
		s.Push(-7) // terminal: result = 7
		s.End()
	}()

	var got []int
	for ev := range s.Events() {
		got = append(got, ev)
	}
	want := []int{1, 2, 3, -7}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d got %d want %d", i, got[i], want[i])
		}
	}
	if r := s.Result(); r != 7 {
		t.Fatalf("Result() = %d want 7", r)
	}
}

func TestEventStreamResultWithoutConsumingEvents(t *testing.T) {
	s := newIntStream()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Push(10)
		s.Push(-5)
		s.End()
	}()
	// Never consume Events(); Result() must still resolve.
	if r := s.Result(); r != 5 {
		t.Fatalf("Result() = %d want 5", r)
	}
	wg.Wait()
}

func TestEventStreamEndWithoutTerminalResolvesZero(t *testing.T) {
	s := newIntStream()
	go func() {
		s.Push(1)
		s.Push(2)
		s.End() // no terminal event, no explicit result
	}()
	for range s.Events() {
	}
	if r := s.Result(); r != 0 {
		t.Fatalf("Result() = %d want 0 (zero value)", r)
	}
}

func TestEventStreamEndWithExplicitResult(t *testing.T) {
	s := newIntStream()
	go func() {
		s.Push(1)
		s.End(99)
	}()
	for range s.Events() {
	}
	if r := s.Result(); r != 99 {
		t.Fatalf("Result() = %d want 99", r)
	}
}

func TestEventStreamPushAfterEndIgnored(t *testing.T) {
	s := newIntStream()
	s.End(3)
	s.Push(42) // ignored
	count := 0
	for range s.Events() {
		count++
	}
	if count != 0 {
		t.Fatalf("expected no events after End, got %d", count)
	}
	if r := s.Result(); r != 3 {
		t.Fatalf("Result() = %d want 3", r)
	}
}

func TestAssistantMessageEventStream(t *testing.T) {
	s := NewAssistantMessageEventStream()
	final := &AssistantMessage{StopReason: StopReasonStop}
	go func() {
		s.Push(&StartEvent{Partial: &AssistantMessage{}})
		s.Push(&DoneEvent{Reason: StopReasonStop, Message: final})
		s.End()
	}()
	var n int
	for range s.Events() {
		n++
	}
	if n != 2 {
		t.Fatalf("expected 2 events, got %d", n)
	}
	if s.Result() != final {
		t.Fatalf("Result() did not return the final message")
	}
}
