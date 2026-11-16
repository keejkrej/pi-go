package gui

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/gui"
)

type wakuSettingsState struct {
	mu             sync.Mutex
	open           bool
	page           string
	search         string
	openDropdownID string
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
			id:    "behavior",
			title: "Behavior",
			icon:  gui.IconSparkle,
			cards: []wakuSettingsCard{
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
		Background(th.Background)

	root.Children(func() {
		// Left Settings Navigation Sidebar (252px, matching Waku settings.rs)
		ui.Column(c).
			Key("waku-settings-sidebar").
			Width(wakuSidebarDefaultWidth).
			Shrink(0).
			FillHeight().
			Background(sidebarBg).
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

				// Search Settings field
				ui.Box(c).FillWidth().Padding(8, 12, 0, 12).Children(func() {
					searchShell := ui.Row(c).
						FillWidth().
						Height(28).
						Padding(0, 8, 0, 8).
						Radius(6).
						Background(th.Inset).
						Border(1, th.BorderStrong).
						AlignItems(ui.Center).
						Gap(6)
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
							if k.Key == ui.KeyEscape && k.Mods == 0 {
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

		ui.Box(c).Width(1).Shrink(0).FillHeight().Background(th.SidebarBorder)

		// Right Settings Content Column
		ui.Column(c).
			Key("waku-settings-content").
			Grow(1).
			Shrink(1).
			MinWidth(0).
			FillHeight().
			Background(th.Surface).
			Children(func() {
				ui.Box(c).
					Key("waku-settings-content-titlebar").
					FillWidth().
					Height(wakuHeaderHeight(c)).
					DragWindow()

				scroll := ui.Scroll(c).
					FillWidth().
					Grow(1).
					Clip().
					Padding(0, 32, 48, 32)
				scroll.Children(func() {
					ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
						inner := ui.Column(c).FillWidth().MaxWidth(760).Gap(15)
						inner.Children(func() {
							if !hasActivePage {
								ui.Text(c, "Settings").TextColor(th.Text).FontSize(18).FontWeight(500)
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

							ui.Text(c, activePage.title).TextColor(th.Text).FontSize(18).FontWeight(500)

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
			})
	})

	if (m.host == nil || !m.host.HasOverlay()) && openDropdownID == "" && !dropdownChanged {
		if c.Shortcut(0, ui.KeyEscape) {
			closeSettings = true
		}
	}

	if selectPageID != "" || dropdownChanged {
		st.mu.Lock()
		if selectPageID != "" {
			st.page = selectPageID
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
