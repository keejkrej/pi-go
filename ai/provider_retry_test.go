package ai_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

func retryTestProviderErr(status *int, headers map[string]string) error {
	h := make(http.Header)
	for k, v := range headers {
		h.Set(k, v)
	}
	msg := "Provider error: undefined"
	if status != nil {
		msg = fmt.Sprintf("Provider error: %d", *status)
	}
	return &ai.ProviderRetryError{
		Message: msg,
		Status:  status,
		Headers: h,
	}
}

func retryIntPtr(v int) *int       { return &v }
func retryInt64Ptr(v int64) *int64 { return &v }

func TestRetryProviderRequest(t *testing.T) {
	t.Run("retries retryable provider errors", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var calls atomic.Int32
			type result struct {
				val string
				err error
			}
			ch := make(chan result, 1)
			go func() {
				v, err := ai.RetryProviderRequest(t.Context(), func(ctx context.Context) (string, error) {
					if calls.Add(1) == 1 {
						return "", retryTestProviderErr(retryIntPtr(429), map[string]string{"retry-after-ms": "1000"})
					}
					return "ok", nil
				}, &ai.ProviderRetryOptions{MaxRetries: retryIntPtr(1)})
				ch <- result{v, err}
			}()

			synctest.Wait()
			if got := calls.Load(); got != 1 {
				t.Fatalf("calls after initial try = %d, want 1", got)
			}

			time.Sleep(999 * time.Millisecond)
			synctest.Wait()
			if got := calls.Load(); got != 1 {
				t.Fatalf("calls after 999ms = %d, want 1", got)
			}

			time.Sleep(1 * time.Millisecond)
			res := <-ch
			if res.err != nil || res.val != "ok" {
				t.Fatalf("got (%q, %v), want (\"ok\", nil)", res.val, res.err)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("total calls = %d, want 2", got)
			}
		})
	})

	t.Run("does not retry errors the provider marks as non-retryable", func(t *testing.T) {
		wantErr := retryTestProviderErr(retryIntPtr(429), map[string]string{"x-should-retry": "false"})
		var calls int
		_, err := ai.RetryProviderRequest(t.Context(), func(context.Context) (string, error) {
			calls++
			return "", wantErr
		}, &ai.ProviderRetryOptions{MaxRetries: retryIntPtr(2)})
		if !errors.Is(err, wantErr) {
			t.Fatalf("got %v, want %v", err, wantErr)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
	})

	t.Run("does not retry statuses listed in noRetryStatuses", func(t *testing.T) {
		wantErr := retryTestProviderErr(retryIntPtr(504), map[string]string{"retry-after-ms": "0"})
		var calls int
		_, err := ai.RetryProviderRequest(t.Context(), func(context.Context) (string, error) {
			calls++
			return "", wantErr
		}, &ai.ProviderRetryOptions{MaxRetries: retryIntPtr(2), NoRetryStatuses: []int{504}})
		if !errors.Is(err, wantErr) {
			t.Fatalf("got %v, want %v", err, wantErr)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
	})

	t.Run("rejects a provider-requested retry delay above the limit", func(t *testing.T) {
		var calls int
		_, err := ai.RetryProviderRequest(t.Context(), func(context.Context) (string, error) {
			calls++
			return "", retryTestProviderErr(retryIntPtr(429), map[string]string{"retry-after": "277403"})
		}, &ai.ProviderRetryOptions{MaxRetries: retryIntPtr(1), MaxRetryDelayMs: retryInt64Ptr(1000)})
		if err == nil || !strings.Contains(err.Error(), "Server requested 277403s retry delay (max: 1s)") {
			t.Fatalf("got err = %v", err)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
	})

	t.Run("allows disabling the provider-requested retry delay cap", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var calls atomic.Int32
			type result struct {
				val string
				err error
			}
			ch := make(chan result, 1)
			go func() {
				v, err := ai.RetryProviderRequest(t.Context(), func(ctx context.Context) (string, error) {
					if calls.Add(1) == 1 {
						return "", retryTestProviderErr(retryIntPtr(429), map[string]string{"retry-after": "2"})
					}
					return "ok", nil
				}, &ai.ProviderRetryOptions{MaxRetries: retryIntPtr(1), MaxRetryDelayMs: retryInt64Ptr(0)})
				ch <- result{v, err}
			}()

			synctest.Wait()
			time.Sleep(1999 * time.Millisecond)
			synctest.Wait()
			if got := calls.Load(); got != 1 {
				t.Fatalf("calls after 1999ms = %d, want 1", got)
			}

			time.Sleep(1 * time.Millisecond)
			res := <-ch
			if res.err != nil || res.val != "ok" {
				t.Fatalf("got (%q, %v), want (\"ok\", nil)", res.val, res.err)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("total calls = %d, want 2", got)
			}
		})
	})

	t.Run("aborts a provider-requested retry delay", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var calls atomic.Int32
			errCh := make(chan error, 1)
			go func() {
				_, err := ai.RetryProviderRequest(ctx, func(context.Context) (string, error) {
					calls.Add(1)
					return "", retryTestProviderErr(retryIntPtr(429), map[string]string{"retry-after": "277403"})
				}, &ai.ProviderRetryOptions{MaxRetries: retryIntPtr(2), MaxRetryDelayMs: retryInt64Ptr(0)})
				errCh <- err
			}()

			synctest.Wait()
			if got := calls.Load(); got != 1 {
				t.Fatalf("calls = %d, want 1", got)
			}
			cancel()

			err := <-errCh
			if err == nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("expected abort/canceled error, got %v", err)
			}
			var named interface{ Name() string }
			if !errors.As(err, &named) || named.Name() != "AbortError" {
				t.Fatalf("expected AbortError name, got %v", err)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("calls after abort = %d, want 1", got)
			}
		})
	})
}
