// Ported from packages/coding-agent/src/core/compaction/compaction.ts (pi v1.5.2).

package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"unicode/utf16"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// CompactionDetails stores tracked read and modified file paths inside CompactionEntry.Details.
type CompactionDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// CompactionResult holds the output of Compact() before persistence to SessionManager.
type CompactionResult struct {
	Summary              string    `json:"summary"`
	FirstKeptEntryId     string    `json:"firstKeptEntryId"`
	TokensBefore         int       `json:"tokensBefore"`
	EstimatedTokensAfter *int      `json:"estimatedTokensAfter,omitzero"`
	Usage                *ai.Usage `json:"usage,omitzero"`
	Details              any       `json:"details,omitzero"`
}

// CompactionSettings configures automatic compaction thresholds and retention budgets.
type CompactionSettings struct {
	Enabled          bool `json:"enabled"`
	ReserveTokens    int  `json:"reserveTokens"`
	KeepRecentTokens int  `json:"keepRecentTokens"`
}

// DefaultCompactionSettings matches TS DEFAULT_COMPACTION_SETTINGS.
var DefaultCompactionSettings = CompactionSettings{
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
}

// ContextUsageEstimate summarizes measured and heuristic token usage for an active context.
type ContextUsageEstimate struct {
	Tokens         int  `json:"tokens"`
	UsageTokens    int  `json:"usageTokens"`
	TrailingTokens int  `json:"trailingTokens"`
	LastUsageIndex *int `json:"lastUsageIndex"`
}

// CutPointResult identifies the entry index where compaction begins retaining raw context.
type CutPointResult struct {
	FirstKeptEntryIndex int  `json:"firstKeptEntryIndex"`
	TurnStartIndex      int  `json:"turnStartIndex"`
	IsSplitTurn         bool `json:"isSplitTurn"`
}

// CompactionPreparation captures everything needed to execute compaction summarization.
type CompactionPreparation struct {
	FirstKeptEntryId    string               `json:"firstKeptEntryId"`
	MessagesToSummarize []agent.AgentMessage `json:"messagesToSummarize"`
	TurnPrefixMessages  []agent.AgentMessage `json:"turnPrefixMessages"`
	IsSplitTurn         bool                 `json:"isSplitTurn"`
	TokensBefore        int                  `json:"tokensBefore"`
	PreviousSummary     *string              `json:"previousSummary,omitzero"`
	FileOps             FileOperations       `json:"fileOps"`
	Settings            CompactionSettings   `json:"settings"`
}

// SummaryWithUsageResult pairs a generated summary string with LLM token usage.
type SummaryWithUsageResult struct {
	Text  string   `json:"text"`
	Usage ai.Usage `json:"usage"`
}

// CompactRunOptions configures Compact(), GenerateSummary(), and GenerateSummaryWithUsage().
// Named CompactRunOptions to avoid collision with extension CompactOptions.
type CompactRunOptions struct {
	Headers            map[string]string
	CustomInstructions *string
	PreviousSummary    *string
	ReserveTokens      *int
	ThinkingLevel      *agent.ThinkingLevel
	StreamFn           agent.StreamFn
	Env                map[string]string
	Retry              *ai.RetryPolicy
	Callbacks          *ai.RetryCallbacks
	SessionId          *string
}

const ccEstimatedImageChars = 4800

func ccStrLen(s string) int {
	return len(utf16.Encode([]rune(s)))
}

func ccExtractStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func ccExtractFileListsFromDetails(details any) (readFiles, modifiedFiles []string) {
	switch d := details.(type) {
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

func ccExtractFileOperations(messages []agent.AgentMessage, entries []SessionEntry, prevCompactionIndex int) FileOperations {
	fileOps := CreateFileOps()

	if prevCompactionIndex >= 0 && prevCompactionIndex < len(entries) {
		if prevCompaction, ok := entries[prevCompactionIndex].(*CompactionEntry); ok && prevCompaction != nil {
			fromHook := prevCompaction.FromHook != nil && *prevCompaction.FromHook
			if !fromHook && prevCompaction.Details != nil {
				readFiles, modifiedFiles := ccExtractFileListsFromDetails(prevCompaction.Details)
				for _, f := range readFiles {
					fileOps.Read[f] = struct{}{}
				}
				for _, f := range modifiedFiles {
					fileOps.Edited[f] = struct{}{}
				}
			}
		}
	}

	for _, msg := range messages {
		ExtractFileOpsFromMessage(msg, &fileOps)
	}
	return fileOps
}

func ccGetMessagesFromProjectedEntryForCompaction(entry ProjectedSessionEntry) []agent.AgentMessage {
	if entry.SourceEntry == nil {
		return nil
	}
	if _, isCompaction := entry.SourceEntry.(*CompactionEntry); isCompaction {
		return nil
	}
	var out []agent.AgentMessage
	for _, m := range entry.Messages {
		if m != nil && m.GetRole() != "system" {
			out = append(out, m)
		}
	}
	return out
}

// CalculateContextTokens computes total context tokens from a Usage record.
func CalculateContextTokens(usage ai.Usage) int {
	return ai.CalculateContextTokens(&usage)
}

func ccGetAssistantUsage(msg agent.AgentMessage) *ai.Usage {
	if msg == nil || msg.GetRole() != "assistant" {
		return nil
	}
	var assistant *ai.AssistantMessage
	if m, ok := msg.(*ai.AssistantMessage); ok {
		assistant = m
	} else {
		return nil
	}
	if assistant == nil ||
		assistant.StopReason == ai.StopReasonAborted ||
		assistant.StopReason == ai.StopReasonError ||
		CalculateContextTokens(assistant.Usage) <= 0 {
		return nil
	}
	copied := assistant.Usage
	return &copied
}

// GetLastAssistantUsage returns the last non-aborted, non-error assistant usage with positive tokens.
func GetLastAssistantUsage(entries []SessionEntry) *ai.Usage {
	for i := len(entries) - 1; i >= 0; i-- {
		if sm, ok := entries[i].(*SessionMessageEntry); ok && sm != nil {
			if usage := ccGetAssistantUsage(sm.Message); usage != nil {
				return usage
			}
		}
	}
	return nil
}

func ccGetLastAssistantUsageInfo(messages []agent.AgentMessage) (*ai.Usage, int) {
	for i := len(messages) - 1; i >= 0; i-- {
		if usage := ccGetAssistantUsage(messages[i]); usage != nil {
			return usage, i
		}
	}
	return nil, -1
}

// EstimateContextTokens estimates total context tokens anchored at the latest valid assistant usage.
func EstimateContextTokens(messages []agent.AgentMessage) ContextUsageEstimate {
	usage, idx := ccGetLastAssistantUsageInfo(messages)
	if usage == nil {
		estimated := 0
		for _, m := range messages {
			estimated += EstimateTokens(m)
		}
		return ContextUsageEstimate{
			Tokens:         estimated,
			UsageTokens:    0,
			TrailingTokens: estimated,
			LastUsageIndex: nil,
		}
	}

	usageTokens := CalculateContextTokens(*usage)
	trailingTokens := 0
	for i := idx + 1; i < len(messages); i++ {
		trailingTokens += EstimateTokens(messages[i])
	}
	idxCopy := idx
	return ContextUsageEstimate{
		Tokens:         usageTokens + trailingTokens,
		UsageTokens:    usageTokens,
		TrailingTokens: trailingTokens,
		LastUsageIndex: &idxCopy,
	}
}

// EstimateProjectedContextTokens estimates projected context tokens without trusting usage
// recorded before a later context edit or compaction entry.
func EstimateProjectedContextTokens(projection SessionProjection, branchEntries []SessionEntry) ContextUsageEstimate {
	estimate := EstimateContextTokens(projection.Messages)
	if estimate.LastUsageIndex != nil {
		projectedMessageIndex := 0
		var usageEntryId string
		for _, entry := range projection.Entries {
			nextMessageIndex := projectedMessageIndex + len(entry.Messages)
			if *estimate.LastUsageIndex < nextMessageIndex {
				if entry.SourceEntry != nil {
					usageEntryId = entry.SourceEntry.GetId()
				}
				break
			}
			projectedMessageIndex = nextMessageIndex
		}

		usageEntryIndex := -1
		if usageEntryId != "" {
			for i, entry := range branchEntries {
				if entry != nil && entry.GetId() == usageEntryId {
					usageEntryIndex = i
					break
				}
			}
		}

		latestInvalidatingEntryIndex := -1
		for i := len(branchEntries) - 1; i >= 0; i-- {
			switch branchEntries[i].(type) {
			case *ContextEditEntry, *CompactionEntry:
				latestInvalidatingEntryIndex = i
			}
			if latestInvalidatingEntryIndex != -1 {
				break
			}
		}
		if usageEntryIndex > latestInvalidatingEntryIndex {
			return estimate
		}
	}

	var sysMsgs []ai.Message
	for _, m := range projection.Messages {
		if s, ok := m.(*ai.SystemMessage); ok && s != nil {
			sysMsgs = append(sysMsgs, s)
		}
	}
	currentSystem := ai.GetCurrentSystemMessage(sysMsgs)
	tokens := 0
	if currentSystem != nil {
		tokens += EstimateTokens(currentSystem)
	}
	for _, m := range projection.Messages {
		if m != nil && m.GetRole() != "system" {
			tokens += EstimateTokens(m)
		}
	}
	return ContextUsageEstimate{
		Tokens:         tokens,
		UsageTokens:    0,
		TrailingTokens: tokens,
		LastUsageIndex: nil,
	}
}

// ShouldCompact reports whether contextTokens exceeds contextWindow - settings.ReserveTokens.
func ShouldCompact(contextTokens, contextWindow int, settings CompactionSettings) bool {
	if !settings.Enabled {
		return false
	}
	return contextTokens > contextWindow-settings.ReserveTokens
}

func ccEstimateContentListChars(blocks ai.ContentList) int {
	chars := 0
	for _, block := range blocks {
		switch b := block.(type) {
		case *ai.TextContent:
			if b != nil {
				chars += ccStrLen(b.Text)
			}
		case *ai.ImageContent:
			if b != nil {
				chars += ccEstimatedImageChars
			}
		}
	}
	return chars
}

func ccEstimateUserContentChars(content ai.UserMessageContent) int {
	if content.Text != nil {
		return ccStrLen(*content.Text)
	}
	return ccEstimateContentListChars(content.Blocks)
}

func ccEstimateSystemContentChars(content ai.SystemMessageContent) int {
	if content.Text != nil {
		return ccStrLen(*content.Text)
	}
	return ccEstimateContentListChars(content.Blocks)
}

func ccCeilDiv4(chars int) int {
	return int(math.Ceil(float64(chars) / 4.0))
}

// EstimateTokens estimates token count for a message using the conservative chars/4 heuristic.
func EstimateTokens(message agent.AgentMessage) int {
	if message == nil {
		return 0
	}
	chars := 0
	switch m := message.(type) {
	case *ai.SystemMessage:
		if m == nil {
			return 0
		}
		chars = ccEstimateSystemContentChars(m.Content)
		if m.Sections != nil {
			for _, section := range m.Sections.All() {
				if section != nil {
					chars += ccStrLen(*section)
				}
			}
		}
		if len(m.ToolsAdded) > 0 {
			if raw, err := jsonx.Marshal(m.ToolsAdded); err == nil {
				chars += ccStrLen(string(raw))
			}
		}
		return ccCeilDiv4(chars)
	case *ai.UserMessage:
		if m == nil {
			return 0
		}
		return ccCeilDiv4(ccEstimateUserContentChars(m.Content))
	case *ai.AssistantMessage:
		if m == nil {
			return 0
		}
		for _, block := range m.Content {
			switch b := block.(type) {
			case *ai.TextContent:
				if b != nil {
					chars += ccStrLen(b.Text)
				}
			case *ai.ThinkingContent:
				if b != nil {
					chars += ccStrLen(b.Thinking)
				}
			case *ai.ToolCall:
				if b != nil {
					chars += ccStrLen(b.Name)
					if raw, err := jsonx.Marshal(b.Arguments); err == nil {
						chars += ccStrLen(string(raw))
					} else {
						chars += 2
					}
				}
			}
		}
		return ccCeilDiv4(chars)
	case *CustomMessage:
		if m == nil {
			return 0
		}
		return ccCeilDiv4(ccEstimateUserContentChars(m.Content))
	case *ai.ToolResultMessage:
		if m == nil {
			return 0
		}
		return ccCeilDiv4(ccEstimateContentListChars(m.Content))
	case *BashExecutionMessage:
		if m == nil {
			return 0
		}
		return ccCeilDiv4(ccStrLen(m.Command) + ccStrLen(m.Output))
	case *BranchSummaryMessage:
		if m == nil {
			return 0
		}
		return ccCeilDiv4(ccStrLen(m.Summary))
	case *CompactionSummaryMessage:
		if m == nil {
			return 0
		}
		return ccCeilDiv4(ccStrLen(m.Summary))
	default:
		return 0
	}
}

func ccIsCutPointMessage(message agent.AgentMessage) bool {
	if message == nil {
		return false
	}
	switch message.GetRole() {
	case "user", "assistant", "bashExecution", "custom", "branchSummary", "compactionSummary":
		return true
	case "toolResult":
		return false
	default:
		return false
	}
}

func ccIsTurnStartMessage(message agent.AgentMessage) bool {
	if message == nil {
		return false
	}
	switch message.GetRole() {
	case "user", "bashExecution", "custom", "branchSummary", "compactionSummary":
		return true
	case "assistant", "toolResult":
		return false
	default:
		return false
	}
}

func ccIsTurnStartEntry(entry SessionEntry) bool {
	if entry == nil {
		return false
	}
	if _, isCompaction := entry.(*CompactionEntry); isCompaction {
		return false
	}
	for _, m := range SessionEntryToContextMessages(entry) {
		if ccIsTurnStartMessage(m) {
			return true
		}
	}
	return false
}

func ccFindValidCutPoints(entries []SessionEntry, startIndex, endIndex int) []int {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		entry := entries[i]
		if entry == nil {
			continue
		}
		if _, isCompaction := entry.(*CompactionEntry); isCompaction {
			continue
		}
		for _, m := range SessionEntryToContextMessages(entry) {
			if ccIsCutPointMessage(m) {
				cutPoints = append(cutPoints, i)
				break
			}
		}
	}
	return cutPoints
}

// FindTurnStartIndex returns the index of the context-visible user-role message starting the turn at entryIndex.
// Returns -1 if no turn start is found at or after startIndex.
func FindTurnStartIndex(entries []SessionEntry, entryIndex, startIndex int) int {
	if entryIndex >= len(entries) {
		entryIndex = len(entries) - 1
	}
	for i := entryIndex; i >= startIndex; i-- {
		if ccIsTurnStartEntry(entries[i]) {
			return i
		}
	}
	return -1
}

// FindCutPoint locates the entry index in [startIndex, endIndex) that retains approximately keepRecentTokens.
func FindCutPoint(entries []SessionEntry, startIndex, endIndex, keepRecentTokens int) CutPointResult {
	cutPoints := ccFindValidCutPoints(entries, startIndex, endIndex)
	if len(cutPoints) == 0 {
		return CutPointResult{
			FirstKeptEntryIndex: startIndex,
			TurnStartIndex:      -1,
			IsSplitTurn:         false,
		}
	}

	accumulatedTokens := 0
	cutIndex := cutPoints[0]

	for i := endIndex - 1; i >= startIndex; i-- {
		entry := entries[i]
		messageTokens := 0
		for _, m := range SessionEntryToContextMessages(entry) {
			messageTokens += EstimateTokens(m)
		}
		if messageTokens == 0 {
			continue
		}
		accumulatedTokens += messageTokens
		if accumulatedTokens >= keepRecentTokens {
			found := cutPoints[len(cutPoints)-1]
			for _, candidate := range cutPoints {
				if candidate >= i {
					found = candidate
					break
				}
			}
			cutIndex = found
			break
		}
	}

	for cutIndex > startIndex {
		prevEntry := entries[cutIndex-1]
		if _, isCompaction := prevEntry.(*CompactionEntry); isCompaction || len(SessionEntryToContextMessages(prevEntry)) > 0 {
			break
		}
		cutIndex--
	}

	cutEntry := entries[cutIndex]
	startsTurn := ccIsTurnStartEntry(cutEntry)
	turnStartIndex := -1
	if !startsTurn {
		turnStartIndex = FindTurnStartIndex(entries, cutIndex, startIndex)
	}
	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !startsTurn && turnStartIndex != -1,
	}
}

const ccSummarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const ccUpdateSummarizationInstructions = `Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const ccUpdateSummarizationPrompt = "The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.\n\n" + ccUpdateSummarizationInstructions

const ccTurnPrefixSummarizationPrompt = `The messages above are earlier context from an ongoing conversation. Later messages are stored separately and do not need to be reconstructed.

Create a concise checkpoint of the user's request and the progress shown above. This checkpoint will be placed before the later messages so the conversation can continue with the necessary context.

## Original Request
[What did the user ask for?]

## Progress So Far
- [Key decisions and work completed in these messages]

## Context Needed to Continue
- [Information from these messages needed to understand the later work]

Only summarize information explicitly present above. Do not infer or recreate later messages.`

// GetSummarizationFailure returns an error message when a summarization response cannot safely be persisted.
func GetSummarizationFailure(response *ai.AssistantMessage, label string) *string {
	if response == nil {
		msg := fmt.Sprintf("%s failed: Unknown error", label)
		return &msg
	}
	if response.StopReason == ai.StopReasonError {
		errText := "Unknown error"
		if response.ErrorMessage != nil && *response.ErrorMessage != "" {
			errText = *response.ErrorMessage
		}
		msg := fmt.Sprintf("%s failed: %s", label, errText)
		return &msg
	}
	if response.StopReason == ai.StopReasonLength {
		msg := fmt.Sprintf("%s failed: generation hit the token cap and the summary is incomplete", label)
		return &msg
	}
	return nil
}

func ccMapToProviderHeaders(src map[string]string) ai.ProviderHeaders {
	if len(src) == 0 {
		return nil
	}
	headers := omap.NewMap[string, *string]()
	for k, v := range src {
		val := v
		headers.Set(k, &val)
	}
	return headers
}

func ccCreateSummarizationOptions(
	model *ai.Model,
	apiKey *string,
	headers map[string]string,
	env map[string]string,
	thinkingLevel *agent.ThinkingLevel,
	sessionId *string,
) *ai.SimpleStreamOptions {
	options := &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:  apiKey,
				Headers: ccMapToProviderHeaders(headers),
				Env:     ai.ProviderEnv(env),
			},
			SessionId: sessionId,
		},
	}
	if model != nil && model.Reasoning && thinkingLevel != nil && *thinkingLevel != "" && *thinkingLevel != agent.ThinkingLevelOff {
		r := ai.ThinkingLevel(*thinkingLevel)
		options.Reasoning = &r
	}
	return options
}

func ccCloneSimpleStreamOptions(src *ai.SimpleStreamOptions, cacheRetention ai.CacheRetention, sessionId string) *ai.SimpleStreamOptions {
	var out ai.SimpleStreamOptions
	if src != nil {
		out = *src
	}
	out.CacheRetention = &cacheRetention
	if out.SessionId == nil || *out.SessionId == "" {
		sid := sessionId
		out.SessionId = &sid
	}
	return &out
}

func ccBuildSummarizationContext(promptText string) *ai.Context {
	sys := SummarizationSystemPrompt
	return &ai.Context{
		SystemPrompt: &sys,
		Messages: ai.MessageList{
			&ai.UserMessage{
				Role: "user",
				Content: ai.UserMessageContent{
					Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: promptText}},
				},
			},
		},
	}
}

func ccHasToolCallBlock(content ai.ContentList) bool {
	for _, block := range content {
		if _, ok := block.(*ai.ToolCall); ok {
			return true
		}
	}
	return false
}

// CompleteSummarization runs one summarization LLM request wrapped in bounded transient retry.
func CompleteSummarization(
	ctx context.Context,
	model *ai.Model,
	c *ai.Context,
	options *ai.SimpleStreamOptions,
	streamFn agent.StreamFn,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
) (*ai.AssistantMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestOptions := ccCloneSimpleStreamOptions(options, ai.CacheRetentionNone, ai.Uuidv7())
	produce := func() (*ai.AssistantMessage, error) {
		if streamFn != nil {
			stream, err := streamFn(ctx, model, ai.NormalizeContext(c), requestOptions)
			if err != nil {
				return nil, err
			}
			return stream.Result(ctx)
		}
		return compat.CompleteSimple(ctx, model, c, requestOptions)
	}
	return ai.RetryAssistantCall(ctx, produce, retry, callbacks)
}

// GenerateSummary generates (or updates) a conversation summary string using the given model.
func GenerateSummary(
	ctx context.Context,
	currentMessages []agent.AgentMessage,
	model *ai.Model,
	apiKey *string,
	options ...*CompactRunOptions,
) (string, error) {
	res, err := GenerateSummaryWithUsage(ctx, currentMessages, model, apiKey, options...)
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

// GenerateSummaryWithUsage generates or updates a conversation summary and returns its token usage.
func GenerateSummaryWithUsage(
	ctx context.Context,
	currentMessages []agent.AgentMessage,
	model *ai.Model,
	apiKey *string,
	options ...*CompactRunOptions,
) (SummaryWithUsageResult, error) {
	var opts CompactRunOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	basePrompt := ccSummarizationPrompt
	if opts.PreviousSummary != nil && *opts.PreviousSummary != "" {
		basePrompt = ccUpdateSummarizationPrompt
	}
	if opts.CustomInstructions != nil && *opts.CustomInstructions != "" {
		basePrompt += "\n\nAdditional focus: " + *opts.CustomInstructions
	}

	llmMessages := ConvertToLlm(currentMessages)
	conversationText := SerializeConversation(llmMessages)

	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n", conversationText)
	if opts.PreviousSummary != nil && *opts.PreviousSummary != "" {
		promptText += fmt.Sprintf("<previous-summary>\n%s\n</previous-summary>\n\n", *opts.PreviousSummary)
	}
	promptText += basePrompt

	completionOptions := ccCreateSummarizationOptions(model, apiKey, opts.Headers, opts.Env, opts.ThinkingLevel, opts.SessionId)
	response, err := CompleteSummarization(
		ctx,
		model,
		ccBuildSummarizationContext(promptText),
		completionOptions,
		opts.StreamFn,
		opts.Retry,
		opts.Callbacks,
	)
	if err != nil {
		return SummaryWithUsageResult{}, err
	}

	if failure := GetSummarizationFailure(response, "Summarization"); failure != nil {
		return SummaryWithUsageResult{}, errors.New(*failure)
	}
	if ccHasToolCallBlock(response.Content) {
		return SummaryWithUsageResult{}, errors.New("Summarization attempted to call a tool")
	}

	return SummaryWithUsageResult{
		Text:  ai.ContentText(response.Content),
		Usage: response.Usage,
	}, nil
}

func ccIsProjectedTurnStart(entry ProjectedSessionEntry) bool {
	if entry.SourceEntry == nil {
		return false
	}
	if _, isCompaction := entry.SourceEntry.(*CompactionEntry); isCompaction {
		return false
	}
	for _, m := range entry.Messages {
		if ccIsTurnStartMessage(m) {
			return true
		}
	}
	return false
}

func ccFindProjectedTurnStartIndex(entries []ProjectedSessionEntry, entryIndex, startIndex int) int {
	if entryIndex >= len(entries) {
		entryIndex = len(entries) - 1
	}
	for i := entryIndex; i >= startIndex; i-- {
		if ccIsProjectedTurnStart(entries[i]) {
			return i
		}
	}
	return -1
}

func ccIsIntrinsicallyVisible(entry ProjectedSessionEntry) bool {
	if entry.SourceEntry == nil {
		return false
	}
	if _, isEdit := entry.SourceEntry.(*ContextEditEntry); isEdit {
		return false
	}
	return len(SessionEntryToContextMessages(entry.SourceEntry)) > 0
}

func ccIsOmitted(entry ProjectedSessionEntry) bool {
	return ccIsIntrinsicallyVisible(entry) && len(entry.Messages) == 0
}

func ccFindProjectedCutPoint(entries []ProjectedSessionEntry, startIndex, endIndex, keepRecentTokens int) CutPointResult {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		entry := entries[i]
		if entry.SourceEntry == nil {
			continue
		}
		if _, isCompaction := entry.SourceEntry.(*CompactionEntry); isCompaction {
			continue
		}
		for _, m := range entry.Messages {
			if ccIsCutPointMessage(m) {
				cutPoints = append(cutPoints, i)
				break
			}
		}
	}
	if len(cutPoints) == 0 {
		return CutPointResult{
			FirstKeptEntryIndex: startIndex,
			TurnStartIndex:      -1,
			IsSplitTurn:         false,
		}
	}

	accumulatedTokens := 0
	exceededBudget := false
	cutIndex := cutPoints[0]
	for i := endIndex - 1; i >= startIndex; i-- {
		messageTokens := 0
		for _, m := range entries[i].Messages {
			messageTokens += EstimateTokens(m)
		}
		if messageTokens == 0 {
			continue
		}
		accumulatedTokens += messageTokens
		if accumulatedTokens >= keepRecentTokens {
			exceededBudget = true
			found := cutPoints[len(cutPoints)-1]
			for _, candidate := range cutPoints {
				if candidate >= i {
					found = candidate
					break
				}
			}
			cutIndex = found
			break
		}
	}

	if cutIndex+1 <= endIndex {
		suffix := entries[cutIndex+1 : endIndex]
		omittedSuffixIds := make(map[string]struct{})
		hasOmittedAssistant := false
		allNonCompactionOrOmitted := true
		for _, entry := range suffix {
			if entry.SourceEntry == nil {
				continue
			}
			if _, isCompaction := entry.SourceEntry.(*CompactionEntry); isCompaction {
				allNonCompactionOrOmitted = false
			}
			if ccIsOmitted(entry) {
				omittedSuffixIds[entry.SourceEntry.GetId()] = struct{}{}
				if sm, ok := entry.SourceEntry.(*SessionMessageEntry); ok && sm != nil && sm.Message != nil && sm.Message.GetRole() == "assistant" {
					hasOmittedAssistant = true
				}
			} else if ccIsIntrinsicallyVisible(entry) {
				allNonCompactionOrOmitted = false
			}
		}
		hasExternalReplacement := false
		for _, entry := range suffix {
			if edit, ok := entry.SourceEntry.(*ContextEditEntry); ok && edit != nil && edit.Replacement != nil {
				if _, inOmitted := omittedSuffixIds[edit.TargetId]; !inOmitted {
					hasExternalReplacement = true
					break
				}
			}
		}
		if exceededBudget && !hasExternalReplacement && hasOmittedAssistant && allNonCompactionOrOmitted {
			cutIndex++
		}
	}

	for cutIndex > startIndex {
		prev := entries[cutIndex-1]
		if prev.SourceEntry != nil {
			if _, isCompaction := prev.SourceEntry.(*CompactionEntry); isCompaction || len(prev.Messages) > 0 {
				break
			}
		}
		cutIndex--
	}

	startsTurn := ccIsProjectedTurnStart(entries[cutIndex])
	turnStartIndex := -1
	if !startsTurn {
		turnStartIndex = ccFindProjectedTurnStartIndex(entries, cutIndex, startIndex)
	}
	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !startsTurn && turnStartIndex != -1,
	}
}

// PrepareCompaction analyzes pathEntries and returns a CompactionPreparation if compaction has messages to summarize.
func PrepareCompaction(pathEntries []SessionEntry, settings CompactionSettings) *CompactionPreparation {
	if len(pathEntries) > 0 {
		if _, isCompaction := pathEntries[len(pathEntries)-1].(*CompactionEntry); isCompaction {
			return nil
		}
	}

	projection := BuildSessionProjection(pathEntries, nil)
	projectedEntries := projection.Entries
	sourceEntries := make([]SessionEntry, len(projectedEntries))
	for i, entry := range projectedEntries {
		sourceEntries[i] = entry.SourceEntry
	}

	prevCompactionIndex := slices.IndexFunc(projectedEntries, func(entry ProjectedSessionEntry) bool {
		if entry.SourceEntry == nil {
			return false
		}
		_, isCompaction := entry.SourceEntry.(*CompactionEntry)
		return isCompaction && len(entry.Messages) > 0
	})

	var previousSummary *string
	boundaryStart := 0
	if prevCompactionIndex >= 0 {
		if comp, ok := projectedEntries[prevCompactionIndex].SourceEntry.(*CompactionEntry); ok && comp != nil {
			s := comp.Summary
			previousSummary = &s
		}
		boundaryStart = prevCompactionIndex + 1
	}
	boundaryEnd := len(projectedEntries)
	if boundaryStart >= boundaryEnd {
		return nil
	}

	tokensBefore := EstimateProjectedContextTokens(projection, pathEntries).Tokens
	cutPoint := ccFindProjectedCutPoint(projectedEntries, boundaryStart, boundaryEnd, settings.KeepRecentTokens)
	if cutPoint.FirstKeptEntryIndex < 0 || cutPoint.FirstKeptEntryIndex >= len(projectedEntries) {
		return nil
	}
	firstKeptEntry := projectedEntries[cutPoint.FirstKeptEntryIndex].SourceEntry
	if firstKeptEntry == nil || firstKeptEntry.GetId() == "" {
		return nil
	}
	firstKeptEntryId := firstKeptEntry.GetId()

	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.IsSplitTurn {
		historyEnd = cutPoint.TurnStartIndex
	}

	var messagesToSummarize []agent.AgentMessage
	if historyEnd > boundaryStart {
		for _, entry := range projectedEntries[boundaryStart:historyEnd] {
			messagesToSummarize = append(messagesToSummarize, ccGetMessagesFromProjectedEntryForCompaction(entry)...)
		}
	}

	var turnPrefixMessages []agent.AgentMessage
	if cutPoint.IsSplitTurn && cutPoint.TurnStartIndex >= 0 && cutPoint.FirstKeptEntryIndex > cutPoint.TurnStartIndex {
		for _, entry := range projectedEntries[cutPoint.TurnStartIndex:cutPoint.FirstKeptEntryIndex] {
			turnPrefixMessages = append(turnPrefixMessages, ccGetMessagesFromProjectedEntryForCompaction(entry)...)
		}
	}

	if len(messagesToSummarize) == 0 && len(turnPrefixMessages) == 0 {
		return nil
	}
	if messagesToSummarize == nil {
		messagesToSummarize = []agent.AgentMessage{}
	}
	if turnPrefixMessages == nil {
		turnPrefixMessages = []agent.AgentMessage{}
	}

	fileOps := ccExtractFileOperations(messagesToSummarize, sourceEntries, prevCompactionIndex)
	if cutPoint.IsSplitTurn {
		for _, msg := range turnPrefixMessages {
			ExtractFileOpsFromMessage(msg, &fileOps)
		}
	}

	return &CompactionPreparation{
		FirstKeptEntryId:    firstKeptEntryId,
		MessagesToSummarize: messagesToSummarize,
		TurnPrefixMessages:  turnPrefixMessages,
		IsSplitTurn:         cutPoint.IsSplitTurn,
		TokensBefore:        tokensBefore,
		PreviousSummary:     previousSummary,
		FileOps:             fileOps,
		Settings:            settings,
	}
}

func ccGenerateTurnPrefixSummary(
	ctx context.Context,
	messages []agent.AgentMessage,
	model *ai.Model,
	apiKey *string,
	opts CompactRunOptions,
) (SummaryWithUsageResult, error) {
	llmMessages := ConvertToLlm(messages)
	conversationText := SerializeConversation(llmMessages)
	promptText := fmt.Sprintf("# Conversation\n%s\n\n# Instructions\n%s", conversationText, ccTurnPrefixSummarizationPrompt)
	if opts.CustomInstructions != nil && *opts.CustomInstructions != "" {
		promptText += "\n\nAdditional focus: " + *opts.CustomInstructions
	}

	completionOptions := ccCreateSummarizationOptions(model, apiKey, opts.Headers, opts.Env, opts.ThinkingLevel, opts.SessionId)
	response, err := CompleteSummarization(
		ctx,
		model,
		ccBuildSummarizationContext(promptText),
		completionOptions,
		opts.StreamFn,
		opts.Retry,
		opts.Callbacks,
	)
	if err != nil {
		return SummaryWithUsageResult{}, err
	}

	if failure := GetSummarizationFailure(response, "Turn prefix summarization"); failure != nil {
		return SummaryWithUsageResult{}, errors.New(*failure)
	}
	if ccHasToolCallBlock(response.Content) {
		return SummaryWithUsageResult{}, errors.New("Turn prefix summarization attempted to call a tool")
	}

	return SummaryWithUsageResult{
		Text:  ai.ContentText(response.Content),
		Usage: response.Usage,
	}, nil
}

// Compact generates summaries for a prepared compaction span and returns the resulting CompactionResult.
func Compact(
	ctx context.Context,
	preparation CompactionPreparation,
	model *ai.Model,
	apiKey *string,
	options ...*CompactRunOptions,
) (*CompactionResult, error) {
	var opts CompactRunOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	var summary string
	var summaryUsage ai.Usage

	if preparation.IsSplitTurn && len(preparation.TurnPrefixMessages) > 0 {
		historyText := "No prior history."
		if preparation.PreviousSummary != nil {
			historyText = *preparation.PreviousSummary
		}
		var historyUsage *ai.Usage
		if len(preparation.MessagesToSummarize) > 0 {
			historyOpts := opts
			historyOpts.PreviousSummary = preparation.PreviousSummary
			historyResult, err := GenerateSummaryWithUsage(ctx, preparation.MessagesToSummarize, model, apiKey, &historyOpts)
			if err != nil {
				return nil, err
			}
			historyText = historyResult.Text
			u := historyResult.Usage
			historyUsage = &u
		}

		turnPrefixResult, err := ccGenerateTurnPrefixSummary(ctx, preparation.TurnPrefixMessages, model, apiKey, opts)
		if err != nil {
			return nil, err
		}
		summary = historyText + "\n\n---\n\n**Turn Context (split turn):**\n\n" + turnPrefixResult.Text
		if historyUsage != nil {
			summaryUsage = CombineUsage(*historyUsage, turnPrefixResult.Usage)
		} else {
			summaryUsage = turnPrefixResult.Usage
		}
	} else {
		sumOpts := opts
		sumOpts.PreviousSummary = preparation.PreviousSummary
		res, err := GenerateSummaryWithUsage(ctx, preparation.MessagesToSummarize, model, apiKey, &sumOpts)
		if err != nil {
			return nil, err
		}
		summary = res.Text
		summaryUsage = res.Usage
	}

	readFiles, modifiedFiles := ComputeFileLists(preparation.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)

	if preparation.FirstKeptEntryId == "" {
		return nil, errors.New("First kept entry has no UUID - session may need migration")
	}

	if readFiles == nil {
		readFiles = []string{}
	}
	if modifiedFiles == nil {
		modifiedFiles = []string{}
	}

	usageCopy := summaryUsage
	return &CompactionResult{
		Summary:          summary,
		FirstKeptEntryId: preparation.FirstKeptEntryId,
		TokensBefore:     preparation.TokensBefore,
		Usage:            &usageCopy,
		Details: &CompactionDetails{
			ReadFiles:     readFiles,
			ModifiedFiles: modifiedFiles,
		},
	}, nil
}
