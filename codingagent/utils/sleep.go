// Ported from packages/coding-agent/src/utils/sleep.ts (pi v1.5.2).

package utils

import (
	"context"
	"errors"
	"math"
	"time"
)

// Sleep waits for ms milliseconds or returns an "Aborted" error if ctx is cancelled.
func Sleep(ctx context.Context, ms float64) error {
	if ctx != nil && ctx.Err() != nil {
		return errors.New("Aborted")
	}

	d := time.Duration(math.Max(0, ms) * float64(time.Millisecond))
	timer := time.NewTimer(d)
	defer timer.Stop()

	if ctx == nil {
		<-timer.C
		return nil
	}

	select {
	case <-ctx.Done():
		return errors.New("Aborted")
	case <-timer.C:
		return nil
	}
}
