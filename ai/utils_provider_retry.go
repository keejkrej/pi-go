// Ported from packages/ai/src/utils/provider-retry.ts (pi v1.5.2).

package ai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
)

const uprDefaultMaxRetryDelayMs int64 = 60_000

// ProviderRetryOptions configures retry behavior around provider SDK/HTTP requests.
type ProviderRetryOptions struct {
	MaxRetries      *int
	MaxRetryDelayMs *int64
	// HTTP statuses that fail at once although the default policy would retry them.
	NoRetryStatuses []int
}

// ProviderError is implemented by HTTP errors carrying optional status and response headers.
type ProviderError interface {
	error
	ProviderStatus() *int
	ProviderHeaders() http.Header
}

// ProviderRetryError is a concrete ProviderError value for provider HTTP responses.
type ProviderRetryError struct {
	Message string
	Status  *int
	Headers http.Header
	Cause   error
}

func (e *ProviderRetryError) Error() string                { return e.Message }
func (e *ProviderRetryError) Unwrap() error                { return e.Cause }
func (e *ProviderRetryError) ProviderStatus() *int         { return e.Status }
func (e *ProviderRetryError) ProviderHeaders() http.Header { return e.Headers }

func uprAsProviderError(err error) (ProviderError, bool) {
	var pe ProviderError
	if errors.As(err, &pe) && pe != nil {
		return pe, true
	}
	return nil, false
}

func uprIsRetryableProviderError(err ProviderError) bool {
	headers := err.ProviderHeaders()
	if headers != nil {
		switch headers.Get("x-should-retry") {
		case "true":
			return true
		case "false":
			return false
		}
	}
	status := err.ProviderStatus()
	if status == nil {
		return true
	}
	s := *status
	return s == 408 || s == 409 || s == 429 || s >= 500
}

func uprValidateServerRetryDelayMs(delayMs float64, maxRetryDelayMs *int64, providerErrMsg string) (int64, error) {
	maxDelay := uprDefaultMaxRetryDelayMs
	if maxRetryDelayMs != nil {
		maxDelay = *maxRetryDelayMs
	}
	if maxDelay > 0 && delayMs > float64(maxDelay) {
		return 0, fmt.Errorf(
			"Server requested %ds retry delay (max: %ds). %s",
			int64(math.Ceil(delayMs/1000)),
			int64(math.Ceil(float64(maxDelay)/1000)),
			providerErrMsg,
		)
	}
	if delayMs <= 0 {
		return 0, nil
	}
	return int64(delayMs), nil
}

var (
	uprNowMs = func() int64 { return time.Now().UnixMilli() }
	uprRand  = rand.Float64
)

func uprGetRetryDelayMs(err ProviderError, retryIndex int, maxRetryDelayMs *int64) (int64, error) {
	headers := err.ProviderHeaders()
	if headers != nil {
		if retryAfterMs := headers.Get("retry-after-ms"); retryAfterMs != "" {
			if value, parseErr := strconv.ParseFloat(retryAfterMs, 64); parseErr == nil && !math.IsNaN(value) && !math.IsInf(value, 0) {
				return uprValidateServerRetryDelayMs(value, maxRetryDelayMs, err.Error())
			}
		}
		if retryAfter := headers.Get("retry-after"); retryAfter != "" {
			var delayMs float64
			if seconds, parseErr := strconv.ParseFloat(retryAfter, 64); parseErr == nil && !math.IsNaN(seconds) {
				delayMs = seconds * 1000
			} else if parsedDate := js.DateParse(retryAfter); !math.IsNaN(parsedDate) {
				delayMs = parsedDate - float64(uprNowMs())
			} else {
				delayMs = math.NaN()
			}
			if !math.IsNaN(delayMs) && !math.IsInf(delayMs, 0) {
				return uprValidateServerRetryDelayMs(delayMs, maxRetryDelayMs, err.Error())
			}
		}
	}

	exponentialDelay := math.Min(0.5*math.Pow(2, float64(retryIndex)), 8) * 1000
	return int64(exponentialDelay * (1 - uprRand()*0.25)), nil
}

func uprAbortableSleep(ctx context.Context, ms int64) error {
	if ctx != nil && ctx.Err() != nil {
		return AbortNewError("Request aborted")
	}
	if ms < 0 {
		ms = 0
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	if ctx == nil {
		<-timer.C
		return nil
	}
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return AbortNewError("Request aborted")
	}
}

// RetryProviderRequest reproduces the OpenAI and Anthropic SDK retry policy with abortable sleep.
func RetryProviderRequest[T any](ctx context.Context, request func(ctx context.Context) (T, error), options *ProviderRetryOptions) (T, error) {
	var zero T
	maxRetries := 0
	var maxRetryDelayMs *int64
	var noRetryStatuses []int
	if options != nil {
		if options.MaxRetries != nil {
			maxRetries = *options.MaxRetries
		}
		maxRetryDelayMs = options.MaxRetryDelayMs
		noRetryStatuses = options.NoRetryStatuses
	}
	retriesRemaining := maxRetries

	for {
		res, err := request(ctx)
		if err == nil {
			return res, nil
		}
		if ctx != nil && ctx.Err() != nil {
			return zero, AbortNewError("Request aborted")
		}
		pe, isPE := uprAsProviderError(err)
		if retriesRemaining <= 0 || !isPE || !uprIsRetryableProviderError(pe) {
			return zero, err
		}
		if status := pe.ProviderStatus(); status != nil && slices.Contains(noRetryStatuses, *status) {
			return zero, err
		}

		retryIndex := maxRetries - retriesRemaining
		retriesRemaining--
		delayMs, delayErr := uprGetRetryDelayMs(pe, retryIndex, maxRetryDelayMs)
		if delayErr != nil {
			return zero, delayErr
		}
		if sleepErr := uprAbortableSleep(ctx, delayMs); sleepErr != nil {
			return zero, sleepErr
		}
	}
}
