package ai_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func mrStrPtr(s string) *string { return &s }

func mrChatModel(provider, id, api string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      api,
			Provider: ai.ProviderId(provider),
			BaseUrl:  "https://api.example.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		ContextWindow: 128000,
		MaxTokens:     8192,
	}
}

func mrImageModel(provider, id, api string) *ai.ImageModel {
	return &ai.ImageModel{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      api,
			Provider: ai.ProviderId(provider),
			BaseUrl:  "https://api.example.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Type:   ai.ModelTypeImage,
		Output: []ai.ModelModality{ai.ModelModalityImage},
	}
}

func mrClassifierModel(provider, id, api string) *ai.ClassifierModel {
	return &ai.ClassifierModel{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      api,
			Provider: ai.ProviderId(provider),
			BaseUrl:  "https://api.example.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Type:          ai.ModelTypeClassifier,
		ContextWindow: 8192,
	}
}

func mrDoneStream(model *ai.Model, text string) *ai.AssistantMessageEventStream {
	s := ai.NewAssistantMessageEventStream()
	msg := &ai.AssistantMessage{
		Role:       "assistant",
		Api:        ai.Api(model.Api),
		Provider:   model.Provider,
		Model:      model.Id,
		StopReason: ai.StopReasonStop,
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
	}
	s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg})
	s.End(msg)
	return s
}

func mrUserContext(text string) *ai.Context {
	return &ai.Context{
		Messages: []ai.Message{
			&ai.UserMessage{
				Role:    "user",
				Content: ai.UserMessageContent{Text: &text},
			},
		},
	}
}

func mrHeaderMap(pairs ...string) ai.ProviderHeaders {
	m := omap.NewMap[string, *string]()
	for i := 0; i+1 < len(pairs); i += 2 {
		v := pairs[i+1]
		m.Set(pairs[i], &v)
	}
	return m
}

func mrStaticHeaderMap(pairs ...string) *omap.Map[string, string] {
	m := omap.NewMap[string, string]()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

type mrDummyInteraction struct {
	promptFn func(ctx context.Context, prompt ai.AuthPrompt) (string, error)
}

func (d mrDummyInteraction) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	return d.promptFn(ctx, prompt)
}
func (mrDummyInteraction) Notify(ai.AuthEvent) {}

func TestModelsRuntimeRegistryCredentialsAndModelsList(t *testing.T) {
	t.Run("InMemoryCredentialStore does not expose secrets in List and queues concurrent Modify", func(t *testing.T) {
		ctx := context.Background()
		store := ai.NewInMemoryCredentialStore()

		if _, err := store.Modify(ctx, "provA", func(ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: mrStrPtr("sk-secret-a")}, nil
		}, nil); err != nil {
			t.Fatalf("Modify provA: %v", err)
		}
		if _, err := store.Modify(ctx, "provB", func(ai.Credential) (ai.Credential, error) {
			return &ai.OAuthCredential{
				Type: ai.AuthTypeOAuth,
				OAuthCredentials: ai.OAuthCredentials{
					Refresh: "ref-secret",
					Access:  "acc-secret",
					Expires: float64(js.DateNow() + 60_000),
				},
			}, nil
		}, nil); err != nil {
			t.Fatalf("Modify provB: %v", err)
		}

		infos, err := store.List(ctx, nil)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(infos) != 2 {
			t.Fatalf("expected 2 entries, got %+v", infos)
		}
		serialized, err := jsonx.Marshal(infos)
		if err != nil {
			t.Fatalf("Marshal infos: %v", err)
		}
		for _, secret := range []string{"sk-secret-a", "ref-secret", "acc-secret"} {
			if strings.Contains(string(serialized), secret) {
				t.Fatalf("List output exposed secret %q: %s", secret, serialized)
			}
		}

		// Queue two Modifys concurrently on provA and check sequential execution + abort on queued waiter.
		enteredFirst := make(chan struct{})
		releaseFirst := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.Modify(ctx, "provA", func(cur ai.Credential) (ai.Credential, error) {
				close(enteredFirst)
				<-releaseFirst
				return cur, nil
			}, nil)
		}()
		<-enteredFirst

		abortWaiter, cancelWaiter := context.WithCancel(ctx)
		cancelWaiter()
		if _, err := store.Modify(abortWaiter, "provA", func(cur ai.Credential) (ai.Credential, error) {
			return cur, nil
		}, nil); err == nil {
			t.Fatal("expected canceled queued Modify to fail")
		}
		close(releaseFirst)
		wg.Wait()
	})

	t.Run("CreateProvider validates api config and Provider CRUD preserves order and error isolation", func(t *testing.T) {
		if _, err := ai.CreateProvider(&ai.CreateProviderOptions{Id: "empty"}); err == nil {
			t.Fatal("expected CreateProvider with no api/images/classifiers to fail")
		}
		if _, err := ai.CreateProvider(&ai.CreateProviderOptions{
			Id: "both",
			Api: ai.ProviderStreams{Stream: func(context.Context, *ai.Model, *ai.TranscriptContext, ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
				return nil
			}},
			ApiByModel: map[string]ai.ProviderStreams{"a": {Stream: func(context.Context, *ai.Model, *ai.TranscriptContext, ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
				return nil
			}}},
		}); err == nil {
			t.Fatal("expected CreateProvider with both Api and ApiByModel to fail")
		}

		models := ai.CreateModels(nil)
		p1, err := ai.CreateProvider(&ai.CreateProviderOptions{
			Id:     "p1",
			Auth:   ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("K1", []string{"P1_KEY"})},
			Models: []ai.AnyModel{mrChatModel("p1", "c1", "api1"), mrImageModel("p1", "i1", "img1"), mrClassifierModel("p1", "cls1", "cls1")},
			ApiByModel: map[string]ai.ProviderStreams{
				"api1": {Stream: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, _ ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
					return mrDoneStream(m, "p1")
				}},
			},
			Images: map[string]ai.ProviderImages{
				"img1": {GenerateImages: func(context.Context, *ai.ImageModel, *ai.ImagesContext, *ai.ImagesOptions) (*ai.AssistantImages, error) {
					return &ai.AssistantImages{}, nil
				}},
			},
			Classifiers: map[string]ai.ProviderClassifier{
				"cls1": {Classify: func(context.Context, *ai.ClassifierModel, *ai.ClassifierContext, *ai.ClassifierOptions) (*ai.ClassifierResult, error) {
					return &ai.ClassifierResult{}, nil
				}},
			},
		})
		if err != nil {
			t.Fatalf("CreateProvider p1: %v", err)
		}
		pBad := &ai.Provider{
			Id:        "pbad",
			Name:      "Bad",
			GetModels: func() []*ai.Model { panic("boom in GetModels") },
		}

		models.SetProvider(p1)
		models.SetProvider(pBad)

		providers := models.GetProviders()
		if len(providers) != 2 || providers[0].Id != "p1" || providers[1].Id != "pbad" {
			t.Fatalf("expected [p1, pbad] in registration order, got %+v", providers)
		}

		// GetModels isolates pBad panic and keeps p1's chat model only (not image/classifier).
		chats := models.GetModels(nil)
		if len(chats) != 1 || chats[0].Id != "c1" {
			t.Fatalf("expected only c1 in chat models, got %+v", chats)
		}
		all := models.GetAllModels(mrStrPtr("p1"))
		if len(all) != 3 {
			t.Fatalf("expected 3 models in GetAllModels(p1), got %d", len(all))
		}
		if len(models.GetModelsOfType(ai.ModelTypeImage, nil)) != 1 || models.GetModelOfType(ai.ModelTypeClassifier, "p1", "cls1") == nil {
			t.Fatal("expected image and classifier models accessible by type")
		}
		if !ai.HasApi(chats[0], "api1") || ai.HasApi(mrImageModel("p1", "x", "api1"), "api1") {
			t.Fatal("expected HasApi true only for matching chat model")
		}

		models.DeleteProvider("pbad")
		if models.GetProvider("pbad") != nil {
			t.Fatal("expected pbad deleted")
		}
		models.ClearProviders()
		if len(models.GetProviders()) != 0 {
			t.Fatal("expected ClearProviders to remove all providers")
		}
	})
}

func TestModelsRuntimeCalculateCostAndThinking(t *testing.T) {
	t.Run("CalculateCost applies tiers, 2x 1h cache write, and TotalFromProvider=false", func(t *testing.T) {
		model := mrChatModel("p", "m", "api")
		model.Cost = ai.ModelCost{
			ModelCostRates: ai.ModelCostRates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
			Tiers: []ai.ModelCostTier{
				{InputTokensAbove: 100_000, ModelCostRates: ai.ModelCostRates{Input: 6, Output: 22.5, CacheRead: 0.6, CacheWrite: 7.5}},
				{InputTokensAbove: 200_000, ModelCostRates: ai.ModelCostRates{Input: 10, Output: 30, CacheRead: 1.0, CacheWrite: 12.5}},
			},
		}
		w1h := 50_000
		usage := &ai.Usage{
			Input:        100_000,
			Output:       10_000,
			CacheRead:    20_000,
			CacheWrite:   60_000, // total input = 180k -> hits >100k tier (6 / 22.5 / 0.6 / 7.5)
			CacheWrite1h: &w1h,   // 50k @ 2*6=12/M, 10k @ 7.5/M
		}
		cost := ai.CalculateCost(model, usage)
		wantIn := 0.6
		wantOut := 0.225
		wantRead := 0.012
		wantWrite := (7.5*10_000 + 12.0*50_000) / 1_000_000 // 0.675
		wantTotal := wantIn + wantOut + wantRead + wantWrite
		if math.Abs(cost.Total-wantTotal) > 1e-9 || math.Abs(cost.CacheWrite-wantWrite) > 1e-9 {
			t.Fatalf("unexpected cost breakdown: %+v (want total=%v write=%v)", cost, wantTotal, wantWrite)
		}
		if cost.TotalFromProvider == nil || *cost.TotalFromProvider {
			t.Fatalf("expected TotalFromProvider=false, got %v", cost.TotalFromProvider)
		}
	})

	t.Run("GetSupportedThinkingLevels, ClampThinkingLevel, and ModelsAreEqual", func(t *testing.T) {
		nonReason := mrChatModel("p", "m0", "a")
		nonReason.Reasoning = false
		if lvls := ai.GetSupportedThinkingLevels(nonReason); !slices.Equal(lvls, []ai.ModelThinkingLevel{ai.ModelThinkingLevelOff}) {
			t.Fatalf("expected [off], got %v", lvls)
		}
		if clamped := ai.ClampThinkingLevel(nonReason, ai.ModelThinkingLevelHigh); clamped != ai.ModelThinkingLevelOff {
			t.Fatalf("expected clamped off, got %s", clamped)
		}

		mapped := mrChatModel("p", "m1", "a")
		mapped.ThinkingLevelMap = &ai.ThinkingLevelMap{
			Minimal: jsonx.Null[string](),
			High:    jsonx.Some("high"),
			Xhigh:   jsonx.Some("xhigh"),
		}
		lvls := ai.GetSupportedThinkingLevels(mapped)
		want := []ai.ModelThinkingLevel{
			ai.ModelThinkingLevelOff,
			ai.ModelThinkingLevelLow,
			ai.ModelThinkingLevelMedium,
			ai.ModelThinkingLevelHigh,
			ai.ModelThinkingLevelXhigh,
		}
		if !slices.Equal(lvls, want) {
			t.Fatalf("expected %v, got %v", want, lvls)
		}
		if got := ai.ClampThinkingLevel(mapped, ai.ModelThinkingLevelMinimal); got != ai.ModelThinkingLevelLow {
			t.Fatalf("expected minimal clamped up to low, got %s", got)
		}
		if got := ai.ClampThinkingLevel(mapped, ai.ModelThinkingLevelMax); got != ai.ModelThinkingLevelXhigh {
			t.Fatalf("expected max clamped down to xhigh, got %s", got)
		}

		c1 := mrChatModel("p", "id", "a")
		i1 := mrImageModel("p", "id", "a")
		if ai.ModelsAreEqual(c1, nil) || ai.ModelsAreEqual(c1, i1) || !ai.ModelsAreEqual(c1, mrChatModel("p", "id", "b")) {
			t.Fatal("ModelsAreEqual mismatch")
		}
	})
}

func TestModelsRuntimeRefreshPhasesGenerationAndAbort(t *testing.T) {
	t.Run("Refresh restores cached models before waiting on network, filters providers, and persists across instances", func(t *testing.T) {
		store := ai.NewInMemoryModelsStore()
		creds := ai.NewInMemoryCredentialStore()
		ctx := context.Background()
		_, _ = creds.Modify(ctx, "dyn", func(ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: mrStrPtr("dyn-key")}, nil
		}, nil)

		var sawBaseUrl *string
		var sawForce *bool
		var sawAllowNetwork []bool

		makeDyn := func(id string) (*ai.Provider, error) {
			return ai.CreateProvider(&ai.CreateProviderOptions{
				Id:      id,
				BaseUrl: mrStrPtr("https://override.example.com"),
				Auth:    ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("Dyn", []string{"DYN_UNUSED"})},
				Models:  []ai.AnyModel{mrChatModel(id, "base", "api")},
				FetchModels: func(_ context.Context, r *ai.RefreshModelsContext) ([]ai.AnyModel, error) {
					sawBaseUrl = r.BaseUrl
					sawForce = r.Force
					sawAllowNetwork = append(sawAllowNetwork, r.AllowNetwork)
					return []ai.AnyModel{mrChatModel(id, "remote-1", "api")}, nil
				},
				Api: ai.ProviderStreams{
					Stream: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, _ ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
						return mrDoneStream(m, "ok")
					},
				},
			})
		}

		dyn, err := makeDyn("dyn")
		if err != nil {
			t.Fatalf("makeDyn: %v", err)
		}
		other, err := ai.CreateProvider(&ai.CreateProviderOptions{
			Id:     "other",
			Auth:   ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("Other", []string{"OTHER_UNUSED"})},
			Models: []ai.AnyModel{mrChatModel("other", "o-base", "api")},
			FetchModels: func(context.Context, *ai.RefreshModelsContext) ([]ai.AnyModel, error) {
				t.Fatal("other provider should not be refreshed when filtered")
				return nil, nil
			},
			Api: ai.ProviderStreams{
				Stream: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, _ ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
					return mrDoneStream(m, "ok")
				},
			},
		})
		if err != nil {
			t.Fatalf("other: %v", err)
		}

		m1 := ai.CreateModels(&ai.CreateModelsOptions{ModelsStore: store, Credentials: creds})
		m1.SetProvider(dyn)
		m1.SetProvider(other)

		force := true
		res, err := m1.Refresh(ctx, &ai.ModelsRefreshOptions{Providers: []string{"dyn"}, Force: &force})
		if err != nil || res.Aborted || res.Errors.Len() != 0 {
			t.Fatalf("Refresh failed: res=%+v err=%v", res, err)
		}
		if sawBaseUrl == nil || *sawBaseUrl != "https://override.example.com" || sawForce == nil || !*sawForce {
			t.Fatalf("expected BaseUrl & Force passed to FetchModels, got base=%v force=%v", sawBaseUrl, sawForce)
		}
		if m1.GetModel("dyn", "remote-1") == nil {
			t.Fatal("expected remote-1 after network refresh")
		}

		// Offline restore into a new Models instance using the same store, including filtering unknown model types.
		rawWithUnknown := &ai.ModelsStoreEntry{
			Models: ai.AnyModelList{
				mrChatModel("dyn", "remote-1", "api"),
				&ai.UnknownAnyModel{Raw: func() *jsonx.Object {
					o := jsonx.NewObject()
					o.Set("type", "future-type")
					o.Set("id", "future-id")
					o.Set("provider", "dyn")
					return o
				}()},
			},
		}
		if err := store.Write(ctx, "dyn", rawWithUnknown, nil); err != nil {
			t.Fatalf("store.Write: %v", err)
		}

		m2 := ai.CreateModels(&ai.CreateModelsOptions{ModelsStore: store})
		dyn2, _ := makeDyn("dyn")
		m2.SetProvider(dyn2)

		noNet := false
		if _, err := m2.Refresh(ctx, &ai.ModelsRefreshOptions{AllowNetwork: &noNet}); err != nil {
			t.Fatalf("offline Refresh: %v", err)
		}
		if m2.GetModel("dyn", "remote-1") == nil || len(m2.GetAllModels(mrStrPtr("dyn"))) != 2 {
			t.Fatalf("expected base + restored remote-1 (future-type stripped), got %+v", m2.GetAllModels(mrStrPtr("dyn")))
		}
	})

	t.Run("Refresh restores cache before blocking auth and honors superseding generations", func(t *testing.T) {
		store := ai.NewInMemoryModelsStore()
		_ = store.Write(context.Background(), "p", &ai.ModelsStoreEntry{
			Models: ai.AnyModelList{mrChatModel("p", "cached-first", "api")},
		}, nil)

		models := ai.CreateModels(&ai.CreateModelsOptions{ModelsStore: store})
		var restored []string
		var netCalls int32
		authBlock := make(chan struct{})
		releaseAuth := make(chan struct{})
		firstPublishEntered := make(chan struct{})
		releaseFirstPublish := make(chan struct{})
		var firstStale bool

		p := &ai.Provider{
			Id:   "p",
			Name: "P",
			Auth: ai.ProviderAuth{
				ApiKey: &ai.ApiKeyAuth{
					Name: "Blocking",
					Resolve: func(ctx context.Context, _ ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
						close(authBlock)
						select {
						case <-releaseAuth:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
						k := "ok"
						return &ai.AuthResult{Auth: ai.ModelAuth{ApiKey: &k}}, nil
					},
				},
			},
			RefreshModels: func(ctx context.Context, r *ai.RefreshModelsContext) error {
				if !r.AllowNetwork && r.Stored != nil {
					for _, m := range r.Stored.Models {
						restored = append(restored, m.(*ai.Model).Id)
					}
					_, err := r.Publish(&ai.ModelsPublication{Update: func() {}})
					return err
				}
				callNo := atomic.AddInt32(&netCalls, 1)
				if callNo == 1 {
					close(firstPublishEntered)
					<-releaseFirstPublish
					ok, err := r.Publish(&ai.ModelsPublication{
						Persist: jsonx.Some(ai.ModelsStoreEntry{Models: ai.AnyModelList{mrChatModel("p", "stale-write", "api")}}),
						Update:  func() { firstStale = true },
					})
					if err == nil && ok {
						t.Error("expected stale generation publish to return ok=false")
					}
				}
				return nil
			},
		}
		models.SetProvider(p)

		// Verify cache restore runs before blocking Resolve() acquires auth!
		done1 := make(chan struct{})
		go func() {
			_, _ = models.Refresh(context.Background(), nil)
			close(done1)
		}()
		<-authBlock
		if !slices.Equal(restored, []string{"cached-first"}) {
			t.Fatalf("expected stored cache restored before auth wait, got %v", restored)
		}
		close(releaseAuth)
		<-firstPublishEntered

		// Superseding SetProvider increments generation while call 1 is paused before Publish.
		models.SetProvider(p)
		close(releaseFirstPublish)
		<-done1
		if firstStale {
			t.Fatal("stale publish Update should not run after superseding generation")
		}
	})

	t.Run("RefreshModelsAuth configured vs credential, rotated OAuth persists across cancel, and non-cooperative abort", func(t *testing.T) {
		ctx := context.Background()
		creds := ai.NewInMemoryCredentialStore()
		_, _ = creds.Modify(ctx, "oauthprov", func(ai.Credential) (ai.Credential, error) {
			return &ai.OAuthCredential{
				Type: ai.AuthTypeOAuth,
				OAuthCredentials: ai.OAuthCredentials{
					Refresh: "old-refresh",
					Access:  "old-access",
					Expires: float64(js.DateNow() - 10_000), // expired
				},
			}, nil
		}, nil)

		var oauthRefreshes int32
		refreshStarted := make(chan struct{})
		allowFinish := make(chan struct{})

		models := ai.CreateModels(&ai.CreateModelsOptions{Credentials: creds})
		models.SetProvider(&ai.Provider{
			Id:                "oauthprov",
			Name:              "OAuth",
			RefreshModelsAuth: ai.RefreshModelsAuthConfigured,
			Auth: ai.ProviderAuth{
				OAuth: &ai.OAuthAuth{
					Name: "OAuth",
					Refresh: func(_ context.Context, c *ai.OAuthCredential) (*ai.OAuthCredential, error) {
						atomic.AddInt32(&oauthRefreshes, 1)
						close(refreshStarted)
						<-allowFinish
						return &ai.OAuthCredential{
							Type: ai.AuthTypeOAuth,
							OAuthCredentials: ai.OAuthCredentials{
								Refresh: "rotated-refresh",
								Access:  "new-access",
								Expires: float64(js.DateNow() + 3_600_000),
							},
						}, nil
					},
					ToAuth: func(_ context.Context, c *ai.OAuthCredential) (ai.ModelAuth, error) {
						return ai.ModelAuth{ApiKey: &c.Access}, nil
					},
				},
			},
			RefreshModels: func(_ context.Context, r *ai.RefreshModelsContext) error {
				if r.AllowNetwork && r.Credential != nil {
					t.Error("expected nil Credential when RefreshModelsAuth is configured")
				}
				return nil
			},
		})

		if res, err := models.Refresh(ctx, nil); err != nil || res.Errors.Len() != 0 {
			t.Fatalf("configured Refresh: res=%+v err=%v", res, err)
		}
		if atomic.LoadInt32(&oauthRefreshes) != 0 {
			t.Fatal("RefreshModelsAuthConfigured must not trigger OAuth refresh")
		}

		// Now switch to default RefreshModelsAuthCredential and cancel caller while Refresh is mid-flight:
		// rotated token must STILL be persisted!
		models.GetProvider("oauthprov").RefreshModelsAuth = ai.RefreshModelsAuthCredential
		cancelCtx, cancel := context.WithCancel(ctx)
		resCh := make(chan ai.ModelsRefreshResult, 1)
		go func() {
			r, _ := models.Refresh(cancelCtx, nil)
			resCh <- r
		}()
		<-refreshStarted
		cancel()
		close(allowFinish)
		res := <-resCh
		if !res.Aborted {
			t.Fatal("expected Refresh to report Aborted=true")
		}
		// Give background RefreshStoredOAuthCredential Modify a moment to commit.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			c, _ := creds.Read(context.Background(), "oauthprov", nil)
			if oc, ok := c.(*ai.OAuthCredential); ok && oc.Refresh == "rotated-refresh" {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		c, _ := creds.Read(context.Background(), "oauthprov", nil)
		if oc, ok := c.(*ai.OAuthCredential); !ok || oc.Refresh != "rotated-refresh" {
			t.Fatalf("expected rotated refresh persisted despite caller cancel, got %#v", c)
		}

		// Non-cooperative Resolve abort returns promptly via RaceWithAbortSignal.
		nonCoop := ai.CreateModels(nil)
		nonCoop.SetProvider(&ai.Provider{
			Id: "hang",
			Auth: ai.ProviderAuth{
				ApiKey: &ai.ApiKeyAuth{
					Name: "Hang",
					Resolve: func(context.Context, ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
						time.Sleep(5 * time.Second)
						return nil, nil
					},
				},
			},
		})
		shortCtx, shortCancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer shortCancel()
		if _, err := nonCoop.GetAuth(shortCtx, "hang", nil); err == nil {
			t.Fatal("expected non-cooperative Resolve aborted on context timeout")
		}
	})
}

func TestModelsRuntimeAuthPrecedenceLoginAndErrors(t *testing.T) {
	ctx := context.Background()
	creds := ai.NewInMemoryCredentialStore()
	models := ai.CreateModels(&ai.CreateModelsOptions{Credentials: creds})

	var oauthRefreshCalls int32
	models.SetProvider(&ai.Provider{
		Id:   "dual",
		Name: "Dual",
		Auth: ai.ProviderAuth{
			ApiKey: ai.EnvApiKeyAuth("Dual Key", []string{"DUAL_TEST_API_KEY"}),
			OAuth: &ai.OAuthAuth{
				Name: "Dual OAuth",
				Login: func(_ context.Context, inter ai.AuthInteraction, _ *ai.LoginOptions) (*ai.OAuthCredential, error) {
					code, err := inter.Prompt(context.Background(), &ai.AuthPromptText{Type: ai.AuthPromptKindText, Message: "code"})
					if err != nil {
						return nil, err
					}
					return &ai.OAuthCredential{
						Type: ai.AuthTypeOAuth,
						OAuthCredentials: ai.OAuthCredentials{
							Refresh: "ref-" + code,
							Access:  "acc-" + code,
							Expires: float64(js.DateNow() + 60_000), // < 5m default threshold
						},
					}, nil
				},
				Refresh: func(_ context.Context, c *ai.OAuthCredential) (*ai.OAuthCredential, error) {
					n := atomic.AddInt32(&oauthRefreshCalls, 1)
					return &ai.OAuthCredential{
						Type: ai.AuthTypeOAuth,
						OAuthCredentials: ai.OAuthCredentials{
							Refresh: c.Refresh,
							Access:  fmt.Sprintf("fresh-%d", n),
							Expires: float64(js.DateNow() + 3_600_000),
						},
					}, nil
				},
				ToAuth: func(_ context.Context, c *ai.OAuthCredential) (ai.ModelAuth, error) {
					return ai.ModelAuth{
						ApiKey:  &c.Access,
						BaseUrl: mrStrPtr("https://oauth-baseurl.example.com"),
					}, nil
				},
			},
		},
		GetModels: func() []*ai.Model { return []*ai.Model{mrChatModel("dual", "d1", "api")} },
		FilterModels: func(ms []*ai.Model, c ai.Credential) []*ai.Model {
			if _, isOAuth := c.(*ai.OAuthCredential); isOAuth {
				return nil // policy filters out d1 under OAuth
			}
			return ms
		},
		Stream: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, opts ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			return mrDoneStream(m, m.BaseUrl+"|"+*opts.BaseStreamOptions().ApiKey)
		},
	})

	// 1. Ambient env key resolves when store is empty.
	t.Setenv("DUAL_TEST_API_KEY", "env-key")
	res, err := models.GetAuth(ctx, "dual", nil)
	if err != nil || res == nil || *res.Auth.ApiKey != "env-key" {
		t.Fatalf("expected ambient env key, got %+v err=%v", res, err)
	}

	// 2. Stored API key overrides env; explicit override key wins over stored key.
	_, _ = creds.Modify(ctx, "dual", func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: mrStrPtr("stored-key")}, nil
	}, nil)
	if res, err = models.GetAuth(ctx, "dual", nil); err != nil || *res.Auth.ApiKey != "stored-key" {
		t.Fatalf("expected stored-key, got %+v err=%v", res, err)
	}
	if res, err = models.GetAuth(ctx, "dual", &ai.AuthResolutionOverrides{ApiKey: mrStrPtr("explicit-key")}); err != nil || *res.Auth.ApiKey != "explicit-key" {
		t.Fatalf("expected explicit-key, got %+v err=%v", res, err)
	}

	// 3. Stale stored credential type blocks fallback when provider lacks that auth type.
	onlyApi, _ := ai.CreateProvider(&ai.CreateProviderOptions{
		Id:   "onlyapi",
		Auth: ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("Only", []string{"DUAL_TEST_API_KEY"})},
		Api: ai.ProviderStreams{Stream: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, _ ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			return mrDoneStream(m, "")
		}},
	})
	models.SetProvider(onlyApi)
	_, _ = creds.Modify(ctx, "onlyapi", func(ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{Type: ai.AuthTypeOAuth}, nil
	}, nil)
	if r, err := models.GetAuth(ctx, "onlyapi", nil); err != nil || r != nil {
		t.Fatalf("expected nil auth when stored oauth conflicts with api-key-only provider, got %+v err=%v", r, err)
	}

	// 4. Login stores OAuth credential; CheckAuth does NOT trigger refresh; FilterModels applies in GetAvailable.
	if _, err := models.Login(ctx, "dual", ai.AuthTypeOAuth, mrDummyInteraction{
		promptFn: func(context.Context, ai.AuthPrompt) (string, error) { return "42", nil },
	}, nil); err != nil {
		t.Fatalf("Login OAuth: %v", err)
	}
	if check, err := models.CheckAuth(ctx, "dual", nil); err != nil || check == nil || check.Type != ai.AuthTypeOAuth {
		t.Fatalf("CheckAuth OAuth: %+v err=%v", check, err)
	}
	if atomic.LoadInt32(&oauthRefreshCalls) != 0 {
		t.Fatal("CheckAuth must not trigger OAuth refresh")
	}
	avail, err := models.GetAvailable(ctx, mrStrPtr("dual"), nil)
	if err != nil || len(avail) != 0 {
		t.Fatalf("expected FilterModels to hide d1 under OAuth, got %+v err=%v", avail, err)
	}

	// 5. GetAuth triggers auto-refresh when <5m validity and MinOAuthValidityMs; BaseUrl applies to Stream.
	if r, err := models.GetAuth(ctx, "dual", nil); err != nil || r == nil || *r.Auth.ApiKey != "fresh-1" {
		t.Fatalf("expected refreshed fresh-1, got %+v err=%v", r, err)
	}
	msg, err := models.Complete(ctx, models.GetModel("dual", "d1"), mrUserContext("hi"), nil)
	if err != nil {
		t.Fatalf("Complete with OAuth BaseUrl: %v", err)
	}
	gotText := msg.Content[0].(*ai.TextContent).Text
	if gotText != "https://oauth-baseurl.example.com|fresh-1" {
		t.Fatalf("expected OAuth baseUrl + fresh-1 passed to Stream, got %q", gotText)
	}

	// Larger MinOAuthValidityMs forces another refresh; error wrapped in *ModelsError with Code OAuth when still too short.
	tooLong := int64(7_200_000)
	if _, err := models.GetAuth(ctx, "dual", &ai.AuthResolutionOverrides{MinOAuthValidityMs: &tooLong}); err == nil {
		t.Fatal("expected error when refreshed token expires sooner than MinOAuthValidityMs")
	} else {
		var mErr *ai.ModelsError
		if !errors.As(err, &mErr) || mErr.Code != ai.ModelsErrorCodeOAuth {
			t.Fatalf("expected ModelsErrorCodeOAuth, got %T (%v)", err, err)
		}
	}

	// 6. Logout removes stored credential.
	if err := models.Logout(ctx, "dual", nil); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if c, _ := creds.Read(ctx, "dual", nil); c != nil {
		t.Fatalf("expected nil credential after Logout, got %#v", c)
	}
}

func TestModelsRuntimeHeaderMergingTransformsAndDispatches(t *testing.T) {
	ctx := context.Background()
	var capturedHeaders ai.ProviderHeaders
	var transformCalls int

	model := mrChatModel("opencode", "m1", "api")
	model.Headers = mrStaticHeaderMap("X-Common", "from-model", "x-model-only", "mval")

	provider, err := ai.CreateProvider(&ai.CreateProviderOptions{
		Id:      "opencode",
		Headers: mrHeaderMap("X-Common", "from-provider", "X-Suppressed", "present"),
		Auth: ai.ProviderAuth{
			OAuth: &ai.OAuthAuth{
				Name: "Static",
				ToAuth: func(_ context.Context, _ *ai.OAuthCredential) (ai.ModelAuth, error) {
					k := "k"
					return ai.ModelAuth{
						ApiKey:  &k,
						Headers: mrHeaderMap("X-Common", "from-auth", "x-auth-only", "aval"),
					}, nil
				},
			},
		},
		Models: []ai.AnyModel{model},
		Api: ai.ProviderStreams{
			Stream: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, opts ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
				capturedHeaders = opts.BaseStreamOptions().Headers
				return mrDoneStream(m, "done")
			},
			StreamSimple: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
				capturedHeaders = opts.Headers
				return mrDoneStream(m, "simple")
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}

	creds := ai.NewInMemoryCredentialStore()
	_, _ = creds.Modify(ctx, "opencode", func(ai.Credential) (ai.Credential, error) {
		return &ai.OAuthCredential{
			Type:             ai.AuthTypeOAuth,
			OAuthCredentials: ai.OAuthCredentials{Expires: float64(js.DateNow() + 600_000)},
		}, nil
	}, nil)

	models := ai.CreateModels(&ai.CreateModelsOptions{Credentials: creds})
	models.SetProvider(provider)

	reqHeaders := omap.NewMap[string, *string]()
	over := "from-request"
	reqHeaders.Set("x-common", &over)   // case-insensitive override replaces X-Common -> x-common
	reqHeaders.Set("x-suppressed", nil) // explicit nil overrides X-Suppressed

	msg, err := models.Complete(ctx, model, mrUserContext("hello"), &ai.ModelsApiStreamOptions{
		ProviderStreamOptions: &ai.StreamOptions{
			SessionId: mrStrPtr("sess-77"),
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				Headers: reqHeaders,
			},
		},
		ModelsRequestTransforms: ai.ModelsRequestTransforms{
			TransformHeaders: func(h ai.ProviderHeaders) (ai.ProviderHeaders, error) {
				transformCalls++
				added := "transformed"
				h.Set("X-Added", &added)
				return h, nil
			},
		},
	})
	if err != nil || msg.StopReason != ai.StopReasonStop {
		t.Fatalf("Complete failed: %+v err=%v", msg, err)
	}
	if transformCalls != 1 {
		t.Fatalf("expected TransformHeaders called once, got %d", transformCalls)
	}
	if v, ok := capturedHeaders.Get("x-common"); !ok || v == nil || *v != "from-request" {
		t.Fatalf("expected x-common=from-request overriding case-insensitively, got %#v", v)
	}
	if _, ok := capturedHeaders.Get("X-Common"); ok {
		t.Fatal("expected old-case X-Common replaced by lowercase x-common")
	}
	if v, ok := capturedHeaders.Get("x-suppressed"); !ok || v != nil {
		t.Fatalf("expected x-suppressed nil suppression preserved, got %v (ok=%v)", v, ok)
	}
	if v, ok := capturedHeaders.Get("x-opencode-session"); !ok || v == nil || *v != "sess-77" {
		t.Fatalf("expected OpenCode session header, got %v", v)
	}
	if v, ok := capturedHeaders.Get("X-Added"); !ok || v == nil || *v != "transformed" {
		t.Fatalf("expected TransformHeaders addition X-Added, got %v", v)
	}

	// Unsupported provider / unconfigured provider / missing optional capabilities yield error values cleanly.
	unknown := mrChatModel("nope", "m", "api")
	errMsg, err := models.Complete(ctx, unknown, mrUserContext("hi"), nil)
	if err != nil || errMsg.StopReason != ai.StopReasonError || !strings.Contains(*errMsg.ErrorMessage, "Unknown provider") {
		t.Fatalf("expected Unknown provider error message, got %+v err=%v", errMsg, err)
	}

	if imgRes, err := models.GenerateImages(ctx, mrImageModel("opencode", "im", "imgapi"), &ai.ImagesContext{}, nil); err != nil || imgRes.StopReason != ai.ImagesStopReasonError {
		t.Fatalf("expected image error result when provider lacks GenerateImages, got %+v err=%v", imgRes, err)
	}
	if clsRes, err := models.Classify(ctx, mrClassifierModel("opencode", "cm", "clsapi"), &ai.ClassifierContext{}, nil); err != nil || clsRes.StopReason != ai.ClassifierStopReasonError {
		t.Fatalf("expected classifier error result when provider lacks Classify, got %+v err=%v", clsRes, err)
	}
	if err := models.CancelDeferred(ctx, model, &ai.DeferredHandle{Id: "h"}, nil); err == nil {
		t.Fatal("expected CancelDeferred error when provider lacks CancelDeferred")
	}
}
