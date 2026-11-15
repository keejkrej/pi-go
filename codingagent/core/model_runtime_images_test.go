// Ported from packages/coding-agent/test/model-runtime-images.test.ts (pi v1.5.2).

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tmriStrPtr(s string) *string { return &s }
func tmriBoolPtr(b bool) *bool    { return &b }

func tmriNewRuntime(t *testing.T, creds ai.CredentialStore, modelsPath string) *ModelRuntime {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("OPENROUTER_API_KEY", "")
	if creds == nil {
		creds = ai.NewInMemoryCredentialStore()
	}
	storePath := filepath.Join(filepath.Dir(modelsPath), "models-store.json")
	rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
		Credentials:     creds,
		ModelsPath:      &modelsPath,
		ModelsStorePath: &storePath,
		RefreshOnCreate: tmriBoolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func TestModelRuntimeImages(t *testing.T) {
	t.Run("separates openrouter image models from chat models and rejects them on chat entry points", func(t *testing.T) {
		dir := t.TempDir()
		rt := tmriNewRuntime(t, nil, filepath.Join(dir, "models.json"))

		imgModels := rt.GetModelsOfType(ai.ModelTypeImage, tmriStrPtr("openrouter"))
		if len(imgModels) == 0 {
			t.Fatal("expected builtin openrouter image models")
		}
		imgAny := rt.GetModelOfType(ai.ModelTypeImage, "openrouter", "black-forest-labs/flux.2-pro")
		imgModel, ok := imgAny.(*ai.ImageModel)
		if !ok || imgModel == nil {
			t.Fatalf("expected flux.2-pro ImageModel, got %T (%v)", imgAny, imgAny)
		}

		// Image model must NOT be listed in chat GetModels().
		for _, m := range rt.GetModels(tmriStrPtr("openrouter")) {
			if m.Id == imgModel.Id {
				t.Fatalf("image model %s leaked into chat GetModels()", m.Id)
			}
		}

		// Non-chat ModelType passed to Stream / StreamSimple / Complete / CompleteSimple must reject as "is not a chat model".
		nonChatTyp := ai.ModelTypeImage
		nonChatAsChat := &ai.Model{
			BaseModel: imgModel.BaseModel,
			Type:      &nonChatTyp,
		}
		msg, err := rt.Complete(t.Context(), nonChatAsChat, &ai.Context{
			Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tmriStrPtr("draw")}}},
		}, nil)
		if err == nil && (msg == nil || msg.ErrorMessage == nil || !strings.Contains(*msg.ErrorMessage, "is not a chat model")) {
			t.Fatalf("expected Complete to fail with 'is not a chat model', got msg=%+v err=%v", msg, err)
		}
		msgSimple, errSimple := rt.CompleteSimple(t.Context(), nonChatAsChat, &ai.Context{
			Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tmriStrPtr("draw")}}},
		}, nil)
		if errSimple == nil && (msgSimple == nil || msgSimple.ErrorMessage == nil || !strings.Contains(*msgSimple.ErrorMessage, "is not a chat model")) {
			t.Fatalf("expected CompleteSimple to fail with 'is not a chat model', got msg=%+v err=%v", msgSimple, errSimple)
		}
	})

	t.Run("extension Image model merges models.json headers, applies TransformHeaders, and returns error result cleanly", func(t *testing.T) {
		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		raw, _ := jsonx.MarshalIndent(jsonx.ObjectOf(
			"providers", jsonx.ObjectOf(
				"ext-img", jsonx.ObjectOf(
					"headers", jsonx.ObjectOf("x-file-hdr", "from-models-json"),
				),
			),
		), "  ")
		if err := os.WriteFile(modelsPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}

		rt := tmriNewRuntime(t, nil, modelsPath)
		var sawKey *string
		var sawHeaders ai.ProviderHeaders

		extHdrs := omap.NewMap[string, string]()
		extHdrs.Set("x-ext-hdr", "from-ext")

		if err := rt.RegisterProvider("ext-img", ProviderConfigInput{
			BaseUrl: tmriStrPtr("https://img.example.com"),
			ApiKey:  tmriStrPtr("$EXT_IMG_KEY"),
			Headers: extHdrs,
			Images: map[string]ai.ProviderImages{
				"mock-img-api": {
					GenerateImages: func(_ context.Context, model *ai.ImageModel, _ *ai.ImagesContext, opts *ai.ImagesOptions) (*ai.AssistantImages, error) {
						sawKey = opts.ApiKey
						sawHeaders = opts.Headers
						return &ai.AssistantImages{
							Api:        ai.ImageApi(model.Api),
							Provider:   model.Provider,
							Model:      model.Id,
							Output:     ai.ContentList{&ai.ImageContent{Type: "image", Data: "abc", MimeType: "image/png"}},
							StopReason: ai.ImagesStopReasonStop,
							Timestamp:  1,
						}, nil
					},
				},
			},
			Models: []ProviderModelConfig{
				&ProviderImageModelConfig{
					ProviderModelConfigBase: ProviderModelConfigBase{Id: "img-gen-1", Name: "Img 1", Api: tmriStrPtr("mock-img-api")},
					Type:                    ai.ModelTypeImage,
					Output:                  []ai.ModelModality{ai.ModelModalityImage},
				},
			},
		}); err != nil {
			t.Fatalf("RegisterProvider: %v", err)
		}

		imgModel := rt.GetModelOfType(ai.ModelTypeImage, "ext-img", "img-gen-1").(*ai.ImageModel)
		imgCtx := &ai.ImagesContext{Input: ai.ContentList{&ai.TextContent{Type: "text", Text: "a cat"}}}

		// Missing env key returns error AssistantImages cleanly instead of returning a Go error.
		t.Setenv("EXT_IMG_KEY", "")
		errRes, err := rt.GenerateImages(t.Context(), imgModel, imgCtx, nil)
		if err != nil {
			t.Fatalf("GenerateImages should return AssistantImages on auth failure, got Go err %v", err)
		}
		if errRes.StopReason != "error" || errRes.ErrorMessage == nil || !strings.Contains(*errRes.ErrorMessage, "EXT_IMG_KEY") {
			t.Fatalf("expected AssistantImages error referencing EXT_IMG_KEY, got %+v", errRes)
		}

		t.Setenv("EXT_IMG_KEY", "img-secret")
		okRes, err := rt.GenerateImages(t.Context(), imgModel, imgCtx, &ai.ModelsImagesOptions{
			ModelsRequestTransforms: ai.ModelsRequestTransforms{
				TransformHeaders: func(h ai.ProviderHeaders) (ai.ProviderHeaders, error) {
					out := omap.NewMap[string, *string]()
					if h != nil {
						for k, v := range h.All() {
							out.Set(k, v)
						}
					}
					v := "yes"
					out.Set("x-img-transform", &v)
					return out, nil
				},
			},
		})
		if err != nil {
			t.Fatalf("GenerateImages: %v", err)
		}
		if okRes.StopReason != ai.ImagesStopReasonStop || len(okRes.Output) != 1 || sawKey == nil || *sawKey != "img-secret" {
			t.Fatalf("GenerateImages mismatch: res=%+v key=%v", okRes, sawKey)
		}
		if v, _ := sawHeaders.Get("x-file-hdr"); v == nil || *v != "from-models-json" {
			t.Fatalf("x-file-hdr = %v", v)
		}
		if v, _ := sawHeaders.Get("x-ext-hdr"); v == nil || *v != "from-ext" {
			t.Fatalf("x-ext-hdr = %v", v)
		}
		if v, _ := sawHeaders.Get("x-img-transform"); v == nil || *v != "yes" {
			t.Fatalf("x-img-transform = %v", v)
		}
	})
}
