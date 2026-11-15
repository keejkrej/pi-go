// Ported from packages/agent/src/proxy.ts (pi v1.5.2).

package agent

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
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/sse"
)

// ProxyAssistantMessageEvent is the stripped wire event sent by the pi proxy server
// before client-side reconstruction of partial AssistantMessage state.
type ProxyAssistantMessageEvent interface{ isProxyAssistantMessageEvent() }

// ProxyEventStart marks stream start.
type ProxyEventStart struct {
	Type string `json:"type"`
}

func (ProxyEventStart) isProxyAssistantMessageEvent() {}

// ProxyEventTextStart starts a text content block at ContentIndex.
type ProxyEventTextStart struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
}

func (ProxyEventTextStart) isProxyAssistantMessageEvent() {}

// ProxyEventTextDelta appends Delta to the text block at ContentIndex.
type ProxyEventTextDelta struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

func (ProxyEventTextDelta) isProxyAssistantMessageEvent() {}

// ProxyEventTextEnd finalizes the text block at ContentIndex.
type ProxyEventTextEnd struct {
	Type             string  `json:"type"`
	ContentIndex     int     `json:"contentIndex"`
	ContentSignature *string `json:"contentSignature,omitzero"`
}

func (ProxyEventTextEnd) isProxyAssistantMessageEvent() {}

// ProxyEventThinkingStart starts a thinking content block at ContentIndex.
type ProxyEventThinkingStart struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
}

func (ProxyEventThinkingStart) isProxyAssistantMessageEvent() {}

// ProxyEventThinkingDelta appends Delta to the thinking block at ContentIndex.
type ProxyEventThinkingDelta struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

func (ProxyEventThinkingDelta) isProxyAssistantMessageEvent() {}

// ProxyEventThinkingEnd finalizes the thinking block at ContentIndex.
type ProxyEventThinkingEnd struct {
	Type             string  `json:"type"`
	ContentIndex     int     `json:"contentIndex"`
	ContentSignature *string `json:"contentSignature,omitzero"`
}

func (ProxyEventThinkingEnd) isProxyAssistantMessageEvent() {}

// ProxyEventToolcallStart starts a toolCall block at ContentIndex.
type ProxyEventToolcallStart struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Id           string `json:"id"`
	ToolName     string `json:"toolName"`
}

func (ProxyEventToolcallStart) isProxyAssistantMessageEvent() {}

// ProxyEventToolcallDelta appends partial JSON Delta to the toolCall at ContentIndex.
type ProxyEventToolcallDelta struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

func (ProxyEventToolcallDelta) isProxyAssistantMessageEvent() {}

// ProxyEventToolcallEnd finalizes the toolCall block at ContentIndex.
type ProxyEventToolcallEnd struct {
	Type         string       `json:"type"`
	ContentIndex int          `json:"contentIndex"`
	ToolCall     *ai.ToolCall `json:"toolCall,omitzero"`
}

func (ProxyEventToolcallEnd) isProxyAssistantMessageEvent() {}

// ProxyEventDone delivers terminal stopReason and usage metadata.
type ProxyEventDone struct {
	Type                  string        `json:"type"`
	Reason                ai.StopReason `json:"reason"`
	Usage                 ai.Usage      `json:"usage"`
	ProviderThinkingLevel *string       `json:"providerThinkingLevel,omitzero"`
}

func (ProxyEventDone) isProxyAssistantMessageEvent() {}

// ProxyEventError delivers terminal error metadata and usage.
type ProxyEventError struct {
	Type                  string        `json:"type"`
	Reason                ai.StopReason `json:"reason"`
	ErrorMessage          *string       `json:"errorMessage,omitzero"`
	Usage                 ai.Usage      `json:"usage"`
	ProviderThinkingLevel *string       `json:"providerThinkingLevel,omitzero"`
}

func (ProxyEventError) isProxyAssistantMessageEvent() {}

// UnknownProxyAssistantMessageEvent preserves unrecognized proxy events.
type UnknownProxyAssistantMessageEvent struct {
	Raw *jsonx.Object
}

func (UnknownProxyAssistantMessageEvent) isProxyAssistantMessageEvent() {}

func (e UnknownProxyAssistantMessageEvent) MarshalJSON() ([]byte, error) {
	if e.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(e.Raw)
}

func (e *UnknownProxyAssistantMessageEvent) UnmarshalJSON(data []byte) error {
	obj, err := typesParseObject(data, "UnknownProxyAssistantMessageEvent")
	if err != nil {
		return err
	}
	e.Raw = obj
	return nil
}

// DecodeProxyAssistantMessageEvent decodes a parsed JSON value into a ProxyAssistantMessageEvent.
func DecodeProxyAssistantMessageEvent(v any) (ProxyAssistantMessageEvent, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("expected object for ProxyAssistantMessageEvent")
	}
	t, _ := typesGetString(obj, "type")
	switch t {
	case "start":
		var out ProxyEventStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "text_start":
		var out ProxyEventTextStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "text_delta":
		var out ProxyEventTextDelta
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "text_end":
		var out ProxyEventTextEnd
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "thinking_start":
		var out ProxyEventThinkingStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "thinking_delta":
		var out ProxyEventThinkingDelta
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "thinking_end":
		var out ProxyEventThinkingEnd
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "toolcall_start":
		var out ProxyEventToolcallStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "toolcall_delta":
		var out ProxyEventToolcallDelta
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "toolcall_end":
		var out ProxyEventToolcallEnd
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "done":
		var out ProxyEventDone
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "error":
		var out ProxyEventError
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return &UnknownProxyAssistantMessageEvent{Raw: obj}, nil
	}
}

// UnmarshalProxyAssistantMessageEvent parses SSE JSON bytes into a ProxyAssistantMessageEvent.
func UnmarshalProxyAssistantMessageEvent(data []byte) (ProxyAssistantMessageEvent, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeProxyAssistantMessageEvent(v)
}

// ProxyStreamOptions configures StreamProxy requests through a backend proxy server.
type ProxyStreamOptions struct {
	ai.SimpleStreamOptions
	// AuthToken is sent in the Authorization Bearer header to the proxy server.
	AuthToken string
	// ProxyUrl is the base URL of the proxy server (POST <ProxyUrl>/api/stream).
	ProxyUrl string
}

// BuildProxyRequestOptions builds the JSON request options object forwarded to the proxy server,
// preserving option semantics while excluding client credentials such as AuthToken and ApiKey.
func BuildProxyRequestOptions(options *ProxyStreamOptions) *jsonx.Object {
	out := jsonx.NewObject()
	if options == nil {
		return out
	}
	if options.Temperature != nil {
		out.Set("temperature", *options.Temperature)
	}
	if options.SamplingParams != nil {
		out.Set("samplingParams", options.SamplingParams)
	}
	if options.MaxTokens != nil {
		out.Set("maxTokens", *options.MaxTokens)
	}
	if options.Reasoning != nil {
		out.Set("reasoning", string(*options.Reasoning))
	}
	if options.CacheRetention != nil {
		out.Set("cacheRetention", string(*options.CacheRetention))
	}
	if options.SessionId != nil {
		out.Set("sessionId", *options.SessionId)
	}
	if options.DisableContinuation != nil {
		out.Set("disableContinuation", *options.DisableContinuation)
	}
	if options.Headers != nil {
		out.Set("headers", options.Headers)
	}
	if options.Metadata != nil {
		out.Set("metadata", options.Metadata)
	}
	if options.Transport != nil {
		out.Set("transport", string(*options.Transport))
	}
	if options.ThinkingBudgets != nil {
		out.Set("thinkingBudgets", options.ThinkingBudgets)
	}
	if options.MaxRetryDelayMs != nil {
		out.Set("maxRetryDelayMs", *options.MaxRetryDelayMs)
	}
	return out
}

// StreamProxy streams assistant events from a pi proxy backend at <ProxyUrl>/api/stream.
// This matches TS streamProxy while using context.Context for cancellation and sse.Reader
// for WHATWG-compliant incremental SSE parsing.
func StreamProxy(
	ctx context.Context,
	model *ai.Model,
	transcript *ai.TranscriptContext,
	options *ProxyStreamOptions,
) *ai.AssistantMessageEventStream {
	if ctx == nil {
		ctx = context.Background()
	}
	stream := ai.NewAssistantMessageEventStream()

	go func() {
		partial := proxyInitialAssistantMessage(model)
		argStreams := make(map[int]*ai.ToolCallArgumentsStream)

		emitError := func(err error) {
			msg := err.Error()
			stopReason := ai.StopReasonError
			if ctx.Err() != nil {
				stopReason = ai.StopReasonAborted
			}
			partial.StopReason = stopReason
			partial.ErrorMessage = &msg
			snap := proxyCloneAssistantMessage(partial)
			stream.Push(&ai.AssistantMessageEventError{
				Type:   "error",
				Reason: stopReason,
				Error:  snap,
			})
			stream.End()
		}

		if model == nil {
			emitError(errors.New("model is required for StreamProxy"))
			return
		}
		if options == nil {
			emitError(errors.New("options are required for StreamProxy"))
			return
		}

		payload := jsonx.ObjectOf(
			"model", model,
			"context", transcript,
			"options", BuildProxyRequestOptions(options),
		)
		var wirePayload any = payload
		if options.OnPayload != nil {
			updated, err := options.OnPayload(payload, model)
			if err != nil {
				emitError(err)
				return
			}
			if updated != nil {
				wirePayload = updated
			}
		}
		bodyBytes, err := jsonx.Marshal(wirePayload)
		if err != nil {
			emitError(err)
			return
		}

		url := strings.TrimRight(options.ProxyUrl, "/") + "/api/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			emitError(err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+options.AuthToken)
		req.Header.Set("Content-Type", "application/json")

		client := options.HTTPClient
		if client == nil {
			client = ai.NodeHttpProxyDefaultClient()
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				emitError(errors.New("Request aborted by user"))
				return
			}
			emitError(err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errMsg := fmt.Sprintf("Proxy error: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
			rawBody, readErr := io.ReadAll(resp.Body)
			if readErr == nil && len(rawBody) > 0 {
				if parsed, parseErr := jsonx.Parse(rawBody); parseErr == nil {
					if obj, ok := parsed.(*jsonx.Object); ok {
						if eStr, ok := typesGetString(obj, "error"); ok && eStr != "" {
							errMsg = "Proxy error: " + eStr
						}
					}
				}
			}
			emitError(errors.New(errMsg))
			return
		}

		reader := sse.NewReader(resp.Body)
		terminalPushed := false

		for {
			sseEv, err := reader.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				if ctx.Err() != nil {
					emitError(errors.New("Request aborted by user"))
					return
				}
				emitError(err)
				return
			}
			data := strings.TrimSpace(sseEv.Data)
			if data == "" || data == "[DONE]" {
				continue
			}

			proxyEvent, err := UnmarshalProxyAssistantMessageEvent([]byte(data))
			if err != nil {
				emitError(err)
				return
			}

			assistantEvent, err := proxyProcessEvent(proxyEvent, partial, argStreams)
			if err != nil {
				emitError(err)
				return
			}
			if ctx.Err() != nil {
				emitError(errors.New("Request aborted by user"))
				return
			}
			if assistantEvent != nil {
				switch assistantEvent.(type) {
				case *ai.AssistantMessageEventDone, *ai.AssistantMessageEventError:
					terminalPushed = true
				}
				stream.Push(assistantEvent)
			}
		}

		if !terminalPushed {
			if ctx.Err() != nil {
				emitError(errors.New("Request aborted by user"))
				return
			}
			emitError(errors.New("Connection closed by proxy server before the response completed"))
			return
		}
		stream.End()
	}()

	return stream
}

func proxyInitialAssistantMessage(model *ai.Model) *ai.AssistantMessage {
	var api ai.Api
	var provider ai.ProviderId
	var id string
	if model != nil {
		api = ai.Api(model.Api)
		provider = model.Provider
		id = model.Id
	}
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{},
		Api:        api,
		Provider:   provider,
		Model:      id,
		Usage:      proxyEmptyUsage(),
		StopReason: ai.StopReasonPending,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func proxyEmptyUsage() ai.Usage {
	return ai.Usage{
		Input:       0,
		Output:      0,
		CacheRead:   0,
		CacheWrite:  0,
		TotalTokens: 0,
		Cost:        ai.UsageCost{},
	}
}

func proxyEnsureContentSlot(partial *ai.AssistantMessage, index int) error {
	if index < 0 {
		return fmt.Errorf("invalid proxy event contentIndex: %d", index)
	}
	for len(partial.Content) <= index {
		partial.Content = append(partial.Content, nil)
	}
	return nil
}

func proxyProcessEvent(
	proxyEvent ProxyAssistantMessageEvent,
	partial *ai.AssistantMessage,
	argStreams map[int]*ai.ToolCallArgumentsStream,
) (ai.AssistantMessageEvent, error) {
	switch ev := proxyEvent.(type) {
	case *ProxyEventStart:
		return &ai.AssistantMessageEventStart{
			Type:    "start",
			Partial: proxyCloneAssistantMessage(partial),
		}, nil

	case *ProxyEventTextStart:
		if err := proxyEnsureContentSlot(partial, ev.ContentIndex); err != nil {
			return nil, err
		}
		partial.Content[ev.ContentIndex] = &ai.TextContent{
			Type: "text",
			Text: "",
		}
		return &ai.AssistantMessageEventTextStart{
			Type:         "text_start",
			ContentIndex: ev.ContentIndex,
			Partial:      proxyCloneAssistantMessage(partial),
		}, nil

	case *ProxyEventTextDelta:
		if ev.ContentIndex < 0 || ev.ContentIndex >= len(partial.Content) {
			return nil, fmt.Errorf("received text_delta for missing content block at index %d", ev.ContentIndex)
		}
		tc, ok := partial.Content[ev.ContentIndex].(*ai.TextContent)
		if !ok || tc == nil {
			return nil, fmt.Errorf("received text_delta for non-text content at index %d", ev.ContentIndex)
		}
		tc.Text += ev.Delta
		return &ai.AssistantMessageEventTextDelta{
			Type:         "text_delta",
			ContentIndex: ev.ContentIndex,
			Delta:        ev.Delta,
			Partial:      proxyCloneAssistantMessage(partial),
		}, nil

	case *ProxyEventTextEnd:
		if ev.ContentIndex < 0 || ev.ContentIndex >= len(partial.Content) {
			return nil, fmt.Errorf("received text_end for missing content block at index %d", ev.ContentIndex)
		}
		tc, ok := partial.Content[ev.ContentIndex].(*ai.TextContent)
		if !ok || tc == nil {
			return nil, fmt.Errorf("received text_end for non-text content at index %d", ev.ContentIndex)
		}
		tc.TextSignature = ev.ContentSignature
		return &ai.AssistantMessageEventTextEnd{
			Type:         "text_end",
			ContentIndex: ev.ContentIndex,
			Content:      tc.Text,
			Partial:      proxyCloneAssistantMessage(partial),
		}, nil

	case *ProxyEventThinkingStart:
		if err := proxyEnsureContentSlot(partial, ev.ContentIndex); err != nil {
			return nil, err
		}
		partial.Content[ev.ContentIndex] = &ai.ThinkingContent{
			Type:     "thinking",
			Thinking: "",
		}
		return &ai.AssistantMessageEventThinkingStart{
			Type:         "thinking_start",
			ContentIndex: ev.ContentIndex,
			Partial:      proxyCloneAssistantMessage(partial),
		}, nil

	case *ProxyEventThinkingDelta:
		if ev.ContentIndex < 0 || ev.ContentIndex >= len(partial.Content) {
			return nil, fmt.Errorf("received thinking_delta for missing content block at index %d", ev.ContentIndex)
		}
		tc, ok := partial.Content[ev.ContentIndex].(*ai.ThinkingContent)
		if !ok || tc == nil {
			return nil, fmt.Errorf("received thinking_delta for non-thinking content at index %d", ev.ContentIndex)
		}
		tc.Thinking += ev.Delta
		return &ai.AssistantMessageEventThinkingDelta{
			Type:         "thinking_delta",
			ContentIndex: ev.ContentIndex,
			Delta:        ev.Delta,
			Partial:      proxyCloneAssistantMessage(partial),
		}, nil

	case *ProxyEventThinkingEnd:
		if ev.ContentIndex < 0 || ev.ContentIndex >= len(partial.Content) {
			return nil, fmt.Errorf("received thinking_end for missing content block at index %d", ev.ContentIndex)
		}
		tc, ok := partial.Content[ev.ContentIndex].(*ai.ThinkingContent)
		if !ok || tc == nil {
			return nil, fmt.Errorf("received thinking_end for non-thinking content at index %d", ev.ContentIndex)
		}
		tc.ThinkingSignature = ev.ContentSignature
		return &ai.AssistantMessageEventThinkingEnd{
			Type:         "thinking_end",
			ContentIndex: ev.ContentIndex,
			Content:      tc.Thinking,
			Partial:      proxyCloneAssistantMessage(partial),
		}, nil

	case *ProxyEventToolcallStart:
		if err := proxyEnsureContentSlot(partial, ev.ContentIndex); err != nil {
			return nil, err
		}
		partial.Content[ev.ContentIndex] = &ai.ToolCall{
			Type:      "toolCall",
			Id:        ev.Id,
			Name:      ev.ToolName,
			Arguments: jsonx.NewObject(),
		}
		argStreams[ev.ContentIndex] = ai.NewToolCallArgumentsStream()
		return &ai.AssistantMessageEventToolcallStart{
			Type:         "toolcall_start",
			ContentIndex: ev.ContentIndex,
			Partial:      proxyCloneAssistantMessage(partial),
		}, nil

	case *ProxyEventToolcallDelta:
		if ev.ContentIndex < 0 || ev.ContentIndex >= len(partial.Content) {
			return nil, fmt.Errorf("received toolcall_delta for missing content block at index %d", ev.ContentIndex)
		}
		tc, ok := partial.Content[ev.ContentIndex].(*ai.ToolCall)
		if !ok || tc == nil {
			return nil, fmt.Errorf("received toolcall_delta for non-toolCall content at index %d", ev.ContentIndex)
		}
		argStream := argStreams[ev.ContentIndex]
		if argStream == nil {
			argStream = ai.NewToolCallArgumentsStream()
			argStreams[ev.ContentIndex] = argStream
		}
		tc.Arguments = argStream.Append(ev.Delta)
		return &ai.AssistantMessageEventToolcallDelta{
			Type:         "toolcall_delta",
			ContentIndex: ev.ContentIndex,
			Delta:        ev.Delta,
			Partial:      proxyCloneAssistantMessage(partial),
		}, nil

	case *ProxyEventToolcallEnd:
		if ev.ContentIndex < 0 || ev.ContentIndex >= len(partial.Content) {
			return nil, fmt.Errorf("received toolcall_end for missing content block at index %d", ev.ContentIndex)
		}
		tc, ok := partial.Content[ev.ContentIndex].(*ai.ToolCall)
		if !ok || tc == nil {
			return nil, fmt.Errorf("received toolcall_end for non-toolCall content at index %d", ev.ContentIndex)
		}
		if ev.ToolCall != nil {
			if ev.ToolCall.Type != "" {
				tc.Type = ev.ToolCall.Type
			}
			if ev.ToolCall.Id != "" {
				tc.Id = ev.ToolCall.Id
			}
			if ev.ToolCall.Name != "" {
				tc.Name = ev.ToolCall.Name
			}
			if ev.ToolCall.Arguments != nil {
				tc.Arguments = ev.ToolCall.Arguments
			}
			if ev.ToolCall.ThoughtSignature != nil {
				tc.ThoughtSignature = proxyCloneStringPtr(ev.ToolCall.ThoughtSignature)
			}
			if ev.ToolCall.Namespace != nil {
				tc.Namespace = proxyCloneStringPtr(ev.ToolCall.Namespace)
			}
			if ev.ToolCall.ArgumentsError != nil {
				tc.ArgumentsError = proxyCloneStringPtr(ev.ToolCall.ArgumentsError)
			}
		}
		delete(argStreams, ev.ContentIndex)
		snap := proxyCloneAssistantMessage(partial)
		tcCopy, _ := snap.Content[ev.ContentIndex].(*ai.ToolCall)
		return &ai.AssistantMessageEventToolcallEnd{
			Type:         "toolcall_end",
			ContentIndex: ev.ContentIndex,
			ToolCall:     tcCopy,
			Partial:      snap,
		}, nil

	case *ProxyEventDone:
		partial.StopReason = ev.Reason
		partial.Usage = ev.Usage
		if ev.ProviderThinkingLevel != nil {
			partial.ProviderThinkingLevel = proxyCloneStringPtr(ev.ProviderThinkingLevel)
		}
		return &ai.AssistantMessageEventDone{
			Type:    "done",
			Reason:  ev.Reason,
			Message: proxyCloneAssistantMessage(partial),
		}, nil

	case *ProxyEventError:
		partial.StopReason = ev.Reason
		partial.ErrorMessage = proxyCloneStringPtr(ev.ErrorMessage)
		partial.Usage = ev.Usage
		if ev.ProviderThinkingLevel != nil {
			partial.ProviderThinkingLevel = proxyCloneStringPtr(ev.ProviderThinkingLevel)
		}
		snap := proxyCloneAssistantMessage(partial)
		return &ai.AssistantMessageEventError{
			Type:   "error",
			Reason: ev.Reason,
			Error:  snap,
		}, nil

	case *UnknownProxyAssistantMessageEvent:
		return nil, nil

	default:
		return nil, fmt.Errorf("unhandled proxy event: %T", proxyEvent)
	}
}

// proxyCloneAssistantMessage creates a race-free snapshot of partial without copying mu.
func proxyCloneAssistantMessage(src *ai.AssistantMessage) *ai.AssistantMessage {
	if src == nil {
		return nil
	}
	var content ai.ContentList
	if src.Content != nil {
		content = make(ai.ContentList, len(src.Content))
		for i, c := range src.Content {
			content[i] = proxyCloneContent(c)
		}
	}
	cp := &ai.AssistantMessage{
		Role:                  src.Role,
		Content:               content,
		Api:                   src.Api,
		Provider:              src.Provider,
		Model:                 src.Model,
		ResponseModel:         proxyCloneStringPtr(src.ResponseModel),
		ResponseId:            proxyCloneStringPtr(src.ResponseId),
		ProviderThinkingLevel: proxyCloneStringPtr(src.ProviderThinkingLevel),
		ThinkingLevel:         proxyCloneThinkingLevelPtr(src.ThinkingLevel),
		Usage:                 src.Usage,
		StopReason:            src.StopReason,
		ErrorMessage:          proxyCloneStringPtr(src.ErrorMessage),
		RawStopReason:         proxyCloneStringPtr(src.RawStopReason),
		Timestamp:             src.Timestamp,
		DurationMs:            proxyCloneInt64Ptr(src.DurationMs),
	}
	if src.Diagnostics != nil {
		cp.Diagnostics = append([]ai.AssistantMessageDiagnostic(nil), src.Diagnostics...)
	}
	if src.EndTurn != nil {
		b := *src.EndTurn
		cp.EndTurn = &b
	}
	if src.Deferred != nil {
		d := *src.Deferred
		cp.Deferred = &d
	}
	return cp
}

func proxyCloneContent(c ai.Content) ai.Content {
	if c == nil {
		return nil
	}
	switch v := c.(type) {
	case *ai.TextContent:
		if v == nil {
			return nil
		}
		cp := *v
		cp.TextSignature = proxyCloneStringPtr(v.TextSignature)
		return &cp
	case *ai.ThinkingContent:
		if v == nil {
			return nil
		}
		cp := *v
		cp.ThinkingSignature = proxyCloneStringPtr(v.ThinkingSignature)
		if v.Redacted != nil {
			b := *v.Redacted
			cp.Redacted = &b
		}
		return &cp
	case *ai.ImageContent:
		if v == nil {
			return nil
		}
		cp := *v
		return &cp
	case *ai.ToolCall:
		if v == nil {
			return nil
		}
		cp := *v
		if v.Arguments != nil {
			if cloned, ok := jsonx.CloneValue(v.Arguments).(*jsonx.Object); ok {
				cp.Arguments = cloned
			}
		}
		cp.ThoughtSignature = proxyCloneStringPtr(v.ThoughtSignature)
		cp.Namespace = proxyCloneStringPtr(v.Namespace)
		cp.ArgumentsError = proxyCloneStringPtr(v.ArgumentsError)
		return &cp
	case *ai.UnknownContent:
		if v == nil {
			return nil
		}
		var raw *jsonx.Object
		if v.Raw != nil {
			raw, _ = jsonx.CloneValue(v.Raw).(*jsonx.Object)
		}
		return &ai.UnknownContent{Raw: raw}
	default:
		return c
	}
}

func proxyCloneStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

func proxyCloneInt64Ptr(n *int64) *int64 {
	if n == nil {
		return nil
	}
	cp := *n
	return &cp
}

func proxyCloneThinkingLevelPtr(l *ai.ModelThinkingLevel) *ai.ModelThinkingLevel {
	if l == nil {
		return nil
	}
	cp := *l
	return &cp
}
