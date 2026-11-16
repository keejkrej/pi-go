// Ported from packages/coding-agent/src/experimental/vacation/vacation.ts (pi v1.0.0).

package vacation

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type vacaSearchData struct {
	seconds int
	results []string
}

// Canned results; each topic takes a different time, so a crash can land between them.
var vacaSearches = map[string]vacaSearchData{
	"weather": {
		seconds: 6,
		results: []string{
			"Saturday: 24°C and sunny",
			"Sunday: 21°C, a short shower around 3 pm",
		},
	},
	"museums": {
		seconds: 10,
		results: []string{
			"Kunsthistorisches Museum: open 10-18, book a time slot",
			"Belvedere: Klimt's The Kiss, quietest before 11",
			"Albertina: Monet to Picasso, open until 21 on Friday",
		},
	},
	"trains": {
		seconds: 30,
		results: []string{
			"Railjet from Salzburg: 2h 22m, every 30 min",
			"Nightjet from Munich: arrives 06:20",
		},
	},
}

var vacaSearchStepDelay = 2 * time.Second

type vacaSearchArgs struct {
	Topic string `json:"topic"`
	City  string `json:"city"`
}

func vacaCreateSearchExtension() *durable.Extension {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("topic", typebox.Union([]*typebox.Schema{
		typebox.Literal("weather"),
		typebox.Literal("museums"),
		typebox.Literal("trains"),
	}))
	props.Set("city", typebox.String())
	schema := typebox.Object(props, jsonx.ObjectOf("required", []any{"topic", "city"}))

	searchTool := durable.DefineTool(&durable.ToolRegistration{
		Name:        "search",
		Description: "Search travel information about a city. Topics: weather, museums, trains. Slow; call several in parallel.",
		Parameters:  schema,
		// Only reads, so a call cut off by a crash simply runs again.
		Replay: durable.ToolReplaySafe,
		Execute: func(args any, api durable.ToolExecutionApi, ctx chord.Context) (*durable.ToolExecutionResult, error) {
			var input vacaSearchArgs
			if err := jsonx.Decode(args, &input); err != nil {
				return nil, err
			}
			found, ok := vacaSearches[input.Topic]
			if !ok {
				return nil, fmt.Errorf("unknown search topic %q", input.Topic)
			}
			steps := int(math.Ceil(float64(found.seconds) / 2.0))
			for step := 1; step <= steps; step++ {
				if ctx != nil && ctx.AbortSignal() != nil {
					if err := ctx.AbortSignal().Err(); err != nil {
						return nil, err
					}
				}
				api.Output(fmt.Sprintf("searching %s in %s: source %d/%d\n", input.Topic, input.City, step, steps))
				timer := time.NewTimer(vacaSearchStepDelay)
				if ctx != nil && ctx.AbortSignal() != nil {
					select {
					case <-ctx.AbortSignal().Done():
						timer.Stop()
						return nil, ctx.AbortSignal().Err()
					case <-timer.C:
					}
				} else {
					<-timer.C
				}
			}
			for _, result := range found.results {
				api.Output(fmt.Sprintf("- %s\n", result))
			}
			return &durable.ToolExecutionResult{}, nil
		},
	})

	return durable.DefineExtension(&durable.Extension{
		Name:  "vacation-search",
		Tools: []*durable.ToolRegistration{searchTool},
	})
}

// Search is what the research subagent gets: only search.
var Search *durable.Extension = vacaCreateSearchExtension()

type vacaResearchInput struct {
	Task string `json:"task"`
}

type vacaResearchState struct {
	Phase  string `json:"phase"`
	Report string `json:"report,omitzero"`
}

type vacaResearchArgs struct {
	Task string `json:"task"`
}

func vacaEntryAssistantText(entry *durable.EntryRecord) string {
	if entry == nil || len(entry.Model) == 0 {
		return ""
	}
	msg, ok := entry.Model[0].(*ai.AssistantMessage)
	if !ok || msg == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range msg.Content {
		if tc, ok := part.(*ai.TextContent); ok && tc != nil {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Delivers the task to its subagent and posts the report to the main conversation as a new message.
var vacaResearchTask = durable.DefineTask[vacaResearchInput, vacaResearchState, any, struct{}](
	durable.TaskDefinition[vacaResearchInput, vacaResearchState, any, struct{}]{
		Name:    "vacation.research",
		Version: 1,
		Initial: func(_ vacaResearchInput) vacaResearchState {
			return vacaResearchState{Phase: "deliver"}
		},
		Phases: map[string]durable.PhaseHandler[vacaResearchInput, vacaResearchState, any, struct{}]{
			"deliver": func(ctx chord.Context, task durable.RunningTask[vacaResearchInput, vacaResearchState], runtime *durable.TaskRuntime[vacaResearchInput, vacaResearchState, any, struct{}]) error {
				// The subagent's conversation is owned by this task. No Session call may run inside a commit.
				var owned *durable.ConversationId
				if err := runtime.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[vacaResearchInput, vacaResearchState]) (*durable.NextTaskState[vacaResearchState, any], error) {
					tid := running.Id
					page, scanErr := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &tid}, 1, nil)
					if scanErr != nil {
						return nil, scanErr
					}
					if len(page.Items) > 0 {
						id := page.Items[0].Id
						owned = &id
					}
					return nil, nil
				}); err != nil {
					return err
				}
				if owned == nil {
					return errors.New("research child conversation not found")
				}
				child, err := runtime.Conversation(ctx, *owned)
				if err != nil {
					return err
				}
				if child == nil {
					return fmt.Errorf("research child conversation %d not found", *owned)
				}
				// A rerun after a crash gets the same submission back.
				sub, err := child.Submit(durable.InputSubmissionDraft{
					RequestId: fmt.Sprintf("research:%d", task.Id),
					Content:   durable.UserInput{Text: &task.Input.Task},
				}, ctx)
				if err != nil {
					return err
				}
				settled, err := sub.Wait(ctx)
				if err != nil {
					return err
				}
				return runtime.Commit(ctx, func(tx durable.Tx, running durable.RunningTask[vacaResearchInput, vacaResearchState]) (*durable.NextTaskState[vacaResearchState, any], error) {
					reason := "?"
					if settled.Status == durable.SubmissionStatusUnanswered && settled.Reason != nil && *settled.Reason != "" {
						reason = *settled.Reason
					}
					report := fmt.Sprintf("[research report] The research failed: %s", reason)
					if settled.Status == durable.SubmissionStatusDone && settled.Type == durable.SubmissionTypeInput && settled.Answer != nil {
						entry, entryErr := tx.TypedEntry(durable.AssistantEntry, *settled.Answer)
						if entryErr != nil {
							return nil, entryErr
						}
						report = "[research report] " + vacaEntryAssistantText(entry)
					}
					return &durable.NextTaskState[vacaResearchState, any]{
						Status:     durable.TaskStateRunning,
						Checkpoint: vacaResearchState{Phase: "report", Report: report},
					}, nil
				})
			},
			"report": func(ctx chord.Context, task durable.RunningTask[vacaResearchInput, vacaResearchState], runtime *durable.TaskRuntime[vacaResearchInput, vacaResearchState, any, struct{}]) error {
				mainConv, err := runtime.Conversation(ctx, runtime.ConversationId())
				if err != nil {
					return err
				}
				if mainConv == nil {
					return fmt.Errorf("main conversation %d not found", runtime.ConversationId())
				}
				report := task.Checkpoint.Report
				if _, err := mainConv.Submit(durable.InputSubmissionDraft{
					RequestId: fmt.Sprintf("research-report:%d", task.Id),
					Content:   durable.UserInput{Text: &report},
					WhenBusy:  durable.SubmissionWhenBusyFollowUp,
				}, ctx); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[vacaResearchInput, vacaResearchState]) (*durable.NextTaskState[vacaResearchState, any], error) {
					return &durable.NextTaskState[vacaResearchState, any]{
						Status: durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[any]{
							Status: durable.TaskOutcomeCompleted,
							HasRes: true,
							Result: nil,
						},
					}, nil
				})
			},
		},
		Abort: func(ctx chord.Context, _ durable.RunningTask[vacaResearchInput, vacaResearchState], runtime *durable.TaskRuntime[vacaResearchInput, vacaResearchState, any, struct{}]) error {
			return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[vacaResearchInput, vacaResearchState]) (*durable.NextTaskState[vacaResearchState, any], error) {
				return &durable.NextTaskState[vacaResearchState, any]{
					Status: durable.TaskStateTerminal,
					Outcome: &durable.TypedTaskOutcome[any]{
						Status: durable.TaskOutcomeAborted,
					},
				}, nil
			})
		},
	},
)

func vacaCreateVacationExtension() *durable.Extension {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("task", typebox.String(jsonx.ObjectOf(
		"description", "What to research, with the city and dates",
	)))
	schema := typebox.Object(props, jsonx.ObjectOf("required", []any{"task"}))

	researchTool := durable.DefineTool(&durable.ToolRegistration{
		Name:        "research",
		Description: "Start a research subagent in the background. It searches while you keep talking with the user; its report arrives later as a message starting with [research report].",
		Parameters:  schema,
		Execute: func(args any, api durable.ToolExecutionApi, ctx chord.Context) (*durable.ToolExecutionResult, error) {
			var input vacaResearchArgs
			if err := jsonx.Decode(args, &input); err != nil {
				return nil, err
			}
			if _, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				// Background: the main conversation stays free while the research runs.
				convId := api.ConversationId()
				bg := true
				owner, createTaskErr := tx.CreateTask(
					vacaResearchTask,
					vacaResearchInput{Task: input.Task},
					durable.TaskOptions{
						Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
						ConversationId: &convId,
						Background:     &bg,
					},
				)
				if createTaskErr != nil {
					return nil, createTaskErr
				}
				child, createConvErr := tx.CreateConversation(durable.ConversationCreateTxOptions{
					Ownership: durable.ConversationOwnership{
						Kind:   durable.ConversationOwnershipTask,
						TaskId: owner,
					},
				})
				if createConvErr != nil {
					return nil, createConvErr
				}
				if cfgErr := durable.Configure(tx, child.Id, durable.AgentChange{
					Extensions:   jsonx.Some(durable.AgentExtensionsSet(Search)),
					Instructions: jsonx.Some("You are a research subagent for a vacation planner. Search weather, museums, and trains in parallel, in one step, then report the findings in a few short bullet points."),
				}); cfgErr != nil {
					return nil, cfgErr
				}
				return nil, nil
			}); err != nil {
				return nil, err
			}
			return &durable.ToolExecutionResult{
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "Research started in the background."}},
				HasContent: true,
			}, nil
		},
	})

	return durable.DefineExtension(&durable.Extension{
		Name:  "vacation",
		Tools: []*durable.ToolRegistration{researchTool},
		Tasks: []durable.AnyTask{vacaResearchTask},
		Sections: []*durable.PromptSection{
			durable.Section(
				"preamble",
				func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
					return "You are a friendly vacation planning assistant. Keep answers short. Hand all research to the research tool; while it runs, keep chatting with the user. When a [research report] arrives, turn it into a short plan.", true, nil
				},
				false,
			),
		},
	})
}

// Vacation is what the main conversation gets: the prompt, research, and the task that delivers its report.
var Vacation *durable.Extension = vacaCreateVacationExtension()
