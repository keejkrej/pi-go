// Ported from packages/ai/src/providers/cloudflare-stream.ts (pi v1.5.2).

package providers

import (
	"context"
	"strings"

	"github.com/keejkrej/pi-go/ai"
)

const (
	csCloudflareAccountId = "CLOUDFLARE_ACCOUNT_ID"
	csCloudflareGatewayId = "CLOUDFLARE_GATEWAY_ID"
)

func csExpandBaseUrl(raw string, env ai.ProviderEnv) string {
	if env == nil {
		return raw
	}
	acc, okAcc := env[csCloudflareAccountId]
	if !okAcc {
		acc = "{" + csCloudflareAccountId + "}"
	}
	gw, okGw := env[csCloudflareGatewayId]
	if !okGw {
		gw = "{" + csCloudflareGatewayId + "}"
	}
	out := strings.ReplaceAll(raw, "{"+csCloudflareAccountId+"}", acc)
	out = strings.ReplaceAll(out, "{"+csCloudflareGatewayId+"}", gw)
	return out
}

// ResolveCloudflareModel materializes Cloudflare account/gateway endpoint placeholders
// in model.BaseUrl from env.
func ResolveCloudflareModel(model *ai.Model, env ai.ProviderEnv) *ai.Model {
	if model == nil || env == nil {
		return model
	}
	baseUrl := csExpandBaseUrl(model.BaseUrl, env)
	if baseUrl == model.BaseUrl {
		return model
	}
	cp := *model
	cp.BaseUrl = baseUrl
	return &cp
}

// ResolveCloudflareClassifierModel materializes Cloudflare account/gateway endpoint placeholders
// in classifier model.BaseUrl from env.
func ResolveCloudflareClassifierModel(model *ai.ClassifierModel, env ai.ProviderEnv) *ai.ClassifierModel {
	if model == nil || env == nil {
		return model
	}
	baseUrl := csExpandBaseUrl(model.BaseUrl, env)
	if baseUrl == model.BaseUrl {
		return model
	}
	cp := *model
	cp.BaseUrl = baseUrl
	return &cp
}

// CloudflareStreams wraps an API implementation so Cloudflare account/gateway endpoint
// placeholders materialize from the resolved provider env before dispatch.
func CloudflareStreams(streams ai.ProviderStreams) ai.ProviderStreams {
	out := ai.ProviderStreams{}
	if streams.Stream != nil {
		out.Stream = func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			var env ai.ProviderEnv
			if options != nil {
				if base := options.BaseStreamOptions(); base != nil {
					env = base.Env
				}
			}
			return streams.Stream(ctx, ResolveCloudflareModel(model, env), transcript, options)
		}
	}
	if streams.StreamSimple != nil {
		out.StreamSimple = func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			var env ai.ProviderEnv
			if options != nil {
				env = options.Env
			}
			return streams.StreamSimple(ctx, ResolveCloudflareModel(model, env), transcript, options)
		}
	}
	return out
}

// CloudflareClassifier is the classifier counterpart of CloudflareStreams.
func CloudflareClassifier(classifier ai.ProviderClassifier) ai.ProviderClassifier {
	out := ai.ProviderClassifier{}
	if classifier.Classify != nil {
		out.Classify = func(ctx context.Context, model *ai.ClassifierModel, c *ai.ClassifierContext, options *ai.ClassifierOptions) (*ai.ClassifierResult, error) {
			var env ai.ProviderEnv
			if options != nil {
				env = options.Env
			}
			return classifier.Classify(ctx, ResolveCloudflareClassifierModel(model, env), c, options)
		}
	}
	return out
}
