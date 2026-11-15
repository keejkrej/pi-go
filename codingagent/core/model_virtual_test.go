// Ported from packages/coding-agent/test/virtual-models.test.ts (pi v1.5.2).

package core

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
)

func tmvBoolPtr(b bool) *bool { return &b }
func tmvIntPtr(i int) *int    { return &i }

type tmvFixture struct {
	runtime    *ModelRuntime
	faux       *providers.FauxProviderHandle
	definition VirtualModelDefinition
	virtual    *ai.Model
}

func tmvSetupRuntime(t *testing.T, requests *[]ModelRouteRequest) tmvFixture {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	modelsPath := filepath.Join(t.TempDir(), "nonexistent-models.json")
	rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
		Credentials:       ai.NewInMemoryCredentialStore(),
		ModelsStore:       NewInMemoryCodingAgentModelsStore(),
		ModelsPath:        &modelsPath,
		AllowModelNetwork: false,
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)

	faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{
			{Id: "small", ContextWindow: tmvIntPtr(1000), MaxTokens: tmvIntPtr(100), Input: []string{"text"}},
			{Id: "large", ContextWindow: tmvIntPtr(50000), MaxTokens: tmvIntPtr(5000), Input: []string{"text", "image"}, Reasoning: tmvBoolPtr(true)},
		},
	})
	if err != nil {
		t.Fatalf("FauxProvider: %v", err)
	}
	rt.RegisterNativeProvider(faux.Provider)

	def := VirtualModelDefinition{
		Provider:       "router",
		Id:             "auto",
		Name:           "Auto",
		ThinkingLevels: []ai.ModelThinkingLevel{ai.ModelThinkingLevelLow, ai.ModelThinkingLevelHigh},
		Route: func(_ context.Context, req ModelRouteRequest) (ModelRoute, error) {
			if requests != nil {
				*requests = append(*requests, req)
			}
			target := "small"
			if req.ThinkingLevel == ai.ModelThinkingLevelHigh {
				target = "large"
			}
			m := rt.GetModel("faux", target)
			return ModelRoute{Model: m, ThinkingLevel: ai.ModelThinkingLevelHigh}, nil
		},
	}
	if err := rt.RegisterVirtualModel(def); err != nil {
		t.Fatalf("RegisterVirtualModel: %v", err)
	}
	if _, err := rt.Refresh(t.Context(), &ai.ModelsRefreshOptions{AllowNetwork: tmvBoolPtr(false)}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return tmvFixture{
		runtime:    rt,
		faux:       faux,
		definition: def,
		virtual:    rt.GetModel("router", "auto"),
	}
}

func tmvAssistantFrom(model *ai.Model, text string) *ai.AssistantMessage {
	msg := providers.FauxAssistantMessage(providers.FauxAssistantText(text), nil)
	msg.Api = ai.Api(model.Api)
	msg.Provider = model.Provider
	msg.Model = model.Id
	return msg
}

func TestVirtualModelHelpersAndBranchSelection(t *testing.T) {
	fix := tmvSetupRuntime(t, nil)
	small := fix.runtime.GetModel("faux", "small")
	large := fix.runtime.GetModel("faux", "large")
	if !IsVirtualModel(fix.virtual) || !IsVirtualAnyModel(fix.virtual) || !IsVirtualModelApi(VirtualModelApi) {
		t.Fatalf("expected router/auto to report virtual")
	}
	if IsVirtualModel(small) || IsVirtualAnyModel(small) || IsVirtualModelApi("openai-completions") {
		t.Fatalf("expected physical model not to report virtual")
	}
	if !IsVirtualAssistantMessage(tmvAssistantFrom(fix.virtual, "")) || IsVirtualAssistantMessage(tmvAssistantFrom(small, "hi")) {
		t.Fatalf("IsVirtualAssistantMessage mismatch")
	}

	// FindLatestResponse skips error and aborted assistant messages.
	good := tmvAssistantFrom(small, "good")
	errMsg := tmvAssistantFrom(large, "")
	errMsg.StopReason = ai.StopReasonError
	abortedMsg := tmvAssistantFrom(large, "")
	abortedMsg.StopReason = ai.StopReasonAborted
	if got := FindLatestResponse([]agent.AgentMessage{good, errMsg, abortedMsg}); got != good {
		t.Fatalf("FindLatestResponse = %+v, want %+v", got, good)
	}

	// #10198: GetBranchSelection looks up only the last model_change.
	countLookups := func(branch []SessionEntry) (*BranchModelRef, []string) {
		var lookups []string
		sel := GetBranchSelection(branch, func(provider, modelId string) *ai.Model {
			lookups = append(lookups, provider+"/"+modelId)
			return fix.runtime.GetModel(provider, modelId)
		})
		return sel, lookups
	}

	var physicalBranch []SessionEntry
	physicalBranch = append(physicalBranch, &ModelChangeEntry{
		SessionEntryBase: SessionEntryBase{Type: "model_change", Id: "mc1"},
		Provider:         string(small.Provider),
		ModelId:          small.Id,
	})
	for i := 0; i < 100; i++ {
		physicalBranch = append(physicalBranch, &SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: "message"},
			Message:          tmvAssistantFrom(large, "ok"),
		})
	}
	if sel, lookups := countLookups(physicalBranch); sel == nil || sel.Provider != "faux" || sel.ModelId != "large" || !slices.Equal(lookups, []string{"faux/small"}) {
		t.Fatalf("physical branch sel=%+v lookups=%v", sel, lookups)
	}

	var routedBranch []SessionEntry
	routedBranch = append(routedBranch,
		&ModelChangeEntry{SessionEntryBase: SessionEntryBase{Type: "model_change"}, Provider: string(small.Provider), ModelId: small.Id},
		&SessionMessageEntry{SessionEntryBase: SessionEntryBase{Type: "message"}, Message: tmvAssistantFrom(small, "ok")},
		&ModelChangeEntry{SessionEntryBase: SessionEntryBase{Type: "model_change"}, Provider: string(fix.virtual.Provider), ModelId: fix.virtual.Id},
	)
	for i := 0; i < 100; i++ {
		routedBranch = append(routedBranch, &SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: "message"},
			Message:          tmvAssistantFrom(large, "ok"),
		})
	}
	if sel, lookups := countLookups(routedBranch); sel == nil || sel.Provider != "router" || sel.ModelId != "auto" || !slices.Equal(lookups, []string{"router/auto"}) {
		t.Fatalf("routed branch sel=%+v lookups=%v", sel, lookups)
	}

	// Trailing model_change without responses after it does not perform any lookup.
	trailingBranch := []SessionEntry{
		&ModelChangeEntry{SessionEntryBase: SessionEntryBase{Type: "model_change"}, Provider: string(fix.virtual.Provider), ModelId: fix.virtual.Id},
		&SessionMessageEntry{SessionEntryBase: SessionEntryBase{Type: "message"}, Message: tmvAssistantFrom(small, "ok")},
		&ModelChangeEntry{SessionEntryBase: SessionEntryBase{Type: "model_change"}, Provider: string(small.Provider), ModelId: small.Id},
	}
	if sel, lookups := countLookups(trailingBranch); sel == nil || sel.ModelId != "small" || len(lookups) != 0 {
		t.Fatalf("trailing change sel=%+v lookups=%v", sel, lookups)
	}

	// Without model_change, picks latest physical response skipping virtual failures.
	noChangeBranch := []SessionEntry{
		&SessionMessageEntry{SessionEntryBase: SessionEntryBase{Type: "message"}, Message: tmvAssistantFrom(small, "ok")},
		&SessionMessageEntry{SessionEntryBase: SessionEntryBase{Type: "message"}, Message: tmvAssistantFrom(large, "ok")},
		&SessionMessageEntry{SessionEntryBase: SessionEntryBase{Type: "message"}, Message: errMsg},
	}
	failedVirt := tmvAssistantFrom(fix.virtual, "")
	failedVirt.StopReason = ai.StopReasonError
	noChangeBranch[2] = &SessionMessageEntry{SessionEntryBase: SessionEntryBase{Type: "message"}, Message: failedVirt}
	if sel, lookups := countLookups(noChangeBranch); sel == nil || sel.ModelId != "large" || len(lookups) != 0 {
		t.Fatalf("noChangeBranch sel=%+v lookups=%v", sel, lookups)
	}

	// GetVirtualModelState returns most recent matching state.
	stateEntries := []SessionEntry{
		&CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom"}, CustomType: VirtualModelStateEntry, Data: VirtualModelStateData{Provider: "router", ModelId: "auto", State: "old"}},
		&CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom"}, CustomType: VirtualModelStateEntry, Data: VirtualModelStateData{Provider: "other", ModelId: "auto", State: "other"}},
		&CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom"}, CustomType: VirtualModelStateEntry, Data: VirtualModelStateData{Provider: "router", ModelId: "auto", State: "latest"}},
	}
	if got := GetVirtualModelState(stateEntries, "router", "auto"); got != "latest" {
		t.Fatalf("GetVirtualModelState = %v, want latest", got)
	}
	if got := GetVirtualModelState(stateEntries, "router", "missing"); got != nil {
		t.Fatalf("expected nil missing virtual state, got %v", got)
	}
}

func TestModelRuntimeVirtualRoutingAndClamping(t *testing.T) {
	var requests []ModelRouteRequest
	fix := tmvSetupRuntime(t, &requests)

	if fix.virtual.Provider != "router" || fix.virtual.Id != "auto" || fix.virtual.ContextWindow != 0 || fix.virtual.MaxTokens != 0 {
		t.Fatalf("unexpected virtual metadata: %+v", fix.virtual)
	}
	if !slices.Equal(fix.virtual.Input, []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage}) {
		t.Fatalf("unexpected virtual input modalities: %v", fix.virtual.Input)
	}
	if lvls := ai.GetSupportedThinkingLevels(fix.virtual); !slices.Equal(lvls, []ai.ModelThinkingLevel{ai.ModelThinkingLevelLow, ai.ModelThinkingLevelHigh}) {
		t.Fatalf("unexpected virtual thinking levels: %v", lvls)
	}

	small := fix.runtime.GetModel("faux", "small")
	large := fix.runtime.GetModel("faux", "large")
	med := ai.ModelThinkingLevelMedium
	prevMsg := tmvAssistantFrom(large, "answer")
	prevMsg.ThinkingLevel = &med
	msgs := []ai.Message{
		&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "first"}}}, Timestamp: 1},
		prevMsg,
		&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "second"}}}, Timestamp: 2},
	}

	lowRoute, err := fix.runtime.ResolveModel(t.Context(), fix.virtual, msgs, ResolveModelOptions{
		Reason:        ModelRouteReasonUser,
		ThinkingLevel: ai.ModelThinkingLevelLow,
	})
	if err != nil {
		t.Fatalf("ResolveModel(low): %v", err)
	}
	if lowRoute.Model.Id != "small" || lowRoute.ThinkingLevel != ai.ModelThinkingLevelOff {
		t.Fatalf("lowRoute = %+v, want small/off", lowRoute)
	}
	if requests[0].Previous == nil || requests[0].Previous.Model != large || requests[0].Previous.ThinkingLevel == nil || *requests[0].Previous.ThinkingLevel != ai.ModelThinkingLevelMedium {
		t.Fatalf("unexpected Previous metadata: %+v", requests[0].Previous)
	}

	highRoute, err := fix.runtime.ResolveModel(t.Context(), fix.virtual, msgs, ResolveModelOptions{
		Reason:        ModelRouteReasonUser,
		ThinkingLevel: ai.ModelThinkingLevelHigh,
	})
	if err != nil || highRoute.Model.Id != "large" || highRoute.ThinkingLevel != ai.ModelThinkingLevelHigh {
		t.Fatalf("highRoute = %+v err=%v", highRoute, err)
	}

	// Retry reports physical failure separately, and omits Failed when the virtual model failed before physical routing.
	high := ai.ModelThinkingLevelHigh
	errText := "overloaded_error"
	failedPhysical := tmvAssistantFrom(large, "")
	failedPhysical.ThinkingLevel = &high
	failedPhysical.StopReason = ai.StopReasonError
	failedPhysical.ErrorMessage = &errText
	_, _ = fix.runtime.ResolveModel(t.Context(), fix.virtual, msgs[:2], ResolveModelOptions{
		Reason:        ModelRouteReasonRetry,
		ThinkingLevel: ai.ModelThinkingLevelLow,
		Failed:        failedPhysical,
	})
	failedVirtual := tmvAssistantFrom(fix.virtual, "")
	failedVirtual.StopReason = ai.StopReasonError
	_, _ = fix.runtime.ResolveModel(t.Context(), fix.virtual, msgs[:2], ResolveModelOptions{
		Reason:        ModelRouteReasonRetry,
		ThinkingLevel: ai.ModelThinkingLevelLow,
		Failed:        failedVirtual,
	})
	if req := requests[len(requests)-2]; req.Previous == nil || req.Previous.Model != large || req.Failed == nil || req.Failed.Model != large || req.Failed.Message != failedPhysical {
		t.Fatalf("retry physical failed metadata mismatch: %+v", req)
	}
	if req := requests[len(requests)-1]; req.Failed != nil {
		t.Fatalf("expected Failed=nil for virtual failure, got %+v", req.Failed)
	}
	_ = small
}

func TestModelRuntimeVirtualRegistrationValidationAndDirectStream(t *testing.T) {
	var requests []ModelRouteRequest
	fix := tmvSetupRuntime(t, &requests)

	// Virtual model conflict with physical model rejected.
	conflictDef := fix.definition
	conflictDef.Provider = "faux"
	conflictDef.Id = "large"
	if err := fix.runtime.RegisterVirtualModel(conflictDef); err == nil || !strings.Contains(err.Error(), "conflicts with a physical model") {
		t.Fatalf("expected conflict error, got %v", err)
	}

	// Co-locate virtual models under a physical provider.
	fauxAuto := fix.definition
	fauxAuto.Provider = "faux"
	fauxAuto.Id = "fast"
	fauxAuto.Route = func(_ context.Context, _ ModelRouteRequest) (ModelRoute, error) {
		return ModelRoute{Model: fix.runtime.GetModel("faux", "small"), ThinkingLevel: ai.ModelThinkingLevelOff}, nil
	}
	if err := fix.runtime.RegisterVirtualModel(fauxAuto); err != nil {
		t.Fatal(err)
	}
	if fast := fix.runtime.GetModel("faux", "fast"); fast == nil {
		t.Fatalf("expected faux/fast model")
	}
	fix.runtime.UnregisterVirtualModel("faux", "fast")
	if fix.runtime.GetModel("faux", "fast") != nil {
		t.Fatalf("expected faux/fast unregistered")
	}

	// Direct CompleteSimple routes with reason "direct" and clamps MaxTokens to routed model's MaxTokens (5000).
	var seenMaxTokens *int
	fix.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, options *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			if options != nil {
				seenMaxTokens = options.MaxTokens
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantText("hello"), nil), nil
		}),
	})
	reasoning := ai.ThinkingLevelHigh
	maxTok := 20000
	msg, err := fix.runtime.CompleteSimple(t.Context(), fix.virtual, &ai.Context{
		Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "hi"}}}, Timestamp: 1}},
	}, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{MaxTokens: &maxTok},
			Reasoning:     &reasoning,
		},
	})
	if err != nil {
		t.Fatalf("CompleteSimple virtual: %v", err)
	}
	if msg.Provider != "faux" || msg.Model != "large" {
		t.Fatalf("unexpected routed message metadata: %+v", msg)
	}
	if seenMaxTokens == nil || *seenMaxTokens != 5000 {
		t.Fatalf("expected MaxTokens clamped to 5000, got %v", seenMaxTokens)
	}
	if len(requests) != 1 || requests[0].Reason != ModelRouteReasonDirect {
		t.Fatalf("expected 1 direct route request, got %+v", requests)
	}

	// Routing to a non-physical (virtual) model is rejected.
	badDef := fix.definition
	badDef.Route = func(_ context.Context, _ ModelRouteRequest) (ModelRoute, error) {
		return ModelRoute{Model: fix.virtual, ThinkingLevel: ai.ModelThinkingLevelOff}, nil
	}
	if err := fix.runtime.RegisterVirtualModel(badDef); err != nil {
		t.Fatal(err)
	}
	if _, err := fix.runtime.ResolveModel(t.Context(), fix.virtual, nil, ResolveModelOptions{Reason: ModelRouteReasonUser, ThinkingLevel: ai.ModelThinkingLevelLow}); err == nil || !strings.Contains(err.Error(), "which is not a physical model") {
		t.Fatalf("expected non-physical error, got %v", err)
	}

	// Standalone WithVirtualModels with nil base constructs virtual-only provider.
	standalone := WithVirtualModels("virtual-only", nil, []*ai.Model{CreateVirtualModel(VirtualModelDefinition{Provider: "virtual-only", Id: "v1"})})
	if standalone.Id != "virtual-only" || len(standalone.GetModels()) != 1 {
		t.Fatalf("unexpected standalone WithVirtualModels provider: %+v", standalone)
	}
}
