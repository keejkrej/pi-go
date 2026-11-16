// Ported from packages/coding-agent/src/experimental/client-tui.ts (pi v1.0.0).

package experimental

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/experimental/plugins"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/tui"
)

// ClientTuiServer bundles one connected server's ID, mode, and remote service sources for ClientTui.
type ClientTuiServer struct {
	ServerId protocol.ServerId
	Radius   bool
	Server   services.ServerServiceSource
	Session  services.SessionServiceSource
}

// ClientTuiOptions configures CreateClientTui.
type ClientTuiOptions struct {
	Command                experimentalcli.ClientCommand
	UI                     tui.TUI
	Terminal               tui.Terminal
	Servers                []ClientTuiServer
	AgentDir               string
	Cwd                    string
	RequestRender          func()
	Finish                 func()
	LoadPresentationFacets func(data chord.JsonValue) ([]chord.FacetLoader, error)
}

type ctServerHandles struct {
	entry       ClientTuiServer
	serverOpen  chord.RemoteServices
	sessionOpen chord.RemoteServices
	directory   services.SessionDirectory
	management  services.SessionManagement
	plugins     services.PresentationPlugins
}

type ctSessionChoice struct {
	handle    *ctServerHandles
	sessionId string
	createdAt int64
}

type ctPresentationUI struct {
	tui *ClientTui
}

func (p *ctPresentationUI) Select(title string, items []services.PresentationSelectItem, selectedValue *string, ctx chord.Context) (*string, error) {
	return p.tui.ctRunSelect(title, items, selectedValue, ctx)
}

func (p *ctPresentationUI) ShowStatus(message string, _ chord.Context) {
	p.tui.ctSetStatus(message)
}

type ctSyncText struct {
	mu    sync.Mutex
	inner *tui.Text
}

func ctNewSyncText(text string, paddingX int, paddingY int) *ctSyncText {
	return &ctSyncText{inner: tui.NewText(text, paddingX, paddingY, nil)}
}

func (t *ctSyncText) SetText(text string) {
	t.mu.Lock()
	t.inner.SetText(text)
	t.mu.Unlock()
}

func (t *ctSyncText) Invalidate() {
	t.mu.Lock()
	t.inner.Invalidate()
	t.mu.Unlock()
}

func (t *ctSyncText) Render(width int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.inner.Render(width))
}

// ClientTui coordinates session discovery/selection, presentation facets, and interactive chat rendering.
type ClientTui struct {
	UI tui.TUI

	mu                     sync.Mutex
	command                experimentalcli.ClientCommand
	servers                []*ctServerHandles
	activeHandle           *ctServerHandles
	activeSessionId        string
	cwd                    string
	agentDir               string
	settings               *core.SettingsManager
	keybindings            *core.KeybindingsManager
	themeController        *core.InteractiveThemeController
	requestRender          func()
	finish                 func()
	loadPresentationFacets func(data chord.JsonValue) ([]chord.FacetLoader, error)

	baseFacets        []chord.Facet
	host              chord.FacetHost
	presDispose       func() error
	slashCommands     services.SlashCommands
	agent             services.AgentController
	transcript        services.Transcript
	models            services.Models
	chat              *ClientChat
	root              *tui.Container
	selectorContainer *tui.Container
	selectorList      *tui.SelectList
	selectorChoices   []ctSessionChoice
	overlayContainer  *tui.Container
	overlaySelectList *tui.SelectList
	cancelPendingSel  func()

	statusText   string
	footerText   string
	busy         bool
	laneAttached bool
	headingComp  *ctSyncText
	statusComp   *ctSyncText
	footerComp   *ctSyncText
	unsubConn    func()
	unsubAttach  func()
	unsubView    func()

	cancelCtx chord.CancelContext
	closedCh  chan struct{}
	closeOnce sync.Once
	disposed  bool
}

// CreateClientTui connects to the provided servers, resolves or picks a session, and initializes the TUI.
func CreateClientTui(options ClientTuiOptions) (*ClientTui, error) {
	if len(options.Servers) == 0 {
		return nil, errors.New("Client requires at least one server")
	}

	cwd := options.Cwd
	if cwd == "" {
		cwd = "."
	}
	settings := core.SettingsManagerCreate(cwd, options.AgentDir)
	kb := core.KeybindingsManagerCreate(options.AgentDir)
	tui.SetKeybindings(kb.KeybindingsManager)

	if theme.CurrentTheme() == nil {
		_, _ = theme.InitTheme("dark", false)
	}

	ui := options.UI
	if ui == nil {
		var tuiMode core.TuiMode = tui.TuiModeRegular
		wheelLines := settings.GetFullscreenWheelScrollLines()
		ui = interactive.CreateInteractiveTui(interactive.InteractiveTuiOptions{
			TuiMode:                    tuiMode,
			ShowHardwareCursor:         settings.GetShowHardwareCursor(),
			Terminal:                   options.Terminal,
			FullscreenCopyOnSelect:     ctBoolPtr(settings.GetFullscreenCopyOnSelect()),
			FullscreenWheelScrollLines: &wheelLines,
		})
	}

	reqRender := options.RequestRender
	if reqRender == nil {
		reqRender = func() {
			ui.RequestRender(nil)
		}
	}

	loadPres := options.LoadPresentationFacets
	if loadPres == nil {
		loadPres = plugins.CreatePresentationFacetLoaders
	}

	ct := &ClientTui{
		UI:                     ui,
		command:                options.Command,
		cwd:                    cwd,
		agentDir:               options.AgentDir,
		settings:               settings,
		keybindings:            kb,
		requestRender:          reqRender,
		finish:                 options.Finish,
		loadPresentationFacets: loadPres,
		root:                   tui.NewContainer(),
		headingComp:            ctNewSyncText("", 1, 0),
		statusComp:             ctNewSyncText("", 1, 0),
		footerComp:             ctNewSyncText(ctStyleDim(ctFormatFooter(nil)), 1, 0),
		cancelCtx:              chord.WithCancel(chord.BackgroundContext),
		closedCh:               make(chan struct{}),
	}

	handles := make([]*ctServerHandles, 0, len(options.Servers))
	for _, entry := range options.Servers {
		if entry.Server == nil || entry.Session == nil {
			_ = ct.Dispose()
			return nil, fmt.Errorf("Server %s is missing service sources", entry.ServerId)
		}
		serverOpen := entry.Server.Open(&chord.RemoteServiceSourceOpenOptions{
			Services: []chord.ServiceRef{
				{Id: services.SessionDirectoryService.Id},
				{Id: services.SessionManagementService.Id},
				{Id: services.PresentationPluginsService.Id},
			},
			AssertAccess: func() {},
			OnError:      func(error) {},
		})
		directory := services.AsSessionDirectory(serverOpen.Use(services.SessionDirectoryService))
		management := services.AsSessionManagement(serverOpen.Use(services.SessionManagementService))
		presPlugins := services.AsPresentationPlugins(serverOpen.Use(services.PresentationPluginsService))
		if err := serverOpen.Ready(ct.cancelCtx.Context); err != nil {
			_ = serverOpen.Dispose(chord.BackgroundContext)
			_ = ct.Dispose()
			return nil, err
		}
		handles = append(handles, &ctServerHandles{
			entry:      entry,
			serverOpen: serverOpen,
			directory:  directory,
			management: management,
			plugins:    presPlugins,
		})
	}
	ct.servers = handles

	if err := ct.ctResolveInitialMode(); err != nil {
		_ = ct.Dispose()
		return nil, err
	}
	return ct, nil
}

// RunClientTui creates a ClientTui and drives its UI event loop until ctx cancels or the user exits.
func RunClientTui(ctx context.Context, options ClientTuiOptions) error {
	ct, err := CreateClientTui(options)
	if err != nil {
		return err
	}
	return ct.Run(ctx)
}

// Run mounts the TUI root, starts UI rendering, and blocks until ctx is done or Finish/Ctrl+D exits.
func (c *ClientTui) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if vp, ok := c.UI.(tui.ViewportTUI); ok {
		vp.SetLayoutRoot(c)
	} else {
		c.UI.SetChildren([]tui.Component{c})
	}
	c.UI.SetFocus(c.FocusTarget())
	c.UI.Start()
	c.requestRender()

	select {
	case <-ctx.Done():
	case <-c.closedCh:
	}

	c.UI.Stop(nil)
	return c.Dispose()
}

// Render renders whichever screen is active (overlay selector, session picker, or chat).
func (c *ClientTui) Render(width int) []string {
	c.mu.Lock()
	if c.overlayContainer != nil || c.selectorContainer != nil || c.chat == nil {
		active := c.ctActiveComponentLocked()
		c.mu.Unlock()
		if active == nil {
			return nil
		}
		return active.Render(width)
	}
	chat := c.chat
	laneAttached := c.laneAttached
	statusText := c.statusText
	headingComp := c.headingComp
	statusComp := c.statusComp
	footerComp := c.footerComp
	activeHandle := c.activeHandle
	activeSessionId := c.activeSessionId
	c.mu.Unlock()

	var lines []string
	if laneAttached && activeHandle != nil && activeSessionId != "" && headingComp != nil {
		lines = append(lines, headingComp.Render(width)...)
	}
	if laneAttached {
		lines = append(lines, chat.Render(width)...)
	} else {
		lines = append(lines, chat.RenderEditorOnly(width)...)
	}
	if statusText != "" && statusComp != nil {
		lines = append(lines, statusComp.Render(width)...)
	}
	if footerComp != nil {
		lines = append(lines, footerComp.Render(width)...)
	}
	return lines
}

// Invalidate drops cached rendering state across active subcomponents.
func (c *ClientTui) Invalidate() {
	c.mu.Lock()
	active := c.ctActiveComponentLocked()
	headingComp := c.headingComp
	statusComp := c.statusComp
	footerComp := c.footerComp
	c.mu.Unlock()
	if headingComp != nil {
		headingComp.Invalidate()
	}
	if statusComp != nil {
		statusComp.Invalidate()
	}
	if footerComp != nil {
		footerComp.Invalidate()
	}
	if active != nil {
		active.Invalidate()
	}
}

// HandleInput routes terminal input to the active selection overlay, session picker, or chat editor.
func (c *ClientTui) HandleInput(data string) {
	c.mu.Lock()
	busy := c.busy
	chat := c.chat
	kb := c.keybindings
	var handler interface{ HandleInput(string) }
	isChat := false
	switch {
	case c.overlaySelectList != nil:
		handler = c.overlaySelectList
	case c.selectorList != nil:
		handler = c.selectorList
	case c.chat != nil:
		handler = c.chat.Editor
		isChat = true
	}
	c.mu.Unlock()

	if busy {
		emptyEditor := chat == nil || len(chat.Editor.GetText()) == 0
		if kb != nil && (kb.Matches(data, core.KeybindingAppClear) || (emptyEditor && kb.Matches(data, core.KeybindingAppExit))) {
			c.Finish()
		}
		return
	}
	if handler != nil {
		handler.HandleInput(data)
		if isChat {
			c.requestRender()
		}
	}
}

// FocusTarget returns the component that should currently hold keyboard focus.
func (c *ClientTui) FocusTarget() tui.Component {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.overlaySelectList != nil:
		return c.overlaySelectList
	case c.selectorList != nil:
		return c.selectorList
	case c.chat != nil:
		return c.chat.Editor
	default:
		return c
	}
}

// Chat returns the active ClientChat component, or nil while only the session picker is displayed.
func (c *ClientTui) Chat() *ClientChat {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chat
}

// ActiveServerId returns the ServerId of the currently attached session.
func (c *ClientTui) ActiveServerId() protocol.ServerId {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeHandle == nil {
		return ""
	}
	return c.activeHandle.entry.ServerId
}

// ActiveSessionId returns the ID of the currently attached session.
func (c *ClientTui) ActiveSessionId() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeSessionId
}

// ShowingSessionSelector reports whether the initial or /sessions picker is currently shown.
func (c *ClientTui) ShowingSessionSelector() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selectorList != nil
}

// SelectList returns the currently visible SelectList (overlay or session picker), or nil.
func (c *ClientTui) SelectList() *tui.SelectList {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.overlaySelectList != nil {
		return c.overlaySelectList
	}
	return c.selectorList
}

// SlashCommands returns the active SlashCommands runtime service, if initialized.
func (c *ClientTui) SlashCommands() services.SlashCommands {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.slashCommands
}

// Agent returns the active AgentController proxy, or nil when no session is attached.
func (c *ClientTui) Agent() services.AgentController {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agent
}

// Models returns the active Models proxy, or nil when no session is attached.
func (c *ClientTui) Models() services.Models {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.models
}

// HostServices returns the active local Chord FacetHost service provider, or nil.
func (c *ClientTui) HostServices() *chord.RemoteServiceProvider {
	c.mu.Lock()
	host := c.host
	c.mu.Unlock()
	if host == nil {
		return nil
	}
	return host.Services()
}

// Finish requests clean TUI termination and invokes options.Finish if configured.
func (c *ClientTui) Finish() {
	c.mu.Lock()
	finishFn := c.finish
	c.mu.Unlock()
	if finishFn != nil {
		finishFn()
		return
	}
	c.closeOnce.Do(func() {
		close(c.closedCh)
	})
}

// AttachSession detaches any current session, prepares plugins, attaches sessionId on serverId, and binds the chat UI.
func (c *ClientTui) AttachSession(serverId protocol.ServerId, sessionId string, ctx ...chord.Context) error {
	callCtx := c.cancelCtx.Context
	if len(ctx) > 0 && ctx[0] != nil {
		callCtx = ctx[0]
	}

	c.mu.Lock()
	var target *ctServerHandles
	for _, h := range c.servers {
		if h.entry.ServerId == serverId {
			target = h
			break
		}
	}
	prevHandle := c.activeHandle
	prevSession := c.activeSessionId
	pkgPaths := append([]string(nil), c.command.PluginPackages...)
	c.mu.Unlock()

	if target == nil {
		return fmt.Errorf("Unknown server %s", serverId)
	}

	if prevHandle != nil && prevSession != "" && (prevHandle != target || prevSession != sessionId) {
		if err := c.ctDetachCurrent(callCtx); err != nil {
			return err
		}
	}

	return c.ctAttachHandle(target, sessionId, pkgPaths, callCtx)
}

// Dispose detaches any active session, tears down the facet host, chat, and pending selections.
func (c *ClientTui) Dispose() error {
	c.closeOnce.Do(func() {
		close(c.closedCh)
	})

	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return nil
	}
	c.disposed = true
	cancelSel := c.cancelPendingSel
	c.cancelPendingSel = nil
	unsubConn := c.unsubConn
	c.unsubConn = nil
	unsubAttach := c.unsubAttach
	c.unsubAttach = nil
	unsubView := c.unsubView
	c.unsubView = nil
	chat := c.chat
	c.chat = nil
	host := c.host
	c.host = nil
	presDisp := c.presDispose
	c.presDispose = nil
	themeCtrl := c.themeController
	c.themeController = nil
	activeHandle := c.activeHandle
	activeSession := c.activeSessionId
	servers := append([]*ctServerHandles(nil), c.servers...)
	c.activeHandle = nil
	c.activeSessionId = ""
	c.mu.Unlock()

	if cancelSel != nil {
		cancelSel()
	}
	if unsubConn != nil {
		unsubConn()
	}
	if unsubAttach != nil {
		unsubAttach()
	}
	if unsubView != nil {
		unsubView()
	}
	if chat != nil {
		chat.Dispose()
	}

	var errs []error
	if activeHandle != nil && activeSession != "" {
		if err := activeHandle.management.Detach(chord.BackgroundContext); err != nil {
			errs = append(errs, err)
		}
		if err := activeHandle.entry.Session.WhenDetached(chord.BackgroundContext); err != nil {
			errs = append(errs, err)
		}
	}
	if host != nil {
		if err := host.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}
	for _, h := range servers {
		if h.sessionOpen != nil {
			if err := h.sessionOpen.Dispose(chord.BackgroundContext); err != nil {
				errs = append(errs, err)
			}
		}
		if h.serverOpen != nil {
			if err := h.serverOpen.Dispose(chord.BackgroundContext); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if presDisp != nil {
		if err := presDisp(); err != nil {
			errs = append(errs, err)
		}
	}
	if themeCtrl != nil {
		themeCtrl.Dispose()
	}
	if c.cancelCtx.Cancel != nil {
		c.cancelCtx.Cancel(nil)
	}
	return errors.Join(errs...)
}

func (c *ClientTui) ctActiveComponentLocked() tui.Component {
	if c.overlayContainer != nil {
		return c.overlayContainer
	}
	if c.selectorContainer != nil {
		return c.selectorContainer
	}
	if c.chat != nil {
		return c.chat
	}
	return c.root
}

func (c *ClientTui) ctResolveInitialMode() error {
	ctx := c.cancelCtx.Context
	pkgPaths := c.command.PluginPackages

	if c.command.Resume {
		c.ctShowSessionSelector()
		return nil
	}

	if c.command.SessionId != nil {
		selectedId := *c.command.SessionId
		var matches []*ctServerHandles
		for _, h := range c.servers {
			dirState, ok := h.directory.State().Value()
			if !ok {
				continue
			}
			for _, s := range dirState.Sessions {
				if s.SessionId == selectedId {
					matches = append(matches, h)
					break
				}
			}
		}
		if len(matches) > 1 {
			return fmt.Errorf("Session %s is available from more than one server", selectedId)
		}
		if len(matches) == 0 {
			return fmt.Errorf("No discovered server contains session %s", selectedId)
		}
		if err := c.ctAttachHandle(matches[0], selectedId, pkgPaths, ctx); err != nil {
			return err
		}
		c.ctSubmitInitialPrompt()
		return nil
	}

	if c.command.Continue {
		choices := c.ctCollectSessionChoices()
		if len(choices) > 0 {
			sort.SliceStable(choices, func(i, j int) bool {
				if choices[i].createdAt != choices[j].createdAt {
					return choices[i].createdAt > choices[j].createdAt
				}
				if choices[i].handle.entry.ServerId != choices[j].handle.entry.ServerId {
					return choices[i].handle.entry.ServerId < choices[j].handle.entry.ServerId
				}
				return choices[i].sessionId < choices[j].sessionId
			})
			chosen := choices[0]
			if err := c.ctAttachHandle(chosen.handle, chosen.sessionId, pkgPaths, ctx); err != nil {
				return err
			}
			c.ctSubmitInitialPrompt()
			return nil
		}
		if len(c.servers) != 1 {
			return errors.New("No existing session to continue")
		}
	}

	if len(c.servers) != 1 {
		c.ctShowSessionSelector()
		return nil
	}
	target := c.servers[0]
	summary, err := target.management.Create(services.SessionCreateOptions{}, ctx)
	if err != nil {
		return err
	}
	if err := c.ctAttachHandle(target, summary.SessionId, pkgPaths, ctx); err != nil {
		return err
	}
	c.ctSubmitInitialPrompt()
	return nil
}

func (c *ClientTui) ctSubmitInitialPrompt() {
	if c.command.Prompt == nil || *c.command.Prompt == "" {
		return
	}
	chat := c.Chat()
	if chat != nil {
		chat.SubmitText(*c.command.Prompt)
	}
}

func (c *ClientTui) ctCollectSessionChoices() []ctSessionChoice {
	c.mu.Lock()
	servers := append([]*ctServerHandles(nil), c.servers...)
	c.mu.Unlock()

	var choices []ctSessionChoice
	for _, h := range servers {
		dirState, ok := h.directory.State().Value()
		if !ok {
			continue
		}
		for _, s := range dirState.Sessions {
			choices = append(choices, ctSessionChoice{
				handle:    h,
				sessionId: s.SessionId,
				createdAt: s.CreatedAt,
			})
		}
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].createdAt != choices[j].createdAt {
			return choices[i].createdAt > choices[j].createdAt
		}
		if choices[i].handle.entry.ServerId != choices[j].handle.entry.ServerId {
			return choices[i].handle.entry.ServerId < choices[j].handle.entry.ServerId
		}
		return choices[i].sessionId < choices[j].sessionId
	})
	return choices
}

func (c *ClientTui) ctShowSessionSelector() {
	choices := c.ctCollectSessionChoices()
	items := make([]tui.SelectItem, 0, len(choices)+1)
	for _, ch := range choices {
		desc := string(ch.handle.entry.ServerId)
		items = append(items, tui.SelectItem{
			Value:       string(ch.handle.entry.ServerId) + "\x00" + ch.sessionId,
			Label:       ch.sessionId,
			Description: &desc,
		})
	}
	c.mu.Lock()
	canCreate := len(c.servers) == 1
	var singleServer *ctServerHandles
	if canCreate {
		singleServer = c.servers[0]
	}
	c.mu.Unlock()

	if canCreate {
		desc := "Create a new session"
		items = append(items, tui.SelectItem{
			Value:       "\x00new",
			Label:       "+ New session",
			Description: &desc,
		})
	}

	maxVis := len(items) + 2
	if maxVis < 4 {
		maxVis = 4
	}
	if maxVis > 12 {
		maxVis = 12
	}
	list := tui.NewSelectList(items, maxVis, theme.GetSelectListTheme(), nil)
	list.OnCancel = func() {
		c.mu.Lock()
		hasChat := c.chat != nil
		c.selectorContainer = nil
		c.selectorList = nil
		var editor tui.Component
		if c.chat != nil {
			editor = c.chat.Editor
		}
		c.mu.Unlock()

		if hasChat && editor != nil {
			c.UI.SetFocus(editor)
			c.requestRender()
			return
		}
		c.Finish()
	}
	list.OnSelect = func(selected tui.SelectItem) {
		ctx := c.cancelCtx.Context
		pkgPaths := c.command.PluginPackages
		if selected.Value == "\x00new" && singleServer != nil {
			go func() {
				summary, err := singleServer.management.Create(services.SessionCreateOptions{}, ctx)
				if err != nil {
					c.Finish()
					return
				}
				if err := c.ctAttachHandle(singleServer, summary.SessionId, pkgPaths, ctx); err != nil {
					chat := c.Chat()
					if chat != nil {
						chat.AppendNotice(err.Error(), true)
					}
					return
				}
				c.mu.Lock()
				c.selectorContainer = nil
				c.selectorList = nil
				c.mu.Unlock()
				c.UI.SetFocus(c.FocusTarget())
				c.requestRender()
			}()
			return
		}
		for _, ch := range choices {
			key := string(ch.handle.entry.ServerId) + "\x00" + ch.sessionId
			if key == selected.Value {
				chosen := ch
				go func() {
					if err := c.AttachSession(chosen.handle.entry.ServerId, chosen.sessionId, ctx); err != nil {
						chat := c.Chat()
						if chat != nil {
							chat.AppendNotice(err.Error(), true)
						}
						return
					}
					c.mu.Lock()
					c.selectorContainer = nil
					c.selectorList = nil
					c.mu.Unlock()
					c.UI.SetFocus(c.FocusTarget())
					c.requestRender()
				}()
				break
			}
		}
	}

	th := theme.CurrentTheme()
	header := "Select a session"
	if th != nil {
		header = th.Bold(th.Fg(theme.FgAccent, header))
	}
	container := tui.NewContainer()
	container.AddChild(components.NewDynamicBorder())
	container.AddChild(tui.NewText(header, 1, 0, nil))
	container.AddChild(list)
	container.AddChild(components.NewDynamicBorder())

	c.mu.Lock()
	c.selectorChoices = choices
	c.selectorContainer = container
	c.selectorList = list
	c.mu.Unlock()

	c.UI.SetFocus(list)
	c.requestRender()
}

func (c *ClientTui) ctDetachCurrent(ctx chord.Context) error {
	c.mu.Lock()
	handle := c.activeHandle
	sessionId := c.activeSessionId
	c.activeHandle = nil
	c.activeSessionId = ""
	c.mu.Unlock()

	if handle == nil || sessionId == "" {
		return nil
	}
	if err := handle.management.Detach(ctx); err != nil {
		return err
	}
	return handle.entry.Session.WhenDetached(ctx)
}

func (c *ClientTui) ctAttachHandle(handle *ctServerHandles, sessionId string, pkgPaths []string, ctx chord.Context) error {
	var resolvedPkgs []string
	if pkgPaths != nil {
		resolvedPkgs = make([]string, 0, len(pkgPaths))
		for _, p := range pkgPaths {
			abs, err := filepath.Abs(p)
			if err != nil {
				return err
			}
			resolvedPkgs = append(resolvedPkgs, abs)
		}
	}

	presData, err := handle.plugins.PrepareSession(services.PrepareSessionPluginsRequest{
		SessionId:    sessionId,
		PackagePaths: resolvedPkgs,
	}, ctx)
	if err != nil {
		return err
	}

	if err := handle.management.Attach(sessionId, ctx); err != nil {
		return err
	}
	if err := handle.entry.Session.WhenAttached(sessionId, ctx); err != nil {
		return err
	}

	if err := c.ctEnsureHostAndReload(handle, presData); err != nil {
		return err
	}

	c.mu.Lock()
	oldUnsubConn := c.unsubConn
	c.unsubConn = nil
	oldUnsubAttach := c.unsubAttach
	c.unsubAttach = nil
	oldUnsubView := c.unsubView
	c.unsubView = nil
	sessionOpen := handle.sessionOpen
	if sessionOpen == nil {
		sessionOpen = handle.entry.Session.Open(&chord.RemoteServiceSourceOpenOptions{
			Services: []chord.ServiceRef{
				{Id: services.AgentControllerService.Id},
				{Id: services.TranscriptService.Id},
				{Id: services.ModelsService.Id},
			},
			AssertAccess: func() {},
			OnError:      func(error) {},
		})
		handle.sessionOpen = sessionOpen
	}
	c.mu.Unlock()

	if oldUnsubConn != nil {
		oldUnsubConn()
	}
	if oldUnsubAttach != nil {
		oldUnsubAttach()
	}
	if oldUnsubView != nil {
		oldUnsubView()
	}

	agent := services.AsAgentController(sessionOpen.Use(services.AgentControllerService))
	transcript := services.AsTranscript(sessionOpen.Use(services.TranscriptService))
	models := services.AsModels(sessionOpen.Use(services.ModelsService))

	if err := sessionOpen.Ready(ctx); err != nil {
		return err
	}

	var initView *durable.ConversationView
	if v, ok := transcript.State().Value(); ok {
		initView = &v
	}

	c.mu.Lock()
	c.activeHandle = handle
	c.activeSessionId = sessionId
	c.agent = agent
	c.transcript = transcript
	c.models = models
	c.laneAttached = true
	c.busy = false
	c.statusText = ""
	c.footerText = ctFormatFooter(initView)
	c.ctSyncChromeLocked()
	c.selectorContainer = nil
	c.selectorList = nil
	chat := c.chat
	slash := c.slashCommands
	c.mu.Unlock()

	if chat == nil {
		created, err := CreateClientChat(ClientChatOptions{
			UI:            c.UI,
			Keybindings:   c.keybindings,
			Settings:      c.settings,
			Agent:         agent,
			Transcript:    transcript,
			SlashCommands: slash,
			AgentDir:      c.agentDir,
			Cwd:           c.cwd,
			RequestRender: c.requestRender,
			OnExit:        c.Finish,
			OnOpenSessions: func() {
				c.ctShowSessionSelector()
			},
		})
		if err != nil {
			return err
		}
		c.mu.Lock()
		if c.disposed {
			c.mu.Unlock()
			created.Dispose()
			return nil
		}
		c.chat = created
		chat = created
		c.mu.Unlock()
	} else {
		chat.BindSession(agent, transcript)
	}

	unsubView := transcript.State().Subscribe(func(view durable.ConversationView, _ chord.Context, _ chord.ReplicatedStateDelivery) error {
		c.ctUpdateFooterFromView(&view)
		return nil
	})
	var unsubConn, unsubAttach func()
	if handle.entry.Radius {
		serverId := handle.entry.ServerId
		unsubConn = handle.entry.Server.Connection().Subscribe(func(state services.ServerConnectionState, _ chord.Context, _ chord.ReplicatedStateDelivery) error {
			c.ctHandleConnectionState(serverId, state)
			return nil
		})
		unsubAttach = handle.entry.Session.Attachment().Subscribe(func(state services.SessionAttachmentState, _ chord.Context, _ chord.ReplicatedStateDelivery) error {
			c.ctHandleAttachmentState(serverId, sessionId, state)
			return nil
		})
	}

	c.mu.Lock()
	disposedNow := c.disposed || c.activeHandle != handle || c.activeSessionId != sessionId
	if !disposedNow {
		c.unsubView = unsubView
		c.unsubConn = unsubConn
		c.unsubAttach = unsubAttach
	}
	c.mu.Unlock()
	if disposedNow {
		if unsubView != nil {
			unsubView()
		}
		if unsubConn != nil {
			unsubConn()
		}
		if unsubAttach != nil {
			unsubAttach()
		}
		return nil
	}

	c.UI.SetFocus(chat.Editor)
	c.requestRender()
	return nil
}

func (c *ClientTui) ctEnsureHostAndReload(handle *ctServerHandles, presData chord.JsonValue) error {
	loadedPres, err := c.ctLoadPresentationData(presData)
	if err != nil {
		return err
	}

	c.mu.Lock()
	existingHost := c.host
	existingHandle := c.activeHandle
	oldPresDispose := c.presDispose
	c.mu.Unlock()

	if existingHost != nil && existingHandle == handle {
		if err := existingHost.Reload(loadedPres.Facets); err != nil {
			if loadedPres.Dispose != nil {
				_ = loadedPres.Dispose()
			}
			return err
		}
		c.mu.Lock()
		c.presDispose = loadedPres.Dispose
		c.mu.Unlock()
		if oldPresDispose != nil {
			_ = oldPresDispose()
		}
		return nil
	}

	if existingHost != nil {
		_ = existingHost.Dispose()
	}
	if oldPresDispose != nil {
		_ = oldPresDispose()
	}

	var capturedSlash services.SlashCommands
	baseFacets := []chord.Facet{
		services.CreateSlashCommandsRuntimeFacet(),
		chord.DefineFacet(chord.Facet{
			Id: "@pi/presentation-ui",
			Setup: func(env chord.FacetEnvironment) error {
				env.Provide(services.PresentationUIService, services.ProvidePresentationUI(&ctPresentationUI{tui: c}))
				cmd := services.AsSlashCommands(env.Use(services.SlashCommandsService))
				capturedSlash = cmd
				env.OnActivate(func() error {
					c.mu.Lock()
					c.slashCommands = cmd
					c.mu.Unlock()
					return nil
				})
				return nil
			},
		}),
		services.CreateBuiltInSlashCommandsFacet(services.BuiltInSlashCommandsFacetOptions{
			ReloadPresentationPlugins: c.ctReloadPresentationData,
		}),
	}

	allFacets := append(append([]chord.Facet(nil), baseFacets...), loadedPres.Facets...)
	host, err := chord.CreateFacetHost(&chord.FacetOptions{
		Facets: allFacets,
		ServiceSources: []chord.RemoteServiceSource{
			handle.entry.Server,
			handle.entry.Session,
		},
	})
	if err != nil {
		if loadedPres.Dispose != nil {
			_ = loadedPres.Dispose()
		}
		return err
	}

	slash := capturedSlash

	c.mu.Lock()
	c.baseFacets = baseFacets
	c.host = host
	c.presDispose = loadedPres.Dispose
	c.slashCommands = slash
	c.mu.Unlock()
	return nil
}

func (c *ClientTui) ctReloadPresentationData(data chord.JsonValue) error {
	loadedPres, err := c.ctLoadPresentationData(data)
	if err != nil {
		return err
	}

	c.mu.Lock()
	host := c.host
	oldDispose := c.presDispose
	c.mu.Unlock()

	if host == nil {
		if loadedPres.Dispose != nil {
			_ = loadedPres.Dispose()
		}
		return errors.New("Presentation facet host is not initialized")
	}

	if err := host.Reload(loadedPres.Facets); err != nil {
		if loadedPres.Dispose != nil {
			_ = loadedPres.Dispose()
		}
		return err
	}

	c.mu.Lock()
	c.presDispose = loadedPres.Dispose
	c.mu.Unlock()
	if oldDispose != nil {
		_ = oldDispose()
	}
	return nil
}

func (c *ClientTui) ctLoadPresentationData(data chord.JsonValue) (*chord.LoadedFacets, error) {
	if data == nil {
		return &chord.LoadedFacets{
			Facets:  nil,
			Dispose: func() error { return nil },
		}, nil
	}
	loaders, err := c.loadPresentationFacets(data)
	if err != nil {
		return nil, err
	}
	if len(loaders) == 0 {
		return &chord.LoadedFacets{
			Facets:  nil,
			Dispose: func() error { return nil },
		}, nil
	}
	return chord.CombineFacetLoaders(loaders).Load()
}

func (c *ClientTui) ctRunSelect(title string, items []services.PresentationSelectItem, selectedValue *string, ctx chord.Context) (*string, error) {
	if ctx == nil {
		ctx = c.cancelCtx.Context
	}
	tuiItems := make([]tui.SelectItem, 0, len(items))
	selectedIdx := 0
	for i, item := range items {
		var descPtr *string
		if item.Description != "" {
			d := item.Description
			descPtr = &d
		}
		tuiItems = append(tuiItems, tui.SelectItem{
			Value:       item.Value,
			Label:       item.Label,
			Description: descPtr,
		})
		if selectedValue != nil && item.Value == *selectedValue {
			selectedIdx = i
		}
	}

	maxVis := len(tuiItems) + 2
	if maxVis < 3 {
		maxVis = 3
	}
	if maxVis > 12 {
		maxVis = 12
	}
	list := tui.NewSelectList(tuiItems, maxVis, theme.GetSelectListTheme(), nil)
	list.SetSelectedIndex(selectedIdx)

	type selectResult struct {
		value *string
	}
	resCh := make(chan selectResult, 1)
	var once sync.Once
	finishSelect := func(val *string) {
		once.Do(func() {
			resCh <- selectResult{value: val}
		})
	}

	list.OnSelect = func(chosen tui.SelectItem) {
		v := chosen.Value
		finishSelect(&v)
	}
	list.OnCancel = func() {
		finishSelect(nil)
	}

	th := theme.CurrentTheme()
	header := title
	if th != nil {
		header = th.Bold(th.Fg(theme.FgAccent, title))
	}
	overlay := tui.NewContainer()
	overlay.AddChild(components.NewDynamicBorder())
	overlay.AddChild(tui.NewText(header, 1, 0, nil))
	overlay.AddChild(list)
	overlay.AddChild(components.NewDynamicBorder())

	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return nil, context.Canceled
	}
	c.overlayContainer = overlay
	c.overlaySelectList = list
	c.cancelPendingSel = func() { finishSelect(nil) }
	c.mu.Unlock()

	c.UI.SetFocus(list)
	c.requestRender()

	var result *string
	var err error
	select {
	case res := <-resCh:
		result = res.value
	case <-ctx.AbortSignal().Done():
		finishSelect(nil)
		err = context.Canceled
	case <-c.closedCh:
		finishSelect(nil)
		err = context.Canceled
	}

	c.mu.Lock()
	if c.overlaySelectList == list {
		c.overlayContainer = nil
		c.overlaySelectList = nil
		c.cancelPendingSel = nil
	}
	focus := c.ctFocusLocked()
	disposed := c.disposed
	c.mu.Unlock()

	if !disposed {
		if focus != nil {
			c.UI.SetFocus(focus)
		}
		c.requestRender()
	}
	return result, err
}

func (c *ClientTui) ctFocusLocked() tui.Component {
	switch {
	case c.overlaySelectList != nil:
		return c.overlaySelectList
	case c.selectorList != nil:
		return c.selectorList
	case c.chat != nil:
		return c.chat.Editor
	default:
		return nil
	}
}

func ctBoolPtr(v bool) *bool {
	return &v
}

func ctStyleDim(text string) string {
	if text == "" {
		return ""
	}
	if th := theme.CurrentTheme(); th != nil {
		return th.Fg(theme.FgDim, text)
	}
	return text
}

func ctFormatFooter(view *durable.ConversationView) string {
	if view == nil {
		return "/model · /thinking · /compact · /reload"
	}
	var agent durable.AgentState
	if raw, ok := view.Docs[durable.AgentDoc.Definition.Kind]; ok && raw != nil {
		_ = jsonx.Decode(raw, &agent)
	}
	model := "no model"
	if agent.Model != nil && agent.Model.Provider != "" && agent.Model.ModelId != "" {
		model = fmt.Sprintf("%s/%s", agent.Model.Provider, agent.Model.ModelId)
	}
	thinking := "off"
	if agent.ThinkingLevel != nil && *agent.ThinkingLevel != "" {
		thinking = string(*agent.ThinkingLevel)
	}
	return fmt.Sprintf("%s · thinking:%s · %d entries · /model · /thinking · /compact · /reload", model, thinking, len(view.Entries))
}

func (c *ClientTui) ctSyncChromeLocked() {
	if c.headingComp != nil {
		if c.activeHandle != nil && c.activeSessionId != "" && c.laneAttached {
			heading := fmt.Sprintf("Server: %s\nSession: %s", c.activeHandle.entry.ServerId, c.activeSessionId)
			c.headingComp.SetText(ctStyleDim(heading))
		} else {
			c.headingComp.SetText("")
		}
	}
	if c.statusComp != nil {
		c.statusComp.SetText(ctStyleDim(c.statusText))
	}
	if c.footerComp != nil {
		c.footerComp.SetText(ctStyleDim(c.footerText))
	}
}

func (c *ClientTui) ctSetStatus(message string) {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return
	}
	c.statusText = message
	c.ctSyncChromeLocked()
	c.mu.Unlock()
	c.requestRender()
}

func (c *ClientTui) ctUpdateFooterFromView(view *durable.ConversationView) {
	nextFooter := ctFormatFooter(view)
	c.mu.Lock()
	if c.disposed || nextFooter == c.footerText {
		c.mu.Unlock()
		return
	}
	c.footerText = nextFooter
	c.ctSyncChromeLocked()
	c.mu.Unlock()
	c.requestRender()
}

func (c *ClientTui) ctHandleConnectionState(serverId protocol.ServerId, state services.ServerConnectionState) {
	c.mu.Lock()
	if c.disposed || c.activeHandle == nil || c.activeHandle.entry.ServerId != serverId {
		c.mu.Unlock()
		return
	}
	var setFocused *bool
	switch state.Status {
	case "connected":
		if c.laneAttached {
			c.mu.Unlock()
			return
		}
		c.busy = true
		c.statusText = "Reattaching Session…"
		setFocused = ctBoolPtr(false)
	case "connecting":
		c.busy = true
		c.laneAttached = false
		c.statusText = "Reconnecting to Radius…"
		setFocused = ctBoolPtr(false)
	default:
		c.busy = true
		c.laneAttached = false
		c.statusText = "Radius disconnected; retrying…"
		setFocused = ctBoolPtr(false)
	}
	c.ctSyncChromeLocked()
	chat := c.chat
	c.mu.Unlock()

	if setFocused != nil && chat != nil {
		chat.Editor.SetFocused(*setFocused)
	}
	c.requestRender()
}

func (c *ClientTui) ctHandleAttachmentState(serverId protocol.ServerId, sessionId string, state services.SessionAttachmentState) {
	c.mu.Lock()
	if c.disposed || c.activeHandle == nil || c.activeHandle.entry.ServerId != serverId || c.activeSessionId != sessionId {
		c.mu.Unlock()
		return
	}
	var setFocused *bool
	switch state.Status {
	case "attached":
		if state.SessionId != sessionId {
			c.mu.Unlock()
			return
		}
		if !c.busy && c.laneAttached {
			c.mu.Unlock()
			return
		}
		c.busy = false
		c.laneAttached = true
		c.statusText = ""
		if c.overlaySelectList == nil && c.selectorList == nil {
			setFocused = ctBoolPtr(true)
		}
	case "attaching":
		if state.SessionId != sessionId {
			c.mu.Unlock()
			return
		}
		c.busy = true
		c.statusText = "Reattaching Session…"
		setFocused = ctBoolPtr(false)
	default:
		c.mu.Unlock()
		return
	}
	c.ctSyncChromeLocked()
	chat := c.chat
	c.mu.Unlock()

	if setFocused != nil && chat != nil {
		chat.Editor.SetFocused(*setFocused)
	}
	c.requestRender()
}
