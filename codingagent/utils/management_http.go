// Ported from packages/coding-agent/src/utils/management-http.ts (pi v1.5.2).

package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

// ManagementHttpDefaultRetries is the default number of retries after an initial attempt.
const ManagementHttpDefaultRetries = 2

// FetchWithRetryOptions configures HTTP requests executed by FetchWithRetry.
type FetchWithRetryOptions struct {
	Method           string
	Headers          http.Header
	Body             []byte
	Retries          *int
	BaseDelayMs      int
	TimeoutMs        int
	AttemptTimeoutMs int
	FollowRedirects  *bool
}

var (
	mhClient = ai.NodeHttpProxyDefaultClient
	mhSleep  = Sleep
)

func mhIsRetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout,
		http.StatusTooEarly,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// FetchWithRetry executes an HTTP request using the process proxy-aware HTTP client,
// retrying transient transport errors, per-attempt timeouts, and retryable HTTP status codes
// while respecting shared timeout budgets and caller cancellation.
func FetchWithRetry(ctx context.Context, rawURL string, options *FetchWithRetryOptions) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	retries := ManagementHttpDefaultRetries
	baseDelayMs := 300
	method := http.MethodGet
	var timeoutMs, attemptTimeoutMs int
	followRedirects := true
	var headers http.Header
	var body []byte

	if options != nil {
		if options.Method != "" {
			method = options.Method
		}
		headers = options.Headers
		body = options.Body
		if options.Retries != nil {
			retries = *options.Retries
			if retries < 0 {
				retries = 0
			}
		}
		if options.BaseDelayMs > 0 {
			baseDelayMs = options.BaseDelayMs
		}
		timeoutMs = options.TimeoutMs
		attemptTimeoutMs = options.AttemptTimeoutMs
		if options.FollowRedirects != nil {
			followRedirects = *options.FollowRedirects
		}
	}

	budgetCtx := ctx
	var cancelBudget context.CancelFunc
	if timeoutMs > 0 {
		budgetCtx, cancelBudget = context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancelBudget()
	}

	baseClient := mhClient()
	client := baseClient
	if !followRedirects {
		copied := *baseClient
		copied.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		client = &copied
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if err := budgetCtx.Err(); err != nil {
			return nil, err
		}

		reqCtx := budgetCtx
		var cancelAttempt context.CancelFunc
		if attemptTimeoutMs > 0 {
			reqCtx, cancelAttempt = context.WithTimeout(budgetCtx, time.Duration(attemptTimeoutMs)*time.Millisecond)
		}

		var bodyReader io.Reader
		if len(body) > 0 {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(reqCtx, method, rawURL, bodyReader)
		if err != nil {
			if cancelAttempt != nil {
				cancelAttempt()
			}
			return nil, err
		}
		for k, vals := range headers {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}

		resp, err := client.Do(req)
		attemptTimedOut := cancelAttempt != nil && errors.Is(reqCtx.Err(), context.DeadlineExceeded) && budgetCtx.Err() == nil
		if cancelAttempt != nil {
			cancelAttempt()
		}

		if err == nil {
			if attempt < retries && mhIsRetryableStatus(resp.StatusCode) {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			} else {
				return resp, nil
			}
		} else {
			lastErr = err
			// Caller or overall budget cancellation must not be retried.
			if budgetCtx.Err() != nil {
				return nil, err
			}
			// Do not retry malformed URLs unless a transient transport/timeout error occurred.
			if !attemptTimedOut && attempt == retries {
				return nil, err
			}
		}

		if attempt == retries {
			break
		}

		delay := float64(baseDelayMs * (1 << attempt))
		if err := mhSleep(budgetCtx, delay); err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("request to %s failed after %d attempts", strings.TrimSpace(rawURL), retries+1)
}
