// Ported from packages/ai/src/auth/oauth/device-code.ts (pi v1.5.2).

package oauth

import (
	"context"
	"errors"
	"math"
	"time"
)

const (
	dcCancelMessage          = "Login cancelled"
	dcTimeoutMessage         = "Device flow timed out"
	dcSlowDownTimeoutMessage = "Device flow timed out after one or more slow_down responses. This is often caused by clock drift in WSL or VM environments. Please sync or restart the VM clock and try again."
	dcMinimumIntervalMs      = 1000
	// RFC 8628 section 3.2: if the authorization server omits `interval`, the client must use 5 seconds.
	dcDefaultPollIntervalSeconds = 5
	// RFC 8628 section 3.5: `slow_down` means the polling interval must increase by 5 seconds.
	dcSlowDownIntervalIncrementMs = 5000
)

// OAuthDeviceCodePollStatus identifies a poll attempt's outcome.
type OAuthDeviceCodePollStatus string

const (
	OAuthDeviceCodePollStatusPending  OAuthDeviceCodePollStatus = "pending"
	OAuthDeviceCodePollStatusSlowDown OAuthDeviceCodePollStatus = "slow_down"
	OAuthDeviceCodePollStatusFailed   OAuthDeviceCodePollStatus = "failed"
	OAuthDeviceCodePollStatusComplete OAuthDeviceCodePollStatus = "complete"
)

// OAuthDeviceCodePollResult is the result of a single RFC 8628 device code poll call.
type OAuthDeviceCodePollResult[T any] struct {
	Status          OAuthDeviceCodePollStatus
	IntervalSeconds *float64
	Message         string
	Value           T
}

// OAuthDeviceCodePollOptions configures device code polling.
type OAuthDeviceCodePollOptions[T any] struct {
	IntervalSeconds     *float64
	ExpiresInSeconds    *float64
	WaitBeforeFirstPoll bool
	Poll                func(ctx context.Context) (OAuthDeviceCodePollResult[T], error)
}

// AbortableSleep waits for ms milliseconds or returns cancelMessage error if ctx is cancelled.
func AbortableSleep(ctx context.Context, ms int64, cancelMessage string) error {
	return dcSleepDuration(ctx, time.Duration(ms)*time.Millisecond, cancelMessage)
}

func dcSleepDuration(ctx context.Context, d time.Duration, cancelMessage string) error {
	if ctx.Err() != nil {
		return errors.New(cancelMessage)
	}
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errors.New(cancelMessage)
	case <-timer.C:
		return nil
	}
}

func dcToIntervalDuration(seconds float64) time.Duration {
	ms := int64(math.Floor(seconds * 1000))
	if ms < dcMinimumIntervalMs {
		ms = dcMinimumIntervalMs
	}
	return time.Duration(ms) * time.Millisecond
}

// PollOAuthDeviceCodeFlow polls an OAuth device authorization endpoint per RFC 8628 until complete, failed, timed out, or cancelled.
func PollOAuthDeviceCodeFlow[T any](ctx context.Context, options OAuthDeviceCodePollOptions[T]) (T, error) {
	var zero T
	var hasDeadline bool
	var deadline time.Time
	if options.ExpiresInSeconds != nil {
		v := *options.ExpiresInSeconds
		if math.IsNaN(v) || math.IsInf(v, -1) || v <= 0 {
			return zero, errors.New(dcTimeoutMessage)
		}
		if !math.IsInf(v, 1) {
			hasDeadline = true
			deadline = time.Now().Add(time.Duration(v * float64(time.Second)))
		}
	}

	initialIntervalSeconds := float64(dcDefaultPollIntervalSeconds)
	if options.IntervalSeconds != nil {
		if math.IsNaN(*options.IntervalSeconds) {
			initialIntervalSeconds = 0
		} else {
			initialIntervalSeconds = *options.IntervalSeconds
		}
	}
	intervalDur := dcToIntervalDuration(initialIntervalSeconds)

	slowDownResponses := 0
	if options.WaitBeforeFirstPoll {
		if !hasDeadline {
			if err := dcSleepDuration(ctx, intervalDur, dcCancelMessage); err != nil {
				return zero, err
			}
		} else if remaining := deadline.Sub(time.Now()); remaining > 0 {
			sleepDur := min(intervalDur, remaining)
			if err := dcSleepDuration(ctx, sleepDur, dcCancelMessage); err != nil {
				return zero, err
			}
		}
	}

	for !hasDeadline || time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return zero, errors.New(dcCancelMessage)
		}

		result, err := options.Poll(ctx)
		if err != nil {
			return zero, err
		}
		switch result.Status {
		case OAuthDeviceCodePollStatusComplete:
			return result.Value, nil
		case OAuthDeviceCodePollStatusFailed:
			return zero, errors.New(result.Message)
		case OAuthDeviceCodePollStatusSlowDown:
			slowDownResponses++
			// Use the server-provided interval when given (GitHub reports the new required minimum
			// in `interval`); trusting only a client-tracked value risks polling early forever under
			// WSL/VM clock drift. Otherwise apply RFC 8628 section 3.5: increase by 5 seconds.
			if result.IntervalSeconds != nil && !math.IsNaN(*result.IntervalSeconds) && !math.IsInf(*result.IntervalSeconds, 0) && *result.IntervalSeconds > 0 {
				intervalDur = dcToIntervalDuration(*result.IntervalSeconds)
			} else {
				nextMs := max(int64(dcMinimumIntervalMs), intervalDur.Milliseconds()+dcSlowDownIntervalIncrementMs)
				intervalDur = time.Duration(nextMs) * time.Millisecond
			}
		}

		if hasDeadline {
			remaining := deadline.Sub(time.Now())
			if remaining <= 0 {
				break
			}
			if err := dcSleepDuration(ctx, min(intervalDur, remaining), dcCancelMessage); err != nil {
				return zero, err
			}
		} else {
			if err := dcSleepDuration(ctx, intervalDur, dcCancelMessage); err != nil {
				return zero, err
			}
		}
	}

	if slowDownResponses > 0 {
		return zero, errors.New(dcSlowDownTimeoutMessage)
	}
	return zero, errors.New(dcTimeoutMessage)
}
