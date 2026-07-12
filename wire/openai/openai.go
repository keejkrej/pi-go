// Package openai is a clean-room OpenAI Chat Completions streaming adapter that
// satisfies agentloop.StreamFn. It uses only the standard library.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	ag "github.com/keejkrej/pi-go/agentloop"
)

// HTTPClient is the http.Client used for requests. Override it in tests.
var HTTPClient = http.DefaultClient

// StreamSimple is an agentloop.StreamFn. It performs a streaming
// chat-completions request and returns an AssistantMessageEventStream. It never
// returns nil and never panics; all failures are encoded as a StartEvent plus
// ErrorEvent with a final AssistantMessage (StopReason error/aborted).
func StreamSimple(ctx context.Context, model *ag.Model, c *ag.Context, opts *ag.StreamOptions) *ag.AssistantMessageEventStream {
	out := ag.NewAssistantMessageEventStream()

	output := &ag.AssistantMessage{
		Api:        model.Api,
		Provider:   model.Provider,
		Model:      model.ID,
		StopReason: ag.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				finishError(ctx, out, output, fmt.Sprintf("panic: %v", r))
			}
		}()
		run(ctx, model, c, opts, out, output)
	}()

	return out
}

// DefaultMaxRetryDelay caps pre-stream retry backoff and server-requested
// Retry-After waits (matches pi's maxRetryDelayMs default of 60s).
const DefaultMaxRetryDelay = 60 * time.Second

// retryableStatus reports whether an HTTP status is worth a pre-stream retry.
func retryableStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500
}

// retryDelay picks the wait before pre-stream retry attempt (1-based),
// honoring a Retry-After header when present, capped at maxDelay.
func retryDelay(attempt int, retryAfter string, maxDelay time.Duration) time.Duration {
	d := time.Second << (attempt - 1)
	if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 0 {
		d = time.Duration(secs) * time.Second
	}
	if d > maxDelay {
		d = maxDelay
	}
	return d
}

// sleepCtx sleeps for d unless ctx is done first; returns true when the full
// delay elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// run performs the request and parses the response, emitting events.
func run(ctx context.Context, model *ag.Model, c *ag.Context, opts *ag.StreamOptions, out *ag.AssistantMessageEventStream, output *ag.AssistantMessage) {
	body := buildRequestBody(model, c, opts)
	raw, err := json.Marshal(body)
	if err != nil {
		finishError(ctx, out, output, "failed to encode request: "+err.Error())
		return
	}

	url := completionsURL(model.BaseURL)
	maxRetries := 0
	maxDelay := DefaultMaxRetryDelay
	if opts != nil {
		maxRetries = opts.MaxRetries
		if opts.MaxRetryDelay > 0 {
			maxDelay = opts.MaxRetryDelay
		}
	}

	// Pre-stream retries are safe: no event has been emitted before the
	// first 2xx response, so a retried request is invisible to consumers.
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			finishError(ctx, out, output, "failed to build request: "+err.Error())
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if opts != nil && opts.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+opts.APIKey)
		}
		for k, v := range model.Headers {
			req.Header.Set(k, v)
		}
		if opts != nil {
			for k, v := range opts.Headers {
				if v == "" {
					req.Header.Del(k)
					continue
				}
				req.Header.Set(k, v)
			}
		}

		resp, err = HTTPClient.Do(req)
		if err != nil {
			if attempt < maxRetries && ctx.Err() == nil &&
				sleepCtx(ctx, retryDelay(attempt+1, "", maxDelay)) {
				continue
			}
			finishError(ctx, out, output, "request failed: "+err.Error())
			return
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			break
		}

		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		retryAfter := resp.Header.Get("Retry-After")
		resp.Body.Close()
		if attempt < maxRetries && retryableStatus(resp.StatusCode) && ctx.Err() == nil &&
			sleepCtx(ctx, retryDelay(attempt+1, retryAfter, maxDelay)) {
			continue
		}
		finishError(ctx, out, output, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(bodyBytes)))
		return
	}
	defer resp.Body.Close()

	// Success: emit start, then parse the stream.
	out.Push(&ag.StartEvent{Partial: output})

	state := newStreamState(out, output, model)
	state.readStream(ctx, resp.Body)
	state.finishBlocks()

	if ctx.Err() != nil {
		output.StopReason = ag.StopReasonAborted
		output.ErrorMessage = "Request was aborted"
		out.Push(&ag.ErrorEvent{Reason: output.StopReason, Error: output})
		out.End()
		return
	}
	if output.StopReason == ag.StopReasonAborted {
		output.ErrorMessage = "Request was aborted"
		out.Push(&ag.ErrorEvent{Reason: output.StopReason, Error: output})
		out.End()
		return
	}
	if output.StopReason == ag.StopReasonError {
		if output.ErrorMessage == "" {
			output.ErrorMessage = "Provider returned an error stop reason"
		}
		out.Push(&ag.ErrorEvent{Reason: output.StopReason, Error: output})
		out.End()
		return
	}
	if !state.hasFinish {
		output.StopReason = ag.StopReasonError
		output.ErrorMessage = "Stream ended without finish_reason"
		out.Push(&ag.ErrorEvent{Reason: output.StopReason, Error: output})
		out.End()
		return
	}

	out.Push(&ag.DoneEvent{Reason: output.StopReason, Message: output})
	out.End()
}

// finishError pushes a StartEvent (if not already emitted) and an ErrorEvent for
// a pre-stream or fatal failure, then ends the stream.
func finishError(ctx context.Context, out *ag.AssistantMessageEventStream, output *ag.AssistantMessage, msg string) {
	out.Push(&ag.StartEvent{Partial: output})
	if ctx.Err() != nil {
		output.StopReason = ag.StopReasonAborted
		output.ErrorMessage = "Request was aborted"
	} else {
		output.StopReason = ag.StopReasonError
		output.ErrorMessage = msg
	}
	out.Push(&ag.ErrorEvent{Reason: output.StopReason, Error: output})
	out.End()
}
