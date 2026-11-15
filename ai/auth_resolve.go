// Ported from packages/ai/src/auth/resolve.ts (pi v1.5.2).

package ai

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
)

const (
	arDefaultOAuthMinValidityMs  int64 = 5 * 60 * 1000
	arDefaultOAuthRefreshTimeout       = 15_000 * time.Millisecond
)

// AuthResolutionOverrides configures explicit API key, env, or token-validity
// overrides for a single auth resolution call. Cancellation uses ctx.
type AuthResolutionOverrides struct {
	ApiKey             *string     `json:"apiKey,omitzero"`
	Env                ProviderEnv `json:"env,omitzero"`
	MinOAuthValidityMs *int64      `json:"minOAuthValidityMs,omitzero"`
}

// ResolveProviderAuth resolves effective auth for a provider from overrides,
// stored credentials (with double-checked OAuth refresh under the store lock),
// or ambient environment/files. Returns (nil, nil) when unconfigured.
func ResolveProviderAuth(
	ctx context.Context,
	providerId string,
	auth ProviderAuth,
	credentials CredentialStore,
	authContext AuthContext,
	overrides *AuthResolutionOverrides,
) (*AuthResult, error) {
	sig := OperationSignal(ctx)
	return RaceWithAbortSignal(sig, func() (*AuthResult, error) {
		return arResolveProviderAuthWithSignal(sig, providerId, auth, credentials, authContext, overrides)
	})
}

func arResolveProviderAuthWithSignal(
	ctx context.Context,
	providerId string,
	auth ProviderAuth,
	credentials CredentialStore,
	authContext AuthContext,
	overrides *AuthResolutionOverrides,
) (*AuthResult, error) {
	if ctx.Err() != nil {
		return nil, uaAbortReason(ctx)
	}
	reqCtx := authContext
	if overrides != nil && len(overrides.Env) > 0 {
		reqCtx = arOverlayEnvAuthContext(authContext, overrides.Env)
	}

	if overrides != nil && overrides.ApiKey != nil && auth.ApiKey != nil {
		k := *overrides.ApiKey
		cred := &ApiKeyCredential{
			Type: AuthTypeApiKey,
			Key:  &k,
			Env:  maps.Clone(overrides.Env),
		}
		return arResolveApiKey(ctx, reqCtx, auth.ApiKey, providerId, cred)
	}

	stored, err := arReadCredential(ctx, credentials, providerId)
	if err != nil {
		return nil, err
	}
	if stored != nil {
		switch c := stored.(type) {
		case *OAuthCredential:
			if auth.OAuth != nil {
				var minValidity *int64
				if overrides != nil {
					minValidity = overrides.MinOAuthValidityMs
				}
				return arResolveStoredOAuth(ctx, credentials, providerId, auth.OAuth, c, minValidity)
			}
			return nil, nil
		case *ApiKeyCredential:
			if auth.ApiKey != nil {
				cred := c
				if overrides != nil && len(overrides.Env) > 0 {
					mergedEnv := make(ProviderEnv, len(c.Env)+len(overrides.Env))
					maps.Copy(mergedEnv, c.Env)
					maps.Copy(mergedEnv, overrides.Env)
					cred = &ApiKeyCredential{
						Type: AuthTypeApiKey,
						Key:  c.Key,
						Env:  mergedEnv,
					}
				}
				return arResolveApiKey(ctx, reqCtx, auth.ApiKey, providerId, cred)
			}
			return nil, nil
		}
	}

	if auth.ApiKey != nil {
		return arResolveApiKey(ctx, reqCtx, auth.ApiKey, providerId, nil)
	}
	return nil, nil
}

type arOverlayAuthContext struct {
	base AuthContext
	env  ProviderEnv
}

func (o arOverlayAuthContext) Env(ctx context.Context, name string) (*string, error) {
	if v := o.env[name]; v != "" {
		return &v, nil
	}
	return o.base.Env(ctx, name)
}

func (o arOverlayAuthContext) FileExists(ctx context.Context, path string) (bool, error) {
	return o.base.FileExists(ctx, path)
}

func arOverlayEnvAuthContext(base AuthContext, env ProviderEnv) AuthContext {
	return arOverlayAuthContext{base: base, env: env}
}

// RefreshStoredOAuthCredential refreshes a stored OAuth credential under the store lock
// using double-checked expiration. Cancellation aborts only waiting for the lock; once
// refresh begins, a bounded timeout protects persistence of rotated refresh tokens.
func RefreshStoredOAuthCredential(
	ctx context.Context,
	credentials CredentialStore,
	providerId string,
	oauth *OAuthAuth,
	needsRefresh func(credential *OAuthCredential) bool,
) (*OAuthCredential, error) {
	sig := OperationSignal(ctx)
	lockWaitCtx, cancelLockWait := context.WithCancel(context.Background())
	defer cancelLockWait()

	var (
		watchMu sync.Mutex
		active  = true
	)
	stopWatch := context.AfterFunc(sig, func() {
		watchMu.Lock()
		shouldCancel := active
		watchMu.Unlock()
		if shouldCancel {
			cancelLockWait()
		}
	})
	defer stopWatch()

	post, err := credentials.Modify(lockWaitCtx, providerId, func(current Credential) (Credential, error) {
		watchMu.Lock()
		active = false
		watchMu.Unlock()
		stopWatch()

		if sig.Err() != nil {
			return nil, uaAbortReason(sig)
		}
		oauthCred, ok := current.(*OAuthCredential)
		if !ok || oauthCred == nil {
			return nil, nil
		}
		if needsRefresh != nil && !needsRefresh(oauthCred) {
			return nil, nil
		}

		refreshCtx, cancelRefresh := context.WithTimeout(context.Background(), arDefaultOAuthRefreshTimeout)
		defer cancelRefresh()
		refreshed, err := oauth.Refresh(refreshCtx, oauthCred)
		if err != nil {
			return nil, NewModelsError(ModelsErrorCodeOAuth, fmt.Sprintf("OAuth refresh failed for %s", providerId), err)
		}
		return refreshed, nil
	}, nil)
	if err != nil {
		var modelsErr *ModelsError
		if errors.As(err, &modelsErr) {
			return nil, err
		}
		if sig.Err() != nil {
			return nil, uaAbortReason(sig)
		}
		return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("Credential store modify failed for %s", providerId), err)
	}
	if oauthCred, ok := post.(*OAuthCredential); ok && oauthCred != nil {
		return oauthCred, nil
	}
	return nil, nil
}

func arResolveStoredOAuth(
	ctx context.Context,
	credentials CredentialStore,
	providerId string,
	oauth *OAuthAuth,
	stored *OAuthCredential,
	minOAuthValidityMs *int64,
) (*AuthResult, error) {
	minValidityMs := arDefaultOAuthMinValidityMs
	if minOAuthValidityMs != nil && *minOAuthValidityMs > minValidityMs {
		minValidityMs = *minOAuthValidityMs
	}
	expiresSoon := func(cred *OAuthCredential) bool {
		return float64(js.DateNow()+minValidityMs) >= cred.Expires
	}

	credential := stored
	if expiresSoon(credential) {
		post, err := RefreshStoredOAuthCredential(ctx, credentials, providerId, oauth, expiresSoon)
		if err != nil {
			return nil, err
		}
		if post == nil {
			return nil, nil
		}
		credential = post
		if minOAuthValidityMs != nil && expiresSoon(credential) {
			return nil, NewModelsError(ModelsErrorCodeOAuth, fmt.Sprintf("OAuth refresh returned a token that expires too soon for %s", providerId))
		}
	}

	modelAuth, err := oauth.ToAuth(ctx, credential)
	if err != nil {
		return nil, NewModelsError(ModelsErrorCodeOAuth, fmt.Sprintf("OAuth auth derivation failed for %s", providerId), err)
	}
	src := "OAuth"
	return &AuthResult{
		Auth:   modelAuth,
		Source: &src,
	}, nil
}

func arResolveApiKey(
	ctx context.Context,
	authContext AuthContext,
	apiKey *ApiKeyAuth,
	providerId string,
	credential *ApiKeyCredential,
) (*AuthResult, error) {
	res, err := apiKey.Resolve(ctx, ApiKeyResolveInput{
		Ctx:        authContext,
		Credential: credential,
	})
	if err != nil {
		return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("API key auth failed for provider %s", providerId), err)
	}
	return res, nil
}

func arReadCredential(ctx context.Context, credentials CredentialStore, providerId string) (Credential, error) {
	cred, err := credentials.Read(ctx, providerId, nil)
	if err != nil {
		return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("Credential store read failed for %s", providerId), err)
	}
	return cred, nil
}
