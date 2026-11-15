// Ported from packages/ai/test/oauth-device-code.test.ts (pi v1.5.2).

package oauth

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func todcPtr[T any](v T) *T {
	return &v
}

func TestOAuthDeviceCode_ResolvesImmediatelyOnComplete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		polls := 0
		val, err := PollOAuthDeviceCodeFlow(t.Context(), OAuthDeviceCodePollOptions[string]{
			IntervalSeconds:  todcPtr(5.0),
			ExpiresInSeconds: todcPtr(60.0),
			Poll: func(ctx context.Context) (OAuthDeviceCodePollResult[string], error) {
				polls++
				return OAuthDeviceCodePollResult[string]{
					Status: OAuthDeviceCodePollStatusComplete,
					Value:  "tok-123",
				}, nil
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "tok-123" {
			t.Fatalf("got %q, want tok-123", val)
		}
		if polls != 1 {
			t.Fatalf("got %d polls, want 1", polls)
		}
	})
}

func TestOAuthDeviceCode_WaitsBeforeFirstPollWhenRequested(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var firstPollElapsed time.Duration
		val, err := PollOAuthDeviceCodeFlow(t.Context(), OAuthDeviceCodePollOptions[string]{
			IntervalSeconds:     todcPtr(3.0),
			ExpiresInSeconds:    todcPtr(60.0),
			WaitBeforeFirstPoll: true,
			Poll: func(ctx context.Context) (OAuthDeviceCodePollResult[string], error) {
				firstPollElapsed = time.Since(start)
				return OAuthDeviceCodePollResult[string]{
					Status: OAuthDeviceCodePollStatusComplete,
					Value:  "ok",
				}, nil
			},
		})
		if err != nil || val != "ok" {
			t.Fatalf("got (%q, %v), want (ok, nil)", val, err)
		}
		if firstPollElapsed != 3*time.Second {
			t.Fatalf("first poll elapsed = %v, want 3s", firstPollElapsed)
		}
	})
}

func TestOAuthDeviceCode_PendingRespectsMinimumAndDefaultInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var times []time.Duration
		start := time.Now()
		val, err := PollOAuthDeviceCodeFlow(t.Context(), OAuthDeviceCodePollOptions[int]{
			IntervalSeconds:  todcPtr(0.1), // clamped to 1000ms
			ExpiresInSeconds: todcPtr(10.0),
			Poll: func(ctx context.Context) (OAuthDeviceCodePollResult[int], error) {
				times = append(times, time.Since(start))
				if len(times) < 3 {
					return OAuthDeviceCodePollResult[int]{Status: OAuthDeviceCodePollStatusPending}, nil
				}
				return OAuthDeviceCodePollResult[int]{Status: OAuthDeviceCodePollStatusComplete, Value: 42}, nil
			},
		})
		if err != nil || val != 42 {
			t.Fatalf("got (%d, %v), want (42, nil)", val, err)
		}
		if len(times) != 3 || times[0] != 0 || times[1] != time.Second || times[2] != 2*time.Second {
			t.Fatalf("unexpected poll timestamps: %v", times)
		}
	})
}

func TestOAuthDeviceCode_SlowDownServerIntervalAndRFCFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var times []time.Duration
		start := time.Now()
		// Start with default 5s interval:
		// poll 1 @0s -> slow_down (no interval) => +5s -> 10s
		// poll 2 @10s -> slow_down (server says 12s) => 12s
		// poll 3 @22s -> complete
		val, err := PollOAuthDeviceCodeFlow(t.Context(), OAuthDeviceCodePollOptions[string]{
			ExpiresInSeconds: todcPtr(60.0),
			Poll: func(ctx context.Context) (OAuthDeviceCodePollResult[string], error) {
				times = append(times, time.Since(start))
				switch len(times) {
				case 1:
					return OAuthDeviceCodePollResult[string]{Status: OAuthDeviceCodePollStatusSlowDown}, nil
				case 2:
					return OAuthDeviceCodePollResult[string]{
						Status:          OAuthDeviceCodePollStatusSlowDown,
						IntervalSeconds: todcPtr(12.0),
					}, nil
				default:
					return OAuthDeviceCodePollResult[string]{Status: OAuthDeviceCodePollStatusComplete, Value: "done"}, nil
				}
			},
		})
		if err != nil || val != "done" {
			t.Fatalf("got (%q, %v), want (done, nil)", val, err)
		}
		if len(times) != 3 || times[0] != 0 || times[1] != 10*time.Second || times[2] != 22*time.Second {
			t.Fatalf("unexpected poll timestamps: %v", times)
		}
	})
}

func TestOAuthDeviceCode_FailedResultReturnsMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, err := PollOAuthDeviceCodeFlow(t.Context(), OAuthDeviceCodePollOptions[string]{
			ExpiresInSeconds: todcPtr(30.0),
			Poll: func(ctx context.Context) (OAuthDeviceCodePollResult[string], error) {
				return OAuthDeviceCodePollResult[string]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: "access_denied",
				}, nil
			},
		})
		if err == nil || err.Error() != "access_denied" {
			t.Fatalf("got %v, want access_denied", err)
		}
	})
}

func TestOAuthDeviceCode_TimesOutNormallyAndAfterSlowDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, err := PollOAuthDeviceCodeFlow(t.Context(), OAuthDeviceCodePollOptions[string]{
			IntervalSeconds:  todcPtr(2.0),
			ExpiresInSeconds: todcPtr(3.0),
			Poll: func(ctx context.Context) (OAuthDeviceCodePollResult[string], error) {
				return OAuthDeviceCodePollResult[string]{Status: OAuthDeviceCodePollStatusPending}, nil
			},
		})
		if err == nil || err.Error() != "Device flow timed out" {
			t.Fatalf("got %v, want Device flow timed out", err)
		}
	})

	synctest.Test(t, func(t *testing.T) {
		_, err := PollOAuthDeviceCodeFlow(t.Context(), OAuthDeviceCodePollOptions[string]{
			IntervalSeconds:  todcPtr(2.0),
			ExpiresInSeconds: todcPtr(5.0),
			Poll: func(ctx context.Context) (OAuthDeviceCodePollResult[string], error) {
				return OAuthDeviceCodePollResult[string]{Status: OAuthDeviceCodePollStatusSlowDown}, nil
			},
		})
		if err == nil || !strings.Contains(err.Error(), "clock drift in WSL or VM environments") {
			t.Fatalf("got %v, want slow_down clock drift timeout message", err)
		}
	})
}

func TestOAuthDeviceCode_CancellationAndAbortableSleep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(500 * time.Millisecond)
			cancel()
		}()
		err := AbortableSleep(ctx, 5000, "Custom abort")
		if err == nil || err.Error() != "Custom abort" {
			t.Fatalf("got %v, want Custom abort", err)
		}
	})

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(500 * time.Millisecond)
			cancel()
		}()
		_, err := PollOAuthDeviceCodeFlow(ctx, OAuthDeviceCodePollOptions[string]{
			IntervalSeconds:     todcPtr(5.0),
			ExpiresInSeconds:    todcPtr(30.0),
			WaitBeforeFirstPoll: true,
			Poll: func(ctx context.Context) (OAuthDeviceCodePollResult[string], error) {
				t.Fatal("poll should not run after cancellation during initial wait")
				return OAuthDeviceCodePollResult[string]{}, nil
			},
		})
		if err == nil || err.Error() != "Login cancelled" {
			t.Fatalf("got %v, want Login cancelled", err)
		}
	})
}
