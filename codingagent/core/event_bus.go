// Ported from packages/coding-agent/src/core/event-bus.ts (pi v1.5.2).

package core

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// EventBus provides pub/sub messaging across extensions and core components.
type EventBus interface {
	Emit(channel string, data any)
	On(channel string, handler func(data any)) func()
}

// EventBusController extends EventBus with Clear for lifecycle cleanup.
type EventBusController interface {
	EventBus
	Clear()
}

type ebListener struct {
	id uint64
	fn func(data any)
}

type ebController struct {
	mu        sync.Mutex
	nextID    uint64
	listeners map[string][]ebListener
	errOut    io.Writer
}

// CreateEventBus creates a thread-safe in-process EventBusController.
func CreateEventBus() EventBusController {
	return &ebController{
		listeners: make(map[string][]ebListener),
		errOut:    os.Stderr,
	}
}

func (b *ebController) Emit(channel string, data any) {
	b.mu.Lock()
	list := append([]ebListener(nil), b.listeners[channel]...)
	errOut := b.errOut
	b.mu.Unlock()

	for _, listener := range list {
		func(fn func(any)) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(errOut, "Event handler error (%s): %v\n", channel, r)
				}
			}()
			fn(data)
		}(listener.fn)
	}
}

func (b *ebController) On(channel string, handler func(data any)) func() {
	if handler == nil {
		return func() {}
	}
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.listeners[channel] = append(b.listeners[channel], ebListener{id: id, fn: handler})
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.listeners[channel]
		for i, entry := range list {
			if entry.id == id {
				b.listeners[channel] = append(list[:i], list[i+1:]...)
				if len(b.listeners[channel]) == 0 {
					delete(b.listeners, channel)
				}
				return
			}
		}
	}
}

func (b *ebController) Clear() {
	b.mu.Lock()
	b.listeners = make(map[string][]ebListener)
	b.mu.Unlock()
}
