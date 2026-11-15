// Ported from packages/coding-agent/src/extensions/llama/index.ts (pi v1.5.2).

package llamaext

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

// LlamaExtensionOptions configures NewLlamaExtension for offline testing or custom UI injection.
type LlamaExtensionOptions struct {
	HTTPClient         *http.Client
	HuggingFaceBaseUrl string
	UiFactory          func(core.ExtensionUIContext) LlamaUi
}

func indexSyncCatalog(
	ctx context.Context,
	ctrl *LlamaProviderController,
	client *LlamaClient,
	ectx core.ExtensionContext,
) ([]LlamaModelInfo, error) {
	models, err := client.Reload(ctx)
	if err != nil {
		models, err = client.List(ctx)
		if err != nil {
			return nil, err
		}
	}
	var autoload bool
	if props, pErr := client.Props(ctx, nil); pErr == nil && props != nil && props.ModelsAutoload != nil {
		autoload = *props.ModelsAutoload
	}
	templates := make(map[string]string)
	for _, m := range models {
		if m.Status.Value == "loaded" {
			id := m.Id
			if props, pErr := client.Props(ctx, &id); pErr == nil && props != nil && props.ChatTemplate != "" {
				templates[id] = props.ChatTemplate
			}
		}
	}
	ctrl.SetCatalog(models, client.BaseUrl(), &LlamaSetCatalogOptions{
		RouterAutoload: autoload,
		Templates:      templates,
	})
	if reg := ectx.ModelRegistry(); reg != nil {
		allowNet := false
		_, _ = reg.Refresh(ctx, &ai.ModelsRefreshOptions{
			AllowNetwork: &allowNet,
			Providers:    []string{LlamaProviderId},
		})
	}
	return models, nil
}

func indexParseRepoSpec(spec string) (repoId string, quant string) {
	trimmed := strings.TrimSpace(spec)
	if colon := strings.LastIndexByte(trimmed, ':'); colon > 0 && colon < len(trimmed)-1 {
		return trimmed[:colon], trimmed[colon+1:]
	}
	return trimmed, ""
}

func indexHandleLoadModel(
	ctx context.Context,
	ctrl *LlamaProviderController,
	client *LlamaClient,
	models []LlamaModelInfo,
	target LlamaModelInfo,
	ui LlamaUi,
	ectx core.ExtensionContext,
) ([]LlamaModelInfo, error) {
	var loaded []LlamaModelInfo
	for _, m := range models {
		if m.Status.Value == "loaded" && m.Id != target.Id {
			loaded = append(loaded, m)
		}
	}

	var unloadedForLoad []LlamaModelInfo
	if len(loaded) > 0 {
		choice, ok, err := ui.Select(ctx, fmt.Sprintf("Load %s", target.Id), []string{
			"Unload loaded models first",
			"Keep loaded models",
			"Cancel",
		})
		if err != nil || !ok || choice == "Cancel" {
			return models, err
		}
		if choice == "Unload loaded models first" {
			if err := ui.RunWithProgress(ctx, "Unloading models", false, func(runCtx context.Context, _ func(LlamaProgress)) error {
				for _, m := range loaded {
					if uErr := client.UnloadAndWait(runCtx, m.Id); uErr != nil {
						return uErr
					}
					unloadedForLoad = append(unloadedForLoad, m)
				}
				return nil
			}); err != nil {
				return models, err
			}
		}
	}

	loadErr := ui.RunWithProgress(ctx, fmt.Sprintf("Loading %s", target.Id), true, func(runCtx context.Context, update func(LlamaProgress)) error {
		_, err := client.LoadAndWait(runCtx, target.Id, update)
		return err
	})
	if loadErr != nil {
		if len(unloadedForLoad) > 0 {
			_ = ui.RunWithProgress(context.Background(), "Restoring previous models", false, func(restoreCtx context.Context, update func(LlamaProgress)) error {
				for _, m := range unloadedForLoad {
					_, _ = client.LoadAndWait(restoreCtx, m.Id, update)
				}
				return nil
			})
		}
		return models, loadErr
	}

	return indexSyncCatalog(ctx, ctrl, client, ectx)
}

func indexHandleUnloadModel(
	ctx context.Context,
	ctrl *LlamaProviderController,
	client *LlamaClient,
	target LlamaModelInfo,
	ui LlamaUi,
	ectx core.ExtensionContext,
) ([]LlamaModelInfo, error) {
	if err := ui.RunWithProgress(ctx, fmt.Sprintf("Unloading %s", target.Id), false, func(runCtx context.Context, _ func(LlamaProgress)) error {
		return client.UnloadAndWait(runCtx, target.Id)
	}); err != nil {
		return nil, err
	}
	return indexSyncCatalog(ctx, ctrl, client, ectx)
}

func indexIsAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "(401)") || strings.Contains(msg, "(403)")
}

func indexIsGated(gated any) bool {
	switch v := gated.(type) {
	case bool:
		return v
	case string:
		return v != "" && v != "false"
	default:
		return false
	}
}

func indexHandleDownloadModel(
	ctx context.Context,
	ctrl *LlamaProviderController,
	client *LlamaClient,
	ui LlamaUi,
	ectx core.ExtensionContext,
	hfOpts *HuggingFaceClientOptions,
) ([]LlamaModelInfo, error) {
	token := FindHuggingFaceToken()
	opts := HuggingFaceClientOptions{Token: token}
	if hfOpts != nil {
		opts.BaseUrl = hfOpts.BaseUrl
		opts.HTTPClient = hfOpts.HTTPClient
		opts.Timeout = hfOpts.Timeout
	}
	hf := NewHuggingFaceClient(&opts)

	for {
		spec, ok, err := ui.SearchHuggingFace(ctx, hf)
		if err != nil {
			return nil, err
		}
		if !ok {
			return indexSyncCatalog(ctx, ctrl, client, ectx)
		}

		repoId, quant := indexParseRepoSpec(spec)
		if quant == "" {
			details, dErr := hf.Details(ctx, repoId)
			if dErr != nil && indexIsAuthError(dErr) {
				tok, tokOk, tokErr := ui.Input(ctx, "Hugging Face token required for "+repoId, "hf_...")
				if tokErr != nil || !tokOk || strings.TrimSpace(tok) == "" {
					if tokErr != nil {
						return nil, tokErr
					}
					continue
				}
				token = strings.TrimSpace(tok)
				hf.SetToken(token)
				details, dErr = hf.Details(ctx, repoId)
			}
			if dErr != nil {
				ui.Notify(dErr.Error(), "error")
				continue
			}
			if indexIsGated(details.Gated) && token == "" {
				tok, tokOk, tokErr := ui.Input(ctx, "Gated model "+repoId+" — enter Hugging Face token", "hf_...")
				if tokErr != nil {
					return nil, tokErr
				}
				if tokOk && strings.TrimSpace(tok) != "" {
					token = strings.TrimSpace(tok)
					hf.SetToken(token)
				}
			}
			if len(details.Quantizations) == 0 {
				ui.Notify("No GGUF quantizations found for "+repoId, "warning")
				continue
			}
			if len(details.Quantizations) == 1 {
				quant = details.Quantizations[0].Name
			} else {
				labels := make([]string, len(details.Quantizations))
				for i, q := range details.Quantizations {
					if q.Size != nil {
						labels[i] = fmt.Sprintf("%s (%s)", q.Name, FormatBytes(*q.Size))
					} else {
						labels[i] = q.Name
					}
				}
				chosen, qOk, qErr := ui.Select(ctx, "Select quantization for "+repoId, labels)
				if qErr != nil {
					return nil, qErr
				}
				if !qOk {
					continue
				}
				quant = strings.SplitN(chosen, " ", 2)[0]
			}
		}

		fullSpec := repoId
		if quant != "" {
			fullSpec = repoId + ":" + quant
		}
		if err := ui.RunWithProgress(ctx, "Downloading "+fullSpec, true, func(runCtx context.Context, update func(LlamaProgress)) error {
			_, dlErr := client.DownloadAndWait(runCtx, fullSpec, update, token)
			return dlErr
		}); err != nil {
			if errors.Is(err, context.Canceled) {
				continue
			}
			ui.Notify(err.Error(), "error")
			continue
		}
		return indexSyncCatalog(ctx, ctrl, client, ectx)
	}
}

func indexRunCommand(
	ctx context.Context,
	ctrl *LlamaProviderController,
	ectx core.ExtensionCommandContext,
	opts *LlamaExtensionOptions,
) error {
	var ui LlamaUi
	if opts != nil && opts.UiFactory != nil {
		ui = opts.UiFactory(ectx.UI())
	} else {
		if !ectx.HasUI() {
			return errors.New("/llama requires an interactive TUI")
		}
		ui = NewExtensionLlamaUi(ectx.UI())
	}

	reg := ectx.ModelRegistry()
	if reg == nil {
		ui.Notify("Model registry is unavailable", "error")
		return nil
	}
	authRes, err := reg.GetProviderAuth(ctx, LlamaProviderId)
	if err != nil || authRes == nil {
		msg := "llama.cpp is not configured. Run /login and select llama.cpp to set the server URL."
		if err != nil {
			msg = fmt.Sprintf("%s (%v)", msg, err)
		}
		ui.Notify(msg, "warning")
		return nil
	}

	serverUrl := authRes.Env[LlamaBaseUrlEnvVar]
	if serverUrl == "" && authRes.Auth.BaseUrl != nil {
		serverUrl = *authRes.Auth.BaseUrl
	}
	if serverUrl == "" {
		serverUrl = DefaultLlamaServerUrl
	}

	var httpClient *http.Client
	var hfOpts *HuggingFaceClientOptions
	if opts != nil {
		httpClient = opts.HTTPClient
		if opts.HuggingFaceBaseUrl != "" || opts.HTTPClient != nil {
			hfOpts = &HuggingFaceClientOptions{
				BaseUrl:    opts.HuggingFaceBaseUrl,
				HTTPClient: opts.HTTPClient,
			}
		}
	}

	client, err := NewLlamaClient(serverUrl, &LlamaClientOptions{
		ApiKey:     authRes.Auth.ApiKey,
		HTTPClient: httpClient,
	})
	if err != nil {
		ui.Notify(err.Error(), "error")
		return nil
	}

	models, err := indexSyncCatalog(ctx, ctrl, client, ectx)
	if err != nil {
		ui.Notify(err.Error(), "error")
		return nil
	}

	for {
		action, err := ui.ShowManager(ctx, models)
		if err != nil {
			ui.Notify(err.Error(), "error")
			return nil
		}
		switch action.Type {
		case LlamaActionCancel:
			return nil
		case LlamaActionLoad:
			if action.Model == nil {
				continue
			}
			next, actErr := indexHandleLoadModel(ctx, ctrl, client, models, *action.Model, ui, ectx)
			if actErr != nil {
				if !errors.Is(actErr, context.Canceled) {
					ui.Notify(actErr.Error(), "error")
				}
				if synced, sErr := indexSyncCatalog(ctx, ctrl, client, ectx); sErr == nil {
					models = synced
				}
			} else {
				models = next
			}
		case LlamaActionUnload:
			if action.Model == nil {
				continue
			}
			next, actErr := indexHandleUnloadModel(ctx, ctrl, client, *action.Model, ui, ectx)
			if actErr != nil {
				ui.Notify(actErr.Error(), "error")
			} else {
				models = next
			}
		case LlamaActionDownload:
			next, actErr := indexHandleDownloadModel(ctx, ctrl, client, ui, ectx, hfOpts)
			if actErr != nil {
				if !errors.Is(actErr, context.Canceled) {
					ui.Notify(actErr.Error(), "error")
				}
			} else if next != nil {
				models = next
			}
		}
	}
}

// NewLlamaExtension returns a configurable llama.cpp ExtensionFactory (primarily for testing).
func NewLlamaExtension(opts *LlamaExtensionOptions) core.ExtensionFactory {
	return func(pi core.ExtensionAPI) error {
		var provOpts *LlamaProviderOptions
		if opts != nil && opts.HTTPClient != nil {
			provOpts = &LlamaProviderOptions{HTTPClient: opts.HTTPClient}
		}
		ctrl := CreateLlamaProvider(provOpts)
		pi.RegisterNativeProvider(ctrl.Provider)

		desc := "Manage llama.cpp router models"
		pi.RegisterCommand("llama", core.RegisteredCommand{
			Name:        "llama",
			Description: &desc,
			Handler: func(ctx context.Context, _ string, ectx core.ExtensionCommandContext) error {
				return indexRunCommand(ctx, ctrl, ectx, opts)
			},
		})
		return nil
	}
}

// Extension registers the built-in llama.cpp native provider and /llama slash command.
func Extension(pi core.ExtensionAPI) error {
	return NewLlamaExtension(nil)(pi)
}
