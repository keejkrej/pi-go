// Ported from packages/durable/test/examples/31-reload-and-restart.ts (pi v1.0.0).

package testkit

import (
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

func e3rarLoadVersioned(version string, onStart func(), gate <-chan struct{}) *durable.Extension {
	return durable.DefineExtension(&durable.Extension{
		Name: "versioned",
		Tools: []*durable.ToolRegistration{
			durable.DefineTool(&durable.ToolRegistration{
				Name:        "version",
				Description: "Return the extension version.",
				Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
				Execute: func(_ any, _ durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
					if onStart != nil {
						onStart()
					}
					if gate != nil {
						var abortCh <-chan struct{}
						if sig := callCtx.AbortSignal(); sig != nil {
							abortCh = sig.Done()
						}
						select {
						case <-gate:
						case <-abortCh:
							return nil, callCtx.AbortSignal().Err()
						}
					}
					return &durable.ToolExecutionResult{
						Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: version}},
						HasContent: true,
					}, nil
				},
			}),
		},
	})
}

func e3rarToolNames(agent *durable.Agent) []string {
	out := make([]string, len(agent.Tools))
	for i, tool := range agent.Tools {
		out[i] = tool.Name
	}
	return out
}

func e3rarLastToolText(conv durable.Conversation, ctx chord.Context) (string, error) {
	page, err := conv.Entries(durable.ConversationEntryQuery{}, 20, nil, ctx)
	if err != nil {
		return "", err
	}
	for _, entry := range page.Items {
		if !durable.ToolResultEntry.Is(&entry) || len(entry.Model) == 0 {
			continue
		}
		res, ok := entry.Model[0].(*ai.ToolResultMessage)
		if !ok {
			continue
		}
		var sb strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*ai.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		return sb.String(), nil
	}
	return "", nil
}

// Example31ReloadAndRestart runs the 31-reload-and-restart example.
func Example31ReloadAndRestart(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext

	directory, err := os.MkdirTemp("", "pi-durable-reload-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	storagePath := filepath.Join(directory, "session.sqlite")

	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	toolUse := ai.StopReasonToolUse
	versionCall := func(id string) providers.FauxResponseStep {
		return &providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("version", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: &id})},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)}
	}
	faux.SetResponses([]providers.FauxResponseStep{
		versionCall("v1"),
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done 1"), nil)},
		versionCall("v2"),
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done 2"), nil)},
	})

	startedCh := make(chan struct{})
	var startOnce sync.Once
	gate := make(chan struct{})

	reg1 := durable.CreateRegistry()
	if err := reg1.Install(e3rarLoadVersioned("v1", func() {
		startOnce.Do(func() { close(startedCh) })
	}, gate)); err != nil {
		return err
	}
	st1, err := sqlite.OpenNodeSqliteStorage(storagePath)
	if err != nil {
		return err
	}
	h1, err := durable.HarnessOpen(st1, durable.HarnessOptions{
		Models:   models,
		Registry: reg1,
	}, ctx)
	if err != nil {
		return err
	}
	closedH1 := false
	defer func() {
		if !closedH1 {
			_ = h1.Close(ctx)
		}
	}()

	root1, err := h1.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}),
		},
	})
	if err != nil {
		return err
	}

	p1 := "run version 1"
	sub1, err := root1.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &p1},
	}, ctx)
	if err != nil {
		return err
	}

	var ctxDone <-chan struct{}
	if sig := ctx.AbortSignal(); sig != nil {
		ctxDone = sig.Done()
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-startedCh:
	case <-ctxDone:
		return ctx.AbortSignal().Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for v1 tool execution to start")
	}

	if err := reg1.Install(e3rarLoadVersioned("v2", nil, nil)); err != nil {
		return err
	}
	close(gate)

	if _, err := sub1.Wait(ctx); err != nil {
		return err
	}
	inFlight, err := e3rarLastToolText(root1, ctx)
	if err != nil {
		return err
	}

	p2 := "run version 2"
	sub2, err := root1.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &p2},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := sub2.Wait(ctx); err != nil {
		return err
	}
	nextRun, err := e3rarLastToolText(root1, ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "in-flight: %s, next: %s\n", inFlight, nextRun)

	if err := h1.Close(ctx); err != nil {
		return err
	}
	closedH1 = true

	st2, err := sqlite.OpenNodeSqliteStorage(storagePath)
	if err != nil {
		return err
	}
	reg2 := durable.CreateRegistry()
	h2, err := durable.HarnessOpen(st2, durable.HarnessOptions{
		Models:   models,
		Registry: reg2,
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = h2.Close(ctx) }()

	root2, err := h2.Root(ctx)
	if err != nil {
		return err
	}
	agentBefore, err := root2.Agent(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "after restart before install: %v\n", e3rarToolNames(agentBefore))

	if err := reg2.Install(e3rarLoadVersioned("v3", nil, nil)); err != nil {
		return err
	}
	agentAfter, err := root2.Agent(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "after install: %v\n", e3rarToolNames(agentAfter))
	return h2.Close(ctx)
}
