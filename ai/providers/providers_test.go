// Ported from packages/ai/test/providers.test.ts (pi v1.5.2).

package providers_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/omap"
)

type tprovFakeAuthContext struct {
	env   map[string]string
	files map[string]bool
}

func (f tprovFakeAuthContext) Env(_ context.Context, name string) (*string, error) {
	v, ok := f.env[name]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (f tprovFakeAuthContext) FileExists(_ context.Context, path string) (bool, error) {
	return f.files[path], nil
}

func tprovPtr[T any](v T) *T { return &v }

func tprovContext() *ai.TranscriptContext {
	hi := "hi"
	return ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &hi}, Timestamp: js.DateNow()},
		},
	})
}

var tprovDefaultImageResize = ai.ModelImageResizeOptions{
	MaxWidth:    tprovPtr(2000),
	MaxHeight:   tprovPtr(2000),
	MaxBytes:    tprovPtr(int(4.5 * 1024 * 1024)),
	JpegQuality: tprovPtr(80),
}

type tprovAuthInteraction struct {
	answers []string
	onInfo  func(ai.AuthEvent)
	onAsk   func(ai.AuthPrompt)
}

func (i *tprovAuthInteraction) Prompt(_ context.Context, prompt ai.AuthPrompt) (string, error) {
	if i.onAsk != nil {
		i.onAsk(prompt)
	}
	if len(i.answers) == 0 {
		return "", errors.New("unexpected prompt")
	}
	ans := i.answers[0]
	i.answers = i.answers[1:]
	return ans, nil
}

func (i *tprovAuthInteraction) Notify(event ai.AuthEvent) {
	if i.onInfo != nil {
		i.onInfo(event)
	}
}

func tprovSupportsMidConvoSystemMessages(m *ai.Model) bool {
	if m == nil || m.Compat.V == nil {
		return false
	}
	switch c := m.Compat.V.(type) {
	case *ai.OpenAICompletionsCompat:
		return c != nil && c.SupportsMidConvoSystemMessages != nil && *c.SupportsMidConvoSystemMessages
	case *ai.OpenAIResponsesCompat:
		return c != nil && c.SupportsMidConvoSystemMessages != nil && *c.SupportsMidConvoSystemMessages
	case *ai.AnthropicMessagesCompat:
		return c != nil && c.SupportsMidConvoSystemMessages != nil && *c.SupportsMidConvoSystemMessages
	case *ai.MistralConversationsCompat:
		return c != nil && c.SupportsMidConvoSystemMessages != nil && *c.SupportsMidConvoSystemMessages
	default:
		return false
	}
}

func TestBuiltinProviders_CatalogAndLookups(t *testing.T) {
	t.Parallel()
	models := providers.BuiltinModels(nil)
	registered := models.GetProviders()
	if len(registered) != len(providers.BuiltinProviders()) {
		t.Fatalf("len(GetProviders) = %d, want %d", len(registered), len(providers.BuiltinProviders()))
	}
	var hasAnthropic bool
	for _, p := range registered {
		if p.Id == "anthropic" {
			hasAnthropic = true
			break
		}
	}
	if !hasAnthropic {
		t.Fatal("expected anthropic in registered providers")
	}

	haiku := models.GetModel("anthropic", "claude-haiku-4-5")
	if haiku == nil || haiku.Api != string(ai.KnownApiAnthropicMessages) {
		t.Fatalf("unexpected claude-haiku-4-5: %+v", haiku)
	}

	all := models.GetModels(nil)
	if len(all) <= 500 {
		t.Fatalf("expected >500 models, got %d", len(all))
	}

	for _, p := range registered {
		pid := p.Id
		list := models.GetAllModels(&pid)
		if len(list) == 0 {
			t.Fatalf("provider %s has 0 models", pid)
		}
		for _, m := range list {
			var mp ai.ProviderId
			switch v := m.(type) {
			case *ai.Model:
				mp = v.Provider
			case *ai.ImageModel:
				mp = v.Provider
			case *ai.ClassifierModel:
				mp = v.Provider
			}
			if string(mp) != pid {
				t.Fatalf("provider %s returned model from %q", pid, mp)
			}
		}
	}

	bal := providers.GetBuiltinModel("radius", "balanced")
	if bal == nil || bal.Api != string(ai.KnownApiPiMessages) || bal.Provider != "radius" {
		t.Fatalf("unexpected radius/balanced: %+v", bal)
	}
}

func TestBuiltinProviders_UnknownProviderReturnsEmpty(t *testing.T) {
	t.Parallel()
	const unknown = providers.BuiltinProvider("not-a-provider")
	if providers.GetBuiltinModel(unknown, "x") != nil ||
		providers.GetBuiltinImageModel(unknown, "x") != nil ||
		providers.GetBuiltinClassifierModel(unknown, "x") != nil ||
		len(providers.GetBuiltinModels(unknown)) != 0 ||
		len(providers.GetBuiltinImageModels(unknown)) != 0 ||
		len(providers.GetBuiltinClassifierModels(unknown)) != 0 ||
		len(providers.GetAllBuiltinModels(unknown)) != 0 ||
		compat.GetModel(unknown, "x") != nil ||
		len(compat.GetModels(unknown)) != 0 {
		t.Fatal("expected empty/nil results for unknown provider id")
	}
}

func TestBuiltinProviders_ConstrainedSamplingCapabilities(t *testing.T) {
	t.Parallel()
	gpt4o := providers.GetBuiltinModel("openai", "gpt-4o")
	if gpt4o == nil {
		t.Fatal("missing openai/gpt-4o")
	}
	c4o, _ := gpt4o.Compat.V.(*ai.OpenAIResponsesCompat)
	if c4o == nil || c4o.SupportsStrictMode == nil || !*c4o.SupportsStrictMode || c4o.SupportsOpenAIGrammarTools != nil {
		t.Fatalf("unexpected gpt-4o compat: %+v", gpt4o.Compat.V)
	}

	gpt54 := providers.GetBuiltinModel("openai", "gpt-5.4")
	if gpt54 == nil {
		t.Fatal("missing openai/gpt-5.4")
	}
	c54, _ := gpt54.Compat.V.(*ai.OpenAIResponsesCompat)
	if c54 == nil || c54.SupportsStrictMode == nil || !*c54.SupportsStrictMode ||
		c54.SupportsOpenAIGrammarTools == nil || !*c54.SupportsOpenAIGrammarTools {
		t.Fatalf("unexpected gpt-5.4 compat: %+v", gpt54.Compat.V)
	}

	haiku := providers.GetBuiltinModel("anthropic", "claude-haiku-4-5")
	if haiku == nil {
		t.Fatal("missing anthropic/claude-haiku-4-5")
	}
	ca, _ := haiku.Compat.V.(*ai.AnthropicMessagesCompat)
	if ca == nil || ca.SupportsStrictTools == nil || !*ca.SupportsStrictTools {
		t.Fatalf("unexpected haiku compat: %+v", haiku.Compat.V)
	}
}

func TestBuiltinProviders_VisionResizeAndLimits(t *testing.T) {
	t.Parallel()
	var visionCount int
	for _, provider := range providers.GetBuiltinProviders() {
		for _, model := range providers.GetBuiltinModels(provider) {
			if slices.Contains(model.Input, ai.ModelModalityImage) {
				visionCount++
				if model.InputLimits == nil || model.InputLimits.Images == nil || model.InputLimits.Images.Resize == nil {
					t.Fatalf("%s/%s missing inputLimits.images.resize", provider, model.Id)
				}
				r := model.InputLimits.Images.Resize
				if *r.MaxWidth != *tprovDefaultImageResize.MaxWidth ||
					*r.MaxHeight != *tprovDefaultImageResize.MaxHeight ||
					*r.MaxBytes != *tprovDefaultImageResize.MaxBytes ||
					*r.JpegQuality != *tprovDefaultImageResize.JpegQuality {
					t.Fatalf("%s/%s resize = %+v", provider, model.Id, r)
				}
			}
		}
	}
	if visionCount == 0 {
		t.Fatal("expected >0 vision models")
	}

	haiku := providers.GetBuiltinModel("anthropic", "claude-haiku-4-5")
	if haiku == nil || haiku.InputLimits == nil || haiku.InputLimits.MaxRequestBytes == nil || *haiku.InputLimits.MaxRequestBytes != 32*1024*1024 ||
		haiku.InputLimits.Images == nil || haiku.InputLimits.Images.MaxPerRequest == nil || *haiku.InputLimits.Images.MaxPerRequest != 100 {
		t.Fatalf("unexpected anthropic/claude-haiku-4-5 inputLimits: %+v", haiku)
	}
	opus5 := providers.GetBuiltinModel("anthropic", "claude-opus-5")
	if opus5 == nil || opus5.InputLimits == nil || opus5.InputLimits.Images == nil || opus5.InputLimits.Images.MaxPerRequest == nil || *opus5.InputLimits.Images.MaxPerRequest != 600 {
		t.Fatalf("unexpected anthropic/claude-opus-5 inputLimits: %+v", opus5)
	}
	bedrockHaiku := providers.GetBuiltinModel("amazon-bedrock", "anthropic.claude-haiku-4-5-20251001-v1:0")
	if bedrockHaiku == nil || bedrockHaiku.InputLimits == nil || bedrockHaiku.InputLimits.Images == nil || bedrockHaiku.InputLimits.Images.MaxPerMessage == nil || *bedrockHaiku.InputLimits.Images.MaxPerMessage != 20 {
		t.Fatalf("unexpected bedrock haiku inputLimits: %+v", bedrockHaiku)
	}
	gpt4o := providers.GetBuiltinModel("openai", "gpt-4o")
	if gpt4o == nil || gpt4o.InputLimits == nil || gpt4o.InputLimits.MaxRequestBytes == nil || *gpt4o.InputLimits.MaxRequestBytes != 512*1024*1024 ||
		gpt4o.InputLimits.Images == nil || gpt4o.InputLimits.Images.MaxPerRequest == nil || *gpt4o.InputLimits.Images.MaxPerRequest != 1500 {
		t.Fatalf("unexpected openai/gpt-4o inputLimits: %+v", gpt4o)
	}
	geminiFlash := providers.GetBuiltinModel("google", "gemini-2.5-flash")
	if geminiFlash == nil || geminiFlash.InputLimits == nil || geminiFlash.InputLimits.MaxRequestBytes == nil || *geminiFlash.InputLimits.MaxRequestBytes != 20*1024*1024 ||
		geminiFlash.InputLimits.Images == nil || geminiFlash.InputLimits.Images.MaxPerRequest == nil || *geminiFlash.InputLimits.Images.MaxPerRequest != 3600 {
		t.Fatalf("unexpected google/gemini-2.5-flash inputLimits: %+v", geminiFlash)
	}

	var openRouterVision *ai.Model
	for _, m := range providers.GetBuiltinModels("openrouter") {
		if slices.Contains(m.Input, ai.ModelModalityImage) {
			openRouterVision = m
			break
		}
	}
	if openRouterVision == nil || openRouterVision.InputLimits == nil ||
		openRouterVision.InputLimits.MaxRequestBytes != nil ||
		openRouterVision.InputLimits.Images == nil ||
		openRouterVision.InputLimits.Images.MaxPerMessage != nil ||
		openRouterVision.InputLimits.Images.MaxPerRequest != nil ||
		openRouterVision.InputLimits.Images.Resize == nil {
		t.Fatalf("unexpected openrouter vision inputLimits: %+v", openRouterVision)
	}
}

func TestBuiltinProviders_GoogleThinkingLevelsAndCompatFlags(t *testing.T) {
	t.Parallel()
	for _, provider := range []providers.BuiltinProvider{"google", "google-vertex"} {
		if lvls := ai.GetSupportedThinkingLevels(providers.GetBuiltinModel(provider, "gemini-3.6-flash")); !slices.Contains(lvls, ai.ModelThinkingLevelMinimal) {
			t.Fatalf("%s/gemini-3.6-flash levels = %v", provider, lvls)
		}
		wantLMH := []ai.ModelThinkingLevel{ai.ModelThinkingLevelLow, ai.ModelThinkingLevelMedium, ai.ModelThinkingLevelHigh}
		if lvls := ai.GetSupportedThinkingLevels(providers.GetBuiltinModel(provider, "gemini-3.8-flash")); !slices.Equal(lvls, wantLMH) {
			t.Fatalf("%s/gemini-3.8-flash levels = %v", provider, lvls)
		}
		if lvls := ai.GetSupportedThinkingLevels(providers.GetBuiltinModel(provider, "gemini-3.1-pro-preview")); !slices.Equal(lvls, wantLMH) {
			t.Fatalf("%s/gemini-3.1-pro-preview levels = %v", provider, lvls)
		}
	}
	if lvls := ai.GetSupportedThinkingLevels(providers.GetBuiltinModel("opencode", "gemini-3.8-flash")); !slices.Equal(lvls, []ai.ModelThinkingLevel{
		ai.ModelThinkingLevelLow, ai.ModelThinkingLevelMedium, ai.ModelThinkingLevelHigh,
	}) {
		t.Fatalf("opencode/gemini-3.8-flash levels = %v", lvls)
	}
	if lvls := ai.GetSupportedThinkingLevels(providers.GetBuiltinModel("google", "gemma-4-31b-it")); !slices.Equal(lvls, []ai.ModelThinkingLevel{
		ai.ModelThinkingLevelMinimal, ai.ModelThinkingLevelHigh,
	}) {
		t.Fatalf("google/gemma-4-31b-it levels = %v", lvls)
	}

	models := providers.BuiltinModels(nil)
	supported := [][2]string{
		{"moonshotai", "kimi-k2.6"},
		{"moonshotai", "kimi-k2.7-code"},
		{"moonshotai", "kimi-k2.7-code-highspeed"},
		{"moonshotai", "kimi-k3"},
		{"moonshotai-cn", "kimi-k2.6"},
		{"moonshotai-cn", "kimi-k2.7-code"},
		{"moonshotai-cn", "kimi-k2.7-code-highspeed"},
		{"moonshotai-cn", "kimi-k3"},
		{"fireworks", "accounts/fireworks/models/kimi-k3"},
		{"fireworks", "accounts/fireworks/routers/kimi-k3-fast"},
		{"openai", "gpt-5.4"},
		{"openai", "gpt-5.5"},
		{"openai", "gpt-6-astra"},
		{"openai-codex", "gpt-5.6-sol"},
		{"anthropic", "claude-opus-5"},
		{"opencode", "gpt-5.4"},
		{"opencode", "gpt-5.6-terra"},
		{"opencode-go", "gpt-5.6-luna"},
		{"opencode", "claude-opus-4-8"},
		{"opencode", "claude-opus-5"},
		{"opencode", "kimi-k3"},
		{"opencode-go", "kimi-k3"},
		{"github-copilot", "gpt-5.6-terra"},
		{"github-copilot", "claude-opus-5"},
		{"github-copilot", "claude-opus-4.8"},
		{"github-copilot", "kimi-k3"},
		{"deepseek", "deepseek-v4-pro"},
		{"openrouter", "openai/gpt-5.6-terra"},
	}
	unsupported := [][2]string{
		{"fireworks", "accounts/fireworks/models/deepseek-v4p1-flash"},
		{"openai", "gpt-4.1"},
		{"openai", "gpt-5.2"},
		{"anthropic", "claude-sonnet-4-5"},
		{"google", "gemini-2.5-pro"},
		{"opencode", "gpt-5.2"},
		{"opencode", "claude-sonnet-4-5"},
		{"github-copilot", "claude-sonnet-4.6"},
		{"deepseek", "deepseek-flash"},
		{"openrouter", "anthropic/claude-opus-5"},
		{"openrouter", "moonshotai/kimi-k3"},
		{"openrouter", "openai/gpt-5.6-terra:batch"},
	}
	for _, pair := range supported {
		if !tprovSupportsMidConvoSystemMessages(models.GetModel(pair[0], pair[1])) {
			t.Fatalf("expected supportsMidConvoSystemMessages for %s/%s", pair[0], pair[1])
		}
	}
	for _, pair := range unsupported {
		if tprovSupportsMidConvoSystemMessages(models.GetModel(pair[0], pair[1])) {
			t.Fatalf("did not expect supportsMidConvoSystemMessages for %s/%s", pair[0], pair[1])
		}
	}

	for _, pair := range [][2]string{{"opencode", "gpt-5.6-terra"}, {"github-copilot", "gpt-5.6-terra"}} {
		m := models.GetModel(pair[0], pair[1])
		rc, _ := m.Compat.V.(*ai.OpenAIResponsesCompat)
		if rc == nil || rc.SupportsAdditionalTools == nil || !*rc.SupportsAdditionalTools || rc.SupportsToolSearch != nil {
			t.Fatalf("unexpected %s/%s compat: %+v", pair[0], pair[1], m.Compat.V)
		}
	}
	for _, p := range []string{"opencode", "github-copilot"} {
		m := models.GetModel(p, "claude-opus-5")
		ac, _ := m.Compat.V.(*ai.AnthropicMessagesCompat)
		if ac != nil && ac.SupportsMidConvoToolChanges != nil {
			t.Fatalf("%s/claude-opus-5 should not expose supportsMidConvoToolChanges", p)
		}
	}
	anthOpus5 := models.GetModel("anthropic", "claude-opus-5")
	if ac, _ := anthOpus5.Compat.V.(*ai.AnthropicMessagesCompat); ac == nil || ac.SupportsMidConvoToolChanges == nil || !*ac.SupportsMidConvoToolChanges {
		t.Fatalf("anthropic/claude-opus-5 compat = %+v", anthOpus5.Compat.V)
	}
	for _, p := range []string{"moonshotai", "moonshotai-cn", "opencode", "opencode-go"} {
		m := models.GetModel(p, "kimi-k3")
		cc, _ := m.Compat.V.(*ai.OpenAICompletionsCompat)
		if cc == nil || cc.SupportsMidConvoToolAdditions == nil || !*cc.SupportsMidConvoToolAdditions {
			t.Fatalf("%s/kimi-k3 compat = %+v", p, m.Compat.V)
		}
	}
	for _, p := range []string{"moonshotai", "moonshotai-cn"} {
		for _, id := range []string{"kimi-k2.6", "kimi-k2.7-code", "kimi-k2.7-code-highspeed"} {
			m := models.GetModel(p, id)
			if cc, _ := m.Compat.V.(*ai.OpenAICompletionsCompat); cc != nil && cc.SupportsMidConvoToolAdditions != nil {
				t.Fatalf("%s/%s should not set supportsMidConvoToolAdditions", p, id)
			}
		}
	}
	if cc, _ := models.GetModel("github-copilot", "kimi-k3").Compat.V.(*ai.OpenAICompletionsCompat); cc != nil && cc.SupportsMidConvoToolAdditions != nil {
		t.Fatal("github-copilot/kimi-k3 should not set supportsMidConvoToolAdditions")
	}
	if cc, _ := models.GetModel("openrouter", "openai/gpt-5.6-terra").Compat.V.(*ai.OpenAICompletionsCompat); cc != nil && cc.SupportsMidConvoToolAdditions != nil {
		t.Fatal("openrouter/openai/gpt-5.6-terra should not set supportsMidConvoToolAdditions")
	}
}

func TestBuiltinProviders_KimiPricingAndAuthResolution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	models := providers.BuiltinModels(nil)

	for _, p := range []string{"moonshotai", "moonshotai-cn"} {
		k3 := models.GetModel(p, "kimi-k3")
		if k3 == nil || k3.Cost.ModelCostRates != (ai.ModelCostRates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 0}) {
			t.Fatalf("%s/kimi-k3 cost = %+v", p, k3)
		}
	}
	kimiCodingExpected := map[string]ai.ModelCostRates{
		"k3":                        {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 0},
		"kimi-for-coding-highspeed": {Input: 1.9, Output: 8, CacheRead: 0.38, CacheWrite: 0},
	}
	for id, want := range kimiCodingExpected {
		m := models.GetModel("kimi-coding", id)
		if m == nil || m.Cost.ModelCostRates != want {
			t.Fatalf("kimi-coding/%s cost = %+v, want %+v", id, m, want)
		}
	}

	anthBearerModels := ai.CreateModels(&ai.CreateModelsOptions{
		AuthContext: tprovFakeAuthContext{
			env: map[string]string{
				"ANTHROPIC_AUTH_TOKEN":  "auth-token",
				"ANTHROPIC_OAUTH_TOKEN": "oauth-token",
				"ANTHROPIC_API_KEY":     "api-key",
			},
		},
	})
	anthBearerModels.SetProvider(providers.AnthropicProvider())
	res, err := anthBearerModels.GetAuth(ctx, "anthropic", nil)
	if err != nil || res == nil || res.Source == nil || *res.Source != "ANTHROPIC_AUTH_TOKEN" {
		t.Fatalf("anthropic bearer auth = %+v, err=%v", res, err)
	}
	if authHdr, ok := res.Auth.Headers.Get("Authorization"); !ok || authHdr == nil || *authHdr != "Bearer auth-token" {
		t.Fatalf("expected Authorization Bearer auth-token, got %v", res.Auth.Headers)
	}

	anthOAuthModels := ai.CreateModels(&ai.CreateModelsOptions{
		AuthContext: tprovFakeAuthContext{
			env: map[string]string{
				"ANTHROPIC_API_KEY":     "key",
				"ANTHROPIC_OAUTH_TOKEN": "oauth-token",
			},
		},
	})
	anthOAuthModels.SetProvider(providers.AnthropicProvider())
	oauthRes, err := anthOAuthModels.GetAuth(ctx, "anthropic", nil)
	if err != nil || oauthRes == nil || oauthRes.Auth.ApiKey == nil || *oauthRes.Auth.ApiKey != "oauth-token" ||
		oauthRes.Source == nil || *oauthRes.Source != "ANTHROPIC_OAUTH_TOKEN" {
		t.Fatalf("anthropic oauth precedence = %+v, err=%v", oauthRes, err)
	}
}

func TestBuiltinProviders_BedrockCloudflareAndVertexAuth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	bedrockAuth := providers.AmazonBedrockProvider().Auth.ApiKey
	cred1, err := bedrockAuth.Login(ctx, &tprovAuthInteraction{answers: []string{"bearer-token", "bedrock-token"}})
	if err != nil || cred1 == nil || cred1.Key == nil || *cred1.Key != "bedrock-token" {
		t.Fatalf("bedrock bearer login = %+v, err=%v", cred1, err)
	}

	var bedrockEvents []ai.AuthEvent
	cred2, err := bedrockAuth.Login(ctx, &tprovAuthInteraction{
		answers: []string{"aws-profile", "work"},
		onInfo:  func(ev ai.AuthEvent) { bedrockEvents = append(bedrockEvents, ev) },
	})
	if err != nil || cred2 == nil || !maps.Equal(cred2.Env, ai.ProviderEnv{"AWS_PROFILE": "work"}) {
		t.Fatalf("bedrock profile login = %+v, err=%v", cred2, err)
	}
	if len(bedrockEvents) != 1 {
		t.Fatalf("expected 1 bedrock info event, got %d", len(bedrockEvents))
	}
	if info, ok := bedrockEvents[0].(*ai.AuthEventInfo); !ok || len(info.Links) == 0 || info.Links[0].Label == nil || *info.Links[0].Label != "AWS credential provider chain" {
		t.Fatalf("unexpected bedrock info event: %+v", bedrockEvents[0])
	}
	resolvedBedrock, err := bedrockAuth.Resolve(ctx, ai.ApiKeyResolveInput{
		Ctx:        tprovFakeAuthContext{},
		Credential: &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Env: ai.ProviderEnv{"AWS_PROFILE": "work"}},
	})
	if err != nil || resolvedBedrock == nil || !maps.Equal(resolvedBedrock.Env, ai.ProviderEnv{"AWS_PROFILE": "work"}) {
		t.Fatalf("bedrock resolve = %+v, err=%v", resolvedBedrock, err)
	}

	ambientBedrock := ai.CreateModels(&ai.CreateModelsOptions{AuthContext: tprovFakeAuthContext{env: map[string]string{"AWS_PROFILE": "dev"}}})
	ambientBedrock.SetProvider(providers.AmazonBedrockProvider())
	bRes, err := ambientBedrock.GetAuth(ctx, "amazon-bedrock", nil)
	if err != nil || bRes == nil || bRes.Auth.ApiKey != nil || bRes.Source == nil || *bRes.Source != "AWS_PROFILE" {
		t.Fatalf("ambient bedrock auth = %+v, err=%v", bRes, err)
	}
	unconfiguredBedrock := ai.CreateModels(&ai.CreateModelsOptions{AuthContext: tprovFakeAuthContext{}})
	unconfiguredBedrock.SetProvider(providers.AmazonBedrockProvider())
	if none, err := unconfiguredBedrock.GetAuth(ctx, "amazon-bedrock", nil); err != nil || none != nil {
		t.Fatalf("expected unconfigured bedrock nil, got %+v, err=%v", none, err)
	}

	// Cloudflare Workers AI
	cfMissing := ai.CreateModels(&ai.CreateModelsOptions{AuthContext: tprovFakeAuthContext{env: map[string]string{"CLOUDFLARE_API_KEY": "cf-key"}}})
	cfMissing.SetProvider(providers.CloudflareWorkersAIProvider())
	if got, err := cfMissing.GetAuth(ctx, "cloudflare-workers-ai", nil); err != nil || got != nil {
		t.Fatalf("expected unconfigured cloudflare-workers-ai nil, got %+v, err=%v", got, err)
	}
	cfOK := ai.CreateModels(&ai.CreateModelsOptions{
		AuthContext: tprovFakeAuthContext{env: map[string]string{"CLOUDFLARE_API_KEY": "cf-key", "CLOUDFLARE_ACCOUNT_ID": "account-id"}},
	})
	cfOK.SetProvider(providers.CloudflareWorkersAIProvider())
	cfRes, err := cfOK.GetAuth(ctx, "cloudflare-workers-ai", nil)
	if err != nil || cfRes == nil || cfRes.Auth.ApiKey == nil || *cfRes.Auth.ApiKey != "cf-key" ||
		!maps.Equal(cfRes.Env, ai.ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "account-id"}) {
		t.Fatalf("cloudflare-workers-ai auth = %+v, err=%v", cfRes, err)
	}

	// Cloudflare AI Gateway
	cagMissing := ai.CreateModels(&ai.CreateModelsOptions{
		AuthContext: tprovFakeAuthContext{env: map[string]string{"CLOUDFLARE_API_KEY": "cf-key", "CLOUDFLARE_ACCOUNT_ID": "account-id"}},
	})
	cagMissing.SetProvider(providers.CloudflareAIGatewayProvider())
	if got, err := cagMissing.GetAuth(ctx, "cloudflare-ai-gateway", nil); err != nil || got != nil {
		t.Fatalf("expected unconfigured cloudflare-ai-gateway nil, got %+v, err=%v", got, err)
	}
	cagOK := ai.CreateModels(&ai.CreateModelsOptions{
		AuthContext: tprovFakeAuthContext{env: map[string]string{
			"CLOUDFLARE_API_KEY":    "cf-key",
			"CLOUDFLARE_ACCOUNT_ID": "account-id",
			"CLOUDFLARE_GATEWAY_ID": "gateway-id",
		}},
	})
	cagOK.SetProvider(providers.CloudflareAIGatewayProvider())
	cagRes, err := cagOK.GetAuth(ctx, "cloudflare-ai-gateway", nil)
	if err != nil || cagRes == nil || cagRes.Auth.Headers == nil ||
		!maps.Equal(cagRes.Env, ai.ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "account-id", "CLOUDFLARE_GATEWAY_ID": "gateway-id"}) {
		t.Fatalf("cloudflare-ai-gateway auth = %+v, err=%v", cagRes, err)
	}
	if v, ok := cagRes.Auth.Headers.Get("cf-aig-authorization"); !ok || v == nil || *v != "Bearer cf-key" {
		t.Fatalf("unexpected cf-aig-authorization: %v", cagRes.Auth.Headers)
	}
	if v, ok := cagRes.Auth.Headers.Get("Authorization"); !ok || v != nil {
		t.Fatalf("expected Authorization: nil, got %v (ok=%v)", v, ok)
	}
	if v, ok := cagRes.Auth.Headers.Get("x-api-key"); !ok || v != nil {
		t.Fatalf("expected x-api-key: nil, got %v (ok=%v)", v, ok)
	}

	// Google Vertex
	vertexAuth := providers.GoogleVertexProvider().Auth.ApiKey
	vCred1, err := vertexAuth.Login(ctx, &tprovAuthInteraction{answers: []string{"api-key", "vertex-key"}})
	if err != nil || vCred1 == nil || vCred1.Key == nil || *vCred1.Key != "vertex-key" {
		t.Fatalf("vertex key login = %+v, err=%v", vCred1, err)
	}
	var vertexEvents []ai.AuthEvent
	vCred2, err := vertexAuth.Login(ctx, &tprovAuthInteraction{
		answers: []string{"adc", "project-id", "us-central1"},
		onInfo:  func(ev ai.AuthEvent) { vertexEvents = append(vertexEvents, ev) },
	})
	wantVertexEnv := ai.ProviderEnv{"GOOGLE_CLOUD_PROJECT": "project-id", "GOOGLE_CLOUD_LOCATION": "us-central1"}
	if err != nil || vCred2 == nil || !maps.Equal(vCred2.Env, wantVertexEnv) {
		t.Fatalf("vertex adc login = %+v, err=%v", vCred2, err)
	}
	if len(vertexEvents) != 1 {
		t.Fatalf("expected 1 vertex info event, got %d", len(vertexEvents))
	}
	if info, ok := vertexEvents[0].(*ai.AuthEventInfo); !ok || len(info.Links) == 0 || info.Links[0].Label == nil || *info.Links[0].Label != "Application Default Credentials" {
		t.Fatalf("unexpected vertex info event: %+v", vertexEvents[0])
	}
	const adcPath = "~/.config/gcloud/application_default_credentials.json"
	vResolved, err := vertexAuth.Resolve(ctx, ai.ApiKeyResolveInput{
		Ctx:        tprovFakeAuthContext{files: map[string]bool{adcPath: true}},
		Credential: &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Env: wantVertexEnv},
	})
	if err != nil || vResolved == nil || !maps.Equal(vResolved.Env, wantVertexEnv) {
		t.Fatalf("vertex resolve = %+v, err=%v", vResolved, err)
	}

	vModels := ai.CreateModels(&ai.CreateModelsOptions{
		AuthContext: tprovFakeAuthContext{
			env:   map[string]string{"GOOGLE_CLOUD_PROJECT": "proj", "GOOGLE_CLOUD_LOCATION": "us-central1"},
			files: map[string]bool{adcPath: true},
		},
	})
	vModels.SetProvider(providers.GoogleVertexProvider())
	vRes, err := vModels.GetAuth(ctx, "google-vertex", nil)
	if err != nil || vRes == nil || vRes.Auth.ApiKey != nil || vRes.Source == nil || !strings.Contains(*vRes.Source, "application default") {
		t.Fatalf("vertex adc getAuth = %+v, err=%v", vRes, err)
	}

	vPartial := ai.CreateModels(&ai.CreateModelsOptions{
		AuthContext: tprovFakeAuthContext{env: map[string]string{"GOOGLE_CLOUD_PROJECT": "proj"}, files: map[string]bool{adcPath: true}},
	})
	vPartial.SetProvider(providers.GoogleVertexProvider())
	if got, err := vPartial.GetAuth(ctx, "google-vertex", nil); err != nil || got != nil {
		t.Fatalf("expected partial vertex auth nil, got %+v, err=%v", got, err)
	}

	vKeyed := ai.CreateModels(&ai.CreateModelsOptions{AuthContext: tprovFakeAuthContext{env: map[string]string{"GOOGLE_CLOUD_API_KEY": "vertex-key"}}})
	vKeyed.SetProvider(providers.GoogleVertexProvider())
	if got, err := vKeyed.GetAuth(ctx, "google-vertex", nil); err != nil || got == nil || got.Auth.ApiKey == nil || *got.Auth.ApiKey != "vertex-key" {
		t.Fatalf("expected keyed vertex auth, got %+v, err=%v", got, err)
	}
}

func TestEnvApiKeyAuth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	auth := ai.EnvApiKeyAuth("Test key", []string{"FIRST_KEY", "SECOND_KEY"})

	stored, err := auth.Resolve(ctx, ai.ApiKeyResolveInput{
		Ctx:        tprovFakeAuthContext{env: map[string]string{"FIRST_KEY": "env"}},
		Credential: &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tprovPtr("stored")},
	})
	if err != nil || stored == nil || stored.Auth.ApiKey == nil || *stored.Auth.ApiKey != "stored" ||
		stored.Source == nil || *stored.Source != "stored credential" {
		t.Fatalf("unexpected stored resolve: %+v, err=%v", stored, err)
	}

	second, err := auth.Resolve(ctx, ai.ApiKeyResolveInput{Ctx: tprovFakeAuthContext{env: map[string]string{"SECOND_KEY": "second"}}})
	if err != nil || second == nil || second.Auth.ApiKey == nil || *second.Auth.ApiKey != "second" ||
		second.Source == nil || *second.Source != "SECOND_KEY" {
		t.Fatalf("unexpected second resolve: %+v, err=%v", second, err)
	}

	empty, err := auth.Resolve(ctx, ai.ApiKeyResolveInput{Ctx: tprovFakeAuthContext{}})
	if err != nil || empty != nil {
		t.Fatalf("expected nil resolve, got %+v, err=%v", empty, err)
	}

	auth2 := ai.EnvApiKeyAuth("Test key", []string{"TEST_KEY"})
	var promptKind ai.AuthPromptKind
	cred, err := auth2.Login(ctx, &tprovAuthInteraction{
		answers: []string{"entered-key"},
		onAsk:   func(p ai.AuthPrompt) { promptKind = p.PromptKind() },
	})
	if err != nil || promptKind != ai.AuthPromptKindSecret || cred == nil || cred.Key == nil || *cred.Key != "entered-key" {
		t.Fatalf("unexpected login result: kind=%s cred=%+v err=%v", promptKind, cred, err)
	}
}

func tprovRecordingStreams(label string, calls *[]string) ai.ProviderStreams {
	respond := func(_ context.Context, model *ai.Model, _ *ai.TranscriptContext, _ ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
		if calls != nil {
			*calls = append(*calls, label+":"+model.Id)
		}
		s := ai.NewAssistantMessageEventStream()
		msg := providers.FauxAssistantMessage(providers.FauxAssistantText("ok"), nil)
		s.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: msg})
		s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg})
		s.End(msg)
		return s
	}
	return ai.ProviderStreams{
		Stream: respond,
		StreamSimple: func(ctx context.Context, model *ai.Model, c *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			return respond(ctx, model, c, opts)
		},
	}
}

func tprovTestModel(apiName, id string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      apiName,
			Provider: "mixed",
			BaseUrl:  "https://example.test/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 10000,
		MaxTokens:     1000,
	}
}

func tprovMustProvider(t *testing.T, opts *ai.CreateProviderOptions) *ai.Provider {
	t.Helper()
	p, err := ai.CreateProvider(opts)
	if err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}
	return p
}

func TestCreateProvider_LazyMixedEnvDeferredAndSupersede(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Lazy deferred capabilities
	loads := 0
	streams := tprovRecordingStreams("deferred", nil)
	streams.FetchDeferred = func(ctx context.Context, model *ai.Model, _ *ai.DeferredHandle, _ *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
		return streams.StreamSimple(ctx, model, tprovContext(), nil)
	}
	lazy := ai.LazyApi(func() (ai.ProviderStreams, error) {
		loads++
		return streams, nil
	}, &ai.LazyApiCapabilities{FetchDeferred: true})
	modelA := tprovTestModel("api-a", "model-a")
	handle := &ai.DeferredHandle{Provider: string(modelA.Provider), ModelId: modelA.Id, Api: modelA.Api, Id: "response-1"}
	if loads != 0 || lazy.CancelDeferred != nil {
		t.Fatalf("unexpected initial lazy state loads=%d cancel=%v", loads, lazy.CancelDeferred != nil)
	}
	res, err := lazy.FetchDeferred(ctx, modelA, handle, nil).Result(ctx)
	if err != nil || res.StopReason != ai.StopReasonStop || loads != 1 {
		t.Fatalf("unexpected lazy fetchDeferred: res=%+v err=%v loads=%d", res, err, loads)
	}

	// Mixed API dispatch
	var calls []string
	mixed := tprovMustProvider(t, &ai.CreateProviderOptions{
		Id:     "mixed",
		Auth:   ai.ProviderAuth{ApiKey: &ai.ApiKeyAuth{Name: "Test", Resolve: func(_ context.Context, _ ai.ApiKeyResolveInput) (*ai.AuthResult, error) { return &ai.AuthResult{}, nil }}},
		Models: []ai.AnyModel{tprovTestModel("api-a", "model-a"), tprovTestModel("api-b", "model-b")},
		ApiByModel: map[string]ai.ProviderStreams{
			"api-a": tprovRecordingStreams("a", &calls),
			"api-b": tprovRecordingStreams("b", &calls),
		},
	})
	m1 := ai.CreateModels(nil)
	m1.SetProvider(mixed)
	hi := "hi"
	reqCtx := &ai.Context{Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &hi}, Timestamp: js.DateNow()}}}
	if _, err := m1.CompleteSimple(ctx, tprovTestModel("api-a", "model-a"), reqCtx, nil); err != nil {
		t.Fatalf("CompleteSimple api-a: %v", err)
	}
	if _, err := m1.CompleteSimple(ctx, tprovTestModel("api-b", "model-b"), reqCtx, nil); err != nil {
		t.Fatalf("CompleteSimple api-b: %v", err)
	}
	if !slices.Equal(calls, []string{"a:model-a", "b:model-b"}) {
		t.Fatalf("mixed calls = %v", calls)
	}

	// Merges provider-resolved env into stream options
	var capturedEnv ai.ProviderEnv
	var capturedApiKey *string
	envModel := tprovTestModel("api-a", "model-a")
	envModel.Provider = "env-provider"
	envRec := tprovRecordingStreams("a", nil)
	envProvider := tprovMustProvider(t, &ai.CreateProviderOptions{
		Id: "env-provider",
		Auth: ai.ProviderAuth{
			ApiKey: &ai.ApiKeyAuth{
				Name: "Test",
				Resolve: func(_ context.Context, _ ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
					return &ai.AuthResult{
						Auth: ai.ModelAuth{ApiKey: tprovPtr("provider-key")},
						Env:  ai.ProviderEnv{"PROVIDER_ONLY": "provider", "SHARED": "provider"},
					}, nil
				},
			},
		},
		Models: []ai.AnyModel{envModel},
		Api: ai.ProviderStreams{
			Stream: func(c context.Context, m *ai.Model, tr *ai.TranscriptContext, o ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
				base := o.BaseStreamOptions()
				capturedEnv = base.Env
				capturedApiKey = base.ApiKey
				return envRec.Stream(c, m, tr, o)
			},
			StreamSimple: func(c context.Context, m *ai.Model, tr *ai.TranscriptContext, o *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
				capturedEnv = o.Env
				capturedApiKey = o.ApiKey
				return envRec.StreamSimple(c, m, tr, o)
			},
		},
	})
	m2 := ai.CreateModels(nil)
	m2.SetProvider(envProvider)
	if _, err := m2.CompleteSimple(ctx, envModel, reqCtx, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey: tprovPtr("request-key"),
					Env:    ai.ProviderEnv{"REQUEST_ONLY": "request", "SHARED": "request"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("CompleteSimple envProvider: %v", err)
	}
	if capturedApiKey == nil || *capturedApiKey != "request-key" ||
		!maps.Equal(capturedEnv, ai.ProviderEnv{"PROVIDER_ONLY": "provider", "REQUEST_ONLY": "request", "SHARED": "request"}) {
		t.Fatalf("captured apiKey=%v env=%v", capturedApiKey, capturedEnv)
	}

	// Deferred fetch and cancellation resolved options
	var fetchedModel *ai.Model
	var fetchedOptions *ai.DeferredFetchOptions
	var cancelledOptions *ai.DeferredCancelOptions
	defModel := tprovTestModel("api-a", "model-a")
	defModel.Provider = "deferred-provider"
	defStreams := tprovRecordingStreams("deferred", nil)
	defStreams.FetchDeferred = func(c context.Context, m *ai.Model, _ *ai.DeferredHandle, opts *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
		fetchedModel = m
		fetchedOptions = opts
		return defStreams.StreamSimple(c, m, tprovContext(), nil)
	}
	defStreams.CancelDeferred = func(_ context.Context, _ *ai.Model, _ *ai.DeferredHandle, opts *ai.DeferredCancelOptions) error {
		cancelledOptions = opts
		return nil
	}
	hdrs := omap.NewMap[string, *string]()
	hdrs.Set("Authorization", tprovPtr("Bearer provider"))
	hdrs.Set("X-Shared", tprovPtr("provider"))
	defProvider := tprovMustProvider(t, &ai.CreateProviderOptions{
		Id: "deferred-provider",
		Auth: ai.ProviderAuth{
			ApiKey: &ai.ApiKeyAuth{
				Name: "Test",
				Resolve: func(_ context.Context, _ ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
					return &ai.AuthResult{
						Auth: ai.ModelAuth{
							ApiKey:  tprovPtr("provider-key"),
							BaseUrl: tprovPtr("https://resolved.test/v1"),
							Headers: hdrs,
						},
						Env: ai.ProviderEnv{"PROVIDER_ONLY": "provider", "SHARED": "provider"},
					}, nil
				},
			},
		},
		Models: []ai.AnyModel{defModel},
		Api:    defStreams,
	})
	m3 := ai.CreateModels(nil)
	m3.SetProvider(defProvider)
	defHandle := &ai.DeferredHandle{Provider: string(defModel.Provider), ModelId: defModel.Id, Api: defModel.Api, Id: "response-1"}
	reqHdrs := omap.NewMap[string, *string]()
	reqHdrs.Set("X-Request", tprovPtr("request"))
	reqHdrs.Set("x-shared", tprovPtr("request"))
	if _, err := m3.FetchDeferred(ctx, defModel, defHandle, &ai.ModelsDeferredFetchOptions{
		DeferredFetchOptions: ai.DeferredFetchOptions{
			Wait: tprovPtr(int64(50)),
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				TimeoutMs: tprovPtr(int64(100)),
				ApiKey:    tprovPtr("request-key"),
				Headers:   reqHdrs,
				Env:       ai.ProviderEnv{"REQUEST_ONLY": "request", "SHARED": "request"},
			},
		},
		ModelsRequestTransforms: ai.ModelsRequestTransforms{
			TransformHeaders: func(h ai.ProviderHeaders) (ai.ProviderHeaders, error) {
				out := h.Clone()
				out.Set("X-Transformed", tprovPtr("yes"))
				return out, nil
			},
		},
	}); err != nil {
		t.Fatalf("FetchDeferred: %v", err)
	}
	if err := m3.CancelDeferred(ctx, defModel, defHandle, &ai.ModelsDeferredCancelOptions{
		DeferredCancelOptions: ai.DeferredCancelOptions{TimeoutMs: tprovPtr(int64(200))},
		ModelsRequestTransforms: ai.ModelsRequestTransforms{
			TransformHeaders: func(h ai.ProviderHeaders) (ai.ProviderHeaders, error) {
				out := h.Clone()
				out.Set("X-Cancel", tprovPtr("yes"))
				return out, nil
			},
		},
	}); err != nil {
		t.Fatalf("CancelDeferred: %v", err)
	}
	if fetchedModel == nil || fetchedModel.BaseUrl != "https://resolved.test/v1" {
		t.Fatalf("fetchedModel.BaseUrl = %+v", fetchedModel)
	}
	if fetchedOptions == nil || *fetchedOptions.Wait != 50 || *fetchedOptions.TimeoutMs != 100 || *fetchedOptions.ApiKey != "request-key" ||
		!maps.Equal(fetchedOptions.Env, ai.ProviderEnv{"PROVIDER_ONLY": "provider", "REQUEST_ONLY": "request", "SHARED": "request"}) {
		t.Fatalf("unexpected fetchedOptions: %+v", fetchedOptions)
	}
	if cancelledOptions == nil || *cancelledOptions.TimeoutMs != 200 || *cancelledOptions.ApiKey != "provider-key" ||
		!maps.Equal(cancelledOptions.Env, ai.ProviderEnv{"PROVIDER_ONLY": "provider", "SHARED": "provider"}) {
		t.Fatalf("unexpected cancelledOptions: %+v", cancelledOptions)
	}

	// Missing API implementation produces stream error
	errRes, err := mixed.StreamSimple(ctx, tprovTestModel("api-ghost", "model-x"), tprovContext(), nil).Result(ctx)
	if err == nil && (errRes == nil || errRes.StopReason != ai.StopReasonError || errRes.ErrorMessage == nil || !strings.Contains(*errRes.ErrorMessage, "no API implementation")) {
		t.Fatalf("expected missing API implementation error, got res=%+v err=%v", errRes, err)
	}
}

func TestCreateProvider_SupersedesOlderDynamicRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var mu sync.Mutex
	fetches := 0
	firstStarted := make(chan struct{})
	firstBlocked := make(chan struct{})

	provider := tprovMustProvider(t, &ai.CreateProviderOptions{
		Id:     "dynamic",
		Auth:   ai.ProviderAuth{ApiKey: &ai.ApiKeyAuth{Name: "Test", Resolve: func(_ context.Context, _ ai.ApiKeyResolveInput) (*ai.AuthResult, error) { return &ai.AuthResult{}, nil }}},
		Models: []ai.AnyModel{},
		FetchModels: func(_ context.Context, _ *ai.RefreshModelsContext) ([]ai.AnyModel, error) {
			mu.Lock()
			fetches++
			cur := fetches
			mu.Unlock()
			if cur == 1 {
				close(firstStarted)
				<-firstBlocked
			}
			id := "listed-1"
			if cur == 2 {
				id = "listed-2"
			}
			return []ai.AnyModel{tprovTestModel("api-a", id)}, nil
		},
		Api: tprovRecordingStreams("a", nil),
	})

	store := ai.NewInMemoryModelsStore()
	models := ai.CreateModels(&ai.CreateModelsOptions{ModelsStore: store})
	models.SetProvider(provider)
	if len(provider.GetModels()) != 0 {
		t.Fatal("expected initially empty dynamic models")
	}

	firstDone := make(chan ai.ModelsRefreshResult, 1)
	go func() {
		r, _ := models.Refresh(ctx, &ai.ModelsRefreshOptions{Providers: []string{"dynamic"}})
		firstDone <- r
	}()
	<-firstStarted
	secondRes, err := models.Refresh(ctx, &ai.ModelsRefreshOptions{Providers: []string{"dynamic"}})
	if err != nil || secondRes.Aborted {
		t.Fatalf("second refresh failed: %+v err=%v", secondRes, err)
	}
	firstRes := <-firstDone
	if firstRes.Aborted {
		t.Fatalf("expected superseded first refresh aborted=false, got %+v", firstRes)
	}
	if got := provider.GetModels(); len(got) != 1 || got[0].Id != "listed-2" {
		t.Fatalf("unexpected provider models after second refresh: %+v", got)
	}
	stored, err := store.Read(ctx, "dynamic", nil)
	if err != nil || stored == nil || len(stored.Models) != 1 || stored.Models[0].(*ai.Model).Id != "listed-2" {
		t.Fatalf("unexpected store entry: %+v, err=%v", stored, err)
	}
	close(firstBlocked)
	time.Sleep(5 * time.Millisecond)
	if got := provider.GetModels(); len(got) != 1 || got[0].Id != "listed-2" {
		t.Fatalf("unexpected provider models after stale completion: %+v", got)
	}
}

func TestBuiltinFauxProvider_DeferredAndCancellation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	hi := "hi"
	reqCtx := &ai.Context{Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &hi}, Timestamp: js.DateNow()}}}

	faux1, err := providers.FauxProvider(nil)
	if err != nil {
		t.Fatalf("FauxProvider: %v", err)
	}
	m1 := ai.CreateModels(nil)
	m1.SetProvider(faux1.Provider)
	faux1.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("hello from faux"), nil)},
	})
	res, err := m1.CompleteSimple(ctx, faux1.GetModel(), reqCtx, nil)
	if err != nil || res.StopReason != ai.StopReasonStop {
		t.Fatalf("CompleteSimple failed: res=%+v err=%v", res, err)
	}
	if faux1.State.CallCount != 1 {
		t.Fatalf("callCount = %d, want 1", faux1.State.CallCount)
	}

	pendingFetches := 1
	pollAfterMs := int64(25)
	faux2, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{
		Deferred: &providers.FauxDeferredOptions{PendingFetches: &pendingFetches, PollAfterMs: &pollAfterMs},
	})
	if err != nil {
		t.Fatalf("FauxProvider deferred: %v", err)
	}
	m2 := ai.CreateModels(nil)
	m2.SetProvider(faux2.Provider)
	faux2.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
	})
	model := faux2.GetModel()
	win := ai.DeferredWindow1h
	submission := m2.StreamSimple(ctx, model, reqCtx, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			Deferred: ai.SimpleStreamDeferredValue{V: &ai.SimpleStreamDeferredWindow{Window: &win}},
		},
	})
	var evTypes []string
	for ev := range submission.All() {
		switch e := ev.(type) {
		case *ai.AssistantMessageEventStart:
			evTypes = append(evTypes, e.Type)
		case *ai.AssistantMessageEventDone:
			evTypes = append(evTypes, e.Type)
		}
	}
	deferred, err := submission.Result(ctx)
	if err != nil || !slices.Equal(evTypes, []string{"start", "done"}) || deferred.StopReason != ai.StopReasonDeferred || len(deferred.Content) != 0 {
		t.Fatalf("unexpected deferred submission: evTypes=%v deferred=%+v err=%v", evTypes, deferred, err)
	}
	if deferred.Deferred == nil || deferred.Deferred.PollAfterMs == nil || *deferred.Deferred.PollAfterMs != 25 ||
		deferred.Deferred.Provider != string(model.Provider) || deferred.Deferred.ModelId != model.Id || deferred.Deferred.Api != model.Api {
		t.Fatalf("unexpected deferred handle: %+v", deferred.Deferred)
	}

	pending, err := m2.FetchDeferred(ctx, model, deferred.Deferred, nil)
	if err != nil || pending.StopReason != ai.StopReasonDeferred {
		t.Fatalf("expected still deferred, got %+v err=%v", pending, err)
	}
	var wait0 int64
	ready, err := m2.FetchDeferred(ctx, model, deferred.Deferred, &ai.ModelsDeferredFetchOptions{
		DeferredFetchOptions: ai.DeferredFetchOptions{Wait: &wait0},
	})
	if err != nil || ready.StopReason != ai.StopReasonStop || ready.Usage.TotalTokens == 0 ||
		faux2.State.CallCount != 1 || faux2.State.DeferredFetchCount != 2 {
		t.Fatalf("unexpected ready deferred: %+v err=%v state=%+v", ready, err, faux2.State)
	}

	// Factory error + cancellation in-band
	faux3, err := providers.FauxProvider(nil)
	if err != nil {
		t.Fatalf("FauxProvider: %v", err)
	}
	m3 := ai.CreateModels(nil)
	m3.SetProvider(faux3.Provider)
	faux3.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			return nil, errors.New("deferred failed")
		}),
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("cancelled"), nil)},
	})
	dModel := faux3.GetModel()
	failSub, err := m3.CompleteSimple(ctx, dModel, reqCtx, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			Deferred: ai.SimpleStreamDeferredValue{V: ai.SimpleStreamDeferredBool(true)},
		},
	})
	if err != nil || failSub.Deferred == nil {
		t.Fatalf("expected deferred handle, got %+v err=%v", failSub, err)
	}
	failed, _ := m3.FetchDeferred(ctx, dModel, failSub.Deferred, nil)
	if failed == nil || failed.StopReason != ai.StopReasonError || failed.ErrorMessage == nil || *failed.ErrorMessage != "deferred failed" {
		t.Fatalf("expected deferred failed error, got %+v", failed)
	}

	cancelSub, err := m3.CompleteSimple(ctx, dModel, reqCtx, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			Deferred: ai.SimpleStreamDeferredValue{V: ai.SimpleStreamDeferredBool(true)},
		},
	})
	if err != nil || cancelSub.Deferred == nil {
		t.Fatalf("expected deferred handle, got %+v err=%v", cancelSub, err)
	}
	if err := m3.CancelDeferred(ctx, dModel, cancelSub.Deferred, nil); err != nil {
		t.Fatalf("CancelDeferred: %v", err)
	}
	cancelled, _ := m3.FetchDeferred(ctx, dModel, cancelSub.Deferred, nil)
	if cancelled == nil || cancelled.StopReason != ai.StopReasonError || cancelled.ErrorMessage == nil || !strings.Contains(*cancelled.ErrorMessage, "was cancelled") {
		t.Fatalf("expected cancelled error message, got %+v", cancelled)
	}
}
