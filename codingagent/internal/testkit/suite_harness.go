// Ported from packages/coding-agent/test/suite/harness.ts (pi v1.0.0).

package testkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func shExtractTextFromContentList(blocks ai.ContentList) string {
	var parts []string
	for _, block := range blocks {
		if tc, ok := block.(*ai.TextContent); ok && tc != nil {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func shExtractTextFromUserContent(content ai.UserMessageContent) string {
	if content.Text != nil {
		return *content.Text
	}
	return shExtractTextFromContentList(content.Blocks)
}

// GetMessageText returns the concatenated text content of a session/agent message.
func GetMessageText(message any) string {
	switch m := message.(type) {
	case *ai.UserMessage:
		if m == nil {
			return ""
		}
		return shExtractTextFromUserContent(m.Content)
	case *ai.AssistantMessage:
		if m == nil {
			return ""
		}
		return shExtractTextFromContentList(m.Content)
	case *ai.ToolResultMessage:
		if m == nil {
			return ""
		}
		return shExtractTextFromContentList(m.Content)
	case *ai.SystemMessage:
		if m == nil {
			return ""
		}
		if m.Content.Text != nil {
			return *m.Content.Text
		}
		return shExtractTextFromContentList(m.Content.Blocks)
	case *core.CustomMessage:
		if m == nil {
			return ""
		}
		return shExtractTextFromUserContent(m.Content)
	case *core.CompactionSummaryMessage:
		if m == nil {
			return ""
		}
		return m.Summary
	case *core.BranchSummaryMessage:
		if m == nil {
			return ""
		}
		return m.Summary
	case *core.BashExecutionMessage:
		if m == nil {
			return ""
		}
		return m.Output
	default:
		return ""
	}
}

// GetUserTexts returns GetMessageText for every user-role message in the session transcript.
func GetUserTexts(h *SuiteHarness) []string {
	msgs := h.Session.Messages()
	var out []string
	for _, m := range msgs {
		if um, ok := m.(*ai.UserMessage); ok && um != nil {
			out = append(out, GetMessageText(um))
		}
	}
	return out
}

// GetAssistantTexts returns GetMessageText for every assistant-role message in the session transcript.
func GetAssistantTexts(h *SuiteHarness) []string {
	msgs := h.Session.Messages()
	var out []string
	for _, m := range msgs {
		if am, ok := m.(*ai.AssistantMessage); ok && am != nil {
			out = append(out, GetMessageText(am))
		}
	}
	return out
}

// GetToolResult returns the latest ToolResultMessage for toolName in the session transcript.
func GetToolResult(h *SuiteHarness, toolName string) (*ai.ToolResultMessage, error) {
	msgs := h.Session.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if tr, ok := msgs[i].(*ai.ToolResultMessage); ok && tr != nil && tr.ToolName == toolName {
			return tr, nil
		}
	}
	return nil, fmt.Errorf("No %s tool result", toolName)
}

// TestUiContextOverrides customizes CreateTestUiContext handlers over core.NoOpUIContext.
type TestUiContextOverrides struct {
	Select  func(ctx context.Context, title string, options []string, opts ...*core.ExtensionUIDialogOptions) (*string, error)
	Confirm func(ctx context.Context, title, message string, opts ...*core.ExtensionUIDialogOptions) (bool, error)
	Input   func(ctx context.Context, title string, placeholder *string, opts ...*core.ExtensionUIDialogOptions) (*string, error)
	Notify  func(message string, notifyType ...string)
}

type shTestUiContext struct {
	core.ExtensionUIContext
	overrides TestUiContextOverrides
}

func (u *shTestUiContext) Select(ctx context.Context, title string, options []string, opts ...*core.ExtensionUIDialogOptions) (*string, error) {
	if u.overrides.Select != nil {
		return u.overrides.Select(ctx, title, options, opts...)
	}
	return u.ExtensionUIContext.Select(ctx, title, options, opts...)
}

func (u *shTestUiContext) Confirm(ctx context.Context, title, message string, opts ...*core.ExtensionUIDialogOptions) (bool, error) {
	if u.overrides.Confirm != nil {
		return u.overrides.Confirm(ctx, title, message, opts...)
	}
	return u.ExtensionUIContext.Confirm(ctx, title, message, opts...)
}

func (u *shTestUiContext) Input(ctx context.Context, title string, placeholder *string, opts ...*core.ExtensionUIDialogOptions) (*string, error) {
	if u.overrides.Input != nil {
		return u.overrides.Input(ctx, title, placeholder, opts...)
	}
	return u.ExtensionUIContext.Input(ctx, title, placeholder, opts...)
}

func (u *shTestUiContext) Notify(message string, notifyType ...string) {
	if u.overrides.Notify != nil {
		u.overrides.Notify(message, notifyType...)
		return
	}
	u.ExtensionUIContext.Notify(message, notifyType...)
}

// CreateTestUiContext returns a no-op ExtensionUIContext with optional dialog/notify overrides.
func CreateTestUiContext(overrides ...*TestUiContextOverrides) core.ExtensionUIContext {
	base := core.NoOpUIContext()
	if len(overrides) == 0 || overrides[0] == nil {
		return base
	}
	return &shTestUiContext{
		ExtensionUIContext: base,
		overrides:          *overrides[0],
	}
}

// SuiteHarnessOptions configures CreateSuiteHarness.
type SuiteHarnessOptions struct {
	Cwd                    string
	Provider               *string
	Models                 []providers.FauxModelDefinition
	Settings               *core.Settings
	Tools                  []*agent.AgentTool
	InitialActiveToolNames []string
	AllowedToolNames       []string
	ExcludedToolNames      []string
	ResourceLoader         core.ResourceLoader
	ExtensionFactories     []core.InlineExtension
	WithConfiguredAuth     *bool
	ModelsJson             any
	SessionManager         *core.SessionManager
}

// SuiteHarness provides an in-memory AgentSession wired to compat.RegisterFauxProvider for suite tests.
type SuiteHarness struct {
	Session         *core.AgentSession
	SessionManager  *core.SessionManager
	SettingsManager *core.SettingsManager
	AuthStorage     *core.AuthStorage
	Faux            *providers.FauxProviderRegistration
	Models          []*ai.Model
	TempDir         string
	Cleanup         func()

	mu     sync.Mutex
	events []core.AgentSessionEvent
}

// GetModel returns the first faux model registered by this harness.
func (h *SuiteHarness) GetModel() *ai.Model {
	return h.Faux.GetModel()
}

// GetModelId returns the registered faux model matching modelId, or nil if absent.
func (h *SuiteHarness) GetModelId(modelId string) *ai.Model {
	return h.Faux.GetModelId(modelId)
}

// SetResponses replaces the queued faux responses.
func (h *SuiteHarness) SetResponses(responses []providers.FauxResponseStep) {
	h.Faux.SetResponses(responses)
}

// AppendResponses appends responses to the faux queue.
func (h *SuiteHarness) AppendResponses(responses []providers.FauxResponseStep) {
	h.Faux.AppendResponses(responses)
}

// GetPendingResponseCount returns the number of remaining scripted responses.
func (h *SuiteHarness) GetPendingResponseCount() int {
	return h.Faux.GetPendingResponseCount()
}

// Events returns a snapshot of all events emitted by the session in order.
func (h *SuiteHarness) Events() []core.AgentSessionEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]core.AgentSessionEvent, len(h.events))
	copy(out, h.events)
	return out
}

// EventsOfType filters captured events by AgentSessionEventType().
func (h *SuiteHarness) EventsOfType(eventType string) []core.AgentSessionEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []core.AgentSessionEvent
	for _, ev := range h.events {
		if ev != nil && ev.AgentSessionEventType() == eventType {
			out = append(out, ev)
		}
	}
	return out
}

// ResetEvents clears captured events without unsubscribing.
func (h *SuiteHarness) ResetEvents() {
	h.mu.Lock()
	h.events = nil
	h.mu.Unlock()
}

// CreateSuiteHarness constructs a suite harness registered with compat.RegisterFauxProvider.
func CreateSuiteHarness(ctx context.Context, options ...*SuiteHarnessOptions) (*SuiteHarness, error) {
	var opts SuiteHarnessOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	tempDir, err := os.MkdirTemp("", "pi-suite-*")
	if err != nil {
		return nil, err
	}
	fauxProvider := compat.RegisterFauxProvider(&providers.RegisterFauxProviderOptions{
		Provider: opts.Provider,
		Models:   opts.Models,
	})
	var modelRuntime *core.ModelRuntime
	cleanupResources := func() {
		if modelRuntime != nil {
			modelRuntime.Close()
		}
		if fauxProvider.Unregister != nil {
			fauxProvider.Unregister()
		}
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	}

	fauxProvider.SetResponses([]providers.FauxResponseStep{})
	model := fauxProvider.GetModel()
	var toolMap map[string]*agent.AgentTool
	if opts.Tools != nil {
		toolMap = make(map[string]*agent.AgentTool, len(opts.Tools))
		for _, t := range opts.Tools {
			if t != nil {
				toolMap[t.Name] = t
			}
		}
	}
	withConfiguredAuth := true
	if opts.WithConfiguredAuth != nil {
		withConfiguredAuth = *opts.WithConfiguredAuth
	}
	extensionRunnerRef := &core.ExtensionRunnerRef{}

	sessionManager := opts.SessionManager
	if sessionManager == nil {
		sessionManager = core.SessionManagerInMemory()
	}
	settingsManager := core.SettingsManagerInMemory(opts.Settings)

	authStorage := core.AuthStorageInMemory()
	fauxKey := "faux-key"
	if withConfiguredAuth {
		if _, err := authStorage.Modify(ctx, string(model.Provider), func(ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &fauxKey}, nil
		}, nil); err != nil {
			cleanupResources()
			return nil, err
		}
	}

	var modelsPathPtr *string
	if opts.ModelsJson != nil {
		modelsPath := filepath.Join(tempDir, "models.json")
		raw, err := jsonx.Marshal(opts.ModelsJson)
		if err != nil {
			cleanupResources()
			return nil, err
		}
		if err := os.WriteFile(modelsPath, raw, 0o600); err != nil {
			cleanupResources()
			return nil, err
		}
		modelsPathPtr = &modelsPath
	}

	var modelRegistry *core.ModelRegistry
	if modelsPathPtr != nil {
		modelRegistry, err = CreateModelRegistry(ctx, authStorage, modelsPathPtr)
	} else {
		modelRegistry, err = CreateInMemoryModelRegistry(ctx, authStorage)
	}
	if err != nil {
		cleanupResources()
		return nil, err
	}
	modelRuntime, err = GetModelRuntime(modelRegistry)
	if err != nil {
		cleanupResources()
		return nil, err
	}

	if withConfiguredAuth {
		providerModels := make([]core.ProviderModelConfig, 0, len(fauxProvider.Models))
		for _, m := range fauxProvider.Models {
			if m == nil {
				continue
			}
			apiCopy := m.Api
			baseUrlCopy := m.BaseUrl
			providerModels = append(providerModels, &core.ProviderChatModelConfig{
				ProviderModelConfigBase: core.ProviderModelConfigBase{
					Id:          m.Id,
					Name:        m.Name,
					Api:         &apiCopy,
					BaseUrl:     &baseUrlCopy,
					Input:       m.Input,
					InputLimits: m.InputLimits,
					Cost:        m.Cost,
				},
				Reasoning:     m.Reasoning,
				ContextWindow: m.ContextWindow,
				MaxTokens:     m.MaxTokens,
			})
		}
		apiStr := fauxProvider.Api
		baseUrl := model.BaseUrl
		if err := modelRegistry.RegisterProvider(string(model.Provider), core.ProviderConfigInput{
			BaseUrl: &baseUrl,
			ApiKey:  &fauxKey,
			Api:     &apiStr,
			Models:  providerModels,
		}); err != nil {
			cleanupResources()
			return nil, err
		}
	}

	ag := agent.NewAgent(&agent.AgentOptions{
		GetApiKey: func(context.Context, string) (string, error) {
			if withConfiguredAuth {
				return "faux-key", nil
			}
			return "", nil
		},
		StreamFn: core.DefaultStreamFn,
		InitialState: &agent.AgentInitialState{
			Model:        model,
			SystemPrompt: "",
			Tools:        []*agent.AgentTool{},
		},
		ConvertToLlm: func(_ context.Context, msgs []agent.AgentMessage) ([]ai.Message, error) {
			return core.ConvertToLlm(msgs), nil
		},
		TransformContext: func(tCtx context.Context, msgs []agent.AgentMessage) ([]agent.AgentMessage, error) {
			runner := extensionRunnerRef.Current
			if runner == nil {
				return msgs, nil
			}
			return runner.EmitContext(tCtx, msgs), nil
		},
	})

	sessionCwd := tempDir
	if opts.Cwd != "" {
		sessionCwd = opts.Cwd
	}
	resourceLoader := opts.ResourceLoader
	if resourceLoader == nil {
		if len(opts.ExtensionFactories) > 0 {
			extRes, extErr := CreateTestExtensionsResultWithFactories(ctx, sessionCwd, opts.ExtensionFactories...)
			if extErr != nil {
				cleanupResources()
				return nil, extErr
			}
			resourceLoader = CreateTestResourceLoader(&CreateTestResourceLoaderOptions{
				ExtensionsResult: &extRes,
			})
		} else {
			resourceLoader = CreateTestResourceLoader()
		}
	}
	if opts.SessionManager != nil {
		if existingContext := sessionManager.BuildSessionContext(); len(existingContext.Messages) > 0 {
			ag.ReplaceMessages(existingContext.Messages)
		}
	}
	session := core.NewAgentSession(core.AgentSessionConfig{
		Agent:                  ag,
		SessionManager:         sessionManager,
		SettingsManager:        settingsManager,
		Cwd:                    sessionCwd,
		ModelRuntime:           modelRuntime,
		ResourceLoader:         resourceLoader,
		BaseToolsOverride:      toolMap,
		InitialActiveToolNames: opts.InitialActiveToolNames,
		UsesDefaultTools:       opts.InitialActiveToolNames == nil,
		AllowedToolNames:       opts.AllowedToolNames,
		ExcludedToolNames:      opts.ExcludedToolNames,
		ExtensionRunnerRef:     extensionRunnerRef,
	})

	h := &SuiteHarness{
		Session:         session,
		SessionManager:  sessionManager,
		SettingsManager: settingsManager,
		AuthStorage:     authStorage,
		Faux:            fauxProvider,
		Models:          fauxProvider.Models,
		TempDir:         tempDir,
	}
	unsub := session.Subscribe(func(ev core.AgentSessionEvent) {
		h.mu.Lock()
		h.events = append(h.events, ev)
		h.mu.Unlock()
	})
	h.Cleanup = func() {
		if unsub != nil {
			unsub()
		}
		session.Dispose()
		cleanupResources()
	}

	return h, nil
}
