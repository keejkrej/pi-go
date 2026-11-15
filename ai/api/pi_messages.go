// Ported from packages/ai/src/api/pi-messages.ts (pi v1.5.2).

package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/sse"
)

// PiMessagesOptions configures a stream request to a pi-messages backend.
type PiMessagesOptions struct {
	ai.StreamOptions
	Reasoning  *ai.ThinkingLevel `json:"reasoning,omitzero"`
	ToolChoice any               `json:"toolChoice,omitzero"`
	// Debug asks the backend for debug metadata (e.g. routing response headers).
	Debug *bool `json:"debug,omitzero"`
}

// PiMessagesRewriteImpact summarizes a server-side message rewrite (e.g. a gateway policy).
type PiMessagesRewriteImpact struct {
	PolicyId            string  `json:"policyId"`
	PolicyVersion       float64 `json:"policyVersion"`
	Changed             bool    `json:"changed"`
	TokenCountChange    float64 `json:"tokenCountChange"`
	MessageCountChange  float64 `json:"messageCountChange"`
	SystemPromptChanged bool    `json:"systemPromptChanged"`
}

// PiMessagesResponseError is a structured HTTP error from a pi-messages backend.
type PiMessagesResponseError struct {
	Message           string
	Code              *string
	DiagnosticDetails *jsonx.Object
	Status            *int
	Headers           http.Header
	Body              string
}

func (e *PiMessagesResponseError) Error() string { return e.Message }

func (e *PiMessagesResponseError) Name() string { return "PiMessagesResponseError" }

func (e *PiMessagesResponseError) ErrorCode() any {
	if e.Code == nil {
		return nil
	}
	return *e.Code
}

func (e *PiMessagesResponseError) ProviderStatus() *int { return e.Status }

func (e *PiMessagesResponseError) ProviderHeaders() http.Header { return e.Headers }

func (e *PiMessagesResponseError) ErrorBody() string { return e.Body }

func pmParseErrorBody(body string) (*jsonx.Object, bool) {
	parsed, err := jsonx.Parse([]byte(body))
	if err != nil {
		return nil, false
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, false
	}
	rawErr, hasErr := obj.Get("error")
	if !hasErr {
		return nil, false
	}
	errObj, ok := rawErr.(*jsonx.Object)
	if !ok || errObj == nil {
		return nil, false
	}
	return errObj, true
}

func pmTruncateDiagnosticString(value string) string {
	const maxLength = 8192
	if js.Len(value) > maxLength {
		return ai.OwnedSlice(value, 0, maxLength) + "…"
	}
	return value
}

func pmStatusText(resp *http.Response) string {
	prefix := strconv.Itoa(resp.StatusCode) + " "
	return strings.TrimPrefix(resp.Status, prefix)
}

func pmFormatResponseError(resp *http.Response, body string, errObj *jsonx.Object) string {
	var msgPtr *string
	var codePtr *string
	if errObj != nil {
		if m, ok := errObj.GetString("message"); ok {
			msgPtr = &m
		}
		if c, ok := errObj.GetString("code"); ok {
			codePtr = &c
		}
	}
	suffix := body
	if msgPtr != nil {
		suffix = *msgPtr
	}
	codeSuffix := ""
	if codePtr != nil {
		codeSuffix = " (" + *codePtr + ")"
	}
	return fmt.Sprintf("%d %s: %s%s", resp.StatusCode, pmStatusText(resp), suffix, codeSuffix)
}

func pmCreateResponseError(model *ai.Model, reqUrl *url.URL, resp *http.Response, body string) *PiMessagesResponseError {
	errObj, hasErrObj := pmParseErrorBody(body)
	var codePtr *string
	if errObj != nil {
		if c, ok := errObj.GetString("code"); ok {
			codePtr = &c
		}
	}
	details := jsonx.ObjectOf(
		"version", 1,
		"provider", string(model.Provider),
		"model", model.Id,
		"url", reqUrl.String(),
		"status", resp.StatusCode,
		"statusText", pmStatusText(resp),
	)
	if hasErrObj {
		details.Set("error", errObj)
	} else {
		details.Set("body", pmTruncateDiagnosticString(body))
	}
	details.Set("timestampMs", js.DateNow())

	status := resp.StatusCode
	return &PiMessagesResponseError{
		Message:           pmFormatResponseError(resp, body, errObj),
		Code:              codePtr,
		DiagnosticDetails: details,
		Status:            &status,
		Headers:           resp.Header.Clone(),
		Body:              body,
	}
}

func pmCreateEmptyUsage() ai.Usage {
	return ai.Usage{}
}

func pmAppendRewriteDiagnostic(message *ai.AssistantMessage, rewriteRaw *jsonx.Object) {
	if rewriteRaw == nil {
		return
	}
	cloned, _ := jsonx.CloneValue(rewriteRaw).(*jsonx.Object)
	if cloned == nil {
		return
	}
	ai.AppendAssistantMessageDiagnostic(message, &ai.AssistantMessageDiagnostic{
		Type:      "pi_messages_rewrite",
		Timestamp: js.DateNow(),
		Details:   cloned,
	})
}

func pmValidateContentIndex(evObj *jsonx.Object, eventType string, contentLen int) (int, error) {
	rawIndex, hasIndex := evObj.Get("contentIndex")
	if !hasIndex {
		return 0, nil
	}
	limit := contentLen - 1
	if strings.HasSuffix(eventType, "_start") {
		limit = contentLen
	}
	num, ok := rawIndex.(float64)
	if !ok || math.IsNaN(num) || math.IsInf(num, 0) || num != math.Trunc(num) || num < 0 || num > float64(limit) {
		rawText, _ := jsonx.Stringify(rawIndex)
		return 0, fmt.Errorf("Invalid contentIndex %s in pi-messages %s event", rawText, eventType)
	}
	return int(num), nil
}

func pmDecodeUsage(raw any) (ai.Usage, error) {
	var u ai.Usage
	if raw == nil {
		return u, nil
	}
	if err := jsonx.Decode(raw, &u); err != nil {
		return u, err
	}
	return u, nil
}

func pmNewEventConverter(model *ai.Model) func(*jsonx.Object) (ai.AssistantMessageEvent, error) {
	partial := &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{},
		Api:        ai.Api(model.Api),
		Provider:   model.Provider,
		Model:      model.Id,
		Usage:      pmCreateEmptyUsage(),
		StopReason: ai.StopReasonPending,
		Timestamp:  js.DateNow(),
	}
	toolArguments := make(map[int]*ai.ToolCallArgumentsStream)

	return func(evObj *jsonx.Object) (ai.AssistantMessageEvent, error) {
		eventType, _ := evObj.GetString("type")
		idx, err := pmValidateContentIndex(evObj, eventType, len(partial.Content))
		if err != nil {
			return nil, err
		}

		switch eventType {
		case "done":
			reasonStr, _ := evObj.GetString("reason")
			reason := ai.StopReason(reasonStr)
			rawUsage, _ := evObj.Get("usage")
			usage, err := pmDecodeUsage(rawUsage)
			if err != nil {
				return nil, err
			}
			partial.StopReason = reason
			partial.Usage = usage
			if rid, ok := evObj.GetString("responseId"); ok {
				partial.ResponseId = &rid
			} else {
				partial.ResponseId = nil
			}
			if rm, ok := evObj.GetString("responseModel"); ok {
				partial.ResponseModel = &rm
			}
			if ptl, ok := evObj.GetString("providerThinkingLevel"); ok {
				partial.ProviderThinkingLevel = &ptl
			}
			if rawRewrite, ok := evObj.Get("rewrite"); ok {
				if rwObj, ok := rawRewrite.(*jsonx.Object); ok {
					pmAppendRewriteDiagnostic(partial, rwObj)
				}
			}
			return &ai.AssistantMessageEventDone{
				Type:    "done",
				Reason:  reason,
				Message: partial,
			}, nil

		case "error":
			reasonStr, _ := evObj.GetString("reason")
			reason := ai.StopReason(reasonStr)
			rawUsage, _ := evObj.Get("usage")
			usage, err := pmDecodeUsage(rawUsage)
			if err != nil {
				return nil, err
			}
			partial.StopReason = reason
			partial.Usage = usage
			if em, ok := evObj.GetString("errorMessage"); ok {
				partial.ErrorMessage = &em
			} else {
				partial.ErrorMessage = nil
			}
			if rid, ok := evObj.GetString("responseId"); ok {
				partial.ResponseId = &rid
			} else {
				partial.ResponseId = nil
			}
			if rm, ok := evObj.GetString("responseModel"); ok {
				partial.ResponseModel = &rm
			}
			if ptl, ok := evObj.GetString("providerThinkingLevel"); ok {
				partial.ProviderThinkingLevel = &ptl
			}
			if rawRewrite, ok := evObj.Get("rewrite"); ok {
				if rwObj, ok := rawRewrite.(*jsonx.Object); ok {
					pmAppendRewriteDiagnostic(partial, rwObj)
				}
			}
			return &ai.AssistantMessageEventError{
				Type:   "error",
				Reason: reason,
				Error:  partial,
			}, nil

		case "start":
			return &ai.AssistantMessageEventStart{
				Type:    "start",
				Partial: partial,
			}, nil

		case "text_start":
			block := &ai.TextContent{Type: "text", Text: ""}
			if idx == len(partial.Content) {
				partial.Content = append(partial.Content, block)
			} else {
				partial.Content[idx] = block
			}
			return &ai.AssistantMessageEventTextStart{
				Type:         "text_start",
				ContentIndex: idx,
				Partial:      partial,
			}, nil

		case "text_delta":
			delta, _ := evObj.GetString("delta")
			if tc, ok := partial.Content[idx].(*ai.TextContent); ok && tc != nil {
				tc.Text += delta
			}
			return &ai.AssistantMessageEventTextDelta{
				Type:         "text_delta",
				ContentIndex: idx,
				Delta:        delta,
				Partial:      partial,
			}, nil

		case "text_end":
			content, _ := evObj.GetString("content")
			var sigPtr *string
			if sig, ok := evObj.GetString("contentSignature"); ok {
				sigPtr = &sig
			}
			if tc, ok := partial.Content[idx].(*ai.TextContent); ok && tc != nil {
				tc.Text = content
				tc.TextSignature = sigPtr
			}
			return &ai.AssistantMessageEventTextEnd{
				Type:         "text_end",
				ContentIndex: idx,
				Content:      content,
				Partial:      partial,
			}, nil

		case "thinking_start":
			block := &ai.ThinkingContent{Type: "thinking", Thinking: ""}
			if idx == len(partial.Content) {
				partial.Content = append(partial.Content, block)
			} else {
				partial.Content[idx] = block
			}
			return &ai.AssistantMessageEventThinkingStart{
				Type:         "thinking_start",
				ContentIndex: idx,
				Partial:      partial,
			}, nil

		case "thinking_delta":
			delta, _ := evObj.GetString("delta")
			if tc, ok := partial.Content[idx].(*ai.ThinkingContent); ok && tc != nil {
				tc.Thinking += delta
			}
			return &ai.AssistantMessageEventThinkingDelta{
				Type:         "thinking_delta",
				ContentIndex: idx,
				Delta:        delta,
				Partial:      partial,
			}, nil

		case "thinking_end":
			content, _ := evObj.GetString("content")
			var sigPtr *string
			if sig, ok := evObj.GetString("contentSignature"); ok {
				sigPtr = &sig
			}
			var redactedPtr *bool
			if red, ok := evObj.GetBool("redacted"); ok {
				redactedPtr = &red
			}
			if tc, ok := partial.Content[idx].(*ai.ThinkingContent); ok && tc != nil {
				tc.Thinking = content
				tc.ThinkingSignature = sigPtr
				tc.Redacted = redactedPtr
			}
			return &ai.AssistantMessageEventThinkingEnd{
				Type:         "thinking_end",
				ContentIndex: idx,
				Content:      content,
				Partial:      partial,
			}, nil

		case "toolcall_start":
			id, _ := evObj.GetString("id")
			toolName, _ := evObj.GetString("toolName")
			block := &ai.ToolCall{
				Type:      "toolCall",
				Id:        id,
				Name:      toolName,
				Arguments: jsonx.NewObject(),
			}
			if idx == len(partial.Content) {
				partial.Content = append(partial.Content, block)
			} else {
				partial.Content[idx] = block
			}
			toolArguments[idx] = ai.NewToolCallArgumentsStream()
			return &ai.AssistantMessageEventToolcallStart{
				Type:         "toolcall_start",
				ContentIndex: idx,
				Partial:      partial,
			}, nil

		case "toolcall_delta":
			delta, _ := evObj.GetString("delta")
			if args := toolArguments[idx]; args != nil {
				if tc, ok := partial.Content[idx].(*ai.ToolCall); ok && tc != nil {
					tc.Arguments = args.Append(delta)
				}
			}
			return &ai.AssistantMessageEventToolcallDelta{
				Type:         "toolcall_delta",
				ContentIndex: idx,
				Delta:        delta,
				Partial:      partial,
			}, nil

		case "toolcall_end":
			rawToolCall, _ := evObj.Get("toolCall")
			var decoded ai.ToolCall
			if rawToolCall != nil {
				if err := jsonx.Decode(rawToolCall, &decoded); err != nil {
					return nil, err
				}
			}
			if existing, ok := partial.Content[idx].(*ai.ToolCall); ok && existing != nil {
				if decoded.Type != "" {
					existing.Type = decoded.Type
				}
				if decoded.Id != "" {
					existing.Id = decoded.Id
				}
				if decoded.Name != "" {
					existing.Name = decoded.Name
				}
				if decoded.Arguments != nil {
					existing.Arguments = decoded.Arguments
				}
				if decoded.ThoughtSignature != nil {
					existing.ThoughtSignature = decoded.ThoughtSignature
				}
				if decoded.Namespace != nil {
					existing.Namespace = decoded.Namespace
				}
				if decoded.ArgumentsError != nil {
					existing.ArgumentsError = decoded.ArgumentsError
				}
				delete(toolArguments, idx)
				return &ai.AssistantMessageEventToolcallEnd{
					Type:         "toolcall_end",
					ContentIndex: idx,
					ToolCall:     existing,
					Partial:      partial,
				}, nil
			}
			delete(toolArguments, idx)
			return &ai.AssistantMessageEventToolcallEnd{
				Type:         "toolcall_end",
				ContentIndex: idx,
				ToolCall:     &decoded,
				Partial:      partial,
			}, nil

		default:
			rawCopy, _ := jsonx.CloneValue(evObj).(*jsonx.Object)
			return &ai.UnknownAssistantMessageEvent{Raw: rawCopy}, nil
		}
	}
}

func pmCreateErrorEvent(model *ai.Model, err error, aborted bool) ai.AssistantMessageEvent {
	reason := ai.StopReasonError
	if aborted {
		reason = ai.StopReasonAborted
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	assistantMessage := &ai.AssistantMessage{
		Role:         "assistant",
		Content:      ai.ContentList{},
		Api:          ai.Api(model.Api),
		Provider:     model.Provider,
		Model:        model.Id,
		Usage:        pmCreateEmptyUsage(),
		StopReason:   reason,
		ErrorMessage: &errStr,
		Timestamp:    js.DateNow(),
	}
	if !aborted {
		if respErr, ok := err.(*PiMessagesResponseError); ok && respErr != nil {
			ai.AppendAssistantMessageDiagnostic(
				assistantMessage,
				ai.CreateAssistantMessageDiagnostic("pi_messages_response_failure", respErr, respErr.DiagnosticDetails),
			)
		}
	}
	return &ai.AssistantMessageEventError{
		Type:   "error",
		Reason: reason,
		Error:  assistantMessage,
	}
}

func pmResolveCacheRetention(cacheRetention *ai.CacheRetention, env ai.ProviderEnv) *ai.CacheRetention {
	if cacheRetention != nil {
		return cacheRetention
	}
	// Backend defaults apply when unset; only the legacy env opt-in is mapped.
	if ai.GetProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		longVal := ai.CacheRetentionLong
		return &longVal
	}
	return nil
}

func pmUnwrapOptions(options ai.ProviderStreamOptions) *PiMessagesOptions {
	if options == nil {
		return &PiMessagesOptions{}
	}
	if pmOpts, ok := options.(*PiMessagesOptions); ok && pmOpts != nil {
		return pmOpts
	}
	base := options.BaseStreamOptions()
	if base != nil {
		return &PiMessagesOptions{StreamOptions: *base}
	}
	return &PiMessagesOptions{}
}

// StreamPiMessages streams assistant events using pi's native wire protocol over SSE.
func StreamPiMessages(
	ctx context.Context,
	model *ai.Model,
	transcriptCtx *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	eventStream := ai.NewAssistantMessageEventStream()
	convertEvent := pmNewEventConverter(model)
	opts := pmUnwrapOptions(options)

	go func() {
		pushErr := func(err error) {
			aborted := ctx != nil && ctx.Err() != nil
			eventStream.Push(pmCreateErrorEvent(model, err, aborted))
		}

		if opts.ApiKey == nil || *opts.ApiKey == "" {
			pushErr(fmt.Errorf(`No API key provided for provider "%s"`, model.Provider))
			return
		}
		apiKey := *opts.ApiKey

		rawEndpoint := strings.TrimRight(model.BaseUrl, "/") + "/messages"
		parsedUrl, err := url.Parse(rawEndpoint)
		if err != nil {
			pushErr(err)
			return
		}
		if opts.Debug != nil && *opts.Debug {
			q := parsedUrl.Query()
			q.Set("debug", "1")
			parsedUrl.RawQuery = q.Encode()
		}

		wireOpts := jsonx.NewObject()
		if opts.Temperature != nil {
			wireOpts.Set("temperature", *opts.Temperature)
		}
		if opts.MaxTokens != nil {
			wireOpts.Set("maxTokens", *opts.MaxTokens)
		}
		if opts.Reasoning != nil {
			wireOpts.Set("reasoning", string(*opts.Reasoning))
		}
		if cr := pmResolveCacheRetention(opts.CacheRetention, opts.Env); cr != nil {
			wireOpts.Set("cacheRetention", string(*cr))
		}
		if opts.SessionId != nil {
			wireOpts.Set("sessionId", *opts.SessionId)
		}
		if opts.ToolChoice != nil {
			wireOpts.Set("toolChoice", opts.ToolChoice)
		}

		var payload any = jsonx.ObjectOf(
			"model", model.Id,
			"context", transcriptCtx,
			"options", wireOpts,
		)
		if opts.OnPayload != nil {
			nextPayload, err := opts.OnPayload(payload, model)
			if err != nil {
				pushErr(err)
				return
			}
			if nextPayload != nil {
				payload = nextPayload
			}
		}

		bodyBytes, err := jsonx.Marshal(payload)
		if err != nil {
			pushErr(err)
			return
		}

		reqCtx := ctx
		if reqCtx == nil {
			reqCtx = context.Background()
		}
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, parsedUrl.String(), bytes.NewReader(bodyBytes))
		if err != nil {
			pushErr(err)
			return
		}

		idHeaders := ai.GetIdentityHeaders(ai.ClientIdentityTarget{
			Provider: string(model.Provider),
			BaseUrl:  model.BaseUrl,
		}, opts.Identity)
		staticHeaders := omap.NewMap[string, *string]()
		authVal := "Bearer " + apiKey
		acceptVal := "text/event-stream"
		ctVal := "application/json"
		staticHeaders.Set("authorization", &authVal)
		staticHeaders.Set("accept", &acceptVal)
		staticHeaders.Set("content-type", &ctVal)
		clshApplyMergedHeaders(req, clshToProviderHeaders(idHeaders), staticHeaders, opts.Headers)

		httpClient := clshResolveHTTPClient(opts.HTTPClient, opts.Env)
		resp, err := httpClient.Do(req)
		if err != nil {
			pushErr(err)
			return
		}
		defer resp.Body.Close()

		if opts.OnResponse != nil {
			if err := opts.OnResponse(&ai.ProviderResponse{
				Status:  resp.StatusCode,
				Headers: ai.HeadersToRecord(resp.Header),
			}, model); err != nil {
				pushErr(err)
				return
			}
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errBytes, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				pushErr(readErr)
				return
			}
			pushErr(pmCreateResponseError(model, parsedUrl, resp, string(errBytes)))
			return
		}

		sseReader := sse.NewReader(resp.Body)
		for {
			sseEv, err := sseReader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				pushErr(err)
				return
			}
			data := js.Trim(sseEv.Data)
			if data == "" || data == "[DONE]" {
				continue
			}
			parsed, err := jsonx.Parse([]byte(data))
			if err != nil {
				pushErr(err)
				return
			}
			evObj, ok := parsed.(*jsonx.Object)
			if !ok || evObj == nil {
				pushErr(fmt.Errorf("Expected pi-messages event object, got %T", parsed))
				return
			}
			if opts.OnProviderStreamEvent != nil {
				if err := opts.OnProviderStreamEvent(evObj, model); err != nil {
					pushErr(err)
					return
				}
			}
			event, err := convertEvent(evObj)
			if err != nil {
				pushErr(err)
				return
			}
			eventStream.Push(event)
			switch event.(type) {
			case *ai.AssistantMessageEventDone, *ai.AssistantMessageEventError:
				return
			}
		}

		pushErr(fmt.Errorf("%s stream ended without a terminal event", model.Provider))
	}()

	return eventStream
}

// StreamSimplePiMessages adapts SimpleStreamOptions to StreamPiMessages.
func StreamSimplePiMessages(
	ctx context.Context,
	model *ai.Model,
	transcriptCtx *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	pmOpts := &PiMessagesOptions{}
	if options != nil {
		pmOpts.StreamOptions = options.StreamOptions
		pmOpts.Reasoning = options.Reasoning
		if options.ToolChoice != nil {
			pmOpts.ToolChoice = *options.ToolChoice
		}
	}
	return StreamPiMessages(ctx, model, transcriptCtx, pmOpts)
}
