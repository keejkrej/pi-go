// Ported from packages/durable/test/examples/19-json.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example19Json runs the 19-json example.
func Example19Json(out io.Writer) error {
	sw := NewSyncWriter(out)
	defer sw.Close()
	out = sw
	ctx := TestContext
	dir, err := os.MkdirTemp("", "pi-durable-json-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi\n"), 0o600); err != nil {
		return err
	}
	st, err := sqlite.OpenNodeSqliteStorage(filepath.Join(dir, "session.sqlite"))
	if err != nil {
		return err
	}

	tps := 200.0
	faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{TokensPerSecond: &tps})
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	toolUse := ai.StopReasonToolUse
	callId := "call-1"
	faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("bash", jsonx.ObjectOf("command", "ls"), &providers.FauxToolCallOptions{Id: &callId})},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantText("This directory holds the durable package sources, tests, and docs."), nil,
		)},
	})

	registry := durable.CreateRegistry()
	if err := registry.Install(durable.DefineExtension(&durable.Extension{
		Name:  "coding",
		Tools: []*durable.ToolRegistration{tools.CreateReadTool(), tools.CreateBashTool()},
		Sections: []*durable.PromptSection{
			durable.Section("preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
				return "You are a concise coding assistant.", true, nil
			}, false),
		},
	})); err != nil {
		return err
	}
	execEnv := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: dir})
	harness, err := durable.HarnessOpen(st, durable.HarnessOptions{
		Models:   models,
		Registry: registry,
		Env:      func(_ durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) { return execEnv, nil },
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

	printJSON := func(v any) {
		raw, err := jsonx.Stringify(v)
		if err == nil {
			_, _ = fmt.Fprintln(out, raw)
		}
	}

	stream, err := durable.WatchEvents(harness, root.Id(), ctx)
	if err != nil {
		return err
	}
	printJSON(stream.Snapshot())
	if err := stream.Start(func(events []durable.AgentEvent, _ chord.Context) error {
		for _, event := range events {
			printJSON(event)
		}
		return nil
	}); err != nil {
		return err
	}
	defer func() { _ = stream.Stop() }()

	prompt := "What is in this directory?"
	submission, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &prompt},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := submission.Wait(ctx); err != nil {
		return err
	}
	if err := harness.WaitForIdle(ctx); err != nil {
		return err
	}
	_ = stream.Stop()
	return harness.Close(ctx)
}
