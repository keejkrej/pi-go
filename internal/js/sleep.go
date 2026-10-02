package js

import (
	"context"
	"time"
)

// maxTimeoutMs is the largest setTimeout delay Node honours (2^31 - 1).
const maxTimeoutMs = 2147483647

// Sleep waits ms milliseconds like `await new Promise(r => setTimeout(r, ms))`
// and aborts early when ctx is done. It returns nil after the delay, or
// context.Cause(ctx) (the abort reason) when ctx is already done or ends
// first. Like Node's setTimeout, a delay below 1 or above 2^31-1 ms becomes
// 1 ms. A nil ctx never aborts.
func Sleep(ctx context.Context, ms int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if ms < 1 || ms > maxTimeoutMs {
		ms = 1
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
