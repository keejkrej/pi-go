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
	panic("unported: AltScreenFlashContainer.Flash")
}

// Dispose clears pending flashes and stops their timers.
func (c *AltScreenFlashContainer) Dispose() {
	panic("unported: AltScreenFlashContainer.Dispose")
}

// Invalidate is a no-op. Flashes have no cached render state.
func (c *AltScreenFlashContainer) Invalidate() {}

// Render returns one styled line per visible flash, truncated to width.
func (c *AltScreenFlashContainer) Render(width int) []string {
	panic("unported: AltScreenFlashContainer.Render")
}
