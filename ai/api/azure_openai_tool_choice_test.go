package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type tatcRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f tatcRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func tatcStrPtr(s string) *string { return &s }

func TestAzureOpenAIToolChoice(t *testing.T) {
	model := &ai.Model{
		Id:            "gpt-5.2",
		Name:          "GPT-5.2 Azure",
		Api:           string(ai.KnownApiAzureOpenaiResponses),
		Provider:      "azure-openai-responses",
		BaseUrl:       "https://my-resource.openai.azure.com/openai/v1",
		Reasoning:     false,
		Input:         []ai.ModelModality{ai.ModelModalityText},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
	userText := "check weather"
	toolProps := omap.NewMap[string, *typebox.Schema]()
	toolProps.Set("city", typebox.String())
	ctx := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &userText}, Timestamp: 1},
		},
		Tools: []*ai.Tool{
			{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters:  typebox.Object(toolProps),
			},
		},
	})

	newClient := func(captured *map[string]any) *http.Client {
		return &http.Client{
			Transport: tatcRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(req.Body)
				_ = json.Unmarshal(raw, captured)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")),
				}, nil
			}),
		}
	}

	t.Run("forwards toolChoice via StreamAzureOpenaiResponses", func(t *testing.T) {
		var body map[string]any
		res, err := StreamAzureOpenaiResponses(context.Background(), model, ctx, &AzureOpenAIResponsesOptions{
			AzureEndpointOptions: AzureEndpointOptions{
				StreamOptions: ai.StreamOptions{ApiKey: tatcStrPtr("k"), HTTPClient: newClient(&body), Env: ai.ProviderEnv{}},
			},
			ToolChoice: "required",
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("Result error: %v", err)
		}
		if res.StopReason != ai.StopReasonStop {
			t.Fatalf("unexpected stop: %s (%v)", res.StopReason, res.ErrorMessage)
		}
		if body["tool_choice"] != "required" {
			t.Fatalf("expected tool_choice=required, got %v", body["tool_choice"])
		}
	})

	t.Run("forwards ToolChoice via StreamSimpleAzureOpenaiResponses", func(t *testing.T) {
		var body map[string]any
		tc := ai.ToolChoiceNone
		res, err := StreamSimpleAzureOpenaiResponses(context.Background(), model, ctx, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: tatcStrPtr("k"), HTTPClient: newClient(&body), Env: ai.ProviderEnv{}},
			ToolChoice:    &tc,
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("Result error: %v", err)
		}
		if res.StopReason != ai.StopReasonStop {
			t.Fatalf("unexpected stop: %s (%v)", res.StopReason, res.ErrorMessage)
		}
		if body["tool_choice"] != "none" {
			t.Fatalf("expected tool_choice=none, got %v", body["tool_choice"])
		}
	})
}
