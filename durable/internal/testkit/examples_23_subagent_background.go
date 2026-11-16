// Ported from packages/durable/test/examples/23-subagent-background.ts (pi v1.0.0).

package testkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type e2sbSubagent struct {
	ConversationId durable.ConversationId `json:"conversationId"`
	Reported       []durable.EntryId      `json:"reported"`
}

type e2sbSubagentsState struct {
	Agents    map[string]e2sbSubagent   `json:"agents"`
	Reporters map[string]durable.TaskId `json:"reporters"`
}

type e2sbAnchorState struct {
	Phase string `json:"phase"`
}

type e2sbReporterInput struct {
	Name           string                 `json:"name"`
	ConversationId durable.ConversationId `json:"conversationId"`
	Message        string                 `json:"message"`
	FollowUp       bool                   `json:"followUp"`
}

type e2sbReporterState struct {
	Phase  string  `json:"phase"`
	Report *string `json:"report,omitzero"`
}

type e2sbToolArgs struct {
	Action   string  `json:"action"`
	Name     *string `json:"name,omitzero"`
	Message  *string `json:"message,omitzero"`
	FollowUp *bool   `json:"followUp,omitzero"`
}

func e2sbTextOf(msg *ai.AssistantMessage) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range msg.Content {
		if tc, ok := c.(*ai.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func e2sbMsgText(msg ai.Message) string {
	switch m := msg.(type) {
	case *ai.UserMessage:
		if m.Content.Text != nil {
			return *m.Content.Text
		}
		var sb strings.Builder
		for _, c := range m.Content.Blocks {
			if tc, ok := c.(*ai.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		return sb.String()
	case *ai.AssistantMessage:
		return e2sbTextOf(m)
	case *ai.ToolResultMessage:
		var sb strings.Builder
		for _, c := range m.Content {
			if tc, ok := c.(*ai.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		return sb.String()
	default:
		return ""
	}
}

func e2sbDecodeSubagents(obj durable.JsonObject) (e2sbSubagentsState, error) {
	st := e2sbSubagentsState{
		Agents:    map[string]e2sbSubagent{},
		Reporters: map[string]durable.TaskId{},
	}
	if obj == nil {
		return st, nil
	}
	if err := jsonx.Decode(obj, &st); err != nil {
		return st, err
	}
	if st.Agents == nil {
		st.Agents = map[string]e2sbSubagent{}
	}
	if st.Reporters == nil {
		st.Reporters = map[string]durable.TaskId{}
	}
	return st, nil
}

// Example23SubagentBackground runs the 23-subagent-background example.
func Example23SubagentBackground(out io.Writer) error {
	sw := NewSyncWriter(out)
	defer sw.Close()
	out = sw
	ctx := TestContext
	subagentsDoc, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "app.subagents",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryLatest,
		Fork:    durable.DocumentForkInitial,
		Initial: func() durable.JsonObject {
			return jsonx.ObjectOf("agents", jsonx.NewObject(), "reporters", jsonx.NewObject())
		},
	})
	if err != nil {
		return err
	}

	anchor := durable.DefineTask[any, e2sbAnchorState, any, struct{}](
		durable.TaskDefinition[any, e2sbAnchorState, any, struct{}]{
			Name:    "app.subagent-anchor",
			Version: 1,
			Initial: func(_ any) e2sbAnchorState { return e2sbAnchorState{Phase: "done"} },
			Phases: map[string]durable.PhaseHandler[any, e2sbAnchorState, any, struct{}]{
				"done": func(taskCtx chord.Context, task durable.RunningTask[any, e2sbAnchorState], runtime *durable.TaskRuntime[any, e2sbAnchorState, any, struct{}]) error {
					return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[any, e2sbAnchorState]) (*durable.NextTaskState[e2sbAnchorState, any], error) {
						c := Completed[e2sbAnchorState, any](task.Checkpoint, nil)
						return &c, nil
					})
				},
			},
			Abort: func(taskCtx chord.Context, _ durable.RunningTask[any, e2sbAnchorState], runtime *durable.TaskRuntime[any, e2sbAnchorState, any, struct{}]) error {
				return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[any, e2sbAnchorState]) (*durable.NextTaskState[e2sbAnchorState, any], error) {
					return &durable.NextTaskState[e2sbAnchorState, any]{
						Status:  durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[any]{Status: durable.TaskOutcomeAborted},
					}, nil
				})
			},
		},
	)

	reporter := durable.DefineTask[e2sbReporterInput, e2sbReporterState, any, struct{}](
		durable.TaskDefinition[e2sbReporterInput, e2sbReporterState, any, struct{}]{
			Name:    "app.subagent-reporter",
			Version: 1,
			Initial: func(_ e2sbReporterInput) e2sbReporterState { return e2sbReporterState{Phase: "deliver"} },
			Phases: map[string]durable.PhaseHandler[e2sbReporterInput, e2sbReporterState, any, struct{}]{
				"deliver": func(taskCtx chord.Context, task durable.RunningTask[e2sbReporterInput, e2sbReporterState], runtime *durable.TaskRuntime[e2sbReporterInput, e2sbReporterState, any, struct{}]) error {
					in := task.Input
					subagent, err := runtime.Conversation(taskCtx, in.ConversationId)
					if err != nil {
						return err
					}
					mode := durable.SubmissionWhenBusySteer
					if in.FollowUp {
						mode = durable.SubmissionWhenBusyFollowUp
					}
					sub, err := subagent.Submit(durable.InputSubmissionDraft{
						RequestId: fmt.Sprintf("subagent:%d", task.Id),
						Content:   ai.UserMessageContent{Text: &in.Message},
						WhenBusy:  mode,
					}, taskCtx)
					if err != nil {
						return err
					}
					settled, err := sub.Wait(taskCtx)
					if err != nil {
						return err
					}
					return runtime.Commit(taskCtx, func(tx durable.Tx, _ durable.RunningTask[e2sbReporterInput, e2sbReporterState]) (*durable.NextTaskState[e2sbReporterState, any], error) {
						next := func(report *string) (*durable.NextTaskState[e2sbReporterState, any], error) {
							return &durable.NextTaskState[e2sbReporterState, any]{
								Status:     durable.TaskStateRunning,
								Checkpoint: e2sbReporterState{Phase: "report", Report: report},
							}, nil
						}
						if settled.Status == durable.SubmissionStatusUnanswered {
							if settled.Reason != nil && *settled.Reason == "aborted" {
								return next(nil)
							}
							rStr := ""
							if settled.Reason != nil {
								rStr = *settled.Reason
							}
							msg := fmt.Sprintf("[subagent %s failed: %s]", in.Name, rStr)
							return next(&msg)
						}
						if settled.Type != durable.SubmissionTypeInput || settled.Answer == nil {
							return next(nil)
						}
						doc, err := tx.Doc(subagentsDoc, runtime.ConversationId())
						if err != nil {
							return nil, err
						}
						st, err := e2sbDecodeSubagents(doc.Value)
						if err != nil {
							return nil, err
						}
						ag := st.Agents[in.Name]
						for _, rep := range ag.Reported {
							if rep == *settled.Answer {
								return next(nil)
							}
						}
						ag.Reported = append(ag.Reported, *settled.Answer)
						st.Agents[in.Name] = ag
						rawAgents, err := jsonx.Encode(st.Agents)
						if err != nil {
							return nil, err
						}
						doc.Value.Set("agents", rawAgents)
						entry, err := tx.TypedEntry(durable.AssistantEntry, *settled.Answer)
						if err != nil {
							return nil, err
						}
						var ans *ai.AssistantMessage
						if entry != nil && len(entry.Model) > 0 {
							ans, _ = entry.Model[0].(*ai.AssistantMessage)
						}
						msg := fmt.Sprintf("[subagent %s answered, no reply needed] %s", in.Name, e2sbTextOf(ans))
						return next(&msg)
					})
				},
				"report": func(taskCtx chord.Context, task durable.RunningTask[e2sbReporterInput, e2sbReporterState], runtime *durable.TaskRuntime[e2sbReporterInput, e2sbReporterState, any, struct{}]) error {
					if task.Checkpoint.Report != nil {
						mainConv, err := runtime.Conversation(taskCtx, runtime.ConversationId())
						if err != nil {
							return err
						}
						reportText := *task.Checkpoint.Report
						if _, err := mainConv.Submit(durable.InputSubmissionDraft{
							RequestId: fmt.Sprintf("subagent-report:%d", task.Id),
							Content:   ai.UserMessageContent{Text: &reportText},
							WhenBusy:  durable.SubmissionWhenBusyFollowUp,
						}, taskCtx); err != nil {
							return err
						}
					}
					return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e2sbReporterInput, e2sbReporterState]) (*durable.NextTaskState[e2sbReporterState, any], error) {
						c := Completed[e2sbReporterState, any](task.Checkpoint, nil)
						return &c, nil
					})
				},
			},
			Abort: func(taskCtx chord.Context, _ durable.RunningTask[e2sbReporterInput, e2sbReporterState], runtime *durable.TaskRuntime[e2sbReporterInput, e2sbReporterState, any, struct{}]) error {
				return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e2sbReporterInput, e2sbReporterState]) (*durable.NextTaskState[e2sbReporterState, any], error) {
					return &durable.NextTaskState[e2sbReporterState, any]{
						Status:  durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[any]{Status: durable.TaskOutcomeAborted},
					}, nil
				})
			},
		},
	)

	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("action", typebox.String())
	props.Set("name", typebox.Optional(typebox.String()))
	props.Set("message", typebox.Optional(typebox.String()))
	props.Set("followUp", typebox.Optional(typebox.Boolean()))

	var subagentTools *durable.Extension
	subagentTool := durable.DefineTool(&durable.ToolRegistration{
		Name:        "subagent",
		Description: "Manage persistent subagents that work in the background.",
		Parameters:  typebox.Object(props),
		Replay:      durable.ToolReplayUnsafe,
		Execute: func(args any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			var in e2sbToolArgs
			if err := jsonx.Decode(args, &in); err != nil {
				return nil, err
			}
			reply := func(text string, cid *durable.ConversationId) *durable.ToolExecutionResult {
				res := &durable.ToolExecutionResult{
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
					HasContent: true,
				}
				if cid != nil && in.Name != nil {
					res.Details = jsonx.ObjectOf("name", *in.Name, "conversationId", float64(*cid))
					res.HasDetails = true
				}
				return res
			}
			snap, err := api.Snapshot(callCtx, subagentsDoc, api.ConversationId())
			if err != nil {
				return nil, err
			}
			reg, err := e2sbDecodeSubagents(snap)
			if err != nil {
				return nil, err
			}
			if in.Action == "status" {
				var names []string
				if in.Name == nil {
					for k := range reg.Agents {
						names = append(names, k)
					}
				} else {
					names = []string{*in.Name}
				}
				var lines []string
				for _, each := range names {
					found, ok := reg.Agents[each]
					if !ok {
						continue
					}
					liveSnap, err := api.Snapshot(callCtx, durable.LiveDoc, found.ConversationId)
					if err != nil {
						return nil, err
					}
					var live durable.LiveState
					if liveSnap != nil {
						_ = jsonx.Decode(liveSnap, &live)
					}
					stateLabel := "idle"
					if live.Run != nil {
						stateLabel = "working"
					}
					lines = append(lines, each+": "+stateLabel)
				}
				if len(lines) == 0 {
					return reply("No subagents.", nil), nil
				}
				return reply(strings.Join(lines, "\n"), nil), nil
			}
			if in.Name == nil {
				return reply(in.Action+" needs a name.", nil), nil
			}
			name := *in.Name
			agent, exists := reg.Agents[name]
			if in.Action != "spawn" && !exists {
				return reply("No subagent named "+name+".", nil), nil
			}
			if in.Action == "stop" {
				convHandle, err := api.Conversation(callCtx, agent.ConversationId)
				if err != nil {
					return nil, err
				}
				if err := convHandle.Abort(callCtx); err != nil {
					return nil, err
				}
				return reply("Stopped "+name+".", &agent.ConversationId), nil
			}
			if in.Message == nil {
				return reply(in.Action+" needs a message.", nil), nil
			}
			msg := *in.Message
			followUp := in.Action == "send" && in.FollowUp != nil && *in.FollowUp

			rawText, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
				doc, err := tx.Doc(subagentsDoc, api.ConversationId())
				if err != nil {
					return "", err
				}
				st, err := e2sbDecodeSubagents(doc.Value)
				if err != nil {
					return "", err
				}
				bg := true
				bgOpts := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation}, Background: &bg}
				if in.Action == "spawn" {
					if _, dup := st.Agents[name]; dup {
						return name + " already exists; use send.", nil
					}
					anchorId, err := tx.CreateTask(anchor, nil, bgOpts)
					if err != nil {
						return "", err
					}
					child, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: anchorId},
					})
					if err != nil {
						return "", err
					}
					if err := durable.Configure(tx, child.Id, durable.AgentChange{
						Extensions:   jsonx.Some(durable.AgentExtensionsEdit(nil, []*durable.Extension{subagentTools})),
						Instructions: jsonx.Some(fmt.Sprintf("You are the subagent %q. Answer the main agent's requests.", name)),
					}); err != nil {
						return "", err
					}
					st.Agents[name] = e2sbSubagent{ConversationId: child.Id, Reported: []durable.EntryId{}}
				}
				cid := st.Agents[name].ConversationId
				repInput := e2sbReporterInput{Name: name, ConversationId: cid, Message: msg, FollowUp: followUp}
				repId, err := tx.CreateTask(reporter, repInput, bgOpts)
				if err != nil {
					return "", err
				}
				st.Reporters[fmt.Sprintf("%d", api.TaskId())] = repId
				rawAgents, err := jsonx.Encode(st.Agents)
				if err != nil {
					return "", err
				}
				rawReps, err := jsonx.Encode(st.Reporters)
				if err != nil {
					return "", err
				}
				doc.Value.Set("agents", rawAgents)
				doc.Value.Set("reporters", rawReps)
				if in.Action == "send" {
					return "Sent to " + name + ".", nil
				}
				return "Started " + name + ".", nil
			})
			if err != nil {
				return nil, err
			}
			afterSnap, err := api.Snapshot(callCtx, subagentsDoc, api.ConversationId())
			if err != nil {
				return nil, err
			}
			afterReg, err := e2sbDecodeSubagents(afterSnap)
			if err != nil {
				return nil, err
			}
			var cidPtr *durable.ConversationId
			if cur, ok := afterReg.Agents[name]; ok {
				c := cur.ConversationId
				cidPtr = &c
			}
			return reply(rawText.(string), cidPtr), nil
		},
	})
	subagentTools = durable.DefineExtension(&durable.Extension{
		Name:  "subagent-tools",
		Tasks: []durable.AnyTask{anchor, reporter},
		Tools: []*durable.ToolRegistration{subagentTool},
	})

	tps := 120.0
	faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{TokensPerSecond: &tps})
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	toolUse := ai.StopReasonToolUse
	toolCallMsg := func(kv ...any) *ai.AssistantMessage {
		return providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("subagent", jsonx.ObjectOf(kv...), nil)},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)
	}
	answerMsg := func(text string) *ai.AssistantMessage {
		return providers.FauxAssistantMessage(providers.FauxAssistantText(text), nil)
	}
	route := providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
		var last ai.Message
		for i := len(transcript.Messages) - 1; i >= 0; i-- {
			if transcript.Messages[i].GetRole() != "system" {
				last = transcript.Messages[i]
				break
			}
		}
		text := e2sbMsgText(last)
		if last != nil && last.GetRole() == "toolResult" {
			return answerMsg("OK. " + text), nil
		}
		switch {
		case strings.Contains(text, "Start a subagent"):
			return toolCallMsg("action", "spawn", "name", "reader", "message", "Summarize the plot of Moby Dick."), nil
		case strings.Contains(text, "whale's name"):
			return toolCallMsg("action", "send", "name", "reader", "message", "What is the whale called?"), nil
		case strings.Contains(text, "every chapter"):
			return toolCallMsg("action", "send", "name", "reader", "message", "Now go through all chapters in detail."), nil
		case strings.Contains(text, "Stop reader"):
			return toolCallMsg("action", "stop", "name", "reader"), nil
		case strings.Contains(text, "my subagents"):
			return toolCallMsg("action", "status"), nil
		case strings.Contains(text, "[subagent"):
			return answerMsg("Noted."), nil
		case strings.Contains(text, "Summarize the plot"):
			return answerMsg("A whale, a captain, an obsession."), nil
		case strings.Contains(text, "whale called"):
			return answerMsg("Moby Dick."), nil
		default:
			var chs []string
			for i := 1; i <= 135; i++ {
				chs = append(chs, fmt.Sprintf("Chapter %d: more whaling.", i))
			}
			return answerMsg(strings.Join(chs, "\n")), nil
		}
	})
	steps := make([]providers.FauxResponseStep, 40)
	for i := range steps {
		steps[i] = route
	}
	faux.SetResponses(steps)

	registry := durable.CreateRegistry()
	if err := registry.Install(subagentTools); err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "pi-durable-subagents-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()

	open := func() (durable.Harness, durable.Conversation, error) {
		st, err := sqlite.OpenNodeSqliteStorage(filepath.Join(directory, "session.sqlite"))
		if err != nil {
			return nil, nil, err
		}
		h, err := durable.HarnessOpen(st, durable.HarnessOptions{Models: models, Registry: registry}, ctx)
		if err != nil {
			return nil, nil, err
		}
		r, err := h.Root(ctx, durable.RootOptions{
			Agent: &durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
		})
		if err != nil {
			_ = h.Close(ctx)
			return nil, nil, err
		}
		return h, r, nil
	}

	follow := func(h durable.Harness, id durable.ConversationId) (durable.AgentEventStream, error) {
		stream, err := durable.WatchEvents(h, id, ctx)
		if err != nil {
			return nil, err
		}
		if err := stream.Start(func(events []durable.AgentEvent, _ chord.Context) error {
			for _, ev := range events {
				me, ok := ev.(*durable.MessageEndEvent)
				if !ok || len(me.Entry.Model) == 0 {
					continue
				}
				switch m := me.Entry.Model[0].(type) {
				case *ai.UserMessage:
					_, _ = fmt.Fprintf(out, "> %s\n", e2sbMsgText(m))
				case *ai.AssistantMessage:
					for _, c := range m.Content {
						if tc, ok := c.(*ai.ToolCall); ok {
							rawArgs, _ := jsonx.Stringify(tc.Arguments)
							_, _ = fmt.Fprintf(out, "  %s %s\n", tc.Name, rawArgs)
						}
					}
					if t := e2sbTextOf(m); t != "" {
						_, _ = fmt.Fprintln(out, t)
					}
				case *ai.ToolResultMessage:
					_, _ = fmt.Fprintf(out, "  -> %s\n", e2sbMsgText(m))
				}
			}
			return nil
		}); err != nil {
			_ = stream.Stop()
			return nil, err
		}
		return stream, nil
	}

	harness, root, err := open()
	if err != nil {
		return err
	}
	var currentHarnessMu sync.Mutex
	currentStream, err := follow(harness, root.Id())
	if err != nil {
		_ = harness.Close(ctx)
		return err
	}
	defer func() {
		currentHarnessMu.Lock()
		st := currentStream
		h := harness
		currentHarnessMu.Unlock()
		if st != nil {
			_ = st.Stop()
		}
		if h != nil {
			_ = h.Close(ctx)
		}
	}()

	say := func(text string) error {
		t := text
		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: ai.UserMessageContent{Text: &t},
		}, ctx)
		if err != nil {
			return err
		}
		_, err = sub.Wait(ctx)
		return err
	}
	settle := func() error {
		snap, err := harness.Snapshot(ctx, subagentsDoc, root.Id())
		if err != nil {
			return err
		}
		st, err := e2sbDecodeSubagents(snap)
		if err != nil {
			return err
		}
		for _, repId := range st.Reporters {
			if _, err := harness.WaitForTask(ctx, repId); err != nil {
				return err
			}
		}
		return root.WaitForIdle(ctx)
	}
	working := func(name string) (bool, error) {
		snap, err := harness.Snapshot(ctx, subagentsDoc, root.Id())
		if err != nil {
			return false, err
		}
		st, err := e2sbDecodeSubagents(snap)
		if err != nil {
			return false, err
		}
		ag, ok := st.Agents[name]
		if !ok {
			return false, nil
		}
		liveSnap, err := harness.Snapshot(ctx, durable.LiveDoc, ag.ConversationId)
		if err != nil || liveSnap == nil {
			return false, err
		}
		var live durable.LiveState
		if err := jsonx.Decode(liveSnap, &live); err != nil {
			return false, err
		}
		return live.Run != nil, nil
	}

	if err := say("Start a subagent named reader that summarizes Moby Dick."); err != nil {
		return err
	}
	if err := settle(); err != nil {
		return err
	}
	if err := say("Ask reader to summarize every chapter."); err != nil {
		return err
	}
	if err := WaitFor(func() (bool, error) { return working("reader") }, 10_000); err != nil {
		return err
	}
	if err := say("Stop reader."); err != nil {
		return err
	}
	if err := settle(); err != nil {
		return err
	}
	if err := say("What are my subagents doing?"); err != nil {
		return err
	}
	if err := settle(); err != nil {
		return err
	}

	reportersCount := func() (int, error) {
		snap, err := harness.Snapshot(ctx, subagentsDoc, root.Id())
		if err != nil {
			return 0, err
		}
		st, err := e2sbDecodeSubagents(snap)
		if err != nil {
			return 0, err
		}
		return len(st.Reporters), nil
	}
	before, err := reportersCount()
	if err != nil {
		return err
	}
	whaleQ := "Ask reader for the whale's name."
	if _, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &whaleQ},
	}, ctx); err != nil {
		return err
	}
	if err := WaitFor(func() (bool, error) {
		n, err := reportersCount()
		return n > before, err
	}, 10_000); err != nil {
		return err
	}

	_ = currentStream.Stop()
	_ = harness.Close(ctx)
	_, _ = fmt.Fprintln(out, "(process restarts)")

	h2, r2, err := open()
	if err != nil {
		return err
	}
	currentHarnessMu.Lock()
	harness = h2
	root = r2
	currentHarnessMu.Unlock()

	s2, err := follow(harness, root.Id())
	if err != nil {
		return err
	}
	currentHarnessMu.Lock()
	currentStream = s2
	currentHarnessMu.Unlock()

	if err := settle(); err != nil {
		return err
	}
	// Give listener callbacks one final flush cycle before stopping stream cleanly.
	time.Sleep(20 * time.Millisecond)
	_ = currentStream.Stop()
	currentStream = nil
	err = harness.Close(ctx)
	harness = nil
	return err
}
