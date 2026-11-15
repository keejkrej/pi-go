// Ported from packages/ai/src/providers/anthropic.ts (pi v1.5.2).

package providers

import (
	"context"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
	"github.com/keejkrej/pi-go/internal/omap"
)

func anthApiKeyAuth() *ai.ApiKeyAuth {
	return &ai.ApiKeyAuth{
		Name: "Anthropic API key",
		Login: func(ctx context.Context, interaction ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
			if ctx != nil && ctx.Err() != nil {
				return nil, allAbortReason(ctx)
			}
			key, err := interaction.Prompt(ctx, &ai.AuthPromptSecret{
				Type:          ai.AuthPromptKindSecret,
				Message:       "Enter Anthropic API key",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			if ctx != nil && ctx.Err() != nil {
				return nil, allAbortReason(ctx)
			}
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
		},
		Resolve: func(ctx context.Context, input ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
			if ctx != nil && ctx.Err() != nil {
				return nil, allAbortReason(ctx)
			}
			if input.Credential != nil && input.Credential.Key != nil && *input.Credential.Key != "" {
				k := *input.Credential.Key
				return &ai.AuthResult{
					Auth:   ai.ModelAuth{ApiKey: &k},
					Env:    input.Credential.Env,
					Source: allPtr("stored credential"),
				}, nil
			}

			authToken, err := input.Ctx.Env(ctx, ai.AnthropicAuthTokenEnv)
			if err != nil {
				return nil, err
			}
			if ctx != nil && ctx.Err() != nil {
				return nil, allAbortReason(ctx)
			}
			if authToken != nil && *authToken != "" {
				headers := omap.NewMap[string, *string]()
				bearer := "Bearer " + *authToken
				headers.Set("Authorization", &bearer)
				return &ai.AuthResult{
					Auth:   ai.ModelAuth{Headers: headers},
					Source: allPtr(ai.AnthropicAuthTokenEnv),
				}, nil
			}

			for _, envVar := range []string{ai.AnthropicOAuthTokenEnv, ai.AnthropicApiKeyEnv} {
				apiKey, err := input.Ctx.Env(ctx, envVar)
				if err != nil {
					return nil, err
				}
				if ctx != nil && ctx.Err() != nil {
					return nil, allAbortReason(ctx)
				}
				if apiKey != nil && *apiKey != "" {
					k := *apiKey
					return &ai.AuthResult{
						Auth:   ai.ModelAuth{ApiKey: &k},
						Source: allPtr(envVar),
					}, nil
				}
			}

			// Workload identity federation: the Anthropic SDK exchanges the identity
			// token for a short-lived access token and refreshes it itself. Last in
			// line so keys and ANTHROPIC_AUTH_TOKEN keep winning, as in the SDK. The
			// ids are provider config rather than auth, so they travel in `env`.
			federation := make(ai.ProviderEnv)
			for _, envVar := range []string{
				ai.AnthropicFederationRuleIdEnv,
				ai.AnthropicOrganizationIdEnv,
				ai.AnthropicIdentityTokenFileEnv,
			} {
				val, err := input.Ctx.Env(ctx, envVar)
				if err != nil {
					return nil, err
				}
				if ctx != nil && ctx.Err() != nil {
					return nil, allAbortReason(ctx)
				}
				if val == nil || *val == "" {
					return nil, nil
				}
				federation[envVar] = *val
			}
			for _, envVar := range []string{
				ai.AnthropicServiceAccountIdEnv,
				ai.AnthropicWorkspaceIdEnv,
			} {
				val, err := input.Ctx.Env(ctx, envVar)
				if err != nil {
					return nil, err
				}
				if ctx != nil && ctx.Err() != nil {
					return nil, allAbortReason(ctx)
				}
				if val != nil && *val != "" {
					federation[envVar] = *val
				}
			}
			return &ai.AuthResult{
				Auth:   ai.ModelAuth{},
				Env:    federation,
				Source: allPtr("workload identity federation"),
			}, nil
		},
	}
}

// AnthropicProvider constructs the built-in Anthropic provider.
func AnthropicProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:      "anthropic",
		Name:    allPtr("Anthropic"),
		BaseUrl: allPtr("https://api.anthropic.com"),
		Auth: ai.ProviderAuth{
			ApiKey: anthApiKeyAuth(),
			OAuth: ai.LazyOAuth(ai.LazyOAuthInput{
				Name:           "Anthropic (Claude Pro/Max)",
				IsSubscription: true,
				Load:           aioauth.LoadAnthropicOAuth,
			}),
		},
		Models: allCatalogModels(catalog.AnthropicModels, nil, nil),
		Api:    api.AnthropicMessagesApi(),
	})
}
