// Ported from packages/ai/src/utils/transcript.ts (pi v1.5.2).

package ai

import (
	"bytes"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// CreateInitialSystemMessage builds the leading system message for a prompt and tool set.
// Returns nil when both are empty.
func CreateInitialSystemMessage(systemPrompt *string, tools []*Tool) *SystemMessage {
	hasSystemPrompt := systemPrompt != nil && len(*systemPrompt) > 0
	hasTools := len(tools) > 0
	if !hasSystemPrompt && !hasTools {
		return nil
	}
	prompt := ""
	if systemPrompt != nil {
		prompt = *systemPrompt
	}
	var toolsAdded []*Tool
	if hasTools {
		toolsAdded = append([]*Tool(nil), tools...)
	}
	return &SystemMessage{
		Role:       "system",
		Content:    SystemMessageContent{Text: &prompt},
		ToolsAdded: toolsAdded,
		Timestamp:  0,
	}
}

// NormalizeContext folds Context.SystemPrompt and Context.Tools into a leading system message.
// This is the only entry point that produces a *TranscriptContext.
func NormalizeContext(ctx *Context) *TranscriptContext {
	if ctx == nil {
		return &TranscriptContext{Messages: MessageList{}}
	}
	initial := CreateInitialSystemMessage(ctx.SystemPrompt, ctx.Tools)
	if initial != nil {
		msgs := make(MessageList, 0, 1+len(ctx.Messages))
		msgs = append(msgs, initial)
		msgs = append(msgs, ctx.Messages...)
		return &TranscriptContext{Messages: msgs}
	}
	msgs := make(MessageList, len(ctx.Messages))
	copy(msgs, ctx.Messages)
	return &TranscriptContext{Messages: msgs}
}

// GetInitialSystemMessage returns the leading system message, if the transcript starts with one.
func GetInitialSystemMessage(messages []Message) *SystemMessage {
	if len(messages) == 0 {
		return nil
	}
	sm, _ := messages[0].(*SystemMessage)
	return sm
}

// WithoutInitialSystemMessage drops the leading system message for APIs that carry the prompt outside the message list.
func WithoutInitialSystemMessage(messages []Message) MessageList {
	if GetInitialSystemMessage(messages) != nil {
		out := make(MessageList, len(messages)-1)
		copy(out, messages[1:])
		return out
	}
	out := make(MessageList, len(messages))
	copy(out, messages)
	return out
}

// GetCurrentTools resolves the tools available after applying every transcript delta in order.
func GetCurrentTools(messages []Message) []*Tool {
	tools := omap.NewMap[string, *Tool]()
	for _, msg := range messages {
		sm, ok := msg.(*SystemMessage)
		if !ok || sm == nil {
			continue
		}
		for _, tool := range sm.ToolsRemoved {
			tools.Delete(tool.Name)
		}
		for _, tool := range sm.ToolsAdded {
			if tool != nil {
				tools.Set(tool.Name, tool)
			}
		}
	}
	vals := tools.Values()
	if vals == nil {
		return []*Tool{}
	}
	return vals
}

// GetCurrentSystemMessage replays every system message into one leading system message holding the current prompt and tools.
func GetCurrentSystemMessage(messages []Message) *SystemMessage {
	var content []string
	sections := omap.NewMap[string, string]()
	var timestamp *int64
	for _, msg := range messages {
		sm, ok := msg.(*SystemMessage)
		if !ok || sm == nil {
			continue
		}
		if timestamp == nil {
			ts := sm.Timestamp
			timestamp = &ts
		}
		if text := ContentText(sm.Content); len(text) > 0 {
			content = append(content, text)
		}
		if sm.Sections != nil {
			for name, val := range sm.Sections.All() {
				if val == nil {
					sections.Delete(name)
				} else {
					sections.Set(name, *val)
				}
			}
		}
	}
	tools := GetCurrentTools(messages)
	if timestamp == nil && len(tools) == 0 {
		return nil
	}
	joined := strings.Join(content, "\n\n")
	var sectionsOut *omap.Map[string, *string]
	if sections.Len() > 0 {
		sectionsOut = omap.NewMap[string, *string]()
		for k, v := range sections.All() {
			val := v
			sectionsOut.Set(k, &val)
		}
	}
	var toolsOut []*Tool
	if len(tools) > 0 {
		toolsOut = tools
	}
	ts := int64(0)
	if timestamp != nil {
		ts = *timestamp
	}
	return &SystemMessage{
		Role:       "system",
		Content:    SystemMessageContent{Text: &joined},
		Sections:   sectionsOut,
		ToolsAdded: toolsOut,
		Timestamp:  ts,
	}
}

// GetCurrentSystemPrompt renders the current system prompt text after replaying every system message.
func GetCurrentSystemPrompt(messages []Message) string {
	sm := GetCurrentSystemMessage(messages)
	if sm == nil {
		return ""
	}
	return GetSystemMessageText(sm)
}

// CollapseSystemMessages rebuilds the transcript for APIs without mid-conversation system messages.
func CollapseSystemMessages(ctx *TranscriptContext) *TranscriptContext {
	if ctx == nil {
		return &TranscriptContext{Messages: MessageList{}}
	}
	head := GetCurrentSystemMessage(ctx.Messages)
	var rest MessageList
	for _, msg := range ctx.Messages {
		if _, isSys := msg.(*SystemMessage); !isSys {
			rest = append(rest, msg)
		}
	}
	if head != nil {
		out := make(MessageList, 0, 1+len(rest))
		out = append(out, head)
		out = append(out, rest...)
		return &TranscriptContext{Messages: out}
	}
	if rest == nil {
		rest = MessageList{}
	}
	return &TranscriptContext{Messages: rest}
}

// ResolveTranscript keeps later system messages in place when supportsMidConvoSystemMessages is true; otherwise collapses them.
func ResolveTranscript(ctx *TranscriptContext, supportsMidConvoSystemMessages *bool) *TranscriptContext {
	if supportsMidConvoSystemMessages != nil && *supportsMidConvoSystemMessages {
		return ctx
	}
	return CollapseSystemMessages(ctx)
}

// ToToolDeclaration strips executable and display-only fields from a tool before transcript comparison or persistence.
func ToToolDeclaration(tool *Tool) *Tool {
	if tool == nil {
		return nil
	}
	var params *typebox.Schema
	if tool.Parameters != nil {
		raw, err := jsonx.Marshal(tool.Parameters)
		if err == nil {
			if parsed, err := jsonx.Parse(raw); err == nil {
				if obj, ok := parsed.(*jsonx.Object); ok {
					params = typebox.Unsafe(obj)
				}
			}
		}
		if params == nil && tool.Parameters.Object != nil {
			params = typebox.Unsafe(tool.Parameters.Object.Clone())
		}
	}
	return &Tool{
		Name:                tool.Name,
		Description:         tool.Description,
		Parameters:          params,
		ConstrainedSampling: tool.ConstrainedSampling,
	}
}

// DeclarationsEqual checks whether two tools declare the same interface to the model.
func DeclarationsEqual(left, right *Tool) bool {
	bLeft, errLeft := jsonx.Marshal(ToToolDeclaration(left))
	bRight, errRight := jsonx.Marshal(ToToolDeclaration(right))
	if errLeft != nil || errRight != nil {
		return false
	}
	return bytes.Equal(bLeft, bRight)
}

// ToolStateChanges describes added and removed tools between two tool snapshots.
type ToolStateChanges struct {
	ToolsAdded   []*Tool         `json:"toolsAdded"`
	ToolsRemoved []ToolReference `json:"toolsRemoved"`
}

// GetToolStateChanges compares two complete tool states. A changed definition is a removal followed by an addition.
func GetToolStateChanges(previous, current []*Tool) ToolStateChanges {
	prevMap := make(map[string]*Tool, len(previous))
	for _, t := range previous {
		if t != nil {
			prevMap[t.Name] = t
		}
	}
	currMap := make(map[string]*Tool, len(current))
	for _, t := range current {
		if t != nil {
			currMap[t.Name] = t
		}
	}

	added := make([]*Tool, 0)
	for _, tool := range current {
		if tool == nil {
			continue
		}
		prev, ok := prevMap[tool.Name]
		if !ok || !DeclarationsEqual(prev, tool) {
			added = append(added, ToToolDeclaration(tool))
		}
	}

	removed := make([]ToolReference, 0)
	for _, tool := range previous {
		if tool == nil {
			continue
		}
		curr, ok := currMap[tool.Name]
		if !ok || !DeclarationsEqual(tool, curr) {
			removed = append(removed, ToolReference{Name: tool.Name})
		}
	}

	return ToolStateChanges{
		ToolsAdded:   added,
		ToolsRemoved: removed,
	}
}

// GetDeclaredTools returns every definition referenced by transcript tool state, in first-declaration order.
func GetDeclaredTools(messages []Message) []*Tool {
	defs := omap.NewMap[string, *Tool]()
	for _, msg := range messages {
		sm, ok := msg.(*SystemMessage)
		if !ok || sm == nil {
			continue
		}
		for _, tool := range sm.ToolsAdded {
			if tool != nil {
				defs.Set(tool.Name, tool)
			}
		}
	}
	vals := defs.Values()
	if vals == nil {
		return []*Tool{}
	}
	return vals
}

// HasToolRedefinitions reports whether a tool name was declared twice with different definitions.
// Deprecated: kept for API compatibility.
func HasToolRedefinitions(messages []Message) bool {
	declared := make(map[string]*Tool)
	for _, msg := range messages {
		sm, ok := msg.(*SystemMessage)
		if !ok || sm == nil {
			continue
		}
		for _, tool := range sm.ToolsAdded {
			if tool == nil {
				continue
			}
			if prev, exists := declared[tool.Name]; exists && !DeclarationsEqual(prev, tool) {
				return true
			}
			declared[tool.Name] = tool
		}
	}
	return false
}

// HasNonAdditiveToolChanges reports whether tool history contains a removal or same-name redeclaration.
func HasNonAdditiveToolChanges(messages []Message) bool {
	var sys []*SystemMessage
	for _, msg := range messages {
		if sm, ok := msg.(*SystemMessage); ok && sm != nil {
			sys = append(sys, sm)
		}
	}
	return utrFindLastNonAdditiveToolChange(sys) >= 0
}

func utrFindLastNonAdditiveToolChange(systemMessages []*SystemMessage) int {
	declared := make(map[string]struct{})
	last := -1
	for i, msg := range systemMessages {
		if len(msg.ToolsRemoved) > 0 {
			last = i
		}
		for _, tool := range msg.ToolsAdded {
			if tool == nil {
				continue
			}
			if _, exists := declared[tool.Name]; exists {
				last = i
			}
			declared[tool.Name] = struct{}{}
		}
	}
	return last
}

// TranscriptToolSupport describes how a transport can express tool changes after the first request.
type TranscriptToolSupport string

const (
	TranscriptToolSupportNone      TranscriptToolSupport = "none"
	TranscriptToolSupportAdditions TranscriptToolSupport = "additions"
	TranscriptToolSupportChanges   TranscriptToolSupport = "changes"
)

// TranscriptTools splits tool declarations between top-level request tools and in-place system messages.
type TranscriptTools struct {
	RequestTools         []*Tool `json:"requestTools"`
	FoldedSystemMessages int     `json:"foldedSystemMessages"`
}

// ResolveTranscriptTools splits tool declarations between the top-level request field and in-place changes.
func ResolveTranscriptTools(messages []Message, support TranscriptToolSupport) TranscriptTools {
	var sys []*SystemMessage
	var sysAsMsg []Message
	for _, msg := range messages {
		if sm, ok := msg.(*SystemMessage); ok && sm != nil {
			sys = append(sys, sm)
			sysAsMsg = append(sysAsMsg, sm)
		}
	}
	if support == TranscriptToolSupportNone {
		return TranscriptTools{
			RequestTools:         GetCurrentTools(sysAsMsg),
			FoldedSystemMessages: len(sys),
		}
	}
	if support == TranscriptToolSupportChanges {
		return TranscriptTools{
			RequestTools:         []*Tool{},
			FoldedSystemMessages: 0,
		}
	}
	initialFolded := 0
	if GetInitialSystemMessage(messages) != nil {
		initialFolded = 1
	}
	lastNonAdd := utrFindLastNonAdditiveToolChange(sys) + 1
	folded := max(initialFolded, lastNonAdd)
	return TranscriptTools{
		RequestTools:         GetCurrentTools(sysAsMsg[:min(folded, len(sysAsMsg))]),
		FoldedSystemMessages: folded,
	}
}
