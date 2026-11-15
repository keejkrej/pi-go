// Ported from packages/tui/src/components/alt-screen-flash.ts (pi v1.0.0).

package tui

import (
	"sync"
	"time"
)

const casfDefaultDurationMs int64 = 1000

type casfFlashEntry struct {
	id      int
	message string
	timer   *time.Timer
}

// AltScreenFlashContainer is transient messages composited by the alternate-screen renderer.
// mu guards entries because flash timers fire off the caller goroutine.
type AltScreenFlashContainer struct {
	mu            sync.Mutex
	entries       []casfFlashEntry
	nextId        int
	requestRender func()
}

// NewAltScreenFlashContainer builds an empty flash stack.
// requestRender is called when a flash is added or expires.
func NewAltScreenFlashContainer(requestRender func()) *AltScreenFlashContainer {
	return &AltScreenFlashContainer{requestRender: requestRender}
}

// Flash shows message for durationMs milliseconds.
// Nil durationMs means casfDefaultDurationMs (1000).
func (c *AltScreenFlashContainer) Flash(message string, durationMs *int64) {
	dur := casfDefaultDurationMs
	if durationMs != nil {
		dur = *durationMs
	}
	if dur < 0 {
		dur = 0
	}

	c.mu.Lock()
	id := c.nextId
	c.nextId++
	timer := time.AfterFunc(time.Duration(dur)*time.Millisecond, func() {
		c.mu.Lock()
		idx := -1
		for i, entry := range c.entries {
			if entry.id == id {
				idx = i
				break
			}
		}
		if idx == -1 {
			c.mu.Unlock()
			return
		}
		c.entries = append(c.entries[:idx], c.entries[idx+1:]...)
		c.mu.Unlock()
		if c.requestRender != nil {
			c.requestRender()
		}
	})
	c.entries = append(c.entries, casfFlashEntry{
		id:      id,
		message: message,
		timer:   timer,
	})
	c.mu.Unlock()

	if c.requestRender != nil {
		c.requestRender()
	}
}

// Dispose clears pending flashes and stops their timers.
func (c *AltScreenFlashContainer) Dispose() {
	c.mu.Lock()
	for _, entry := range c.entries {
		if entry.timer != nil {
			entry.timer.Stop()
		}
	}
	c.entries = nil
	c.mu.Unlock()
}

// Invalidate is a no-op. Flashes have no cached render state.
func (c *AltScreenFlashContainer) Invalidate() {}

// Render returns one styled line per visible flash, truncated to width.
func (c *AltScreenFlashContainer) Render(width int) []string {
	c.mu.Lock()
	entries := append([]casfFlashEntry(nil), c.entries...)
	c.mu.Unlock()

	out := make([]string, len(entries))
	for i, entry := range entries {
		msg := TruncateToWidth(" "+entry.message+" ", width, "", false)
		out[i] = "\x1b[7m" + msg + "\x1b[27m"
	}
	return out
}
