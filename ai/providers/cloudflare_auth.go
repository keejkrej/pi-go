// Ported from packages/ai/src/providers/cloudflare-auth.ts (pi v1.5.2).

package providers

import (
	"context"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	caCloudflareApiKey    = "CLOUDFLARE_API_KEY"
	caCloudflareAccountId = "CLOUDFLARE_ACCOUNT_ID"
	caCloudflareGatewayId = "CLOUDFLARE_GATEWAY_ID"
)

type caCloudflareAuthKind string

const (
	caKindWorkersAI caCloudflareAuthKind = "workers-ai"
	caKindAIGateway caCloudflareAuthKind = "ai-gateway"
)

type caResolvedCloudflareEnv struct {
	apiKey string
	env    ai.ProviderEnv
	source string
}

func caAbortError(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil && cause != ctx.Err() {
		return cause
	}
	if ctx.Err() == context.DeadlineExceeded {
		return ctx.Err()
	}
	return ai.AbortNewError("")
}

func caResolveValue(ctx context.Context, name string, authCtx ai.AuthContext, credential *ai.ApiKeyCredential) (string, bool, error) {
	// Per-field merge: prefer the credential value, fall back to ambient env.
	// A credential carrying only the API key must still pick up the account /
	// gateway id from the environment.
	if credential != nil {
		if name == caCloudflareApiKey {
			if credential.Key != nil {
				return *credential.Key, true, nil
			}
		} else if credential.Env != nil {
			if val, ok := credential.Env[name]; ok {
				return val, true, nil
			}
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return "", false, caAbortError(ctx)
	}
	if authCtx == nil {
		return "", false, nil
	}
	value, err := authCtx.Env(ctx, name)
	if err != nil {
		return "", false, err
	}
	if ctx != nil && ctx.Err() != nil {
		return "", false, caAbortError(ctx)
	}
	if value == nil {
		return "", false, nil
	}
	return *value, true, nil
}

func caResolveCloudflareEnv(
	ctx context.Context,
	kind caCloudflareAuthKind,
	authCtx ai.AuthContext,
	credential *ai.ApiKeyCredential,
) (*caResolvedCloudflareEnv, error) {
	apiKey, okKey, err := caResolveValue(ctx, caCloudflareApiKey, authCtx, credential)
	if err != nil {
		return nil, err
	}
	accountId, okAcc, err := caResolveValue(ctx, caCloudflareAccountId, authCtx, credential)
	if err != nil {
		return nil, err
	}
	var gatewayId string
	var okGw bool
	if kind == caKindAIGateway {
		gatewayId, okGw, err = caResolveValue(ctx, caCloudflareGatewayId, authCtx, credential)
		if err != nil {
			return nil, err
		}
	}

	if !okKey || apiKey == "" || !okAcc || accountId == "" || (kind == caKindAIGateway && (!okGw || gatewayId == "")) {
		return nil, nil
	}

	env := ai.ProviderEnv{
		caCloudflareAccountId: accountId,
	}
	if gatewayId != "" {
		env[caCloudflareGatewayId] = gatewayId
	}
	source := caCloudflareApiKey
	if credential != nil {
		source = "stored credential"
	}
	return &caResolvedCloudflareEnv{
		apiKey: apiKey,
		env:    env,
		source: source,
	}, nil
}

// CloudflareWorkersAIAuth returns API-key auth for Cloudflare Workers AI.
func CloudflareWorkersAIAuth() *ai.ApiKeyAuth {
	return &ai.ApiKeyAuth{
		Name: "Cloudflare API key",
		Login: func(ctx context.Context, interaction ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
			key, err := interaction.Prompt(ctx, &ai.AuthPromptSecret{
				Type:          ai.AuthPromptKindSecret,
				Message:       "Enter Cloudflare API key",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			accountId, err := interaction.Prompt(ctx, &ai.AuthPromptText{
				Type:          ai.AuthPromptKindText,
				Message:       "Enter Cloudflare account ID",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			return &ai.ApiKeyCredential{
				Type: ai.AuthTypeApiKey,
				Key:  &key,
				Env:  ai.ProviderEnv{caCloudflareAccountId: accountId},
			}, nil
		},
		Resolve: func(ctx context.Context, input ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
			resolved, err := caResolveCloudflareEnv(ctx, caKindWorkersAI, input.Ctx, input.Credential)
			if err != nil || resolved == nil {
				return nil, err
			}
			key := resolved.apiKey
			src := resolved.source
			return &ai.AuthResult{
				Auth:   ai.ModelAuth{ApiKey: &key},
				Env:    resolved.env,
				Source: &src,
			}, nil
		},
	}
}

// CloudflareAIGatewayAuth returns API-key auth for Cloudflare AI Gateway.
func CloudflareAIGatewayAuth() *ai.ApiKeyAuth {
	return &ai.ApiKeyAuth{
		Name: "Cloudflare API key",
		Login: func(ctx context.Context, interaction ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
			key, err := interaction.Prompt(ctx, &ai.AuthPromptSecret{
				Type:          ai.AuthPromptKindSecret,
				Message:       "Enter Cloudflare API key",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			accountId, err := interaction.Prompt(ctx, &ai.AuthPromptText{
				Type:          ai.AuthPromptKindText,
				Message:       "Enter Cloudflare account ID",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			gatewayId, err := interaction.Prompt(ctx, &ai.AuthPromptText{
				Type:          ai.AuthPromptKindText,
				Message:       "Enter Cloudflare AI Gateway ID",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			return &ai.ApiKeyCredential{
				Type: ai.AuthTypeApiKey,
				Key:  &key,
				Env: ai.ProviderEnv{
					caCloudflareAccountId: accountId,
					caCloudflareGatewayId: gatewayId,
				},
			}, nil
		},
		Resolve: func(ctx context.Context, input ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
			resolved, err := caResolveCloudflareEnv(ctx, caKindAIGateway, input.Ctx, input.Credential)
			if err != nil || resolved == nil {
				return nil, err
			}
			headers := omap.NewMap[string, *string]()
			bearer := "Bearer " + resolved.apiKey
			headers.Set("cf-aig-authorization", &bearer)
			headers.Set("Authorization", nil)
			headers.Set("x-api-key", nil)
			src := resolved.source
			return &ai.AuthResult{
				Auth:   ai.ModelAuth{Headers: headers},
				Env:    resolved.env,
				Source: &src,
			}, nil
		},
	}
}
