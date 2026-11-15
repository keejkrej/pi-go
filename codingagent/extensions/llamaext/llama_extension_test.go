// Ported from packages/coding-agent/test/llama-extension.test.ts (pi v1.5.2).

package llamaext

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/tui"
)

func tleNewServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func tleWriteJSON(w http.ResponseWriter, val any) {
	w.Header().Set("Content-Type", "application/json")
	data, err := jsonx.Marshal(val)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

func tleStrPtr(s string) *string { return &s }

func tleIntPtr(n int) *int { return &n }

func tleBoolPtr(b bool) *bool { return &b }

type tleEmptyAuthContext struct{}

func (tleEmptyAuthContext) Env(context.Context, string) (*string, error) { return nil, nil }
func (tleEmptyAuthContext) FileExists(context.Context, string) (bool, error) {
	return false, nil
}

type tlePromptInteraction struct {
	answers []string
}

func (p *tlePromptInteraction) Prompt(_ context.Context, _ ai.AuthPrompt) (string, error) {
	if len(p.answers) == 0 {
		return "", nil
	}
	v := p.answers[0]
	p.answers = p.answers[1:]
	return v, nil
}

func (*tlePromptInteraction) Notify(ai.AuthEvent) {}

func TestLlamaExtension_RegistersNativeProviderAndCommand(t *testing.T) {
	rt := core.CreateExtensionRuntime()
	ext, err := core.LoadExtensionFromFactoryWithRuntime(
		context.Background(),
		core.InlineExtension{
			Name:    "llama.cpp",
			Factory: Extension,
			Builtin: true,
		},
		t.TempDir(),
		rt,
	)
	if err != nil {
		t.Fatalf("LoadExtensionFromFactoryWithRuntime failed: %v", err)
	}
	cmd, ok := ext.Commands["llama"]
	if !ok {
		t.Fatal("expected /llama command to be registered")
	}
	if cmd.Description == nil || *cmd.Description != "Manage llama.cpp router models" {
		t.Fatalf("unexpected /llama description: %#v", cmd.Description)
	}
	if len(rt.PendingNativeProviderRegistrations) != 1 || rt.PendingNativeProviderRegistrations[0].Provider.Id != LlamaProviderId {
		t.Fatalf("unexpected pending native providers: %#v", rt.PendingNativeProviderRegistrations)
	}
}

func TestLlamaClient_NormalizesUrls(t *testing.T) {
	got, err := NormalizeLlamaServerUrl("http://127.0.0.1:9931/v1/")
	if err != nil || got != "http://127.0.0.1:9931" {
		t.Fatalf("NormalizeLlamaServerUrl = (%q, %v), want %q", got, err, "http://127.0.0.1:9931")
	}
	got, err = NormalizeLlamaServerUrl("https://example.com/prefix/v1")
	if err != nil || got != "https://example.com/prefix" {
		t.Fatalf("NormalizeLlamaServerUrl = (%q, %v), want %q", got, err, "https://example.com/prefix")
	}
	if inf := LlamaInferenceUrl("https://example.com/prefix"); inf != "https://example.com/prefix/v1" {
		t.Fatalf("LlamaInferenceUrl = %q, want %q", inf, "https://example.com/prefix/v1")
	}
	if _, err := NormalizeLlamaServerUrl("file:///tmp/llama"); err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("expected http or https error, got %v", err)
	}
}

func TestLlamaClient_RefusedConnectionIncludesServerAndCause(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	baseUrl := "http://" + addr
	client, err := NewLlamaClient(baseUrl, &LlamaClientOptions{HTTPClient: &http.Client{}})
	if err != nil {
		t.Fatalf("NewLlamaClient failed: %v", err)
	}
	_, err = client.List(context.Background())
	wantSub := fmt.Sprintf("Could not reach llama.cpp server at %s: connect ECONNREFUSED %s", baseUrl, addr)
	if err == nil || !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("error = %v, want substring %q", err, wantSub)
	}
}

func TestLlamaProvider_ExposesLoadedAndSleepingModelsWithMetadata(t *testing.T) {
	ctrl := CreateLlamaProvider()
	ctrl.SetCatalog([]LlamaModelInfo{
		{
			Id:     "loaded",
			Status: LlamaModelStatus{Value: "loaded", Args: []string{"llama-server", "--n-gpu-layers", "999"}},
			Architecture: &LlamaModelArchitecture{
				InputModalities: []string{"text", "image"},
			},
			Meta: &LlamaModelMeta{NCtx: tleIntPtr(65536), NCtxTrain: tleIntPtr(131072)},
		},
		{Id: "sleeping", Status: LlamaModelStatus{Value: "sleeping"}},
		{Id: "unloaded", Status: LlamaModelStatus{Value: "unloaded"}},
		{Id: "loading", Status: LlamaModelStatus{Value: "loading"}},
	}, "http://localhost:9931")

	models := ctrl.Provider.GetModels()
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].Id != "loaded" || models[0].BaseUrl != "http://localhost:9931/v1" ||
		models[0].ContextWindow != 65536 || models[0].MaxTokens != 65536 ||
		!slices.Equal(models[0].Input, []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage}) {
		t.Fatalf("unexpected loaded model: %+v", models[0])
	}
	if models[1].Id != "sleeping" || models[1].BaseUrl != "http://localhost:9931/v1" {
		t.Fatalf("unexpected sleeping model: %+v", models[1])
	}
}

func TestLlamaProvider_DiscoversChatTemplateThinkingSupport(t *testing.T) {
	propsRequests := 0
	srv := tleNewServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			tleWriteJSON(w, LlamaModelsResponse{
				Data: []LlamaModelInfo{
					{Id: "qwen", Status: LlamaModelStatus{Value: "loaded"}, Meta: &LlamaModelMeta{NCtx: tleIntPtr(32768)}},
				},
			})
		case "/props":
			propsRequests++
			if r.URL.Query().Get("model") != "qwen" || r.URL.Query().Get("autoload") != "false" {
				t.Fatalf("unexpected /props query: %s", r.URL.RawQuery)
			}
			tleWriteJSON(w, LlamaServerProps{ChatTemplate: "{% if enable_thinking %}think{% endif %}"})
		default:
			http.NotFound(w, r)
		}
	})

	ctrl := CreateLlamaProvider(&LlamaProviderOptions{HTTPClient: srv.Client()})
	err := ctrl.Provider.RefreshModels(context.Background(), &ai.RefreshModelsContext{
		Credential: &ai.ApiKeyCredential{
			Type: ai.AuthTypeApiKey,
			Key:  tleStrPtr("local"),
			Env:  ai.ProviderEnv{LlamaBaseUrlEnvVar: srv.URL},
		},
		Publish: func(pub *ai.ModelsPublication) (bool, error) {
			if pub.Update != nil {
				pub.Update()
			}
			return true, nil
		},
		AllowNetwork: true,
	})
	if err != nil {
		t.Fatalf("RefreshModels failed: %v", err)
	}
	if propsRequests != 1 {
		t.Fatalf("propsRequests = %d, want 1", propsRequests)
	}
	models := ctrl.Provider.GetModels()
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1", len(models))
	}
	m := models[0]
	if !m.Reasoning || m.ThinkingLevelMap == nil {
		t.Fatalf("expected reasoning=true and ThinkingLevelMap, got %+v", m)
	}
	if off, ok := m.ThinkingLevelMap.Off.Get(); !ok || off != "off" {
		t.Fatalf("unexpected ThinkingLevelMap.Off: %+v", m.ThinkingLevelMap)
	}
	if med, ok := m.ThinkingLevelMap.Medium.Get(); !ok || med != "medium" {
		t.Fatalf("unexpected ThinkingLevelMap.Medium: %+v", m.ThinkingLevelMap)
	}
	if !m.ThinkingLevelMap.Minimal.IsNull() || !m.ThinkingLevelMap.Low.IsNull() ||
		!m.ThinkingLevelMap.High.IsNull() || !m.ThinkingLevelMap.Xhigh.IsNull() {
		t.Fatalf("expected Minimal/Low/High/Xhigh to be Null in ThinkingLevelMap: %+v", m.ThinkingLevelMap)
	}
	compat, ok := m.Compat.V.(*ai.OpenAICompletionsCompat)
	if !ok || compat.ThinkingFormat == nil || *compat.ThinkingFormat != ai.ThinkingFormatQwenChatTemplate {
		t.Fatalf("unexpected Compat: %#v", m.Compat.V)
	}
}

func TestLlamaProvider_PersistsAndRestoresCacheOnlyStartupAndAutoloadContext(t *testing.T) {
	loaded := true
	var unloadedArgs []string
	srv := tleNewServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RequestURI() {
		case "/models":
			var data []LlamaModelInfo
			if loaded {
				data = []LlamaModelInfo{
					{Id: "qwen", Status: LlamaModelStatus{Value: "loaded"}, Source: tleStrPtr("preset"), Meta: &LlamaModelMeta{NCtx: tleIntPtr(65536), NCtxTrain: tleIntPtr(128000)}},
					{Id: "sleeping", Status: LlamaModelStatus{Value: "sleeping"}, Meta: &LlamaModelMeta{NCtx: tleIntPtr(32768)}},
					{Id: "unloaded-cache", Status: LlamaModelStatus{Value: "unloaded"}, Source: tleStrPtr("cache")},
				}
			} else {
				data = []LlamaModelInfo{
					{Id: "qwen", Status: LlamaModelStatus{Value: "unloaded", Args: unloadedArgs}, Source: tleStrPtr("preset"), Meta: &LlamaModelMeta{NCtxTrain: tleIntPtr(128000)}},
				}
			}
			tleWriteJSON(w, LlamaModelsResponse{Data: data})
		case "/props?autoload=false&model=qwen", "/props?model=qwen&autoload=false":
			tleWriteJSON(w, LlamaServerProps{})
		case "/props":
			tleWriteJSON(w, LlamaServerProps{Role: "router", ModelsAutoload: tleBoolPtr(true)})
		default:
			http.NotFound(w, r)
		}
	})

	var cachedEntry *ai.ModelsStoreEntry
	publish := func(pub *ai.ModelsPublication) (bool, error) {
		if pub.Persist.IsNull() {
			cachedEntry = nil
		} else if pVal, ok := pub.Persist.Get(); ok {
			raw, err := jsonx.Marshal(pVal)
			if err != nil {
				return false, err
			}
			var cloned ai.ModelsStoreEntry
			if err := jsonx.Unmarshal(raw, &cloned); err != nil {
				return false, err
			}
			cachedEntry = &cloned
		}
		if pub.Update != nil {
			pub.Update()
		}
		return true, nil
	}
	cred := &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tleStrPtr("local"), Env: ai.ProviderEnv{LlamaBaseUrlEnvVar: srv.URL}}

	first := CreateLlamaProvider(&LlamaProviderOptions{HTTPClient: srv.Client()})
	if err := first.Provider.RefreshModels(context.Background(), &ai.RefreshModelsContext{
		Credential:   cred,
		Publish:      publish,
		AllowNetwork: true,
	}); err != nil {
		t.Fatalf("first refresh failed: %v", err)
	}
	if len(first.Provider.GetModels()) != 2 {
		t.Fatalf("first chat models = %d, want 2", len(first.Provider.GetModels()))
	}

	// Cache-only startup restore with AllowNetwork=false
	restored := CreateLlamaProvider(&LlamaProviderOptions{HTTPClient: srv.Client()})
	if err := restored.Provider.RefreshModels(context.Background(), &ai.RefreshModelsContext{
		Credential:   cred,
		Stored:       cachedEntry,
		Publish:      publish,
		AllowNetwork: false,
	}); err != nil {
		t.Fatalf("cache-only restore failed: %v", err)
	}
	if got := restored.Provider.GetModels(); len(got) != 2 || got[0].ContextWindow != 65536 || got[1].ContextWindow != 32768 {
		t.Fatalf("unexpected restored chat models: %+v", got)
	}

	// Reload while preset is unloaded preserves cached 65536 window when args omit --ctx-size.
	loaded = false
	second := CreateLlamaProvider(&LlamaProviderOptions{HTTPClient: srv.Client()})
	if err := second.Provider.RefreshModels(context.Background(), &ai.RefreshModelsContext{
		Credential:   cred,
		Stored:       cachedEntry,
		Publish:      publish,
		AllowNetwork: true,
	}); err != nil {
		t.Fatalf("second refresh failed: %v", err)
	}
	if got := second.Provider.GetModels(); len(got) != 1 || got[0].ContextWindow != 65536 {
		t.Fatalf("expected cached contextWindow=65536 when unloaded, got %+v", got)
	}

	// Explicit --ctx-size overrides cached contextWindow.
	unloadedArgs = []string{"llama-server", "--ctx-size", "32768"}
	if err := second.Provider.RefreshModels(context.Background(), &ai.RefreshModelsContext{
		Credential:   cred,
		Stored:       cachedEntry,
		Publish:      publish,
		AllowNetwork: true,
	}); err != nil {
		t.Fatalf("third refresh failed: %v", err)
	}
	if got := second.Provider.GetModels(); len(got) != 1 || got[0].ContextWindow != 32768 {
		t.Fatalf("expected --ctx-size 32768 override, got %+v", got)
	}
}

func TestLlamaProvider_AutoloadPresetsAndDecisionClassifiers(t *testing.T) {
	propsModels := make([]string, 0)
	var systemOneRequests []*jsonx.Object

	srv := tleNewServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			if got := r.Header.Get("Authorization"); got != "Bearer local" {
				t.Fatalf("Authorization = %q, want Bearer local", got)
			}
			tleWriteJSON(w, LlamaModelsResponse{
				Data: []LlamaModelInfo{
					{
						Id:           "qwen",
						Status:       LlamaModelStatus{Value: "loaded"},
						Architecture: &LlamaModelArchitecture{InputModalities: []string{"text"}, OutputModalities: []string{"text"}},
						Meta:         &LlamaModelMeta{NCtx: tleIntPtr(32768)},
					},
					{
						Id:           "kev",
						Status:       LlamaModelStatus{Value: "loaded"},
						Architecture: &LlamaModelArchitecture{InputModalities: []string{"text"}, OutputModalities: []string{"decisions"}},
						Meta:         &LlamaModelMeta{NCtx: tleIntPtr(8192)},
					},
					{
						Id:           "laya",
						Status:       LlamaModelStatus{Value: "sleeping"},
						Architecture: &LlamaModelArchitecture{InputModalities: []string{"text"}, OutputModalities: []string{"decisions"}},
					},
					{Id: "preset", Status: LlamaModelStatus{Value: "unloaded"}, Source: tleStrPtr("preset"), Meta: &LlamaModelMeta{NCtx: tleIntPtr(65536)}},
					{Id: "failed-preset", Status: LlamaModelStatus{Value: "unloaded", Failed: tleBoolPtr(true)}, Source: tleStrPtr("preset")},
					{Id: "cache", Status: LlamaModelStatus{Value: "unloaded"}, Source: tleStrPtr("cache")},
				},
			})
		case "/props":
			if m := r.URL.Query().Get("model"); m != "" {
				propsModels = append(propsModels, m)
				tleWriteJSON(w, LlamaServerProps{})
				return
			}
			tleWriteJSON(w, LlamaServerProps{Role: "router", ModelsAutoload: tleBoolPtr(true)})
		case "/v1/systemone":
			var payload *jsonx.Object
			dec, _ := io.ReadAll(r.Body)
			if err := jsonx.Unmarshal(dec, &payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			systemOneRequests = append(systemOneRequests, payload)
			modelName, _ := payload.GetString("model")
			qObj, _ := payload.GetObject("questions")
			answers := jsonx.NewObject()
			if qObj != nil {
				for k := range qObj.All() {
					ans := jsonx.NewObject()
					ans.Set("type", "noul")
					ans.Set("noul", 0.82)
					answers.Set(k, ans)
				}
			}
			resp := jsonx.NewObject()
			resp.Set("model", modelName)
			resp.Set("answers", answers)
			usage := jsonx.NewObject()
			usage.Set("input_tokens", 42)
			usage.Set("output_tokens", 0)
			resp.Set("usage", usage)
			tleWriteJSON(w, resp)
		default:
			http.NotFound(w, r)
		}
	})

	ctrl := CreateLlamaProvider(&LlamaProviderOptions{HTTPClient: srv.Client()})
	cred := &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tleStrPtr("local"), Env: ai.ProviderEnv{LlamaBaseUrlEnvVar: srv.URL}}
	if err := ctrl.Provider.RefreshModels(context.Background(), &ai.RefreshModelsContext{
		Credential: cred,
		Publish: func(pub *ai.ModelsPublication) (bool, error) {
			if pub.Update != nil {
				pub.Update()
			}
			return true, nil
		},
		AllowNetwork: true,
	}); err != nil {
		t.Fatalf("RefreshModels failed: %v", err)
	}

	if !slices.Equal(propsModels, []string{"qwen"}) {
		t.Fatalf("propsModels = %v, want [qwen] (decision model kev must not be probed)", propsModels)
	}

	chats := ctrl.Provider.GetModels()
	chatIds := make([]string, len(chats))
	for i, c := range chats {
		chatIds[i] = c.Id
	}
	if !slices.Equal(chatIds, []string{"qwen", "preset"}) {
		t.Fatalf("chatIds = %v, want [qwen preset]", chatIds)
	}

	var kev *ai.ClassifierModel
	var classifiers [][3]string
	for _, anyModel := range ctrl.Provider.GetAllModels() {
		if cm, ok := anyModel.(*ai.ClassifierModel); ok {
			classifiers = append(classifiers, [3]string{cm.Id, cm.Api, cm.BaseUrl})
			if cm.Id == "kev" {
				kev = cm
			}
		}
	}
	wantClassifiers := [][3]string{
		{"qwen", "llama-cpp-classify", srv.URL},
		{"kev", "typesafe-system-one", srv.URL + "/v1"},
		{"laya", "typesafe-system-one", srv.URL + "/v1"},
		{"preset", "llama-cpp-classify", srv.URL},
	}
	if !slices.Equal(classifiers, wantClassifiers) {
		t.Fatalf("classifiers = %v, want %v", classifiers, wantClassifiers)
	}

	if kev == nil || kev.ContextWindow != 8192 {
		t.Fatalf("unexpected kev classifier: %+v", kev)
	}
	questions := omap.NewMap[string, ai.ClassifierQuestion]()
	questions.Set("angry", &ai.ClassifierBoolQuestion{
		Type:         "bool",
		Instructions: "Is the customer angry?",
		Criteria:     ai.ClassifierBoolCriteria{True: "angry", False: "calm"},
	})
	state := jsonx.NewObject()
	state.Set("message", "I was charged twice.")
	apiKey := "local"
	res, err := ctrl.Provider.Classify(
		context.Background(),
		kev,
		&ai.ClassifierContext{State: state, Questions: questions},
		&ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey:     &apiKey,
				HTTPClient: srv.Client(),
			},
		},
	)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if res.ErrorMessage != nil {
		t.Fatalf("unexpected errorMessage: %s", *res.ErrorMessage)
	}
	ansAny, ok := res.Answers.Get("angry")
	if !ok {
		t.Fatal("missing angry answer")
	}
	boolAns, ok := ansAny.(*ai.ClassifierBoolAnswer)
	if !ok || boolAns.Probability != 0.82 {
		t.Fatalf("unexpected bool answer: %#v", ansAny)
	}
	if len(systemOneRequests) != 1 {
		t.Fatalf("systemOneRequests = %d, want 1", len(systemOneRequests))
	}
}

func TestLlamaProvider_ClassifiesThroughLlamaServerAndStripsV1(t *testing.T) {
	var pathsMu sync.Mutex
	var paths []string
	srv := tleNewServer(t, func(w http.ResponseWriter, r *http.Request) {
		pathsMu.Lock()
		paths = append(paths, r.URL.Path)
		pathsMu.Unlock()
		switch r.URL.Path {
		case "/tokenize":
			rawBody, _ := io.ReadAll(r.Body)
			var payload struct {
				Content string `json:"content"`
			}
			_ = jsonx.Unmarshal(rawBody, &payload)
			runes := []rune(payload.Content)
			tokens := make([]int, len(runes))
			for i, rn := range runes {
				tokens[i] = int(rn)
			}
			tleWriteJSON(w, map[string]any{"tokens": tokens})
		case "/apply-template":
			tleWriteJSON(w, map[string]any{"prompt": "<|im_start|>assistant\n"})
		case "/completion":
			tleWriteJSON(w, map[string]any{
				"completion_probabilities": []any{
					map[string]any{
						"top_logprobs": []any{
							map[string]any{"id": 66, "token": "B", "logprob": -0.1},
							map[string]any{"id": 65, "token": "A", "logprob": -2.4},
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	})

	ctrl := CreateLlamaProvider(&LlamaProviderOptions{HTTPClient: srv.Client()})
	ctrl.SetCatalog([]LlamaModelInfo{{Id: "qwen", Status: LlamaModelStatus{Value: "loaded"}}}, srv.URL)
	var classifier *ai.ClassifierModel
	for _, m := range ctrl.Provider.GetAllModels() {
		if cm, ok := m.(*ai.ClassifierModel); ok {
			classifier = cm
			break
		}
	}
	if classifier == nil {
		t.Fatal("missing classifier model")
	}
	// Simulate auth resolving baseUrl with /v1.
	withV1 := *classifier
	withV1.BaseUrl = srv.URL + "/v1"

	questions := omap.NewMap[string, ai.ClassifierQuestion]()
	criteria := omap.NewMap[string, string]()
	criteria.Set("billing", "")
	criteria.Set("ci", "")
	questions.Set("kind", &ai.ClassifierChoiceQuestion{
		Type:         "choice",
		Instructions: "What is this about?",
		Criteria:     criteria,
	})
	state := jsonx.NewObject()
	state.Set("message", "The build is red again.")
	apiKey := "local"
	res, err := ctrl.Provider.Classify(
		context.Background(),
		&withV1,
		&ai.ClassifierContext{State: state, Questions: questions},
		&ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey:     &apiKey,
				HTTPClient: srv.Client(),
			},
		},
	)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if res.ErrorMessage != nil {
		t.Fatalf("unexpected errorMessage: %s", *res.ErrorMessage)
	}
	ansAny, _ := res.Answers.Get("kind")
	choiceAns, ok := ansAny.(*ai.ClassifierChoiceAnswer)
	if !ok || choiceAns.Choice != "ci" {
		t.Fatalf("unexpected choice answer: %#v", ansAny)
	}
	pathsMu.Lock()
	gotPaths := slices.Clone(paths)
	pathsMu.Unlock()
	if !slices.Contains(gotPaths, "/completion") {
		t.Fatalf("expected /completion path (without /v1), got %v", gotPaths)
	}
}

func TestLlamaProvider_AuthDormantAndLoginResolve(t *testing.T) {
	srv := tleNewServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q, want Bearer secret", r.Header.Get("Authorization"))
		}
		tleWriteJSON(w, LlamaModelsResponse{Data: []LlamaModelInfo{}})
	})

	ctrl := CreateLlamaProvider(&LlamaProviderOptions{HTTPClient: srv.Client()})
	auth := ctrl.Provider.Auth.ApiKey
	if auth == nil {
		t.Fatal("expected ApiKeyAuth")
	}

	chk, err := auth.Check(context.Background(), ai.ApiKeyCheckInput{Ctx: tleEmptyAuthContext{}})
	if err != nil || chk != nil {
		t.Fatalf("Check unconfigured = (%+v, %v), want nil", chk, err)
	}
	res, err := auth.Resolve(context.Background(), ai.ApiKeyResolveInput{Ctx: tleEmptyAuthContext{}})
	if err != nil || res != nil {
		t.Fatalf("Resolve unconfigured = (%+v, %v), want nil", res, err)
	}

	cred, err := auth.Login(context.Background(), &tlePromptInteraction{answers: []string{srv.URL, "secret"}})
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if cred.Key == nil || *cred.Key != "secret" || cred.Env[LlamaBaseUrlEnvVar] != srv.URL {
		t.Fatalf("unexpected credential: %+v", cred)
	}

	resolved, err := auth.Resolve(context.Background(), ai.ApiKeyResolveInput{
		Ctx:        tleEmptyAuthContext{},
		Credential: cred,
	})
	if err != nil || resolved == nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if resolved.Auth.ApiKey == nil || *resolved.Auth.ApiKey != "secret" ||
		resolved.Auth.BaseUrl == nil || *resolved.Auth.BaseUrl != srv.URL+"/v1" ||
		resolved.Env[LlamaBaseUrlEnvVar] != srv.URL {
		t.Fatalf("unexpected resolved auth: %+v", resolved)
	}
}

func TestLlamaProvider_HybridDecisionAndDisabledAutoload(t *testing.T) {
	ctrl := CreateLlamaProvider()
	ctrl.SetCatalog([]LlamaModelInfo{
		{Id: "decide", Status: LlamaModelStatus{Value: "sleeping"}, Architecture: &LlamaModelArchitecture{OutputModalities: []string{"decisions"}}},
		{Id: "hybrid", Status: LlamaModelStatus{Value: "loaded"}, Architecture: &LlamaModelArchitecture{OutputModalities: []string{"text", "decisions"}}},
	}, "http://localhost:9931")

	chats := ctrl.Provider.GetModels()
	if len(chats) != 1 || chats[0].Id != "hybrid" {
		t.Fatalf("unexpected chat models: %+v", chats)
	}
	all := ctrl.Provider.GetAllModels()
	if len(all) != 3 { // 1 chat + 2 classifiers
		t.Fatalf("len(GetAllModels) = %d, want 3", len(all))
	}

	srv := tleNewServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			tleWriteJSON(w, LlamaModelsResponse{Data: []LlamaModelInfo{
				{Id: "preset", Status: LlamaModelStatus{Value: "unloaded"}, Source: tleStrPtr("preset")},
			}})
		case "/props":
			tleWriteJSON(w, LlamaServerProps{Role: "router", ModelsAutoload: tleBoolPtr(false)})
		default:
			http.NotFound(w, r)
		}
	})
	ctrl2 := CreateLlamaProvider(&LlamaProviderOptions{HTTPClient: srv.Client()})
	if err := ctrl2.Provider.RefreshModels(context.Background(), &ai.RefreshModelsContext{
		Credential:   &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tleStrPtr("local"), Env: ai.ProviderEnv{LlamaBaseUrlEnvVar: srv.URL}},
		Publish:      func(pub *ai.ModelsPublication) (bool, error) { pub.Update(); return true, nil },
		AllowNetwork: true,
	}); err != nil {
		t.Fatalf("RefreshModels failed: %v", err)
	}
	if got := ctrl2.Provider.GetModels(); len(got) != 0 {
		t.Fatalf("expected 0 chat models when autoload disabled, got %+v", got)
	}
}

func TestHuggingFaceClient_SearchDetailsAndTokenLookup(t *testing.T) {
	srv := tleNewServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hf-secret" {
			t.Fatalf("Authorization = %q, want Bearer hf-secret", r.Header.Get("Authorization"))
		}
		if r.URL.Path == "/api/models" {
			q := r.URL.Query()
			if q.Get("search") != "qwen coder" || q.Get("filter") != "gguf" || q.Get("sort") != "downloads" {
				t.Fatalf("unexpected search query: %v", q)
			}
			tleWriteJSON(w, []HuggingFaceModel{{Id: "owner/model-GGUF", Downloads: 1200}})
			return
		}
		if r.URL.Path == "/api/models/owner/model-GGUF" && r.URL.Query().Get("blobs") == "true" {
			tleWriteJSON(w, map[string]any{
				"id":    "owner/model-GGUF",
				"gated": "manual",
				"siblings": []any{
					map[string]any{"rfilename": "model-Q5_K_M.gguf", "size": 6000},
					map[string]any{"rfilename": "model-Q4_K_M-00001-of-00002.gguf", "size": 2000},
					map[string]any{"rfilename": "model-Q4_K_M-00002-of-00002.gguf", "size": 3000},
					map[string]any{"rfilename": "mmproj-F16.gguf", "size": 1000},
				},
			})
			return
		}
		http.NotFound(w, r)
	})

	hf := NewHuggingFaceClient(&HuggingFaceClientOptions{
		Token:      "hf-secret",
		BaseUrl:    srv.URL,
		HTTPClient: srv.Client(),
	})
	results, err := hf.Search(context.Background(), "qwen coder")
	if err != nil || len(results) != 1 || results[0].Id != "owner/model-GGUF" {
		t.Fatalf("Search = (%+v, %v)", results, err)
	}
	details, err := hf.Details(context.Background(), "owner/model-GGUF")
	if err != nil {
		t.Fatalf("Details failed: %v", err)
	}
	if details.Gated != "manual" || len(details.Quantizations) != 2 ||
		details.Quantizations[0].Name != "Q4_K_M" || *details.Quantizations[0].Size != 5000 ||
		details.Quantizations[1].Name != "Q5_K_M" || *details.Quantizations[1].Size != 6000 {
		t.Fatalf("unexpected details: %+v", details)
	}

	if tok := FindHuggingFaceToken(map[string]string{"HF_TOKEN": " hf-secret "}); tok != "hf-secret" {
		t.Fatalf("FindHuggingFaceToken = %q, want hf-secret", tok)
	}
	tmp := t.TempDir()
	tokPath := filepath.Join(tmp, "hf-token")
	if err := os.WriteFile(tokPath, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok := FindHuggingFaceToken(map[string]string{"HF_TOKEN_PATH": tokPath}); tok != "file-token" {
		t.Fatalf("FindHuggingFaceToken(HF_TOKEN_PATH) = %q, want file-token", tok)
	}
}

func TestLlamaClient_LoadAndDownloadWithSSEProgress(t *testing.T) {
	var mu sync.Mutex
	loadStatus := "unloaded"
	dlStatus := "missing"
	subs := make(map[chan string]struct{})
	send := func(msg string) {
		mu.Lock()
		targets := make([]chan string, 0, len(subs))
		for ch := range subs {
			targets = append(targets, ch)
		}
		mu.Unlock()
		for _, ch := range targets {
			select {
			case ch <- msg:
			default:
			}
		}
	}

	srv := tleNewServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/models/sse":
			ch := make(chan string, 16)
			mu.Lock()
			subs[ch] = struct{}{}
			mu.Unlock()
			defer func() {
				mu.Lock()
				delete(subs, ch)
				mu.Unlock()
			}()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			for {
				select {
				case <-r.Context().Done():
					return
				case msg := <-ch:
					_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
			}
		case r.URL.Path == "/models/load" && r.Method == http.MethodPost:
			mu.Lock()
			loadStatus = "loading"
			mu.Unlock()
			tleWriteJSON(w, map[string]any{"success": true})
			go func() {
				time.Sleep(15 * time.Millisecond)
				ev1, _ := jsonx.Stringify(map[string]any{
					"model": "test-model",
					"event": "status_change",
					"data": map[string]any{
						"status": "loading",
						"progress": map[string]any{
							"stages":  []string{"text_model", "mmproj_model"},
							"current": "text_model",
							"value":   0.5,
						},
					},
				})
				send(ev1)
				mu.Lock()
				loadStatus = "loaded"
				mu.Unlock()
				ev2, _ := jsonx.Stringify(map[string]any{
					"model": "test-model",
					"event": "status_change",
					"data":  map[string]any{"status": "loaded"},
				})
				send(ev2)
			}()
		case r.URL.Path == "/models" && r.Method == http.MethodPost:
			mu.Lock()
			dlStatus = "downloading"
			mu.Unlock()
			tleWriteJSON(w, map[string]any{"success": true})
			go func() {
				time.Sleep(15 * time.Millisecond)
				ev1, _ := jsonx.Stringify(map[string]any{
					"model": "owner/repo:Q4_K_M",
					"event": "download_progress",
					"data": map[string]any{
						"progress": map[string]any{
							"https://example/model.gguf": map[string]any{"done": 512, "total": 1024},
						},
					},
				})
				send(ev1)
				mu.Lock()
				dlStatus = "unloaded"
				mu.Unlock()
				ev2, _ := jsonx.Stringify(map[string]any{
					"model": "owner/repo:Q4_K_M",
					"event": "download_finished",
					"data":  map[string]any{},
				})
				send(ev2)
			}()
		case r.URL.Path == "/models" && r.Method == http.MethodGet:
			mu.Lock()
			ls := loadStatus
			ds := dlStatus
			mu.Unlock()
			if ds != "missing" {
				tleWriteJSON(w, LlamaModelsResponse{Data: []LlamaModelInfo{{Id: "owner/repo:Q4_K_M", Status: LlamaModelStatus{Value: ds}}}})
				return
			}
			tleWriteJSON(w, LlamaModelsResponse{Data: []LlamaModelInfo{{Id: "test-model", Status: LlamaModelStatus{Value: ls}}}})
		default:
			http.NotFound(w, r)
		}
	})

	client, err := NewLlamaClient(srv.URL, &LlamaClientOptions{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}

	var loadMsgs []string
	loaded, err := client.LoadAndWait(context.Background(), "test-model", func(p LlamaProgress) {
		loadMsgs = append(loadMsgs, p.Message)
	})
	if err != nil || loaded.Status.Value != "loaded" {
		t.Fatalf("LoadAndWait = (%+v, %v)", loaded, err)
	}
	if !slices.Contains(loadMsgs, "Loading text model") {
		t.Fatalf("expected 'Loading text model', got %v", loadMsgs)
	}

	var dlProg []LlamaProgress
	models, err := client.DownloadAndWait(context.Background(), "owner/repo:Q4_K_M", func(p LlamaProgress) {
		dlProg = append(dlProg, p)
	})
	if err != nil || len(models) != 1 || models[0].Id != "owner/repo:Q4_K_M" || models[0].Status.Value != "unloaded" {
		t.Fatalf("DownloadAndWait = (%+v, %v)", models, err)
	}
	if len(dlProg) == 0 || dlProg[0].Message != "Downloading model" ||
		dlProg[0].Ratio == nil || *dlProg[0].Ratio != 0.5 ||
		dlProg[0].Detail == nil || *dlProg[0].Detail != "512 B / 1.00 KiB" {
		t.Fatalf("unexpected download progress: %+v", dlProg)
	}
}

type tleMockUi struct {
	actions  []LlamaManagerAction
	selects  []string
	notifies []string
}

func (u *tleMockUi) Select(_ context.Context, _ string, _ []string) (string, bool, error) {
	if len(u.selects) == 0 {
		return "", false, nil
	}
	v := u.selects[0]
	u.selects = u.selects[1:]
	return v, true, nil
}
func (u *tleMockUi) Confirm(context.Context, string, string) (bool, error) { return true, nil }
func (u *tleMockUi) Input(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}
func (u *tleMockUi) Notify(msg, level string) {
	u.notifies = append(u.notifies, level+":"+msg)
}
func (u *tleMockUi) ShowManager(_ context.Context, _ []LlamaModelInfo) (LlamaManagerAction, error) {
	if len(u.actions) == 0 {
		return LlamaManagerAction{Type: LlamaActionCancel}, nil
	}
	a := u.actions[0]
	u.actions = u.actions[1:]
	return a, nil
}
func (u *tleMockUi) SearchHuggingFace(context.Context, *HuggingFaceClient) (string, bool, error) {
	return "owner/repo:Q4_K_M", true, nil
}
func (u *tleMockUi) RunWithProgress(ctx context.Context, _ string, _ bool, fn func(context.Context, func(LlamaProgress)) error) error {
	return fn(ctx, func(LlamaProgress) {})
}

func TestLlamaExtension_CommandFlowUnloadRestoreAndComponents(t *testing.T) {
	var mu sync.Mutex
	modelStates := map[string]string{
		"cur-loaded": "loaded",
		"next-model": "unloaded",
	}
	var ops []string

	srv := tleNewServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/models/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Closing SSE early exercises GET /models polling fallback too.
		case r.URL.Path == "/models/unload":
			var body struct {
				Model string `json:"model"`
			}
			dec, _ := io.ReadAll(r.Body)
			_ = jsonx.Unmarshal(dec, &body)
			mu.Lock()
			ops = append(ops, "unload:"+body.Model)
			modelStates[body.Model] = "unloaded"
			mu.Unlock()
			tleWriteJSON(w, map[string]any{"success": true})
		case r.URL.Path == "/models/load":
			var body struct {
				Model string `json:"model"`
			}
			dec, _ := io.ReadAll(r.Body)
			_ = jsonx.Unmarshal(dec, &body)
			mu.Lock()
			ops = append(ops, "load:"+body.Model)
			if body.Model == "next-model" {
				mu.Unlock()
				http.Error(w, `{"error":{"message":"OOM"}}`, http.StatusInternalServerError)
				return
			}
			modelStates[body.Model] = "loaded"
			mu.Unlock()
			tleWriteJSON(w, map[string]any{"success": true})
		case r.URL.Path == "/models" && r.Method == http.MethodGet:
			mu.Lock()
			data := []LlamaModelInfo{
				{Id: "cur-loaded", Status: LlamaModelStatus{Value: modelStates["cur-loaded"]}},
				{Id: "next-model", Status: LlamaModelStatus{Value: modelStates["next-model"]}},
			}
			mu.Unlock()
			tleWriteJSON(w, LlamaModelsResponse{Data: data})
		case r.URL.Path == "/props":
			tleWriteJSON(w, LlamaServerProps{})
		default:
			http.NotFound(w, r)
		}
	})

	// Test component rendering cleanly with no panics.
	th := theme.CurrentTheme()
	view := NewLlamaViewComponent(th, []LlamaModelInfo{
		{Id: "cur-loaded", Status: LlamaModelStatus{Value: "loaded"}},
	}, nil)
	lines := view.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines from LlamaViewComponent")
	}

	search := NewHuggingFaceSearchComponent(nil, th, nil, nil, nil)
	search.Dispose()
	if !search.CapturesViewportInput() {
		t.Fatal("expected CapturesViewportInput true")
	}
	if sLines := search.Render(80); len(sLines) == 0 {
		t.Fatal("expected rendered lines from HuggingFaceSearchComponent")
	}

	// Build a minimal runtime + ModelRegistry with llama.cpp credential stored.
	authPath := filepath.Join(t.TempDir(), "auth.json")
	modelsPath := ""
	mrRuntime, err := core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		AuthPath:   &authPath,
		ModelsPath: &modelsPath,
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime failed: %v", err)
	}
	defer mrRuntime.Close()

	rt := core.CreateExtensionRuntime()
	ext, err := core.LoadExtensionFromFactoryWithRuntime(
		context.Background(),
		core.InlineExtension{
			Name: "llama.cpp",
			Factory: NewLlamaExtension(&LlamaExtensionOptions{
				HTTPClient: srv.Client(),
				UiFactory: func(core.ExtensionUIContext) LlamaUi {
					return &tleMockUi{
						selects: []string{"Unload loaded models first"},
						actions: []LlamaManagerAction{
							{Type: LlamaActionLoad, Model: &LlamaModelInfo{Id: "next-model", Status: LlamaModelStatus{Value: "unloaded"}}},
							{Type: LlamaActionCancel},
						},
					}
				},
			}),
			Builtin: true,
		},
		t.TempDir(),
		rt,
	)
	if err != nil {
		t.Fatalf("LoadExtensionFromFactoryWithRuntime failed: %v", err)
	}
	for _, p := range rt.PendingNativeProviderRegistrations {
		if err := mrRuntime.RegisterNativeProvider(p.Provider); err != nil {
			t.Fatal(err)
		}
	}
	reg := core.NewModelRegistry(mrRuntime)
	if _, err := mrRuntime.Login(context.Background(), LlamaProviderId, ai.AuthTypeApiKey, &tlePromptInteraction{answers: []string{srv.URL, "secret"}}, nil); err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	cmdCtx := &tleFakeCommandContext{reg: reg}
	if err := ext.Commands["llama"].Handler(context.Background(), "", cmdCtx); err != nil {
		t.Fatalf("/llama command failed: %v", err)
	}

	mu.Lock()
	gotOps := slices.Clone(ops)
	restoredState := modelStates["cur-loaded"]
	mu.Unlock()
	if !slices.Equal(gotOps, []string{"unload:cur-loaded", "load:next-model", "load:cur-loaded"}) {
		t.Fatalf("expected unload -> failed load -> restore, got %v", gotOps)
	}
	if restoredState != "loaded" {
		t.Fatalf("cur-loaded status = %q, want loaded after restore", restoredState)
	}
}

type tleFakeCommandContext struct {
	reg *core.ModelRegistry
}

func (*tleFakeCommandContext) UI() core.ExtensionUIContext                 { return nil }
func (*tleFakeCommandContext) Mode() core.ExtensionMode                    { return core.ExtensionModeTui }
func (*tleFakeCommandContext) HasUI() bool                                 { return true }
func (*tleFakeCommandContext) Cwd() string                                 { return "." }
func (*tleFakeCommandContext) SessionManager() core.ReadonlySessionManager { return nil }
func (c *tleFakeCommandContext) ModelRegistry() *core.ModelRegistry        { return c.reg }
func (*tleFakeCommandContext) Model() *ai.Model                            { return nil }
func (*tleFakeCommandContext) ScopedModels() []core.ScopedModel            { return nil }
func (*tleFakeCommandContext) ThinkingLevel() agent.ThinkingLevel          { return agent.ThinkingLevelOff }
func (*tleFakeCommandContext) IsIdle() bool                                { return true }
func (*tleFakeCommandContext) IsProjectTrusted() bool                      { return true }
func (*tleFakeCommandContext) Signal() context.Context                     { return context.Background() }
func (*tleFakeCommandContext) Abort()                                      {}
func (*tleFakeCommandContext) HasPendingMessages() bool                    { return false }
func (*tleFakeCommandContext) Shutdown()                                   {}
func (*tleFakeCommandContext) GetContextUsage() *core.ContextUsage         { return nil }
func (*tleFakeCommandContext) Compact(...*core.CompactOptions)             {}
func (*tleFakeCommandContext) GetSystemPrompt() string                     { return "" }
func (*tleFakeCommandContext) WaitForIdle(context.Context) error           { return nil }
func (*tleFakeCommandContext) NewSession(context.Context, ...*core.ExtensionNewSessionOptions) (core.CancelledResult, error) {
	return core.CancelledResult{}, nil
}
func (*tleFakeCommandContext) Fork(context.Context, string, ...*core.ExtensionForkOptions) (core.CancelledResult, error) {
	return core.CancelledResult{}, nil
}
func (*tleFakeCommandContext) NavigateTree(context.Context, string, ...*core.ExtensionNavigateTreeOptions) (core.CancelledResult, error) {
	return core.CancelledResult{}, nil
}
func (*tleFakeCommandContext) SwitchSession(context.Context, string, ...*core.ExtensionSwitchSessionOptions) (core.CancelledResult, error) {
	return core.CancelledResult{}, nil
}
func (*tleFakeCommandContext) Reload(context.Context) error { return nil }
func (*tleFakeCommandContext) GetSystemPromptOptions() core.BuildSystemPromptOptions {
	return core.BuildSystemPromptOptions{}
}
func (*tleFakeCommandContext) SendUserMessage(context.Context, any, ...*core.SendUserMessageOptions) error {
	return nil
}

var _ tui.Component = (*LlamaViewComponent)(nil)
