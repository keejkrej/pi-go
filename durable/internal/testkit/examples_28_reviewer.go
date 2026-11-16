// Ported from packages/durable/test/examples/28-reviewer.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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

func e2rTextOf(msg *ai.AssistantMessage) string {
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

// Example28Reviewer runs the 28-reviewer example.
func Example28Reviewer(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	const doneMarker = "No further findings."
	reviewerExt := durable.DefineExtension(&durable.Extension{
		Name: "reviewer",
		Sections: []*durable.PromptSection{
			durable.Section("role", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
				return "You review diffs. Report problems as a list. Never edit files.", true, nil
			}),
		},
		Hooks: []durable.HookRegistration{
			durable.Hook(durable.GenerationTask, durable.GenerationHooks{
				OnYield: func(answer *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) (*durable.GenerationYieldDecision, error) {
					if strings.Contains(e2rTextOf(answer), doneMarker) {
						return nil, nil
					}
					cont := fmt.Sprintf("Look again for anything you missed. Say %q when there is nothing left.", doneMarker)
					return &durable.GenerationYieldDecision{Continue: ai.UserMessageContent{Text: &cont}}, nil
				},
			}),
		},
	})

	worktree, err := os.MkdirTemp("", "pi-durable-review-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(worktree) }()
	if err := os.WriteFile(filepath.Join(worktree, "user.ts"), []byte("export const name = (user) => user.name;\n"), 0o600); err != nil {
		return err
	}

	faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{{Id: "big"}, {Id: "small"}},
	})
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	toolUse := ai.StopReasonToolUse
	faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("read", jsonx.ObjectOf("path", "user.ts"), nil)},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("1. `name` does not handle a missing user."), nil)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("2. `user` has no type. "+doneMarker), nil)},
	})

	registry := durable.CreateRegistry()
	if err := registry.Install(tools.CodingTools); err != nil {
		return err
	}
	if err := registry.Install(reviewerExt); err != nil {
		return err
	}
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
		StaticSettings: &durable.HarnessSettings{
			Extensions:    []*durable.Extension{tools.CodingTools},
			HasExtensions: true,
		},
		Env: func(target durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
			cwd := worktree
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

	if _, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "big"})},
	}); err != nil {
		return err
	}

	reviewer, err := harness.CreateConversation(durable.ConversationCreateOptions{
		Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		Agent: &durable.AgentChange{
			Model:      jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "small"}),
			Extensions: jsonx.Some(durable.AgentExtensionsSet(tools.CodingTools, reviewerExt)),
			Tools:      jsonx.Some(durable.AgentToolsSet(tools.CreateReadTool())),
			Cwd:        jsonx.Some(worktree),
		},
	}, ctx)
	if err != nil {
		return err
	}
	ag, err := reviewer.Agent(ctx)
	if err != nil {
		return err
	}
	extNames := make([]string, len(ag.Extensions))
	for i, e := range ag.Extensions {
		extNames[i] = e.Name
	}
	toolNames := make([]string, len(ag.Tools))
	for i, t := range ag.Tools {
		toolNames[i] = t.Name
	}
	modelId := ""
	if ag.Model != nil {
		modelId = ag.Model.ModelId
	}
	_, _ = fmt.Fprintf(out, "reviewer: %s %v %v %v\n", modelId, extNames, toolNames, ag.Cwd != nil && *ag.Cwd == worktree)

	q := "Review user.ts."
	sub, err := reviewer.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := sub.Wait(ctx); err != nil {
		return err
	}
	page, err := reviewer.Entries(durable.ConversationEntryQuery{}, 20, nil, ctx)
	if err != nil {
		return err
	}
	items := slices.Clone(page.Items)
	slices.Reverse(items)
	for _, entry := range items {
		if len(entry.Model) == 0 {
			continue
		}
		if u, ok := entry.Model[0].(*ai.UserMessage); ok && u.Content.Text != nil {
			_, _ = fmt.Fprintf(out, "> %s\n", *u.Content.Text)
		}
		if durable.AssistantEntry.Is(&entry) {
			if am, ok := entry.Model[0].(*ai.AssistantMessage); ok && e2rTextOf(am) != "" {
				_, _ = fmt.Fprintf(out, "reviewer: %s\n", e2rTextOf(am))
			}
		}
	}
	return harness.Close(ctx)
}
