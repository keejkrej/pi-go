// Ported from packages/coding-agent/src/cli/auth-check.ts (pi v1.5.2).

package cli

import (
	"context"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

// AuthCheckStatus reports provider credential readiness for `pi auth check`.
type AuthCheckStatus string

const (
	AuthCheckStatusReady    AuthCheckStatus = "ready"
	AuthCheckStatusNotReady AuthCheckStatus = "not_ready"
	AuthCheckStatusInvalid  AuthCheckStatus = "invalid"
)

// AuthCheckReason identifies why `pi auth check` returned not_ready or invalid.
type AuthCheckReason string

const (
	AuthCheckReasonProviderNotFound         AuthCheckReason = "provider_not_found"
	AuthCheckReasonCredentialsNotConfigured AuthCheckReason = "credentials_not_configured"
	AuthCheckReasonCredentialNotAvailable   AuthCheckReason = "credential_not_available"
	AuthCheckReasonInvalidState             AuthCheckReason = "invalid_state"
)

// AuthCheckOptions configures CheckProviderAuth and GetProviderCredential.
type AuthCheckOptions struct {
	Refresh bool
}

// AuthCheckResult is the JSON-serializable result of `pi auth check`.
type AuthCheckResult struct {
	Status   AuthCheckStatus  `json:"status"`
	Provider string           `json:"provider"`
	Reason   *AuthCheckReason `json:"reason,omitzero"`
	AuthType *ai.AuthType     `json:"authType,omitzero"`
}

// CheckProviderAuth inspects stored and ambient auth readiness for the provider
// named by args.Provider or resolved from args.Model.
func CheckProviderAuth(
	ctx context.Context,
	args core.Args,
	modelRuntime *core.ModelRuntime,
	options ...*AuthCheckOptions,
) (AuthCheckResult, error) {
	refresh := false
	if len(options) > 0 && options[0] != nil {
		refresh = options[0].Refresh
	}

	cliProvider, cliModel, err := ValidateAuthCommandArgs(args, AuthCommandKindCheck)
	if err != nil {
		return AuthCheckResult{}, err
	}

	var provider string
	if cliProvider != nil {
		provider = *cliProvider
	}
	if cliModel != nil {
		resolved := core.ResolveCliModel(core.ResolveCliModelOptions{
			CliProvider:  cliProvider,
			CliModel:     cliModel,
			ModelRuntime: modelRuntime,
		})
		if resolved.Error != nil || resolved.Model == nil {
			msg := `Unable to resolve model "` + *cliModel + `"`
			if resolved.Error != nil {
				msg = *resolved.Error
			}
			return AuthCheckResult{}, &AuthCommandError{Message: msg}
		}
		provider = string(resolved.Model.Provider)
	}
	if provider == "" {
		return AuthCheckResult{}, &AuthCommandError{Message: "Unable to resolve an auth provider"}
	}

	reasonInvalid := AuthCheckReasonInvalidState
	if modelRuntime.GetError() != nil {
		return AuthCheckResult{Status: AuthCheckStatusInvalid, Provider: provider, Reason: &reasonInvalid}, nil
	}
	if modelRuntime.GetProvider(provider) == nil {
		reasonMissing := AuthCheckReasonProviderNotFound
		return AuthCheckResult{Status: AuthCheckStatusNotReady, Provider: provider, Reason: &reasonMissing}, nil
	}

	auth, err := modelRuntime.CheckAuth(ctx, provider, nil)
	if err != nil {
		return AuthCheckResult{Status: AuthCheckStatusInvalid, Provider: provider, Reason: &reasonInvalid}, nil
	}
	reasonUnconfigured := AuthCheckReasonCredentialsNotConfigured
	if auth == nil {
		return AuthCheckResult{Status: AuthCheckStatusNotReady, Provider: provider, Reason: &reasonUnconfigured}, nil
	}
	if refresh {
		resolvedAuth, err := modelRuntime.GetAuth(ctx, provider, nil)
		if err != nil {
			return AuthCheckResult{Status: AuthCheckStatusInvalid, Provider: provider, Reason: &reasonInvalid}, nil
		}
		if resolvedAuth == nil {
			return AuthCheckResult{Status: AuthCheckStatusNotReady, Provider: provider, Reason: &reasonUnconfigured}, nil
		}
	}

	authType := auth.Type
	return AuthCheckResult{Status: AuthCheckStatusReady, Provider: provider, AuthType: &authType}, nil
}

// GetProviderCredential returns the stored OAuth access token (when Refresh is false)
// or resolves/refreshes the runtime auth credential for providerId.
func GetProviderCredential(
	ctx context.Context,
	providerId string,
	modelRuntime *core.ModelRuntime,
	credentials ai.CredentialStore,
	options AuthCheckOptions,
) (*string, error) {
	credential, err := credentials.Read(ctx, providerId, nil)
	if err != nil {
		return nil, err
	}
	if !options.Refresh {
		if oauth, ok := credential.(*ai.OAuthCredential); ok && oauth != nil {
			access := oauth.Access
			return &access, nil
		}
	}
	auth, err := modelRuntime.GetAuth(ctx, providerId, nil)
	if err != nil {
		return nil, err
	}
	return GetAuthCredential(auth), nil
}

// CreateAuthCheckModelRuntime creates a local ModelRuntime backed by an in-memory
// catalog store without issuing startup network requests.
func CreateAuthCheckModelRuntime(ctx context.Context, credentials ai.CredentialStore) (*core.ModelRuntime, error) {
	refreshOnCreate := false
	return core.CreateModelRuntime(ctx, &core.CreateModelRuntimeOptions{
		Credentials:       credentials,
		ModelsStore:       core.NewInMemoryCodingAgentModelsStore(),
		AllowModelNetwork: false,
		RefreshOnCreate:   &refreshOnCreate,
	})
}
