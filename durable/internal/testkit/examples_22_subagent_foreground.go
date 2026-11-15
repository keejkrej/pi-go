// Ported from packages/durable/test/examples/22-subagent-foreground.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type e2sfArgs struct {
	Task string `json:"task"`
}

func e2sfAnswerText(api durable.ToolExecutionApi, answer durable.EntryId, callCtx chord.Context) (string, error) {
	raw, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
		return tx.TypedEntry(durable.AssistantEntry, answer)
	})
	if err != nil {
		return "", err
	}
	entry := raw.(*durable.EntryRecord)
	msg := entry.Model[0].(*ai.AssistantMessage)
	var sb strings.Builder
	for _, c := range msg.Content {
		if tc, ok := c.(*ai.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), nil
}

// Example22SubagentForeground runs the 22-subagent-foreground example.
func Example22SubagentForeground(out io.Writer) error {
	sw := NewSyncWriter(out)
	defer sw.Close()
	out = sw
	ctx := TestContext
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("task", typebox.String())

	var subagent *durable.Extension
	subagent = durable.DefineExtension(&durable.Extension{
		Name: "subagent",
		Tools: []*durable.ToolRegistration{
			durable.DefineTool(&durable.ToolRegistration{
				Name:        "subagent",
				Description: "Delegate a self-contained task to a subagent and get its answer back.",
				Parameters:  typebox.Object(props),
				Replay:      durable.ToolReplaySafe,
				Execute: func(args any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
					var in e2sfArgs
					if err := jsonx.Decode(args, &in); err != nil {
						return nil, err
					}
					rawChild, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
						tid := api.TaskId()
						existing, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &tid}, 1, nil)
						if err != nil {
							return durable.ConversationId(0), err
						}
						if len(existing.Items) > 0 {
							return existing.Items[0].Id, nil
						}
						created, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
							Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: tid},
						})
						if err != nil {
							return durable.ConversationId(0), err
						}
						if err := durable.Configure(tx, created.Id, durable.AgentChange{
							Extensions: jsonx.Some(durable.AgentExtensionsEdit(nil, []*durable.Extension{subagent})),
						}); err != nil {
							return durable.ConversationId(0), err
						}
						return created.Id, nil
					})
					if err != nil {
						return nil, err
					}
					child := rawChild.(durable.ConversationId)
					if err := api.Details(jsonx.ObjectOf("conversationId", float64(child)), callCtx); err != nil {
						return nil, err
					}
					handle, err := api.Conversation(callCtx, child)
					if err != nil {
						return nil, err
					}
					sub, err := handle.Submit(durable.InputSubmissionDraft{
						RequestId: fmt.Sprintf("subagent:%d", api.TaskId()),
						Content:   ai.UserMessageContent{Text: &in.Task},
					}, callCtx)
					if err != nil {
						return nil, err
					}
					settled, err := sub.Wait(callCtx)
					if err != nil {
						return nil, err
					}
					if settled.Status != durable.SubmissionStatusDone || settled.Type != durable.SubmissionTypeInput || settled.Answer == nil {
						return nil, fmt.Errorf("subagent failed: %s", settled.Status)
					}
					text, err := e2sfAnswerText(api, *settled.Answer, callCtx)
					if err != nil {
						return nil, err
					}
					return &durable.ToolExecutionResult{
						Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
						HasContent: true,
						Details:    jsonx.ObjectOf("conversationId", float64(child)),
						HasDetails: true,
					}, nil
				},
			}),
		},
	})

	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	toolUse := ai.StopReasonToolUse
	callId := "call-1"
	faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("subagent", jsonx.ObjectOf("task", "Name three prime numbers."), &providers.FauxToolCallOptions{Id: &callId})},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("2, 3, and 5."), nil)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("The subagent says: 2, 3, and 5."), nil)},
	})

	registry := durable.CreateRegistry()
	if err := registry.Install(subagent); err != nil {
		return err
	}
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	root, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
	})
	if err != nil {
		return err
	}

	var mu sync.Mutex
	attached := map[durable.ConversationId]bool{}
	var streams []durable.AgentEventStream
	stopAll := func() {
		mu.Lock()
		list := e2sfCloneStreams(streams)
		streams = nil
		mu.Unlock()
		for _, s := range list {
			_ = s.Stop()
		}
	}
	defer stopAll()

	printEvent := func(indent string, event durable.AgentEvent) {
		switch e := event.(type) {
		case *durable.MessageEndEvent:
			if e.Entry.Kind == durable.AssistantEntry.Kind && len(e.Entry.Model) > 0 {
				if msg, ok := e.Entry.Model[0].(*ai.AssistantMessage); ok {
					var sb strings.Builder
					for _, c := range msg.Content {
						if tc, ok := c.(*ai.TextContent); ok {
							sb.WriteString(tc.Text)
						}
					}
					if sb.Len() > 0 {
						_, _ = fmt.Fprintf(out, "%sassistant: %s\n", indent, sb.String())
					}
				}
			}
		case *durable.ToolExecutionStartEvent:
			rawArgs, _ := jsonx.Stringify(e.Args)
			_, _ = fmt.Fprintf(out, "%stool %s(%s)\n", indent, e.ToolName, rawArgs)
		}
	}

	var attach func(id durable.ConversationId, indent string) error
	attach = func(id durable.ConversationId, indent string) error {
		mu.Lock()
		if attached[id] {
			mu.Unlock()
			return nil
		}
		attached[id] = true
		mu.Unlock()

		stream, err := durable.WatchEvents(harness, id, ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		streams = append(streams, stream)
		mu.Unlock()

		return stream.Start(func(events []durable.AgentEvent, _ chord.Context) error {
			for _, ev := range events {
				printEvent(indent, ev)
				upd, ok := ev.(*durable.ToolExecutionUpdateEvent)
				if !ok {
					continue
				}
				det, has := upd.Details.Get()
				if !has {
					continue
				}
				if obj, ok := det.(*jsonx.Object); ok {
					if rawId, exists := obj.Get("conversationId"); exists {
						if fid, ok := rawId.(float64); ok {
							_ = attach(durable.ConversationId(int64(fid)), indent+"  ")
						}
					}
				}
			}
			return nil
		})
	}
	if err := attach(root.Id(), ""); err != nil {
		return err
	}

	q := "Use the subagent tool to find three prime numbers, then tell me what it said."
	sub, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := sub.Wait(ctx); err != nil {
		return err
	}
	if err := harness.WaitForIdle(ctx); err != nil {
		return err
	}
	stopAll()
	return harness.Close(ctx)
}

func e2sfCloneStreams(in []durable.AgentEventStream) []durable.AgentEventStream {
	out := make([]durable.AgentEventStream, len(in))
	copy(out, in)
	return out
}
