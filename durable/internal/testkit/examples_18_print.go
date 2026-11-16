// Ported from packages/durable/test/examples/18-print.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example18Print runs the 18-print example.
func Example18Print(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	dir, err := os.MkdirTemp("", "pi-durable-print-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("durable\n"), 0o600); err != nil {
		return err
	}

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
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
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

	prompt := "What is in this directory?"
	submission, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &prompt},
	}, ctx)
	if err != nil {
		return err
	}
	settled, err := submission.Wait(ctx)
	if err != nil {
		return err
	}
	if settled.Status == durable.SubmissionStatusDone && settled.Type == durable.SubmissionTypeInput && settled.Answer != nil {
		raw, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.TypedEntry(durable.AssistantEntry, *settled.Answer)
		})
		if err != nil {
			return err
		}
		entry := raw.(*durable.EntryRecord)
		answer := entry.Model[0].(*ai.AssistantMessage)
		var sb strings.Builder
		for _, c := range answer.Content {
			if tc, ok := c.(*ai.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		_, _ = fmt.Fprintln(out, sb.String())
	} else {
		reason := string(settled.Status)
		if settled.Reason != nil {
			reason = *settled.Reason
		}
		return fmt.Errorf("unanswered: %s", reason)
	}
	return harness.Close(ctx)
}
