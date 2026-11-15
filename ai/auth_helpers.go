// Ported from packages/ai/src/auth/helpers.ts (pi v1.5.2).

package ai

import (
	"context"
	"fmt"
	"sync"
)

// EnvApiKeyAuth returns standard api-key auth: stored credential key wins,
// otherwise the first set environment variable in envVars resolves.
func EnvApiKeyAuth(name string, envVars []string) *ApiKeyAuth {
	vars := append([]string(nil), envVars...)
	return &ApiKeyAuth{
		Name: name,
		Login: func(ctx context.Context, interaction AuthInteraction) (*ApiKeyCredential, error) {
			if ctx != nil && ctx.Err() != nil {
				return nil, uaAbortReason(ctx)
			}
			key, err := interaction.Prompt(ctx, &AuthPromptSecret{
				Type:          AuthPromptKindSecret,
				Message:       fmt.Sprintf("Enter %s", name),
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			if ctx != nil && ctx.Err() != nil {
				return nil, uaAbortReason(ctx)
			}
			return &ApiKeyCredential{
				Type: AuthTypeApiKey,
				Key:  &key,
			}, nil
		},
		Resolve: func(ctx context.Context, input ApiKeyResolveInput) (*AuthResult, error) {
			if ctx != nil && ctx.Err() != nil {
				return nil, uaAbortReason(ctx)
			}
			if input.Credential != nil && input.Credential.Key != nil && *input.Credential.Key != "" {
				src := "stored credential"
				k := *input.Credential.Key
				return &AuthResult{
					Auth:   ModelAuth{ApiKey: &k},
					Env:    input.Credential.Env,
					Source: &src,
				}, nil
			}
			for _, envVar := range vars {
				val, err := input.Ctx.Env(ctx, envVar)
				if err != nil {
					return nil, err
				}
				if ctx != nil && ctx.Err() != nil {
					return nil, uaAbortReason(ctx)
				}
				if val != nil && *val != "" {
					src := envVar
					k := *val
					return &AuthResult{
						Auth:   ModelAuth{ApiKey: &k},
						Source: &src,
					}, nil
				}
			}
			return nil, nil
		},
	}
}

// LazyOAuthInput configures a lazily loaded OAuthAuth wrapper.
type LazyOAuthInput struct {
	Name           string
	IsSubscription bool
	LoginLabel     *string
	Load           func() (*OAuthAuth, error)
}

// LazyOAuth wraps a lazily loaded OAuthAuth implementation so provider definitions
// can advertise OAuth metadata without eagerly initializing the flow.
func LazyOAuth(input LazyOAuthInput) *OAuthAuth {
	var (
		mu     sync.Mutex
		loaded bool
		cached *OAuthAuth
	)
	getImpl := func() (*OAuthAuth, error) {
		mu.Lock()
		defer mu.Unlock()
		if loaded {
			return cached, nil
		}
		if input.Load == nil {
			return nil, fmt.Errorf("lazy OAuth loader is nil for %s", input.Name)
		}
		impl, err := input.Load()
		if err != nil {
			return nil, err
		}
		if impl == nil {
			return nil, fmt.Errorf("lazy OAuth loader returned nil for %s", input.Name)
		}
		cached = impl
		loaded = true
		return cached, nil
	}

	return &OAuthAuth{
		Name:           input.Name,
		IsSubscription: input.IsSubscription,
		LoginLabel:     input.LoginLabel,
		Login: func(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
			impl, err := getImpl()
			if err != nil {
				return nil, err
			}
			return impl.Login(ctx, interaction, options)
		},
		Refresh: func(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
			impl, err := getImpl()
			if err != nil {
				return nil, err
			}
			return impl.Refresh(ctx, credential)
		},
		ToAuth: func(ctx context.Context, credential *OAuthCredential) (ModelAuth, error) {
			impl, err := getImpl()
			if err != nil {
				return ModelAuth{}, err
			}
			return impl.ToAuth(ctx, credential)
		},
	}
}
