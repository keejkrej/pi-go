// Ported from packages/ai/src/providers/google-vertex.ts (pi v1.5.2).

package providers

import (
	"context"
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

const gvVertexAdcPath = "~/.config/gcloud/application_default_credentials.json"

func gvEnv(ctx context.Context, authCtx ai.AuthContext, name string) (*string, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, allAbortReason(ctx)
	}
	val, err := authCtx.Env(ctx, name)
	if err != nil {
		return nil, err
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, allAbortReason(ctx)
	}
	return val, nil
}

func gvCredentialEnv(credential *ai.ApiKeyCredential, name string) *string {
	if credential != nil && credential.Env != nil {
		if v, ok := credential.Env[name]; ok && v != "" {
			return &v
		}
	}
	return nil
}

// Vertex accepts an explicit API key or Application Default Credentials
// (`gcloud auth application-default login`). ADC additionally requires
// project and location env vars, which the implementation reads itself.
var gvVertexAuth = &ai.ApiKeyAuth{
	Name: "Google Cloud credentials",
	Login: func(ctx context.Context, interaction ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
		if ctx != nil && ctx.Err() != nil {
			return nil, allAbortReason(ctx)
		}
		method, err := interaction.Prompt(ctx, &ai.AuthPromptSelect{
			Type:    ai.AuthPromptKindSelect,
			Message: "Select Google Vertex AI authentication method:",
			Options: []ai.AuthSelectOption{
				{Id: "api-key", Label: "Google Cloud API key"},
				{Id: "adc", Label: "Application Default Credentials"},
				{Id: "service-account", Label: "Service account credentials file"},
			},
			PromptContext: ctx,
		})
		if err != nil {
			return nil, err
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, allAbortReason(ctx)
		}
		if method == "api-key" {
			key, err := interaction.Prompt(ctx, &ai.AuthPromptSecret{
				Type:          ai.AuthPromptKindSecret,
				Message:       "Enter Google Cloud API key",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
		}
		if method != "adc" && method != "service-account" {
			return nil, fmt.Errorf("Unknown Google Vertex AI auth method: %s", method)
		}
		infoMsg := "Provide a service account credentials file, project, and location."
		if method == "adc" {
			infoMsg = "Run `gcloud auth application-default login`, then provide the project and location."
		}
		interaction.Notify(&ai.AuthEventInfo{
			Type:    ai.AuthEventKindInfo,
			Message: infoMsg,
			Links: []ai.AuthInfoLink{
				{
					Label: allPtr("Application Default Credentials"),
					Url:   "https://cloud.google.com/docs/authentication/provide-credentials-adc",
				},
			},
		})
		project, err := interaction.Prompt(ctx, &ai.AuthPromptText{
			Type:          ai.AuthPromptKindText,
			Message:       "Enter Google Cloud project ID",
			PromptContext: ctx,
		})
		if err != nil {
			return nil, err
		}
		location, err := interaction.Prompt(ctx, &ai.AuthPromptText{
			Type:          ai.AuthPromptKindText,
			Message:       "Enter Google Cloud location",
			PromptContext: ctx,
		})
		if err != nil {
			return nil, err
		}
		env := ai.ProviderEnv{
			"GOOGLE_CLOUD_PROJECT":  project,
			"GOOGLE_CLOUD_LOCATION": location,
		}
		if method == "service-account" {
			credentialsPath, err := interaction.Prompt(ctx, &ai.AuthPromptText{
				Type:          ai.AuthPromptKindText,
				Message:       "Enter service account credentials file path",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			if credentialsPath != "" {
				env["GOOGLE_APPLICATION_CREDENTIALS"] = credentialsPath
			}
		}
		return &ai.ApiKeyCredential{
			Type: ai.AuthTypeApiKey,
			Env:  env,
		}, nil
	},
	Resolve: func(ctx context.Context, input ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
		if input.Credential != nil && input.Credential.Key != nil && *input.Credential.Key != "" {
			k := *input.Credential.Key
			return &ai.AuthResult{
				Auth:   ai.ModelAuth{ApiKey: &k},
				Source: allPtr("stored credential"),
			}, nil
		}
		if envKey, err := gvEnv(ctx, input.Ctx, "GOOGLE_CLOUD_API_KEY"); err != nil {
			return nil, err
		} else if envKey != nil && *envKey != "" {
			k := *envKey
			return &ai.AuthResult{
				Auth:   ai.ModelAuth{ApiKey: &k},
				Source: allPtr("GOOGLE_CLOUD_API_KEY"),
			}, nil
		}

		adcPath := gvCredentialEnv(input.Credential, "GOOGLE_APPLICATION_CREDENTIALS")
		if adcPath == nil {
			var err error
			adcPath, err = gvEnv(ctx, input.Ctx, "GOOGLE_APPLICATION_CREDENTIALS")
			if err != nil {
				return nil, err
			}
		}
		pathToCheck := gvVertexAdcPath
		if adcPath != nil && *adcPath != "" {
			pathToCheck = *adcPath
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, allAbortReason(ctx)
		}
		hasCredentials, err := input.Ctx.FileExists(ctx, pathToCheck)
		if err != nil {
			return nil, err
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, allAbortReason(ctx)
		}

		project := gvCredentialEnv(input.Credential, "GOOGLE_CLOUD_PROJECT")
		if project == nil {
			project, err = gvEnv(ctx, input.Ctx, "GOOGLE_CLOUD_PROJECT")
			if err != nil {
				return nil, err
			}
		}
		if project == nil || *project == "" {
			project, err = gvEnv(ctx, input.Ctx, "GCLOUD_PROJECT")
			if err != nil {
				return nil, err
			}
		}

		location := gvCredentialEnv(input.Credential, "GOOGLE_CLOUD_LOCATION")
		if location == nil {
			location, err = gvEnv(ctx, input.Ctx, "GOOGLE_CLOUD_LOCATION")
			if err != nil {
				return nil, err
			}
		}

		if hasCredentials && project != nil && *project != "" && location != nil && *location != "" {
			var credEnv ai.ProviderEnv
			source := "gcloud application default credentials"
			if input.Credential != nil {
				credEnv = input.Credential.Env
				source = "stored credential"
			}
			return &ai.AuthResult{
				Auth:   ai.ModelAuth{},
				Env:    credEnv,
				Source: allPtr(source),
			}, nil
		}
		return nil, nil
	},
}

// GoogleVertexProvider constructs the built-in Google Vertex AI provider.
func GoogleVertexProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:     "google-vertex",
		Name:   allPtr("Google Vertex AI"),
		Auth:   ai.ProviderAuth{ApiKey: gvVertexAuth},
		Models: allCatalogModels(catalog.GoogleVertexModels, nil, nil),
		Api:    api.GoogleVertexApi(),
	})
}
