// Ported from packages/coding-agent/test/utilities.ts (pi v1.0.0).

package testkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ApiKey returns the API key configured in the test environment for authenticated Anthropic tests.
func ApiKey() string {
	if tok := os.Getenv("ANTHROPIC_OAUTH_TOKEN"); tok != "" {
		return tok
	}
	return os.Getenv("ANTHROPIC_API_KEY")
}

// PiAgentDir returns the path to the real user agent config directory (~/.pi/agent).
func PiAgentDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".pi", "agent")
	}
	return filepath.Join(home, ".pi", "agent")
}

func utilAuthPath() string {
	return filepath.Join(PiAgentDir(), "auth.json")
}

func utilLoadAuthStorage() map[string]ai.Credential {
	authPath := utilAuthPath()
	data, err := os.ReadFile(authPath)
	if err != nil {
		return map[string]ai.Credential{}
	}
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return map[string]ai.Credential{}
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return map[string]ai.Credential{}
	}
	out := make(map[string]ai.Credential, obj.Len())
	for k, v := range obj.All() {
		if cred, err := ai.DecodeCredential(v); err == nil && cred != nil {
			out[k] = cred
		}
	}
	return out
}

func utilSaveAuthStorage(storage map[string]ai.Credential) error {
	authPath := utilAuthPath()
	configDir := filepath.Dir(authPath)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	obj := jsonx.NewObject()
	for k, v := range storage {
		obj.Set(k, v)
	}
	raw, err := jsonx.MarshalIndent(obj, "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(authPath, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(authPath, 0o600)
}

// ResolveApiKey resolves an API key for provider from ~/.pi/agent/auth.json, refreshing expired
// OAuth credentials when necessary.
func ResolveApiKey(ctx context.Context, provider string) (string, error) {
	storage := utilLoadAuthStorage()
	entry, ok := storage[provider]
	if !ok || entry == nil {
		return "", nil
	}
	switch cred := entry.(type) {
	case *ai.ApiKeyCredential:
		if cred.Key == nil {
			return "", nil
		}
		return *cred.Key, nil
	case *ai.OAuthCredential:
		var oauthProvider *ai.OAuthAuth
		for _, candidate := range providers.BuiltinProviders() {
			if candidate != nil && candidate.Id == provider && candidate.Auth.OAuth != nil {
				oauthProvider = candidate.Auth.OAuth
				break
			}
		}
		if oauthProvider == nil {
			return "", nil
		}
		current := cred
		if oauthProvider.Refresh != nil && float64(time.Now().UnixMilli()) >= current.Expires {
			refreshed, err := oauthProvider.Refresh(ctx, current)
			if err != nil {
				return "", err
			}
			current = refreshed
			storage[provider] = current
			if err := utilSaveAuthStorage(storage); err != nil {
				return "", err
			}
		}
		if oauthProvider.ToAuth == nil {
			return current.Access, nil
		}
		modelAuth, err := oauthProvider.ToAuth(ctx, current)
		if err != nil {
			return "", err
		}
		if modelAuth.ApiKey == nil {
			return "", nil
		}
		return *modelAuth.ApiKey, nil
	default:
		return "", nil
	}
}

// HasAuthForProvider reports whether provider has stored credentials in ~/.pi/agent/auth.json.
func HasAuthForProvider(provider string) bool {
	storage := utilLoadAuthStorage()
	_, ok := storage[provider]
	return ok
}

// GetRealAuthStorage returns an *core.AuthStorage backed by ~/.pi/agent/auth.json.
func GetRealAuthStorage() *core.AuthStorage {
	return core.AuthStorageCreate(utilAuthPath())
}

// UserMsg builds a minimal user message for testing.
func UserMsg(text string) *ai.UserMessage {
	t := text
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: &t},
		Timestamp: time.Now().UnixMilli(),
	}
}

// AssistantMsg builds a minimal assistant message for testing.
func AssistantMsg(text string) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Role:     "assistant",
		Content:  ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
		Api:      ai.KnownApiAnthropicMessages,
		Provider: ai.KnownProviderAnthropic,
		Model:    "test",
		Usage: ai.Usage{
			Input:       1,
			Output:      1,
			TotalTokens: 2,
		},
		StopReason: ai.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// ReadSessionFileRoles reads a session JSONL file and returns one label per line: the message role
// for message entries, or the entry type otherwise.
func ReadSessionFileRoles(file string) ([]string, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return []string{}, nil
	}
	lines := strings.Split(trimmed, "\n")
	roles := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parsed, err := jsonx.Parse([]byte(line))
		if err != nil {
			return nil, err
		}
		obj, ok := parsed.(*jsonx.Object)
		if !ok || obj == nil {
			continue
		}
		if msgRaw, hasMsg := obj.Get("message"); hasMsg {
			if msgObj, ok := msgRaw.(*jsonx.Object); ok && msgObj != nil {
				if role, hasRole := msgObj.GetString("role"); hasRole && role != "" {
					roles = append(roles, role)
					continue
				}
			}
		}
		typ, _ := obj.GetString("type")
		roles = append(roles, typ)
	}
	return roles, nil
}

// TestSessionOptions configures CreateTestSession.
type TestSessionOptions struct {
	InMemory          bool
	SystemPrompt      *string
	SettingsOverrides *core.Settings
}

// TestSessionContext holds resources returned by CreateTestSession that require Cleanup.
type TestSessionContext struct {
	Session        *core.AgentSession
	SessionManager *core.SessionManager
	TempDir        string
	Cleanup        func()
}

// CreateTestExtensionsResult returns a barebone empty LoadExtensionsResult (0 extensions).
func CreateTestExtensionsResult() core.LoadExtensionsResult {
	return core.LoadExtensionsResult{
		Extensions: []core.Extension{},
		Errors:     []core.ExtensionLoadError{},
		Warnings:   []core.ExtensionLoadWarning{},
		Runtime:    core.CreateExtensionRuntime(),
	}
}

// CreateTestExtensionsResultWithFactories loads inline extension factories into a LoadExtensionsResult.
func CreateTestExtensionsResultWithFactories(ctx context.Context, cwd string, factories ...core.InlineExtension) (core.LoadExtensionsResult, error) {
	if len(factories) == 0 {
		return CreateTestExtensionsResult(), nil
	}
	res := core.LoadExtensionsWithOptions(ctx, nil, &core.LoadExtensionsOptions{
		Cwd:       cwd,
		Factories: factories,
	})
	if len(res.Errors) > 0 {
		return res, fmt.Errorf("extension load error (%s): %s", res.Errors[0].Path, res.Errors[0].Error)
	}
	return res, nil
}

// CreateTestResourceLoaderOptions configures CreateTestResourceLoader.
type CreateTestResourceLoaderOptions struct {
	ExtensionsResult   *core.LoadExtensionsResult
	Skills             []core.Skill
	Prompts            []core.PromptTemplate
	AgentsFiles        []core.ContextFile
	SystemPrompt       *string
	AppendSystemPrompt []string
}

type utilTestResourceLoader struct {
	extensionsResult          core.LoadExtensionsResult
	skills                    core.LoadSkillsResult
	prompts                   core.LoadPromptTemplatesResult
	themes                    core.LoadThemesResult
	agentsFiles               core.LoadContextFilesResult
	systemPrompt              *string
	systemPromptSource        *core.SystemPromptSource
	appendSystemPrompt        []string
	appendSystemPromptSources []core.SystemPromptSource
	pathMetadata              map[string]utils.PathMetadata
}

func (l *utilTestResourceLoader) Reload(context.Context, ...*core.ResourceLoaderReloadOptions) error {
	return nil
}

func (l *utilTestResourceLoader) GetExtensions() core.LoadExtensionsResult {
	return l.extensionsResult
}

func (l *utilTestResourceLoader) GetSkills() core.LoadSkillsResult {
	return l.skills
}

func (l *utilTestResourceLoader) GetPrompts() core.LoadPromptTemplatesResult {
	return l.prompts
}

func (l *utilTestResourceLoader) GetThemes() core.LoadThemesResult {
	return l.themes
}

func (l *utilTestResourceLoader) GetAgentsFiles() core.LoadContextFilesResult {
	return l.agentsFiles
}

func (l *utilTestResourceLoader) GetSystemPrompt() *string {
	return l.systemPrompt
}

func (l *utilTestResourceLoader) GetSystemPromptSource() *core.SystemPromptSource {
	return l.systemPromptSource
}

func (l *utilTestResourceLoader) GetAppendSystemPrompt() []string {
	return append([]string(nil), l.appendSystemPrompt...)
}

func (l *utilTestResourceLoader) GetAppendSystemPromptSources() []core.SystemPromptSource {
	return append([]core.SystemPromptSource(nil), l.appendSystemPromptSources...)
}

func (l *utilTestResourceLoader) ExtendResources(core.ResourceExtensionPaths) {}

func (l *utilTestResourceLoader) GetPathMetadata() map[string]utils.PathMetadata {
	return l.pathMetadata
}

// CreateTestResourceLoader returns an in-memory ResourceLoader stub for tests.
func CreateTestResourceLoader(options ...*CreateTestResourceLoaderOptions) core.ResourceLoader {
	var opts CreateTestResourceLoaderOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}
	extResult := CreateTestExtensionsResult()
	if opts.ExtensionsResult != nil {
		extResult = *opts.ExtensionsResult
	}
	skills := opts.Skills
	if skills == nil {
		skills = []core.Skill{}
	}
	prompts := opts.Prompts
	if prompts == nil {
		prompts = []core.PromptTemplate{}
	}
	agentsFiles := opts.AgentsFiles
	if agentsFiles == nil {
		agentsFiles = []core.ContextFile{}
	}
	appendSys := opts.AppendSystemPrompt
	if appendSys == nil {
		appendSys = []string{}
	}
	return &utilTestResourceLoader{
		extensionsResult: extResult,
		skills: core.LoadSkillsResult{
			Skills:      skills,
			Diagnostics: []core.ResourceDiagnostic{},
		},
		prompts: core.LoadPromptTemplatesResult{
			Templates:   prompts,
			Diagnostics: []core.ResourceDiagnostic{},
		},
		themes: core.LoadThemesResult{
			Diagnostics: []core.ResourceDiagnostic{},
		},
		agentsFiles: core.LoadContextFilesResult{
			AgentsFiles: agentsFiles,
		},
		systemPrompt:              opts.SystemPrompt,
		appendSystemPrompt:        appendSys,
		appendSystemPromptSources: []core.SystemPromptSource{},
		pathMetadata:              map[string]utils.PathMetadata{},
	}
}

// CreateTestSession creates an AgentSession wired for authenticated e2e testing with cleanup.
func CreateTestSession(ctx context.Context, options ...*TestSessionOptions) (*TestSessionContext, error) {
	var opts TestSessionOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}
	tempDir, err := os.MkdirTemp("", "pi-test-*")
	if err != nil {
		return nil, err
	}
	cleanupDir := func() {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	}

	model := providers.GetBuiltinModel(ai.KnownProviderAnthropic, "claude-sonnet-4-5")
	if model == nil {
		cleanupDir()
		return nil, errors.New("testkit.CreateTestSession: claude-sonnet-4-5 model not found")
	}
	systemPrompt := "You are a helpful assistant. Be extremely concise."
	if opts.SystemPrompt != nil {
		systemPrompt = *opts.SystemPrompt
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = tempDir
	}
	ag := agent.NewAgent(&agent.AgentOptions{
		GetApiKey: func(context.Context, string) (string, error) {
			return ApiKey(), nil
		},
		InitialState: &agent.AgentInitialState{
			Model:        model,
			SystemPrompt: systemPrompt,
			Tools:        core.CreateCodingTools(cwd),
		},
		StreamFn: func(streamCtx context.Context, m *ai.Model, c *ai.TranscriptContext, so *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			var messages []ai.Message
			if c != nil {
				messages = c.Messages
			}
			return compat.StreamSimple(streamCtx, m, &ai.Context{Messages: messages}, so), nil
		},
		ConvertToLlm: func(_ context.Context, msgs []agent.AgentMessage) ([]ai.Message, error) {
			return core.ConvertToLlm(msgs), nil
		},
	})

	var sessionManager *core.SessionManager
	if opts.InMemory {
		sessionManager = core.SessionManagerInMemory()
	} else {
		sessionManager, err = core.SessionManagerCreate(tempDir, "")
		if err != nil {
			cleanupDir()
			return nil, err
		}
	}
	settingsManager := core.SettingsManagerCreate(tempDir, tempDir)
	if opts.SettingsOverrides != nil {
		settingsManager.ApplyOverrides(opts.SettingsOverrides)
	}

	authStorage := core.AuthStorageCreate(filepath.Join(tempDir, "auth.json"))
	modelRegistry, err := CreateModelRegistry(ctx, authStorage, &tempDir)
	if err != nil {
		cleanupDir()
		return nil, err
	}
	modelRuntime, err := GetModelRuntime(modelRegistry)
	if err != nil {
		cleanupDir()
		return nil, err
	}

	session := core.NewAgentSession(core.AgentSessionConfig{
		Agent:           ag,
		SessionManager:  sessionManager,
		SettingsManager: settingsManager,
		Cwd:             tempDir,
		ModelRuntime:    modelRuntime,
		ResourceLoader:  CreateTestResourceLoader(),
	})

	// Subscribe to enable session persistence just like interactive callers.
	unsub := session.Subscribe(func(core.AgentSessionEvent) {})

	return &TestSessionContext{
		Session:        session,
		SessionManager: sessionManager,
		TempDir:        tempDir,
		Cleanup: func() {
			if unsub != nil {
				unsub()
			}
			session.Dispose()
			modelRuntime.Close()
			cleanupDir()
		},
	}, nil
}

// TestTreeMessage specifies one node to append when constructing a test tree.
type TestTreeMessage struct {
	Role       string
	Text       string
	BranchFrom string
}

// BuildTestTree populates session with messages and branches, returning a map from message text to entry ID.
func BuildTestTree(session *core.SessionManager, messages []TestTreeMessage) (map[string]string, error) {
	ids := make(map[string]string, len(messages))
	for _, msg := range messages {
		if msg.BranchFrom != "" {
			branchFromId, ok := ids[msg.BranchFrom]
			if !ok || branchFromId == "" {
				return nil, fmt.Errorf("Cannot branch from unknown entry: %s", msg.BranchFrom)
			}
			if err := session.Branch(branchFromId); err != nil {
				return nil, err
			}
		}
		var (
			id  string
			err error
		)
		if msg.Role == "user" {
			id, err = session.AppendMessage(UserMsg(msg.Text))
		} else {
			id, err = session.AppendMessage(AssistantMsg(msg.Text))
		}
		if err != nil {
			return nil, err
		}
		ids[msg.Text] = id
	}
	return ids, nil
}
