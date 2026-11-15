// Ported from packages/coding-agent/src/modes/interactive/interactive-mode.ts (pi v1.5.2).

package interactive

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// InteractiveModeOptions configures NewInteractiveMode and RunInteractiveMode.
type InteractiveModeOptions struct {
	MigratedProviders     []string
	StartupDiagnostics    []core.AgentSessionRuntimeDiagnostic
	ModelFallbackMessage  *string
	AutoTrustOnReloadCwd  *string
	InitialMessage        *string
	InitialImages         []ai.ImageContent
	InitialMessages       []string
	Verbose               bool
	TuiMode               *core.TuiMode
	InitialThemeSetting   *string
	Terminal              tui.Terminal
	Stdin                 io.Reader
	Stdout                io.Writer
	Stderr                io.Writer
	IsInteractiveTerminal func() bool
	DisableVersionCheck   bool
	DisableTmuxCheck      bool
}

type imWidgetEntry struct {
	Key       string
	Placement core.WidgetPlacement
	Lines     []string
	Factory   func(t tui.TUI, th *theme.Theme) tui.Component
}

type imCompactionQueuedMessage struct {
	Text string
	Mode string // "steer" or "followUp"
}

type imStartupNotice struct {
	Kind    string // "warning" | "error" | "status"
	Message string
}

type imKeyInputListenerEntry struct {
	ID uint64
	Fn core.TerminalInputHandler
}

type imEditor interface {
	tui.Component
	GetText() string
	SetText(text string)
	HandleInput(data string)
	AddToHistory(text string)
	InsertTextAtCursor(text string)
	GetExpandedText() string
	SetAutocompleteProvider(provider tui.AutocompleteProvider)
	SetPaddingX(padding int)
	SetAutocompleteMaxVisible(maxVisible int)
}

// InteractiveMode coordinates the terminal UI, editor, transcript, selectors,
// auth flows, and AgentSession events for an interactive Pi session.
type InteractiveMode struct {
	mu sync.Mutex

	runtime *core.AgentSessionRuntime
	options InteractiveModeOptions

	// Core TUI hierarchy & controllers
	ui                         tui.TUI
	uiMode                     core.TuiMode
	chatViewport               *ChatViewport
	headerContainer            *tui.Container
	builtInHeader              tui.Component
	customHeader               tui.Component
	chatContainer              *tui.Container
	pendingMessagesContainer   *tui.Container
	statusContainer            *tui.Container
	aboveEditorWidgetContainer *tui.Container
	editorContainer            *tui.Container
	belowEditorWidgetContainer *tui.Container
	defaultEditor              *components.CustomEditor
	editor                     imEditor
	footerDataProvider         *core.FooterDataProvider
	footer                     *components.FooterComponent
	customFooter               tui.Component
	keybindings                *core.KeybindingsManager
	themeController            *core.InteractiveThemeController
	programStatusReporter      *ProgramStatusReporter
	autocompleteProvider       tui.AutocompleteProvider
	fdPath                     *string

	// Lifecycle & background control
	isInitialized      bool
	initOnce           sync.Once
	initErr            error
	started            bool
	stopped            bool
	stopCh             chan struct{}
	bgCtx              context.Context
	bgCancel           context.CancelFunc
	versionCheckCancel context.CancelFunc
	lastTerminalStatus string

	// Active subscriptions & listeners
	unsubscribeSession      func()
	unsubscribeFooterBranch func()
	unsubscribeUIInput      func()
	extensionErrUnsub       func()
	nextTerminalListenerID  uint64
	terminalInputListeners  []imKeyInputListenerEntry

	// Streaming & transcript state
	streamingComponent          *components.AssistantMessageComponent
	streamingMessage            *ai.AssistantMessage
	pendingTools                map[string]*components.ToolExecutionComponent
	pendingToolsOrder           []string
	transcriptRenderedCount     int
	transcriptRenderGeneration  uint64
	lastThinkingLevel           string
	toolExpanded                bool
	hideThinkingBlock           bool
	hiddenThinkingLabel         *string
	hasShownAnthropicWarning    bool
	installChangeWarningShown   bool
	cacheMissNoticeCount        int
	startupVersionNoticeHandled bool

	// Working / status indicators & abort handlers
	loadingAnimation        *components.StatusIndicator
	retryIndicator          *components.RetryStatusIndicator
	retryEscapeHandler      func()
	compactionIndicator     *components.CompactionStatusIndicator
	compactionCancel        context.CancelFunc
	compactionEscapeHandler func()
	bashCancel              context.CancelFunc
	bashEscapeHandler       func()
	bashComponent           *components.BashExecutionComponent
	isBashMode              bool
	idleStatus              *components.IdleStatus
	idleStatusInserted      bool
	defaultWorkingMessage   string
	workingMessage          *string
	workingVisible          bool
	workingIndicator        *core.WorkingIndicatorOptions
	defaultWorkingColorFn   func(string) string
	pendingWorkingMessage   *string

	// Input / confirmation / queue state
	queuedSteering            []string
	queuedFollowUps           []string
	compactionQueuedMessages  []imCompactionQueuedMessage
	lastSigintTime            time.Time
	lastEscapeTime            time.Time
	doubleEscapeConfirmTimer  *time.Timer
	extensionWidgetsOrder     []string
	extensionWidgets          map[string]*imWidgetEntry
	extensionCommandsWraped   bool
	customEditorFactory       core.EditorFactory
	customAutocompleteFactory core.AutocompleteProviderFactory
	customOverlayCancel       func()
	overlayHandle             tui.OverlayHandle
	pendingSelectorMode       bool

	// Notice / prompt delivery synchronization
	headerRendered       bool
	queuedStartupNotices []imStartupNotice
	pendingInputCh       chan string
	pendingInputMu       sync.Mutex
	runNotifyCh          chan struct{}
	modelCatalogRefresh  bool
	isDisposing          bool
	footerProxy          *imcFooterProxy
	footerRenderMu       sync.Mutex
	sessionWriteDepth    int
	cachedSessionName    string
	bgWg                 sync.WaitGroup
}

// NewInteractiveMode constructs an InteractiveMode controller bound to runtime.
func NewInteractiveMode(runtime *core.AgentSessionRuntime, opts *InteractiveModeOptions) *InteractiveMode {
	var resolvedOpts InteractiveModeOptions
	if opts != nil {
		resolvedOpts = *opts
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())

	var cwd string
	if runtime != nil {
		cwd = runtime.Cwd()
	}

	m := &InteractiveMode{
		runtime:                    runtime,
		options:                    resolvedOpts,
		stopCh:                     make(chan struct{}),
		bgCtx:                      bgCtx,
		bgCancel:                   bgCancel,
		pendingTools:               make(map[string]*components.ToolExecutionComponent),
		extensionWidgets:           make(map[string]*imWidgetEntry),
		defaultWorkingMessage:      "Working...",
		workingVisible:             true,
		runNotifyCh:                make(chan struct{}, 1),
		headerContainer:            tui.NewContainer(),
		chatContainer:              tui.NewContainer(),
		pendingMessagesContainer:   tui.NewContainer(),
		statusContainer:            tui.NewContainer(),
		aboveEditorWidgetContainer: tui.NewContainer(),
		editorContainer:            tui.NewContainer(),
		belowEditorWidgetContainer: tui.NewContainer(),
		footerDataProvider:         core.NewFooterDataProvider(cwd),
		keybindings:                imLoadKeybindings(runtime),
	}
	if m.keybindings != nil && m.keybindings.KeybindingsManager != nil {
		tui.SetKeybindings(m.keybindings.KeybindingsManager)
	}
	m.imcSyncCachedSessionNameFrom(m.imSession())
	m.programStatusReporter = NewProgramStatusReporter(
		func() tui.Terminal {
			ui := m.imCurrentUI()
			if ui == nil {
				return nil
			}
			return ui.Terminal()
		},
		m.imcCachedSessionName,
	)
	return m
}

func imLoadKeybindings(runtime *core.AgentSessionRuntime) *core.KeybindingsManager {
	if runtime != nil && runtime.Services != nil && runtime.Services.AgentDir != "" {
		return core.KeybindingsManagerCreate(runtime.Services.AgentDir)
	}
	return core.KeybindingsManagerCreate()
}

// RunInteractiveMode initializes, runs, and cleans up interactive TUI mode.
func RunInteractiveMode(ctx context.Context, runtime *core.AgentSessionRuntime, opts *InteractiveModeOptions) error {
	mode := NewInteractiveMode(runtime, opts)
	return mode.Run(ctx)
}

func (m *InteractiveMode) imSession() *core.AgentSession {
	if m == nil || m.runtime == nil {
		return nil
	}
	return m.runtime.Session
}

func (m *InteractiveMode) imServices() *core.AgentSessionServices {
	if m == nil || m.runtime == nil {
		return nil
	}
	return m.runtime.Services
}

func (m *InteractiveMode) imSettingsManager() *core.SettingsManager {
	if svc := m.imServices(); svc != nil && svc.SettingsManager != nil {
		return svc.SettingsManager
	}
	if s := m.imSession(); s != nil {
		return s.SettingsManager
	}
	return nil
}

func (m *InteractiveMode) imSessionManager() *core.SessionManager {
	if s := m.imSession(); s != nil {
		return s.SessionManager
	}
	return nil
}

func (m *InteractiveMode) imModelRuntime() *core.ModelRuntime {
	if svc := m.imServices(); svc != nil && svc.ModelRuntime != nil {
		return svc.ModelRuntime
	}
	if s := m.imSession(); s != nil {
		return s.ModelRuntime()
	}
	return nil
}

func (m *InteractiveMode) imModelRegistry() *core.ModelRegistry {
	if svc := m.imServices(); svc != nil && svc.ModelRegistry != nil {
		return svc.ModelRegistry
	}
	return nil
}

func (m *InteractiveMode) imAuthStorage() *core.AuthStorage {
	if svc := m.imServices(); svc != nil && svc.AuthStorage != nil {
		return svc.AuthStorage
	}
	return nil
}

func (m *InteractiveMode) imResourceLoader() core.ResourceLoader {
	if svc := m.imServices(); svc != nil && svc.ResourceLoader != nil {
		return svc.ResourceLoader
	}
	if s := m.imSession(); s != nil {
		return s.ResourceLoader()
	}
	return nil
}

func (m *InteractiveMode) imExtensionRunner() *core.ExtensionRunner {
	if s := m.imSession(); s != nil {
		return s.ExtensionRunner()
	}
	return nil
}

func (m *InteractiveMode) imCwd() string {
	if m != nil && m.runtime != nil {
		if cwd := m.runtime.Cwd(); cwd != "" {
			return cwd
		}
	}
	if svc := m.imServices(); svc != nil && svc.Cwd != "" {
		return svc.Cwd
	}
	return ""
}

func (m *InteractiveMode) imAgentDir() string {
	if svc := m.imServices(); svc != nil && svc.AgentDir != "" {
		return svc.AgentDir
	}
	return ""
}

func (m *InteractiveMode) imCurrentUI() tui.TUI {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	ui := m.ui
	m.mu.Unlock()
	return ui
}

func (m *InteractiveMode) imIsStopped() bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	return stopped
}

// imPostUI marshals fn onto the TUI event loop asynchronously without holding any locks.
func (m *InteractiveMode) imPostUI(fn func()) {
	if m == nil || fn == nil {
		return
	}
	ui := m.imCurrentUI()
	if ui != nil {
		ui.Post(fn)
		return
	}
	fn()
}

// imCallUI executes fn synchronously on the TUI event loop.
// Callers MUST NOT hold m.mu or invoke this from inside a TUI input/render callback.
func (m *InteractiveMode) imCallUI(ctx context.Context, fn func()) bool {
	if m == nil || fn == nil {
		return false
	}
	ui := m.imCurrentUI()
	if ui == nil {
		fn()
		return true
	}
	done := make(chan struct{})
	ui.Post(func() {
		defer close(done)
		fn()
	})
	if ctx == nil {
		select {
		case <-done:
			return true
		case <-m.stopCh:
			return false
		}
	}
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	case <-m.stopCh:
		return false
	}
}

func (m *InteractiveMode) imRequestRender(force ...bool) {
	ui := m.imCurrentUI()
	if ui == nil {
		return
	}
	var forcePtr *bool
	if len(force) > 0 && force[0] {
		f := true
		forcePtr = &f
	}
	ui.RequestRender(forcePtr)
}

func (m *InteractiveMode) imOutputPad() int {
	sm := m.imSettingsManager()
	if sm == nil {
		return 0
	}
	return sm.GetOutputPad()
}

func (m *InteractiveMode) imHideThinkingBlock() bool {
	sm := m.imSettingsManager()
	if sm == nil {
		return false
	}
	return sm.GetHideThinkingBlock()
}

func (m *InteractiveMode) imShowImages() bool {
	sm := m.imSettingsManager()
	if sm == nil {
		return true
	}
	return sm.GetShowImages()
}

func (m *InteractiveMode) imImageWidthCells() int {
	sm := m.imSettingsManager()
	if sm == nil {
		return 40
	}
	return sm.GetImageWidthCells()
}

func (m *InteractiveMode) imUiMode() core.TuiMode {
	if m != nil && m.options.TuiMode != nil && *m.options.TuiMode != "" {
		return *m.options.TuiMode
	}
	if sm := m.imSettingsManager(); sm != nil {
		if mode := sm.GetTuiMode(); mode != "" {
			return mode
		}
	}
	return tui.TuiModeRegular
}

func (m *InteractiveMode) imSpawn(fn func()) {
	if m == nil || fn == nil {
		return
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.bgWg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.bgWg.Done()
		fn()
	}()
}

func (m *InteractiveMode) imDefaultWorkingLabel() string {
	m.mu.Lock()
	msg := m.workingMessage
	def := m.defaultWorkingMessage
	m.mu.Unlock()
	if msg != nil && strings.TrimSpace(*msg) != "" {
		return *msg
	}
	return def
}
