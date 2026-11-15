package ai_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func idHeaderValue(headers *omap.Map[string, *string], key string) (string, bool) {
	if headers == nil {
		return "", false
	}
	v, ok := headers.Get(key)
	if !ok || v == nil {
		return "", false
	}
	return *v, true
}

func idMapValue(headers *omap.Map[string, string], key string) (string, bool) {
	if headers == nil {
		return "", false
	}
	return headers.Get(key)
}

func TestFormatUserAgentAndModeJSON(t *testing.T) {
	t.Run("default detailed format includes PiAiVersion, platform, go version, arch", func(t *testing.T) {
		ua := ai.FormatUserAgent(nil)
		expected := fmt.Sprintf("pi/%s (%s; go/%s; %s)",
			ai.PiAiVersion,
			js.Platform(),
			strings.TrimPrefix(runtime.Version(), "go"),
			js.Arch(),
		)
		if ua != expected {
			t.Fatalf("expected %q, got %q", expected, ua)
		}
	})

	t.Run("short, custom, and version overrides format User-Agent", func(t *testing.T) {
		if got := ai.FormatUserAgent(&ai.ClientIdentity{
			UserAgent: ai.UserAgentModeValue{Val: ai.UserAgentModeShort},
		}); got != "pi" {
			t.Fatalf("expected short User-Agent 'pi', got %q", got)
		}

		if got := ai.FormatUserAgent(&ai.ClientIdentity{
			UserAgent: ai.UserAgentModeValue{Val: &ai.UserAgentModeCustom{Custom: "my-agent/2.0"}},
		}); got != "my-agent/2.0" {
			t.Fatalf("expected custom User-Agent, got %q", got)
		}

		ver := "9.8.7"
		if got := ai.FormatUserAgent(&ai.ClientIdentity{Version: &ver}); !strings.HasPrefix(got, "pi/9.8.7 (") {
			t.Fatalf("expected custom version prefix, got %q", got)
		}
	})

	t.Run("UserAgentModeValue JSON roundtrips built-in, custom, and unknown forms", func(t *testing.T) {
		for _, tc := range []struct {
			raw  string
			want string
		}{
			{`"short"`, `"short"`},
			{`{"custom":"acme/1.0"}`, `{"custom":"acme/1.0"}`},
			{`"future-mode"`, `"future-mode"`},
		} {
			var v ai.UserAgentModeValue
			if err := jsonx.Unmarshal([]byte(tc.raw), &v); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.raw, err)
			}
			out, err := jsonx.Marshal(v)
			if err != nil {
				t.Fatalf("marshal %s: %v", tc.raw, err)
			}
			if string(out) != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, string(out))
			}
		}
	})
}

func TestMergeAndAttributionHeaders(t *testing.T) {
	t.Run("MergeClientIdentity overrides field-by-field and supports explicit attribution clear", func(t *testing.T) {
		v1 := "1.0.0"
		v2 := "2.0.0"
		base := &ai.ClientIdentity{
			UserAgent:   ai.UserAgentModeValue{Val: ai.UserAgentModeShort},
			Attribution: jsonx.Some(ai.PiAttribution),
			Version:     &v1,
		}
		override := &ai.ClientIdentity{
			Attribution: jsonx.Null[ai.ClientAttribution](),
			Version:     &v2,
		}
		merged := ai.MergeClientIdentity(base, override)
		if merged.UserAgent.Val != ai.UserAgentModeShort {
			t.Fatalf("expected UserAgent preserved as short, got %#v", merged.UserAgent.Val)
		}
		if merged.Version == nil || *merged.Version != "2.0.0" {
			t.Fatalf("expected Version overridden to 2.0.0, got %v", merged.Version)
		}
		if _, hasAttr := merged.Attribution.Get(); hasAttr {
			t.Fatal("expected explicit jsonx.Null attribution to clear earlier attribution")
		}
		if ai.MergeClientIdentity(nil, nil) != nil {
			t.Fatal("expected merging only nils to return nil")
		}
	})

	t.Run("GetAttributionHeaders and GetIdentityHeaders target OpenRouter, Nvidia NIM, OpenCode, and generic hosts", func(t *testing.T) {
		id := &ai.ClientIdentity{
			UserAgent:   ai.UserAgentModeValue{Val: ai.UserAgentModeShort},
			Attribution: jsonx.Some(ai.PiAttribution),
		}

		// OpenRouter by provider or baseUrl
		orHeaders := ai.GetIdentityHeaders(ai.ClientIdentityTarget{Provider: "openrouter", BaseUrl: "https://openrouter.ai/api/v1"}, id)
		if ua, _ := idMapValue(orHeaders, "User-Agent"); ua != "pi" {
			t.Fatalf("expected User-Agent=pi, got %q", ua)
		}
		if ref, _ := idMapValue(orHeaders, "HTTP-Referer"); ref != "https://pi.dev" {
			t.Fatalf("expected HTTP-Referer=https://pi.dev, got %q", ref)
		}
		if title, _ := idMapValue(orHeaders, "X-OpenRouter-Title"); title != "pi" {
			t.Fatalf("expected X-OpenRouter-Title=pi, got %q", title)
		}
		if cats, _ := idMapValue(orHeaders, "X-OpenRouter-Categories"); cats != "cli-agent" {
			t.Fatalf("expected X-OpenRouter-Categories=cli-agent, got %q", cats)
		}

		// Nvidia NIM by host
		nvHeaders := ai.GetAttributionHeaders(ai.ClientIdentityTarget{Provider: "custom", BaseUrl: "https://integrate.api.nvidia.com/v1"}, id)
		if origin, _ := idMapValue(nvHeaders, "X-BILLING-INVOKE-ORIGIN"); origin != "Pi" {
			t.Fatalf("expected capitalized origin Pi, got %q", origin)
		}

		// OpenCode by provider + session headers
		ocHeaders := ai.GetAttributionHeaders(ai.ClientIdentityTarget{Provider: "opencode-go", BaseUrl: "https://example.com"}, id)
		if client, _ := idMapValue(ocHeaders, "x-opencode-client"); client != "pi" {
			t.Fatalf("expected x-opencode-client=pi, got %q", client)
		}
		sess := "sess-xyz"
		ocSess := ai.GetOpenCodeSessionHeaders(ai.ClientIdentityTarget{Provider: "custom", BaseUrl: "https://opencode.ai/zen"}, &sess)
		if sVal, _ := idMapValue(ocSess, "x-opencode-session"); sVal != "sess-xyz" {
			t.Fatalf("expected x-opencode-session=sess-xyz, got %q", sVal)
		}
		if none := ai.GetOpenCodeSessionHeaders(ai.ClientIdentityTarget{Provider: "openai", BaseUrl: "https://api.openai.com"}, &sess); none != nil {
			t.Fatalf("expected nil session headers for non-OpenCode target, got %v", none)
		}

		// Opt-out via null attribution omits provider attribution headers
		optOut := &ai.ClientIdentity{Attribution: jsonx.Null[ai.ClientAttribution]()}
		if h := ai.GetAttributionHeaders(ai.ClientIdentityTarget{Provider: "openrouter"}, optOut); h.Len() != 0 {
			t.Fatalf("expected empty attribution headers when opted out, got len=%d", h.Len())
		}
	})
}

func TestWithClientIdentityWrappersAndModels(t *testing.T) {
	var seenIdentity *ai.ClientIdentity
	var seenHeaders ai.ProviderHeaders

	provider, err := ai.CreateProvider(&ai.CreateProviderOptions{
		Id:   "opencode",
		Auth: ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("Key", []string{"TEST_ID_KEY"})},
		Models: []ai.AnyModel{&ai.Model{
			BaseModel: ai.BaseModel{
				Id:       "m1",
				Name:     "M1",
				Api:      "mock-api",
				Provider: "opencode",
				BaseUrl:  "https://opencode.ai/v1",
				Input:    []ai.ModelModality{ai.ModelModalityText},
			},
			ContextWindow: 8000,
			MaxTokens:     1000,
		}},
		Api: ai.ProviderStreams{
			Stream: func(_ context.Context, model *ai.Model, _ *ai.TranscriptContext, opts ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
				base := opts.BaseStreamOptions()
				seenIdentity = base.Identity
				seenHeaders = base.Headers
				s := ai.NewAssistantMessageEventStream()
				msg := &ai.AssistantMessage{
					Role:       "assistant",
					Api:        ai.Api(model.Api),
					Provider:   model.Provider,
					Model:      model.Id,
					StopReason: ai.StopReasonStop,
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
				}
				s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg})
				s.End(msg)
				return s
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}

	vBase := "1.2.3"
	vWrap := "2.0.0"
	baseModels := ai.CreateModels(&ai.CreateModelsOptions{
		Identity: &ai.ClientIdentity{
			Version:     &vBase,
			Attribution: jsonx.Some(ai.PiAttribution),
		},
	})
	baseModels.SetProvider(provider)

	wrapped := ai.WithClientIdentityMutable(baseModels, &ai.ClientIdentity{
		UserAgent: ai.UserAgentModeValue{Val: ai.UserAgentModeShort},
		Version:   &vWrap,
	})

	model := wrapped.GetModel("opencode", "m1")
	apiKey := "k-direct"
	sess := "sess-abc"

	// Request identity explicitly clears attribution while inheriting wrapper UserAgent/Version
	msg, err := wrapped.Complete(context.Background(), model, &ai.Context{
		Messages: []ai.Message{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &apiKey}}},
	}, &ai.ModelsApiStreamOptions{
		ProviderStreamOptions: &ai.StreamOptions{
			SessionId: &sess,
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey:   &apiKey,
				Identity: &ai.ClientIdentity{Attribution: jsonx.Null[ai.ClientAttribution]()},
			},
		},
	})
	if err != nil || msg.StopReason != ai.StopReasonStop {
		t.Fatalf("Complete failed: msg=%+v err=%v", msg, err)
	}
	if seenIdentity == nil || seenIdentity.Version == nil || *seenIdentity.Version != "2.0.0" {
		t.Fatalf("expected wrapper version=2.0.0, got %+v", seenIdentity)
	}
	if seenIdentity.UserAgent.Val != ai.UserAgentModeShort {
		t.Fatalf("expected wrapper short UserAgent, got %#v", seenIdentity.UserAgent.Val)
	}
	if _, ok := seenIdentity.Attribution.Get(); ok {
		t.Fatal("expected request-level Attribution null to opt out")
	}
	if sVal, ok := idHeaderValue(seenHeaders, "x-opencode-session"); !ok || sVal != "sess-abc" {
		t.Fatalf("expected x-opencode-session=sess-abc in headers, got %q (ok=%v)", sVal, ok)
	}
}
