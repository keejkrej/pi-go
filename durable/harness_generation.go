// Ported from packages/durable/src/harness/generation.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// GenerationInput is the empty input payload for GenerationTask.
type GenerationInput struct{}

// GenerationCheckpoint records durable generation phase state.
type GenerationCheckpoint struct {
	Phase         string                     `json:"phase"`
	Attempt       int                        `json:"attempt,omitzero"`
	Compacted     *TaskId                    `json:"compacted,omitzero"`
	Overflow      *string                    `json:"overflow,omitzero"`
	Model         *ModelRef                  `json:"model,omitzero"`
	ThinkingLevel *ai.ModelThinkingLevel     `json:"thinkingLevel,omitzero"`
	StreamOptions *ConversationStreamOptions `json:"streamOptions,omitzero"`
	Cutoff        *EntryId                   `json:"cutoff,omitzero"`
	Until         *int64                     `json:"until,omitzero"`
	Handle        *ai.DeferredHandle         `json:"handle,omitzero"`
	PollAt        *int64                     `json:"pollAt,omitzero"`
	Assistant     *EntryId                   `json:"assistant,omitzero"`
	Tools         []TaskId                   `json:"tools,omitzero"`
	Pending       []string                   `json:"pending,omitzero"`
}

// GenerationResult holds the appended assistant entry ID of a completed generation turn.
type GenerationResult struct {
	EntryId EntryId `json:"entryId"`
}

type hgRuntime = *TaskRuntime[GenerationInput, GenerationCheckpoint, GenerationResult, GenerationHooks]
type hgNext = NextTaskState[GenerationCheckpoint, GenerationResult]

type hgRequest struct {
	attempt   int
	compacted *TaskId
	model     ModelRef
	cutoff    EntryId
	messages  []ai.Message
	hasMsgs   bool
	pollAt    *int64
}

const (
	hgPartialThrottleMs  = 100 * time.Millisecond
	hgDefaultPollAfterMs = int64(5000)
)

// GenerationTask is the built-in durable model generation task ("pi.generation").
var GenerationTask = DefineTask(TaskDefinition[GenerationInput, GenerationCheckpoint, GenerationResult, GenerationHooks]{
	Name:    "pi.generation",
	Version: 1,
	Initial: func(_ GenerationInput) GenerationCheckpoint {
		return GenerationCheckpoint{Phase: "prepare", Attempt: 1}
	},
})

func init() {
	GenerationTask.Definition.Phases = map[string]PhaseHandler[GenerationInput, GenerationCheckpoint, GenerationResult, GenerationHooks]{
		"prepare": func(ctx chord.Context, task RunningTask[GenerationInput, GenerationCheckpoint], runtime hgRuntime) error {
			conversationId := runtime.ConversationId()
			agent, err := runtime.Agent(ctx)
			if err != nil {
				return err
			}
			settings := runtime.Settings()
			modelRef := agent.Model
			thinkingLevel := agent.ThinkingLevel
			var resolved *ai.Model
			if modelRef != nil {
				resolved = runtime.Models().GetModel(modelRef.Provider, modelRef.ModelId)
			}
			if modelRef == nil || resolved == nil {
				return hgFailNoModel(ctx, runtime, modelRef)
			}
			cp := task.Checkpoint
			if cp.Compacted != nil && cp.Overflow != nil {
				outcomes, err := runtime.Outcomes(ctx, []TaskId{*cp.Compacted})
				if err != nil {
					return err
				}
				var compResult CompactionResult
				completed := len(outcomes) > 0 && outcomes[0].Status == TaskOutcomeCompleted
				if completed {
					if resVal, ok := outcomes[0].Result.Get(); ok {
						_ = jsonx.Decode(resVal, &compResult)
					}
				}
				if !completed || compResult.EntryId == nil {
					return hgFailModelError(ctx, runtime, *cp.Overflow)
				}
			}

			view, err := runtime.Context(ctx, conversationId)
			if err != nil {
				return err
			}
			shown := ReplaySections(view.Messages)
			report := func(err error) {
				runtime.Report(err)
			}
			var execEnv env.ExecutionEnv
			envVal, envErr := runtime.Env(ctx)
			if envErr != nil {
				if hutIsContextAborted(ctx) {
					return envErr
				}
				report(envErr)
			} else {
				execEnv = envVal
			}
			shownMap := make(map[string]string, shown.Len())
			for k, v := range shown.All() {
				shownMap[k] = v
			}
			input := PromptInput{
				ConversationId: conversationId,
				Agent:          agent,
				Env:            execEnv,
				Shown:          shownMap,
				Read:           runtime.Base(),
			}
			desired, err := RenderSections(agent.Sections, input, shown, report, ctx)
			if err != nil {
				return err
			}
			entries := PlanSystemEntries(view, desired, agent.Tools, runtime.Now())

			var threshold string
			if cp.Compacted == nil {
				threshold = hgThresholdCompaction(view, entries, resolved.ContextWindow, settings.Compaction)
			}
			if threshold == "blocking" {
				tid := runtime.TaskId()
				return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
					child, err := CreateCompaction(tx, conversationId, CompactionInput{Reason: CompactionReasonThreshold}, &tid)
					if err != nil {
						return nil, err
					}
					return &hgNext{
						Status: TaskStateWaiting,
						Checkpoint: GenerationCheckpoint{
							Phase:     "prepare",
							Attempt:   cp.Attempt,
							Compacted: &child,
						},
						On:     []TaskId{child},
						Policy: JoinPolicyAllSettled,
					}, nil
				})
			}

			return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
				page, err := tx.ScanEntries(EntryQuery{ConversationId: conversationId}, 1, nil)
				if err != nil {
					return nil, err
				}
				var cutoff EntryId
				hasCutoff := false
				if len(page.Items) > 0 {
					cutoff = page.Items[0].Id
					hasCutoff = true
				}
				for _, draft := range entries {
					rec, err := tx.AppendTypedEntry(SystemEntry, conversationId, draft)
					if err != nil {
						return nil, err
					}
					cutoff = rec.Id
					hasCutoff = true
				}
				if !hasCutoff {
					return nil, fmt.Errorf("Conversation %d has no entries to send", conversationId)
				}
				if threshold == "background" {
					live, err := tx.Doc(LiveDoc, conversationId)
					if err != nil {
						return nil, err
					}
					if _, hasComp := live.Value.Get("compactions"); !hasComp {
						if _, err := CreateCompaction(tx, conversationId, CompactionInput{Reason: CompactionReasonThreshold}, nil); err != nil {
							return nil, err
						}
					}
				}
				streamCopy := haCopyStreamOptions(settings.Stream)
				modelCopy := *modelRef
				tlCopy := thinkingLevel
				cutoffCopy := cutoff
				return &hgNext{
					Status: TaskStateRunning,
					Checkpoint: GenerationCheckpoint{
						Phase:         "request",
						Attempt:       cp.Attempt,
						Compacted:     cp.Compacted,
						Model:         &modelCopy,
						ThinkingLevel: &tlCopy,
						StreamOptions: &streamCopy,
						Cutoff:        &cutoffCopy,
					},
				}, nil
			})
		},

		"request": func(ctx chord.Context, task RunningTask[GenerationInput, GenerationCheckpoint], runtime hgRuntime) error {
			cp := task.Checkpoint
			if cp.Model == nil || cp.Cutoff == nil || cp.StreamOptions == nil {
				return fmt.Errorf("generation request checkpoint missing required fields")
			}
			thinkingLevel := ai.ModelThinkingLevel("off")
			if cp.ThinkingLevel != nil {
				thinkingLevel = *cp.ThinkingLevel
			}
			conversationId := runtime.ConversationId()
			if err := runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
				live, err := tx.Doc(LiveDoc, conversationId)
				if err != nil {
					return nil, err
				}
				if err := hgConvertPartial(tx, live.Value, conversationId); err != nil {
					return nil, err
				}
				live.Value.Set("generation", jsonx.ObjectOf("attempt", float64(cp.Attempt)))
				return nil, nil
			}); err != nil {
				return err
			}

			model := runtime.Models().GetModel(cp.Model.Provider, cp.Model.ModelId)
			if model == nil {
				return hgFailNoModel(ctx, runtime, cp.Model)
			}
			view, err := runtime.Context(ctx, conversationId, *cp.Cutoff)
			if err != nil {
				return err
			}
			messages := view.Messages
			if err := runtime.Hooks().Each(func(h GenerationHooks) error {
				if h.BeforeRequest == nil {
					return nil
				}
				replaced, err := h.BeforeRequest(GenerationRequest{Messages: messages}, runtime.Base(), ctx)
				if err != nil {
					return err
				}
				if replaced != nil {
					messages = replaced.Messages
				}
				return nil
			}); err != nil {
				return err
			}

			opts := hgBuildSimpleStreamOptions(*cp.StreamOptions, thinkingLevel)
			message, err := hgStreamResponse(ctx, runtime, model, messages, opts, cp.Attempt)
			if err != nil {
				return err
			}
			req := hgRequest{
				attempt:   cp.Attempt,
				compacted: cp.Compacted,
				model:     *cp.Model,
				cutoff:    *cp.Cutoff,
				messages:  view.Messages,
				hasMsgs:   true,
			}
			return hgClassify(ctx, runtime, req, message)
		},

		"retry": func(ctx chord.Context, task RunningTask[GenerationInput, GenerationCheckpoint], runtime hgRuntime) error {
			cp := task.Checkpoint
			var until int64
			if cp.Until != nil {
				until = *cp.Until
			}
			if err := runtime.Sleep(ctx, until); err != nil {
				return err
			}
			return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
				live, err := tx.Doc(LiveDoc, runtime.ConversationId())
				if err != nil {
					return nil, err
				}
				nextAttempt := cp.Attempt + 1
				live.Value.Set("generation", jsonx.ObjectOf("attempt", float64(nextAttempt)))
				return &hgNext{
					Status: TaskStateRunning,
					Checkpoint: GenerationCheckpoint{
						Phase:     "prepare",
						Attempt:   nextAttempt,
						Compacted: cp.Compacted,
					},
				}, nil
			})
		},

		"poll": func(ctx chord.Context, task RunningTask[GenerationInput, GenerationCheckpoint], runtime hgRuntime) error {
			cp := task.Checkpoint
			if cp.Model == nil || cp.Handle == nil || cp.Cutoff == nil || cp.PollAt == nil {
				return fmt.Errorf("generation poll checkpoint missing required fields")
			}
			model := runtime.Models().GetModel(cp.Model.Provider, cp.Model.ModelId)
			if model == nil {
				return hgFailNoModel(ctx, runtime, cp.Model)
			}
			if err := runtime.Sleep(ctx, *cp.PollAt); err != nil {
				return err
			}
			fetchCtx := chord.WithAbortSignal(runtime.Signal(), ctx)
			message, err := runtime.Models().FetchDeferred(hutAbortSignal(fetchCtx), model, cp.Handle, nil)
			if err != nil {
				return err
			}
			pollAt := *cp.PollAt
			req := hgRequest{
				attempt:   cp.Attempt,
				compacted: cp.Compacted,
				model:     *cp.Model,
				cutoff:    *cp.Cutoff,
				pollAt:    &pollAt,
			}
			return hgClassify(ctx, runtime, req, message)
		},

		"tools": func(ctx chord.Context, task RunningTask[GenerationInput, GenerationCheckpoint], runtime hgRuntime) error {
			cp := task.Checkpoint
			if cp.Assistant == nil {
				return fmt.Errorf("generation tools checkpoint missing assistant")
			}
			if len(cp.Pending) == 0 {
				return hgFinishToolRound(ctx, runtime, *cp.Assistant, cp.Tools)
			}
			nextCallId := cp.Pending[0]
			rest := append([]string(nil), cp.Pending[1:]...)
			return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
				live, err := tx.Doc(LiveDoc, runtime.ConversationId())
				if err != nil {
					return nil, err
				}
				taskId, err := hgCreateToolTask(tx, runtime, *cp.Assistant, nextCallId)
				if err != nil {
					return nil, err
				}
				if rawTools, ok := live.Value.Get("tools"); ok {
					if arr, isArr := rawTools.([]any); isArr {
						for _, item := range arr {
							if obj, isObj := item.(*jsonx.Object); isObj && obj != nil {
								cid, _ := obj.Get("callId")
								_, hasTid := obj.Get("taskId")
								if cid == nextCallId && !hasTid {
									obj.Set("taskId", float64(taskId))
									break
								}
							}
						}
					}
				}
				nextTools := make([]TaskId, len(cp.Tools)+1)
				copy(nextTools, cp.Tools)
				nextTools[len(cp.Tools)] = taskId
				return &hgNext{
					Status: TaskStateWaiting,
					Checkpoint: GenerationCheckpoint{
						Phase:     "tools",
						Assistant: cp.Assistant,
						Tools:     nextTools,
						Pending:   rest,
					},
					On:     []TaskId{taskId},
					Policy: JoinPolicyAllSettled,
				}, nil
			})
		},
	}
	GenerationTask.Definition.Abort = func(ctx chord.Context, task RunningTask[GenerationInput, GenerationCheckpoint], runtime hgRuntime) error {
		cp := task.Checkpoint
		if cp.Phase == "poll" && cp.Model != nil && cp.Handle != nil {
			model := runtime.Models().GetModel(cp.Model.Provider, cp.Model.ModelId)
			if model != nil {
				cancelCtx := chord.WithAbortSignal(runtime.Signal(), ctx)
				if err := runtime.Models().CancelDeferred(hutAbortSignal(cancelCtx), model, cp.Handle, nil); err != nil {
					runtime.Report(err)
				}
			}
		}
		conversationId := runtime.ConversationId()
		var unstarted []*ai.ToolCall
		if cp.Phase == "tools" && cp.Assistant != nil && len(cp.Pending) > 0 {
			var err error
			unstarted, err = hgReadCalls(ctx, runtime, *cp.Assistant, cp.Pending)
			if err != nil {
				return err
			}
		}
		return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
			live, err := tx.Doc(LiveDoc, conversationId)
			if err != nil {
				return nil, err
			}
			if err := hgConvertPartial(tx, live.Value, conversationId); err != nil {
				return nil, err
			}
			for _, call := range unstarted {
				res := HarnessError("aborted", fmt.Sprintf("Tool %s was aborted", call.Name))
				if _, err := AppendToolResult(tx, conversationId, call, res, runtime.Now()); err != nil {
					return nil, err
				}
			}
			reason := "aborted"
			if err := EndRun(tx, live.Value, runtime.TaskId(), SubmissionSettlement{
				Status: SubmissionStatusUnanswered,
				Reason: &reason,
			}); err != nil {
				return nil, err
			}
			return &hgNext{
				Status: TaskStateTerminal,
				Outcome: &TypedTaskOutcome[GenerationResult]{
					Status: TaskOutcomeAborted,
				},
			}, nil
		})
	}
}

func hgBuildSimpleStreamOptions(streamOpts ConversationStreamOptions, thinkingLevel ai.ModelThinkingLevel) *ai.ModelsSimpleStreamOptions {
	opts := &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				Transport:      streamOpts.Transport,
				CacheRetention: streamOpts.CacheRetention,
				Metadata:       streamOpts.Metadata,
			},
		},
	}
	opts.TimeoutMs = streamOpts.TimeoutMs
	opts.MaxRetries = streamOpts.MaxRetries
	opts.MaxRetryDelayMs = streamOpts.MaxRetryDelayMs
	if streamOpts.Headers != nil {
		headers := omap.NewMap[string, *string]()
		for k, v := range streamOpts.Headers {
			val := v
			headers.Set(k, &val)
		}
		opts.Headers = headers
	}
	if streamOpts.Deferred != nil {
		if streamOpts.Deferred.Window != "" {
			w := ai.DeferredWindow(streamOpts.Deferred.Window)
			opts.Deferred = ai.SimpleStreamDeferredValue{
				V: &ai.SimpleStreamDeferredWindow{Window: &w},
			}
		} else {
			opts.Deferred = ai.SimpleStreamDeferredValue{
				V: ai.SimpleStreamDeferredBool(streamOpts.Deferred.Enabled),
			}
		}
	}
	if thinkingLevel != "" && thinkingLevel != ai.ModelThinkingLevelOff {
		r := ai.ThinkingLevel(thinkingLevel)
		opts.Reasoning = &r
	}
	return opts
}

func hgReadCalls(ctx chord.Context, runtime hgRuntime, assistant EntryId, callIds []string) ([]*ai.ToolCall, error) {
	entry, err := runtime.TypedEntry(ctx, AssistantEntry, assistant)
	if err != nil {
		return nil, err
	}
	var calls []*ai.ToolCall
	if entry != nil && len(entry.Model) > 0 {
		if asst, ok := entry.Model[0].(*ai.AssistantMessage); ok {
			for _, block := range asst.Content {
				if tc, ok := block.(*ai.ToolCall); ok {
					calls = append(calls, tc)
				}
			}
		}
	}
	var selected []*ai.ToolCall
	for _, id := range callIds {
		for _, c := range calls {
			if c.Id == id {
				selected = append(selected, c)
				break
			}
		}
	}
	return selected, nil
}

func hgCreateToolTask(tx Tx, runtime hgRuntime, assistant EntryId, callId string) (TaskId, error) {
	tid := runtime.TaskId()
	return tx.CreateTask(
		ToolTask,
		ToolTaskInput{Assistant: assistant, CallId: callId},
		TaskOptions{
			Ownership: TaskOwnership{
				Kind:   TaskOwnershipTask,
				TaskId: tid,
			},
		},
	)
}

func hgThresholdCompaction(view *ContextView, planned []EntryDraft, contextWindow int, policy CompactionPolicy) string {
	if !policy.Enabled || contextWindow <= 0 {
		return ""
	}
	var extra []ai.Message
	for _, entry := range planned {
		extra = append(extra, entry.Model...)
	}
	tokens := EstimateContext(view, extra)
	blocking := contextWindow - policy.ReserveTokens
	background := blocking - policy.BackgroundTokens
	over := ""
	if tokens > blocking {
		over = "blocking"
	} else if policy.BackgroundTokens > 0 && tokens > background {
		over = "background"
	}
	if over == "" {
		return ""
	}
	if _, ok := SelectCut(view, policy.KeepRecentTokens); !ok {
		return ""
	}
	return over
}

func hgFailModelError(ctx chord.Context, runtime hgRuntime, text string) error {
	return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
		live, err := tx.Doc(LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		reason := "model_error"
		if err := EndRun(tx, live.Value, runtime.TaskId(), SubmissionSettlement{
			Status: SubmissionStatusUnanswered,
			Reason: &reason,
			Detail: text,
		}); err != nil {
			return nil, err
		}
		return &hgNext{
			Status: TaskStateTerminal,
			Outcome: &TypedTaskOutcome[GenerationResult]{
				Status: TaskOutcomeFailed,
				Error: &TaskOutcomeError{
					Message: text,
					Detail:  jsonx.ObjectOf("reason", "model_error"),
				},
			},
		}, nil
	})
}

func hgFailNoModel(ctx chord.Context, runtime hgRuntime, ref *ModelRef) error {
	msg := "No model is configured"
	if ref != nil {
		msg = fmt.Sprintf("Model %s/%s is not available", ref.Provider, ref.ModelId)
	}
	return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
		live, err := tx.Doc(LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		reason := "no_model"
		if err := EndRun(tx, live.Value, runtime.TaskId(), SubmissionSettlement{
			Status: SubmissionStatusUnanswered,
			Reason: &reason,
		}); err != nil {
			return nil, err
		}
		return &hgNext{
			Status: TaskStateTerminal,
			Outcome: &TypedTaskOutcome[GenerationResult]{
				Status: TaskOutcomeFailed,
				Error: &TaskOutcomeError{
					Message: msg,
					Detail:  jsonx.ObjectOf("reason", "no_model"),
				},
			},
		}, nil
	})
}

// hgConvertPartial appends a committed in-flight partial as an aborted assistant entry.
func hgConvertPartial(tx Tx, live *jsonx.Object, conversationId ConversationId) error {
	genObj := hlGetObj(live, "generation")
	if genObj == nil {
		return nil
	}
	rawPartial, ok := genObj.Get("message")
	if !ok || rawPartial == nil {
		return nil
	}
	copied, err := stCopyStrictJsonValue(rawPartial)
	if err != nil {
		return err
	}
	var msg ai.AssistantMessage
	if err := jsonx.Decode(copied, &msg); err != nil {
		return err
	}
	msg.StopReason = ai.StopReasonAborted
	_, err = hgAppendAssistant(tx, conversationId, &msg)
	return err
}

func hgStreamResponse(
	ctx chord.Context,
	runtime hgRuntime,
	model *ai.Model,
	messages []ai.Message,
	opts *ai.ModelsSimpleStreamOptions,
	attempt int,
) (*ai.AssistantMessage, error) {
	var (
		mu       sync.Mutex
		pending  JsonValue
		timer    *time.Timer
		inFlight bool
		stopped  bool
		wg       sync.WaitGroup
	)
	var flush func()
	flush = func() {
		mu.Lock()
		timer = nil
		partial := pending
		pending = nil
		if partial == nil || stopped || inFlight {
			mu.Unlock()
			return
		}
		inFlight = true
		wg.Add(1)
		mu.Unlock()

		go func() {
			defer wg.Done()
			err := runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
				live, err := tx.Doc(LiveDoc, runtime.ConversationId())
				if err != nil {
					return nil, err
				}
				gen := hlGetObj(live.Value, "generation")
				if gen == nil {
					gen = jsonx.ObjectOf("attempt", float64(attempt))
					live.Value.Set("generation", gen)
				}
				hjAssignJson(gen, "message", partial)
				return nil, nil
			})
			if err != nil && runtime.Signal().Err() == nil {
				runtime.Report(err)
			}
			mu.Lock()
			inFlight = false
			if pending != nil && !stopped && timer == nil {
				timer = time.AfterFunc(hgPartialThrottleMs, flush)
			}
			mu.Unlock()
		}()
	}

	defer func() {
		mu.Lock()
		stopped = true
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		mu.Unlock()
		wg.Wait()
	}()

	streamCtx := chord.WithAbortSignal(runtime.Signal(), ctx)
	aiCtx := &ai.Context{Messages: append([]ai.Message(nil), messages...)}
	events := runtime.Models().StreamSimple(hutAbortSignal(streamCtx), model, aiCtx, opts)
	sig := hutAbortSignal(streamCtx)
	for {
		ev, ok, err := events.Next(sig)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		partial := hgEventPartial(ev)
		if partial == nil || len(partial.Content) == 0 {
			continue
		}
		encoded, err := jsonx.Encode(partial)
		if err != nil {
			continue
		}
		mu.Lock()
		pending = encoded
		if timer == nil && !inFlight && !stopped {
			timer = time.AfterFunc(hgPartialThrottleMs, flush)
		}
		mu.Unlock()
	}
	return events.Result(sig)
}

func hgEventPartial(ev ai.AssistantMessageEvent) *ai.AssistantMessage {
	switch e := ev.(type) {
	case *ai.AssistantMessageEventStart:
		return e.Partial
	case *ai.AssistantMessageEventTextStart:
		return e.Partial
	case *ai.AssistantMessageEventTextDelta:
		return e.Partial
	case *ai.AssistantMessageEventTextEnd:
		return e.Partial
	case *ai.AssistantMessageEventThinkingStart:
		return e.Partial
	case *ai.AssistantMessageEventThinkingDelta:
		return e.Partial
	case *ai.AssistantMessageEventThinkingEnd:
		return e.Partial
	case *ai.AssistantMessageEventToolcallStart:
		return e.Partial
	case *ai.AssistantMessageEventToolcallDelta:
		return e.Partial
	case *ai.AssistantMessageEventToolcallEnd:
		return e.Partial
	default:
		return nil
	}
}

func hgClassify(ctx chord.Context, runtime hgRuntime, req hgRequest, message *ai.AssistantMessage) error {
	if err := runtime.Signal().Err(); err != nil {
		return hutSignalErr(runtime.Signal())
	}
	conversationId := runtime.ConversationId()
	if message.StopReason == ai.StopReasonDeferred && message.Deferred != nil {
		handle := *message.Deferred
		pollAfter := hgDefaultPollAfterMs
		if handle.PollAfterMs != nil {
			pollAfter = *handle.PollAfterMs
		}
		pollAt := runtime.Now() + pollAfter
		if req.pollAt != nil && *req.pollAt+1 > pollAt {
			pollAt = *req.pollAt + 1
		}
		return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
			live, err := tx.Doc(LiveDoc, conversationId)
			if err != nil {
				return nil, err
			}
			live.Value.Set("generation", jsonx.ObjectOf(
				"attempt", float64(req.attempt),
				"deferred", jsonx.ObjectOf("pollAt", float64(pollAt)),
			))
			modelCopy := req.model
			cutoffCopy := req.cutoff
			pollAtCopy := pollAt
			return &hgNext{
				Status: TaskStateRunning,
				Checkpoint: GenerationCheckpoint{
					Phase:     "poll",
					Attempt:   req.attempt,
					Compacted: req.compacted,
					Model:     &modelCopy,
					Cutoff:    &cutoffCopy,
					Handle:    &handle,
					PollAt:    &pollAtCopy,
				},
			}, nil
		})
	}

	if err := runtime.Hooks().Each(func(h GenerationHooks) error {
		if h.AfterResponse == nil {
			return nil
		}
		return h.AfterResponse(message, runtime.Base(), ctx)
	}); err != nil {
		return err
	}

	var calls []*ai.ToolCall
	for _, block := range message.Content {
		if tc, ok := block.(*ai.ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	if message.StopReason == ai.StopReasonToolUse && len(calls) > 0 {
		return hgStartToolRound(ctx, runtime, req, message, calls)
	}
	if message.StopReason == ai.StopReasonStop || message.StopReason == ai.StopReasonLength || message.StopReason == ai.StopReasonToolUse {
		return hgAnswer(ctx, runtime, message)
	}

	settings := runtime.Settings()
	overflow := message.StopReason == ai.StopReasonError && ai.IsContextOverflow(message)
	if overflow && req.compacted == nil && settings.Compaction.Enabled {
		view, err := runtime.Context(ctx, conversationId, req.cutoff)
		if err != nil {
			return err
		}
		if _, ok := SelectCut(view, settings.Compaction.KeepRecentTokens); ok {
			errText := "Context overflow"
			if message.ErrorMessage != nil {
				errText = *message.ErrorMessage
			}
			return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
				live, err := tx.Doc(LiveDoc, conversationId)
				if err != nil {
					return nil, err
				}
				if _, err := hgAppendAssistant(tx, conversationId, message); err != nil {
					return nil, err
				}
				live.Value.Delete("generation")
				tid := runtime.TaskId()
				child, err := CreateCompaction(tx, conversationId, CompactionInput{Reason: CompactionReasonOverflow}, &tid)
				if err != nil {
					return nil, err
				}
				return &hgNext{
					Status: TaskStateWaiting,
					Checkpoint: GenerationCheckpoint{
						Phase:     "prepare",
						Attempt:   req.attempt,
						Compacted: &child,
						Overflow:  &errText,
					},
					On:     []TaskId{child},
					Policy: JoinPolicyAllSettled,
				}, nil
			})
		}
	}

	policy := settings.Retry
	retry := message.StopReason == ai.StopReasonError &&
		!overflow &&
		ai.IsRetryableAssistantError(message) &&
		policy.Enabled &&
		req.attempt <= policy.MaxRetries
	var until int64
	if retry {
		aiPolicy := ai.RetryPolicy{
			Enabled:         policy.Enabled,
			MaxRetries:      policy.MaxRetries,
			BaseDelayMs:     policy.BaseDelayMs,
			MaxAgentDelayMs: policy.MaxAgentDelayMs,
		}
		until = runtime.Now() + ai.RetryDelayMs(&aiPolicy, req.attempt)
	}
	return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
		live, err := tx.Doc(LiveDoc, conversationId)
		if err != nil {
			return nil, err
		}
		if _, err := hgAppendAssistant(tx, conversationId, message); err != nil {
			return nil, err
		}
		if retry {
			errMsg := ""
			if message.ErrorMessage != nil {
				errMsg = *message.ErrorMessage
			}
			live.Value.Set("generation", jsonx.ObjectOf(
				"attempt", float64(req.attempt),
				"retry", jsonx.ObjectOf("at", float64(until), "error", errMsg),
			))
			untilCopy := until
			return &hgNext{
				Status: TaskStateRunning,
				Checkpoint: GenerationCheckpoint{
					Phase:     "retry",
					Attempt:   req.attempt,
					Compacted: req.compacted,
					Until:     &untilCopy,
				},
			}, nil
		}
		text := fmt.Sprintf("Model response ended with stop reason %s", message.StopReason)
		if message.ErrorMessage != nil {
			text = *message.ErrorMessage
		}
		reason := "model_error"
		if err := EndRun(tx, live.Value, runtime.TaskId(), SubmissionSettlement{
			Status: SubmissionStatusUnanswered,
			Reason: &reason,
			Detail: text,
		}); err != nil {
			return nil, err
		}
		return &hgNext{
			Status: TaskStateTerminal,
			Outcome: &TypedTaskOutcome[GenerationResult]{
				Status: TaskOutcomeFailed,
				Error: &TaskOutcomeError{
					Message: text,
					Detail:  jsonx.ObjectOf("reason", "model_error"),
				},
			},
		}, nil
	})
}

func hgAnswer(ctx chord.Context, runtime hgRuntime, message *ai.AssistantMessage) error {
	var continuation *UserInput
	if err := runtime.Hooks().Each(func(h GenerationHooks) error {
		if h.OnYield == nil || continuation != nil {
			return nil
		}
		dec, err := h.OnYield(message, runtime.Base(), ctx)
		if err != nil {
			return err
		}
		if dec != nil && (dec.Continue.Text != nil || dec.Continue.Blocks != nil) {
			c := dec.Continue
			continuation = &c
		}
		return nil
	}); err != nil {
		return err
	}

	conversationId := runtime.ConversationId()
	return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
		settings := runtime.Settings()
		boundary, err := PrepareBoundary(tx, conversationId, QueueModes{
			SteeringMode: settings.SteeringMode,
			FollowUpMode: settings.FollowUpMode,
		})
		if err != nil {
			return nil, err
		}
		live, err := tx.Doc(LiveDoc, conversationId)
		if err != nil {
			return nil, err
		}
		entry, err := hgAppendAssistant(tx, conversationId, message)
		if err != nil {
			return nil, err
		}
		result := &hgNext{
			Status: TaskStateTerminal,
			Outcome: &TypedTaskOutcome[GenerationResult]{
				Status: TaskOutcomeCompleted,
				Result: GenerationResult{EntryId: entry.Id},
				HasRes: true,
			},
		}
		applied, err := ApplyBoundary(tx, boundary, BoundaryFinal, runtime.Now())
		if err != nil {
			return nil, err
		}
		if continuation != nil && len(applied.Users) == 0 && !applied.Reset {
			userMsg := &ai.UserMessage{
				Role:      "user",
				Content:   *continuation,
				Timestamp: runtime.Now(),
			}
			if _, err := tx.AppendTypedEntry(UserEntry, conversationId, EntryDraft{
				Model: ai.MessageList{userMsg},
			}); err != nil {
				return nil, err
			}
			nextGen, err := hgCreateGeneration(tx, conversationId)
			if err != nil {
				return nil, err
			}
			hgHandOver(live.Value, runtime.TaskId(), nextGen)
			live.Value.Delete("generation")
			return result, nil
		}
		ans := entry.Id
		if err := EndRun(tx, live.Value, runtime.TaskId(), SubmissionSettlement{
			Status: SubmissionStatusDone,
			Answer: &ans,
		}); err != nil {
			return nil, err
		}
		if len(applied.Users) > 0 {
			if err := hgStartRun(tx, conversationId, live.Value, applied.Users); err != nil {
				return nil, err
			}
		}
		return result, nil
	})
}

func hgStartToolRound(
	ctx chord.Context,
	runtime hgRuntime,
	req hgRequest,
	message *ai.AssistantMessage,
	calls []*ai.ToolCall,
) error {
	conversationId := runtime.ConversationId()
	messages := req.messages
	if !req.hasMsgs {
		view, err := runtime.Context(ctx, conversationId, req.cutoff)
		if err != nil {
			return err
		}
		messages = view.Messages
	}
	offered := make(map[string]struct{})
	for _, t := range ai.GetCurrentTools(messages) {
		offered[t.Name] = struct{}{}
	}
	agent, err := runtime.Agent(ctx)
	if err != nil {
		return err
	}
	sequential := runtime.Settings().ToolExecution == ToolExecutionModeSequential
	if !sequential {
		for _, call := range calls {
			if _, ok := offered[call.Name]; !ok {
				continue
			}
			if t := htlFindTool(agent.Tools, call.Name); t != nil && t.ExecutionMode == ToolExecutionModeSequential {
				sequential = true
				break
			}
		}
	}

	return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
		live, err := tx.Doc(LiveDoc, conversationId)
		if err != nil {
			return nil, err
		}
		entry, err := hgAppendAssistant(tx, conversationId, message)
		if err != nil {
			return nil, err
		}
		var slots []ToolSlot
		var tools []TaskId
		var pending []string
		for _, call := range calls {
			if _, ok := offered[call.Name]; !ok {
				unavailable := HarnessError("tool_unavailable", fmt.Sprintf("Tool %s is not available", call.Name))
				resEntry, err := AppendToolResult(tx, conversationId, call, unavailable, runtime.Now())
				if err != nil {
					return nil, err
				}
				rid := resEntry.Id
				slots = append(slots, ToolSlot{
					CallId: call.Id,
					Name:   call.Name,
					Status: ToolSlotDone,
					Entry:  &rid,
				})
				continue
			}
			if sequential && len(tools) > 0 {
				pending = append(pending, call.Id)
				slots = append(slots, ToolSlot{
					CallId: call.Id,
					Name:   call.Name,
					Status: ToolSlotPending,
				})
				continue
			}
			taskId, err := hgCreateToolTask(tx, runtime, entry.Id, call.Id)
			if err != nil {
				return nil, err
			}
			tools = append(tools, taskId)
			tidCopy := taskId
			slots = append(slots, ToolSlot{
				CallId: call.Id,
				Name:   call.Name,
				TaskId: &tidCopy,
				Status: ToolSlotPending,
			})
		}
		live.Value.Delete("generation")
		encodedSlots, err := jsonx.Encode(slots)
		if err != nil {
			return nil, err
		}
		live.Value.Set("tools", encodedSlots)
		asstId := entry.Id
		return &hgNext{
			Status: TaskStateWaiting,
			Checkpoint: GenerationCheckpoint{
				Phase:     "tools",
				Assistant: &asstId,
				Tools:     tools,
				Pending:   pending,
			},
			On:     tools,
			Policy: JoinPolicyAllSettled,
		}, nil
	})
}

func hgFinishToolRound(
	ctx chord.Context,
	runtime hgRuntime,
	assistant EntryId,
	tools []TaskId,
) error {
	conversationId := runtime.ConversationId()
	controls := make(map[TaskId]*ToolControl, len(tools))
	var controlOrder []*ToolControl
	outcomes, err := runtime.Outcomes(ctx, tools)
	if err != nil {
		return err
	}
	for i, id := range tools {
		var ctrl *ToolControl
		if i < len(outcomes) && outcomes[i].Status == TaskOutcomeCompleted {
			if resVal, ok := outcomes[i].Result.Get(); ok {
				var res ToolTaskResult
				if err := jsonx.Decode(resVal, &res); err == nil {
					ctrl = res.Control
				}
			}
		}
		controls[id] = ctrl
		controlOrder = append(controlOrder, ctrl)
	}

	liveObj, err := runtime.Snapshot(ctx, LiveDoc, conversationId)
	if err != nil {
		return err
	}
	var slots []ToolSlot
	if liveObj != nil {
		var liveState LiveState
		if err := jsonx.Decode(liveObj, &liveState); err == nil {
			slots = liveState.Tools
		}
	}
	var results []EntryId
	for _, slot := range slots {
		if slot.Entry != nil {
			results = append(results, *slot.Entry)
		}
	}

	if err := runtime.Hooks().Each(func(h GenerationHooks) error {
		if h.AfterTools == nil {
			return nil
		}
		return h.AfterTools(assistant, results, runtime.Base(), ctx)
	}); err != nil {
		return err
	}

	terminate := len(slots) > 0
	for _, slot := range slots {
		if slot.TaskId == nil {
			terminate = false
			break
		}
		c := controls[*slot.TaskId]
		if c == nil || !c.Terminate {
			terminate = false
			break
		}
	}
	var added []string
	var handoff *string
	for _, c := range controlOrder {
		if c == nil {
			continue
		}
		added = append(added, c.AddTools...)
		if c.Handoff != nil {
			h := *c.Handoff
			handoff = &h
		}
	}

	return runtime.Commit(ctx, func(tx Tx, _ RunningTask[GenerationInput, GenerationCheckpoint]) (*hgNext, error) {
		settings := runtime.Settings()
		boundary, err := PrepareBoundary(tx, conversationId, QueueModes{
			SteeringMode: settings.SteeringMode,
			FollowUpMode: settings.FollowUpMode,
		})
		if err != nil {
			return nil, err
		}
		if len(added) > 0 {
			if err := AddTools(tx, conversationId, added); err != nil {
				return nil, err
			}
		}
		live, err := tx.Doc(LiveDoc, conversationId)
		if err != nil {
			return nil, err
		}
		now := runtime.Now()
		if terminate || handoff != nil {
			if handoff != nil {
				userMsg := &ai.UserMessage{
					Role:      "user",
					Content:   ai.UserMessageContent{Text: handoff},
					Timestamp: now,
				}
				entry, err := tx.AppendTypedEntry(ResetEntry, conversationId, EntryDraft{
					Head:  EntryHeadSelf(),
					Model: ai.MessageList{userMsg},
				})
				if err != nil {
					return nil, err
				}
				hid := entry.Id
				boundary.Head = &hid
			}
			applied, err := ApplyBoundary(tx, boundary, BoundaryFinal, now)
			if err != nil {
				return nil, err
			}
			ans := assistant
			if err := EndRun(tx, live.Value, runtime.TaskId(), SubmissionSettlement{
				Status: SubmissionStatusDone,
				Answer: &ans,
			}); err != nil {
				return nil, err
			}
			if len(applied.Users) > 0 {
				if err := hgStartRun(tx, conversationId, live.Value, applied.Users); err != nil {
					return nil, err
				}
			}
		} else {
			applied, err := ApplyBoundary(tx, boundary, BoundaryPostTools, now)
			if err != nil {
				return nil, err
			}
			if applied.Reset {
				reason := "reset"
				if err := EndRun(tx, live.Value, runtime.TaskId(), SubmissionSettlement{
					Status: SubmissionStatusUnanswered,
					Reason: &reason,
				}); err != nil {
					return nil, err
				}
				if len(applied.Users) > 0 {
					if err := hgStartRun(tx, conversationId, live.Value, applied.Users); err != nil {
						return nil, err
					}
				}
			} else {
				live.Value.Delete("tools")
				if runObj := hlGetObj(live.Value, "run"); runObj != nil {
					if tid, ok := hlGetInt64(runObj, "taskId"); ok && TaskId(tid) == runtime.TaskId() && len(applied.Users) > 0 {
						var arr []any
						if rawInputs, has := runObj.Get("inputs"); has {
							if s, isArr := rawInputs.([]any); isArr {
								arr = append([]any(nil), s...)
							}
						}
						for _, uid := range applied.Users {
							arr = append(arr, float64(uid))
						}
						runObj.Set("inputs", arr)
					}
				}
				nextGen, err := hgCreateGeneration(tx, conversationId)
				if err != nil {
					return nil, err
				}
				hgHandOver(live.Value, runtime.TaskId(), nextGen)
			}
		}
		return &hgNext{
			Status: TaskStateTerminal,
			Outcome: &TypedTaskOutcome[GenerationResult]{
				Status: TaskOutcomeCompleted,
				Result: GenerationResult{EntryId: assistant},
				HasRes: true,
			},
		}, nil
	})
}

func hgAppendAssistant(tx Tx, conversationId ConversationId, message *ai.AssistantMessage) (*EntryRecord, error) {
	if err := huRecordUsage(tx, conversationId, "models", fmt.Sprintf("%s/%s", message.Provider, message.Model), message.Usage); err != nil {
		return nil, err
	}
	return tx.AppendTypedEntry(AssistantEntry, conversationId, EntryDraft{
		Model: ai.MessageList{message},
	})
}

func hgStartRun(tx Tx, conversationId ConversationId, live *jsonx.Object, inputs []SubmissionId) error {
	taskId, err := hgCreateGeneration(tx, conversationId)
	if err != nil {
		return err
	}
	inVals := make([]any, len(inputs))
	for i, id := range inputs {
		inVals[i] = float64(id)
	}
	live.Set("run", jsonx.ObjectOf(
		"taskId", float64(taskId),
		"inputs", inVals,
	))
	return nil
}

func hgCreateGeneration(tx Tx, conversationId ConversationId) (TaskId, error) {
	cid := conversationId
	return tx.CreateTask(GenerationTask, GenerationInput{}, TaskOptions{
		Ownership:      TaskOwnership{Kind: TaskOwnershipConversation},
		ConversationId: &cid,
	})
}

func hgHandOver(live *jsonx.Object, from TaskId, to TaskId) {
	runObj := hlGetObj(live, "run")
	if runObj == nil {
		return
	}
	if tid, ok := hlGetInt64(runObj, "taskId"); ok && TaskId(tid) == from {
		runObj.Set("taskId", float64(to))
	}
}

var _ = math.Floor
