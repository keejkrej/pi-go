// Ported from packages/coding-agent/src/core/remote-catalog-provider.ts (pi v1.5.2).

package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	rcpDefaultCatalogBaseUrl         = "https://pi.dev"
	rcpRemoteCatalogAttemptTimeoutMs = 4_000
	// RemoteCatalogRefreshIntervalMs is the minimum interval between non-forced catalog requests.
	RemoteCatalogRefreshIntervalMs int64 = 4 * 60 * 60 * 1000
)

// RemoteCatalogModelTypes lists the model types requested from and accepted from pi.dev catalogs.
var RemoteCatalogModelTypes = []ai.ModelType{ai.ModelTypeChat, ai.ModelTypeImage, ai.ModelTypeClassifier}

// PersonalizeCatalog replaces a catalog with a credential-specific model list.
type PersonalizeCatalog func(ctx context.Context, catalog []ai.AnyModel, refresh *ai.RefreshModelsContext) ([]ai.AnyModel, error)

func rcpIsSupportedModelType(obj *jsonx.Object) bool {
	if obj == nil {
		return false
	}
	typVal, ok := obj.Get("type")
	if !ok {
		return true
	}
	typStr, isStr := typVal.(string)
	if !isStr {
		return false
	}
	return slices.Contains(RemoteCatalogModelTypes, ai.ModelType(typStr))
}

func rcpAnyModelId(model ai.AnyModel) string {
	switch m := model.(type) {
	case *ai.Model:
		if m != nil {
			return m.Id
		}
	case *ai.ImageModel:
		if m != nil {
			return m.Id
		}
	case *ai.ClassifierModel:
		if m != nil {
			return m.Id
		}
	}
	return ""
}

func rcpAnyModelProvider(model ai.AnyModel) string {
	switch m := model.(type) {
	case *ai.Model:
		if m != nil {
			return string(m.Provider)
		}
	case *ai.ImageModel:
		if m != nil {
			return string(m.Provider)
		}
	case *ai.ClassifierModel:
		if m != nil {
			return string(m.Provider)
		}
	}
	return ""
}

func rcpMergeModels(baseline []ai.AnyModel, dynamic []ai.AnyModel) []ai.AnyModel {
	merged := omap.NewMap[string, ai.AnyModel]()
	for _, model := range baseline {
		key := fmt.Sprintf("%s\x00%s", ai.GetModelType(model), rcpAnyModelId(model))
		merged.Set(key, model)
	}
	for _, model := range dynamic {
		key := fmt.Sprintf("%s\x00%s", ai.GetModelType(model), rcpAnyModelId(model))
		merged.Set(key, model)
	}
	return merged.Values()
}

func rcpToAnyModels(models []*ai.Model) []ai.AnyModel {
	out := make([]ai.AnyModel, 0, len(models))
	for _, m := range models {
		out = append(out, m)
	}
	return out
}

func rcpFilterChatModels(models []ai.AnyModel) []*ai.Model {
	var out []*ai.Model
	for _, m := range models {
		if ai.IsModelType(m, ai.ModelTypeChat) {
			if chat, ok := m.(*ai.Model); ok && chat != nil {
				out = append(out, chat)
			}
		}
	}
	return out
}

func rcpFilterProviderModels(models []ai.AnyModel, providerId string) []ai.AnyModel {
	var out []ai.AnyModel
	for _, m := range models {
		if rcpAnyModelProvider(m) == providerId {
			out = append(out, m)
		}
	}
	return out
}

func rcpParseCatalog(providerId string, value any) ([]ai.AnyModel, error) {
	var entries []any
	switch v := value.(type) {
	case []any:
		entries = v
	case *jsonx.Object:
		if modelsVal, ok := v.Get("models"); ok {
			if arr, ok := modelsVal.([]any); ok {
				entries = arr
			} else {
				entries = v.Values()
			}
		} else {
			entries = v.Values()
		}
	default:
		return nil, fmt.Errorf("Invalid model catalog for provider %q", providerId)
	}

	var result []ai.AnyModel
	for _, item := range entries {
		obj, ok := item.(*jsonx.Object)
		if !ok || obj == nil || !obj.Has("id") {
			continue
		}
		if !rcpIsSupportedModelType(obj) {
			continue
		}
		cloned := obj.Clone()
		cloned.Set("provider", providerId)
		decoded, err := ai.DecodeAnyModel(cloned)
		if err != nil {
			return nil, err
		}
		result = append(result, decoded)
	}
	return result, nil
}

type rcpCatalogPayload struct {
	models       []ai.AnyModel
	lastModified int64
	etag         *string
}

func rcpReadCatalog(providerId string, resp *http.Response) (rcpCatalogPayload, error) {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return rcpCatalogPayload{}, err
	}
	parsed, err := jsonx.Parse(bodyBytes)
	if err != nil {
		return rcpCatalogPayload{}, err
	}
	models, err := rcpParseCatalog(providerId, parsed)
	if err != nil {
		return rcpCatalogPayload{}, err
	}
	var lastModified int64
	if lmHeader := resp.Header.Get("Last-Modified"); lmHeader != "" {
		if parsedTime, err := http.ParseTime(lmHeader); err == nil {
			lastModified = parsedTime.UnixMilli()
		}
	}
	var etag *string
	if etagHeader := resp.Header.Get("ETag"); etagHeader != "" {
		e := etagHeader
		etag = &e
	}
	return rcpCatalogPayload{
		models:       models,
		lastModified: lastModified,
		etag:         etag,
	}, nil
}

func rcpIsNewer(lastModified *int64, localGeneratedAt *int64) bool {
	return localGeneratedAt == nil || (lastModified != nil && *lastModified > *localGeneratedAt)
}

func rcpRemoteModels(entry *ai.ModelsStoreEntry, localGeneratedAt *int64) []ai.AnyModel {
	if entry != nil && rcpIsNewer(entry.LastModified, localGeneratedAt) {
		return entry.Models
	}
	return nil
}

func rcpBuildCatalogUrl(providerId, catalogBaseUrl string) (string, error) {
	base, err := url.Parse(catalogBaseUrl)
	if err != nil {
		return "", err
	}
	relPath := "/api/models/providers/" + url.PathEscape(providerId)
	ref, err := url.Parse(relPath)
	if err != nil {
		return "", err
	}
	resolved := base.ResolveReference(ref)
	types := make([]string, len(RemoteCatalogModelTypes))
	for i, t := range RemoteCatalogModelTypes {
		types[i] = string(t)
	}
	q := resolved.Query()
	q.Set("types", strings.Join(types, ","))
	resolved.RawQuery = q.Encode()
	return resolved.String(), nil
}

func rcpRequestCatalog(ctx context.Context, providerId, catalogBaseUrl string, validator *string) (*http.Response, error) {
	targetUrl, err := rcpBuildCatalogUrl(providerId, catalogBaseUrl)
	if err != nil {
		return nil, err
	}
	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	headers.Set("User-Agent", utils.GetPiUserAgent(utils.VERSION))
	if validator != nil && *validator != "" {
		headers.Set("If-None-Match", *validator)
	}
	return utils.FetchWithRetry(ctx, targetUrl, &utils.FetchWithRetryOptions{
		Method:           http.MethodGet,
		Headers:          headers,
		AttemptTimeoutMs: rcpRemoteCatalogAttemptTimeoutMs,
	})
}

func rcpGetProviderAllModels(provider *ai.Provider) []ai.AnyModel {
	if provider == nil {
		return nil
	}
	if provider.GetAllModels != nil {
		return provider.GetAllModels()
	}
	if provider.GetModels != nil {
		return rcpToAnyModels(provider.GetModels())
	}
	return nil
}

func rcpCloneProvider(src *ai.Provider) *ai.Provider {
	if src == nil {
		return nil
	}
	return &ai.Provider{
		Id:                src.Id,
		Name:              src.Name,
		BaseUrl:           src.BaseUrl,
		Headers:           src.Headers,
		Auth:              src.Auth,
		GetModels:         src.GetModels,
		GetAllModels:      src.GetAllModels,
		RefreshModels:     src.RefreshModels,
		RefreshModelsAuth: src.RefreshModelsAuth,
		FilterModels:      src.FilterModels,
		FilterAllModels:   src.FilterAllModels,
		Stream:            src.Stream,
		StreamSimple:      src.StreamSimple,
		FetchDeferred:     src.FetchDeferred,
		CancelDeferred:    src.CancelDeferred,
		GenerateImages:    src.GenerateImages,
		Classify:          src.Classify,
	}
}

// WithRemoteCatalog adds a persisted pi.dev catalog overlay to a static built-in provider.
func WithRemoteCatalog(provider *ai.Provider, catalogBaseUrl string, localGeneratedAt *int64) *ai.Provider {
	if catalogBaseUrl == "" {
		catalogBaseUrl = rcpDefaultCatalogBaseUrl
	}
	var mu sync.RWMutex
	var dynamicModels []ai.AnyModel

	base := rcpCloneProvider(provider)
	base.GetModels = func() []*ai.Model {
		mu.RLock()
		dyn := slices.Clone(dynamicModels)
		mu.RUnlock()
		return rcpFilterChatModels(rcpMergeModels(rcpToAnyModels(provider.GetModels()), dyn))
	}
	base.GetAllModels = func() []ai.AnyModel {
		mu.RLock()
		dyn := slices.Clone(dynamicModels)
		mu.RUnlock()
		return rcpMergeModels(rcpGetProviderAllModels(provider), dyn)
	}
	// pi.dev serves the catalog, so a stale provider login must not block or fail its refresh.
	base.RefreshModelsAuth = ai.RefreshModelsAuthConfigured
	base.RefreshModels = func(ctx context.Context, refresh *ai.RefreshModelsContext) error {
		stored := refresh.Stored
		restored := rcpFilterProviderModels(rcpRemoteModels(stored, localGeneratedAt), provider.Id)
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
		if !ok || !refresh.AllowNetwork || ctx.Err() != nil {
			return nil
		}

		force := refresh.Force != nil && *refresh.Force
		if !force && stored != nil && stored.CheckedAt != nil && stored.LastModified != nil &&
			time.Now().UnixMilli()-*stored.CheckedAt < RemoteCatalogRefreshIntervalMs {
			return nil
		}

		var validator *string
		if stored != nil && len(stored.Models) > 0 {
			validator = stored.Etag
		}
		resp, err := rcpRequestCatalog(ctx, provider.Id, catalogBaseUrl, validator)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if ctx.Err() != nil {
			return nil
		}

		checkedAt := time.Now().UnixMilli()
		if resp.StatusCode == http.StatusNotModified && stored != nil {
			nextStored := *stored
			nextStored.CheckedAt = &checkedAt
			_, err := refresh.Publish(&ai.ModelsPublication{
				Persist: jsonx.Some(nextStored),
			})
			return err
		}
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
			var models ai.AnyModelList
			if stored != nil {
				models = stored.Models
			} else {
				models = ai.AnyModelList{}
			}
			zero := int64(0)
			_, err := refresh.Publish(&ai.ModelsPublication{
				Persist: jsonx.Some(ai.ModelsStoreEntry{
					Models:       models,
					CheckedAt:    &checkedAt,
					LastModified: &zero,
					Etag:         nil,
				}),
			})
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var nextStored ai.ModelsStoreEntry
			if stored != nil {
				nextStored = *stored
			} else {
				nextStored = ai.ModelsStoreEntry{Models: ai.AnyModelList{}}
			}
			nextStored.CheckedAt = &checkedAt
			if _, pubErr := refresh.Publish(&ai.ModelsPublication{
				Persist: jsonx.Some(nextStored),
			}); pubErr != nil {
				return pubErr
			}
			return fmt.Errorf("Model catalog request failed for %s: %d", provider.Id, resp.StatusCode)
		}

		catalog, err := rcpReadCatalog(provider.Id, resp)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		entry := ai.ModelsStoreEntry{
			Models:       ai.AnyModelList(catalog.models),
			LastModified: &catalog.lastModified,
			CheckedAt:    &checkedAt,
			Etag:         catalog.etag,
		}
		published := rcpRemoteModels(&entry, localGeneratedAt)
		_, err = refresh.Publish(&ai.ModelsPublication{
			Persist: jsonx.Some(entry),
			Update: func() {
				mu.Lock()
				dynamicModels = published
				mu.Unlock()
			},
		})
		return err
	}
	return base
}

// WithPersonalizedCatalog adds a remote catalog overlay passed through personalize on network refresh.
func WithPersonalizedCatalog(provider *ai.Provider, personalize PersonalizeCatalog, catalogBaseUrl string, localGeneratedAt *int64) *ai.Provider {
	if catalogBaseUrl == "" {
		catalogBaseUrl = rcpDefaultCatalogBaseUrl
	}
	var mu sync.RWMutex
	var personalModels []ai.AnyModel

	builtinModels := func() []ai.AnyModel {
		return rcpGetProviderAllModels(provider)
	}
	getAllModels := func() []ai.AnyModel {
		mu.RLock()
		pm := slices.Clone(personalModels)
		mu.RUnlock()
		if len(pm) > 0 {
			return pm
		}
		return builtinModels()
	}

	base := rcpCloneProvider(provider)
	base.GetModels = func() []*ai.Model {
		return rcpFilterChatModels(getAllModels())
	}
	base.GetAllModels = getAllModels
	base.RefreshModels = func(ctx context.Context, refresh *ai.RefreshModelsContext) error {
		var storedModels []ai.AnyModel
		if refresh.Stored != nil {
			storedModels = refresh.Stored.Models
		}
		restored := rcpFilterProviderModels(storedModels, provider.Id)
		ok, err := refresh.Publish(&ai.ModelsPublication{
			Update: func() {
				mu.Lock()
				personalModels = restored
				mu.Unlock()
			},
		})
		if err != nil {
			return err
		}
		if !ok || !refresh.AllowNetwork || ctx.Err() != nil {
			return nil
		}

		resp, err := rcpRequestCatalog(ctx, provider.Id, catalogBaseUrl, nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if ctx.Err() != nil {
			return nil
		}

		var remote []ai.AnyModel
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusNotImplemented {
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return fmt.Errorf("Model catalog request failed for %s: %d", provider.Id, resp.StatusCode)
			}
			catalog, err := rcpReadCatalog(provider.Id, resp)
			if err != nil {
				return err
			}
			if rcpIsNewer(&catalog.lastModified, localGeneratedAt) {
				remote = catalog.models
			}
		}

		personalized, err := personalize(ctx, rcpMergeModels(builtinModels(), remote), refresh)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		checkedAt := time.Now().UnixMilli()
		_, err = refresh.Publish(&ai.ModelsPublication{
			Persist: jsonx.Some(ai.ModelsStoreEntry{
				Models:    ai.AnyModelList(personalized),
				CheckedAt: &checkedAt,
			}),
			Update: func() {
				mu.Lock()
				personalModels = personalized
				mu.Unlock()
			},
		})
		return err
	}
	return base
}
