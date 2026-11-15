// Ported from packages/ai/src/utils/sleep.ts (pi v1.5.2).

package ai

import (
	"context"
	"time"
)

// Sleep waits ms milliseconds or until ctx is canceled.
func Sleep(ctx context.Context, ms int64) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return uaAbortReason(ctx)
		}
	}
	d := time.Duration(ms) * time.Millisecond
	if d < 0 {
		d = 0
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	if ctx == nil {
		<-timer.C
		return nil
	}
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return uaAbortReason(ctx)
	}
}
