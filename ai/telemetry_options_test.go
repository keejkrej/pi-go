package ai_test

import (
	"context"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/telemetry"
)

type telOptSpyContext struct {
	id         string
	startCalls int
}

func (s *telOptSpyContext) Metrics() telemetry.TelemetryMetrics { return nil }
func (s *telOptSpyContext) Propagation() telemetry.TelemetryPropagation {
	return telemetry.NoopTelemetryPropagation
}
func (s *telOptSpyContext) StartSpan(options *telemetry.SpanOptions, callback func(span telemetry.TelemetrySpan) (any, error)) (any, error) {
	s.startCalls++
	return callback(&telOptSpySpan{ctx: s})
}

type telOptSpySpan struct {
	ctx *telOptSpyContext
}

func (s *telOptSpySpan) Metrics() telemetry.TelemetryMetrics { return nil }
func (s *telOptSpySpan) Propagation() telemetry.TelemetryPropagation {
	return telemetry.NoopTelemetryPropagation
}
func (s *telOptSpySpan) StartSpan(options *telemetry.SpanOptions, callback func(span telemetry.TelemetrySpan) (any, error)) (any, error) {
	return s.ctx.StartSpan(options, callback)
}
func (s *telOptSpySpan) AddLink(telemetry.SpanLink)                {}
func (s *telOptSpySpan) AddEvent(string, telemetry.SpanAttributes) {}
func (s *telOptSpySpan) SetAttributes(telemetry.SpanAttributes)    {}
func (s *telOptSpySpan) SetStatus(telemetry.SpanStatus)            {}

func TestTelemetryOptionsPropagationAndImagesRegistry(t *testing.T) {
	chatModel := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "m-chat",
			Name:     "M Chat",
			Api:      "tel-opt-chat",
			Provider: "telprov",
			BaseUrl:  "https://example.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 4096,
		MaxTokens:     512,
	}
	imgModel := &ai.ImageModel{
		BaseModel: ai.BaseModel{
			Id:       "m-img",
			Name:     "M Img",
			Api:      "tel-opt-images",
			Provider: "telprov",
			BaseUrl:  "https://example.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Type:   ai.ModelTypeImage,
		Output: []ai.ModelModality{ai.ModelModalityImage},
	}
	clsModel := &ai.ClassifierModel{
		BaseModel: ai.BaseModel{
			Id:       "m-cls",
			Name:     "M Cls",
			Api:      "tel-opt-classifier",
			Provider: "telprov",
			BaseUrl:  "https://example.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Type:          ai.ModelTypeClassifier,
		ContextWindow: 1024,
	}

	var gotStreamTel, gotSimpleTel, gotFetchTel, gotCancelTel, gotImgTel, gotClsTel telemetry.TelemetryContext

	makeDoneStream := func(m *ai.Model) *ai.AssistantMessageEventStream {
		s := ai.NewAssistantMessageEventStream()
		msg := &ai.AssistantMessage{
			Role:       "assistant",
			Api:        ai.Api(m.Api),
			Provider:   m.Provider,
			Model:      m.Id,
			StopReason: ai.StopReasonStop,
			Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
		}
		s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg})
		s.End(msg)
		return s
	}

	prov, err := ai.CreateProvider(&ai.CreateProviderOptions{
		Id:     "telprov",
		Auth:   ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("Key", []string{"TEL_OPT_KEY"})},
		Models: []ai.AnyModel{chatModel, imgModel, clsModel},
		ApiByModel: map[string]ai.ProviderStreams{
			"tel-opt-chat": {
				Stream: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, opts ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
					gotStreamTel = opts.BaseStreamOptions().TelemetryContext
					return makeDoneStream(m)
				},
				StreamSimple: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
					gotSimpleTel = opts.TelemetryContext
					return makeDoneStream(m)
				},
				FetchDeferred: func(_ context.Context, m *ai.Model, _ *ai.DeferredHandle, opts *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
					gotFetchTel = opts.TelemetryContext
					return makeDoneStream(m)
				},
				CancelDeferred: func(_ context.Context, _ *ai.Model, _ *ai.DeferredHandle, opts *ai.DeferredCancelOptions) error {
					gotCancelTel = opts.TelemetryContext
					return nil
				},
			},
		},
		Images: map[string]ai.ProviderImages{
			"tel-opt-images": {
				GenerateImages: func(_ context.Context, m *ai.ImageModel, _ *ai.ImagesContext, opts *ai.ImagesOptions) (*ai.AssistantImages, error) {
					gotImgTel = opts.TelemetryContext
					return &ai.AssistantImages{Api: ai.ImageApi(m.Api), Provider: m.Provider, Model: m.Id, StopReason: ai.ImagesStopReasonStop}, nil
				},
			},
		},
		Classifiers: map[string]ai.ProviderClassifier{
			"tel-opt-classifier": {
				Classify: func(_ context.Context, m *ai.ClassifierModel, _ *ai.ClassifierContext, opts *ai.ClassifierOptions) (*ai.ClassifierResult, error) {
					gotClsTel = opts.TelemetryContext
					return &ai.ClassifierResult{Api: ai.ClassifierApi(m.Api), Provider: m.Provider, Model: m.Id, StopReason: ai.ClassifierStopReasonStop}, nil
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}

	models := ai.CreateModels(nil)
	models.SetProvider(prov)
	ctx := context.Background()
	apiKey := "k"
	hi := "hi"
	userCtx := &ai.Context{Messages: []ai.Message{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &hi}}}}
	handle := &ai.DeferredHandle{Provider: "telprov", ModelId: "m-chat", Api: "tel-opt-chat", Id: "h1"}

	spyStream := &telOptSpyContext{id: "stream"}
	if _, err := models.Complete(ctx, chatModel, userCtx, &ai.ModelsApiStreamOptions{
		ProviderStreamOptions: &ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: &apiKey, TelemetryContext: spyStream},
		},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	spySimple := &telOptSpyContext{id: "simple"}
	if _, err := models.CompleteSimple(ctx, chatModel, userCtx, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: &apiKey, TelemetryContext: spySimple},
			},
		},
	}); err != nil {
		t.Fatalf("CompleteSimple: %v", err)
	}

	spyFetch := &telOptSpyContext{id: "fetch"}
	if _, err := models.FetchDeferred(ctx, chatModel, handle, &ai.ModelsDeferredFetchOptions{
		DeferredFetchOptions: ai.DeferredFetchOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: &apiKey, TelemetryContext: spyFetch},
		},
	}); err != nil {
		t.Fatalf("FetchDeferred: %v", err)
	}

	spyCancel := &telOptSpyContext{id: "cancel"}
	if err := models.CancelDeferred(ctx, chatModel, handle, &ai.ModelsDeferredCancelOptions{
		DeferredCancelOptions: ai.DeferredCancelOptions{ApiKey: &apiKey, TelemetryContext: spyCancel},
	}); err != nil {
		t.Fatalf("CancelDeferred: %v", err)
	}

	spyImg := &telOptSpyContext{id: "img"}
	if _, err := models.GenerateImages(ctx, imgModel, &ai.ImagesContext{Input: ai.ContentList{&ai.TextContent{Type: "text", Text: "draw"}}}, &ai.ModelsImagesOptions{
		ImagesOptions: ai.ImagesOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ImageModel]{ApiKey: &apiKey, TelemetryContext: spyImg},
		},
	}); err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	spyCls := &telOptSpyContext{id: "cls"}
	if _, err := models.Classify(ctx, clsModel, &ai.ClassifierContext{}, &ai.ModelsClassifierOptions{
		ClassifierOptions: ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{ApiKey: &apiKey, TelemetryContext: spyCls},
		},
	}); err != nil {
		t.Fatalf("Classify: %v", err)
	}

	if gotStreamTel != spyStream || gotSimpleTel != spySimple || gotFetchTel != spyFetch ||
		gotCancelTel != spyCancel || gotImgTel != spyImg || gotClsTel != spyCls {
		t.Fatalf("TelemetryContext did not survive through all provider entry points")
	}
	if spyStream.startCalls != 1 || spySimple.startCalls != 1 || spyFetch.startCalls != 1 {
		t.Fatalf("expected ObserveModelRequest to start 1 span each, got stream=%d simple=%d fetch=%d",
			spyStream.startCalls, spySimple.startCalls, spyFetch.startCalls)
	}

	// Verify ImagesApiRegistry preserves TelemetryContext and checks API mismatch.
	testApi := ai.ImageApi("test-images-registry-tel")
	var regTel telemetry.TelemetryContext
	ai.RegisterImagesApiProvider(ai.ImagesApiProvider{
		Api: testApi,
		GenerateImages: func(_ context.Context, m *ai.ImageModel, _ *ai.ImagesContext, opts *ai.ImagesOptions) (*ai.AssistantImages, error) {
			regTel = opts.TelemetryContext
			return &ai.AssistantImages{Api: ai.ImageApi(m.Api), Provider: m.Provider, Model: m.Id, StopReason: ai.ImagesStopReasonStop}, nil
		},
	}, nil)
	regProvider, ok := ai.GetImagesApiProvider(testApi)
	if !ok || regProvider == nil {
		t.Fatal("expected registered images provider")
	}
	spyReg := &telOptSpyContext{id: "reg-img"}
	if _, err := regProvider.GenerateImages(ctx, &ai.ImageModel{BaseModel: ai.BaseModel{Api: string(testApi), Id: "i1", Provider: "p"}}, &ai.ImagesContext{}, &ai.ImagesOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ImageModel]{TelemetryContext: spyReg},
	}); err != nil {
		t.Fatalf("registered GenerateImages failed: %v", err)
	}
	if regTel != spyReg {
		t.Fatal("expected ImagesApiProvider to preserve TelemetryContext")
	}
	if _, err := regProvider.GenerateImages(ctx, &ai.ImageModel{BaseModel: ai.BaseModel{Api: "wrong-api"}}, &ai.ImagesContext{}, nil); err == nil {
		t.Fatal("expected API mismatch error from ImagesApiProvider wrapper")
	}
}
