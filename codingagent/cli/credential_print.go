// Ported from packages/coding-agent/src/cli/credential-print.ts (pi v1.5.2).

package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

const cpDefaultBearerTokenMinExpiryMs int64 = 30 * 60_000

type cpProviderTarget struct {
	id    string
	model *ai.Model
}

type cpCredentialEntry struct {
	providerId string
	value      string
}

// ResolveCredentialForPrint resolves one configured provider credential for
// `auth print-api-key` or `auth print-bearer-token`, refreshing OAuth tokens when needed.
func ResolveCredentialForPrint(
	ctx context.Context,
	args core.Args,
	modelRuntime *core.ModelRuntime,
	kind AuthCommandKind,
	minExpiryMs *int64,
) (string, error) {
	cliProvider, cliModel, err := ValidateAuthCommandArgs(args, kind)
	if err != nil {
		return "", err
	}

	listedCredentials, err := modelRuntime.ListCredentials(ctx, nil)
	if err != nil {
		return "", err
	}
	credentialTypes := make(map[string]ai.AuthType, len(listedCredentials))
	for _, cred := range listedCredentials {
		credentialTypes[cred.ProviderId] = cred.Type
	}

	var providers []cpProviderTarget
	if cliProvider != nil {
		provider := modelRuntime.GetProvider(*cliProvider)
		if provider == nil {
			return "", &AuthCommandError{
				Message: fmt.Sprintf(`Unknown provider "%s". Use --list-models to see available providers.`, *cliProvider),
			}
		}
		if cliModel != nil {
			resolved := core.ResolveCliModel(core.ResolveCliModelOptions{
				CliProvider:  &provider.Id,
				CliModel:     cliModel,
				ModelRuntime: modelRuntime,
			})
			if resolved.Error != nil || resolved.Model == nil {
				msg := "Unable to resolve the requested provider/model"
				if resolved.Error != nil {
					msg = *resolved.Error
				}
				return "", &AuthCommandError{Message: msg}
			}
			providers = append(providers, cpProviderTarget{id: provider.Id, model: resolved.Model})
		} else {
			providers = append(providers, cpProviderTarget{id: provider.Id})
		}
	} else {
		models := modelRuntime.GetModels(nil)
		for _, provider := range modelRuntime.GetProviders() {
			if _, ok := credentialTypes[provider.Id]; !ok {
				continue
			}
			provId := provider.Id
			resolved := core.ResolveModelReference(
				core.ModelReferenceRequest{
					Text:           *cliModel,
					Provider:       &provId,
					ThinkingSuffix: core.ThinkingSuffixStrict,
					Fuzzy:          true,
				},
				models,
				func(_ string) bool { return true },
			)
			if resolved.Status == core.ModelReferenceResolved && resolved.Model != nil {
				providers = append(providers, cpProviderTarget{id: provider.Id, model: resolved.Model})
			}
		}
		if len(providers) == 0 {
			return "", &AuthCommandError{
				Message: fmt.Sprintf(`Model "%s" not found. Use --list-models to see available models.`, *cliModel),
			}
		}
	}

	var credentials []cpCredentialEntry
	for _, provider := range providers {
		credType, hasType := credentialTypes[provider.id]
		if kind == AuthCommandKindApiKey && hasType && credType == ai.AuthTypeOAuth {
			continue
		}
		if kind == AuthCommandKindBearerToken && (!hasType || credType != ai.AuthTypeOAuth) {
			continue
		}

		var overrides *ai.AuthResolutionOverrides
		if kind == AuthCommandKindBearerToken {
			minMs := cpDefaultBearerTokenMinExpiryMs
			if minExpiryMs != nil {
				minMs = *minExpiryMs
			}
			overrides = &ai.AuthResolutionOverrides{MinOAuthValidityMs: &minMs}
		}

		var auth *ai.AuthResult
		if provider.model != nil {
			auth, err = modelRuntime.GetAuthModel(ctx, provider.model, overrides)
		} else {
			auth, err = modelRuntime.GetAuth(ctx, provider.id, overrides)
		}
		if err != nil {
			return "", err
		}
		if value := GetAuthCredential(auth); value != nil {
			credentials = append(credentials, cpCredentialEntry{providerId: provider.id, value: *value})
		}
	}

	if len(credentials) == 1 {
		return credentials[0].value, nil
	}
	if len(credentials) == 0 {
		var providerId string
		if len(providers) > 0 {
			providerId = providers[0].id
		}
		credType, hasType := credentialTypes[providerId]
		if cliProvider != nil && kind == AuthCommandKindApiKey && hasType && credType == ai.AuthTypeOAuth {
			return "", &AuthCommandError{
				Message: fmt.Sprintf(`Provider "%s" is configured with OAuth, not an API key`, providerId),
			}
		}
		if cliProvider != nil && kind == AuthCommandKindBearerToken && (!hasType || credType != ai.AuthTypeOAuth) {
			return "", &AuthCommandError{
				Message: fmt.Sprintf(`Provider "%s" is not configured with an OAuth bearer token`, providerId),
			}
		}
		label := "OAuth bearer token"
		if kind == AuthCommandKindApiKey {
			label = "API key"
		}
		return "", &AuthCommandError{Message: fmt.Sprintf("No usable %s is configured", label)}
	}

	ids := make([]string, len(credentials))
	for i, cred := range credentials {
		ids[i] = cred.providerId
	}
	return "", &AuthCommandError{
		Message: fmt.Sprintf("Multiple configured providers matched (%s). Specify --provider.", strings.Join(ids, ", ")),
	}
}
