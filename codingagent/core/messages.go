// Ported from packages/coding-agent/src/core/messages.ts (pi v1.5.2).

package core

import (
	"fmt"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Summary prompt wrapper markers used when converting branch/compaction summary messages for LLMs.
const (
	CompactionSummaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	CompactionSummarySuffix = "\n</summary>"
	BranchSummaryPrefix     = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	BranchSummarySuffix     = "</summary>"
)

// BashExecutionMessage records a user-initiated `!` or `!!` bash execution in the session.
type BashExecutionMessage struct {
	Role               string  `json:"role"`
	Command            string  `json:"command"`
	Output             string  `json:"output"`
	ExitCode           *int    `json:"exitCode,omitzero"`
	Cancelled          bool    `json:"cancelled"`
	Truncated          bool    `json:"truncated"`
	FullOutputPath     *string `json:"fullOutputPath,omitzero"`
	Timestamp          int64   `json:"timestamp"`
	ExcludeFromContext *bool   `json:"excludeFromContext,omitzero"`
}

func (m *BashExecutionMessage) GetRole() string {
	if m != nil && m.Role != "" {
		return m.Role
	}
	return "bashExecution"
}

// CustomMessage is an extension-injected message entry stored in the conversation.
type CustomMessage struct {
	Role       string                `json:"role"`
	CustomType string                `json:"customType"`
	Content    ai.UserMessageContent `json:"content"`
	Display    bool                  `json:"display"`
	Details    jsonx.Opt[any]        `json:"details,omitzero"`
	Timestamp  int64                 `json:"timestamp"`
}

func (m *CustomMessage) GetRole() string {
	if m != nil && m.Role != "" {
		return m.Role
	}
	return "custom"
}

// BranchSummaryMessage inserts a summarized abandoned branch back into active context.
type BranchSummaryMessage struct {
	Role      string  `json:"role"`
	Summary   string  `json:"summary"`
	FromId    *string `json:"fromId"`
	Timestamp int64   `json:"timestamp"`
}

func (m *BranchSummaryMessage) GetRole() string {
	if m != nil && m.Role != "" {
		return m.Role
	}
	return "branchSummary"
}

// CompactionSummaryMessage represents a compacted history summary in the conversation.
type CompactionSummaryMessage struct {
	Role         string `json:"role"`
	Summary      string `json:"summary"`
	TokensBefore int    `json:"tokensBefore"`
	Timestamp    int64  `json:"timestamp"`
}

func (m *CompactionSummaryMessage) GetRole() string {
	if m != nil && m.Role != "" {
		return m.Role
	}
	return "compactionSummary"
}

// BashExecutionToText converts a BashExecutionMessage to LLM user message text.
func BashExecutionToText(msg BashExecutionMessage) string {
	text := fmt.Sprintf("Ran `%s`\n", msg.Command)
	if msg.Output != "" {
		text += fmt.Sprintf("```\n%s\n```", msg.Output)
	} else {
		text += "(no output)"
	}
	if msg.Cancelled {
		text += "\n\n(command cancelled)"
	} else if msg.ExitCode != nil && *msg.ExitCode != 0 {
		text += fmt.Sprintf("\n\nCommand exited with code %d", *msg.ExitCode)
	}
	if msg.Truncated && msg.FullOutputPath != nil && *msg.FullOutputPath != "" {
		text += fmt.Sprintf("\n\n[Output truncated. Full output: %s]", *msg.FullOutputPath)
	}
	return text
}

func messParseTimestamp(ts string) int64 {
	if ts == "" {
		return 0
	}
	if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return parsed.UnixMilli()
	}
	if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
		return parsed.UnixMilli()
	}
	return 0
}

// CreateBranchSummaryMessage constructs a BranchSummaryMessage from a session entry timestamp.
func CreateBranchSummaryMessage(summary, fromId, timestamp string) *BranchSummaryMessage {
	var fromPtr *string
	if fromId != "" {
		f := fromId
		fromPtr = &f
	}
	return &BranchSummaryMessage{
		Role:      "branchSummary",
		Summary:   summary,
		FromId:    fromPtr,
		Timestamp: messParseTimestamp(timestamp),
	}
}

// CreateCompactionSummaryMessage constructs a CompactionSummaryMessage from a session entry timestamp.
func CreateCompactionSummaryMessage(summary string, tokensBefore int, timestamp string) *CompactionSummaryMessage {
	return &CompactionSummaryMessage{
		Role:         "compactionSummary",
		Summary:      summary,
		TokensBefore: tokensBefore,
		Timestamp:    messParseTimestamp(timestamp),
	}
}

// CreateCustomMessage converts CustomMessageEntry fields into a CustomMessage AgentMessage.
func CreateCustomMessage(customType string, content any, display bool, details any, timestamp string) *CustomMessage {
	var det jsonx.Opt[any]
	if details != nil {
		det = jsonx.Some(details)
	}
	return &CustomMessage{
		Role:       "custom",
		CustomType: customType,
		Content:    messNormalizeUserContent(content),
		Display:    display,
		Details:    det,
		Timestamp:  messParseTimestamp(timestamp),
	}
}

func messNormalizeUserContent(v any) ai.UserMessageContent {
	switch c := v.(type) {
	case ai.UserMessageContent:
		return c
	case *ai.UserMessageContent:
		if c != nil {
			return *c
		}
	case string:
		s := c
		return ai.UserMessageContent{Text: &s}
	case *string:
		if c != nil {
			s := *c
			return ai.UserMessageContent{Text: &s}
		}
	case ai.ContentList:
		return ai.UserMessageContent{Blocks: c}
	case []ai.Content:
		return ai.UserMessageContent{Blocks: ai.ContentList(c)}
	case []*ai.TextContent:
		blocks := make(ai.ContentList, len(c))
		for i, b := range c {
			blocks[i] = b
		}
		return ai.UserMessageContent{Blocks: blocks}
	default:
		if raw, err := jsonx.Marshal(v); err == nil {
			var parsed ai.UserMessageContent
			if err := parsed.UnmarshalJSON(raw); err == nil {
				return parsed
			}
		}
	}
	empty := ""
	return ai.UserMessageContent{Text: &empty}
}

func messTextUserMessage(text string, timestamp int64) *ai.UserMessage {
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: text}}},
		Timestamp: timestamp,
	}
}

// ConvertToLlm transforms AgentMessages (including coding-agent custom messages) to LLM-compatible ai.Messages.
func ConvertToLlm(messages []agent.AgentMessage) []ai.Message {
	out := make([]ai.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		switch m := msg.(type) {
		case *BashExecutionMessage:
			if m == nil || (m.ExcludeFromContext != nil && *m.ExcludeFromContext) {
				continue
			}
			out = append(out, messTextUserMessage(BashExecutionToText(*m), m.Timestamp))
		case *CustomMessage:
			if m == nil {
				continue
			}
			var content ai.UserMessageContent
			if m.Content.Text != nil {
				content = ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: *m.Content.Text}}}
			} else {
				content = ai.UserMessageContent{Blocks: m.Content.Blocks}
			}
			out = append(out, &ai.UserMessage{
				Role:      "user",
				Content:   content,
				Timestamp: m.Timestamp,
			})
		case *BranchSummaryMessage:
			if m == nil {
				continue
			}
			out = append(out, messTextUserMessage(BranchSummaryPrefix+m.Summary+BranchSummarySuffix, m.Timestamp))
		case *CompactionSummaryMessage:
			if m == nil {
				continue
			}
			out = append(out, messTextUserMessage(CompactionSummaryPrefix+m.Summary+CompactionSummarySuffix, m.Timestamp))
		case *ai.SystemMessage:
			if m != nil {
				out = append(out, m)
			}
		case *ai.UserMessage:
			if m != nil {
				out = append(out, m)
			}
		case *ai.AssistantMessage:
			if m != nil {
				out = append(out, m)
			}
		case *ai.ToolResultMessage:
			if m != nil {
				out = append(out, m)
			}
		}
	}
	return out
}
