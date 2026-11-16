// Ported from packages/durable/src/entries.ts (pi v1.0.0).

package durable

import "github.com/keejkrej/pi-go/internal/js"

// DefineEntry defines a typed entry kind whose Is() guard narrows by EntryRecord.Kind.
func DefineEntry(kind string) (*Entry, error) {
	if kind == "" {
		return nil, js.NewTypeError("Entry kind must be a non-empty string")
	}
	return &Entry{Kind: kind}, nil
}

func entrMustDefine(kind string) *Entry {
	entry, err := DefineEntry(kind)
	if err != nil {
		panic(err)
	}
	return entry
}

var (
	// UserEntry is user input: model is [UserMessage]. Written by submissions.
	UserEntry = entrMustDefine("pi.user")
	// AssistantEntry is a provider result with any stop reason: model is [AssistantMessage]. Written by generation.
	AssistantEntry = entrMustDefine("pi.assistant")
	// SystemEntry is a positional prompt and tool change: model is [SystemMessage] with empty content.
	SystemEntry = entrMustDefine("pi.system")
	// ToolResultEntry is a tool result: model is [ToolResultMessage], whose content ends with the rendered diagnostics block;
	// data holds the structured diagnostics, possibly none.
	ToolResultEntry = entrMustDefine("pi.tool-result")
	// ResetEntry starts a new context: always head "self", with model absent for a plain reset or [UserMessage] carrying handoff text.
	ResetEntry = entrMustDefine("pi.reset")
	// CompactionEntry is a compaction summary: model is [UserMessage] with the wrapped summary, head the first kept entry.
	CompactionEntry = entrMustDefine("pi.compaction")
)
