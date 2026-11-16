// Ported from packages/durable/test/examples/14-chat.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example14Chat runs the 14-chat example.
func Example14Chat(out io.Writer) error {
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
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Paris."), nil)},
	})

	registry := durable.CreateRegistry()
	if err := registry.Install(durable.DefineExtension(&durable.Extension{
		Name: "terse",
		Sections: []*durable.PromptSection{
			durable.Section("preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
				return "You answer in one word.", true, nil
			}, false),
		},
	})); err != nil {
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

	q := "Capital of France?"
	capital, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q},
	}, ctx)
	if err != nil {
		return err
	}
	answered, err := capital.Wait(ctx)
	if err != nil {
		return err
	}
	if answered.Status == durable.SubmissionStatusDone && answered.Type == durable.SubmissionTypeInput && answered.Answer != nil {
		rawEntry, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.TypedEntry(durable.AssistantEntry, *answered.Answer)
		})
		if err != nil {
			return err
		}
		if entry, ok := rawEntry.(*durable.EntryRecord); ok && entry != nil && len(entry.Model) > 0 {
			if reply, ok := entry.Model[0].(*ai.AssistantMessage); ok {
				_, _ = fmt.Fprintf(out, "answer: %v\n", reply.Content)
			}
		}
	}

	transcript, err := root.Entries(durable.ConversationEntryQuery{}, 10, nil, ctx)
	if err != nil {
		return err
	}
	items := slices.Clone(transcript.Items)
	slices.Reverse(items)
	kinds := make([]string, len(items))
	for i, entry := range items {
		kinds[i] = entry.Kind
	}
	_, _ = fmt.Fprintf(out, "transcript: %v\n", kinds)
	return harness.Close(ctx)
}
