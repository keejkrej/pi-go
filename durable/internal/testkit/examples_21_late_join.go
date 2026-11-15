// Ported from packages/durable/test/examples/21-late-join.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// Example21LateJoin runs the 21-late-join example.
func Example21LateJoin(out io.Writer) error {
	sw := NewSyncWriter(out)
	defer sw.Close()
	out = sw
	ctx := TestContext
	halfway := make(chan struct{})
	var halfwayOnce sync.Once
	releaseTool := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseTool) })

	registry := durable.CreateRegistry()
	if err := registry.Install(durable.DefineExtension(&durable.Extension{
		Name: "count",
		Tools: []*durable.ToolRegistration{
			durable.DefineTool(&durable.ToolRegistration{
				Name:        "count",
				Description: "Counts to ten",
				Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
				Execute: func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
					for n := 1; n <= 10; n++ {
						api.Output(fmt.Sprintf("%d\n", n))
						if n == 5 {
							halfwayOnce.Do(func() { close(halfway) })
						}
						timer := time.NewTimer(15 * time.Millisecond)
						select {
						case <-timer.C:
						case <-callCtx.AbortSignal().Done():
							timer.Stop()
							return nil, callCtx.AbortSignal().Err()
						}
					}
					select {
					case <-releaseTool:
					case <-callCtx.AbortSignal().Done():
						return nil, callCtx.AbortSignal().Err()
					}
					return &durable.ToolExecutionResult{HasContent: true}, nil
				},
			}),
		},
	})); err != nil {
		return err
	}

	tps := 80.0
	one := 1
	faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{
		TokensPerSecond: &tps,
		TokenSize:       &providers.FauxTokenSizeOptions{Min: &one, Max: &one},
	})
	if err != nil {
		return err
	}
	toolUse := ai.StopReasonToolUse
	callId := "call-1"
	faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("count", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: &callId})},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantText("Counted to ten, and this answer streams slowly."), nil,
		)},
	})
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
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

	q := "Count to ten, then tell me."
	submission, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q},
	}, ctx)
	if err != nil {
		return err
	}
	<-halfway

	view, err := root.ViewState(ctx)
	if err != nil {
		return err
	}
	defer view.Dispose()
	viewVal, _ := view.Value()
	var live durable.LiveState
	if liveObj, ok := viewVal.Docs["pi.live"]; ok && liveObj != nil {
		_ = jsonx.Decode(liveObj, &live)
	}
	entryKinds := make([]string, len(viewVal.Entries))
	for i, e := range viewVal.Entries {
		entryKinds[i] = e.Kind
	}
	_, _ = fmt.Fprintf(out, "view entries: %v\n", entryKinds)
	if len(live.Tools) > 0 {
		outStr := ""
		if live.Tools[0].Output != nil {
			outStr = *live.Tools[0].Output
		}
		_, _ = fmt.Fprintf(out, "view tool slot: %s %q\n", live.Tools[0].Status, outStr)
	}
	stopView := view.Subscribe(func(val durable.ConversationView, _ chord.Context, _ chord.ReplicatedStateDelivery) error {
		var l durable.LiveState
		if liveObj, ok := val.Docs["pi.live"]; ok && liveObj != nil {
			_ = jsonx.Decode(liveObj, &l)
		}
		if len(l.Tools) > 0 && l.Tools[0].Status == durable.ToolSlotRunning {
			outStr := ""
			if l.Tools[0].Output != nil {
				outStr = *l.Tools[0].Output
			}
			_, _ = fmt.Fprintf(out, "view output now: %q\n", outStr)
		}
		return nil
	})
	defer stopView()

	stream, err := durable.WatchEvents(harness, root.Id(), ctx)
	if err != nil {
		return err
	}
	snap := stream.Snapshot()
	snapTools := make([]string, len(snap.Tools))
	for i, slot := range snap.Tools {
		snapTools[i] = fmt.Sprintf("%s %s", slot.Name, slot.Status)
	}
	_, _ = fmt.Fprintf(out, "snapshot tools: %v\n", snapTools)
	output := ""
	if len(snap.Tools) > 0 && snap.Tools[0].Output != nil {
		output = *snap.Tools[0].Output
	}
	var outMu sync.Mutex
	if err := stream.Start(func(events []durable.AgentEvent, _ chord.Context) error {
		outMu.Lock()
		defer outMu.Unlock()
		for _, event := range events {
			switch e := event.(type) {
			case *durable.ToolExecutionUpdateEvent:
				if e.Output != nil {
					if e.Output.Set != nil {
						output = *e.Output.Set
					} else {
						trim := 0
						if e.Output.TrimStart != nil && *e.Output.TrimStart > 0 && *e.Output.TrimStart <= len(output) {
							trim = *e.Output.TrimStart
						}
						app := ""
						if e.Output.Append != nil {
							app = *e.Output.Append
						}
						output = output[trim:] + app
					}
					_, _ = fmt.Fprintf(out, "event output now: %q\n", output)
				}
			case *durable.MessageUpdateEvent:
				var sb strings.Builder
				for _, ch := range e.Changes {
					if ch.Type == "text_delta" && ch.Delta != nil {
						sb.WriteString(*ch.Delta)
					}
				}
				_, _ = fmt.Fprintf(out, "event text delta: %q\n", sb.String())
			default:
				_, _ = fmt.Fprintf(out, "event: %s\n", event.AgentEventType())
			}
		}
		return nil
	}); err != nil {
		return err
	}
	releaseOnce.Do(func() { close(releaseTool) })

	if _, err := submission.Wait(ctx); err != nil {
		return err
	}
	if err := harness.WaitForIdle(ctx); err != nil {
		return err
	}
	_ = stream.Stop()
	stopView()
	view.Dispose()
	return harness.Close(ctx)
}
