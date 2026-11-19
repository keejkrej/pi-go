package gui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
)

type spSkillEntry struct {
	key             string
	name            string
	description     string
	filePath        string
	baseDir         string
	scope           utils.SourceScope
	scopeLabel      string
	enabled         bool
	canToggle       bool
	supportingFiles int
	totalBytes      int64
	modifiedAt      time.Time
	body            string
}

type wakuSettingsState struct {
	mu             sync.Mutex
	open           bool
	page           string
	search         string
	openDropdownID string

	skillsLoaded       bool
	skills             []spSkillEntry
	skillsSearch       string
	skillsSourceFilter string
	skillsSelectedKey  string
	skillsCopiedKey    string
	skillsCopiedAt     time.Time
	skillsMdView       *gui.MarkdownView
	skillsMdKey        string
}

func newWakuSettingsState() *wakuSettingsState {
	return &wakuSettingsState{
		page: "general",
	}
}

type wakuSettingsCardKind uint8

const (
	wakuSettingInfo wakuSettingsCardKind = iota
	wakuSettingToggle
	wakuSettingDropdown
	wakuSettingModelPicker
)

type wakuSettingsCard struct {
	id          string
	title       string
	description string
	kind        wakuSettingsCardKind
	boolVal     bool
	curValue    string
	options     []string
}

type wakuSettingsPageDef struct {
	id    string
	title string
	icon  *ui.SVG
	cards []wakuSettingsCard
}

// OpenSettingsPage switches the GUI into Waku's full-window two-pane Settings view.
func (m *GuiMode) OpenSettingsPage(page string) {
	if m == nil {
		return
	}
	m.ensureWakuState()
	m.settingsState.mu.Lock()
	m.settingsState.open = true
	if strings.TrimSpace(page) != "" {
		m.settingsState.page = strings.TrimSpace(page)
	} else if m.settingsState.page == "" {
		m.settingsState.page = "general"
	}
	m.settingsState.openDropdownID = ""
	m.settingsState.skillsLoaded = false
	m.settingsState.mu.Unlock()
	if m.host != nil {
		m.host.RequestRender()
	}
}

// CloseSettingsPage closes the full-window Settings view and returns to the workspace.
func (m *GuiMode) CloseSettingsPage() {
	if m == nil {
		return
	}
	m.ensureWakuState()
	m.settingsState.mu.Lock()
	m.settingsState.open = false
	m.settingsState.openDropdownID = ""
	m.settingsState.skillsLoaded = false
	m.settingsState.mu.Unlock()
	if m.host != nil {
		m.host.RequestRender()
	}
}

// SettingsOpen reports whether the full-window Waku Settings view is active.
func (m *GuiMode) SettingsOpen() bool {
	if m == nil || m.settingsState == nil {
		return false
	}
	m.settingsState.mu.Lock()
	open := m.settingsState.open
	m.settingsState.mu.Unlock()
	return open
}

func (m *GuiMode) applyInteractiveSettingChange(id, value string) {
	if m == nil {
		return
	}
	sess := m.Session()
	var sm *core.SettingsManager
	if sess != nil {
		sm = sess.SettingsManager
	}
	ApplySettingsChange(sm, sess, id, value)

	if id == "theme" && m.host != nil {
		isDark := m.currentGuiTheme().Dark
		if !m.opts.SkipWindow && mygo.App.IsReady() {
			isDark = mygo.Theme.IsDark()
		}
		target := ResolveThemeForSetting(value, isDark)
		if target == "" {
			target = value
		}
		_, ts := ApplyThemeToHost(m.host, target)
		if ts != nil {
			m.applyThemeSet(ts)
		}
	}
	if id == "hideThinkingBlock" && sm != nil {
		hide := sm.GetHideThinkingBlock()
		m.mu.Lock()
		m.hideThinking = hide
		entries := append([]gmTranscriptEntry(nil), m.entries...)
		m.mu.Unlock()
		for _, e := range entries {
			if ac, ok := e.card.(*AssistantMessageCard); ok {
				ac.SetHideThinkingBlock(hide)
			}
		}
	}
	m.updateFooterAndTitle()
	if m.host != nil {
		m.host.RequestRender()
	}
}

func (m *GuiMode) showDefaultModelPicker() {
	sess := m.Session()
	if m == nil || m.host == nil || sess == nil || sess.ModelRegistry() == nil {
		return
	}
	models := sess.ModelRegistry().GetAvailable()
	var items []gui.SelectItem
	selectedIdx := 0
	var curKey string
	if sm := sess.SettingsManager; sm != nil {
		var prov, mod string
		if p := sm.GetDefaultProvider(); p != nil {
			prov = *p
		}
		if dm := sm.GetDefaultModel(); dm != nil {
			mod = *dm
		}
		if prov != "" && mod != "" {
			curKey = selModelKey(prov, mod)
		} else {
			curKey = mod
		}
	}
	for i, mod := range models {
		if mod == nil {
			continue
		}
		key := selModelKey(string(mod.Provider), mod.Id)
		badge := string(mod.Provider)
		if key == curKey {
			badge = "active"
			selectedIdx = i
		}
		lbl := mod.Name
		if lbl == "" {
			lbl = mod.Id
		}
		items = append(items, gui.SelectItem{
			Value:       key,
			Label:       lbl,
			Description: key,
			Badge:       badge,
		})
	}

	th := m.currentGuiTheme()
	sl := gui.NewSelectList(items)
	sl.SetSearchable(true)
	sl.SetAutoFocus(true)
	sl.SetConfirmOnClick(true)
	sl.SetPlaceholder("Search default model...")
	sl.SetMaxHeight(340)
	sl.SetSelectedIndex(selectedIdx)
	if th != nil {
		sl.SetTheme(th)
	}

	var dh *DialogHandle
	sl.SetOnSelect(func(it gui.SelectItem) {
		if dh != nil {
			dh.Close()
		}
		m.applyInteractiveSettingChange("defaultModel", it.Value)
	})
	sl.SetOnCancel(func() {
		if dh != nil {
			dh.Close()
		}
	})

	dh = selShowOverlay(m.host, gui.OverlayOptions{
		Width:            640,
		Height:           460,
		MaxWidthPercent:  92,
		MaxHeightPercent: 84,
	}, nil, func(c *ui.Context, confirmDone func(), cancel func()) {
		var closeClicked bool
		curTh := gui.ResolveGuiTheme(c, th)
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			selRenderDialogHeader(c, curTh, "Default Model", "Choose the default provider/model for new sessions", func() {
				ui.Badge(c, fmt.Sprintf("%d models", len(items)))
			})
			sl.View(c)
			selRenderDialogFooter(c, curTh, "Enter to select · Esc to cancel", func() {
				if ui.Button(c, "Cancel").Clicked() {
					closeClicked = true
				}
			})
		})
		if closeClicked {
			cancel()
		}
	})
	m.setActiveDialog(dh)
}

func (m *GuiMode) buildWakuSettingsPages() []wakuSettingsPageDef {
	sess := m.Session()
	var sm *core.SettingsManager
	if sess != nil {
		sm = sess.SettingsManager
	}

	autoCompact := true
	autoRetry := true
	steering := string(agent.QueueModeOneAtATime)
	followUp := string(agent.QueueModeOneAtATime)
	transport := string(core.TransportAuto)
	cacheWarming := string(core.CacheWarmingOff)
	hideThinking := false
	showCacheMiss := false
	showImages := true
	autoResize := true
	blockImages := false
	collapseChangelog := false
	doubleEscape := string(core.DoubleEscapeActionTree)
	treeFilter := string(core.TreeFilterModeDefault)
	mermaidMode := string(core.MermaidRenderingFinal)
	projectTrust := string(core.DefaultProjectTrustAsk)
	quietStartup := "false"
	thinkingVal := string(core.DefaultThinkingLevel)
	themeVal := "system"
	defaultModelVal := "(none)"

	if sm != nil {
		autoCompact = sm.GetCompactionEnabled()
		autoRetry = sm.GetRetryEnabled()
		steering = string(sm.GetSteeringMode())
		followUp = string(sm.GetFollowUpMode())
		transport = string(sm.GetTransport())
		cacheWarming = string(sm.GetCacheWarmingMode())
		hideThinking = sm.GetHideThinkingBlock()
		showCacheMiss = sm.GetShowCacheMissNotices()
		showImages = sm.GetShowImages()
		autoResize = sm.GetImageAutoResize()
		blockImages = sm.GetBlockImages()
		collapseChangelog = sm.GetCollapseChangelog()
		doubleEscape = string(sm.GetDoubleEscapeAction())
		treeFilter = string(sm.GetTreeFilterMode())
		mermaidMode = string(sm.GetMermaidRenderingMode())
		projectTrust = string(sm.GetDefaultProjectTrust())
		quietStartup = selQuietStartupString(sm.GetQuietStartup())
		if t := sm.GetThemeSetting(); t != nil && *t != "" {
			themeVal = *t
		} else if t := sm.GetTheme(); t != nil && *t != "" {
			themeVal = *t
		}
		if tl := sm.GetDefaultThinkingLevel(); tl != nil && *tl != "" {
			thinkingVal = string(*tl)
		}
		prov := ""
		if p := sm.GetDefaultProvider(); p != nil {
			prov = *p
		}
		mod := ""
		if dm := sm.GetDefaultModel(); dm != nil {
			mod = *dm
		}
		if prov != "" && mod != "" {
			defaultModelVal = selModelKey(prov, mod)
		} else if mod != "" {
			defaultModelVal = mod
		}
	}
	if sess != nil {
		autoCompact = sess.AutoCompactionEnabled()
		autoRetry = sess.AutoRetryEnabled()
		if tl := sess.ThinkingLevel(); tl != "" {
			thinkingVal = string(tl)
		}
	}

	themeOpts := []string{"system", "dark", "light"}
	for _, ti := range theme.GetAvailableThemesWithPaths() {
		if ti.Name != "" && !slices.Contains(themeOpts, ti.Name) {
			themeOpts = append(themeOpts, ti.Name)
		}
	}

	thinkingLevels := core.ThinkingLevelOptions
	if sess != nil {
		if avail := sess.GetAvailableThinkingLevels(); len(avail) > 0 {
			thinkingLevels = avail
		}
	}
	thinkingOpts := make([]string, 0, len(thinkingLevels))
	for _, lvl := range thinkingLevels {
		thinkingOpts = append(thinkingOpts, string(lvl))
	}

	return []wakuSettingsPageDef{
		{
			id:    "general",
			title: "General",
			icon:  gui.IconSettings,
			cards: []wakuSettingsCard{
				{
					id:          "localByDefault",
					title:       "Local by default",
					description: "Projects, conversations, and settings are stored on this computer",
					kind:        wakuSettingInfo,
				},
				{
					id:          "autocompact",
					title:       "Auto-Compact",
					description: "Automatically compact context near the window limit",
					kind:        wakuSettingToggle,
					boolVal:     autoCompact,
				},
				{
					id:          "autoRetry",
					title:       "Auto-Retry",
					description: "Automatically retry transient provider and rate-limit errors",
					kind:        wakuSettingToggle,
					boolVal:     autoRetry,
				},
				{
					id:          "defaultProjectTrust",
					title:       "Default Project Trust",
					description: "Project configuration trust prompt policy",
					kind:        wakuSettingDropdown,
					curValue:    projectTrust,
					options:     []string{string(core.DefaultProjectTrustAsk), string(core.DefaultProjectTrustAlways), string(core.DefaultProjectTrustNever)},
				},
				{
					id:          "steeringMode",
					title:       "Steering Mode",
					description: "How queued steering messages are delivered during runs",
					kind:        wakuSettingDropdown,
					curValue:    steering,
					options:     []string{string(agent.QueueModeOneAtATime), string(agent.QueueModeAll)},
				},
				{
					id:          "followUpMode",
					title:       "Follow-up Mode",
					description: "How queued follow-up messages are delivered after a turn",
					kind:        wakuSettingDropdown,
					curValue:    followUp,
					options:     []string{string(agent.QueueModeOneAtATime), string(agent.QueueModeAll)},
				},
				{
					id:          "doubleEscapeAction",
					title:       "Double-Escape Action",
					description: "Action when pressing Escape twice in an empty composer",
					kind:        wakuSettingDropdown,
					curValue:    doubleEscape,
					options:     []string{string(core.DoubleEscapeActionTree), string(core.DoubleEscapeActionFork), string(core.DoubleEscapeActionNone)},
				},
			},
		},
		{
			id:    "appearance",
			title: "Appearance",
			icon:  gui.IconAppearance,
			cards: []wakuSettingsCard{
				{
					id:          "theme",
					title:       "Theme",
					description: "Color palette for window surfaces, syntax, and controls",
					kind:        wakuSettingDropdown,
					curValue:    themeVal,
					options:     themeOpts,
				},
				{
					id:          "hideThinkingBlock",
					title:       "Hide Thinking Blocks",
					description: "Collapse assistant reasoning blocks in the transcript by default",
					kind:        wakuSettingToggle,
					boolVal:     hideThinking,
				},
				{
					id:          "mermaidRenderingMode",
					title:       "Mermaid Diagrams",
					description: "When to render fenced Mermaid diagrams in Markdown responses",
					kind:        wakuSettingDropdown,
					curValue:    mermaidMode,
					options:     []string{string(core.MermaidRenderingOff), string(core.MermaidRenderingFinal), string(core.MermaidRenderingStreaming)},
				},
				{
					id:          "showImages",
					title:       "Show Images",
					description: "Render image attachments inline in transcript and tool views",
					kind:        wakuSettingToggle,
					boolVal:     showImages,
				},
				{
					id:          "imageAutoResize",
					title:       "Auto-Resize Images",
					description: "Downscale oversized images automatically before upload",
					kind:        wakuSettingToggle,
					boolVal:     autoResize,
				},
				{
					id:          "quietStartup",
					title:       "Quiet Startup",
					description: "Suppress welcome chrome on launch",
					kind:        wakuSettingDropdown,
					curValue:    quietStartup,
					options:     []string{"false", "true", "header"},
				},
				{
					id:          "collapseChangelog",
					title:       "Collapse Changelog",
					description: "Show condensed changelog summary on updates",
					kind:        wakuSettingToggle,
					boolVal:     collapseChangelog,
				},
				{
					id:          "treeFilterMode",
					title:       "Default Tree Filter",
					description: "Initial filter when opening the session tree",
					kind:        wakuSettingDropdown,
					curValue:    treeFilter,
					options:     []string{string(core.TreeFilterModeDefault), string(core.TreeFilterModeNoTools), string(core.TreeFilterModeUserOnly), string(core.TreeFilterModeLabeledOnly), string(core.TreeFilterModeAll)},
				},
			},
		},
		{
			id:    "providers",
			title: "Providers",
			icon:  gui.IconPiLogo,
			cards: []wakuSettingsCard{
				{
					id:          "defaultModel",
					title:       "Default Model",
					description: "Default provider and model used when starting new tasks",
					kind:        wakuSettingModelPicker,
					curValue:    defaultModelVal,
				},
				{
					id:          "thinkingLevel",
					title:       "Thinking Level",
					description: "Default reasoning budget for reasoning-capable models",
					kind:        wakuSettingDropdown,
					curValue:    thinkingVal,
					options:     thinkingOpts,
				},
				{
					id:          "transport",
					title:       "Transport",
					description: "Preferred API wire protocol for streaming responses",
					kind:        wakuSettingDropdown,
					curValue:    transport,
					options:     []string{string(core.TransportAuto), string(core.TransportSse), string(core.TransportWebsocket), string(core.TransportWebsocketCached)},
				},
				{
					id:          "cacheWarming",
					title:       "Cache Warming",
					description: "Prompt cache warming cadence during streaming or idle",
					kind:        wakuSettingDropdown,
					curValue:    cacheWarming,
					options:     []string{string(core.CacheWarmingOff), string(core.CacheWarmingStreaming), string(core.CacheWarmingIdle)},
				},
				{
					id:          "showCacheMissNotices",
					title:       "Show Cache Miss Notices",
					description: "Display provider prompt cache miss diagnostics",
					kind:        wakuSettingToggle,
					boolVal:     showCacheMiss,
				},
				{
					id:          "blockImages",
					title:       "Block Images",
					description: "Prevent attaching or sending image payloads to providers",
					kind:        wakuSettingToggle,
					boolVal:     blockImages,
				},
			},
		},
		{
			id:    "skills",
			title: "Skills",
			icon:  gui.IconSparkle,
			cards: []wakuSettingsCard{
				{
					id:          "skillsRegistry",
					title:       "Agent Skills",
					description: "Browse and inspect loaded Pi workspace and global skills",
					kind:        wakuSettingInfo,
				},
			},
		},
		{
			id:    "usage",
			title: "Usage",
			icon:  gui.IconInfo,
			cards: []wakuSettingsCard{
				{
					id:          "usageSummary",
					title:       "Token & Cost Usage",
					description: "Inspect token throughput, cache hits, and spend for this workspace",
					kind:        wakuSettingInfo,
				},
			},
		},
	}
}

func wakuSettingsPageMatches(p wakuSettingsPageDef, query string) bool {
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(p.title), query) {
		return true
	}
	for _, card := range p.cards {
		if strings.Contains(strings.ToLower(card.title), query) ||
			strings.Contains(strings.ToLower(card.description), query) {
			return true
		}
	}
	return false
}

func (m *GuiMode) renderWakuSettingsView(c *ui.Context, th *gui.GuiTheme) ui.Element {
	m.ensureWakuState()
	st := m.settingsState

	st.mu.Lock()
	curPageID := st.page
	openDropdownID := st.openDropdownID
	searchVal := st.search
	st.mu.Unlock()

	allPages := m.buildWakuSettingsPages()
	query := strings.ToLower(strings.TrimSpace(searchVal))
	var visiblePages []wakuSettingsPageDef
	for _, p := range allPages {
		if wakuSettingsPageMatches(p, query) {
			visiblePages = append(visiblePages, p)
		}
	}

	var activePage wakuSettingsPageDef
	hasActivePage := false
	for _, p := range visiblePages {
		if p.id == curPageID {
			activePage = p
			hasActivePage = true
			break
		}
	}
	if !hasActivePage && len(visiblePages) > 0 {
		activePage = visiblePages[0]
		hasActivePage = true
	}

	sidebarBg := th.SidebarBg
	if c != nil && c.Vibrancy() {
		sidebarBg = th.SidebarBg.Alpha(0.76)
	}

	var (
		closeSettings     bool
		selectPageID      string
		nextDropdownID    string
		dropdownChanged   bool
		pendingChangeID   string
		pendingChangeVal  string
		openDefaultPicker bool
	)

	root := ui.Row(c).
		Key("waku-settings-root").
		Fill().
		Clip().
		Background(th.Background).
		Role(ui.RoleMain).
		Label("Settings")

	root.Children(func() {
		// Left Settings Navigation Sidebar (252px, matching Waku settings.rs)
		ui.Column(c).
			Key("waku-settings-sidebar").
			Width(wakuSidebarDefaultWidth).
			Shrink(0).
			FillHeight().
			Background(sidebarBg).
			Role(ui.RoleNavigation).
			Label("Settings Sidebar").
			Children(func() {
				topH := wakuHeaderHeight(c)
				inset := wakuTrafficLightInset(c)
				ui.Row(c).
					Key("waku-settings-sidebar-titlebar").
					FillWidth().
					Height(topH).
					AlignItems(ui.Center).
					Children(func() {
						if inset > 0 {
							ui.Box(c).Width(inset).FillHeight().DragWindow()
						}
						ui.Box(c).Grow(1).FillHeight().DragWindow()
					})

				// <- Back button
				ui.Box(c).FillWidth().Padding(0, 12, 0, 12).Children(func() {
					backBtn := ui.ButtonBase(c).
						FillWidth().
						Height(34).
						Padding(0, 9, 0, 9).
						Radius(8).
						AlignItems(ui.Center).
						Justify(ui.Start).
						Gap(9).
						Cursor(ui.CursorPointer).
						Label("Back")
					if backBtn.Hovered() {
						backBtn.Background(th.Overlay)
					}
					backBtn.Children(func() {
						gui.IconView(c, gui.IconArrowLeft, 15, th.TextTertiary)
						ui.Text(c, "Back").TextColor(th.TextSecondary).FontSize(13).SingleLine()
					})
					if backBtn.Clicked() {
						closeSettings = true
					}
				})

				// Search Settings field (32px height with Up/Down page cycling)
				ui.Box(c).FillWidth().Padding(8, 12, 0, 12).Children(func() {
					searchShell := ui.Row(c).
						FillWidth().
						Height(32).
						Padding(0, 8, 0, 8).
						Radius(6).
						Background(th.Inset).
						Border(1, th.BorderStrong).
						AlignItems(ui.Center).
						Gap(6).
						Role(ui.RoleRegion).
						Label("Settings Search Field")
					searchShell.Children(func() {
						gui.IconView(c, gui.IconSearch, 13, th.TextTertiary)
						in := ui.TextInputBase(c, &st.search).
							Grow(1).
							Shrink(1).
							MinWidth(0).
							Placeholder("Search Settings").
							Label("Search Settings")
						if in.Focused() {
							searchShell.Border(1, th.Accent)
						}
						in.OnTextKey(func(k ui.TextKey) bool {
							switch {
							case (k.Key == ui.KeyDown || k.Key == ui.KeyUp) && k.Mods == 0 && len(visiblePages) > 0:
								curIdx := 0
								for i, p := range visiblePages {
									if hasActivePage && p.id == activePage.id {
										curIdx = i
										break
									}
								}
								if k.Key == ui.KeyDown {
									curIdx = (curIdx + 1) % len(visiblePages)
								} else {
									curIdx--
									if curIdx < 0 {
										curIdx = len(visiblePages) - 1
									}
								}
								selectPageID = visiblePages[curIdx].id
								return true
							case k.Key == ui.KeyEscape && k.Mods == 0:
								if strings.TrimSpace(st.search) != "" {
									st.search = ""
								} else {
									closeSettings = true
								}
								return true
							}
							return false
						})
					})
				})

				ui.Box(c).FillWidth().Height(18)

				// Category Navigation Items
				ui.Column(c).FillWidth().Padding(0, 12, 0, 12).Gap(3).Children(func() {
					for _, p := range visiblePages {
						pCopy := p
						isSel := hasActivePage && activePage.id == pCopy.id
						navBtn := ui.ButtonBase(c).
							Key("settings-nav-"+pCopy.id).
							FillWidth().
							Height(36).
							Padding(0, 11, 0, 11).
							Radius(8).
							AlignItems(ui.Center).
							Justify(ui.Start).
							Gap(10).
							Cursor(ui.CursorPointer).
							Label(pCopy.title)
						if isSel || navBtn.Hovered() {
							navBtn.Background(th.SidebarItemBg)
						}
						iconCol := th.TextTertiary
						txtCol := th.TextSecondary
						if isSel {
							iconCol = th.TextSecondary
							txtCol = th.Text
						}
						navBtn.Children(func() {
							gui.IconView(c, pCopy.icon, 15, iconCol)
							ui.Text(c, pCopy.title).TextColor(txtCol).FontSize(13).SingleLine()
						})
						if navBtn.Clicked() {
							selectPageID = pCopy.id
							nextDropdownID = ""
							dropdownChanged = true
						}
					}
				})
			})

		// Right Settings Content Column (left 1px SidebarBorder overlay keeps exact 252+1028 geometry)
		ui.Column(c).
			Key("waku-settings-content").
			Grow(1).
			Shrink(1).
			MinWidth(0).
			FillHeight().
			Background(th.Surface).
			Role(ui.RoleRegion).
			Label("Settings Content").
			Children(func() {
				ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(1).Background(th.SidebarBorder)
				ui.Box(c).
					Key("waku-settings-content-titlebar").
					FillWidth().
					Height(wakuHeaderHeight(c)).
					DragWindow()

				if hasActivePage && activePage.id == "skills" {
					ui.Column(c).
						FillWidth().
						Grow(1).
						MinHeight(0).
						Clip().
						Padding(0, 32, 48, 32).
						AlignItems(ui.Center).
						Children(func() {
							ui.Column(c).
								FillWidth().
								MaxWidth(1024).
								Grow(1).
								MinHeight(0).
								Gap(15).
								Role(ui.RoleRegion).
								Label("Settings Content Inner").
								Children(func() {
									m.renderWakuSettingsSkillsPage(c, th)
								})
						})
				} else {
					scroll := ui.Scroll(c).
						FillWidth().
						Grow(1).
						Clip().
						Padding(0, 32, 48, 32)
					scroll.Children(func() {
						ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
							maxInnerW := float32(760)
							if hasActivePage && activePage.id == "usage" {
								maxInnerW = 1024
							}
							inner := ui.Column(c).
								FillWidth().
								MaxWidth(maxInnerW).
								Gap(15).
								Role(ui.RoleRegion).
								Label("Settings Content Inner")
							inner.Children(func() {
								if !hasActivePage {
									ui.Text(c, "Settings").TextColor(th.Text).FontSize(18).FontWeight(500).Role(ui.RoleHeading)
									ui.Box(c).
										FillWidth().
										Padding(14, 20, 14, 20).
										Radius(13).
										Background(th.Raised).
										Children(func() {
											ui.Text(c, "No matching settings").TextColor(th.TextSecondary).FontSize(13)
										})
									return
								}

								ui.Text(c, activePage.title).TextColor(th.Text).FontSize(18).FontWeight(500).Role(ui.RoleHeading)

								if activePage.id == "usage" {
									m.renderWakuSettingsUsagePage(c, th)
									return
								}

								for _, card := range activePage.cards {
									cardCopy := card
									shell := ui.Box(c).
										Key("settings-card-" + cardCopy.id).
										FillWidth().
										Radius(13).
										Background(th.Raised)
									if cardCopy.kind == wakuSettingInfo {
										shell.Padding(14, 20, 14, 20).Children(func() {
											ui.Column(c).FillWidth().MinWidth(0).Gap(5).Children(func() {
												ui.Text(c, cardCopy.title).TextColor(th.Text).FontSize(13.5).FontWeight(500)
												ui.Text(c, cardCopy.description).TextColor(th.TextSecondary).FontSize(12.5)
											})
										})
										continue
									}

									shell.MinHeight(60).Padding(12, 20, 12, 20).Children(func() {
										ui.Row(c).
											FillWidth().
											MinWidth(0).
											AlignItems(ui.Center).
											Justify(ui.SpaceBetween).
											Gap(24).
											Children(func() {
												ui.Column(c).Shrink(1).Grow(1).MinWidth(0).Gap(5).Children(func() {
													ui.Text(c, cardCopy.title).TextColor(th.Text).FontSize(13.5).FontWeight(500)
													ui.Text(c, cardCopy.description).TextColor(th.TextSecondary).FontSize(12.5)
												})

												switch cardCopy.kind {
												case wakuSettingToggle:
													if gui.WakuSwitch(c, th, cardCopy.boolVal, false, "Toggle "+cardCopy.title) {
														next := "true"
														if cardCopy.boolVal {
															next = "false"
														}
														pendingChangeID = cardCopy.id
														pendingChangeVal = next
													}

												case wakuSettingModelPicker:
													chipLbl := cardCopy.curValue
													if chipLbl == "" {
														chipLbl = "(none)"
													}
													btn := ui.ButtonBase(c).
														Key("settings-picker-"+cardCopy.id).
														MinWidth(128).
														Height(29).
														Padding(0, 10, 0, 10).
														Radius(7).
														Border(1, th.BorderStrong).
														AlignItems(ui.Center).
														Justify(ui.SpaceBetween).
														Gap(8).
														Cursor(ui.CursorPointer).
														Label(cardCopy.title + ": " + chipLbl)
													if btn.Hovered() {
														btn.Background(th.Overlay)
													}
													btn.Children(func() {
														ui.Text(c, chipLbl).TextColor(th.Text).FontSize(12.5).SingleLine()
														gui.IconView(c, gui.IconChevronRight, 11, th.TextTertiary)
													})
													if btn.Clicked() {
														openDefaultPicker = true
													}

												case wakuSettingDropdown:
													popOpen := openDropdownID == cardCopy.id
													btn := ui.ButtonBase(c).
														Key("settings-dropdown-"+cardCopy.id).
														MinWidth(128).
														Height(29).
														Padding(0, 10, 0, 10).
														Radius(7).
														Border(1, th.BorderStrong).
														AlignItems(ui.Center).
														Justify(ui.SpaceBetween).
														Gap(8).
														Cursor(ui.CursorPointer).
														Label(cardCopy.title + ": " + cardCopy.curValue)
													if popOpen || btn.Hovered() {
														btn.Background(th.Overlay)
													}
													btn.Children(func() {
														ui.Text(c, cardCopy.curValue).TextColor(th.Text).FontSize(12.5).SingleLine()
														gui.IconView(c, gui.IconChevronDown, 11, th.TextTertiary)
													})
													if btn.Clicked() {
														popOpen = !popOpen
														dropdownChanged = true
														if popOpen {
															nextDropdownID = cardCopy.id
														} else {
															nextDropdownID = ""
														}
													}
													wasOpen := popOpen
													ui.PopoverBase(c, btn, &popOpen, func(panel ui.Element) {
														panel.AttachTo(btn, ui.AnchorBottomEnd, ui.AnchorTopEnd).
															Margin(4, 0, 0, 0).
															MinWidth(148).
															Background(th.Raised).
															Border(1, th.BorderStrong).
															Radius(9).
															Padding(5).
															Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.34))
														panel.Children(func() {
															ui.Scroll(c).FillWidth().MaxHeight(260).Children(func() {
																ui.Column(c).FillWidth().Gap(2).Children(func() {
																	for _, opt := range cardCopy.options {
																		optCopy := opt
																		isCur := optCopy == cardCopy.curValue
																		optBtn := ui.ButtonBase(c).
																			FillWidth().
																			Height(28).
																			Padding(0, 8, 0, 8).
																			Radius(6).
																			AlignItems(ui.Center).
																			Justify(ui.SpaceBetween).
																			Gap(8).
																			Cursor(ui.CursorPointer).
																			Label(optCopy)
																		if optBtn.Hovered() {
																			optBtn.Background(th.Overlay)
																		}
																		fg := th.TextSecondary
																		if isCur {
																			fg = th.Text
																		}
																		optBtn.Children(func() {
																			ui.Text(c, optCopy).TextColor(fg).FontSize(12.5).SingleLine()
																			if isCur {
																				gui.IconView(c, gui.IconCheck, 11, th.TextTertiary)
																			}
																		})
																		if optBtn.Clicked() {
																			pendingChangeID = cardCopy.id
																			pendingChangeVal = optCopy
																			popOpen = false
																			nextDropdownID = ""
																			dropdownChanged = true
																		}
																	}
																})
															})
														})
													})
													if wasOpen && !popOpen {
														nextDropdownID = ""
														dropdownChanged = true
													}
												}
											})
									})
								}
							})
						})
					})
				}
			})
	})

	if (m.host == nil || !m.host.HasOverlay()) && openDropdownID == "" && !dropdownChanged {
		if c.Shortcut(0, ui.KeyEscape) {
			closeSettings = true
		}
	}

	renderWakuFloatingToast(c, m, th)

	if selectPageID != "" || dropdownChanged {
		st.mu.Lock()
		if selectPageID != "" {
			st.page = selectPageID
			if selectPageID == "skills" {
				st.skillsLoaded = false
			}
		}
		if dropdownChanged {
			st.openDropdownID = nextDropdownID
		}
		st.mu.Unlock()
	}
	if pendingChangeID != "" {
		m.applyInteractiveSettingChange(pendingChangeID, pendingChangeVal)
	}
	if openDefaultPicker {
		m.showDefaultModelPicker()
	}
	if closeSettings {
		m.CloseSettingsPage()
	}

	return root
}

func (m *GuiMode) renderWakuSettingsSkillsPage(c *ui.Context, th *gui.GuiTheme) {
	st := m.settingsState
	sess := m.Session()

	st.mu.Lock()
	loaded := st.skillsLoaded
	skills := append([]spSkillEntry(nil), st.skills...)
	sourceFilter := st.skillsSourceFilter
	selectedKey := st.skillsSelectedKey
	copiedKey := st.skillsCopiedKey
	copiedAt := st.skillsCopiedAt
	mdView := st.skillsMdView
	mdKey := st.skillsMdKey
	st.mu.Unlock()

	if !loaded {
		skills = spScanSkillsCatalog(sess)
		st.mu.Lock()
		st.skills = append([]spSkillEntry(nil), skills...)
		st.skillsLoaded = true
		st.mu.Unlock()
	}

	if len(skills) == 0 {
		ui.Column(c).
			FillWidth().
			Grow(1).
			MinHeight(0).
			Center().
			Gap(10).
			Padding(40).
			Children(func() {
				ui.Box(c).
					Size(44, 44).
					Radius(11).
					Background(th.Overlay).
					Center().
					Children(func() {
						gui.IconView(c, gui.IconPackage, 21, th.TextTertiary)
					})
				ui.Text(c, "No skills yet").TextColor(th.Text).FontSize(13).FontWeight(500)
				ui.Box(c).MaxWidth(420).Children(func() {
					ui.Text(c, "Place SKILL.md modules in ~/.pi/agent/skills or .pi/skills inside your workspace to load reusable instructions and tools.").
						TextColor(th.TextSecondary).FontSize(12.5).Wrap()
				})
			})
		return
	}

	query := strings.ToLower(strings.TrimSpace(st.skillsSearch))
	visible := make([]spSkillEntry, 0, len(skills))
	disabledCount := 0
	for _, sk := range skills {
		if !sk.enabled {
			disabledCount++
		}
		if sourceFilter == "user" && sk.scope == utils.SourceScopeProject {
			continue
		}
		if sourceFilter == "project" && sk.scope != utils.SourceScopeProject {
			continue
		}
		if query != "" {
			hay := strings.ToLower(sk.name + " " + sk.description + " " + sk.scopeLabel)
			if !strings.Contains(hay, query) {
				continue
			}
		}
		visible = append(visible, sk)
	}

	var selIdx = -1
	for i, sk := range visible {
		if sk.key == selectedKey {
			selIdx = i
			break
		}
	}
	if selIdx < 0 && len(visible) > 0 {
		selIdx = 0
		selectedKey = visible[0].key
	}

	var (
		nextFilter     string
		filterChanged  bool
		nextSelected   string
		toggleKey      string
		openFilePath   string
		revealFilePath string
		copyDirPath    string
		copyDirKey     string
	)

	ui.Row(c).FillWidth().Grow(1).MinHeight(0).Gap(0).Children(func() {
		// Left master list column (Width 264, right border 1px th.Border, full height)
		ui.Column(c).
			Key("settings-skills-list-col").
			Width(264).
			Shrink(0).
			FillHeight().
			MinHeight(0).
			BorderColor(th.Border).
			BorderWidth(0, 1, 0, 0).
			Children(func() {
				// Top search input + source filter chips
				ui.Column(c).FillWidth().Padding(4, 12, 8, 0).Gap(7).Children(func() {
					searchShell := ui.Row(c).
						FillWidth().
						Height(28).
						Padding(0, 9, 0, 9).
						Radius(7).
						Background(th.Inset).
						Border(1, th.BorderStrong).
						AlignItems(ui.Center).
						Gap(6).
						Role(ui.RoleRegion).
						Label("Search skills field")
					searchShell.Children(func() {
						gui.IconView(c, gui.IconSearch, 13, th.TextTertiary)
						in := ui.TextInputBase(c, &st.skillsSearch).
							Grow(1).
							Shrink(1).
							MinWidth(0).
							FontSize(12.5).
							TextColor(th.Text).
							Placeholder("Search skills").
							Label("Search skills")
						if in.Focused() {
							searchShell.Border(1, th.Accent)
						}
						in.OnTextKey(func(k ui.TextKey) bool {
							switch {
							case (k.Key == ui.KeyDown || k.Key == ui.KeyUp) && k.Mods == 0 && len(visible) > 0:
								cur := selIdx
								if cur < 0 {
									cur = 0
								}
								if k.Key == ui.KeyDown {
									cur = (cur + 1) % len(visible)
								} else {
									cur = (cur - 1 + len(visible)) % len(visible)
								}
								nextSelected = visible[cur].key
								return true
							case k.Key == ui.KeyEscape && k.Mods == 0 && strings.TrimSpace(k.Text()) != "":
								k.SetText("")
								st.skillsSearch = ""
								return true
							}
							return false
						})
					})

					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
						for _, chip := range []struct {
							id    string
							label string
						}{
							{"", "All"},
							{"user", "User"},
							{"project", "Project"},
						} {
							chipCopy := chip
							active := sourceFilter == chipCopy.id
							cBtn := ui.ButtonBase(c).
								Key("skills-filter-"+chipCopy.label).
								Height(24).
								Padding(0, 9, 0, 9).
								Radius(6).
								AlignItems(ui.Center).
								Justify(ui.Center).
								Cursor(ui.CursorPointer).
								Selected(active).
								Label(chipCopy.label)
							fg := th.TextSecondary
							if active {
								cBtn.Background(th.OverlayStrong)
								fg = th.Text
							} else if cBtn.Hovered() {
								cBtn.Background(th.Overlay)
							}
							cBtn.Children(func() {
								txt := ui.Text(c, chipCopy.label).TextColor(fg).FontSize(12).SingleLine()
								if active {
									txt.FontWeight(500)
								}
							})
							if cBtn.Clicked() {
								nextFilter = chipCopy.id
								filterChanged = true
							}
						}
					})
				})

				// Scrollable skill rows
				ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Padding(0, 8, 8, 0).Children(func() {
					ui.Column(c).FillWidth().Gap(1).Children(func() {
						if len(visible) == 0 {
							ui.Box(c).FillWidth().Padding(16, 12, 16, 8).Children(func() {
								ui.Text(c, "No skills match your search").TextColor(th.TextTertiary).FontSize(12.5).Wrap()
							})
							return
						}

						var userCount, projCount int
						for _, sk := range visible {
							if sk.scope == utils.SourceScopeProject {
								projCount++
							} else {
								userCount++
							}
						}

						lastScope := ""
						for idx, sk := range visible {
							skCopy := sk
							if skCopy.scopeLabel != lastScope {
								lastScope = skCopy.scopeLabel
								secCount := userCount
								if skCopy.scope == utils.SourceScopeProject {
									secCount = projCount
								}
								topPad := float32(10)
								if idx == 0 {
									topPad = 6
								}
								ui.Row(c).
									FillWidth().
									Padding(topPad, 9, 4, 9).
									AlignItems(ui.Center).
									Gap(6).
									Children(func() {
										ui.Text(c, strings.ToUpper(skCopy.scopeLabel)).
											TextColor(th.TextTertiary).
											FontSize(11.5).
											FontWeight(600).
											SingleLine()
										ui.Textf(c, "%d", secCount).
											TextColor(th.TextGhost).
											FontSize(11.5).
											SingleLine()
									})
							}

							isSelected := selIdx >= 0 && visible[selIdx].key == skCopy.key
							rowBtn := ui.ButtonBase(c).
								Key(fmt.Sprintf("settings-skill-%d-%s", idx, skCopy.name)).
								FillWidth().
								Padding(7, 9, 7, 9).
								Radius(8).
								AlignItems(ui.Center).
								Justify(ui.Start).
								Gap(9).
								Cursor(ui.CursorPointer).
								Selected(isSelected).
								Label(skCopy.name)
							if isSelected {
								rowBtn.Background(th.SidebarItemBg)
							} else if rowBtn.Hovered() {
								rowBtn.Background(th.Overlay)
							}
							iconCol := th.TextSecondary
							nameCol := th.Text
							if !skCopy.enabled {
								iconCol = th.TextSecondary.Alpha(0.45)
								nameCol = th.TextSecondary
							}
							rowBtn.Children(func() {
								ui.Box(c).
									Size(26, 26).
									Shrink(0).
									Radius(6).
									Background(th.Overlay).
									Center().
									Children(func() {
										gui.IconView(c, gui.IconPackage, 13, iconCol)
									})
								ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
									ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
										ui.Text(c, skCopy.name).
											TextColor(nameCol).
											FontSize(12.5).
											FontWeight(500).
											SingleLine().
											Ellipsis("…").
											Grow(1).
											Shrink(1).
											MinWidth(0)
										if !skCopy.enabled {
											ui.Text(c, "Disabled").
												TextColor(th.Warning).
												FontSize(11.5).
												SingleLine().
												Shrink(0)
										}
									})
									sub := strings.TrimSpace(skCopy.description)
									if sub == "" {
										sub = skCopy.scopeLabel
									}
									ui.Text(c, sub).
										TextColor(th.TextTertiary).
										FontSize(12.5).
										SingleLine().
										Ellipsis("…")
								})
							})
							if rowBtn.Clicked() {
								nextSelected = skCopy.key
							}
						}
					})
				})

				// Bottom footer (Height 26, BorderTop 1px th.Border)
				footerLabel := spFormatSkillsFooter(len(visible), len(skills), disabledCount, query != "" || sourceFilter != "")
				ui.Row(c).
					FillWidth().
					Height(26).
					Shrink(0).
					Padding(0, 12, 0, 12).
					BorderColor(th.Border).
					BorderWidth(1, 0, 0, 0).
					AlignItems(ui.Center).
					Justify(ui.Center).
					Children(func() {
						ui.Text(c, footerLabel).TextColor(th.TextGhost).FontSize(12).SingleLine()
					})
			})

		// Right detail pane (Grow 1, MinWidth 0, full height, padded)
		ui.Scroll(c).
			FillHeight().
			Grow(1).
			Shrink(1).
			MinWidth(0).
			MinHeight(0).
			Padding(4, 0, 16, 24).
			Children(func() {
				if selIdx < 0 || selIdx >= len(visible) {
					ui.Column(c).Fill().Center().Gap(8).Padding(40).Children(func() {
						gui.IconView(c, gui.IconPackage, 22, th.TextGhost)
						ui.Text(c, "Select a skill").TextColor(th.TextGhost).FontSize(12.5)
					})
					return
				}

				sel := visible[selIdx]
				iconCol := th.TextSecondary
				nameCol := th.Text
				if !sel.enabled {
					iconCol = th.TextSecondary.Alpha(0.45)
					nameCol = th.TextSecondary
				}

				ui.Column(c).FillWidth().Gap(0).Children(func() {
					// Header: icon tile + skill name & subtitle + 36x20 pill toggle switch
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
						ui.Box(c).
							Size(38, 38).
							Shrink(0).
							Radius(9).
							Background(th.Overlay).
							Center().
							Children(func() {
								gui.IconView(c, gui.IconPackage, 18, iconCol)
							})

						ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
							ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
								ui.Text(c, sel.name).
									TextColor(nameCol).
									FontSize(15).
									FontWeight(500).
									SingleLine().
									Ellipsis("…").
									Shrink(1).
									MinWidth(0)
								if !sel.enabled {
									ui.Text(c, "Disabled").
										TextColor(th.Warning).
										FontSize(12.5).
										SingleLine().
										Shrink(0)
								}
							})
							ui.Text(c, spSkillSubtitle(sel)).
								TextColor(th.TextTertiary).
								FontSize(12.5).
								SingleLine().
								Ellipsis("…")
						})

						if gui.WakuSwitch(c, th, sel.enabled, !sel.canToggle, "Toggle "+sel.name) {
							toggleKey = sel.key
						}
					})

					// Description
					desc := strings.TrimSpace(sel.description)
					if desc == "" {
						desc = "No description"
					}
					ui.Box(c).FillWidth().Margin(14, 0, 0, 0).Children(func() {
						ui.Text(c, desc).TextColor(th.TextSecondary).FontSize(12.5).Wrap()
					})

					// Hairline-divided metadata rows (Invoke, Location, Contents, Updated)
					metaRows := []struct {
						label string
						value string
						mono  bool
					}{
						{label: "Invoke", value: "/skill:" + sel.name, mono: true},
						{label: "Location", value: utils.ShortenHomePath(sel.baseDir), mono: true},
						{label: "Contents", value: spFormatSkillContents(sel.supportingFiles, sel.totalBytes), mono: false},
						{label: "Updated", value: spFormatSkillUpdated(sel.modifiedAt), mono: false},
					}
					ui.Column(c).FillWidth().Margin(16, 0, 0, 0).Gap(0).Children(func() {
						for i, mr := range metaRows {
							row := ui.Row(c).
								FillWidth().
								Padding(8, 0, 8, 0).
								AlignItems(ui.Center).
								Gap(12)
							if i < len(metaRows)-1 {
								row.BorderColor(th.Border).BorderWidth(0, 0, 1, 0)
							}
							row.Children(func() {
								ui.Box(c).Width(84).Shrink(0).Children(func() {
									ui.Text(c, mr.label).TextColor(th.TextTertiary).FontSize(12.5).SingleLine()
								})
								valCol := ui.Box(c).Grow(1).Shrink(1).MinWidth(0)
								valCol.Children(func() {
									t := ui.Text(c, mr.value).TextColor(th.TextSecondary).SingleLine().Ellipsis("…")
									if mr.mono {
										t.Font("monospace").FontSize(12)
									} else {
										t.FontSize(12.5)
									}
								})
							})
						}
					})

					// Action buttons (Open SKILL.md, Reveal, Copy Path)
					copiedRecently := copiedKey == sel.key && !copiedAt.IsZero() && time.Since(copiedAt) < 2*time.Second
					ui.Row(c).FillWidth().Margin(16, 0, 0, 0).AlignItems(ui.Center).Gap(7).Children(func() {
						renderAction := func(key, label string, ic *ui.SVG, onClick func()) {
							btn := ui.ButtonBase(c).
								Key(key).
								Height(26).
								Padding(0, 10, 0, 10).
								Radius(6).
								Border(1, th.BorderStrong).
								AlignItems(ui.Center).
								Justify(ui.Center).
								Gap(5).
								Cursor(ui.CursorPointer).
								Label(label)
							if btn.Hovered() {
								btn.Background(th.Overlay)
							}
							btn.Children(func() {
								gui.IconView(c, ic, 11, th.TextTertiary)
								ui.Text(c, label).TextColor(th.TextSecondary).FontSize(12.5).SingleLine()
							})
							if btn.Clicked() {
								onClick()
							}
						}

						renderAction("skill-open-"+sel.name, "Open SKILL.md", gui.IconPencil, func() {
							openFilePath = sel.filePath
						})
						renderAction("skill-reveal-"+sel.name, "Reveal", gui.IconFolder, func() {
							revealFilePath = sel.filePath
						})
						copyLbl := "Copy Path"
						copyIc := gui.IconCopy
						if copiedRecently {
							copyLbl = "Copied"
							copyIc = gui.IconCheck
						}
						renderAction("skill-copy-"+sel.name, copyLbl, copyIc, func() {
							copyDirPath = sel.baseDir
							copyDirKey = sel.key
						})
					})

					// Skill documentation preview
					if strings.TrimSpace(sel.body) != "" {
						if mdView == nil {
							mdView = gui.NewMarkdownView(sel.body, gui.MarkdownConfig{
								Done:     true,
								Theme:    th,
								WrapCode: true,
								OnCopyCode: func(_, code string) {
									_ = gmWriteClipboardText(m.host, code)
								},
							})
							mdKey = sel.key
							st.mu.Lock()
							st.skillsMdView = mdView
							st.skillsMdKey = mdKey
							st.mu.Unlock()
						} else {
							if mdKey != sel.key {
								mdView.SetText(sel.body)
								mdKey = sel.key
								st.mu.Lock()
								st.skillsMdKey = mdKey
								st.mu.Unlock()
							}
							mdView.SetTheme(th)
						}

						ui.Column(c).
							FillWidth().
							Margin(18, 0, 0, 0).
							Padding(14, 0, 0, 0).
							BorderColor(th.Border).
							BorderWidth(1, 0, 0, 0).
							Gap(10).
							Children(func() {
								ui.Text(c, "SKILL.md").TextColor(th.TextGhost).Font("monospace").FontSize(12).SingleLine()
								mdView.View(c)
							})
					}
				})
			})
	})

	if filterChanged || nextSelected != "" || copyDirKey != "" {
		st.mu.Lock()
		if filterChanged {
			st.skillsSourceFilter = nextFilter
		}
		if nextSelected != "" {
			st.skillsSelectedKey = nextSelected
		}
		if copyDirKey != "" {
			st.skillsCopiedKey = copyDirKey
			st.skillsCopiedAt = time.Now()
		}
		st.mu.Unlock()
	}
	if copyDirPath != "" {
		_ = gmWriteClipboardText(m.host, copyDirPath)
		m.toast.show(wakuToastSuccess, "Copied", utils.ShortenHomePath(copyDirPath), nil)
	}
	if openFilePath != "" && !m.opts.SkipWindow {
		utils.OpenBrowser(openFilePath)
	}
	if revealFilePath != "" && !m.opts.SkipWindow {
		spRevealSkillPath(revealFilePath)
	}
	if toggleKey != "" {
		m.spHandleToggleSkill(toggleKey)
	}
}

func (m *GuiMode) spHandleToggleSkill(key string) {
	if m == nil || m.settingsState == nil {
		return
	}
	st := m.settingsState
	st.mu.Lock()
	idx := -1
	var target spSkillEntry
	for i, sk := range st.skills {
		if sk.key == key {
			idx = i
			target = sk
			break
		}
	}
	st.mu.Unlock()
	if idx < 0 || !target.canToggle {
		return
	}

	nextEnabled := !target.enabled
	updated, err := spToggleSkillFile(target, nextEnabled)
	if err != nil {
		m.toast.show(wakuToastError, "Skill Update Failed", err.Error(), nil)
		return
	}

	st.mu.Lock()
	if idx < len(st.skills) && st.skills[idx].key == key {
		st.skills[idx] = updated
	}
	st.mu.Unlock()

	if sess := m.Session(); sess != nil && sess.ResourceLoader() != nil {
		rl := sess.ResourceLoader()
		go func() {
			_ = rl.Reload(context.Background())
		}()
	}
	if m.host != nil {
		m.host.RequestRender()
	}
}

func spToggleSkillFile(entry spSkillEntry, targetEnabled bool) (spSkillEntry, error) {
	newName := "SKILL.md.disabled"
	if targetEnabled {
		newName = "SKILL.md"
	}
	newPath := filepath.Join(entry.baseDir, newName)
	if entry.filePath != newPath {
		if err := os.Rename(entry.filePath, newPath); err != nil {
			return entry, err
		}
	}
	entry.filePath = newPath
	entry.enabled = targetEnabled
	if info, err := os.Stat(newPath); err == nil {
		entry.modifiedAt = info.ModTime()
	}
	return entry, nil
}

func spScanSkillsCatalog(sess *core.AgentSession) []spSkillEntry {
	byKey := make(map[string]spSkillEntry)
	addEntry := func(entry spSkillEntry) {
		if entry.key == "" {
			return
		}
		if _, exists := byKey[entry.key]; !exists {
			byKey[entry.key] = entry
		}
	}

	if sess != nil && sess.ResourceLoader() != nil {
		loaded := sess.ResourceLoader().GetSkills()
		for _, sk := range loaded.Skills {
			scope := sk.SourceInfo.Scope
			if scope == "" {
				scope = utils.SourceScopeUser
			}
			if entry, ok := spLoadSkillFromFile(sk.FilePath, sk.Name, sk.Description, scope, true); ok {
				addEntry(entry)
			}
		}
	}

	scanRoot := func(root string, scope utils.SourceScope) {
		if strings.TrimSpace(root) == "" {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return
		}
		for _, ent := range entries {
			if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
				continue
			}
			subDir := filepath.Join(root, ent.Name())
			for _, cand := range []struct {
				name    string
				enabled bool
			}{
				{"SKILL.md", true},
				{"SKILL.md.disabled", false},
			} {
				p := filepath.Join(subDir, cand.name)
				if info, err := os.Stat(p); err == nil && !info.IsDir() {
					if entry, ok := spLoadSkillFromFile(p, "", "", scope, cand.enabled); ok {
						addEntry(entry)
					}
					break
				}
			}
		}
	}

	if agentDir := utils.GetAgentDir(); agentDir != "" {
		scanRoot(filepath.Join(agentDir, "skills"), utils.SourceScopeUser)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		scanRoot(filepath.Join(home, ".agents", "skills"), utils.SourceScopeUser)
	}
	if sess != nil && sess.Cwd() != "" {
		scanRoot(filepath.Join(sess.Cwd(), ".pi", "skills"), utils.SourceScopeProject)
		scanRoot(filepath.Join(sess.Cwd(), ".agents", "skills"), utils.SourceScopeProject)
	}

	out := make([]spSkillEntry, 0, len(byKey))
	for _, entry := range byKey {
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi := out[i].scope == utils.SourceScopeProject
		pj := out[j].scope == utils.SourceScopeProject
		if pi != pj {
			return !pi
		}
		ni := strings.ToLower(out[i].name)
		nj := strings.ToLower(out[j].name)
		if ni != nj {
			return ni < nj
		}
		return out[i].key < out[j].key
	})
	return out
}

func spLoadSkillFromFile(filePath, fallbackName, fallbackDesc string, scope utils.SourceScope, enabled bool) (spSkillEntry, bool) {
	filePath = filepath.Clean(filePath)
	baseDir := filepath.Dir(filePath)
	fileName := filepath.Base(filePath)
	canToggle := fileName == "SKILL.md" || fileName == "SKILL.md.disabled"
	key := filePath
	if canToggle {
		key = filepath.Clean(baseDir)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return spSkillEntry{}, false
	}
	parsed, parseErr := utils.ParseFrontmatter[core.SkillFrontmatter](string(data))
	name := strings.TrimSpace(fallbackName)
	desc := strings.TrimSpace(fallbackDesc)
	body := ""
	if parseErr == nil {
		if n := strings.TrimSpace(parsed.Frontmatter.Name); n != "" {
			name = n
		}
		if d := strings.TrimSpace(parsed.Frontmatter.Description); d != "" {
			desc = d
		}
		body = strings.TrimSpace(parsed.Body)
	}
	if name == "" {
		if canToggle {
			name = filepath.Base(baseDir)
		} else {
			name = strings.TrimSuffix(fileName, filepath.Ext(fileName))
		}
	}

	var modTime time.Time
	var totalBytes int64
	supporting := 0
	if info, statErr := os.Stat(filePath); statErr == nil {
		modTime = info.ModTime()
		totalBytes = info.Size()
	}
	if canToggle {
		supporting, totalBytes = spCountDirContents(baseDir, fileName, totalBytes)
	}

	scopeLabel := "User"
	if scope == utils.SourceScopeProject {
		scopeLabel = "Project"
	}

	return spSkillEntry{
		key:             key,
		name:            name,
		description:     desc,
		filePath:        filePath,
		baseDir:         baseDir,
		scope:           scope,
		scopeLabel:      scopeLabel,
		enabled:         enabled,
		canToggle:       canToggle,
		supportingFiles: supporting,
		totalBytes:      totalBytes,
		modifiedAt:      modTime,
		body:            body,
	}, true
}

func spCountDirContents(dir, mainFile string, fallbackBytes int64) (int, int64) {
	var supporting int
	var total int64
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil {
			total += info.Size()
		}
		if filepath.Dir(p) == dir && name == mainFile {
			return nil
		}
		supporting++
		return nil
	})
	if total == 0 {
		total = fallbackBytes
	}
	return supporting, total
}

func spSkillSubtitle(sk spSkillEntry) string {
	dir := sk.baseDir
	if sk.canToggle {
		dir = filepath.Dir(sk.baseDir)
	}
	return sk.scopeLabel + " · " + utils.ShortenHomePath(dir)
}

func spFormatSkillsFooter(shown, total, disabled int, filtered bool) string {
	if filtered {
		return fmt.Sprintf("%d of %d shown", shown, total)
	}
	caption := fmt.Sprintf("%d skills", total)
	if total == 1 {
		caption = "1 skill"
	}
	if disabled > 0 {
		caption += fmt.Sprintf(" · %d disabled", disabled)
	}
	return caption
}

func spFormatSkillContents(supporting int, totalBytes int64) string {
	sizeStr := spFormatBytes(totalBytes)
	if supporting == 1 {
		return "1 supporting file · " + sizeStr
	}
	if supporting > 1 {
		return fmt.Sprintf("%d supporting files · %s", supporting, sizeStr)
	}
	return sizeStr
}

func spFormatBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(b)/1024.0)
	}
	return fmt.Sprintf("%.1f MB", float64(b)/(1024.0*1024.0))
}

func spFormatSkillUpdated(mod time.Time) string {
	if mod.IsZero() {
		return "—"
	}
	elapsed := time.Since(mod)
	if elapsed < 90*time.Second {
		return "Just now"
	}
	if elapsed < time.Hour {
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	}
	if elapsed < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(elapsed.Hours()/24))
}

func spRevealSkillPath(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", target)
	case "windows":
		cmd = exec.Command("explorer", "/select,"+target)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(target))
	}
	_ = cmd.Start()
}

func (m *GuiMode) renderWakuSettingsUsagePage(c *ui.Context, th *gui.GuiTheme) {
	var st FooterState
	if m.footer != nil {
		st = m.footer.State()
	}
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Row(c).FillWidth().Gap(12).Children(func() {
			for _, card := range []struct {
				title string
				value string
				sub   string
			}{
				{title: "Session Cost", value: FormatCost(st.TotalCost), sub: "Estimated API spend"},
				{title: "Input Tokens", value: FormatTokens(st.InputTokens), sub: "Prompt & context tokens"},
				{title: "Output Tokens", value: FormatTokens(st.OutputTokens), sub: "Generated assistant tokens"},
			} {
				cc := card
				ui.Column(c).
					Grow(1).
					Shrink(1).
					MinWidth(0).
					Radius(13).
					Background(th.Raised).
					Padding(14, 18, 14, 18).
					Gap(4).
					Children(func() {
						ui.Text(c, cc.title).TextColor(th.TextSecondary).FontSize(12).FontWeight(500)
						ui.Text(c, cc.value).TextColor(th.Text).Font("monospace").FontSize(18).FontWeight(600)
						ui.Text(c, cc.sub).TextColor(th.TextTertiary).FontSize(11.5)
					})
			}
		})

		ui.Column(c).
			FillWidth().
			Radius(13).
			Background(th.Raised).
			Padding(14, 20, 14, 20).
			Gap(6).
			Children(func() {
				ui.Text(c, "Cache & Context Summary").TextColor(th.Text).FontSize(13.5).FontWeight(500)
				wakuPopoverStatRow(c, th, "Cache read", FormatTokens(st.CacheReadTokens))
				wakuPopoverStatRow(c, th, "Cache write", FormatTokens(st.CacheWriteTokens))
				wakuPopoverStatRow(c, th, "Active model", wakuModelChipLabel(m.Session(), st))
			})
	})
}
