// Ported from packages/ai/src/providers/openrouter.ts (pi v1.5.2).

package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	openpBaseUrl             = "https://openrouter.ai/api/v1"
	openpUserModelsTimeoutMs = 15_000
)

var openpHttpClient *http.Client

func openpCredentialKey(cred ai.Credential) string {
	switch c := cred.(type) {
	case *ai.OAuthCredential:
		if c != nil {
			return c.Access
		}
	case *ai.ApiKeyCredential:
		if c != nil && c.Key != nil {
			return *c.Key
		}
	}
	return ""
}

// PersonalizeOpenRouterCatalog restricts an OpenRouter catalog to the chat models the key may use,
// with the key's limits and prices from `GET /models/user`. Image and classifier models are
// returned unchanged. A missing limit keeps the catalog value. The request uses the provider's
// configured base URL, so regional endpoints apply their region's filtering. When the endpoint
// rejects the key (401/403) or does not exist (404), the catalog is returned unchanged.
func PersonalizeOpenRouterCatalog(
	ctx context.Context,
	models []ai.AnyModel,
	refresh *ai.RefreshModelsContext,
) ([]ai.AnyModel, error) {
	return openpPersonalizeWithClient(ctx, models, refresh, openpHttpClient)
}

func openpPersonalizeWithClient(
	ctx context.Context,
	models []ai.AnyModel,
	refresh *ai.RefreshModelsContext,
	client *http.Client,
) ([]ai.AnyModel, error) {
	if refresh == nil {
		return slices.Clone(models), nil
	}
	key := openpCredentialKey(refresh.Credential)
	if key == "" {
		return slices.Clone(models), nil
	}
	baseUrl := openpBaseUrl
	if refresh.BaseUrl != nil && *refresh.BaseUrl != "" {
		baseUrl = *refresh.BaseUrl
	}
	endpoint := strings.TrimRight(baseUrl, "/") + "/models/user"

	reqCtx, cancel := context.WithTimeout(ai.OperationSignal(ctx), openpUserModelsTimeoutMs*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	if client == nil {
		client = ai.NodeHttpProxyDefaultClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusNotFound {
		return slices.Clone(models), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenRouter model list request failed: %d", resp.StatusCode)
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	parsed, err := jsonx.Parse(rawBody)
	if err != nil {
		return nil, fmt.Errorf("Invalid OpenRouter model list response: %w", err)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("Invalid OpenRouter model list response")
	}
	rawData, hasData := obj.Get("data")
	entries, ok := rawData.([]any)
	if !hasData || !ok {
		return nil, fmt.Errorf("Invalid OpenRouter model list response")
	}

	allowed := make(map[string]OpenRouterModel, len(entries))
	for _, rawEntry := range entries {
		entryObj, ok := rawEntry.(*jsonx.Object)
		if !ok || entryObj == nil {
			continue
		}
		id, hasId := entryObj.GetString("id")
		if !hasId {
			continue
		}
		var m OpenRouterModel
		if err := jsonx.Decode(entryObj, &m); err == nil && m.Id == id {
			allowed[m.Id] = m
		}
	}

	out := make([]ai.AnyModel, 0, len(models))
	for _, m := range models {
		if !ai.IsModelType(m, ai.ModelTypeChat) {
			out = append(out, m)
			continue
		}
		chat, ok := m.(*ai.Model)
		if !ok || chat == nil {
			continue
		}
		entry, ok := allowed[chat.Id]
		if !ok {
			continue
		}
		cp := *chat
		// OpenRouter reports a missing limit as absent, null or 0, like the generator's fallback.
		if entry.TopProvider != nil {
			if cw, ok := entry.TopProvider.ContextLength.Get(); ok && cw != 0 {
				cp.ContextWindow = cw
			} else if cw, ok := entry.ContextLength.Get(); ok && cw != 0 {
				cp.ContextWindow = cw
			}
			if mt, ok := entry.TopProvider.MaxCompletionTokens.Get(); ok && mt != 0 {
				cp.MaxTokens = mt
			}
		} else if cw, ok := entry.ContextLength.Get(); ok && cw != 0 {
			cp.ContextWindow = cw
		}
		if entry.Pricing != nil {
			cp.Cost = OpenRouterCost(entry.Pricing)
		}
		out = append(out, &cp)
	}
	return out, nil
}

// OpenrouterProvider constructs the built-in OpenRouter provider.
func OpenrouterProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      "openrouter",
		Name:    allPtr("OpenRouter"),
		BaseUrl: allPtr(openpBaseUrl),
		Auth: ai.ProviderAuth{
			ApiKey: ai.EnvApiKeyAuth("OpenRouter API key", []string{"OPENROUTER_API_KEY"}),
			OAuth: ai.LazyOAuth(ai.LazyOAuthInput{
				Name:       "OpenRouter OAuth",
				LoginLabel: allPtr("Sign in with OpenRouter"),
				Load:       aioauth.LoadOpenRouterOAuth,
			}),
		},
		Models: allCatalogModels(catalog.OpenrouterModels, catalog.OpenrouterImageModels, catalog.OpenrouterClassifierModels),
		ApiByModel: map[string]ai.ProviderStreams{
			"anthropic-messages": api.AnthropicMessagesApi(),
			"openai-completions": api.OpenAICompletionsApi(),
		},
		Images: map[string]ai.ProviderImages{
			"openrouter-images": api.OpenrouterImagesApi(),
		},
		// OpenRouter serves TypeSafe's System One protocol at /api/v1/systemone.
		Classifiers: map[string]ai.ProviderClassifier{
			"typesafe-system-one": api.TypesafeSystemOneApi(),
		},
	})
}
