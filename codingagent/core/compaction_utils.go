// Ported from packages/coding-agent/src/core/compaction/utils.ts (pi v1.5.2).

package core

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// cuToolResultMaxChars caps serialized tool result text included in summarization prompts.
const cuToolResultMaxChars = 2000

// FileOperations tracks files read, edited, and written during a session span.
type FileOperations struct {
	Read    map[string]struct{} `json:"read"`
	Edited  map[string]struct{} `json:"edited"`
	Written map[string]struct{} `json:"written"`
}

// CreateFileOps returns an initialized FileOperations value with non-nil sets.
func CreateFileOps() FileOperations {
	return FileOperations{
		Read:    make(map[string]struct{}),
		Edited:  make(map[string]struct{}),
		Written: make(map[string]struct{}),
	}
}

func cuEnsureFileOps(fileOps *FileOperations) {
	if fileOps.Read == nil {
		fileOps.Read = make(map[string]struct{})
	}
	if fileOps.Edited == nil {
		fileOps.Edited = make(map[string]struct{})
	}
	if fileOps.Written == nil {
		fileOps.Written = make(map[string]struct{})
	}
}

func cuAddFileOp(toolName string, args *jsonx.Object, fileOps *FileOperations) {
	if args == nil {
		return
	}
	pathVal, ok := args.Get("path")
	if !ok {
		return
	}
	path, ok := pathVal.(string)
	if !ok || path == "" {
		return
	}
	cuEnsureFileOps(fileOps)
	switch toolName {
	case "read":
		fileOps.Read[path] = struct{}{}
	case "write":
		fileOps.Written[path] = struct{}{}
	case "edit":
		fileOps.Edited[path] = struct{}{}
	}
}

// ExtractFileOpsFromMessage collects file read/write/edit tool operations from an assistant message
// or from nested tool calls recorded on a tool result message.
func ExtractFileOpsFromMessage(message agent.AgentMessage, fileOps *FileOperations) {
	if fileOps == nil || message == nil {
		return
	}
	switch msg := message.(type) {
	case *ai.ToolResultMessage:
		if msg == nil || msg.NestedCalls == nil {
			return
		}
		for _, call := range msg.NestedCalls.Calls {
			cuAddFileOp(call.Name, call.Arguments, fileOps)
		}
	case *ai.AssistantMessage:
		if msg == nil {
			return
		}
		for _, block := range msg.Content {
			tc, ok := block.(*ai.ToolCall)
			if !ok || tc == nil {
				continue
			}
			cuAddFileOp(tc.Name, tc.Arguments, fileOps)
		}
	}
}

// ComputeFileLists computes sorted read-only and modified file lists from tracked operations.
// Files that were edited or written are excluded from readFiles.
func ComputeFileLists(fileOps FileOperations) (readFiles []string, modifiedFiles []string) {
	modifiedSet := make(map[string]struct{}, len(fileOps.Edited)+len(fileOps.Written))
	for f := range fileOps.Edited {
		modifiedSet[f] = struct{}{}
	}
	for f := range fileOps.Written {
		modifiedSet[f] = struct{}{}
	}

	readFiles = make([]string, 0, len(fileOps.Read))
	for f := range fileOps.Read {
		if _, isMod := modifiedSet[f]; !isMod {
			readFiles = append(readFiles, f)
		}
	}
	sort.Strings(readFiles)

	modifiedFiles = make([]string, 0, len(modifiedSet))
	for f := range modifiedSet {
		modifiedFiles = append(modifiedFiles, f)
	}
	sort.Strings(modifiedFiles)

	return readFiles, modifiedFiles
}

// FormatFileOperations formats read and modified file lists as XML sections appended to summaries.
func FormatFileOperations(readFiles, modifiedFiles []string) string {
	var sections []string
	if len(readFiles) > 0 {
		sections = append(sections, fmt.Sprintf("<read-files>\n%s\n</read-files>", strings.Join(readFiles, "\n")))
	}
	if len(modifiedFiles) > 0 {
		sections = append(sections, fmt.Sprintf("<modified-files>\n%s\n</modified-files>", strings.Join(modifiedFiles, "\n")))
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

// cuTruncateForSummary truncates text to maxChars UTF-16 code units matching TS string length semantics.
func cuTruncateForSummary(text string, maxChars int) string {
	utf16Units := utf16.Encode([]rune(text))
	if len(utf16Units) <= maxChars {
		return text
	}
	truncatedChars := len(utf16Units) - maxChars
	return fmt.Sprintf("%s\n\n[... %d more characters truncated]", SliceHead(text, maxChars), truncatedChars)
}

func cuFormatToolCallArgs(args *jsonx.Object) string {
	if args == nil || args.Len() == 0 {
		return ""
	}
	parts := make([]string, 0, args.Len())
	for k, v := range args.All() {
		raw, err := jsonx.Marshal(v)
		var valStr string
		if err != nil {
			valStr = fmt.Sprintf("%q", fmt.Sprint(v))
		} else {
			valStr = string(raw)
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, valStr))
	}
	return strings.Join(parts, ", ")
}

func cuHasTextBlock(content ai.ContentList) bool {
	for _, block := range content {
		if _, ok := block.(*ai.TextContent); ok {
			return true
		}
	}
	return false
}

// SerializeConversation formats LLM messages into plain-text dialogue for summarization prompts.
// Tool results are truncated to keep the summarization request within context limits.
func SerializeConversation(messages []ai.Message) string {
	var parts []string

	for _, message := range messages {
		if message == nil {
			continue
		}
		switch msg := message.(type) {
		case *ai.UserMessage:
			if msg == nil {
				continue
			}
			content := ai.ContentText(msg.Content, "")
			if content != "" {
				parts = append(parts, "[User]: "+content)
			}
		case *ai.AssistantMessage:
			if msg == nil {
				continue
			}
			var toolCalls []string
			for _, block := range msg.Content {
				if tc, ok := block.(*ai.ToolCall); ok && tc != nil {
					toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", tc.Name, cuFormatToolCallArgs(tc.Arguments)))
				}
			}
			if cuHasTextBlock(msg.Content) {
				parts = append(parts, "[Assistant]: "+ai.ContentText(msg.Content))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
			}
		case *ai.ToolResultMessage:
			if msg == nil {
				continue
			}
			content := ai.ContentText(msg.Content, "")
			if content != "" {
				parts = append(parts, "[Tool result]: "+cuTruncateForSummary(content, cuToolResultMaxChars))
			}
		}
	}

	return strings.Join(parts, "\n\n")
}

// SummarizationSystemPrompt instructs the summarization LLM to emit only the structured checkpoint.
const SummarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`
