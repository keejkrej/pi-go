// Ported from packages/durable/test/examples/30-tool-override.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type e3toTiming struct {
	command string
	ms      int64
}

func e3toLastToolResult(conv durable.Conversation, ctx chord.Context) (string, error) {
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

// Example30ToolOverride runs the 30-tool-override example.
func Example30ToolOverride(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext

	project, err := os.MkdirTemp("", "pi-durable-venv-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(project) }()

	venvBin := filepath.Join(project, ".venv", "bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		return err
	}
	activate := fmt.Sprintf("export VIRTUAL_ENV=%q\n", filepath.Join(project, ".venv"))
	if err := os.WriteFile(filepath.Join(venvBin, "activate"), []byte(activate), 0o600); err != nil {
		return err
	}

	venv := durable.DefineExtension(&durable.Extension{
		Name: "venv",
		Tools: []*durable.ToolRegistration{
			tools.CreateBashTool(&tools.BashToolOptions{
				CommandPrefix: "source .venv/bin/activate",
			}),
		},
	})

	var mu sync.Mutex
	var timings []e3toTiming

	baseBash := tools.CreateBashTool()
	timing := durable.DefineExtension(&durable.Extension{
		Name: "timing",
		Wraps: []durable.Wrap{
			durable.WrapTool(baseBash, func(bash *durable.ToolRegistration) *durable.ToolRegistration {
				cp := *bash
				orig := bash.Execute
				cp.Execute = func(args any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
					start := time.Now()
					res, execErr := orig(args, api, callCtx)
					elapsed := time.Since(start).Milliseconds()
					var in tools.BashToolInput
					cmd := ""
					if err := jsonx.Decode(args, &in); err == nil {
						cmd = in.Command
					}
					mu.Lock()
					timings = append(timings, e3toTiming{command: cmd, ms: elapsed})
					mu.Unlock()
					return res, execErr
				}
				return &cp
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
	bashStep := func(command, id string) providers.FauxResponseStep {
		return &providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("bash", jsonx.ObjectOf("command", command), &providers.FauxToolCallOptions{Id: &id})},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)}
	}
	faux.SetResponses([]providers.FauxResponseStep{
		bashStep("echo venv: $VIRTUAL_ENV", "b1"),
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("checked plain"), nil)},
		bashStep("echo venv: $VIRTUAL_ENV", "b2"),
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("checked python"), nil)},
	})

	registry := durable.CreateRegistry()
	if err := registry.Install(tools.CodingTools); err != nil {
		return err
	}
	if err := registry.Install(timing); err != nil {
		return err
	}

	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
		Env: func(target durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
			cwd := project
			if target.Cwd != nil && *target.Cwd != "" {
				cwd = *target.Cwd
			}
			return env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd}), nil
		},
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	model := durable.ModelRef{Provider: "faux", ModelId: "faux-1"}
	plain, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(model),
			Cwd:   jsonx.Some(project),
		},
	})
	if err != nil {
		return err
	}

	python, err := harness.CreateConversation(durable.ConversationCreateOptions{
		Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		Agent: &durable.AgentChange{
			Model:      jsonx.Some(model),
			Cwd:        jsonx.Some(project),
			Extensions: jsonx.Some(durable.AgentExtensionsEdit([]*durable.Extension{venv}, nil)),
		},
	}, ctx)
	if err != nil {
		return err
	}

	prompt := "check VIRTUAL_ENV"
	sub1, err := plain.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &prompt},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := sub1.Wait(ctx); err != nil {
		return err
	}
	plainOut, err := e3toLastToolResult(plain, ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "plain: %s\n", strings.TrimSpace(plainOut))

	sub2, err := python.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &prompt},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := sub2.Wait(ctx); err != nil {
		return err
	}
	pythonOut, err := e3toLastToolResult(python, ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "python: %s\n", strings.TrimSpace(pythonOut))

	mu.Lock()
	recorded := slices.Clone(timings)
	mu.Unlock()
	_, _ = fmt.Fprintf(out, "timings: %d %v\n", len(recorded), recorded)
	return harness.Close(ctx)
}
