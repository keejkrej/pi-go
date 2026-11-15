// Ported from packages/ai/test/opencode-provider-headers.test.ts (pi v1.5.2).

package providers_test

import (
	"context"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tophModel() *ai.Model {
	return &ai.Model{
		Id:            "test-model",
		Name:          "Test model",
		Api:           "test-api",
		Provider:      "opencode",
		BaseUrl:       "https://opencode.ai/zen/v1",
		Reasoning:     false,
		Input:         []ai.ModelModality{"text"},
		Cost:          ai.ModelCost{},
		ContextWindow: 1000,
		MaxTokens:     100,
	}
}

func tophContext() *ai.TranscriptContext {
	hi := "hi"
	return ai.NormalizeContext(&ai.Context{
		Messages: []ai.Message{
			&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Text: &hi},
				Timestamp: 0,
			},
		},
	})
}

func tophCompletedStream() *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	msg := providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("ok")}, nil)
	stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: msg})
	stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg})
	stream.End(msg)
	return stream
}

func tophRecordingStreams(capture func(headers ai.ProviderHeaders)) ai.ProviderStreams {
	return ai.ProviderStreams{
		Stream: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, options ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			if options != nil && options.BaseStreamOptions() != nil {
				capture(options.BaseStreamOptions().Headers)
			} else {
				capture(nil)
			}
			return tophCompletedStream()
		},
		StreamSimple: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			if options != nil {
				capture(options.Headers)
			} else {
				capture(nil)
			}
			return tophCompletedStream()
		},
	}
}

func tophDrainStream(t *testing.T, s *ai.AssistantMessageEventStream) {
	t.Helper()
	if _, err := s.Result(context.Background()); err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
}

func TestOpenCodeProviderHeaders_MapsSessionIdWithoutCacheRetention(t *testing.T) {
	model := tophModel()
	tr := tophContext()
	sid := "conversation-1"
	none := ai.CacheRetentionNone

	t.Run("stream", func(t *testing.T) {
		var captured ai.ProviderHeaders
		streams := providers.WithOpenCodeSessionHeader(tophRecordingStreams(func(h ai.ProviderHeaders) {
			captured = h
		}))
		tophDrainStream(t, streams.Stream(context.Background(), model, tr, &ai.StreamOptions{
			SessionId:      &sid,
			CacheRetention: &none,
		}))
		if captured == nil {
			t.Fatalf("expected non-nil headers")
		}
		v, ok := captured.Get("x-opencode-session")
		if !ok || v == nil || *v != "conversation-1" || captured.Len() != 1 {
			t.Fatalf("got headers %v, want x-opencode-session=conversation-1", captured)
		}
	})

	t.Run("streamSimple", func(t *testing.T) {
		var captured ai.ProviderHeaders
		streams := providers.WithOpenCodeSessionHeader(tophRecordingStreams(func(h ai.ProviderHeaders) {
			captured = h
		}))
		tophDrainStream(t, streams.StreamSimple(context.Background(), model, tr, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				SessionId:      &sid,
				CacheRetention: &none,
			},
		}))
		if captured == nil {
			t.Fatalf("expected non-nil headers")
		}
		v, ok := captured.Get("x-opencode-session")
		if !ok || v == nil || *v != "conversation-1" || captured.Len() != 1 {
			t.Fatalf("got headers %v, want x-opencode-session=conversation-1", captured)
		}
	})
}

func TestOpenCodeProviderHeaders_PreservesCaseInsensitiveCallerOverride(t *testing.T) {
	model := tophModel()
	tr := tophContext()
	sid := "generated-value"
	callerVal := "caller-value"

	cases := []struct {
		name string
		val  *string
	}{
		{name: "override_value", val: &callerVal},
		{name: "override_nil", val: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured ai.ProviderHeaders
			streams := providers.WithOpenCodeSessionHeader(tophRecordingStreams(func(h ai.ProviderHeaders) {
				captured = h
			}))
			headers := omap.NewMap[string, *string]()
			headers.Set("X-OpenCode-Session", tc.val)
			tophDrainStream(t, streams.StreamSimple(context.Background(), model, tr, &ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					SessionId: &sid,
					Headers:   headers,
				},
			}))
			if captured == nil || captured.Len() != 1 {
				t.Fatalf("expected exactly 1 header, got %v", captured)
			}
			v, ok := captured.Get("X-OpenCode-Session")
			if !ok {
				t.Fatalf("expected X-OpenCode-Session key")
			}
			if tc.val == nil {
				if v != nil {
					t.Fatalf("expected nil header override, got %q", *v)
				}
			} else if v == nil || *v != *tc.val {
				t.Fatalf("expected %q, got %v", *tc.val, v)
			}
		})
	}
}

func TestOpenCodeProviderHeaders_DoesNotFabricateWhenSessionIdAbsent(t *testing.T) {
	model := tophModel()
	tr := tophContext()
	var captured ai.ProviderHeaders
	streams := providers.WithOpenCodeSessionHeader(tophRecordingStreams(func(h ai.ProviderHeaders) {
		captured = h
	}))
	headers := omap.NewMap[string, *string]()
	val := "value"
	headers.Set("x-custom", &val)
	tophDrainStream(t, streams.StreamSimple(context.Background(), model, tr, &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			Headers: headers,
		},
	}))
	if captured == nil || captured.Len() != 1 {
		t.Fatalf("expected 1 header, got %v", captured)
	}
	got, ok := captured.Get("x-custom")
	if !ok || got == nil || *got != "value" {
		t.Fatalf("expected x-custom=value, got %v", got)
	}
}
