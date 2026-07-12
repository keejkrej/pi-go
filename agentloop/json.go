package agentloop

import (
	"encoding/json"
	"fmt"
)

// This file is the transcript (de)serialization layer: a JSON codec for the
// sealed AgentMessage/Content unions, wire-compatible with the message shapes
// pi persists in its JSONL session logs (camelCase fields, "type"/"role"
// discriminators). It enables hosts to store and resume transcripts.

type jsonContent struct {
	Type string `json:"type"`
	// text
	Text          string `json:"text,omitempty"`
	TextSignature string `json:"textSignature,omitempty"`
	// thinking
	Thinking          string `json:"thinking,omitempty"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	Redacted          bool   `json:"redacted,omitempty"`
	// image
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	// toolCall
	ID               string         `json:"id,omitempty"`
	Name             string         `json:"name,omitempty"`
	Arguments        map[string]any `json:"arguments,omitempty"`
	ThoughtSignature string         `json:"thoughtSignature,omitempty"`
}

type jsonUsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type jsonUsage struct {
	Input        int           `json:"input"`
	Output       int           `json:"output"`
	CacheRead    int           `json:"cacheRead"`
	CacheWrite   int           `json:"cacheWrite"`
	CacheWrite1h int           `json:"cacheWrite1h,omitempty"`
	Reasoning    int           `json:"reasoning,omitempty"`
	TotalTokens  int           `json:"totalTokens"`
	Cost         jsonUsageCost `json:"cost"`
}

type jsonMessage struct {
	Role string `json:"role"`
	// user + assistant + toolResult
	Content   json.RawMessage `json:"content,omitempty"`
	Timestamp int64           `json:"timestamp,omitempty"`
	// assistant
	Api           string     `json:"api,omitempty"`
	Provider      string     `json:"provider,omitempty"`
	Model         string     `json:"model,omitempty"`
	ResponseModel string     `json:"responseModel,omitempty"`
	ResponseID    string     `json:"responseId,omitempty"`
	Usage         *jsonUsage `json:"usage,omitempty"`
	StopReason    StopReason `json:"stopReason,omitempty"`
	ErrorMessage  string     `json:"errorMessage,omitempty"`
	// toolResult
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
}

func contentToJSON(blocks []Content) []jsonContent {
	out := make([]jsonContent, 0, len(blocks))
	for _, b := range blocks {
		switch c := b.(type) {
		case *TextContent:
			out = append(out, jsonContent{Type: "text", Text: c.Text, TextSignature: c.TextSignature})
		case *ThinkingContent:
			out = append(out, jsonContent{Type: "thinking", Thinking: c.Thinking, ThinkingSignature: c.ThinkingSignature, Redacted: c.Redacted})
		case *ImageContent:
			out = append(out, jsonContent{Type: "image", Data: c.Data, MimeType: c.MimeType})
		case *ToolCall:
			out = append(out, jsonContent{Type: "toolCall", ID: c.ID, Name: c.Name, Arguments: c.Arguments, ThoughtSignature: c.ThoughtSignature})
		}
	}
	return out
}

func contentFromJSON(raw json.RawMessage) ([]Content, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// pi serializes single-text user content as a plain string in some paths;
	// accept both forms.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []Content{&TextContent{Text: s}}, nil
	}
	var blocks []jsonContent
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("invalid content: %w", err)
	}
	var out []Content // nil for empty content, matching in-memory messages
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, &TextContent{Text: b.Text, TextSignature: b.TextSignature})
		case "thinking":
			out = append(out, &ThinkingContent{Thinking: b.Thinking, ThinkingSignature: b.ThinkingSignature, Redacted: b.Redacted})
		case "image":
			out = append(out, &ImageContent{Data: b.Data, MimeType: b.MimeType})
		case "toolCall":
			out = append(out, &ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Arguments, ThoughtSignature: b.ThoughtSignature})
		default:
			return nil, fmt.Errorf("unknown content type %q", b.Type)
		}
	}
	return out, nil
}

func usageToJSON(u Usage) *jsonUsage {
	return &jsonUsage{
		Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
		CacheWrite1h: u.CacheWrite1h, Reasoning: u.Reasoning, TotalTokens: u.TotalTokens,
		Cost: jsonUsageCost{
			Input: u.Cost.Input, Output: u.Cost.Output, CacheRead: u.Cost.CacheRead,
			CacheWrite: u.Cost.CacheWrite, Total: u.Cost.Total,
		},
	}
}

func usageFromJSON(u *jsonUsage) Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{
		Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
		CacheWrite1h: u.CacheWrite1h, Reasoning: u.Reasoning, TotalTokens: u.TotalTokens,
		Cost: UsageCost{
			Input: u.Cost.Input, Output: u.Cost.Output, CacheRead: u.Cost.CacheRead,
			CacheWrite: u.Cost.CacheWrite, Total: u.Cost.Total,
		},
	}
}

// MarshalMessage encodes one message with a "role" discriminator.
func MarshalMessage(m AgentMessage) ([]byte, error) {
	jm, err := messageToJSON(m)
	if err != nil {
		return nil, err
	}
	return json.Marshal(jm)
}

func messageToJSON(m AgentMessage) (*jsonMessage, error) {
	marshalContent := func(blocks []Content) (json.RawMessage, error) {
		raw, err := json.Marshal(contentToJSON(blocks))
		if err != nil {
			return nil, err
		}
		return raw, nil
	}

	switch msg := m.(type) {
	case *UserMessage:
		content, err := marshalContent(msg.Content)
		if err != nil {
			return nil, err
		}
		return &jsonMessage{Role: "user", Content: content, Timestamp: msg.Timestamp}, nil
	case *AssistantMessage:
		content, err := marshalContent(msg.Content)
		if err != nil {
			return nil, err
		}
		return &jsonMessage{
			Role: "assistant", Content: content, Timestamp: msg.Timestamp,
			Api: msg.Api, Provider: msg.Provider, Model: msg.Model,
			ResponseModel: msg.ResponseModel, ResponseID: msg.ResponseID,
			Usage: usageToJSON(msg.Usage), StopReason: msg.StopReason,
			ErrorMessage: msg.ErrorMessage,
		}, nil
	case *ToolResultMessage:
		content, err := marshalContent(msg.Content)
		if err != nil {
			return nil, err
		}
		var details json.RawMessage
		if msg.Details != nil {
			raw, err := json.Marshal(msg.Details)
			if err != nil {
				return nil, fmt.Errorf("tool result details: %w", err)
			}
			details = raw
		}
		return &jsonMessage{
			Role: "toolResult", Content: content, Timestamp: msg.Timestamp,
			ToolCallID: msg.ToolCallID, ToolName: msg.ToolName,
			Details: details, IsError: msg.IsError,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported message type %T", m)
	}
}

// UnmarshalMessage decodes one message by its "role" discriminator. Details on
// tool results decode to generic JSON values (map/slice/string/float64).
func UnmarshalMessage(data []byte) (AgentMessage, error) {
	var jm jsonMessage
	if err := json.Unmarshal(data, &jm); err != nil {
		return nil, err
	}
	content, err := contentFromJSON(jm.Content)
	if err != nil {
		return nil, err
	}
	switch jm.Role {
	case "user":
		return &UserMessage{Content: content, Timestamp: jm.Timestamp}, nil
	case "assistant":
		return &AssistantMessage{
			Content: content, Timestamp: jm.Timestamp,
			Api: jm.Api, Provider: jm.Provider, Model: jm.Model,
			ResponseModel: jm.ResponseModel, ResponseID: jm.ResponseID,
			Usage: usageFromJSON(jm.Usage), StopReason: jm.StopReason,
			ErrorMessage: jm.ErrorMessage,
		}, nil
	case "toolResult":
		var details any
		if len(jm.Details) > 0 {
			if err := json.Unmarshal(jm.Details, &details); err != nil {
				return nil, fmt.Errorf("tool result details: %w", err)
			}
		}
		return &ToolResultMessage{
			Content: content, Timestamp: jm.Timestamp,
			ToolCallID: jm.ToolCallID, ToolName: jm.ToolName,
			Details: details, IsError: jm.IsError,
		}, nil
	default:
		return nil, fmt.Errorf("unknown message role %q", jm.Role)
	}
}

// MarshalMessages encodes a transcript as a JSON array.
func MarshalMessages(messages []AgentMessage) ([]byte, error) {
	out := make([]*jsonMessage, 0, len(messages))
	for _, m := range messages {
		jm, err := messageToJSON(m)
		if err != nil {
			return nil, err
		}
		out = append(out, jm)
	}
	return json.Marshal(out)
}

// UnmarshalMessages decodes a transcript from a JSON array.
func UnmarshalMessages(data []byte) ([]AgentMessage, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}
	out := make([]AgentMessage, 0, len(raws))
	for i, raw := range raws {
		m, err := UnmarshalMessage(raw)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		out = append(out, m)
	}
	return out, nil
}
