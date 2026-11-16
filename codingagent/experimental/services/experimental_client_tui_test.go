// Ported from packages/coding-agent/test/experimental-client-tui.test.ts (pi v1.0.0).

package services_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/chord/node"
	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/codingagent/experimental"
	"github.com/keejkrej/pi-go/codingagent/experimental/plugins"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/tui"
)

const tectServerId = protocol.ServerId("00000000-0000-4000-8000-000000000001")

var tectInitThemeOnce sync.Once

type tectFakeTerminal struct {
	mu      sync.Mutex
	onInput func(string)
}

func (ft *tectFakeTerminal) Start(onInput func(string), _ func()) {
	ft.mu.Lock()
	ft.onInput = onInput
	ft.mu.Unlock()
}

func (ft *tectFakeTerminal) Stop() {}

func (ft *tectFakeTerminal) DrainInput(*int64, *int64) error { return nil }

func (ft *tectFakeTerminal) Write(string) {}

func (ft *tectFakeTerminal) Columns() int { return 80 }

func (ft *tectFakeTerminal) Rows() int { return 24 }

func (ft *tectFakeTerminal) KittyProtocolActive() bool { return false }

func (ft *tectFakeTerminal) MoveBy(int) {}

func (ft *tectFakeTerminal) HideCursor() {}

func (ft *tectFakeTerminal) ShowCursor() {}

func (ft *tectFakeTerminal) ClearLine() {}

func (ft *tectFakeTerminal) ClearFromCursor() {}

func (ft *tectFakeTerminal) ClearScreen() {}

func (ft *tectFakeTerminal) SetTitle(string) {}

func (ft *tectFakeTerminal) SetProgress(bool) {}

func (ft *tectFakeTerminal) SetProgramStatus(tui.ProgramStatus) {}

type tectOpenServices struct {
	binding chord.RemoteServiceBinding
	ready   func(call chord.Context) error
}

func (o *tectOpenServices) Use(service any) any {
	return o.binding.Use(service)
}

func (o *tectOpenServices) Observe(service any, handler func(service any, call chord.Context) error) func() {
	return o.binding.Observe(service, handler)
}

func (o *tectOpenServices) Ready(call chord.Context) error {
	if o.ready != nil {
		return o.ready(call)
	}
	return o.binding.Ready(call)
}

func (o *tectOpenServices) Dispose(chord.Context) error {
	return nil
}

type tectServerSource struct {
	binding    chord.RemoteServiceBinding
	connection chord.ReplicatedState[services.ServerConnectionState]
	catalogue  []chord.ServiceCatalogueEntry
}

func (s *tectServerSource) AcceptsUnavailableServices() bool { return false }

func (s *tectServerSource) Catalogue(chord.Context) ([]chord.ServiceCatalogueEntry, error) {
	return s.catalogue, nil
}

func (s *tectServerSource) Connection() chord.ReplicatedState[services.ServerConnectionState] {
	return s.connection
}

func (s *tectServerSource) Open(*chord.RemoteServiceSourceOpenOptions) chord.RemoteServices {
	return &tectOpenServices{
		binding: s.binding,
		ready: func(call chord.Context) error {
			if err := s.binding.Rebind(true, call); err != nil {
				return err
			}
			return s.binding.Ready(call)
		},
	}
}

func (s *tectServerSource) Dispose(ctx chord.Context) error {
	return s.binding.Dispose(ctx)
}

type tectSessionSource struct {
	binding      chord.RemoteServiceBinding
	attachment   chord.MutableReplicatedState[services.SessionAttachmentState]
	whenAttached func(sessionId string, ctx chord.Context) error
	whenDetached func(ctx chord.Context) error
}

func (s *tectSessionSource) AcceptsUnavailableServices() bool { return true }

func (s *tectSessionSource) Catalogue(chord.Context) ([]chord.ServiceCatalogueEntry, error) {
	return nil, nil
}

func (s *tectSessionSource) Attachment() chord.ReplicatedState[services.SessionAttachmentState] {
	return s.attachment
}

func (s *tectSessionSource) Open(*chord.RemoteServiceSourceOpenOptions) chord.RemoteServices {
	return &tectOpenServices{binding: s.binding}
}

func (s *tectSessionSource) WhenAttached(sessionId string, ctx chord.Context) error {
	return s.whenAttached(sessionId, ctx)
}

func (s *tectSessionSource) WhenDetached(ctx chord.Context) error {
	return s.whenDetached(ctx)
}

func (s *tectSessionSource) Dispose(ctx chord.Context) error {
	return s.binding.Dispose(ctx)
}

type tectSessionMgmtImpl struct {
	create func(services.SessionCreateOptions, chord.Context) (services.SessionSummary, error)
	remove func(string, chord.Context) error
	attach func(string, chord.Context) error
	detach func(chord.Context) error
}

func (m *tectSessionMgmtImpl) Create(opts services.SessionCreateOptions, ctx chord.Context) (services.SessionSummary, error) {
	return m.create(opts, ctx)
}
func (m *tectSessionMgmtImpl) Remove(id string, ctx chord.Context) error { return m.remove(id, ctx) }
func (m *tectSessionMgmtImpl) Attach(id string, ctx chord.Context) error { return m.attach(id, ctx) }
func (m *tectSessionMgmtImpl) Detach(ctx chord.Context) error            { return m.detach(ctx) }

type tectPresentationPluginsImpl struct {
	prepare func(services.PrepareSessionPluginsRequest, chord.Context) (chord.JsonValue, error)
	reload  func(chord.Context) (chord.JsonValue, error)
}

func (p *tectPresentationPluginsImpl) PrepareSession(req services.PrepareSessionPluginsRequest, ctx chord.Context) (chord.JsonValue, error) {
	return p.prepare(req, ctx)
}
func (p *tectPresentationPluginsImpl) Reload(ctx chord.Context) (chord.JsonValue, error) {
	return p.reload(ctx)
}

type tectSessionPluginsImpl struct {
	reload func(chord.Context) error
}

func (p *tectSessionPluginsImpl) Reload(ctx chord.Context) error {
	return p.reload(ctx)
}

type tectModelsImpl struct {
	state             any
	cycleThinking     func(chord.Context) error
	getThinkingLevels func(chord.Context) ([]ai.ModelThinkingLevel, error)
	refresh           func(chord.Context) error
	selectFn          func(services.ModelRef, chord.Context) error
	selectThinking    func(ai.ModelThinkingLevel, chord.Context) error
}

func (m *tectModelsImpl) State() chord.ReplicatedState[services.ModelsState] { return nil }
func (m *tectModelsImpl) CycleThinking(ctx chord.Context) error              { return m.cycleThinking(ctx) }
func (m *tectModelsImpl) GetThinkingLevels(ctx chord.Context) ([]ai.ModelThinkingLevel, error) {
	return m.getThinkingLevels(ctx)
}
func (m *tectModelsImpl) Refresh(ctx chord.Context) error { return m.refresh(ctx) }
func (m *tectModelsImpl) Select(model services.ModelRef, ctx chord.Context) error {
	return m.selectFn(model, ctx)
}
func (m *tectModelsImpl) SelectThinking(level ai.ModelThinkingLevel, ctx chord.Context) error {
	return m.selectThinking(level, ctx)
}

func tectSession(sessionId string, createdAt int64) services.SessionSummary {
	return services.SessionSummary{
		ServerId:  tectServerId,
		SessionId: sessionId,
		CreatedAt: createdAt,
	}
}

func tectEncodeJson(t *testing.T, val any) chord.JsonValue {
	t.Helper()
	raw, err := jsonx.Marshal(val)
	if err != nil {
		t.Fatalf("jsonx.Marshal: %v", err)
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		t.Fatalf("jsonx.Parse: %v", err)
	}
	return parsed
}

func tectPtr[T any](v T) *T {
	return &v
}

func tectWaitFor(t *testing.T, desc string, fn func() bool, onTimeout ...func() string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(onTimeout) > 0 && onTimeout[0] != nil {
		t.Fatalf("timed out waiting for %s; state:\n%s", desc, onTimeout[0]())
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func tectRenderText(c *experimental.ClientTui) string {
	return strings.Join(c.Render(80), "\n")
}

func TestExperimentalClientTuiLifecycle(t *testing.T) {
	tectInitThemeOnce.Do(func() {
		_, _ = theme.InitTheme("dark", false)
	})

	cases := []struct {
		kind      string
		command   experimentalcli.ClientCommand
		sessionId string
		creates   int
	}{
		{
			kind:      "new",
			command:   experimentalcli.ClientCommand{Command: "client"},
			sessionId: "two",
			creates:   1,
		},
		{
			kind:      "continued",
			command:   experimentalcli.ClientCommand{Command: "client", Continue: true},
			sessionId: "one",
			creates:   0,
		},
		{
			kind:      "plugin-selected",
			command:   experimentalcli.ClientCommand{Command: "client", PluginPackages: []string{"./example-plugin"}},
			sessionId: "two",
			creates:   1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			var mu sync.Mutex
			dirVal := services.SessionDirectoryState{
				Revision: 1,
				Sessions: []services.SessionSummary{tectSession("one", 1)},
			}
			directoryState, err := chord.NewReplicatedState[chord.JsonValue](tectEncodeJson(t, dirVal))
			if err != nil {
				t.Fatalf("NewReplicatedState(directory): %v", err)
			}
			attachmentState, err := chord.NewReplicatedState(services.SessionAttachmentState{Status: "detached"})
			if err != nil {
				t.Fatalf("NewReplicatedState(attachment): %v", err)
			}
			connectionState, err := chord.NewReplicatedState(services.ServerConnectionState{
				Status: "connected",
				Since:  tectPtr("now"),
			})
			if err != nil {
				t.Fatalf("NewReplicatedState(connection): %v", err)
			}
			modelsVal := services.ModelsState{
				Catalog: services.ModelsCatalogState{
					Revision: 1,
					AvailableModels: []services.ModelSummary{
						{Provider: "test", ModelId: "one", Name: "Model One", Reasoning: false},
						{Provider: "test", ModelId: "two", Name: "Model Two", Reasoning: true},
					},
				},
				Configuration: services.ModelsConfigurationState{
					Model:         &services.ModelRef{Provider: "test", ModelId: "one"},
					ThinkingLevel: ai.ModelThinkingLevelOff,
				},
				Refresh: services.ModelsRefreshState{Status: "idle"},
			}
			modelsState, err := chord.NewReplicatedState[chord.JsonValue](tectEncodeJson(t, modelsVal))
			if err != nil {
				t.Fatalf("NewReplicatedState(models): %v", err)
			}

			var createCalls int
			var selectedModels []services.ModelRef
			var selectedThinkings []ai.ModelThinkingLevel
			var prompts []string
			promptGate := make(chan struct{})
			var closePromptGateOnce sync.Once
			releasePrompt := func() {
				closePromptGateOnce.Do(func() {
					close(promptGate)
				})
			}

			fc, err := testkit.OpenFauxConversation(providers.FauxResponseFactory(func(
				ctx context.Context,
				tx *ai.TranscriptContext,
				_ *ai.SimpleStreamOptions,
				_ *providers.FauxProviderState,
				_ *ai.Model,
			) (*ai.AssistantMessage, error) {
				if tx != nil && len(tx.Messages) > 0 {
					last := tx.Messages[len(tx.Messages)-1]
					if um, ok := last.(*ai.UserMessage); ok {
						text := testkit.GetMessageText(um)
						mu.Lock()
						prompts = append(prompts, text)
						mu.Unlock()
					}
				}
				select {
				case <-promptGate:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("remote answer"), nil), nil
			}))
			if err != nil {
				t.Fatalf("OpenFauxConversation: %v", err)
			}
			defer func() {
				releasePrompt()
				_ = fc.Close()
			}()

			viewState, err := fc.Conversation.ViewState(chord.BackgroundContext)
			if err != nil {
				t.Fatalf("ViewState: %v", err)
			}
			initialView, _ := viewState.Value()
			transcriptState, err := chord.NewReplicatedState[chord.JsonValue](tectEncodeJson(t, initialView))
			if err != nil {
				viewState.Dispose()
				t.Fatalf("NewReplicatedState(transcript): %v", err)
			}
			unsubView := viewState.Subscribe(func(value durable.ConversationView, call chord.Context, _ chord.ReplicatedStateDelivery) error {
				if call == nil {
					call = chord.BackgroundContext
				}
				return transcriptState.Replace(call, tectEncodeJson(t, value))
			})
			defer func() {
				unsubView()
				viewState.Dispose()
			}()

			reloadSource := "\"use strict\";\nconst { defineFacet } = require(\"@earendil-works/chord\");\nmodule.exports = { default: defineFacet({ id: \"test-tui-facet\", setup() {} }) };\n"
			sum := sha256.Sum256([]byte(reloadSource))
			reloadArtifact := node.FacetBundleArtifact{
				Format:        node.FacetBundleArtifactFormat,
				FormatVersion: node.FacetBundleArtifactFormatVersion,
				Plugin:        node.FacetBundlePlugin{Id: "test-tui-plugin"},
				EntryName:     "tui",
				Entry: node.FacetBundleEntry{
					File:            "tui.cjs",
					Integrity:       "sha256-" + base64.StdEncoding.EncodeToString(sum[:]),
					ExternalImports: []string{"@earendil-works/chord"},
				},
				Source: reloadSource,
			}
			reloadData, err := plugins.CreatePresentationFacetData([]node.FacetBundleArtifact{reloadArtifact})
			if err != nil {
				t.Fatalf("CreatePresentationFacetData: %v", err)
			}
			// Verify pure-Go CJS loader reports explicit unsupported error without panicking.
			cjsLoaders, err := plugins.CreatePresentationFacetLoaders(reloadData)
			if err != nil || len(cjsLoaders) != 1 {
				t.Fatalf("CreatePresentationFacetLoaders: loaders=%v err=%v", cjsLoaders, err)
			}
			if _, err := cjsLoaders[0].Load(); err == nil || !strings.Contains(err.Error(), "unsupported") {
				t.Fatalf("expected CJS unsupported error, got %v", err)
			}

			var preparedReq *services.PrepareSessionPluginsRequest
			var reloadPresentationCount atomic.Int32
			var reloadSessionCount atomic.Int32

			singletonMode := chord.ServiceModeSingleton
			serverProvider, err := chord.NewRemoteServiceProvider([]chord.RemoteServiceProviderEntry{
				{Id: services.SessionDirectoryService.Id, Mode: &singletonMode},
				{Id: services.SessionManagementService.Id, Mode: &singletonMode},
				{Id: services.PresentationPluginsService.Id, Mode: &singletonMode},
			})
			if err != nil {
				t.Fatalf("NewRemoteServiceProvider(server): %v", err)
			}
			defer func() { _ = serverProvider.Dispose() }()

			if err := serverProvider.Provide(services.SessionDirectoryService, services.ProvideSessionDirectory(directoryState)); err != nil {
				t.Fatalf("Provide(SessionDirectory): %v", err)
			}
			if err := serverProvider.Provide(services.PresentationPluginsService, services.ProvidePresentationPlugins(&tectPresentationPluginsImpl{
				prepare: func(req services.PrepareSessionPluginsRequest, _ chord.Context) (chord.JsonValue, error) {
					mu.Lock()
					cp := services.PrepareSessionPluginsRequest{
						SessionId:    req.SessionId,
						PackagePaths: slices.Clone(req.PackagePaths),
					}
					preparedReq = &cp
					mu.Unlock()
					return reloadData, nil
				},
				reload: func(chord.Context) (chord.JsonValue, error) {
					reloadPresentationCount.Add(1)
					return reloadData, nil
				},
			})); err != nil {
				t.Fatalf("Provide(PresentationPlugins): %v", err)
			}
			if err := serverProvider.Provide(services.SessionManagementService, services.ProvideSessionManagement(&tectSessionMgmtImpl{
				create: func(_ services.SessionCreateOptions, ctx chord.Context) (services.SessionSummary, error) {
					created := tectSession("two", 2)
					mu.Lock()
					createCalls++
					dirVal.Revision = 2
					dirVal.Sessions = append(dirVal.Sessions, created)
					nextVal := dirVal
					mu.Unlock()
					if err := directoryState.Replace(ctx, tectEncodeJson(t, nextVal)); err != nil {
						return services.SessionSummary{}, err
					}
					return created, nil
				},
				remove: func(string, chord.Context) error { return nil },
				attach: func(sessionId string, ctx chord.Context) error {
					return attachmentState.Replace(ctx, services.SessionAttachmentState{Status: "attaching", SessionId: sessionId})
				},
				detach: func(ctx chord.Context) error {
					return attachmentState.Replace(ctx, services.SessionAttachmentState{Status: "detached"})
				},
			})); err != nil {
				t.Fatalf("Provide(SessionManagement): %v", err)
			}

			sessionProvider, err := chord.NewRemoteServiceProvider([]chord.RemoteServiceProviderEntry{
				{Id: services.ModelsService.Id, Mode: &singletonMode},
				{Id: services.AgentControllerService.Id, Mode: &singletonMode},
				{Id: services.SessionPluginsService.Id, Mode: &singletonMode},
				{Id: services.TranscriptService.Id, Mode: &singletonMode},
			})
			if err != nil {
				t.Fatalf("NewRemoteServiceProvider(session): %v", err)
			}
			defer func() { _ = sessionProvider.Dispose() }()

			if err := sessionProvider.Provide(services.SessionPluginsService, services.ProvideSessionPlugins(&tectSessionPluginsImpl{
				reload: func(chord.Context) error {
					reloadSessionCount.Add(1)
					return nil
				},
			})); err != nil {
				t.Fatalf("Provide(SessionPlugins): %v", err)
			}
			if err := sessionProvider.Provide(services.ModelsService, services.ProvideModels(modelsState, &tectModelsImpl{
				cycleThinking: func(chord.Context) error { return nil },
				getThinkingLevels: func(chord.Context) ([]ai.ModelThinkingLevel, error) {
					return []ai.ModelThinkingLevel{ai.ModelThinkingLevelOff, ai.ModelThinkingLevelHigh}, nil
				},
				refresh: func(chord.Context) error { return nil },
				selectFn: func(model services.ModelRef, ctx chord.Context) error {
					mu.Lock()
					selectedModels = append(selectedModels, model)
					modelsVal.Configuration.Model = &model
					nextVal := modelsVal
					mu.Unlock()
					return modelsState.Replace(ctx, tectEncodeJson(t, nextVal))
				},
				selectThinking: func(level ai.ModelThinkingLevel, ctx chord.Context) error {
					mu.Lock()
					selectedThinkings = append(selectedThinkings, level)
					modelsVal.Configuration.ThinkingLevel = level
					nextVal := modelsVal
					mu.Unlock()
					return modelsState.Replace(ctx, tectEncodeJson(t, nextVal))
				},
			})); err != nil {
				t.Fatalf("Provide(Models): %v", err)
			}
			if err := sessionProvider.Provide(services.AgentControllerService, services.ProvideAgentController(services.CreateAgentController(fc.Harness, fc.Conversation))); err != nil {
				t.Fatalf("Provide(AgentController): %v", err)
			}
			if err := sessionProvider.Provide(services.TranscriptService, services.ProvideTranscript(transcriptState)); err != nil {
				t.Fatalf("Provide(Transcript): %v", err)
			}

			boundFalse := false
			serverNamespace, err := chord.CreateRemoteServiceBinding(&chord.RemoteServiceBindingOptions{
				Services: []chord.ServiceRef{
					{Id: services.SessionDirectoryService.Id},
					{Id: services.SessionManagementService.Id},
					{Id: services.PresentationPluginsService.Id},
				},
				Transport: chord.CreateLoopbackServiceTransport(serverProvider),
				Bound:     &boundFalse,
			})
			if err != nil {
				t.Fatalf("CreateRemoteServiceBinding(server): %v", err)
			}
			defer func() { _ = serverNamespace.Dispose(chord.BackgroundContext) }()

			sessionNamespace, err := chord.CreateRemoteServiceBinding(&chord.RemoteServiceBindingOptions{
				Services: []chord.ServiceRef{
					{Id: services.ModelsService.Id},
					{Id: services.AgentControllerService.Id},
					{Id: services.SessionPluginsService.Id},
					{Id: services.TranscriptService.Id},
				},
				Transport: chord.CreateLoopbackServiceTransport(sessionProvider),
				Bound:     &boundFalse,
			})
			if err != nil {
				t.Fatalf("CreateRemoteServiceBinding(session): %v", err)
			}
			defer func() { _ = sessionNamespace.Dispose(chord.BackgroundContext) }()

			serverSource := &tectServerSource{
				binding:    serverNamespace,
				connection: connectionState,
				catalogue:  serverProvider.Catalogue(),
			}
			sessionSource := &tectSessionSource{
				binding:    sessionNamespace,
				attachment: attachmentState,
				whenAttached: func(sessionId string, ctx chord.Context) error {
					if err := sessionNamespace.Rebind(true, ctx); err != nil {
						return err
					}
					if err := sessionNamespace.Ready(ctx); err != nil {
						return err
					}
					return attachmentState.Replace(ctx, services.SessionAttachmentState{Status: "attached", SessionId: sessionId})
				},
				whenDetached: func(ctx chord.Context) error {
					if err := sessionNamespace.Rebind(false, ctx); err != nil {
						return err
					}
					return attachmentState.Replace(ctx, services.SessionAttachmentState{Status: "detached"})
				},
			}

			var finished atomic.Bool
			var requestRenderCount atomic.Int32
			fakeTerm := &tectFakeTerminal{}
			ui := tui.NewTuiMainScreen(fakeTerm, nil, nil)

			component, err := experimental.CreateClientTui(experimental.ClientTuiOptions{
				Command:  tc.command,
				UI:       ui,
				Terminal: fakeTerm,
				Servers: []experimental.ClientTuiServer{{
					ServerId: tectServerId,
					Radius:   true,
					Server:   serverSource,
					Session:  sessionSource,
				}},
				RequestRender: func() {
					requestRenderCount.Add(1)
				},
				Finish: func() {
					finished.Store(true)
				},
				LoadPresentationFacets: func(chord.JsonValue) ([]chord.FacetLoader, error) {
					return []chord.FacetLoader{
						chord.CreateStaticFacetLoader([]chord.Facet{
							chord.DefineFacet(chord.Facet{
								Id: "test-tui-facet",
								Setup: func(env chord.FacetEnvironment) error {
									env.Use(services.ModelsService)
									return nil
								},
							}),
						}),
					}, nil
				},
			})
			if err != nil {
				t.Fatalf("CreateClientTui: %v", err)
			}
			defer func() {
				_ = component.Dispose()
			}()

			mu.Lock()
			gotCreateCalls := createCalls
			gotPrepared := preparedReq
			mu.Unlock()
			if gotCreateCalls != tc.creates {
				t.Fatalf("create calls = %d, want %d", gotCreateCalls, tc.creates)
			}
			if gotPrepared == nil || gotPrepared.SessionId != tc.sessionId {
				t.Fatalf("preparedReq = %+v, want sessionId %q", gotPrepared, tc.sessionId)
			}
			var wantPkgPaths []string
			if len(tc.command.PluginPackages) > 0 {
				for _, pkgPath := range tc.command.PluginPackages {
					abs, err := filepath.Abs(pkgPath)
					if err != nil {
						t.Fatalf("Abs(%q): %v", pkgPath, err)
					}
					wantPkgPaths = append(wantPkgPaths, abs)
				}
			}
			if !slices.Equal(gotPrepared.PackagePaths, wantPkgPaths) {
				t.Fatalf("preparedReq.PackagePaths = %v, want %v", gotPrepared.PackagePaths, wantPkgPaths)
			}
			attVal, _ := attachmentState.Value()
			if attVal.Status != "attached" || attVal.SessionId != tc.sessionId {
				t.Fatalf("attachmentState = %+v, want attached/%s", attVal, tc.sessionId)
			}
			if len(selectedModels) != 0 {
				t.Fatalf("expected no model selection at startup, got %v", selectedModels)
			}
			rendered := tectRenderText(component)
			if !strings.Contains(rendered, fmt.Sprintf("Server: %s", tectServerId)) {
				t.Fatalf("expected Server header in render:\n%s", rendered)
			}
			if !strings.Contains(rendered, fmt.Sprintf("Session: %s", tc.sessionId)) {
				t.Fatalf("expected Session header in render:\n%s", rendered)
			}
			if !strings.Contains(rendered, "faux/faux-1") {
				t.Fatalf("expected model header in render:\n%s", rendered)
			}
			if strings.Contains(rendered, "Experimental Sessions") || strings.Contains(rendered, "Experimental Models") {
				t.Fatalf("did not expect selector headers in render:\n%s", rendered)
			}

			if tc.kind != "new" {
				return
			}

			component.HandleInput("hello")
			component.HandleInput("\r")
			tectWaitFor(t, "prompt recorded", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return slices.Equal(prompts, []string{"hello"})
			})
			tectWaitFor(t, "Working... indicator", func() bool {
				return strings.Contains(tectRenderText(component), "Working...")
			})
			releasePrompt()
			tectWaitFor(t, "remote answer rendered", func() bool {
				return strings.Contains(tectRenderText(component), "remote answer")
			})
			rendered = tectRenderText(component)
			if !strings.Contains(rendered, "hello") {
				t.Fatalf("expected user prompt in render:\n%s", rendered)
			}
			if strings.Contains(rendered, "Working...") {
				t.Fatalf("did not expect Working... after prompt completed:\n%s", rendered)
			}

			component.HandleInput("/reload")
			component.HandleInput("\x1b")
			component.HandleInput("\r")
			tectWaitFor(t, "/reload handlers invoked", func() bool {
				return reloadPresentationCount.Load() == 1 && reloadSessionCount.Load() == 1
			})
			tectWaitFor(t, "Reloaded plugins. status", func() bool {
				return strings.Contains(tectRenderText(component), "Reloaded plugins.")
			})

			_ = attachmentState.Replace(chord.BackgroundContext, services.SessionAttachmentState{Status: "detached"})
			_ = connectionState.Replace(chord.BackgroundContext, services.ServerConnectionState{
				Status:  "disconnected",
				Since:   tectPtr("later"),
				Reason:  tectPtr("network lost"),
				RetryAt: nil,
			})
			tectWaitFor(t, "retrying banner", func() bool {
				return strings.Contains(tectRenderText(component), "retrying")
			})
			component.HandleInput("\x03")
			if !finished.Load() {
				t.Fatalf("expected Ctrl+C during disconnect to finish")
			}
			finished.Store(false)
			component.HandleInput("\x04")
			if !finished.Load() {
				t.Fatalf("expected Ctrl+D during disconnect to finish")
			}
			finished.Store(false)

			_ = connectionState.Replace(chord.BackgroundContext, services.ServerConnectionState{
				Status:  "connecting",
				Attempt: tectPtr(1),
			})
			_ = connectionState.Replace(chord.BackgroundContext, services.ServerConnectionState{
				Status: "connected",
				Since:  tectPtr("reconnected"),
			})
			_ = attachmentState.Replace(chord.BackgroundContext, services.SessionAttachmentState{Status: "attached", SessionId: tc.sessionId})
			tectWaitFor(t, "Reattaching banner cleared", func() bool {
				return !strings.Contains(tectRenderText(component), "Reattaching")
			})

			component.HandleInput("/model")
			component.HandleInput("\x1b")
			component.HandleInput("\r")
			tectWaitFor(t, "Select model prompt", func() bool {
				return strings.Contains(tectRenderText(component), "Select model:")
			}, func() string { return tectRenderText(component) })
			component.HandleInput("\x1b[B")
			component.HandleInput("\r")
			tectWaitFor(t, "model two selected", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(selectedModels) == 1 && selectedModels[0] == (services.ModelRef{Provider: "test", ModelId: "two"})
			})
			tectWaitFor(t, "Select model closed", func() bool {
				return !strings.Contains(tectRenderText(component), "Select model:")
			})

			component.HandleInput("/thinking")
			component.HandleInput("\x1b")
			component.HandleInput("\r")
			tectWaitFor(t, "Select thinking level prompt", func() bool {
				return strings.Contains(tectRenderText(component), "Select thinking level:")
			})
			component.HandleInput("\x1b[B")
			component.HandleInput("\r")
			tectWaitFor(t, "thinking high selected", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(selectedThinkings) == 1 && selectedThinkings[0] == ai.ModelThinkingLevelHigh
			})

			component.HandleInput("\x03")
			tectWaitFor(t, "Ctrl+C finishes TUI", finished.Load)

			if err := component.Dispose(); err != nil {
				t.Fatalf("Dispose: %v", err)
			}
			rendersAfterClose := requestRenderCount.Load()
			mu.Lock()
			dirVal.Revision = 3
			dirVal.Sessions = nil
			nextDir := dirVal
			modelsVal.Refresh = services.ModelsRefreshState{Status: "refreshing"}
			nextModels := modelsVal
			mu.Unlock()
			_ = directoryState.Replace(chord.BackgroundContext, tectEncodeJson(t, nextDir))
			_ = attachmentState.Replace(chord.BackgroundContext, services.SessionAttachmentState{Status: "detached"})
			_ = modelsState.Replace(chord.BackgroundContext, tectEncodeJson(t, nextModels))
			time.Sleep(25 * time.Millisecond)
			if got := requestRenderCount.Load(); got != rendersAfterClose {
				t.Fatalf("requestRender count increased after Dispose: got %d, want %d", got, rendersAfterClose)
			}
		})
	}
}
