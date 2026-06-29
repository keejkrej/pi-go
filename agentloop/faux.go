package agentloop

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// FauxStreamFn returns a StreamFn that, on each successive call, replays the
// next scripted *AssistantMessage by synthesizing its event sequence:
//
//	StartEvent -> for each content block: (text|thinking)_start/_delta/_end or
//	toolcall_start/_delta(JSON args)/_end -> Done/Error.
//
// If a scripted message has StopReason error/aborted it ends with ErrorEvent;
// otherwise DoneEvent with the message's stop reason (default toolUse when it
// has tool calls, else stop). ctx cancellation ends with an aborted error
// message. If calls exceed the script length, the last scripted message repeats;
// with no script, a simple "stop" message is returned.
func FauxStreamFn(responses ...*AssistantMessage) StreamFn {
	var mu sync.Mutex
	idx := 0

	return func(ctx context.Context, model *Model, c *Context, opts *StreamOptions) *AssistantMessageEventStream {
		mu.Lock()
		var msg *AssistantMessage
		switch {
		case len(responses) == 0:
			msg = FauxText("")
		case idx < len(responses):
			msg = responses[idx]
			idx++
		default:
			msg = responses[len(responses)-1]
		}
		mu.Unlock()

		stream := NewAssistantMessageEventStream()
		go fauxReplay(ctx, stream, model, msg)
		return stream
	}
}

func fauxReplay(ctx context.Context, stream *AssistantMessageEventStream, model *Model, scripted *AssistantMessage) {
	// Build the message that will be carried as Partial/Message throughout.
	out := &AssistantMessage{
		Content:       nil,
		Api:           modelApi(model),
		Provider:      modelProvider(model),
		Model:         modelID(model),
		ResponseModel: scripted.ResponseModel,
		ResponseID:    scripted.ResponseID,
		Usage:         scripted.Usage,
		StopReason:    scripted.StopReason,
		ErrorMessage:  scripted.ErrorMessage,
		Timestamp:     time.Now().UnixMilli(),
	}
	if out.Api == "" {
		out.Api = scripted.Api
	}
	if out.Provider == "" {
		out.Provider = scripted.Provider
	}
	if out.Model == "" {
		out.Model = scripted.Model
	}

	// snapshot returns a deep copy of out so each in-stream event carries a
	// stable Partial. The producer keeps mutating its private accumulator while
	// the consumer reads the snapshot concurrently; this keeps the stream
	// data-race free (the TS version relies on single-threaded microtask
	// ordering, which Go's concurrent producer/consumer does not have).
	snapshot := func() *AssistantMessage {
		cp := *out
		cp.Content = append([]Content(nil), out.Content...)
		return &cp
	}

	stream.Push(&StartEvent{Partial: snapshot()})

	if ctx.Err() != nil {
		out.StopReason = StopReasonAborted
		out.ErrorMessage = "Operation aborted"
		stream.Push(&ErrorEvent{Reason: StopReasonAborted, Error: out})
		stream.End()
		return
	}

	for _, block := range scripted.Content {
		idxBlock := len(out.Content)
		switch b := block.(type) {
		case *TextContent:
			out.Content = append(out.Content, &TextContent{})
			stream.Push(&TextStartEvent{ContentIndex: idxBlock, Partial: snapshot()})
			out.Content[idxBlock] = &TextContent{Text: b.Text, TextSignature: b.TextSignature}
			stream.Push(&TextDeltaEvent{ContentIndex: idxBlock, Delta: b.Text, Partial: snapshot()})
			stream.Push(&TextEndEvent{ContentIndex: idxBlock, Content: b.Text, Partial: snapshot()})
		case *ThinkingContent:
			out.Content = append(out.Content, &ThinkingContent{})
			stream.Push(&ThinkingStartEvent{ContentIndex: idxBlock, Partial: snapshot()})
			out.Content[idxBlock] = &ThinkingContent{
				Thinking:          b.Thinking,
				ThinkingSignature: b.ThinkingSignature,
				Redacted:          b.Redacted,
			}
			stream.Push(&ThinkingDeltaEvent{ContentIndex: idxBlock, Delta: b.Thinking, Partial: snapshot()})
			stream.Push(&ThinkingEndEvent{ContentIndex: idxBlock, Content: b.Thinking, Partial: snapshot()})
		case *ToolCall:
			out.Content = append(out.Content, &ToolCall{ID: b.ID, Name: b.Name, ThoughtSignature: b.ThoughtSignature})
			stream.Push(&ToolCallStartEvent{ContentIndex: idxBlock, Partial: snapshot()})
			argsJSON := "{}"
			if b.Arguments != nil {
				if data, err := json.Marshal(b.Arguments); err == nil {
					argsJSON = string(data)
				}
			}
			tcBlock := &ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Arguments, ThoughtSignature: b.ThoughtSignature}
			out.Content[idxBlock] = tcBlock
			stream.Push(&ToolCallDeltaEvent{ContentIndex: idxBlock, Delta: argsJSON, Partial: snapshot()})
			stream.Push(&ToolCallEndEvent{ContentIndex: idxBlock, ToolCall: tcBlock, Partial: snapshot()})
		case *ImageContent:
			out.Content = append(out.Content, &ImageContent{Data: b.Data, MimeType: b.MimeType})
		}

		if ctx.Err() != nil {
			out.StopReason = StopReasonAborted
			out.ErrorMessage = "Operation aborted"
			stream.Push(&ErrorEvent{Reason: StopReasonAborted, Error: out})
			stream.End()
			return
		}
	}

	reason := scripted.StopReason
	if reason == "" {
		if len(out.ToolCalls()) > 0 {
			reason = StopReasonToolUse
		} else {
			reason = StopReasonStop
		}
		out.StopReason = reason
	}

	if reason == StopReasonError || reason == StopReasonAborted {
		stream.Push(&ErrorEvent{Reason: reason, Error: out})
	} else {
		stream.Push(&DoneEvent{Reason: reason, Message: out})
	}
	stream.End()
}

// FauxText builds an assistant message with a single text block (StopReason stop).
func FauxText(text string) *AssistantMessage {
	return &AssistantMessage{
		Content:    []Content{&TextContent{Text: text}},
		StopReason: StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// FauxToolCall builds an assistant message with a single tool call (StopReason toolUse).
func FauxToolCall(id, name string, args map[string]any) *AssistantMessage {
	return &AssistantMessage{
		Content:    []Content{&ToolCall{ID: id, Name: name, Arguments: args}},
		StopReason: StopReasonToolUse,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// FauxAssistant builds an assistant message with the given stop reason and blocks.
func FauxAssistant(stop StopReason, blocks ...Content) *AssistantMessage {
	return &AssistantMessage{
		Content:    blocks,
		StopReason: stop,
		Timestamp:  time.Now().UnixMilli(),
	}
}
