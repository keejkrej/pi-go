// Ported from packages/ai/src/providers/github-copilot.ts (pi v1.5.2).

package providers

import (
	"context"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
)

// Copilot serves System One at `/v1/systemone`, while its chat APIs have no version prefix.
// The login replaces the catalog base URL with the account's endpoint, so the prefix is added per request.
func gcCopilotSystemOne(classifier ai.ProviderClassifier) ai.ProviderClassifier {
	return ai.ProviderClassifier{
		Classify: func(ctx context.Context, model *ai.ClassifierModel, c *ai.ClassifierContext, options *ai.ClassifierOptions) (*ai.ClassifierResult, error) {
			cp := *model
			cp.BaseUrl = strings.TrimRight(model.BaseUrl, "/") + "/v1"
			return classifier.Classify(ctx, &cp, c, options)
		},
	}
}

func gcAvailableModelIds(credential ai.Credential) (map[string]struct{}, bool) {
	oauth, ok := credential.(*ai.OAuthCredential)
	if !ok || oauth == nil || oauth.Extra == nil {
		return nil, false
	}
	raw, exists := oauth.Extra.Get("availableModelIds")
	if !exists || raw == nil {
		return nil, false
	}
	set := make(map[string]struct{})
	switch v := raw.(type) {
	case []string:
		for _, id := range v {
			set[id] = struct{}{}
		}
		return set, true
	case []any:
		for _, elem := range v {
			s, ok := elem.(string)
			if !ok {
				return nil, false
			}
			set[s] = struct{}{}
		}
		return set, true
	default:
		return nil, false
	}
}

// GithubCopilotProvider constructs the built-in GitHub Copilot provider.
func GithubCopilotProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      "github-copilot",
		Name:    allPtr("GitHub Copilot"),
		BaseUrl: allPtr("https://api.individual.githubcopilot.com"),
		Auth: ai.ProviderAuth{
			ApiKey: ai.EnvApiKeyAuth("GitHub Copilot token", []string{"COPILOT_GITHUB_TOKEN"}),
			OAuth: ai.LazyOAuth(ai.LazyOAuthInput{
				Name:           "GitHub Copilot",
				IsSubscription: true,
				Load:           aioauth.LoadGitHubCopilotOAuth,
			}),
		},
		Models: allCatalogModels(catalog.GithubCopilotModels, nil, catalog.GithubCopilotClassifierModels),
		FilterModels: func(models []*ai.Model, credential ai.Credential) []*ai.Model {
			available, ok := gcAvailableModelIds(credential)
			if !ok {
				return models
			}
			out := make([]*ai.Model, 0, len(models))
			for _, m := range models {
				if _, keep := available[m.Id]; keep {
					out = append(out, m)
				}
			}
			return out
		},
		ApiByModel: map[string]ai.ProviderStreams{
			"anthropic-messages": api.AnthropicMessagesApi(),
			"openai-completions": api.OpenAICompletionsApi(),
			"openai-responses":   api.OpenAIResponsesApi(),
		},
		Classifiers: map[string]ai.ProviderClassifier{
			"typesafe-system-one": gcCopilotSystemOne(api.TypesafeSystemOneApi()),
		},
	})
}
