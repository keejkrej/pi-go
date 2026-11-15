// Ported from packages/coding-agent/src/extensions/llama/provider.ts (pi v1.5.2).

package llamaext

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	// LlamaProviderId is the native provider ID registered for llama.cpp routers.
	LlamaProviderId = "llama.cpp"
	// DefaultLlamaServerUrl is the default local llama-server management URL.
	DefaultLlamaServerUrl = "http://127.0.0.1:9931"
	// DefaultLlamaApiKey is used when a llama.cpp server has no configured bearer key.
	DefaultLlamaApiKey = "local"
	// LlamaBaseUrlEnvVar holds the normalized llama.cpp management URL.
	LlamaBaseUrlEnvVar = "LLAMA_BASE_URL"
)

const provDefaultContextWindow = 8192

// LlamaProviderOptions configures CreateLlamaProvider.
type LlamaProviderOptions struct {
	HTTPClient *http.Client
}

// LlamaSetCatalogOptions supplies optional router autoload or template metadata when populating catalogs manually.
type LlamaSetCatalogOptions struct {
	RouterAutoload bool
	Templates      map[string]string
}

// LlamaProviderController pairs the native *ai.Provider with SetCatalog for runtime catalog updates.
type LlamaProviderController struct {
	Provider   *ai.Provider
	SetCatalog func(models []LlamaModelInfo, managementUrl string, opts ...*LlamaSetCatalogOptions)
}

func provBoolPtr(v bool) *bool { return &v }

func provStrPtr(s string) *string { return &s }

func provNormalizeEnvUrl(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	norm, err := NormalizeLlamaServerUrl(trimmed)
	if err != nil {
		return "", false
	}
	return norm, true
}

func provCredentialBaseUrl(ctx context.Context, cred *ai.ApiKeyCredential, authCtx ai.AuthContext) (url string, source string, ok bool) {
	if cred != nil && len(cred.Env) > 0 {
		if u, valid := provNormalizeEnvUrl(cred.Env[LlamaBaseUrlEnvVar]); valid {
			return u, "stored credential", true
		}
	}
	if authCtx != nil {
		if envVal, err := authCtx.Env(ctx, LlamaBaseUrlEnvVar); err == nil && envVal != nil {
			if u, valid := provNormalizeEnvUrl(*envVal); valid {
				return u, LlamaBaseUrlEnvVar, true
			}
		}
	}
	if cred != nil && cred.Key != nil && strings.TrimSpace(*cred.Key) != "" {
		return DefaultLlamaServerUrl, "stored credential", true
	}
	return "", "", false
}

func provParseCtxSizeArgs(args []string) int {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "--ctx-size" || arg == "-c") && i+1 < len(args) {
			if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
				return n
			}
		} else if strings.HasPrefix(arg, "--ctx-size=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(arg, "--ctx-size=")); err == nil && n > 0 {
				return n
			}
		} else if strings.HasPrefix(arg, "-c=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(arg, "-c=")); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

func provResolveContextWindow(info LlamaModelInfo, cachedContextWindows map[string]int) int {
	if info.Meta != nil && info.Meta.NCtx != nil && *info.Meta.NCtx > 0 {
		return *info.Meta.NCtx
	}
	if n := provParseCtxSizeArgs(info.Status.Args); n > 0 {
		return n
	}
	if cached, ok := cachedContextWindows[info.Id]; ok && cached > 0 {
		return cached
	}
	if info.Meta != nil && info.Meta.NCtxTrain != nil && *info.Meta.NCtxTrain > 0 {
		return *info.Meta.NCtxTrain
	}
	return provDefaultContextWindow
}

func provExtractCachedContextWindows(stored *ai.ModelsStoreEntry) map[string]int {
	out := make(map[string]int)
	if stored == nil {
		return out
	}
	for _, anyModel := range stored.Models {
		switch m := anyModel.(type) {
		case *ai.Model:
			if m != nil && m.ContextWindow > 0 {
				out[m.Id] = m.ContextWindow
			}
		case *ai.ClassifierModel:
			if m != nil && m.ContextWindow > 0 {
				if _, exists := out[m.Id]; !exists {
					out[m.Id] = m.ContextWindow
				}
			}
		}
	}
	return out
}

func provIsSelectable(info LlamaModelInfo, routerAutoload bool) bool {
	switch info.Status.Value {
	case "loaded", "sleeping":
		return true
	case "unloaded":
		return routerAutoload &&
			info.Source != nil && *info.Source == "preset" &&
			!(info.Status.Failed != nil && *info.Status.Failed)
	default:
		return false
	}
}

func provBuildCatalogModels(
	catalog []LlamaModelInfo,
	managementUrl string,
	routerAutoload bool,
	templates map[string]string,
	cachedContextWindows map[string]int,
) ([]*ai.Model, []*ai.ClassifierModel) {
	inferenceUrl := LlamaInferenceUrl(managementUrl)
	chatModels := make([]*ai.Model, 0)
	classifierModels := make([]*ai.ClassifierModel, 0)

	for _, info := range catalog {
		if !provIsSelectable(info, routerAutoload) {
			continue
		}
		ctxWin := provResolveContextWindow(info, cachedContextWindows)
		var outMods []string
		var inMods []string
		if info.Architecture != nil {
			outMods = info.Architecture.OutputModalities
			inMods = info.Architecture.InputModalities
		}
		hasDecisions := slices.Contains(outMods, "decisions")
		hasChat := !hasDecisions || slices.Contains(outMods, "text")

		inputs := []ai.ModelModality{ai.ModelModalityText}
		if slices.Contains(inMods, "image") {
			inputs = append(inputs, ai.ModelModalityImage)
		}

		if hasChat {
			m := &ai.Model{
				BaseModel: ai.BaseModel{
					Id:       info.Id,
					Name:     info.Id,
					Api:      string(ai.KnownApiOpenaiCompletions),
					Provider: ai.ProviderId(LlamaProviderId),
					BaseUrl:  inferenceUrl,
					Input:    inputs,
					Cost:     ai.ModelCost{},
				},
				ContextWindow: ctxWin,
				MaxTokens:     ctxWin,
			}
			if tpl, ok := templates[info.Id]; ok && strings.Contains(tpl, "enable_thinking") {
				tf := ai.ThinkingFormatQwenChatTemplate
				m.Reasoning = true
				m.ThinkingLevelMap = &ai.ThinkingLevelMap{
					Off:     jsonx.Some("off"),
					Minimal: jsonx.Null[string](),
					Low:     jsonx.Null[string](),
					Medium:  jsonx.Some("medium"),
					High:    jsonx.Null[string](),
					Xhigh:   jsonx.Null[string](),
				}
				m.Compat = ai.ModelCompatValue{
					V: &ai.OpenAICompletionsCompat{
						ThinkingFormat: &tf,
					},
				}
			}
			chatModels = append(chatModels, m)
		}

		if hasDecisions {
			classifierModels = append(classifierModels, &ai.ClassifierModel{
				BaseModel: ai.BaseModel{
					Id:       info.Id,
					Name:     info.Id,
					Api:      string(ai.KnownClassifierApiTypesafeSystemOne),
					Provider: ai.ProviderId(LlamaProviderId),
					BaseUrl:  inferenceUrl,
					Input:    inputs,
					Cost:     ai.ModelCost{},
				},
				Type:          ai.ModelTypeClassifier,
				ContextWindow: ctxWin,
			})
		} else {
			classifierModels = append(classifierModels, &ai.ClassifierModel{
				BaseModel: ai.BaseModel{
					Id:       info.Id,
					Name:     info.Id,
					Api:      string(ai.KnownClassifierApiLlamaCppClassify),
					Provider: ai.ProviderId(LlamaProviderId),
					BaseUrl:  managementUrl,
					Input:    inputs,
					Cost:     ai.ModelCost{},
				},
				Type:          ai.ModelTypeClassifier,
				ContextWindow: ctxWin,
			})
		}
	}

	return chatModels, classifierModels
}

func provCombineModels(chat []*ai.Model, classifiers []*ai.ClassifierModel) []ai.AnyModel {
	all := make([]ai.AnyModel, 0, len(chat)+len(classifiers))
	for _, m := range chat {
		all = append(all, m)
	}
	for _, c := range classifiers {
		all = append(all, c)
	}
	return all
}

func provInjectStreamOptionsHTTPClient(opts ai.ProviderStreamOptions, client *http.Client) ai.ProviderStreamOptions {
	if client == nil {
		return opts
	}
	if opts == nil {
		return &ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{HTTPClient: client},
		}
	}
	if base := opts.BaseStreamOptions(); base != nil && base.HTTPClient == nil {
		if direct, ok := opts.(*ai.StreamOptions); ok {
			cloned := *direct
			cloned.HTTPClient = client
			return &cloned
		}
		if simple, ok := opts.(*ai.SimpleStreamOptions); ok {
			cloned := *simple
			cloned.HTTPClient = client
			return &cloned
		}
		base.HTTPClient = client
	}
	return opts
}

// CreateLlamaProvider constructs the native llama.cpp *ai.Provider and its catalog controller.
func CreateLlamaProvider(opts ...*LlamaProviderOptions) *LlamaProviderController {
	httpClient := ai.NodeHttpProxyDefaultClient()
	if len(opts) > 0 && opts[0] != nil && opts[0].HTTPClient != nil {
		httpClient = opts[0].HTTPClient
	}

	var (
		mu               sync.RWMutex
		chatModels       []*ai.Model
		classifierModels []*ai.ClassifierModel
	)

	setCatalog := func(catalog []LlamaModelInfo, managementUrl string, catOpts ...*LlamaSetCatalogOptions) {
		normUrl, err := NormalizeLlamaServerUrl(managementUrl)
		if err != nil {
			normUrl = strings.TrimRight(managementUrl, "/")
		}
		routerAutoload := false
		var templates map[string]string
		if len(catOpts) > 0 && catOpts[0] != nil {
			routerAutoload = catOpts[0].RouterAutoload
			templates = catOpts[0].Templates
		}
		chats, classifiers := provBuildCatalogModels(catalog, normUrl, routerAutoload, templates, nil)
		mu.Lock()
		chatModels = chats
		classifierModels = classifiers
		mu.Unlock()
	}

	provider := &ai.Provider{
		Id:                LlamaProviderId,
		Name:              "llama.cpp",
		RefreshModelsAuth: ai.RefreshModelsAuthCredential,
		Auth: ai.ProviderAuth{
			ApiKey: &ai.ApiKeyAuth{
				Name: "llama.cpp",
				Login: func(ctx context.Context, interaction ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
					rawUrl, err := interaction.Prompt(ctx, &ai.AuthPromptText{
						Type:          ai.AuthPromptKindText,
						Message:       "llama.cpp server URL",
						Placeholder:   provStrPtr(DefaultLlamaServerUrl),
						PromptContext: ctx,
					})
					if err != nil {
						return nil, err
					}
					if strings.TrimSpace(rawUrl) == "" {
						rawUrl = DefaultLlamaServerUrl
					}
					normUrl, err := NormalizeLlamaServerUrl(rawUrl)
					if err != nil {
						return nil, err
					}

					rawKey, err := interaction.Prompt(ctx, &ai.AuthPromptSecret{
						Type:          ai.AuthPromptKindSecret,
						Message:       "API key (optional)",
						Placeholder:   provStrPtr(""),
						PromptContext: ctx,
					})
					if err != nil {
						return nil, err
					}
					trimmedKey := strings.TrimSpace(rawKey)
					var keyPtr *string
					if trimmedKey != "" {
						keyPtr = &trimmedKey
					}
					client, err := NewLlamaClient(normUrl, &LlamaClientOptions{
						ApiKey:     keyPtr,
						HTTPClient: httpClient,
					})
					if err != nil {
						return nil, err
					}
					if _, err := client.List(ctx); err != nil {
						return nil, err
					}
					return &ai.ApiKeyCredential{
						Type: ai.AuthTypeApiKey,
						Key:  keyPtr,
						Env:  ai.ProviderEnv{LlamaBaseUrlEnvVar: normUrl},
					}, nil
				},
				Check: func(ctx context.Context, input ai.ApiKeyCheckInput) (*ai.AuthCheck, error) {
					_, source, ok := provCredentialBaseUrl(ctx, input.Credential, input.Ctx)
					if !ok {
						return nil, nil
					}
					return &ai.AuthCheck{
						Type:   ai.AuthTypeApiKey,
						Source: &source,
					}, nil
				},
				Resolve: func(ctx context.Context, input ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
					normUrl, source, ok := provCredentialBaseUrl(ctx, input.Credential, input.Ctx)
					if !ok {
						return nil, nil
					}
					apiKey := DefaultLlamaApiKey
					if input.Credential != nil && input.Credential.Key != nil && strings.TrimSpace(*input.Credential.Key) != "" {
						apiKey = strings.TrimSpace(*input.Credential.Key)
					}
					inferenceUrl := LlamaInferenceUrl(normUrl)
					return &ai.AuthResult{
						Auth: ai.ModelAuth{
							ApiKey:  &apiKey,
							BaseUrl: &inferenceUrl,
						},
						Env:    ai.ProviderEnv{LlamaBaseUrlEnvVar: normUrl},
						Source: &source,
					}, nil
				},
			},
		},
		GetModels: func() []*ai.Model {
			mu.RLock()
			defer mu.RUnlock()
			return slices.Clone(chatModels)
		},
		GetAllModels: func() []ai.AnyModel {
			mu.RLock()
			defer mu.RUnlock()
			return provCombineModels(chatModels, classifierModels)
		},
		RefreshModels: func(ctx context.Context, refresh *ai.RefreshModelsContext) error {
			if refresh == nil {
				return nil
			}
			cachedWindows := provExtractCachedContextWindows(refresh.Stored)
			if refresh.Stored != nil {
				var restoredChats []*ai.Model
				var restoredClassifiers []*ai.ClassifierModel
				for _, anyModel := range refresh.Stored.Models {
					switch m := anyModel.(type) {
					case *ai.Model:
						if m != nil {
							restoredChats = append(restoredChats, m)
						}
					case *ai.ClassifierModel:
						if m != nil {
							restoredClassifiers = append(restoredClassifiers, m)
						}
					}
				}
				ok, err := refresh.Publish(&ai.ModelsPublication{
					Update: func() {
						mu.Lock()
						chatModels = restoredChats
						classifierModels = restoredClassifiers
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

			if !refresh.AllowNetwork || (ctx != nil && ctx.Err() != nil) {
				return nil
			}

			apiCred, _ := refresh.Credential.(*ai.ApiKeyCredential)
			managementUrl, _, ok := provCredentialBaseUrl(ctx, apiCred, ai.DefaultProviderAuthContext())
			if !ok {
				if refresh.BaseUrl != nil && strings.TrimSpace(*refresh.BaseUrl) != "" {
					if norm, err := NormalizeLlamaServerUrl(*refresh.BaseUrl); err == nil {
						managementUrl = norm
					}
				}
			}
			if managementUrl == "" {
				return nil
			}

			var keyPtr *string
			if apiCred != nil && apiCred.Key != nil && strings.TrimSpace(*apiCred.Key) != "" {
				k := strings.TrimSpace(*apiCred.Key)
				keyPtr = &k
			}
			client, err := NewLlamaClient(managementUrl, &LlamaClientOptions{
				ApiKey:     keyPtr,
				HTTPClient: httpClient,
			})
			if err != nil {
				return err
			}

			catalog, err := client.List(ctx)
			if err != nil {
				return err
			}

			needsAutoloadCheck := false
			for _, m := range catalog {
				if m.Status.Value == "unloaded" &&
					m.Source != nil && *m.Source == "preset" &&
					!(m.Status.Failed != nil && *m.Status.Failed) {
					needsAutoloadCheck = true
					break
				}
			}
			routerAutoload := false
			if needsAutoloadCheck {
				if props, pErr := client.Props(ctx, nil); pErr == nil && props != nil && props.ModelsAutoload != nil {
					routerAutoload = *props.ModelsAutoload
				}
			}

			templates := make(map[string]string)
			for _, m := range catalog {
				if m.Status.Value != "loaded" {
					continue
				}
				var outMods []string
				if m.Architecture != nil {
					outMods = m.Architecture.OutputModalities
				}
				hasDecisions := slices.Contains(outMods, "decisions")
				hasChat := !hasDecisions || slices.Contains(outMods, "text")
				if !hasChat {
					continue
				}
				id := m.Id
				if props, pErr := client.Props(ctx, &id); pErr == nil && props != nil && props.ChatTemplate != "" {
					templates[id] = props.ChatTemplate
				}
			}

			nextChats, nextClassifiers := provBuildCatalogModels(catalog, managementUrl, routerAutoload, templates, cachedWindows)
			all := provCombineModels(nextChats, nextClassifiers)
			now := js.DateNow()
			_, err = refresh.Publish(&ai.ModelsPublication{
				Persist: jsonx.Some(ai.ModelsStoreEntry{
					Models:    ai.AnyModelList(all),
					CheckedAt: &now,
				}),
				Update: func() {
					mu.Lock()
					chatModels = nextChats
					classifierModels = nextClassifiers
					mu.Unlock()
				},
			})
			return err
		},
		Stream: func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			return api.StreamOpenaiCompletions(ctx, model, transcript, provInjectStreamOptionsHTTPClient(options, httpClient))
		},
		StreamSimple: func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			if options != nil && options.HTTPClient == nil && httpClient != nil {
				cloned := *options
				cloned.HTTPClient = httpClient
				options = &cloned
			} else if options == nil && httpClient != nil {
				options = &ai.SimpleStreamOptions{
					StreamOptions: ai.StreamOptions{
						ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{HTTPClient: httpClient},
					},
				}
			}
			return api.StreamSimpleOpenaiCompletions(ctx, model, transcript, options)
		},
		Classify: func(ctx context.Context, model *ai.ClassifierModel, c *ai.ClassifierContext, options *ai.ClassifierOptions) (*ai.ClassifierResult, error) {
			effectiveOpts := options
			if httpClient != nil {
				if effectiveOpts == nil {
					effectiveOpts = &ai.ClassifierOptions{
						ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{HTTPClient: httpClient},
					}
				} else if effectiveOpts.HTTPClient == nil {
					cloned := *effectiveOpts
					cloned.HTTPClient = httpClient
					effectiveOpts = &cloned
				}
			}
			if model != nil && ai.KnownClassifierApi(model.Api) == ai.KnownClassifierApiTypesafeSystemOne {
				return api.ClassifyTypesafeSystemOne(ctx, model, c, effectiveOpts)
			}
			effectiveModel := model
			if model != nil {
				if norm, err := NormalizeLlamaServerUrl(model.BaseUrl); err == nil && norm != model.BaseUrl {
					cloned := *model
					cloned.BaseUrl = norm
					effectiveModel = &cloned
				}
			}
			return api.ClassifyLlamaCppClassify(ctx, effectiveModel, c, effectiveOpts)
		},
	}

	return &LlamaProviderController{
		Provider:   provider,
		SetCatalog: setCatalog,
	}
}
