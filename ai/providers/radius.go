// Ported from packages/ai/src/providers/radius.ts (pi v1.5.2).

package providers

import (
	"context"
	"slices"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// RadiusProviderOptions configures a Radius gateway provider instance.
type RadiusProviderOptions struct {
	Id      *string
	Name    *string
	Gateway *string
}

func radiusToAnyModels(models []*ai.Model) ai.AnyModelList {
	out := make(ai.AnyModelList, len(models))
	for i, m := range models {
		out[i] = m
	}
	return out
}

func radiusCheckedAtNow() *int64 {
	now := js.DateNow()
	return &now
}

func radiusCredentialKey(cred ai.Credential) *string {
	switch c := cred.(type) {
	case *ai.OAuthCredential:
		if c != nil && c.Access != "" {
			s := c.Access
			return &s
		}
	case *ai.ApiKeyCredential:
		if c != nil && c.Key != nil && *c.Key != "" {
			s := *c.Key
			return &s
		}
	}
	return nil
}

// RadiusProvider constructs a Radius gateway provider with a persisted, dynamically refreshed catalog.
func RadiusProvider(options *RadiusProviderOptions) *ai.Provider {
	id := "radius"
	name := "Radius"
	rawGateway := aioauth.DefaultRadiusGateway
	if options != nil {
		if options.Id != nil && *options.Id != "" {
			id = *options.Id
		}
		if options.Name != nil && *options.Name != "" {
			name = *options.Name
		}
		if options.Gateway != nil && *options.Gateway != "" {
			rawGateway = *options.Gateway
		}
	}
	gateway := aioauth.NormalizeRadiusGatewayUrl(rawGateway)

	baselineModels := []*ai.Model{}
	if gateway == aioauth.NormalizeRadiusGatewayUrl(aioauth.DefaultRadiusGateway) && catalog.RadiusModels != nil {
		for _, m := range catalog.RadiusModels.Values() {
			if m == nil {
				continue
			}
			cp := *m
			cp.Provider = ai.ProviderId(id)
			baselineModels = append(baselineModels, &cp)
		}
	}

	// Gateway catalog for this account. Radius org owners can disable models, so once known it replaces
	// the shipped baseline instead of overlaying it. The baseline only covers the time before any catalog exists.
	var mu sync.RWMutex
	var dynamicModels []*ai.Model
	streams := api.PiMessagesApi()

	return &ai.Provider{
		Id:   id,
		Name: name,
		Auth: ai.ProviderAuth{
			ApiKey: ai.EnvApiKeyAuth("Radius API key", []string{"RADIUS_API_KEY"}),
			OAuth: ai.LazyOAuth(ai.LazyOAuthInput{
				Name: name,
				Load: func() (*ai.OAuthAuth, error) {
					return aioauth.LoadRadiusOAuth(aioauth.RadiusOAuthOptions{
						Name:    name,
						Gateway: gateway,
					})
				},
			}),
		},
		GetModels: func() []*ai.Model {
			mu.RLock()
			defer mu.RUnlock()
			if dynamicModels != nil {
				return slices.Clone(dynamicModels)
			}
			return slices.Clone(baselineModels)
		},
		RefreshModels: func(ctx context.Context, refresh *ai.RefreshModelsContext) error {
			if refresh == nil {
				return nil
			}
			stored := refresh.Stored
			if stored != nil {
				restored := make([]*ai.Model, 0, len(stored.Models))
				for _, anyModel := range stored.Models {
					if m, ok := anyModel.(*ai.Model); ok && m != nil && string(m.Provider) == id {
						restored = append(restored, m)
					}
				}
				ok, err := refresh.Publish(&ai.ModelsPublication{
					Update: func() {
						mu.Lock()
						dynamicModels = restored
						mu.Unlock()
					},
				})
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
			}

			// Import catalogs cached by the pre-ModelsStore Radius implementation.
			if stored == nil {
				if oauthCred, ok := refresh.Credential.(*ai.OAuthCredential); ok && oauthCred != nil {
					legacy := aioauth.GetRadiusModels(id, oauthCred)
					if len(legacy) > 0 {
						ok, err := refresh.Publish(&ai.ModelsPublication{
							Persist: jsonx.Some(ai.ModelsStoreEntry{
								Models:    radiusToAnyModels(legacy),
								CheckedAt: radiusCheckedAtNow(),
							}),
							Update: func() {
								mu.Lock()
								dynamicModels = legacy
								mu.Unlock()
							},
						})
						if err != nil {
							return err
						}
						if !ok {
							return nil
						}
					}
				}
			}

			if !refresh.AllowNetwork || (ctx != nil && ctx.Err() != nil) {
				return nil
			}
			apiKey := radiusCredentialKey(refresh.Credential)
			cfg, err := aioauth.LoadRadiusGatewayConfig(ctx, gateway, apiKey)
			if err != nil {
				return err
			}
			if ctx != nil && ctx.Err() != nil {
				return nil
			}
			refreshed := aioauth.GetRadiusModelsFromConfig(id, cfg)
			_, err = refresh.Publish(&ai.ModelsPublication{
				Persist: jsonx.Some(ai.ModelsStoreEntry{
					Models:    radiusToAnyModels(refreshed),
					CheckedAt: radiusCheckedAtNow(),
				}),
				Update: func() {
					mu.Lock()
					dynamicModels = refreshed
					mu.Unlock()
				},
			})
			return err
		},
		Stream:       streams.Stream,
		StreamSimple: streams.StreamSimple,
	}
}
