// Ported from packages/coding-agent/test/experimental-durable-support.ts (pi v1.0.0).

package testkit

import (
	"context"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// FauxConversation holds an in-memory durable Harness and root Conversation backed by a Faux provider.
type FauxConversation struct {
	Faux         *providers.FauxProviderHandle
	Harness      durable.Harness
	Conversation durable.Conversation
}

// Close closes the underlying durable Harness.
func (f *FauxConversation) Close() error {
	if f == nil || f.Harness == nil {
		return nil
	}
	return f.Harness.Close(chord.BackgroundContext)
}

// OpenFauxConversation opens an in-memory durable Harness whose root conversation answers with the faux provider.
func OpenFauxConversation(responses ...providers.FauxResponseStep) (*FauxConversation, error) {
	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return nil, err
	}
	faux.SetResponses(responses)

	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)

	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: durable.CreateRegistry(),
	}, chord.BackgroundContext)
	if err != nil {
		return nil, err
	}

	model := faux.GetModel()
	conversation, err := harness.Root(chord.BackgroundContext, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{
				Provider: string(model.Provider),
				ModelId:  model.Id,
			}),
		},
	})
	if err != nil {
		_ = harness.Close(chord.BackgroundContext)
		return nil, err
	}

	return &FauxConversation{
		Faux:         faux,
		Harness:      harness,
		Conversation: conversation,
	}, nil
}

// PendingFauxResponse represents a faux response step that blocks until its request context is cancelled.
type PendingFauxResponse struct {
	Step    providers.FauxResponseStep
	Reached <-chan struct{}
}

// PendingResponse returns a faux response that waits until its generation is aborted.
func PendingResponse() PendingFauxResponse {
	reached := make(chan struct{})
	var once sync.Once
	step := providers.FauxResponseFactory(func(ctx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
		once.Do(func() { close(reached) })
		<-ctx.Done()
		return nil, ctx.Err()
	})
	return PendingFauxResponse{
		Step:    step,
		Reached: reached,
	}
}
