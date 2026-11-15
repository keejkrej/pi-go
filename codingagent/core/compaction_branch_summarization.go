// Ported from packages/coding-agent/src/core/compaction/branch-summarization.ts (pi v1.5.2).

package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// BranchSummaryResult describes the outcome of GenerateBranchSummary.
type BranchSummaryResult struct {
	Summary       *string   `json:"summary,omitzero"`
	Usage         *ai.Usage `json:"usage,omitzero"`
	ReadFiles     []string  `json:"readFiles,omitzero"`
	ModifiedFiles []string  `json:"modifiedFiles,omitzero"`
	Aborted       bool      `json:"aborted,omitzero"`
	Error         *string   `json:"error,omitzero"`
}

// BranchSummaryDetails stores cumulative read/modified file paths in BranchSummaryEntry.Details.
type BranchSummaryDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// BranchPreparation holds the messages, file ops, and token count extracted for branch summarization.
type BranchPreparation struct {
	Messages    []agent.AgentMessage `json:"messages"`
	FileOps     FileOperations       `json:"fileOps"`
	TotalTokens int                  `json:"totalTokens"`
}

// CollectEntriesResult lists session entries along an abandoned branch up to the common ancestor.
type CollectEntriesResult struct {
	Entries          []SessionEntry `json:"entries"`
	CommonAncestorId *string        `json:"commonAncestorId"`
}

// GenerateBranchSummaryOptions configures GenerateBranchSummary.
type GenerateBranchSummaryOptions struct {
	Model               *ai.Model
	ApiKey              *string
	Headers             map[string]string
	Env                 map[string]string
	ThinkingLevel       *agent.ThinkingLevel
	CustomInstructions  *string
	ReplaceInstructions *bool
	ReserveTokens       *int
	StreamFn            agent.StreamFn
	Retry               *ai.RetryPolicy
	Callbacks           *ai.RetryCallbacks
	SessionId           *string
}

// CollectEntriesForBranchSummary collects chronological entries from oldLeafId back to its deepest common ancestor with targetId.
func CollectEntriesForBranchSummary(session ReadonlySessionManager, oldLeafId *string, targetId string) CollectEntriesResult {
	if session == nil || oldLeafId == nil || *oldLeafId == "" {
		return CollectEntriesResult{
			Entries:          []SessionEntry{},
			CommonAncestorId: nil,
		}
	}

	oldPathSet := make(map[string]struct{})
	for _, e := range session.GetBranch(*oldLeafId) {
		if e != nil {
			oldPathSet[e.GetId()] = struct{}{}
		}
	}
	targetPath := session.GetBranch(targetId)

	var commonAncestorId *string
	for i := len(targetPath) - 1; i >= 0; i-- {
		if targetPath[i] == nil {
			continue
		}
		id := targetPath[i].GetId()
		if _, ok := oldPathSet[id]; ok {
			idCopy := id
			commonAncestorId = &idCopy
			break
		}
	}

	var entries []SessionEntry
	current := *oldLeafId
	for current != "" && (commonAncestorId == nil || current != *commonAncestorId) {
		entry := session.GetEntry(current)
		if entry == nil {
			break
		}
		entries = append(entries, entry)
		parent := entry.GetParentId()
		if parent == nil {
			break
		}
		current = *parent
	}

	slices.Reverse(entries)
	if entries == nil {
		entries = []SessionEntry{}
	}
	return CollectEntriesResult{
		Entries:          entries,
		CommonAncestorId: commonAncestorId,
	}
}

func cbsGetMessageFromEntry(entry SessionEntry, edit *ContextEditEntry) agent.AgentMessage {
	if entry == nil {
		return nil
	}
	switch e := entry.(type) {
	case *SessionMessageEntry:
		if e == nil || e.Message == nil {
			return nil
		}
		if e.Message.GetRole() == "toolResult" {
			return nil
		}
		if edit != nil {
			projected := ProjectContextEntry(e, edit)
			if len(projected) > 0 {
				return projected[0]
			}
			return nil
		}
		return e.Message
	case *CustomMessageEntry:
		if e == nil {
			return nil
		}
		if edit != nil {
			projected := ProjectContextEntry(e, edit)
			if len(projected) > 0 {
				return projected[0]
			}
			return nil
		}
		return CreateCustomMessage(e.CustomType, e.Content, e.Display, e.Details, e.Timestamp)
	case *BranchSummaryEntry:
		if e == nil {
			return nil
		}
		return CreateBranchSummaryMessage(e.Summary, e.FromId, e.Timestamp)
	case *CompactionEntry:
		if e == nil {
			return nil
		}
		return CreateCompactionSummaryMessage(e.Summary, e.TokensBefore, e.Timestamp)
	case *ThinkingLevelChangeEntry, *ModelChangeEntry, *CustomEntry, *LabelEntry, *SessionInfoEntry:
		return nil
	default:
		return nil
	}
}

func cbsExtractBranchSummaryDetailsLists(details any) (readFiles, modifiedFiles []string) {
	switch d := details.(type) {
	case *BranchSummaryDetails:
		if d != nil {
			return d.ReadFiles, d.ModifiedFiles
		}
	case BranchSummaryDetails:
		return d.ReadFiles, d.ModifiedFiles
	case *CompactionDetails:
		if d != nil {
			return d.ReadFiles, d.ModifiedFiles
		}
	case CompactionDetails:
		return d.ReadFiles, d.ModifiedFiles
	case map[string]any:
		return ccExtractStringSlice(d["readFiles"]), ccExtractStringSlice(d["modifiedFiles"])
	case *jsonx.Object:
		if d != nil {
			if rf, ok := d.Get("readFiles"); ok {
				readFiles = ccExtractStringSlice(rf)
			}
			if mf, ok := d.Get("modifiedFiles"); ok {
				modifiedFiles = ccExtractStringSlice(mf)
			}
		}
	}
	return readFiles, modifiedFiles
}

// PrepareBranchEntries extracts messages from chronological session entries up to tokenBudget (0 = unlimited),
// walking newest to oldest and accumulating file operations across all branch summaries.
func PrepareBranchEntries(entries []SessionEntry, tokenBudget ...int) BranchPreparation {
	budget := 0
	if len(tokenBudget) > 0 {
		budget = tokenBudget[0]
	}

	var messages []agent.AgentMessage
	fileOps := CreateFileOps()
	totalTokens := 0

	for _, entry := range entries {
		bs, ok := entry.(*BranchSummaryEntry)
		if !ok || bs == nil {
			continue
		}
		fromHook := bs.FromHook != nil && *bs.FromHook
		if !fromHook && bs.Details != nil {
			readFiles, modifiedFiles := cbsExtractBranchSummaryDetailsLists(bs.Details)
			for _, f := range readFiles {
				fileOps.Read[f] = struct{}{}
			}
			for _, f := range modifiedFiles {
				fileOps.Edited[f] = struct{}{}
			}
		}
	}

	edits := make(map[string]*ContextEditEntry)
	for _, entry := range entries {
		if edit, ok := entry.(*ContextEditEntry); ok && edit != nil {
			edits[edit.TargetId] = edit
		}
	}

	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry == nil {
			continue
		}
		message := cbsGetMessageFromEntry(entry, edits[entry.GetId()])
		if message == nil {
			continue
		}

		ExtractFileOpsFromMessage(message, &fileOps)
		tokens := EstimateTokens(message)

		if budget > 0 && totalTokens+tokens > budget {
			_, isCompaction := entry.(*CompactionEntry)
			_, isBranchSummary := entry.(*BranchSummaryEntry)
			if isCompaction || isBranchSummary {
				if float64(totalTokens) < math.Floor(float64(budget)*0.9) || float64(totalTokens) < float64(budget)*0.9 {
					messages = append([]agent.AgentMessage{message}, messages...)
					totalTokens += tokens
				}
			}
			break
		}

		messages = append([]agent.AgentMessage{message}, messages...)
		totalTokens += tokens
	}

	if messages == nil {
		messages = []agent.AgentMessage{}
	}
	return BranchPreparation{
		Messages:    messages,
		FileOps:     fileOps,
		TotalTokens: totalTokens,
	}
}

const cbsBranchSummaryPreamble = `The user explored a different conversation branch before returning here.
Summary of that exploration:

`

const cbsBranchSummaryPrompt = `Create a structured summary of this conversation branch for context when returning later.

Use this EXACT format:

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work that was started but not finished]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue this work]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// GenerateBranchSummary generates a structured summary of abandoned branch entries.
func GenerateBranchSummary(
	ctx context.Context,
	entries []SessionEntry,
	options GenerateBranchSummaryOptions,
) (*BranchSummaryResult, error) {
	if options.Model == nil {
		return nil, errors.New("branch summarization requires a model")
	}

	contextWindow := options.Model.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	reserveTokens := 16384
	if options.ReserveTokens != nil {
		reserveTokens = *options.ReserveTokens
	}
	tokenBudget := contextWindow - reserveTokens

	prep := PrepareBranchEntries(entries, tokenBudget)
	if len(prep.Messages) == 0 {
		summary := "No content to summarize"
		return &BranchSummaryResult{Summary: &summary}, nil
	}

	llmMessages := ConvertToLlm(prep.Messages)
	conversationText := SerializeConversation(llmMessages)

	replaceInstructions := options.ReplaceInstructions != nil && *options.ReplaceInstructions
	hasCustom := options.CustomInstructions != nil && *options.CustomInstructions != ""
	var instructions string
	switch {
	case replaceInstructions && hasCustom:
		instructions = *options.CustomInstructions
	case hasCustom:
		instructions = cbsBranchSummaryPrompt + "\n\nAdditional focus: " + *options.CustomInstructions
	default:
		instructions = cbsBranchSummaryPrompt
	}

	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n%s", conversationText, instructions)
	requestOptions := ccCreateSummarizationOptions(
		options.Model,
		options.ApiKey,
		options.Headers,
		options.Env,
		options.ThinkingLevel,
		options.SessionId,
	)

	response, err := CompleteSummarization(
		ctx,
		options.Model,
		ccBuildSummarizationContext(promptText),
		requestOptions,
		options.StreamFn,
		options.Retry,
		options.Callbacks,
	)
	if err != nil {
		return nil, err
	}

	if response.StopReason == ai.StopReasonAborted {
		return &BranchSummaryResult{Aborted: true}, nil
	}
	if failure := GetSummarizationFailure(response, "Branch summarization"); failure != nil {
		return &BranchSummaryResult{Error: failure}, nil
	}
	if ccHasToolCallBlock(response.Content) {
		msg := "Branch summarization attempted to call a tool"
		return &BranchSummaryResult{Error: &msg}, nil
	}

	summary := cbsBranchSummaryPreamble + ai.ContentText(response.Content)
	readFiles, modifiedFiles := ComputeFileLists(prep.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)
	if summary == "" {
		summary = "No summary generated"
	}
	if readFiles == nil {
		readFiles = []string{}
	}
	if modifiedFiles == nil {
		modifiedFiles = []string{}
	}
	usageCopy := response.Usage
	return &BranchSummaryResult{
		Summary:       &summary,
		Usage:         &usageCopy,
		ReadFiles:     readFiles,
		ModifiedFiles: modifiedFiles,
	}, nil
}
