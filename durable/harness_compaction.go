// Ported from packages/durable/src/harness/compaction.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"math"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// CompactionInput configures a built-in compaction task.
type CompactionInput struct {
	Reason       CompactionReason `json:"reason"`
	Instructions *string          `json:"instructions,omitzero"`
}

// SummaryRequest holds the pinned summarization request fields in a compaction checkpoint.
type SummaryRequest struct {
	Attempt       int                       `json:"attempt"`
	Model         ModelRef                  `json:"model"`
	ThinkingLevel ai.ModelThinkingLevel     `json:"thinkingLevel"`
	StreamOptions ConversationStreamOptions `json:"streamOptions"`
	MaxTokens     int                       `json:"maxTokens"`
	Tail          EntryId                   `json:"tail"`
	FirstKept     EntryId                   `json:"firstKept"`
}

// CompactionCheckpoint records the durable phase and request parameters of CompactionTask.
type CompactionCheckpoint struct {
	Phase         string                     `json:"phase"`
	Until         *int64                     `json:"until,omitzero"`
	Attempt       int                        `json:"attempt,omitzero"`
	Model         *ModelRef                  `json:"model,omitzero"`
	ThinkingLevel *ai.ModelThinkingLevel     `json:"thinkingLevel,omitzero"`
	StreamOptions *ConversationStreamOptions `json:"streamOptions,omitzero"`
	MaxTokens     int                        `json:"maxTokens,omitzero"`
	Tail          *EntryId                   `json:"tail,omitzero"`
	FirstKept     *EntryId                   `json:"firstKept,omitzero"`
}

type hcRuntime = *TaskRuntime[CompactionInput, CompactionCheckpoint, CompactionResult, CompactionHooks]
type hcNext = NextTaskState[CompactionCheckpoint, CompactionResult]

const (
	hcToolResultMaxChars = 2000
	hcSummaryPrefix      = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	hcSummarySuffix      = "\n</summary>"

	hcSummarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`

	hcSummarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work. If the conversation starts with an earlier summary, preserve its information and fold the newer messages into it.

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
)

// CompactionTask is the built-in context compaction task ("pi.compaction").
var CompactionTask = DefineTask(TaskDefinition[CompactionInput, CompactionCheckpoint, CompactionResult, CompactionHooks]{
	Name:    "pi.compaction",
	Version: 1,
	Initial: func(_ CompactionInput) CompactionCheckpoint {
		return CompactionCheckpoint{Phase: "select"}
	},
	Phases: map[string]PhaseHandler[CompactionInput, CompactionCheckpoint, CompactionResult, CompactionHooks]{
		"select": func(ctx chord.Context, task RunningTask[CompactionInput, CompactionCheckpoint], runtime hcRuntime) error {
			conversationId := runtime.ConversationId()
			agent, err := runtime.Agent(ctx)
			if err != nil {
				return err
			}
			settings := runtime.Settings()
			ref := agent.Model
			var model *ai.Model
			if ref != nil {
				model = runtime.Models().GetModel(ref.Provider, ref.ModelId)
			}
			if ref == nil || model == nil {
				return hcFailNoModel(ctx, runtime, ref)
			}
			policy := settings.Compaction
			view, err := runtime.Context(ctx, conversationId)
			if err != nil {
				return err
			}
			cut, ok := SelectCut(view, policy.KeepRecentTokens)
			if !ok {
				return hcComplete(ctx, runtime)
			}
			firstKept := view.Entries[cut].Id
			hookIn := CompactionHookInput{
				Reason:       task.Input.Reason,
				Entries:      append([]EntryRecord(nil), view.Entries[:cut]...),
				Messages:     SummarizedMessages(view, cut),
				FirstKept:    firstKept,
				Instructions: task.Input.Instructions,
			}
			var decision *BeforeCompactDecision
			if err := runtime.Hooks().Each(func(h CompactionHooks) error {
				if h.BeforeCompact == nil || decision != nil {
					return nil
				}
				d, err := h.BeforeCompact(hookIn, runtime.Base(), ctx)
				if err != nil {
					return err
				}
				if d != nil {
					decision = d
				}
				return nil
			}); err != nil {
				return err
			}
			if decision != nil && decision.Decline {
				return hcComplete(ctx, runtime)
			}
			if decision != nil && decision.Summary != nil {
				return hcPlace(ctx, runtime, firstKept, *decision.Summary)
			}
			maxTokens := int(math.Floor(0.8 * float64(policy.ReserveTokens)))
			if model.MaxTokens > 0 && model.MaxTokens < maxTokens {
				maxTokens = model.MaxTokens
			}
			tail := firstKept
			for _, entry := range view.Entries {
				if entry.Id > tail {
					tail = entry.Id
				}
			}
			refCopy := *ref
			tlCopy := agent.ThinkingLevel
			streamCopy := haCopyStreamOptions(settings.Stream)
			tailCopy := tail
			firstKeptCopy := firstKept
			return runtime.Commit(ctx, func(_ Tx, _ RunningTask[CompactionInput, CompactionCheckpoint]) (*hcNext, error) {
				return &hcNext{
					Status: TaskStateRunning,
					Checkpoint: CompactionCheckpoint{
						Phase:         "summarize",
						Attempt:       1,
						Model:         &refCopy,
						ThinkingLevel: &tlCopy,
						StreamOptions: &streamCopy,
						MaxTokens:     maxTokens,
						Tail:          &tailCopy,
						FirstKept:     &firstKeptCopy,
					},
				}, nil
			})
		},

		"summarize": func(ctx chord.Context, task RunningTask[CompactionInput, CompactionCheckpoint], runtime hcRuntime) error {
			cp := task.Checkpoint
			if cp.Model == nil || cp.Tail == nil || cp.FirstKept == nil {
				return fmt.Errorf("compaction summarize checkpoint missing required fields")
			}
			model := runtime.Models().GetModel(cp.Model.Provider, cp.Model.ModelId)
			if model == nil {
				return hcFailNoModel(ctx, runtime, cp.Model)
			}
			view, err := runtime.Context(ctx, runtime.ConversationId(), *cp.Tail)
			if err != nil {
				return err
			}
			cut := -1
			for i, e := range view.Entries {
				if e.Id == *cp.FirstKept {
					cut = i
					break
				}
			}
			if cut < 0 {
				cut = 0
			}
			now := runtime.Now()
			sysText := hcSummarizationSystemPrompt
			promptText := hcSummaryPrompt(SummarizedMessages(view, cut), task.Input.Instructions)
			messages := ai.MessageList{
				&ai.SystemMessage{
					Role:      "system",
					Content:   ai.SystemMessageContent{Text: &sysText},
					Timestamp: now,
				},
				&ai.UserMessage{
					Role: "user",
					Content: ai.UserMessageContent{
						Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: promptText}},
					},
					Timestamp: now,
				},
			}
			var streamOpts ConversationStreamOptions
			if cp.StreamOptions != nil {
				streamOpts = *cp.StreamOptions
			}
			streamOpts.Deferred = nil
			cacheNone := ai.CacheRetentionNone
			streamOpts.CacheRetention = &cacheNone
			thinkingLevel := ai.ModelThinkingLevel("off")
			if cp.ThinkingLevel != nil {
				thinkingLevel = *cp.ThinkingLevel
			}
			opts := hgBuildSimpleStreamOptions(streamOpts, thinkingLevel)
			maxTok := cp.MaxTokens
			opts.MaxTokens = &maxTok

			reqCtx := chord.WithAbortSignal(runtime.Signal(), ctx)
			message, err := runtime.Models().CompleteSimple(hutAbortSignal(reqCtx), model, &ai.Context{Messages: messages}, opts)
			if err != nil {
				return err
			}
			if err := runtime.Signal().Err(); err != nil {
				return hutSignalErr(runtime.Signal())
			}

			summary, hasSummary := hcSummaryText(message)
			policy := runtime.Settings().Retry
			retry := message.StopReason == ai.StopReasonError &&
				ai.IsRetryableAssistantError(message) &&
				policy.Enabled &&
				cp.Attempt <= policy.MaxRetries
			var until int64
			if retry {
				aiPolicy := ai.RetryPolicy{
					Enabled:         policy.Enabled,
					MaxRetries:      policy.MaxRetries,
					BaseDelayMs:     policy.BaseDelayMs,
					MaxAgentDelayMs: policy.MaxAgentDelayMs,
				}
				until = runtime.Now() + ai.RetryDelayMs(&aiPolicy, cp.Attempt)
			}

			return runtime.Commit(ctx, func(tx Tx, current RunningTask[CompactionInput, CompactionCheckpoint]) (*hcNext, error) {
				if err := huRecordUsage(tx, runtime.ConversationId(), "models", fmt.Sprintf("%s/%s", message.Provider, message.Model), message.Usage); err != nil {
					return nil, err
				}
				live, err := tx.Doc(LiveDoc, runtime.ConversationId())
				if err != nil {
					return nil, err
				}
				if hasSummary {
					return hcPlaceSummary(tx, runtime, current, live.Value, *cp.FirstKept, summary)
				}
				if retry {
					if status, ok := GetCompactionStatus(live.Value, runtime.TaskId()); ok {
						errMsg := ""
						if message.ErrorMessage != nil {
							errMsg = *message.ErrorMessage
						}
						status.Set("retry", jsonx.ObjectOf("at", float64(until), "error", errMsg))
					}
					nextCp := cp
					nextCp.Phase = "retry"
					u := until
					nextCp.Until = &u
					return &hcNext{
						Status:     TaskStateRunning,
						Checkpoint: nextCp,
					}, nil
				}
				RemoveCompactionStatus(live.Value, runtime.TaskId())
				text := hcSummaryFailure(message)
				return &hcNext{
					Status: TaskStateTerminal,
					Outcome: &TypedTaskOutcome[CompactionResult]{
						Status: TaskOutcomeFailed,
						Error: &TaskOutcomeError{
							Message: text,
							Detail:  jsonx.ObjectOf("reason", "model_error"),
						},
					},
				}, nil
			})
		},

		"retry": func(ctx chord.Context, task RunningTask[CompactionInput, CompactionCheckpoint], runtime hcRuntime) error {
			cp := task.Checkpoint
			var until int64
			if cp.Until != nil {
				until = *cp.Until
			}
			if err := runtime.Sleep(ctx, until); err != nil {
				return err
			}
			attempt := cp.Attempt + 1
			return runtime.Commit(ctx, func(tx Tx, _ RunningTask[CompactionInput, CompactionCheckpoint]) (*hcNext, error) {
				live, err := tx.Doc(LiveDoc, runtime.ConversationId())
				if err != nil {
					return nil, err
				}
				if status, ok := GetCompactionStatus(live.Value, runtime.TaskId()); ok {
					status.Set("attempt", float64(attempt))
					status.Delete("retry")
				}
				nextCp := cp
				nextCp.Phase = "summarize"
				nextCp.Until = nil
				nextCp.Attempt = attempt
				return &hcNext{
					Status:     TaskStateRunning,
					Checkpoint: nextCp,
				}, nil
			})
		},
	},
	Abort: func(ctx chord.Context, _ RunningTask[CompactionInput, CompactionCheckpoint], runtime hcRuntime) error {
		return runtime.Commit(ctx, func(tx Tx, _ RunningTask[CompactionInput, CompactionCheckpoint]) (*hcNext, error) {
			live, err := tx.Doc(LiveDoc, runtime.ConversationId())
			if err != nil {
				return nil, err
			}
			RemoveCompactionStatus(live.Value, runtime.TaskId())
			return &hcNext{
				Status: TaskStateTerminal,
				Outcome: &TypedTaskOutcome[CompactionResult]{
					Status: TaskOutcomeAborted,
				},
			}, nil
		})
	},
})

// CreateCompaction creates a compaction task and records its initial status in pi.live.
func CreateCompaction(
	tx Tx,
	conversationId ConversationId,
	input CompactionInput,
	owner *TaskId,
) (TaskId, error) {
	ownership := TaskOwnership{Kind: TaskOwnershipConversation}
	if owner != nil {
		ownership = TaskOwnership{Kind: TaskOwnershipTask, TaskId: *owner}
	}
	background := owner == nil && input.Reason != CompactionReasonManual
	cid := conversationId
	bg := background
	taskId, err := tx.CreateTask(CompactionTask, input, TaskOptions{
		Ownership:      ownership,
		ConversationId: &cid,
		Background:     &bg,
	})
	if err != nil {
		return 0, err
	}
	live, err := tx.Doc(LiveDoc, conversationId)
	if err != nil {
		return 0, err
	}
	if err := AddCompactionStatus(live.Value, CompactionStatus{
		TaskId:   taskId,
		Reason:   input.Reason,
		Blocking: owner != nil,
		Attempt:  1,
	}); err != nil {
		return 0, err
	}
	return taskId, nil
}

// SelectCut chooses the index in view.Entries of the first kept entry, or (0, false) if nothing can be compacted.
func SelectCut(view *ContextView, keepRecentTokens int) (int, bool) {
	if view == nil {
		return 0, false
	}
	contributions := view.Contributions
	start := 0
	if view.Head != nil {
		start = 1
	}
	var candidates []int
	for i := start; i < len(contributions); i++ {
		if hcIsCandidate(contributions, i) {
			candidates = append(candidates, i)
		}
	}
	kept := 0
	cut := -1
	for i := len(contributions) - 1; i >= start; i-- {
		for _, m := range contributions[i] {
			kept += ai.EstimateMessageTokens(m)
		}
		if kept < keepRecentTokens {
			continue
		}
		for _, c := range candidates {
			if c >= i {
				cut = c
				break
			}
		}
		if cut < 0 && len(candidates) > 0 {
			cut = candidates[len(candidates)-1]
		}
		break
	}
	if cut < 0 {
		return 0, false
	}
	for i := start; i < cut; i++ {
		if len(contributions[i]) > 0 {
			return cut, true
		}
	}
	return 0, false
}

func hcIsCandidate(contributions [][]ai.Message, index int) bool {
	if len(contributions[index]) == 0 {
		return false
	}
	first := contributions[index][0]
	if first.GetRole() == "assistant" {
		return true
	}
	if first.GetRole() != "user" {
		return false
	}
	calls := make(map[string]struct{})
	for before := index - 1; before >= 0; before-- {
		var assistant *ai.AssistantMessage
		for i := len(contributions[before]) - 1; i >= 0; i-- {
			if a, ok := contributions[before][i].(*ai.AssistantMessage); ok {
				assistant = a
				break
			}
		}
		if assistant == nil {
			continue
		}
		for _, blk := range assistant.Content {
			if tc, ok := blk.(*ai.ToolCall); ok {
				calls[tc.Id] = struct{}{}
			}
		}
		break
	}
	if len(calls) == 0 {
		return true
	}
	for after := index; after < len(contributions); after++ {
		for pos, msg := range contributions[after] {
			if msg.GetRole() == "assistant" && (after > index || pos > 0) {
				return true
			}
			if tr, ok := msg.(*ai.ToolResultMessage); ok {
				if _, match := calls[tr.ToolCallId]; match {
					return false
				}
			}
		}
	}
	return true
}

// SummarizedMessages returns model messages of entries before cut ordered like model context.
func SummarizedMessages(view *ContextView, cut int) []ai.Message {
	var flat []ai.Message
	for i := 0; i < cut && i < len(view.Contributions); i++ {
		flat = append(flat, view.Contributions[i]...)
	}
	return OrderToolResults(flat)
}

// EstimateContext estimates token count for a request over view followed by extra messages.
func EstimateContext(view *ContextView, extra []ai.Message) int {
	var measured *ai.AssistantMessage
	after := EntryId(-1)
	if view.Head != nil {
		after = view.Head.Id
	}
	for i := len(view.Entries) - 1; i >= 0 && measured == nil; i-- {
		if view.Head != nil && view.Entries[i].Id <= after {
			continue
		}
		for j := len(view.Contributions[i]) - 1; j >= 0; j-- {
			if a, ok := view.Contributions[i][j].(*ai.AssistantMessage); ok && ai.CalculateContextTokens(&a.Usage) > 0 {
				measured = a
				break
			}
		}
	}
	from := 0
	if measured != nil {
		for i := len(view.Messages) - 1; i >= 0; i-- {
			if a, ok := view.Messages[i].(*ai.AssistantMessage); ok && a == measured {
				from = i + 1
				break
			}
		}
	}
	tokens := 0
	if measured != nil {
		tokens = ai.CalculateContextTokens(&measured.Usage)
	}
	for _, m := range view.Messages[from:] {
		tokens += ai.EstimateMessageTokens(m)
	}
	for _, m := range extra {
		tokens += ai.EstimateMessageTokens(m)
	}
	return tokens
}

func hcSummaryText(message *ai.AssistantMessage) (string, bool) {
	if message.StopReason != ai.StopReasonStop {
		return "", false
	}
	var texts []string
	for _, blk := range message.Content {
		if _, ok := blk.(*ai.ToolCall); ok {
			return "", false
		}
		if t, ok := blk.(*ai.TextContent); ok {
			texts = append(texts, t.Text)
		}
	}
	joined := strings.TrimSpace(strings.Join(texts, "\n"))
	if joined == "" {
		return "", false
	}
	return joined, true
}

func hcSummaryFailure(message *ai.AssistantMessage) string {
	if message.StopReason == ai.StopReasonError || message.StopReason == ai.StopReasonAborted {
		reason := string(message.StopReason)
		if message.ErrorMessage != nil {
			reason = *message.ErrorMessage
		}
		return fmt.Sprintf("Summarization failed: %s", reason)
	}
	if message.StopReason == ai.StopReasonLength {
		return "Summarization hit the token limit; the summary is incomplete"
	}
	for _, blk := range message.Content {
		if _, ok := blk.(*ai.ToolCall); ok {
			return "Summarization attempted to call a tool"
		}
	}
	return "Summarization produced no text"
}

func hcSummaryPrompt(messages []ai.Message, instructions *string) string {
	focus := ""
	if instructions != nil {
		focus = "\n\nAdditional focus: " + *instructions
	}
	return fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n%s%s", SerializeConversation(messages), hcSummarizationPrompt, focus)
}

// SerializeConversation converts model messages into plain text for the summarizer. System messages are omitted.
func SerializeConversation(messages []ai.Message) string {
	var parts []string
	for _, msg := range messages {
		switch m := msg.(type) {
		case *ai.UserMessage:
			text := hcUserText(m.Content)
			if text != "" {
				parts = append(parts, "[User]: "+text)
			}
		case *ai.AssistantMessage:
			var thinking []string
			var text []string
			var calls []string
			for _, blk := range m.Content {
				switch b := blk.(type) {
				case *ai.ThinkingContent:
					thinking = append(thinking, b.Thinking)
				case *ai.TextContent:
					text = append(text, b.Text)
				case *ai.ToolCall:
					var argParts []string
					if b.Arguments != nil {
						for k, v := range b.Arguments.All() {
							encoded, err := jsonx.Stringify(v)
							if err != nil {
								encoded = "null"
							}
							argParts = append(argParts, fmt.Sprintf("%s=%s", k, encoded))
						}
					}
					calls = append(calls, fmt.Sprintf("%s(%s)", b.Name, strings.Join(argParts, ", ")))
				}
			}
			if len(thinking) > 0 {
				parts = append(parts, "[Assistant thinking]: "+strings.Join(thinking, "\n"))
			}
			if len(text) > 0 {
				parts = append(parts, "[Assistant]: "+strings.Join(text, "\n"))
			}
			if len(calls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(calls, "; "))
			}
		case *ai.ToolResultMessage:
			text := hcContentBlocksText(m.Content)
			if text != "" {
				parts = append(parts, "[Tool result]: "+hcTruncate(text, hcToolResultMaxChars))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func hcUserText(c ai.UserMessageContent) string {
	if c.Text != nil {
		return *c.Text
	}
	return hcContentBlocksText(c.Blocks)
}

func hcContentBlocksText(blocks ai.ContentList) string {
	var texts []string
	for _, blk := range blocks {
		if tc, ok := blk.(*ai.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func hcTruncate(text string, maxChars int) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return fmt.Sprintf("%s\n\n[... %d more characters truncated]", string(runes[:maxChars]), len(runes)-maxChars)
}

func hcPlace(ctx chord.Context, runtime hcRuntime, firstKept EntryId, summary string) error {
	return runtime.Commit(ctx, func(tx Tx, current RunningTask[CompactionInput, CompactionCheckpoint]) (*hcNext, error) {
		live, err := tx.Doc(LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		return hcPlaceSummary(tx, runtime, current, live.Value, firstKept, summary)
	})
}

func hcPlaceSummary(
	tx Tx,
	runtime hcRuntime,
	current RunningTask[CompactionInput, CompactionCheckpoint],
	live *jsonx.Object,
	firstKept EntryId,
	summary string,
) (*hcNext, error) {
	RemoveCompactionStatus(live, runtime.TaskId())
	text := hcSummaryPrefix + summary + hcSummarySuffix
	entry := EntryDraft{
		Kind: CompactionEntry.Kind,
		Head: EntryHeadAt(firstKept),
		Model: ai.MessageList{
			&ai.UserMessage{
				Role: "user",
				Content: ai.UserMessageContent{
					Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
				},
				Timestamp: runtime.Now(),
			},
		},
		Data: jsonx.ObjectOf("reason", string(current.Input.Reason)),
	}
	var result CompactionResult
	if current.Owner == nil {
		settings := runtime.Settings()
		sid, err := hsubAdmitSubmission(
			tx,
			runtime.ConversationId(),
			SubmissionDraft{
				Type:      SubmissionTypeWrite,
				RequestId: fmt.Sprintf("compaction:%d", runtime.TaskId()),
				Entry:     &entry,
			},
			runtime.Now(),
			QueueModes{
				SteeringMode: settings.SteeringMode,
				FollowUpMode: settings.FollowUpMode,
			},
		)
		if err != nil {
			return nil, err
		}
		result.SubmissionId = &sid
	} else {
		rec, err := tx.AppendEntry(runtime.ConversationId(), entry)
		if err != nil {
			return nil, err
		}
		eid := rec.Id
		result.EntryId = &eid
	}
	return &hcNext{
		Status: TaskStateTerminal,
		Outcome: &TypedTaskOutcome[CompactionResult]{
			Status: TaskOutcomeCompleted,
			Result: result,
			HasRes: true,
		},
	}, nil
}

func hcComplete(ctx chord.Context, runtime hcRuntime) error {
	return runtime.Commit(ctx, func(tx Tx, _ RunningTask[CompactionInput, CompactionCheckpoint]) (*hcNext, error) {
		live, err := tx.Doc(LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		RemoveCompactionStatus(live.Value, runtime.TaskId())
		return &hcNext{
			Status: TaskStateTerminal,
			Outcome: &TypedTaskOutcome[CompactionResult]{
				Status: TaskOutcomeCompleted,
				Result: CompactionResult{},
				HasRes: true,
			},
		}, nil
	})
}

func hcFailNoModel(ctx chord.Context, runtime hcRuntime, ref *ModelRef) error {
	msg := "No model is configured"
	if ref != nil {
		msg = fmt.Sprintf("Model %s/%s is not available", ref.Provider, ref.ModelId)
	}
	return runtime.Commit(ctx, func(tx Tx, _ RunningTask[CompactionInput, CompactionCheckpoint]) (*hcNext, error) {
		live, err := tx.Doc(LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		RemoveCompactionStatus(live.Value, runtime.TaskId())
		return &hcNext{
			Status: TaskStateTerminal,
			Outcome: &TypedTaskOutcome[CompactionResult]{
				Status: TaskOutcomeFailed,
				Error: &TaskOutcomeError{
					Message: msg,
					Detail:  jsonx.ObjectOf("reason", "no_model"),
				},
			},
		}, nil
	})
}
