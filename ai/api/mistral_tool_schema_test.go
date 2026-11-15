// Ported from packages/ai/test/mistral-tool-schema.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tmtsStrPtr(s string) *string { return &s }

func TestMistralToolSchemaStrictSerialization(t *testing.T) {
	var seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seenBody = string(raw)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"bad request"}`)
	}))
	defer srv.Close()

	nestedProps := omap.NewMap[string, *typebox.Schema]()
	nestedProps.Set("value", typebox.String())
	outerProps := omap.NewMap[string, *typebox.Schema]()
	outerProps.Set("nested", typebox.Object(nestedProps))
	parameters := typebox.Object(outerProps)

	model := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "devstral-medium-latest",
			Name:     "devstral-medium-latest",
			Api:      string(ai.KnownApiMistralConversations),
			Provider: "mistral",
			BaseUrl:  srv.URL,
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 128000,
		MaxTokens:     16384,
	}

	var capturedPayload *jsonx.Object
	transcript := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tmtsStrPtr("Hi")}, Timestamp: 1},
		},
		Tools: []*ai.Tool{
			{
				Name:        "inspect_schema",
				Description: "Inspect the schema",
				Parameters:  parameters,
				ConstrainedSampling: ai.ToolConstrainedSamplingValue{
					V: &ai.ConstrainedSamplingJsonSchema{
						Type:   "json_schema",
						Strict: ai.ConstrainedSamplingStrictRequire,
					},
				},
			},
		},
	})

	msg, err := api.StreamMistralConversations(context.Background(), model, transcript, &api.MistralOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey: tmtsStrPtr("fake-key"),
				OnPayload: func(payload any, m *ai.Model) (any, error) {
					capturedPayload = payload.(*jsonx.Object).Clone()
					return payload, nil
				},
			},
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if msg.StopReason != ai.StopReasonError {
		t.Fatalf("expected error response, got %+v", msg)
	}
	if capturedPayload == nil {
		t.Fatal("expected capturedPayload")
	}
	toolsArr, _ := capturedPayload.GetArray("tools")
	if len(toolsArr) != 1 {
		t.Fatalf("expected 1 serialized tool, got %d", len(toolsArr))
	}
	fnObj, _ := toolsArr[0].(*jsonx.Object).GetObject("function")
	if strict, ok := fnObj.GetBool("strict"); !ok || !strict {
		t.Fatalf("expected strict=true on tool function, got %v", fnObj)
	}
	if params, _ := fnObj.GetObject("parameters"); params == nil || !params.Has("properties") {
		t.Fatalf("expected serialized parameters object, got %v", fnObj)
	}
	if seenBody == "" {
		t.Fatal("expected non-empty request body")
	}
}
