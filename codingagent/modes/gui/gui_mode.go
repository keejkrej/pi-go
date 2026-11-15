// Desktop GUI mode orchestrator mapping pi interactive coding-agent lifecycle,
// transcript cards, composer editor, status banners, footer, and keybindings onto
// github.com/keejkrej/pi-go/gui and github.com/egoist/mygo@v0.9.0.
package gui

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// GuiModeOptions configures desktop GUI mode startup, matching InteractiveModeOptions
// alongside MyGo host/window and headless testing hooks.
type GuiModeOptions struct {
	MigratedProviders    []string
	StartupDiagnostics   []core.AgentSessionRuntimeDiagnostic
	ModelFallbackMessage *string
	AutoTrustOnReloadCwd *string
	InitialMessage       *string
	InitialImages        []ai.ImageContent
	InitialMessages      []string
	Verbose              bool
	InitialThemeSetting  *string

	// Desktop/test host configuration.
	GUI        *gui.GUI
	Desktop    *gui.DesktopHooks
	Title      string
	Window     mygo.WindowOptions
	SkipWindow bool
	OnReady    func(mode *GuiMode)
}

type gmNoticeLevel string

const (
	gmNoticeInfo    gmNoticeLevel = "info"
	gmNoticeWarning gmNoticeLevel = "warning"
	gmNoticeError   gmNoticeLevel = "error"
)

type gmTranscriptCard interface {
	MessageCard
	SetTheme(th *gui.GuiTheme)
}

type gmTranscriptEntry struct {
	id   string
	kind string
	card gmTranscriptCard
}

type gmWidgetItem struct {
	key       string
	placement core.WidgetPlacement
	lines     []string
	render    func(c *ui.Context, th *gui.GuiTheme) ui.Element
}

// gmNoticeCard renders informational, warning, or error notifications inline in the transcript.
type gmNoticeCard struct {
	mu    sync.Mutex
	title string
	body  string
	level gmNoticeLevel
	theme *gui.GuiTheme
}

func gmNewNoticeCard(title, body string, level gmNoticeLevel, th *gui.GuiTheme) *gmNoticeCard {
	return &gmNoticeCard{
		title: title,
		body:  body,
		level: level,
		theme: th,
	}
}

func (n *gmNoticeCard) SetTheme(th *gui.GuiTheme) {
	n.mu.Lock()
	n.theme = th
	n.mu.Unlock()
}

func (n *gmNoticeCard) View(c *ui.Context) ui.Element {
	n.mu.Lock()
	title := n.title
	body := n.body
	level := n.level
	th := gui.ResolveGuiTheme(c, n.theme)
	n.mu.Unlock()

	borderCol := th.BorderMuted
	titleCol := th.Accent
	bgCol := th.Surface
	switch level {
	case gmNoticeWarning:
		borderCol = th.Warning
		titleCol = th.Warning
	case gmNoticeError:
		borderCol = th.Error
		titleCol = th.Error
		bgCol = th.UserMessageBg
	}

	card := ui.Column(c).
		FillWidth().
		Background(bgCol).
		Border(1, borderCol).
		Radius(th.Radius).
		Padding(8, 12).
		Gap(4)
	card.Children(func() {
		if title != "" {
			ui.Text(c, title).TextColor(titleCol).FontWeight(600).FontSize(12)
		}
		if body != "" {
			ui.Text(c, body).TextColor(th.Text).FontSize(12)
		}
	})
	return card
}

// gmExitConfirmWindow defines the double-press timeout for Ctrl+C exit confirmation.
const gmExitConfirmWindow = 2 * time.Second

// gmDoubleEscapeWindow defines the double-press timeout for Escape restore/fork actions.
const gmDoubleEscapeWindow = 600 * time.Millisecond

// GuiMode orchestrates the native MyGo desktop GUI frontend for a coding-agent session runtime.
type GuiMode struct {
	runtime *core.AgentSessionRuntime
	opts    GuiModeOptions

	host           *gui.GUI
	scrollView     *gui.ScrollView
	editor         *gui.Editor
	footer         *FooterBar
	footerProvider *core.FooterDataProvider
	statusBanner   *StatusBanner
	queueBanner    *QueueBanner
	keybindings    *gui.KeybindingsManager
	uiCtx          *gevExtensionUIContext

	mu                     sync.Mutex
	entries                []gmTranscriptEntry
	entryIndex             map[string]int
	streamingCard          *AssistantMessageCard
	streamingEntryID       string
	streamingTools         map[string]*ToolExecutionCard
	toolRenderers          map[string]ToolGuiRenderer
	customMessageRenderers map[string]CustomMessageRenderer
	bashCard               *BashExecutionCard
	bashEntryID            string
	bashCancel             context.CancelFunc
	bashRunning            bool
	pendingBashNotices     int

	toolsExpanded       bool
	thinkingExpanded    bool
	hideThinking        bool
	hiddenThinkingLabel string
	workingVisible      bool
	workingMessage      string
	workingFrames       []string
	compacting          bool
	retrying            bool

	steeringQueue []string
	followUpQueue []string
	widgets       []gmWidgetItem
	headerBanner  string
	footerBanner  string
	titleOverride string
	sessionName   string
	themeSet      *ThemeSet
	themeUnsub    func()
	sessionUnsub  func()
	activeDialog  *DialogHandle

	lastExitPress      time.Time
	lastEscPress       time.Time
	pendingInitHandled bool
	initialized        bool
	stopped            bool
	isRunning          bool
	autoStartedApp     bool
	runCtx             context.Context
	runCancel          context.CancelFunc
	doneOnce           sync.Once
	doneCh             chan struct{}
	nextSeq            atomic.Uint64

	terminalInputHandlers []core.TerminalInputHandler
	autocompleteFactories []core.AutocompleteProviderFactory
	editorFactory         core.EditorFactory
	shutdownHandlers      []func()

	// Overridable only inside unit tests to exercise Run lifecycle without native OS loop.
	appRunHook  func() error
	appQuitHook func()
}

// NewGuiMode constructs and initializes a GuiMode controller for runtime.
// Callers in headless tests may render View(c) directly or call Run(ctx) with SkipWindow: true.
func NewGuiMode(runtime *core.AgentSessionRuntime, opts *GuiModeOptions) *GuiMode {
	var cfg GuiModeOptions
	if opts != nil {
		cfg = *opts
	}

	host := cfg.GUI
	if host == nil {
		host = gui.New(gui.Options{
			Title:                gmInitialTitle(runtime, cfg.Title),
			IncludeStatusInTitle: true,
			Window:               cfg.Window,
			Desktop:              cfg.Desktop,
		})
	} else if cfg.Desktop != nil {
		host.SetDesktopHooks(cfg.Desktop)
	}

	km := gmLoadKeybindings()
	sm := gmRuntimeSettings(runtime)
	cwd := gmRuntimeCwd(runtime)

	ts := gmInitTheme(host, sm, cfg.InitialThemeSetting, !cfg.SkipWindow)
	var guiTheme *gui.GuiTheme
	if ts != nil {
		guiTheme = ts.Gui
	}

	m := &GuiMode{
		runtime:                runtime,
		opts:                   cfg,
		host:                   host,
		scrollView:             gui.NewScrollView(),
		footerProvider:         core.NewFooterDataProvider(cwd),
		statusBanner:           NewStatusBanner(),
		queueBanner:            NewQueueBanner(),
		keybindings:            km,
		entryIndex:             make(map[string]int),
		streamingTools:         make(map[string]*ToolExecutionCard),
		toolRenderers:          make(map[string]ToolGuiRenderer),
		customMessageRenderers: make(map[string]CustomMessageRenderer),
		workingVisible:         true,
		themeSet:               ts,
		doneCh:                 make(chan struct{}),
		appRunHook:             mygo.App.Run,
		appQuitHook:            mygo.App.Quit,
	}
	if sm != nil {
		m.hideThinking = sm.GetHideThinkingBlock()
	}

	m.editor = gui.NewEditor(gui.EditorOptions{
		Placeholder: "Message pi... (/ for commands, @ for files, ! for bash)",
		MinLines:    2,
		MaxLines:    8,
		Theme:       guiTheme,
		Keybindings: km,
		AutoFocus:   true,
	})
	if sm != nil {
		if mv := sm.GetAutocompleteMaxVisible(); mv > 0 {
			m.editor.AutocompletePopup().SetMaxVisible(mv)
		}
	}

	m.footer = NewFooterBar(
		WithFooterDataProvider(m.footerProvider),
		WithFooterTheme(guiTheme),
		WithFooterOnModelClick(func() { m.ShowModelSelector() }),
		WithFooterOnThinkingClick(func() { m.ShowThinkingSelector() }),
		WithFooterOnSessionClick(func() { m.ShowSessionSelector() }),
	)

	m.scrollView.SetFollowEnd(true)
	m.scrollView.SetSmoothFollow(true)
	m.scrollView.SetShowJumpButton(true)
	m.scrollView.SetTheme(guiTheme)
	m.statusBanner.SetTheme(guiTheme)
	m.queueBanner.SetTheme(guiTheme)

	m.statusBanner.SetOnAbort(func() { m.handleEscapeAbort() })
	m.queueBanner.SetOnClear(func() { m.restoreQueuedMessages() })

	m.editor.SetOnSubmit(func(ev gui.EditorSubmitEvent) { m.handleEditorSubmit(ev, false) })
	m.editor.SetOnFollowUp(func(ev gui.EditorSubmitEvent) { m.handleEditorSubmit(ev, true) })
	m.editor.SetOnCancel(func() { m.handleEscapeAbort() })

	m.uiCtx = gevNewExtensionUIContext(m)
	m.themeUnsub = BindThemeChanges(m.host, func(updated *ThemeSet) {
		m.applyThemeSet(updated)
	})
	m.host.SetRootView(func(c *ui.Context) {
		_ = m.View(c)
	})

	m.bindActiveSession(context.Background())
	m.RefreshAutocomplete()
	m.rebuildTranscript()
	m.updateFooterAndTitle()
	return m
}

// RunGuiMode constructs a GuiMode instance and runs its lifecycle until completion or ctx cancellation.
func RunGuiMode(ctx context.Context, runtime *core.AgentSessionRuntime, opts *GuiModeOptions) error {
	mode := NewGuiMode(runtime, opts)
	return mode.Run(ctx)
}

// GUI returns the underlying Layer-L0 *gui.GUI host instance.
func (m *GuiMode) GUI() *gui.GUI {
	if m == nil {
		return nil
	}
	return m.host
}

// Runtime returns the active *core.AgentSessionRuntime bound to this mode.
func (m *GuiMode) Runtime() *core.AgentSessionRuntime {
	if m == nil {
		return nil
	}
	return m.runtime
}

// Session returns the currently active *core.AgentSession on Runtime, or nil.
func (m *GuiMode) Session() *core.AgentSession {
	if m == nil || m.runtime == nil {
		return nil
	}
	return m.runtime.Session
}

// FlushPending executes any queued UI callbacks immediately. Primarily useful in synchronous tests.
func (m *GuiMode) FlushPending() int {
	if m == nil || m.host == nil {
		return 0
	}
	return m.host.FlushPending()
}

// RegisterToolRenderer registers a custom native MyGo GUI renderer for toolName.
func (m *GuiMode) RegisterToolRenderer(toolName string, renderer ToolGuiRenderer) {
	if m == nil || toolName == "" {
		return
	}
	m.mu.Lock()
	if renderer == nil {
		delete(m.toolRenderers, toolName)
	} else {
		m.toolRenderers[toolName] = renderer
	}
	m.mu.Unlock()
}

// RegisterCustomMessageRenderer registers a custom native MyGo GUI renderer for extension customType messages.
func (m *GuiMode) RegisterCustomMessageRenderer(customType string, renderer CustomMessageRenderer) {
	if m == nil || customType == "" {
		return
	}
	m.mu.Lock()
	if renderer == nil {
		delete(m.customMessageRenderers, customType)
	} else {
		m.customMessageRenderers[customType] = renderer
	}
	m.mu.Unlock()
}

// OnShutdown registers an additional cleanup callback executed when Stop runs.
func (m *GuiMode) OnShutdown(fn func()) {
	if m == nil || fn == nil {
		return
	}
	m.mu.Lock()
	m.shutdownHandlers = append(m.shutdownHandlers, fn)
	m.mu.Unlock()
}

// Run starts the GUI mode lifecycle (creating the MyGo window unless SkipWindow is true),
// applies startup diagnostics/initial messages, and blocks until the window closes or ctx cancels.
func (m *GuiMode) Run(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runCtx, runCancel := context.WithCancel(ctx)
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		runCancel()
		return nil
	}
	m.isRunning = true
	m.runCtx = runCtx
	m.runCancel = runCancel
	skipWindow := m.opts.SkipWindow
	m.mu.Unlock()

	defer func() {
		m.Stop()
		runCancel()
	}()

	// Ensure context cancellation tears down Wait/App cleanly.
	go func() {
		select {
		case <-runCtx.Done():
			m.Stop()
		case <-m.doneCh:
		}
	}()

	m.bindActiveSession(runCtx)
	m.handleStartupState(runCtx)

	if skipWindow {
		m.notifyReady(runCtx)
		<-m.doneCh
		if err := runCtx.Err(); err != nil && err != context.Canceled {
			return err
		}
		return nil
	}

	m.mu.Lock()
	winOpts := m.opts.Window
	if winOpts.Title == "" {
		winOpts.Title = m.host.EffectiveTitle()
	}
	if winOpts.Width == 0 && winOpts.Height == 0 {
		winOpts.Width = 1120
		winOpts.Height = 780
	}
	m.autoStartedApp = true
	m.mu.Unlock()

	mygo.App.WhenReady(func() {
		win := m.host.Start(winOpts)
		if win != nil {
			win.OnClosed(func() {
				m.Stop()
			})
		}
		if m.opts.InitialThemeSetting != nil {
			resolved := ResolveThemeForSetting(*m.opts.InitialThemeSetting, mygo.Theme.IsDark())
			if resolved != "" {
				_, ts := ApplyThemeToHost(m.host, resolved)
				if ts != nil {
					m.applyThemeSet(ts)
				}
			}
		}
		m.notifyReady(runCtx)
	})

	runErr := m.appRunHook()
	m.Stop()
	return runErr
}

// Stop tears down event subscriptions, dialogs, window state, and unblocks Run cleanly.
func (m *GuiMode) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	m.isRunning = false
	runCancel := m.runCancel
	bashCancel := m.bashCancel
	sessUnsub := m.sessionUnsub
	m.sessionUnsub = nil
	themeUnsub := m.themeUnsub
	m.themeUnsub = nil
	dlg := m.activeDialog
	m.activeDialog = nil
	fp := m.footerProvider
	ft := m.footer
	autoApp := m.autoStartedApp
	quitHook := m.appQuitHook
	extraShutdown := append([]func(){}, m.shutdownHandlers...)
	m.shutdownHandlers = nil
	m.mu.Unlock()

	if runCancel != nil {
		runCancel()
	}
	if bashCancel != nil {
		bashCancel()
	}
	if dlg != nil {
		dlg.Close()
	}
	if sessUnsub != nil {
		sessUnsub()
	}
	if themeUnsub != nil {
		themeUnsub()
	}
	if ft != nil {
		ft.Dispose()
	}
	if fp != nil {
		fp.Dispose()
	}
	for _, fn := range extraShutdown {
		if fn != nil {
			fn()
		}
	}
	if m.host != nil {
		m.host.Stop()
	}
	m.doneOnce.Do(func() {
		close(m.doneCh)
	})
	if autoApp && quitHook != nil {
		quitHook()
	}
}

// View builds the root native MyGo GUI element tree for the current frame.
// Callers may render View(c) directly in ui.NewTester or allow GUI.Start to drive it.
func (m *GuiMode) View(c *ui.Context) ui.Element {
	if m == nil {
		return ui.Box(c).Fill()
	}

	m.mu.Lock()
	ts := m.themeSet
	entries := append([]gmTranscriptEntry(nil), m.entries...)
	widgets := append([]gmWidgetItem(nil), m.widgets...)
	hdrBanner := m.headerBanner
	ftrBanner := m.footerBanner
	m.mu.Unlock()

	var themeOverride *gui.GuiTheme
	if ts != nil {
		themeOverride = ts.Gui
	}
	th := gui.ResolveGuiTheme(c, themeOverride)

	if m.host == nil || !m.host.HasOverlay() {
		m.handleAppShortcuts(c)
	}

	root := ui.Column(c).
		Fill().
		Background(th.Background).
		Padding(10, 14).
		Gap(8)

	root.Children(func() {
		if hdrBanner != "" {
			ui.Row(c).
				FillWidth().
				Background(th.Surface).
				Border(1, th.BorderMuted).
				Radius(th.Radius).
				Padding(6, 10).
				Children(func() {
					ui.Text(c, hdrBanner).TextColor(th.Muted).FontSize(12)
				})
		}

		// Scrollable transcript takes all remaining vertical space.
		ui.Box(c).FillWidth().Grow(1).Children(func() {
			m.scrollView.View(c, len(entries), func(rc *ui.Context, idx int) {
				if idx < 0 || idx >= len(entries) {
					return
				}
				entry := entries[idx]
				ui.Box(rc).Key(entry.id).FillWidth().Padding(0, 0, 8, 0).Children(func() {
					if entry.card != nil {
						entry.card.View(rc)
					}
				})
			})
		})

		m.statusBanner.View(c)
		m.queueBanner.View(c)
		gmRenderWidgets(c, th, widgets, core.WidgetPlacementAboveEditor)

		if m.editor != nil {
			m.editor.View(c)
		}

		gmRenderWidgets(c, th, widgets, core.WidgetPlacementBelowEditor)

		if ftrBanner != "" {
			ui.Row(c).
				FillWidth().
				Background(th.Surface).
				Border(1, th.BorderMuted).
				Radius(th.Radius).
				Padding(4, 10).
				Children(func() {
					ui.Text(c, ftrBanner).TextColor(th.Muted).FontSize(11)
				})
		} else if m.footer != nil {
			m.footer.View(c)
		}
	})

	return root
}

// ShowInfo appends an informational notice card to the transcript.
func (m *GuiMode) ShowInfo(title, message string) {
	m.appendNotice(gmNoticeInfo, title, message)
}

// ShowWarning appends a warning notice card to the transcript.
func (m *GuiMode) ShowWarning(title, message string) {
	m.appendNotice(gmNoticeWarning, title, message)
}

// ShowError appends an error notice card to the transcript and surfaces it in StatusBanner.
func (m *GuiMode) ShowError(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	m.appendNotice(gmNoticeError, "Error", message)
	if m.statusBanner != nil {
		m.statusBanner.SetCustomNotice(message, true)
	}
	if m.host != nil {
		m.host.RequestRender()
	}
}

func (m *GuiMode) nextEntryID(prefix string) string {
	if m == nil {
		return fmt.Sprintf("%s:%d", prefix, time.Now().UnixNano())
	}
	seq := m.nextSeq.Add(1)
	return fmt.Sprintf("%s:%d", prefix, seq)
}

func (m *GuiMode) appendNotice(level gmNoticeLevel, title, body string) {
	th := m.currentGuiTheme()
	card := gmNewNoticeCard(title, body, level, th)
	m.appendCard("notice", m.nextEntryID("notice"), card)
}

func (m *GuiMode) appendCard(kind, id string, card gmTranscriptCard) {
	if card == nil {
		return
	}
	if id == "" {
		id = m.nextEntryID(kind)
	}
	m.mu.Lock()
	if existingIdx, exists := m.entryIndex[id]; exists && existingIdx >= 0 && existingIdx < len(m.entries) {
		m.entries[existingIdx].card = card
		m.mu.Unlock()
		if m.host != nil {
			m.host.RequestRender()
		}
		return
	}
	idx := len(m.entries)
	m.entries = append(m.entries, gmTranscriptEntry{
		id:   id,
		kind: kind,
		card: card,
	})
	m.entryIndex[id] = idx
	m.mu.Unlock()
	if m.scrollView != nil && !m.scrollView.Locked() {
		m.scrollView.JumpToBottom()
	}
	if m.host != nil {
		m.host.RequestRender()
	}
}

func (m *GuiMode) postUI(fn func()) {
	if fn == nil || m == nil {
		return
	}
	if m.host != nil {
		m.host.Post(fn)
		m.host.RequestRender()
		return
	}
	fn()
}

func (m *GuiMode) currentGuiTheme() *gui.GuiTheme {
	m.mu.Lock()
	ts := m.themeSet
	m.mu.Unlock()
	if ts != nil && ts.Gui != nil {
		return ts.Gui
	}
	if m.host != nil {
		return m.host.Theme()
	}
	return ResolveActiveGuiTheme()
}

func (m *GuiMode) applyThemeSet(ts *ThemeSet) {
	if ts == nil {
		return
	}
	guiTh := ts.Gui
	m.mu.Lock()
	m.themeSet = ts
	entries := append([]gmTranscriptEntry(nil), m.entries...)
	dlg := m.activeDialog
	m.mu.Unlock()

	for _, e := range entries {
		if e.card != nil {
			e.card.SetTheme(guiTh)
		}
	}
	if m.editor != nil {
		m.editor.SetTheme(guiTh)
	}
	if m.scrollView != nil {
		m.scrollView.SetTheme(guiTh)
	}
	if m.footer != nil {
		m.footer.SetTheme(guiTh)
	}
	if m.statusBanner != nil {
		m.statusBanner.SetTheme(guiTh)
	}
	if m.queueBanner != nil {
		m.queueBanner.SetTheme(guiTh)
	}
	_ = dlg
	if m.host != nil {
		m.host.RequestRender()
	}
}

func (m *GuiMode) notifyReady(ctx context.Context) {
	m.mu.Lock()
	handled := m.pendingInitHandled
	m.pendingInitHandled = true
	onReady := m.opts.OnReady
	m.mu.Unlock()

	if onReady != nil {
		onReady(m)
	}
	if !handled {
		go m.processInitialInputs(ctx)
	}
}

func (m *GuiMode) handleStartupState(ctx context.Context) {
	m.mu.Lock()
	migrated := append([]string(nil), m.opts.MigratedProviders...)
	diags := append([]core.AgentSessionRuntimeDiagnostic(nil), m.opts.StartupDiagnostics...)
	fallbackMsg := m.opts.ModelFallbackMessage
	autoTrustCwd := m.opts.AutoTrustOnReloadCwd
	m.mu.Unlock()

	if len(migrated) > 0 {
		m.ShowInfo("Auth Migrated", fmt.Sprintf("Migrated credentials for: %s", strings.Join(migrated, ", ")))
	}
	for _, d := range diags {
		if strings.TrimSpace(d.Message) != "" {
			if d.Type == "error" {
				m.ShowError(d.Message)
			} else {
				m.ShowWarning("Startup Diagnostic", d.Message)
			}
		}
	}
	if fallbackMsg != nil && strings.TrimSpace(*fallbackMsg) != "" {
		m.ShowWarning("Model Fallback", *fallbackMsg)
	}
	if autoTrustCwd != nil && strings.TrimSpace(*autoTrustCwd) != "" {
		m.checkProjectTrustOnStartup(ctx, *autoTrustCwd)
	}
	m.checkNewChangelog()
}

func (m *GuiMode) checkNewChangelog() {
	sess := m.Session()
	if sess == nil || sess.SettingsManager == nil {
		return
	}
	sm := sess.SettingsManager
	if sm.GetCollapseChangelog() {
		return
	}
	lastVer := sm.GetLastChangelogVersion()
	if lastVer == nil {
		sm.SetLastChangelogVersion(utils.VERSION)
		return
	}
	if *lastVer == utils.VERSION {
		return
	}
	entries, err := utils.ParseChangelog(utils.GetChangelogPath())
	if err != nil {
		sm.SetLastChangelogVersion(utils.VERSION)
		return
	}
	newer, _ := utils.GetNewEntries(entries, *lastVer)
	if len(newer) > 0 {
		var sb strings.Builder
		for _, entry := range newer {
			sb.WriteString(utils.NormalizeChangelogLinksForEntry(entry.Content, entry))
			sb.WriteString("\n\n")
		}
		m.ShowInfo("Updated to v"+utils.VERSION, strings.TrimSpace(sb.String()))
	}
	sm.SetLastChangelogVersion(utils.VERSION)
}

func (m *GuiMode) checkProjectTrustOnStartup(ctx context.Context, cwd string) {
	sess := m.Session()
	if sess == nil || sess.SettingsManager == nil || sess.SettingsManager.IsProjectTrusted() {
		return
	}
	if !core.HasTrustRequiringProjectResources(cwd) {
		return
	}
	options := core.GetProjectTrustOptions(cwd)
	if len(options) == 0 {
		return
	}
	m.ShowTrustSelector(ctx, cwd, options)
}

func (m *GuiMode) processInitialInputs(ctx context.Context) {
	m.mu.Lock()
	initMsg := m.opts.InitialMessage
	initImgs := append([]ai.ImageContent(nil), m.opts.InitialImages...)
	initMore := append([]string(nil), m.opts.InitialMessages...)
	m.mu.Unlock()

	if initMsg != nil && (strings.TrimSpace(*initMsg) != "" || len(initImgs) > 0) {
		var imgPtrs []*ai.ImageContent
		for i := range initImgs {
			cp := initImgs[i]
			imgPtrs = append(imgPtrs, &cp)
		}
		m.dispatchTextOrPrompt(ctx, *initMsg, imgPtrs, false)
	}
	for _, msg := range initMore {
		if ctx.Err() != nil {
			return
		}
		if strings.TrimSpace(msg) == "" {
			continue
		}
		m.dispatchTextOrPrompt(ctx, msg, nil, false)
	}
}

func (m *GuiMode) bindActiveSession(ctx context.Context) {
	sess := m.Session()
	if sess == nil {
		return
	}

	sessName := ""
	if n := sess.SessionName(); n != nil {
		sessName = strings.TrimSpace(*n)
	}

	m.mu.Lock()
	prevUnsub := m.sessionUnsub
	m.sessionUnsub = nil
	m.sessionName = sessName
	if m.initialized && prevUnsub != nil {
		// Still rebind on session replacement; detach previous first.
	}
	m.initialized = true
	m.mu.Unlock()

	if prevUnsub != nil {
		prevUnsub()
	}

	unsub := gevSubscribeSession(m, sess)
	m.mu.Lock()
	m.sessionUnsub = unsub
	m.mu.Unlock()

	if m.footerProvider != nil {
		m.footerProvider.SetCwd(sess.Cwd())
		if reg := sess.ModelRegistry(); reg != nil {
			if avail := reg.GetAvailable(); len(avail) >= 0 {
				m.footerProvider.SetAvailableProviderCount(len(gmUniqueProviders(avail)))
			}
		}
	}

	_ = sess.BindExtensions(ctx, core.ExtensionBindings{
		UIContext: m.uiCtx,
		Mode:      core.ExtensionModeTui,
		CommandContextActions: &core.ExtensionCommandContextActions{
			WaitForIdle: func(wctx context.Context) error {
				return sess.WaitForIdle(wctx)
			},
			NewSession: func(nctx context.Context, options ...*core.ExtensionNewSessionOptions) (core.CancelledResult, error) {
				if m.runtime == nil {
					return core.CancelledResult{Cancelled: true}, nil
				}
				var rOpts *core.NewSessionRuntimeOptions
				if len(options) > 0 && options[0] != nil {
					extOpt := options[0]
					rOpts = &core.NewSessionRuntimeOptions{
						ParentSession: extOpt.ParentSession,
					}
					if extOpt.Setup != nil {
						setupFn := extOpt.Setup
						rOpts.Setup = func(sm *core.SessionManager) error {
							return setupFn(nctx, sm)
						}
					}
				}
				res, err := m.runtime.NewSession(nctx, rOpts)
				if err != nil {
					return core.CancelledResult{Cancelled: true}, err
				}
				if (res == nil || !res.Cancelled) && len(options) > 0 && options[0] != nil && options[0].WithSession != nil {
					if active := m.Session(); active != nil {
						_ = options[0].WithSession(nctx, active.CreateReplacedSessionContext())
					}
				}
				m.postUI(func() {
					m.handleSessionReplaced()
				})
				return core.CancelledResult{Cancelled: res != nil && res.Cancelled}, nil
			},
			Fork: func(fctx context.Context, entryId string, options ...*core.ExtensionForkOptions) (core.CancelledResult, error) {
				if m.runtime == nil {
					return core.CancelledResult{Cancelled: true}, nil
				}
				var forkOpts *core.ForkSessionOptions
				if len(options) > 0 && options[0] != nil && options[0].Position != "" {
					forkOpts = &core.ForkSessionOptions{Position: options[0].Position}
				}
				res, err := m.runtime.Fork(fctx, entryId, forkOpts)
				if err != nil {
					return core.CancelledResult{Cancelled: true}, err
				}
				if (res == nil || !res.Cancelled) && len(options) > 0 && options[0] != nil && options[0].WithSession != nil {
					if active := m.Session(); active != nil {
						_ = options[0].WithSession(fctx, active.CreateReplacedSessionContext())
					}
				}
				m.postUI(func() {
					m.handleSessionReplaced()
				})
				return core.CancelledResult{Cancelled: res != nil && res.Cancelled}, nil
			},
			NavigateTree: func(tctx context.Context, targetId string, options ...*core.ExtensionNavigateTreeOptions) (core.CancelledResult, error) {
				var navOpts *core.NavigateTreeOptions
				if len(options) > 0 && options[0] != nil {
					navOpts = &core.NavigateTreeOptions{
						Summarize:           options[0].Summarize,
						CustomInstructions:  options[0].CustomInstructions,
						ReplaceInstructions: options[0].ReplaceInstructions,
						Label:               options[0].Label,
					}
				}
				res, err := sess.NavigateTree(tctx, targetId, navOpts)
				if err != nil {
					return core.CancelledResult{Cancelled: true}, err
				}
				m.postUI(func() {
					m.rebuildTranscript()
					if res != nil && res.EditorText != nil && m.editor != nil {
						m.editor.SetText(*res.EditorText)
					}
					m.updateFooterAndTitle()
				})
				return core.CancelledResult{Cancelled: res != nil && res.Cancelled}, nil
			},
			SwitchSession: func(sctx context.Context, sessionPath string, options ...*core.ExtensionSwitchSessionOptions) (core.CancelledResult, error) {
				if m.runtime == nil {
					return core.CancelledResult{Cancelled: true}, nil
				}
				res, err := m.runtime.SwitchSession(sctx, sessionPath)
				if err != nil {
					return core.CancelledResult{Cancelled: true}, err
				}
				if (res == nil || !res.Cancelled) && len(options) > 0 && options[0] != nil && options[0].WithSession != nil {
					if active := m.Session(); active != nil {
						_ = options[0].WithSession(sctx, active.CreateReplacedSessionContext())
					}
				}
				m.postUI(func() {
					m.handleSessionReplaced()
				})
				return core.CancelledResult{Cancelled: res != nil && res.Cancelled}, nil
			},
			Reload: func(rctx context.Context) error {
				err := sess.Reload(rctx)
				if err == nil {
					m.postUI(func() {
						m.RefreshAutocomplete()
						m.updateFooterAndTitle()
					})
				}
				return err
			},
		},
		AbortHandler: func() {
			m.handleEscapeAbort()
		},
		ShutdownHandler: func() {
			m.Stop()
		},
		OnError: func(extErr core.ExtensionError) {
			m.postUI(func() {
				m.ShowError(fmt.Sprintf("Extension error (%s): %s", extErr.ExtensionPath, extErr.Error))
			})
		},
	})
}

func (m *GuiMode) handleSessionReplaced() {
	ctx := m.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	m.bindActiveSession(ctx)
	m.RefreshAutocomplete()
	m.rebuildTranscript()
	m.updateFooterAndTitle()
}

func (m *GuiMode) rebuildTranscript() {
	th := m.currentGuiTheme()
	sess := m.Session()
	entries := make([]gmTranscriptEntry, 0, 16)
	entryIdx := make(map[string]int)

	if hdr := m.buildHeaderAnnouncement(sess, th); hdr != nil {
		entryIdx["header"] = len(entries)
		entries = append(entries, gmTranscriptEntry{
			id:   "header",
			kind: "header",
			card: hdr,
		})
	}

	if sess != nil && sess.SessionManager != nil {
		cwd := sess.Cwd()
		sm := sess.SettingsManager
		showImages := sm == nil || sm.GetShowImages()

		m.mu.Lock()
		hideThinking := m.hideThinking
		thinkingExpanded := m.thinkingExpanded
		hiddenLabel := m.hiddenThinkingLabel
		toolsExpanded := m.toolsExpanded
		customMsgRenderers := make(map[string]CustomMessageRenderer, len(m.customMessageRenderers))
		for k, v := range m.customMessageRenderers {
			customMsgRenderers[k] = v
		}
		toolRenderers := make(map[string]ToolGuiRenderer, len(m.toolRenderers))
		for k, v := range m.toolRenderers {
			toolRenderers[k] = v
		}
		m.mu.Unlock()

		ctxEntries := sess.SessionManager.BuildContextEntries()
		toolCards := make(map[string]*ToolExecutionCard)

		for i, se := range ctxEntries {
			switch e := se.(type) {
			case *core.SessionMessageEntry:
				if e == nil || e.Message == nil {
					continue
				}
				switch msg := e.Message.(type) {
				case *ai.UserMessage:
					txt, imgs := gevExtractUserContent(msg.Content)
					if sb := core.ParseSkillBlock(txt); sb != nil {
						skID := fmt.Sprintf("skill:%s:%d", e.Id, i)
						skCard := NewSkillInvocationCard(*sb,
							WithCardTheme(th),
							WithCardExpanded(toolsExpanded),
							WithCardOnLink(gmOpenLink),
						)
						entryIdx[skID] = len(entries)
						entries = append(entries, gmTranscriptEntry{id: skID, kind: "skill", card: skCard})
						if sb.UserMessage != nil && strings.TrimSpace(*sb.UserMessage) != "" {
							uid := fmt.Sprintf("user:%s:%d", e.Id, i)
							uc := NewUserMessageCard(*sb.UserMessage,
								WithUserImages(imgs),
								WithUserTimestamp(msg.Timestamp),
								WithUserMessageId(e.Id),
								WithUserTheme(th),
								WithUserOnLink(gmOpenLink),
							)
							entryIdx[uid] = len(entries)
							entries = append(entries, gmTranscriptEntry{id: uid, kind: "user", card: uc})
						}
					} else {
						uid := fmt.Sprintf("user:%s:%d", e.Id, i)
						uc := NewUserMessageCard(txt,
							WithUserImages(imgs),
							WithUserTimestamp(msg.Timestamp),
							WithUserMessageId(e.Id),
							WithUserTheme(th),
							WithUserOnLink(gmOpenLink),
						)
						entryIdx[uid] = len(entries)
						entries = append(entries, gmTranscriptEntry{id: uid, kind: "user", card: uc})
					}

				case *ai.AssistantMessage:
					aid := fmt.Sprintf("assistant:%s:%d", e.Id, i)
					ac := NewAssistantMessageCard(msg,
						WithAssistantStreaming(false),
						WithAssistantHideThinking(hideThinking),
						WithAssistantThinkingExpanded(thinkingExpanded),
						WithAssistantHiddenThinkingLabel(hiddenLabel),
						WithAssistantShowUsage(true),
						WithAssistantTheme(th),
						WithAssistantOnLink(gmOpenLink),
					)
					entryIdx[aid] = len(entries)
					entries = append(entries, gmTranscriptEntry{id: aid, kind: "assistant", card: ac})
					for _, blk := range msg.Content {
						if tc, ok := blk.(*ai.ToolCall); ok && tc != nil {
							tid := "tool:" + tc.Id
							card := NewToolExecutionCard(tc.Id, tc.Name, tc.Arguments,
								WithToolCwd(cwd),
								WithToolExpanded(toolsExpanded),
								WithToolShowImages(showImages),
								WithToolTheme(th),
								WithToolCustomRenderer(toolRenderers[tc.Name]),
							)
							toolCards[tc.Id] = card
							entryIdx[tid] = len(entries)
							entries = append(entries, gmTranscriptEntry{id: tid, kind: "tool", card: card})
						}
					}

				case *ai.ToolResultMessage:
					if card, ok := toolCards[msg.ToolCallId]; ok && card != nil {
						res := agent.AgentToolResult{
							Content: msg.Content,
							IsError: &msg.IsError,
						}
						if det, hasDet := msg.Details.Get(); hasDet {
							res.Details = det
						}
						card.Complete(res, msg.IsError, msg.DurationMs)
					}

				case *core.BashExecutionMessage:
					bid := fmt.Sprintf("bash:%s:%d", e.Id, i)
					excl := msg.ExcludeFromContext != nil && *msg.ExcludeFromContext
					bc := NewBashExecutionCard(msg.Command, excl,
						WithBashExpanded(toolsExpanded),
						WithBashTheme(th),
					)
					bc.SetFromMessage(msg)
					entryIdx[bid] = len(entries)
					entries = append(entries, gmTranscriptEntry{id: bid, kind: "bash", card: bc})

				case *core.CompactionSummaryMessage:
					cid := fmt.Sprintf("compaction:%s:%d", e.Id, i)
					cc := NewCompactionSummaryCard(msg,
						WithCardExpanded(toolsExpanded),
						WithCardTheme(th),
						WithCardOnLink(gmOpenLink),
					)
					entryIdx[cid] = len(entries)
					entries = append(entries, gmTranscriptEntry{id: cid, kind: "compaction", card: cc})

				case *core.BranchSummaryMessage:
					bsid := fmt.Sprintf("branch:%s:%d", e.Id, i)
					bsc := NewBranchSummaryCard(msg,
						WithCardExpanded(toolsExpanded),
						WithCardTheme(th),
						WithCardOnLink(gmOpenLink),
					)
					entryIdx[bsid] = len(entries)
					entries = append(entries, gmTranscriptEntry{id: bsid, kind: "branch", card: bsc})

				case *core.CustomMessage:
					if msg.Display {
						cmid := fmt.Sprintf("custom:%s:%d", e.Id, i)
						cmc := NewCustomMessageCard(msg,
							WithCustomRenderer(gmResolveCustomMessageRenderer(m, sess, msg.CustomType, customMsgRenderers)),
							WithCustomExpanded(toolsExpanded),
							WithCustomTheme(th),
							WithCustomOnLink(gmOpenLink),
						)
						entryIdx[cmid] = len(entries)
						entries = append(entries, gmTranscriptEntry{id: cmid, kind: "custom", card: cmc})
					}
				}

			case *core.CompactionEntry:
				if e != nil {
					cid := fmt.Sprintf("compaction_entry:%s:%d", e.Id, i)
					cmsg := &core.CompactionSummaryMessage{
						Role:         "compactionSummary",
						Summary:      e.Summary,
						TokensBefore: e.TokensBefore,
					}
					cc := NewCompactionSummaryCard(cmsg,
						WithCardExpanded(toolsExpanded),
						WithCardTheme(th),
						WithCardOnLink(gmOpenLink),
					)
					entryIdx[cid] = len(entries)
					entries = append(entries, gmTranscriptEntry{id: cid, kind: "compaction", card: cc})
				}

			case *core.BranchSummaryEntry:
				if e != nil {
					bsid := fmt.Sprintf("branch_entry:%s:%d", e.Id, i)
					from := e.FromId
					bmsg := &core.BranchSummaryMessage{
						Role:    "branchSummary",
						Summary: e.Summary,
						FromId:  &from,
					}
					bsc := NewBranchSummaryCard(bmsg,
						WithCardExpanded(toolsExpanded),
						WithCardTheme(th),
						WithCardOnLink(gmOpenLink),
					)
					entryIdx[bsid] = len(entries)
					entries = append(entries, gmTranscriptEntry{id: bsid, kind: "branch", card: bsc})
				}

			case *core.CustomMessageEntry:
				if e != nil && e.Display {
					cmid := fmt.Sprintf("custom_entry:%s:%d", e.Id, i)
					cmsg := &core.CustomMessage{
						Role:       "custom",
						CustomType: e.CustomType,
						Content:    e.Content,
						Display:    e.Display,
					}
					if e.Details != nil {
						cmsg.Details = jsonx.Some(e.Details)
					}
					cmc := NewCustomMessageCard(cmsg,
						WithCustomRenderer(gmResolveCustomMessageRenderer(m, sess, e.CustomType, customMsgRenderers)),
						WithCustomExpanded(toolsExpanded),
						WithCustomTheme(th),
						WithCustomOnLink(gmOpenLink),
					)
					entryIdx[cmid] = len(entries)
					entries = append(entries, gmTranscriptEntry{id: cmid, kind: "custom", card: cmc})
				}
			}
		}
	}

	m.mu.Lock()
	m.entries = entries
	m.entryIndex = entryIdx
	m.streamingCard = nil
	m.streamingEntryID = ""
	m.streamingTools = make(map[string]*ToolExecutionCard)
	m.mu.Unlock()

	if m.scrollView != nil {
		m.scrollView.JumpToBottom()
	}
	if m.host != nil {
		m.host.RequestRender()
	}
}

func (m *GuiMode) buildHeaderAnnouncement(sess *core.AgentSession, th *gui.GuiTheme) *HeaderAnnouncement {
	if sess == nil {
		return nil
	}
	sm := sess.SettingsManager
	if sm != nil && sm.GetQuietStartup().Silent {
		return nil
	}

	modelLabel := "No model"
	if mod := sess.Model(); mod != nil {
		modelLabel = fmt.Sprintf("%s/%s", mod.Provider, mod.Id)
	}

	var extNames, skillNames, promptNames, themeNames []string
	if sm == nil || !sm.GetQuietStartup().Header {
		if rl := sess.ResourceLoader(); rl != nil {
			for _, ext := range rl.GetExtensions().Extensions {
				if ext.Path != "" {
					extNames = append(extNames, filepath.Base(ext.Path))
				}
			}
			for _, sk := range rl.GetSkills().Skills {
				skillNames = append(skillNames, sk.Name)
			}
			for _, pt := range rl.GetPrompts().Templates {
				promptNames = append(promptNames, pt.Name)
			}
		}
		for _, thInfo := range theme.GetAvailableThemesWithPaths() {
			themeNames = append(themeNames, thInfo.Name)
		}
	}

	return NewHeaderAnnouncement(HeaderAnnouncementOptions{
		Version:    utils.VERSION,
		ModelLabel: modelLabel,
		Cwd:        sess.Cwd(),
		Extensions: extNames,
		Skills:     skillNames,
		Prompts:    promptNames,
		Themes:     themeNames,
		Theme:      th,
		OnOpenUrl:  gmOpenLink,
	})
}

func (m *GuiMode) setCachedSessionName(name string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.sessionName = strings.TrimSpace(name)
	m.mu.Unlock()
}

func (m *GuiMode) buildSafeFooterState(sess *core.AgentSession) FooterState {
	m.mu.Lock()
	sessionName := m.sessionName
	workingMsg := m.workingMessage
	m.mu.Unlock()

	var st FooterState
	st.SessionName = sessionName
	st.WorkingMessage = workingMsg

	if fp := m.footerProvider; fp != nil {
		st.GitBranch = fp.GetGitBranch()
		if raw := fp.GetExtensionStatuses(); len(raw) > 0 {
			cp := make(map[string]string, len(raw))
			for k, v := range raw {
				cp[k] = v
			}
			st.ExtensionStatuses = cp
		}
		st.AvailableProviders = fp.GetAvailableProviderCount()
	}
	if sess == nil {
		return st
	}

	st.Cwd = sess.Cwd()
	st.ThinkingLevel = sess.ThinkingLevel()
	st.AutoCompactEnabled = sess.AutoCompactionEnabled()
	st.SteeringCount = len(sess.GetSteeringMessages())
	st.FollowUpCount = len(sess.GetFollowUpMessages())
	st.IsStreaming = sess.IsStreaming()
	st.IsCompacting = sess.IsCompacting()
	st.IsRetrying = sess.IsRetrying()
	st.RetryAttempt = sess.RetryAttempt()

	if mdl := sess.Model(); mdl != nil {
		st.Provider = string(mdl.Provider)
		st.ModelId = mdl.Id
		st.ModelName = mdl.Name
		st.ContextWindow = mdl.ContextWindow
		if reg := sess.ModelRegistry(); reg != nil {
			st.UsingSubscription = reg.IsUsingOAuth(mdl)
		}
	}
	if routed := sess.RoutedModel(); routed != nil && routed.Model != nil {
		st.RoutedProvider = string(routed.Model.Provider)
		st.RoutedModelId = routed.Model.Id
		if st.ContextWindow <= 0 && routed.Model.ContextWindow > 0 {
			st.ContextWindow = routed.Model.ContextWindow
		}
	}

	totals := core.CreateUsageTotals()
	lastContextTokens := 0
	hasLastContextTokens := false
	for _, msg := range sess.Messages() {
		switch tm := msg.(type) {
		case *ai.AssistantMessage:
			if tm == nil {
				continue
			}
			core.AddUsageToTotals(&totals, tm.Usage)
			if tm.StopReason != ai.StopReasonAborted && tm.StopReason != ai.StopReasonError {
				if tok := core.CalculateContextTokens(tm.Usage); tok > 0 {
					lastContextTokens = tok
					hasLastContextTokens = true
				}
			}
		case *core.CompactionSummaryMessage:
			lastContextTokens = 0
			hasLastContextTokens = false
		}
	}
	st.InputTokens = totals.Input
	st.OutputTokens = totals.Output
	st.CacheReadTokens = totals.CacheRead
	st.CacheWriteTokens = totals.CacheWrite
	st.TotalCost = totals.Cost
	if st.ContextWindow > 0 && hasLastContextTokens {
		st.ContextTokens = lastContextTokens
		st.HasContextTokens = true
		st.ContextPercent = float64(lastContextTokens) * 100.0 / float64(st.ContextWindow)
		st.HasContextPercent = true
	}

	if cw := sess.CacheWarmingStatus(); cw != nil && cw.State != "" && cw.State != "idle" && cw.State != "disabled" {
		st.CacheWarmingLabel = "cache:" + cw.State
	}
	return st
}

func (m *GuiMode) updateFooterAndTitle() {
	sess := m.Session()
	sessionName := ""
	if m.footer != nil && sess != nil {
		st := m.buildSafeFooterState(sess)
		sessionName = st.SessionName
		m.footer.SetState(st)
	} else {
		m.mu.Lock()
		sessionName = m.sessionName
		m.mu.Unlock()
	}

	m.mu.Lock()
	titleOv := m.titleOverride
	explicitTitle := m.opts.Title
	m.mu.Unlock()

	title := titleOv
	if title == "" {
		title = explicitTitle
	}
	if title == "" {
		title = gmInitialTitleWithName(m.runtime, "", sessionName)
	}
	if m.host != nil {
		m.host.SetTitle(title)
	}
}

func (m *GuiMode) updateProgramStatus(state gui.ProgramStatusState, msg string, kind ...gui.ProgramAttentionKind) {
	if m == nil || m.host == nil {
		return
	}
	var att gui.ProgramAttentionKind
	if len(kind) > 0 {
		att = kind[0]
	}
	m.host.SetProgramStatus(gui.ProgramStatus{
		App:     "pi",
		State:   state,
		Kind:    att,
		Message: msg,
	})
}

func gmResolveCustomMessageRenderer(
	m *GuiMode,
	sess *core.AgentSession,
	customType string,
	customRenderers map[string]CustomMessageRenderer,
) CustomMessageRenderer {
	if customRenderers != nil {
		if fn := customRenderers[customType]; fn != nil {
			return fn
		}
	}
	if sess == nil || customType == "" {
		return nil
	}
	runner := sess.ExtensionRunner()
	if runner == nil {
		return nil
	}
	extRenderer := runner.GetMessageRenderer(customType)
	if extRenderer == nil {
		return nil
	}
	return func(c *ui.Context, msg *core.CustomMessage, expanded bool, th *gui.GuiTheme) ui.Element {
		var tuiTheme *theme.Theme
		if m != nil && m.uiCtx != nil {
			tuiTheme = m.uiCtx.Theme()
		}
		if tuiTheme == nil {
			tuiTheme = theme.CurrentTheme()
		}
		comp := extRenderer(msg, core.MessageRenderOptions{Expanded: expanded}, tuiTheme)
		if comp == nil {
			return ui.Element{}
		}
		rawLines := comp.Render(92)
		cleanLines := make([]string, 0, len(rawLines))
		for _, line := range rawLines {
			clean := strings.TrimRight(utils.StripAnsi(line), " \t\r")
			cleanLines = append(cleanLines, clean)
		}
		for len(cleanLines) > 0 && cleanLines[len(cleanLines)-1] == "" {
			cleanLines = cleanLines[:len(cleanLines)-1]
		}
		body := strings.TrimSpace(strings.Join(cleanLines, "\n"))
		if body == "" {
			return ui.Element{}
		}
		th = gui.ResolveGuiTheme(c, th)
		card := ui.Column(c).
			FillWidth().
			Background(th.CustomMessageBg).
			Border(1, th.BorderMuted).
			Radius(th.Radius).
			Padding(10, 14).
			Gap(6)
		card.Children(func() {
			ui.Text(c, "["+customType+"]").TextColor(th.CustomMessageLabel).FontWeight(700)
			ui.Text(c, body).TextColor(th.Text).Font("monospace").FontSize(12).Selectable().Wrap()
		})
		return card
	}
}

// RefreshAutocomplete rebuilds the slash command + @file autocomplete provider on Editor.
func (m *GuiMode) RefreshAutocomplete() {
	if m == nil || m.editor == nil {
		return
	}
	cwd := gmRuntimeCwd(m.runtime)
	commands := m.buildSlashCommandList()
	var provider gui.AutocompleteProvider = gui.NewCombinedAutocompleteProvider(commands, cwd)
	m.editor.SetAutocompleteProvider(provider)
}

func (m *GuiMode) handleEditorSubmit(ev gui.EditorSubmitEvent, asFollowUp bool) {
	text := strings.TrimSpace(ev.Text)
	if text == "" && len(ev.Attachments) == 0 {
		return
	}
	if m.editor != nil {
		m.editor.AddToHistory(ev.Text)
		m.editor.Clear()
	}

	ctx := m.runCtx
	if ctx == nil {
		ctx = context.Background()
	}

	if ev.BashMode || strings.HasPrefix(text, "!") {
		cmdStr := text
		excl := ev.ExcludeFromContext
		if strings.HasPrefix(cmdStr, "!!") {
			cmdStr = strings.TrimSpace(strings.TrimPrefix(cmdStr, "!!"))
			excl = true
		} else if strings.HasPrefix(cmdStr, "!") {
			cmdStr = strings.TrimSpace(strings.TrimPrefix(cmdStr, "!"))
		}
		if cmdStr != "" {
			go m.executeBashCommand(ctx, cmdStr, excl)
		}
		return
	}

	images := gmExtractAttachmentImages(ev.Attachments, m.runtime)
	fileContext := gmFormatAttachmentFiles(ev.Attachments)
	if fileContext != "" {
		if text != "" {
			text = text + "\n\n" + fileContext
		} else {
			text = fileContext
		}
	}

	go m.dispatchTextOrPrompt(ctx, text, images, asFollowUp)
}

func (m *GuiMode) dispatchTextOrPrompt(ctx context.Context, text string, images []*ai.ImageContent, asFollowUp bool) {
	sess := m.Session()
	if sess == nil {
		m.postUI(func() { m.ShowError("No active session") })
		return
	}

	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "/") && len(images) == 0 {
		if m.HandleSlashCommand(ctx, trimmed) {
			return
		}
	}

	var opts *core.PromptOptions
	if sess.IsStreaming() || sess.IsCompacting() {
		behavior := "steer"
		if asFollowUp {
			behavior = "followUp"
		}
		opts = &core.PromptOptions{
			Images:            images,
			StreamingBehavior: behavior,
			Source:            core.InputSourceInteractive,
		}
	} else {
		opts = &core.PromptOptions{
			Images: images,
			Source: core.InputSourceInteractive,
		}
	}

	if err := sess.Prompt(ctx, text, opts); err != nil {
		m.postUI(func() { m.ShowError(err.Error()) })
	}
}

func (m *GuiMode) executeBashCommand(ctx context.Context, command string, excludeFromContext bool) {
	sess := m.Session()
	if sess == nil {
		m.postUI(func() { m.ShowError("No active session") })
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	id := m.nextEntryID("bash")

	m.postUI(func() {
		th := m.currentGuiTheme()
		m.mu.Lock()
		expanded := m.toolsExpanded
		m.mu.Unlock()
		card := NewBashExecutionCard(command, excludeFromContext,
			WithBashExpanded(expanded),
			WithBashTheme(th),
			WithBashOnCancel(func() {
				cancel()
				sess.AbortBash()
			}),
		)
		m.mu.Lock()
		m.bashCard = card
		m.bashEntryID = id
		m.bashCancel = cancel
		m.bashRunning = true
		m.mu.Unlock()
		m.appendCard("bash", id, card)
		m.statusBanner.SetWorking(true, "Running: "+command)
	})

	defer cancel()
	res, err := sess.ExecuteBash(runCtx, command, func(chunk string) {
		m.postUI(func() {
			m.mu.Lock()
			bc := m.bashCard
			m.mu.Unlock()
			if bc != nil {
				bc.AppendOutput(chunk)
				m.host.RequestRender()
			}
		})
	}, &core.ExecuteBashOptions{
		ExcludeFromContext: excludeFromContext,
		Id:                 &id,
	})

	m.postUI(func() {
		m.mu.Lock()
		bc := m.bashCard
		m.bashCard = nil
		m.bashCancel = nil
		m.bashRunning = false
		m.mu.Unlock()
		if bc != nil {
			bc.SetComplete(res.ExitCode, res.Cancelled, res.Truncated, res.FullOutputPath, nil)
		}
		if err != nil {
			m.ShowError(err.Error())
		}
		if !sess.IsStreaming() && !sess.IsCompacting() && !sess.IsRetrying() {
			m.statusBanner.SetWorking(false, "")
		}
	})

	if !excludeFromContext {
		sess.RecordBashResult(command, res, &core.RecordBashResultOptions{
			ExcludeFromContext: excludeFromContext,
		})
	}
}

func (m *GuiMode) handleAppShortcuts(c *ui.Context) {
	km := m.keybindings
	if km == nil {
		return
	}
	km.OnAppContext(c, gui.AppModelSelect, func() { m.ShowModelSelector() })
	km.OnAppContext(c, gui.AppModelCycleForward, func() { m.cycleModel("forward") })
	km.OnAppContext(c, gui.AppModelCycleBackward, func() { m.cycleModel("backward") })
	km.OnAppContext(c, gui.AppThinkingCycle, func() { m.cycleThinkingLevel() })
	km.OnAppContext(c, gui.AppToolsExpand, func() { m.toggleToolsExpanded() })
	km.OnAppContext(c, gui.AppThinkingToggle, func() { m.toggleThinkingExpanded() })
	km.OnAppContext(c, gui.AppMessageCopy, func() { m.copyLastAssistantMessage() })
	km.OnAppContext(c, gui.AppMessageDequeue, func() { m.restoreQueuedMessages() })
	km.OnAppContext(c, gui.AppClear, func() { m.handleDoubleCtrlC() })
	km.OnAppContext(c, gui.AppExit, func() {
		if m.editor != nil && strings.TrimSpace(m.editor.Text()) == "" {
			m.Stop()
		}
	})
	km.OnAppContext(c, gui.AppSessionNew, func() { m.newSession(m.currentCtx()) })
	km.OnAppContext(c, gui.AppSessionTree, func() { m.ShowTreeSelector() })
	km.OnAppContext(c, gui.AppSessionFork, func() { m.ShowForkSelector() })
	km.OnAppContext(c, gui.AppSessionResume, func() { m.ShowSessionSelector() })
	km.OnAppContext(c, gui.AppClipboardPasteImage, func() { m.pasteImageFromClipboard() })
}

func (m *GuiMode) currentCtx() context.Context {
	if m.runCtx != nil {
		return m.runCtx
	}
	return context.Background()
}

func (m *GuiMode) handleEscapeAbort() {
	if m.host != nil && m.host.HasOverlay() {
		m.host.HideOverlay()
		return
	}
	sess := m.Session()
	if sess == nil {
		return
	}
	if sess.IsBashRunning() {
		sess.AbortBash()
		return
	}
	if sess.IsCompacting() {
		sess.AbortCompaction()
		return
	}
	if sess.IsRetrying() {
		sess.AbortRetry()
		return
	}
	if sess.IsStreaming() {
		_ = sess.Abort(m.currentCtx())
		return
	}

	// Idle double-Escape triggers configured doubleEscapeAction (tree vs fork).
	now := time.Now()
	m.mu.Lock()
	elapsed := now.Sub(m.lastEscPress)
	m.lastEscPress = now
	m.mu.Unlock()

	if elapsed <= gmDoubleEscapeWindow && (m.editor == nil || m.editor.Text() == "") && sess.SettingsManager != nil {
		action := sess.SettingsManager.GetDoubleEscapeAction()
		switch action {
		case core.DoubleEscapeActionTree:
			m.ShowTreeSelector()
		case core.DoubleEscapeActionFork:
			m.ShowForkSelector()
		}
	}
}

func (m *GuiMode) handleDoubleCtrlC() {
	if m.editor != nil && m.editor.Text() != "" {
		m.editor.Clear()
		return
	}
	now := time.Now()
	m.mu.Lock()
	elapsed := now.Sub(m.lastExitPress)
	m.lastExitPress = now
	m.mu.Unlock()

	if elapsed <= gmExitConfirmWindow {
		m.Stop()
		return
	}
	if m.statusBanner != nil {
		m.statusBanner.SetCustomNotice("Press Ctrl+C again to quit", false)
	}
}

func (m *GuiMode) restoreQueuedMessages() {
	sess := m.Session()
	if sess == nil || m.editor == nil {
		return
	}
	cleared := sess.ClearQueue()
	var parts []string
	parts = append(parts, cleared.Steering...)
	parts = append(parts, cleared.FollowUp...)
	if len(parts) == 0 {
		return
	}
	restored := strings.Join(parts, "\n\n")
	cur := strings.TrimSpace(m.editor.Text())
	if cur != "" {
		restored = restored + "\n\n" + cur
	}
	m.editor.SetText(restored)
}

func (m *GuiMode) cycleModel(direction string) {
	sess := m.Session()
	if sess == nil {
		return
	}
	res, err := sess.CycleModel(m.currentCtx(), direction)
	if err != nil {
		m.ShowError(err.Error())
		return
	}
	if res == nil || res.Model == nil {
		m.ShowInfo("Models", "Only one model available to cycle")
		return
	}
	m.updateFooterAndTitle()
	m.ShowInfo("Model", fmt.Sprintf("Switched to %s/%s (thinking: %s)", res.Model.Provider, res.Model.Id, res.ThinkingLevel))
}

func (m *GuiMode) cycleThinkingLevel() {
	sess := m.Session()
	if sess == nil {
		return
	}
	next := sess.CycleThinkingLevel()
	if next == nil {
		m.ShowInfo("Thinking", "Current model does not support reasoning levels")
		return
	}
	m.updateFooterAndTitle()
}

func (m *GuiMode) toggleToolsExpanded() {
	m.mu.Lock()
	next := !m.toolsExpanded
	m.toolsExpanded = next
	entries := append([]gmTranscriptEntry(nil), m.entries...)
	m.mu.Unlock()

	for _, e := range entries {
		switch c := e.card.(type) {
		case *ToolExecutionCard:
			c.SetExpanded(next)
		case *BashExecutionCard:
			c.SetExpanded(next)
		case *CompactionSummaryCard:
			c.SetExpanded(next)
		case *BranchSummaryCard:
			c.SetExpanded(next)
		case *SkillInvocationCard:
			c.SetExpanded(next)
		case *CustomMessageCard:
			c.SetExpanded(next)
		}
	}
	if m.host != nil {
		m.host.RequestRender()
	}
}

func (m *GuiMode) toggleThinkingExpanded() {
	m.mu.Lock()
	next := !m.thinkingExpanded
	m.thinkingExpanded = next
	entries := append([]gmTranscriptEntry(nil), m.entries...)
	m.mu.Unlock()

	for _, e := range entries {
		if ac, ok := e.card.(*AssistantMessageCard); ok {
			ac.SetThinkingExpanded(next)
		}
	}
	if m.host != nil {
		m.host.RequestRender()
	}
}

func (m *GuiMode) copyLastAssistantMessage() {
	sess := m.Session()
	if sess == nil {
		return
	}
	last := sess.GetLastAssistantText()
	if last == nil || strings.TrimSpace(*last) == "" {
		m.ShowInfo("Copy", "No assistant message to copy")
		return
	}
	if err := gmWriteClipboardText(m.host, *last); err != nil {
		m.ShowError("Failed to copy: " + err.Error())
		return
	}
	m.ShowInfo("Copied", "Last assistant message copied to clipboard")
}

func (m *GuiMode) pasteImageFromClipboard() {
	if m.host == nil || m.editor == nil {
		return
	}
	pngBytes, err := m.host.ReadClipboardPNG()
	if err != nil || len(pngBytes) == 0 {
		return
	}
	m.editor.AddImageAttachment("clipboard.png", pngBytes)
	m.host.RequestRender()
}

func (m *GuiMode) setActiveDialog(dlg *DialogHandle) {
	m.mu.Lock()
	prev := m.activeDialog
	m.activeDialog = dlg
	m.mu.Unlock()
	if prev != nil && prev != dlg {
		prev.Close()
	}
}

func gmRenderWidgets(c *ui.Context, th *gui.GuiTheme, widgets []gmWidgetItem, placement core.WidgetPlacement) {
	for _, w := range widgets {
		if w.placement != placement {
			continue
		}
		item := w
		if item.render != nil {
			ui.Box(c).Key("widget:" + item.key).FillWidth().Children(func() {
				item.render(c, th)
			})
			continue
		}
		if len(item.lines) > 0 {
			ui.Column(c).
				Key("widget:"+item.key).
				FillWidth().
				Background(th.Surface).
				Border(1, th.BorderMuted).
				Radius(th.Radius).
				Padding(6, 10).
				Gap(2).
				Children(func() {
					for _, line := range item.lines {
						ui.Text(c, line).TextColor(th.Muted).FontSize(12)
					}
				})
		}
	}
}

func gmLoadKeybindings() *gui.KeybindingsManager {
	path := filepath.Join(utils.GetAgentDir(), "keybindings.json")
	if km, err := gui.LoadKeybindingsFromFile(path); err == nil && km != nil {
		return km
	}
	return gui.NewKeybindingsManager()
}

func gmRuntimeSettings(rt *core.AgentSessionRuntime) *core.SettingsManager {
	if rt != nil && rt.Session != nil {
		return rt.Session.SettingsManager
	}
	return nil
}

func gmRuntimeCwd(rt *core.AgentSessionRuntime) string {
	if rt != nil {
		return rt.Cwd()
	}
	return ""
}

func gmInitialTitle(rt *core.AgentSessionRuntime, explicit string) string {
	sessName := ""
	if rt != nil && rt.Session != nil {
		if name := rt.Session.SessionName(); name != nil {
			sessName = strings.TrimSpace(*name)
		}
	}
	return gmInitialTitleWithName(rt, explicit, sessName)
}

func gmInitialTitleWithName(rt *core.AgentSessionRuntime, explicit, sessName string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if trimmed := strings.TrimSpace(sessName); trimmed != "" {
		return "pi — " + trimmed
	}
	if rt != nil {
		if cwd := rt.Cwd(); cwd != "" {
			return "pi — " + filepath.Base(cwd)
		}
	}
	return "pi"
}

func gmInitTheme(host *gui.GUI, sm *core.SettingsManager, initialSetting *string, allowAppThemeQuery bool) *ThemeSet {
	setting := ""
	if initialSetting != nil && strings.TrimSpace(*initialSetting) != "" {
		setting = strings.TrimSpace(*initialSetting)
	} else if sm != nil {
		if ts := sm.GetThemeSetting(); ts != nil && strings.TrimSpace(*ts) != "" {
			setting = strings.TrimSpace(*ts)
		} else if t := sm.GetTheme(); t != nil && strings.TrimSpace(*t) != "" {
			setting = strings.TrimSpace(*t)
		}
	}
	isDark := false
	if allowAppThemeQuery && mygo.App.IsReady() {
		isDark = mygo.Theme.IsDark()
	}
	resolved := ResolveThemeForSetting(setting, isDark)
	if resolved != "" {
		_, ts := ApplyThemeToHost(host, resolved)
		if ts != nil {
			return ts
		}
	}
	cur := theme.CurrentTheme()
	if cur != nil {
		return ApplyThemeInstanceToHost(host, cur)
	}
	gth := ResolveActiveGuiTheme()
	return &ThemeSet{Gui: gth}
}

func gmOpenLink(target string) {
	if strings.TrimSpace(target) != "" {
		utils.OpenBrowser(target)
	}
}

func gmWriteClipboardText(host *gui.GUI, text string) error {
	if host != nil {
		if err := host.WriteClipboardText(text); err == nil {
			return nil
		}
	}
	return utils.CopyToClipboard(text)
}

func gmUniqueProviders(models []*ai.Model) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, mod := range models {
		if mod == nil {
			continue
		}
		p := string(mod.Provider)
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func gmExtractAttachmentImages(atts []gui.EditorAttachment, rt *core.AgentSessionRuntime) []*ai.ImageContent {
	var autoResize *bool
	if sm := gmRuntimeSettings(rt); sm != nil {
		v := sm.GetImageAutoResize()
		autoResize = &v
	}
	var out []*ai.ImageContent
	for _, att := range atts {
		if att.Kind != gui.AttachmentImage || len(att.Data) == 0 {
			continue
		}
		mime := att.MIMEType
		if mime == "" {
			mime = utils.DetectSupportedImageMimeType(att.Data)
		}
		if mime == "" {
			mime = "image/png"
		}
		proc := utils.ProcessImage(att.Data, mime, &utils.ProcessImageOptions{
			AutoResizeImages: autoResize,
		})
		if !proc.Ok {
			continue
		}
		encoded := proc.Data
		if encoded == "" {
			encoded = base64.StdEncoding.EncodeToString(att.Data)
		}
		outMime := proc.MimeType
		if outMime == "" {
			outMime = mime
		}
		out = append(out, &ai.ImageContent{
			Type:     "image",
			Data:     encoded,
			MimeType: outMime,
		})
	}
	return out
}

func gmFormatAttachmentFiles(atts []gui.EditorAttachment) string {
	var lines []string
	for _, att := range atts {
		if att.Kind == gui.AttachmentFile && att.Path != "" {
			lines = append(lines, "@"+att.Path)
		}
	}
	return strings.Join(lines, " ")
}
