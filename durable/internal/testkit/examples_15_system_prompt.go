// Ported from packages/durable/test/examples/15-system-prompt.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example15SystemPrompt runs the 15-system-prompt example.
func Example15SystemPrompt(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Done."), nil)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Done."), nil)},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Done."), nil)},
	})

	coding := durable.DefineExtension(&durable.Extension{
		Name: "coding",
		Sections: []*durable.PromptSection{
			durable.Section("preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
				return "You are a coding agent.", true, nil
			}, false),
			durable.Section("cwd", func(input durable.PromptInput, _ chord.Context) (string, bool, error) {
				if input.Env == nil {
					return "", false, nil
				}
				return input.Env.Cwd(), true, nil
			}),
		},
	})
	agentsMd := durable.DefineExtension(&durable.Extension{
		Name: "agents-md",
		Sections: []*durable.PromptSection{
			durable.Section("agents_md", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
				return "Run npm run check after changes.", true, nil
			}),
		},
	})
	terse := durable.DefineExtension(&durable.Extension{
		Name: "terse",
		Wraps: []durable.Wrap{
			durable.WrapSection("preamble", func(preamble *durable.PromptSection) *durable.PromptSection {
				return &durable.PromptSection{
					Key: preamble.Key,
					Tag: preamble.Tag,
					Render: func(input durable.PromptInput, renderCtx chord.Context) (string, bool, error) {
						base, ok, err := preamble.Render(input, renderCtx)
						if err != nil || !ok {
							return base, ok, err
						}
						return base + " Be terse.", true, nil
					},
				}
			}),
		},
	})

	registry := durable.CreateRegistry()
	for _, ext := range []*durable.Extension{coding, agentsMd, terse} {
		if err := registry.Install(ext); err != nil {
			return err
		}
	}
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
		Env: func(target durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
			cwd := "/"
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

	model := durable.ModelRef{Provider: "faux", ModelId: "faux-1"}
	repoCwd := "/repo"
	root, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{Model: jsonx.Some(model), Cwd: jsonx.Some(repoCwd)},
	})
	if err != nil {
		return err
	}
	subagent, err := harness.CreateConversation(durable.ConversationCreateOptions{
		Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		Agent: &durable.AgentChange{
			Model:        jsonx.Some(model),
			Cwd:          jsonx.Some(repoCwd),
			Extensions:   jsonx.Some(durable.AgentExtensionsEdit(nil, []*durable.Extension{agentsMd})),
			Instructions: jsonx.Some("Only read; never edit files."),
		},
	}, ctx)
	if err != nil {
		return err
	}

	systemEntries := func(conv durable.Conversation) ([]ai.Message, error) {
		page, err := conv.Entries(durable.ConversationEntryQuery{}, 20, nil, ctx)
		if err != nil {
			return nil, err
		}
		items := slices.Clone(page.Items)
		slices.Reverse(items)
		var outMsgs []ai.Message
		for _, entry := range items {
			if durable.SystemEntry.Is(&entry) {
				outMsgs = append(outMsgs, entry.Model...)
			}
		}
		return outMsgs, nil
	}

	for _, pair := range []struct {
		conv durable.Conversation
		text string
	}{
		{root, "Fix the build."},
		{subagent, "Read the logs."},
	} {
		t := pair.text
		sub, err := pair.conv.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: ai.UserMessageContent{Text: &t},
		}, ctx)
		if err != nil {
			return err
		}
		if _, err := sub.Wait(ctx); err != nil {
			return err
		}
	}

	rSys, err := systemEntries(root)
	if err != nil {
		return err
	}
	sSys, err := systemEntries(subagent)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "root system prompt: %d\nsubagent system prompt: %d\n", len(rSys), len(sSys))

	if err := root.Configure(durable.AgentChange{Cwd: jsonx.Some("/repo/packages")}, ctx); err != nil {
		return err
	}
	t3 := "Now the package."
	s3, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &t3},
	}, ctx)
	if err != nil {
		return err
	}
	if _, err := s3.Wait(ctx); err != nil {
		return err
	}
	rSysAfter, err := systemEntries(root)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "root system entries after cwd change: %d\n", len(rSysAfter))
	return harness.Close(ctx)
}
