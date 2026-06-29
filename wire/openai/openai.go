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

// run performs the request and parses the response, emitting events.
func run(ctx context.Context, model *ag.Model, c *ag.Context, opts *ag.StreamOptions, out *ag.AssistantMessageEventStream, output *ag.AssistantMessage) {
	body := buildRequestBody(model, c, opts)
	raw, err := json.Marshal(body)
	if err != nil {
		finishError(ctx, out, output, "failed to encode request: "+err.Error())
		return
	}

	url := completionsURL(model.BaseURL)
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

	resp, err := HTTPClient.Do(req)
	if err != nil {
		finishError(ctx, out, output, "request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		msg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
		finishError(ctx, out, output, msg)
		return
	}

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
