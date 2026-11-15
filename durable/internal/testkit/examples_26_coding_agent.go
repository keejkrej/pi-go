// Ported from packages/durable/test/examples/26-coding-agent.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example26CodingAgent runs the 26-coding-agent example.
func Example26CodingAgent(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	workspace, err := os.MkdirTemp("", "pi-durable-agent-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	appDir := filepath.Join(workspace, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return err
	}

	coding := durable.DefineExtension(&durable.Extension{
		Name: "coding",
		Sections: []*durable.PromptSection{
			durable.Section("preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
				return "You are a coding agent. Use the tools to inspect the project.", true, nil
			}, false),
			durable.Section("cwd", func(input durable.PromptInput, _ chord.Context) (string, bool, error) {
				if input.Env == nil {
					return "", false, nil
				}
				return input.Env.Cwd(), true, nil
			}),
		},
	})
	registry := durable.CreateRegistry()
	if err := registry.Install(tools.CodingTools); err != nil {
		return err
	}
	if err := registry.Install(coding); err != nil {
		return err
	}

	var parallelTools atomic.Bool
	parallelTools.Store(true)
	maxRetries := 3

	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	toolUse := ai.StopReasonToolUse
	pwdMsg := func() *ai.AssistantMessage {
		return providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("bash", jsonx.ObjectOf("command", "pwd"), nil)},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)
	}
	faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{Message: pwdMsg()},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Done."), nil)},
		&providers.FauxResponseMessage{Message: pwdMsg()},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Done."), nil)},
	})

	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
		Settings: func() durable.HarnessSettings {
			mode := durable.ToolExecutionModeSequential
			if parallelTools.Load() {
				mode = durable.ToolExecutionModeParallel
			}
			return durable.HarnessSettings{
				ToolExecution: mode,
				Retry:         &durable.ConversationRetryPolicyPatch{MaxRetries: &maxRetries},
			}
		},
		Env: func(target durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
			cwd := workspace
			if target.Cwd != nil {
				cwd = *target.Cwd
			}
			return env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd}), nil
		},
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	root, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}),
			Cwd:   jsonx.Some(workspace),
		},
	})
	if err != nil {
		return err
	}

	lastBashOutput := func() (string, error) {
		page, err := root.Entries(durable.ConversationEntryQuery{}, 10, nil, ctx)
		if err != nil {
			return "", err
		}
		for _, entry := range page.Items {
			if durable.ToolResultEntry.Is(&entry) && len(entry.Model) > 0 {
				if tr, ok := entry.Model[0].(*ai.ToolResultMessage); ok {
					var sb strings.Builder
					for _, c := range tr.Content {
						if tc, ok := c.(*ai.TextContent); ok {
							sb.WriteString(strings.TrimSpace(tc.Text))
						}
					}
					return sb.String(), nil
				}
			}
		}
		return "", nil
	}

	q1 := "Where are we?"
	s1, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q1},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := s1.Wait(ctx); err != nil {
		return err
	}
	out1, err := lastBashOutput()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "bash ran in: %s\n", out1)

	if err := root.Configure(durable.AgentChange{Cwd: jsonx.Some(appDir)}, ctx); err != nil {
		return err
	}
	parallelTools.Store(false)
	q2 := "And now?"
	s2, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q2},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := s2.Wait(ctx); err != nil {
		return err
	}
	out2, err := lastBashOutput()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "bash ran in: %s\n", out2)
	return harness.Close(ctx)
}
