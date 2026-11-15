// Ported from packages/ai/src/providers/amazon-bedrock.ts (pi v1.5.2).

package providers

import (
	"context"
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/ai/catalog"
)

func abEnv(ctx context.Context, authCtx ai.AuthContext, name string) (*string, error) {
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

func abNonEmpty(s *string) bool {
	return s != nil && *s != ""
}

// Bedrock accepts a bearer token or the AWS SDK's default credential chain.
// The login flow can store a token/profile choice; resolve also detects ambient
// AWS credentials without copying them into pi's credential store.
var abBedrockAuth = &ai.ApiKeyAuth{
	Name: "AWS credentials or bearer token",
	Login: func(ctx context.Context, interaction ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
		if ctx != nil && ctx.Err() != nil {
			return nil, allAbortReason(ctx)
		}
		method, err := interaction.Prompt(ctx, &ai.AuthPromptSelect{
			Type:    ai.AuthPromptKindSelect,
			Message: "Select Amazon Bedrock authentication method:",
			Options: []ai.AuthSelectOption{
				{Id: "bearer-token", Label: "Bearer token"},
				{Id: "aws-profile", Label: "AWS profile"},
				{Id: "credential-chain", Label: "Existing AWS credential chain"},
			},
			PromptContext: ctx,
		})
		if err != nil {
			return nil, err
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, allAbortReason(ctx)
		}
		if method == "bearer-token" {
			key, err := interaction.Prompt(ctx, &ai.AuthPromptSecret{
				Type:          ai.AuthPromptKindSecret,
				Message:       "Enter Amazon Bedrock bearer token",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			return &ai.ApiKeyCredential{
				Type: ai.AuthTypeApiKey,
				Key:  &key,
			}, nil
		}
		interaction.Notify(&ai.AuthEventInfo{
			Type:    ai.AuthEventKindInfo,
			Message: "Amazon Bedrock supports AWS profiles, IAM credentials, and role-based credentials.",
			Links: []ai.AuthInfoLink{
				{
					Label: allPtr("AWS credential provider chain"),
					Url:   "https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html",
				},
			},
		})
		if method == "aws-profile" {
			profile, err := interaction.Prompt(ctx, &ai.AuthPromptText{
				Type:          ai.AuthPromptKindText,
				Message:       "Enter AWS profile name",
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			return &ai.ApiKeyCredential{
				Type: ai.AuthTypeApiKey,
				Env:  ai.ProviderEnv{"AWS_PROFILE": profile},
			}, nil
		}
		if method != "credential-chain" {
			return nil, fmt.Errorf("Unknown Amazon Bedrock auth method: %s", method)
		}
		if _, err := interaction.Prompt(ctx, &ai.AuthPromptText{
			Type:          ai.AuthPromptKindText,
			Message:       "Configure AWS credentials, then press Enter to continue",
			PromptContext: ctx,
		}); err != nil {
			return nil, err
		}
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey}, nil
	},
	Resolve: func(ctx context.Context, input ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
		if input.Credential != nil && abNonEmpty(input.Credential.Key) {
			k := *input.Credential.Key
			return &ai.AuthResult{
				Auth:   ai.ModelAuth{ApiKey: &k},
				Env:    input.Credential.Env,
				Source: allPtr("stored credential"),
			}, nil
		}
		if v, err := abEnv(ctx, input.Ctx, "AWS_BEARER_TOKEN_BEDROCK"); err != nil {
			return nil, err
		} else if abNonEmpty(v) {
			return &ai.AuthResult{Auth: ai.ModelAuth{}, Source: allPtr("AWS_BEARER_TOKEN_BEDROCK")}, nil
		}

		storedProfile := ""
		var credEnv ai.ProviderEnv
		if input.Credential != nil && input.Credential.Env != nil {
			credEnv = input.Credential.Env
			storedProfile = credEnv["AWS_PROFILE"]
		}
		if storedProfile != "" {
			return &ai.AuthResult{Auth: ai.ModelAuth{}, Env: credEnv, Source: allPtr("stored credential")}, nil
		}
		if v, err := abEnv(ctx, input.Ctx, "AWS_PROFILE"); err != nil {
			return nil, err
		} else if abNonEmpty(v) {
			return &ai.AuthResult{Auth: ai.ModelAuth{}, Env: credEnv, Source: allPtr("AWS_PROFILE")}, nil
		}

		accessKeyId, err := abEnv(ctx, input.Ctx, "AWS_ACCESS_KEY_ID")
		if err != nil {
			return nil, err
		}
		secretKey, err := abEnv(ctx, input.Ctx, "AWS_SECRET_ACCESS_KEY")
		if err != nil {
			return nil, err
		}
		if abNonEmpty(accessKeyId) && abNonEmpty(secretKey) {
			return &ai.AuthResult{Auth: ai.ModelAuth{}, Source: allPtr("AWS access keys")}, nil
		}

		if v, err := abEnv(ctx, input.Ctx, "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"); err != nil {
			return nil, err
		} else if abNonEmpty(v) {
			return &ai.AuthResult{Auth: ai.ModelAuth{}, Source: allPtr("ECS task role")}, nil
		}
		if v, err := abEnv(ctx, input.Ctx, "AWS_CONTAINER_CREDENTIALS_FULL_URI"); err != nil {
			return nil, err
		} else if abNonEmpty(v) {
			return &ai.AuthResult{Auth: ai.ModelAuth{}, Source: allPtr("ECS task role")}, nil
		}
		if v, err := abEnv(ctx, input.Ctx, "AWS_WEB_IDENTITY_TOKEN_FILE"); err != nil {
			return nil, err
		} else if abNonEmpty(v) {
			return &ai.AuthResult{Auth: ai.ModelAuth{}, Source: allPtr("web identity token")}, nil
		}
		return nil, nil
	},
}

// AmazonBedrockProvider constructs the built-in Amazon Bedrock provider.
func AmazonBedrockProvider() *ai.Provider {
	return allMustCreateProvider(&ai.CreateProviderOptions{
		Id:     "amazon-bedrock",
		Name:   allPtr("Amazon Bedrock"),
		Auth:   ai.ProviderAuth{ApiKey: abBedrockAuth},
		Models: allCatalogModels(catalog.AmazonBedrockModels, nil, nil),
		Api:    api.BedrockConverseStreamApi(),
	})
}
