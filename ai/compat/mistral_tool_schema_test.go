// Ported from packages/ai/test/mistral-tool-schema.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tmtsPtr[T any](v T) *T { return &v }

func TestMistralToolSchema_StripsTypeBoxMetadataAndKeepsStrictSchema(t *testing.T) {
	base := compat.GetModel("mistral", "devstral-medium-latest")
	if base == nil {
		t.Fatal("missing mistral/devstral-medium-latest")
	}
	model := *base
	model.BaseUrl = "http://127.0.0.1:9"

	nestedProps := omap.NewMap[string, *typebox.Schema]()
	nestedProps.Set("value", typebox.String())
	rootProps := omap.NewMap[string, *typebox.Schema]()
	rootProps.Set("nested", typebox.Object(nestedProps))
	parameters := typebox.Object(rootProps)

	reqCtx := &ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Text: tmtsPtr("Hi")},
				Timestamp: js.DateNow(),
			},
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
	}

	var captured *jsonx.Object
	apiKey := "fake-key"
	response, err := compat.Complete(context.Background(), &model, reqCtx, &ai.StreamOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
			ApiKey: &apiKey,
			OnPayload: func(payload any, _ *ai.Model) (any, error) {
				if obj, ok := payload.(*jsonx.Object); ok {
					captured = obj
				}
				return payload, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected captured payload")
	}

	rawTools, _ := captured.Get("tools")
	tools, _ := rawTools.([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	tool0, _ := tools[0].(*jsonx.Object)
	if tool0 == nil {
		t.Fatalf("tools[0] type = %T, want *jsonx.Object", tools[0])
	}
	fnRaw, _ := tool0.Get("function")
	fnObj, _ := fnRaw.(*jsonx.Object)
	if fnObj == nil {
		t.Fatalf("tools[0].function type = %T", fnRaw)
	}
	if strict, _ := fnObj.Get("strict"); strict != true {
		t.Fatalf("tools[0].function.strict = %v, want true", strict)
	}

	paramsRaw, _ := fnObj.Get("parameters")
	paramsObj, _ := paramsRaw.(*jsonx.Object)
	if paramsObj == nil {
		t.Fatalf("parameters type = %T, want *jsonx.Object", paramsRaw)
	}
	propsRaw, _ := paramsObj.Get("properties")
	propsObj, _ := propsRaw.(*jsonx.Object)
	if propsObj == nil {
		t.Fatalf("parameters.properties type = %T", propsRaw)
	}
	nestedRaw, _ := propsObj.Get("nested")
	nestedObj, _ := nestedRaw.(*jsonx.Object)
	if nestedObj == nil {
		t.Fatalf("parameters.properties.nested type = %T", nestedRaw)
	}
	if _, hasSymbolKey := paramsObj.Get("~"); hasSymbolKey {
		t.Fatal("unexpected TypeBox internal symbol property on parameters")
	}
	if _, err := jsonx.Marshal(paramsObj); err != nil {
		t.Fatalf("jsonx.Marshal(parameters) failed: %v", err)
	}

	if response.StopReason != ai.StopReasonError {
		t.Fatalf("stopReason = %q, want error", response.StopReason)
	}
	if response.ErrorMessage != nil && strings.Contains(*response.ErrorMessage, "Input validation failed") {
		t.Fatalf("unexpected validation failure: %s", *response.ErrorMessage)
	}
}
