// Ported from packages/coding-agent/src/experimental/services/transcript-provider.ts (pi v1.0.0).

package services

import (
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
)

// CreateTranscriptServiceFacet serves the conversation's durable view state. The facet owns and disposes the state.
func CreateTranscriptServiceFacet(conversation durable.Conversation, ctx chord.Context) (chord.Facet, error) {
	if ctx == nil {
		ctx = chord.BackgroundContext
	}
	state, err := conversation.ViewState(ctx)
	if err != nil {
		return chord.Facet{}, err
	}
	initial, _ := state.Value()
	initialJson, err := connEncodeJsonValue(initial)
	if err != nil {
		state.Dispose()
		return chord.Facet{}, err
	}
	repState, err := chord.NewReplicatedState(initialJson)
	if err != nil {
		state.Dispose()
		return chord.Facet{}, err
	}
	return chord.DefineFacet(chord.Facet{
		Id: "@pi/transcript",
		Setup: func(env chord.FacetEnvironment) error {
			env.Own(func() error {
				state.Dispose()
				return nil
			})
			env.Provide(TranscriptService, ProvideTranscript(repState))
			env.OnActivate(func() error {
				unsub := state.Subscribe(func(value durable.ConversationView, call chord.Context, _ chord.ReplicatedStateDelivery) error {
					encoded, err := connEncodeJsonValue(value)
					if err != nil {
						return err
					}
					if call == nil {
						call = chord.BackgroundContext
					}
					return repState.Replace(call, encoded)
				})
				env.Own(func() error {
					unsub()
					return nil
				})
				return nil
			})
			return nil
		},
	}), nil
}
