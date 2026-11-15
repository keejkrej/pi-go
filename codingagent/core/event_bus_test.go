package core

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestEventBusEmitUnsubscribeAndClear(t *testing.T) {
	bus := CreateEventBus()

	var got []any
	unsub1 := bus.On("alpha", func(data any) {
		got = append(got, "1:", data)
	})
	unsub2 := bus.On("alpha", func(data any) {
		got = append(got, "2:", data)
	})

	bus.Emit("alpha", "hello")
	if len(got) != 4 {
		t.Fatalf("expected 4 elements recorded, got %v", got)
	}

	unsub1()
	unsub1() // idempotent unsubscribe
	got = nil
	bus.Emit("alpha", "second")
	if len(got) != 2 || got[0] != "2:" || got[1] != "second" {
		t.Fatalf("unexpected deliveries after unsub1: %v", got)
	}

	unsub2()
	bus.On("beta", func(data any) {
		got = append(got, data)
	})
	bus.Clear()
	got = nil
	bus.Emit("beta", "ignored")
	if len(got) != 0 {
		t.Fatalf("expected no deliveries after Clear, got %v", got)
	}
}

func TestEventBusRecoversHandlerPanicAndContinues(t *testing.T) {
	var errBuf bytes.Buffer
	bus := &ebController{
		listeners: make(map[string][]ebListener),
		errOut:    &errBuf,
	}

	calledAfterPanic := false
	bus.On("evt", func(data any) {
		panic("boom")
	})
	bus.On("evt", func(data any) {
		calledAfterPanic = true
	})

	bus.Emit("evt", nil)
	if !calledAfterPanic {
		t.Fatal("expected second listener to run after panic in first listener")
	}
	if !strings.Contains(errBuf.String(), "Event handler error (evt): boom") {
		t.Fatalf("unexpected panic error log: %q", errBuf.String())
	}
}

func TestEventBusConcurrentSafe(t *testing.T) {
	bus := CreateEventBus()
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			unsub := bus.On("ch", func(data any) {})
			bus.Emit("ch", id)
			unsub()
		}(i)
	}
	wg.Wait()
}
