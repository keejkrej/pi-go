// Ported from packages/coding-agent/src/modes/json-event.ts (pi v1.5.2).

package modes

import (
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// JsonAgentSessionEvent is the wire event emitted by the JSON and RPC stdout
// protocols: either any non-message_update core.AgentSessionEvent or a
// delta-only JsonMessageUpdateEvent.
type JsonAgentSessionEvent interface {
	AgentSessionEventType() string
}

// JsonAssistantMessageEvent is a streaming assistant message event with
// cumulative `partial` snapshots stripped.
type JsonAssistantMessageEvent interface {
	isJsonAssistantMessageEvent()
}

// JsonMessageUpdateEvent replaces core.AgentSessionEventMessageUpdate on the
// wire, retaining constant-size cumulative usage while dropping full snapshots.
type JsonMessageUpdateEvent struct {
	Type                  string                    `json:"type"`
	Usage                 ai.Usage                  `json:"usage"`
	AssistantMessageEvent JsonAssistantMessageEvent `json:"assistantMessageEvent"`
}

// AgentSessionEventType implements JsonAgentSessionEvent.
func (*JsonMessageUpdateEvent) AgentSessionEventType() string {
	return "message_update"
}

// MarshalJSON encodes the update event with type guaranteed to be "message_update".
func (e JsonMessageUpdateEvent) MarshalJSON() ([]byte, error) {
	type alias JsonMessageUpdateEvent
	if e.Type == "" {
		e.Type = "message_update"
	}
	return jsonx.Marshal(alias(e))
}

// JsonAssistantMessageEventStart opens a stream without carrying a partial snapshot.
type JsonAssistantMessageEventStart struct {
	Type string `json:"type"`
}

func (*JsonAssistantMessageEventStart) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventTextStart opens a text block without carrying a partial snapshot.
type JsonAssistantMessageEventTextStart struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
}

func (*JsonAssistantMessageEventTextStart) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventTextDelta appends text without carrying a partial snapshot.
type JsonAssistantMessageEventTextDelta struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

func (*JsonAssistantMessageEventTextDelta) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventTextEnd closes a text block without carrying a partial snapshot.
type JsonAssistantMessageEventTextEnd struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Content      string `json:"content"`
}

func (*JsonAssistantMessageEventTextEnd) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventThinkingStart opens a thinking block without carrying a partial snapshot.
type JsonAssistantMessageEventThinkingStart struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
}

func (*JsonAssistantMessageEventThinkingStart) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventThinkingDelta appends thinking text without carrying a partial snapshot.
type JsonAssistantMessageEventThinkingDelta struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

func (*JsonAssistantMessageEventThinkingDelta) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventThinkingEnd closes a thinking block without carrying a partial snapshot.
type JsonAssistantMessageEventThinkingEnd struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Content      string `json:"content"`
}

func (*JsonAssistantMessageEventThinkingEnd) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventToolcallStart opens a tool call with its constant id and name.
type JsonAssistantMessageEventToolcallStart struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Id           string `json:"id"`
	ToolName     string `json:"toolName"`
}

func (*JsonAssistantMessageEventToolcallStart) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventToolcallDelta appends tool-call JSON without carrying a partial snapshot.
type JsonAssistantMessageEventToolcallDelta struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

func (*JsonAssistantMessageEventToolcallDelta) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventToolcallEnd closes a tool call without carrying a partial snapshot.
type JsonAssistantMessageEventToolcallEnd struct {
	Type         string       `json:"type"`
	ContentIndex int          `json:"contentIndex"`
	ToolCall     *ai.ToolCall `json:"toolCall"`
}

func (*JsonAssistantMessageEventToolcallEnd) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventDone ends a successful stream.
type JsonAssistantMessageEventDone struct {
	Type    string               `json:"type"`
	Reason  ai.StopReason        `json:"reason"`
	Message *ai.AssistantMessage `json:"message"`
}

func (*JsonAssistantMessageEventDone) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventError ends a stream with an error or abort.
type JsonAssistantMessageEventError struct {
	Type   string               `json:"type"`
	Reason ai.StopReason        `json:"reason"`
	Error  *ai.AssistantMessage `json:"error"`
}

func (*JsonAssistantMessageEventError) isJsonAssistantMessageEvent() {}

// JsonAssistantMessageEventUnknown preserves an unrecognized stream event with any partial field stripped.
type JsonAssistantMessageEventUnknown struct {
	Raw *jsonx.Object
}

func (*JsonAssistantMessageEventUnknown) isJsonAssistantMessageEvent() {}

// MarshalJSON writes the underlying raw object without any cumulative partial field.
func (u *JsonAssistantMessageEventUnknown) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func jeEventType(actual, fallback string) string {
	if actual != "" {
		return actual
	}
	return fallback
}

func jeToJsonAssistantMessageEvent(event ai.AssistantMessageEvent) (JsonAssistantMessageEvent, error) {
	if event == nil {
		return nil, fmt.Errorf("assistantMessageEvent cannot be nil")
	}

	switch ev := event.(type) {
	case *ai.AssistantMessageEventStart:
		return &JsonAssistantMessageEventStart{
			Type: jeEventType(ev.Type, "start"),
		}, nil
	case *ai.AssistantMessageEventTextStart:
		return &JsonAssistantMessageEventTextStart{
			Type:         jeEventType(ev.Type, "text_start"),
			ContentIndex: ev.ContentIndex,
		}, nil
	case *ai.AssistantMessageEventTextDelta:
		return &JsonAssistantMessageEventTextDelta{
			Type:         jeEventType(ev.Type, "text_delta"),
			ContentIndex: ev.ContentIndex,
			Delta:        ev.Delta,
		}, nil
	case *ai.AssistantMessageEventTextEnd:
		return &JsonAssistantMessageEventTextEnd{
			Type:         jeEventType(ev.Type, "text_end"),
			ContentIndex: ev.ContentIndex,
			Content:      ev.Content,
		}, nil
	case *ai.AssistantMessageEventThinkingStart:
		return &JsonAssistantMessageEventThinkingStart{
			Type:         jeEventType(ev.Type, "thinking_start"),
			ContentIndex: ev.ContentIndex,
		}, nil
	case *ai.AssistantMessageEventThinkingDelta:
		return &JsonAssistantMessageEventThinkingDelta{
			Type:         jeEventType(ev.Type, "thinking_delta"),
			ContentIndex: ev.ContentIndex,
			Delta:        ev.Delta,
		}, nil
	case *ai.AssistantMessageEventThinkingEnd:
		return &JsonAssistantMessageEventThinkingEnd{
			Type:         jeEventType(ev.Type, "thinking_end"),
			ContentIndex: ev.ContentIndex,
			Content:      ev.Content,
		}, nil
	case *ai.AssistantMessageEventToolcallStart:
		var tc *ai.ToolCall
		if ev.Partial != nil && ev.ContentIndex >= 0 && ev.ContentIndex < len(ev.Partial.Content) {
			if candidate, ok := ev.Partial.Content[ev.ContentIndex].(*ai.ToolCall); ok {
				tc = candidate
			}
		}
		if tc == nil || tc.Type != "toolCall" {
			return nil, fmt.Errorf("toolcall_start content at index %d is not a tool call", ev.ContentIndex)
		}
		return &JsonAssistantMessageEventToolcallStart{
			Type:         jeEventType(ev.Type, "toolcall_start"),
			ContentIndex: ev.ContentIndex,
			Id:           tc.Id,
			ToolName:     tc.Name,
		}, nil
	case *ai.AssistantMessageEventToolcallDelta:
		return &JsonAssistantMessageEventToolcallDelta{
			Type:         jeEventType(ev.Type, "toolcall_delta"),
			ContentIndex: ev.ContentIndex,
			Delta:        ev.Delta,
		}, nil
	case *ai.AssistantMessageEventToolcallEnd:
		return &JsonAssistantMessageEventToolcallEnd{
			Type:         jeEventType(ev.Type, "toolcall_end"),
			ContentIndex: ev.ContentIndex,
			ToolCall:     ev.ToolCall,
		}, nil
	case *ai.AssistantMessageEventDone:
		return &JsonAssistantMessageEventDone{
			Type:    jeEventType(ev.Type, "done"),
			Reason:  ev.Reason,
			Message: ev.Message,
		}, nil
	case *ai.AssistantMessageEventError:
		return &JsonAssistantMessageEventError{
			Type:   jeEventType(ev.Type, "error"),
			Reason: ev.Reason,
			Error:  ev.Error,
		}, nil
	case *ai.UnknownAssistantMessageEvent:
		if ev.Raw == nil {
			return &JsonAssistantMessageEventUnknown{}, nil
		}
		cloned := ev.Raw.Clone()
		cloned.Delete("partial")
		return &JsonAssistantMessageEventUnknown{Raw: cloned}, nil
	default:
		return nil, fmt.Errorf("unsupported assistant message event type %T", event)
	}
}

// ToJsonEvent removes cumulative assistant snapshots from streaming wire events.
// `message_start` provides the initial message, deltas build it, and
// `message_end` provides the final authoritative message. Cumulative usage,
// tool-call ids, and tool names remain available because their size is constant.
func ToJsonEvent(event core.AgentSessionEvent) (JsonAgentSessionEvent, error) {
	if event == nil {
		return nil, fmt.Errorf("event cannot be nil")
	}
	msgUpdate, ok := event.(*core.AgentSessionEventMessageUpdate)
	if !ok {
		return event, nil
	}
	assistantMsg, ok := msgUpdate.Message.(*ai.AssistantMessage)
	if !ok || assistantMsg == nil || assistantMsg.GetRole() != "assistant" {
		return nil, fmt.Errorf("message_update message is not an assistant message")
	}
	wireEvent, err := jeToJsonAssistantMessageEvent(msgUpdate.AssistantMessageEvent)
	if err != nil {
		return nil, err
	}
	return &JsonMessageUpdateEvent{
		Type:                  "message_update",
		Usage:                 assistantMsg.Usage,
		AssistantMessageEvent: wireEvent,
	}, nil
}
