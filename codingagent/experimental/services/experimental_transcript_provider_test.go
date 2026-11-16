// Ported from packages/coding-agent/test/experimental-transcript-provider.test.ts (pi v1.0.0).

package services_test

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/durable"
)

func TestTranscriptServiceReplicatesConversationViewAsItChanges(t *testing.T) {
	fc, err := testkit.OpenFauxConversation(providers.FauxResponseMessage{
		Message: providers.FauxAssistantMessage(providers.FauxAssistantText("answer"), nil),
	})
	if err != nil {
		t.Fatalf("OpenFauxConversation failed: %v", err)
	}
	defer func() { _ = fc.Close() }()

	transcriptFacet, err := services.CreateTranscriptServiceFacet(fc.Conversation, chord.BackgroundContext)
	if err != nil {
		t.Fatalf("CreateTranscriptServiceFacet failed: %v", err)
	}

	var mu sync.Mutex
	var views []durable.ConversationView
	consumer := chord.DefineFacet(chord.Facet{
		Id: "test-transcript-consumer",
		Setup: func(env chord.FacetEnvironment) error {
			transcript := services.AsTranscript(env.Use(services.TranscriptService))
			env.OnActivate(func() error {
				unsub := transcript.State().Subscribe(func(value durable.ConversationView, _ chord.Context, _ chord.ReplicatedStateDelivery) error {
					mu.Lock()
					views = append(views, value)
					mu.Unlock()
					return nil
				})
				env.Own(func() error {
					unsub()
					return nil
				})
				return nil
			})
			return nil
		},
	})

	host, err := chord.CreateFacetHost(&chord.FacetOptions{
		Facets: []chord.Facet{transcriptFacet, consumer},
	})
	if err != nil {
		t.Fatalf("CreateFacetHost failed: %v", err)
	}
	defer func() { _ = host.Dispose() }()

	mu.Lock()
	if len(views) == 0 || len(views[0].Entries) != 0 {
		t.Fatalf("expected initial view with empty entries, got %+v", views)
	}
	mu.Unlock()

	msg := "question"
	sub, err := fc.Conversation.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: durable.UserInput{Text: &msg},
	}, chord.BackgroundContext)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if _, err := sub.Wait(chord.BackgroundContext); err != nil {
		t.Fatalf("submission Wait failed: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		var lastKinds []string
		if len(views) > 0 {
			last := views[len(views)-1]
			for _, e := range last.Entries {
				lastKinds = append(lastKinds, e.Kind)
			}
		}
		mu.Unlock()
		if reflect.DeepEqual(lastKinds, []string{"pi.user", "pi.assistant"}) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("timed out waiting for [pi.user pi.assistant] entries, got %+v", views)
}
