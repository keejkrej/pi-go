package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	wakuSidebarDefaultWidth float32 = 264
	wakuSidebarMinWidth     float32 = 200
	wakuSidebarMaxWidth     float32 = 420
)

type wakuLayoutPrefs struct {
	SidebarOpen     bool     `json:"sidebarOpen"`
	SidebarWidth    float32  `json:"sidebarWidth"`
	SidebarGroupBy  string   `json:"sidebarGroupBy,omitzero"`
	RightPanelOpen  bool     `json:"rightPanelOpen"`
	RightPanelWidth float32  `json:"rightPanelWidth"`
	RightPanelTab   string   `json:"rightPanelTab,omitzero"`
	PinnedSessions  []string `json:"pinnedSessions,omitzero"`
}

func wakuLayoutPrefsPath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "pi-go", "gui-layout.json")
}

type wakuSidebarState struct {
	mu       sync.Mutex
	open     bool
	width    float32
	groupBy  string // "date" or "project"
	sessions []core.SessionInfo
	loading  bool
	pinned   map[string]bool
	navBack  []string
	navFwd   []string
	split    wakuSplitDragTracker
	loadSeq  atomic.Uint64
	persist  bool
	initDone bool
}

func newWakuSidebarState(skipPersist bool) *wakuSidebarState {
	st := &wakuSidebarState{
		open:     true,
		width:    wakuSidebarDefaultWidth,
		groupBy:  "date",
		pinned:   make(map[string]bool),
		persist:  !skipPersist,
		initDone: true,
	}
	if !skipPersist {
		st.loadPrefsLocked()
	}
	return st
}

func (s *wakuSidebarState) loadPrefsLocked() {
	data, err := os.ReadFile(wakuLayoutPrefsPath())
	if err != nil || len(data) == 0 {
		return
	}
	var prefs wakuLayoutPrefs
	if err := jsonx.Unmarshal(data, &prefs); err != nil {
		return
	}
	s.open = prefs.SidebarOpen
	if prefs.SidebarWidth >= wakuSidebarMinWidth && prefs.SidebarWidth <= wakuSidebarMaxWidth {
		s.width = prefs.SidebarWidth
	}
	if prefs.SidebarGroupBy == "project" || prefs.SidebarGroupBy == "date" {
		s.groupBy = prefs.SidebarGroupBy
	}
	for _, p := range prefs.PinnedSessions {
		if strings.TrimSpace(p) != "" {
			s.pinned[p] = true
		}
	}
}

func (m *GuiMode) loadLayoutPrefs() *wakuLayoutPrefs {
	if m == nil || m.opts.SkipWindow {
		return nil
	}
	data, err := os.ReadFile(wakuLayoutPrefsPath())
	if err != nil || len(data) == 0 {
		return nil
	}
	var prefs wakuLayoutPrefs
	if err := jsonx.Unmarshal(data, &prefs); err != nil {
		return nil
	}
	return &prefs
}

func (m *GuiMode) saveLayoutPrefs() {
	if m == nil || m.opts.SkipWindow {
		return
	}
	m.ensureWakuState()
	sb := m.sidebar
	rp := m.rightPanel

	var prefs wakuLayoutPrefs
	sb.mu.Lock()
	prefs.SidebarOpen = sb.open
	prefs.SidebarWidth = sb.width
	prefs.SidebarGroupBy = sb.groupBy
	for p, on := range sb.pinned {
		if on {
			prefs.PinnedSessions = append(prefs.PinnedSessions, p)
		}
	}
	sb.mu.Unlock()
	slices.Sort(prefs.PinnedSessions)

	rp.mu.Lock()
	prefs.RightPanelOpen = rp.open
	prefs.RightPanelWidth = rp.width
	prefs.RightPanelTab = rp.activeTab
	rp.mu.Unlock()

	raw, err := jsonx.MarshalIndent(prefs, "  ")
	if err != nil {
		return
	}
	path := wakuLayoutPrefsPath()
	go func() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return
		}
		_ = os.WriteFile(path, raw, 0o600)
	}()
}

func (m *GuiMode) toggleLeftSidebar() {
	if m == nil {
		return
	}
	m.ensureWakuState()
	m.sidebar.mu.Lock()
	m.sidebar.open = !m.sidebar.open
	opened := m.sidebar.open
	m.sidebar.mu.Unlock()
	m.saveLayoutPrefs()
	if opened {
		m.refreshSidebarSessions()
	}
	if m.host != nil {
		m.host.RequestRender()
	}
}

func (m *GuiMode) toggleRightPanel() {
	if m == nil {
		return
	}
	m.ensureWakuState()
	m.rightPanel.mu.Lock()
	m.rightPanel.open = !m.rightPanel.open
	opened := m.rightPanel.open
	tab := m.rightPanel.activeTab
	m.rightPanel.mu.Unlock()
	m.saveLayoutPrefs()
	if opened {
		m.refreshRightPanelChanges()
		if tab == "tree" {
			m.refreshRightPanelTreeSnapshot()
		}
	}
	if m.host != nil {
		m.host.RequestRender()
	}
}

func (m *GuiMode) refreshSidebarSessions() {
	if m == nil {
		return
	}
	m.ensureWakuState()
	sb := m.sidebar
	sess := m.Session()

	var curFile, curId, curCwd, curDir, curName, curFirst string
	var curCount int
	var curCreated, curModified time.Time

	if sess != nil {
		curId, curFile, curDir, curName, curCreated = m.cachedSessionMeta()
		curCwd = sess.Cwd()
		curFirst = m.firstTranscriptUserTitle(80)
		curCount = len(sess.Messages())
		curModified = time.Now()
	}

	seq := sb.loadSeq.Add(1)
	sb.mu.Lock()
	sb.loading = true
	sb.mu.Unlock()

	ctx := m.currentCtx()
	go func() {
		defaultDir := core.GetDefaultSessionDir(curCwd)
		customDir := ""
		if curDir != "" && !utils.PathsEqual(curDir, defaultDir) && !utils.PathsEqual(curDir, utils.GetSessionsDir()) {
			customDir = curDir
		}

		var listed []core.SessionInfo
		var err error
		if customDir != "" || curDir != "" || utils.GetSessionsDir() != "" {
			listed, err = core.SessionManagerListAll(ctx, customDir)
			if err != nil && curCwd != "" {
				listed, _ = core.SessionManagerList(ctx, curCwd, customDir)
			}
		}

		// Ensure active session appears even when brand new (0 msgs) or in-memory.
		foundActive := false
		for i := range listed {
			if (curFile != "" && listed[i].Path == curFile) || (curId != "" && listed[i].Id == curId) {
				foundActive = true
				if curName != "" {
					cp := curName
					listed[i].Name = &cp
				}
				if curFirst != "" && listed[i].FirstMessage == "" {
					listed[i].FirstMessage = curFirst
				}
				if curCount > listed[i].MessageCount {
					listed[i].MessageCount = curCount
				}
				break
			}
		}
		if !foundActive && sess != nil {
			if curCreated.IsZero() {
				curCreated = time.Now()
			}
			var namePtr *string
			if curName != "" {
				cp := curName
				namePtr = &cp
			}
			activeInfo := core.SessionInfo{
				Path:         curFile,
				Id:           curId,
				Cwd:          curCwd,
				Name:         namePtr,
				Created:      curCreated,
				Modified:     curModified,
				MessageCount: curCount,
				FirstMessage: curFirst,
			}
			listed = append([]core.SessionInfo{activeInfo}, listed...)
		}

		if sb.loadSeq.Load() != seq {
			return
		}
		sb.mu.Lock()
		if sb.loadSeq.Load() == seq {
			sb.sessions = listed
			sb.loading = false
		}
		sb.mu.Unlock()

		m.postUI(func() {
			if m.host != nil {
				m.host.RequestRender()
			}
		})
	}()
}

func (m *GuiMode) switchToSessionPath(targetPath string, recordHistory bool) {
	if m == nil || m.runtime == nil || strings.TrimSpace(targetPath) == "" {
		return
	}
	_, curPath, _, _, _ := m.cachedSessionMeta()
	if curPath == targetPath {
		return
	}
	if recordHistory && curPath != "" {
		m.ensureWakuState()
		m.sidebar.mu.Lock()
		m.sidebar.navBack = append(m.sidebar.navBack, curPath)
		if len(m.sidebar.navBack) > 50 {
			m.sidebar.navBack = m.sidebar.navBack[len(m.sidebar.navBack)-50:]
		}
		m.sidebar.navFwd = nil
		m.sidebar.mu.Unlock()
	}

	go func() {
		res, err := m.runtime.SwitchSession(m.currentCtx(), targetPath)
		if err != nil {
			m.postUI(func() { m.ShowError("Failed to switch session: " + err.Error()) })
			return
		}
		if res != nil && res.Cancelled {
			return
		}
		m.postUI(func() { m.handleSessionReplaced() })
	}()
}

func (m *GuiMode) navigateSessionHistory(dir int) {
	if m == nil {
		return
	}
	m.ensureWakuState()
	_, curPath, _, _, _ := m.cachedSessionMeta()

	var target string
	sb := m.sidebar
	sb.mu.Lock()
	if dir < 0 && len(sb.navBack) > 0 {
		target = sb.navBack[len(sb.navBack)-1]
		sb.navBack = sb.navBack[:len(sb.navBack)-1]
		if curPath != "" {
			sb.navFwd = append(sb.navFwd, curPath)
		}
	} else if dir > 0 && len(sb.navFwd) > 0 {
		target = sb.navFwd[len(sb.navFwd)-1]
		sb.navFwd = sb.navFwd[:len(sb.navFwd)-1]
		if curPath != "" {
			sb.navBack = append(sb.navBack, curPath)
		}
	}
	sb.mu.Unlock()

	if target != "" {
		m.switchToSessionPath(target, false)
	}
}

func (m *GuiMode) toggleQuickThemeDarkLight() {
	if m == nil {
		return
	}
	cur := m.currentGuiTheme()
	target := "light"
	if !cur.Dark {
		target = "dark"
	}
	if sess := m.Session(); sess != nil && sess.SettingsManager != nil {
		sess.SettingsManager.SetTheme(target)
	}
	_, ts := ApplyThemeToHost(m.host, target)
	if ts != nil {
		m.applyThemeSet(ts)
	}
	m.updateFooterAndTitle()
}

func (m *GuiMode) renderWakuSidebar(c *ui.Context, th *gui.GuiTheme) {
	if m == nil {
		return
	}
	m.ensureWakuState()
	sb := m.sidebar

	sb.mu.Lock()
	width := sb.width
	groupBy := sb.groupBy
	sessions := append([]core.SessionInfo(nil), sb.sessions...)
	pinned := make(map[string]bool, len(sb.pinned))
	for k, v := range sb.pinned {
		pinned[k] = v
	}
	sb.mu.Unlock()

	sess := m.Session()
	activeID, activeFile, _, _, _ := m.cachedSessionMeta()
	var isStreaming bool
	if sess != nil {
		isStreaming = sess.IsStreaming() || sess.IsCompacting() || sess.IsRetrying()
	}

	bg := th.SidebarBg
	if c != nil && c.Vibrancy() {
		bg = th.SidebarBg.Alpha(0.76)
	}

	var toggleSidebar, newTask, openSearch, toggleGroup, toggleTheme, openSettings bool
	var switchPath string
	var togglePinKey string
	var renameTarget *core.SessionInfo
	var deleteTarget *core.SessionInfo
	var copyPathTarget string
	var forkActive bool

	panel := ui.Column(c).
		Key("waku-sidebar").
		Width(width).
		Shrink(0).
		FillHeight().
		Background(bg)

	panel.Children(func() {
		// 1. Top 48px traffic-light bar + sidebar toggle
		topH := wakuHeaderHeight(c)
		ui.Row(c).
			FillWidth().
			Height(topH).
			AlignItems(ui.Center).
			Padding(0, 10, 0, 10).
			Gap(4).
			Children(func() {
				inset := wakuTrafficLightInset(c)
				if inset > 12 {
					ui.Box(c).Width(inset - 10).FillHeight().DragWindow()
				}
				ui.Box(c).Grow(1).FillHeight().DragWindow()
				if wakuIconButton(c, th, gui.IconPanelLeft, "Collapse Sidebar", false, th.TextSecondary) {
					toggleSidebar = true
				}
			})

		// 2. Primary Action rows: New Task & Search
		ui.Column(c).FillWidth().Padding(2, 8, 6, 8).Gap(2).Children(func() {
			newBtn := ui.ButtonBase(c).
				FillWidth().
				Height(32).
				Padding(0, 10, 0, 10).
				Radius(8).
				AlignItems(ui.Center).
				Gap(9).
				Cursor(ui.CursorPointer).
				Label("New Task")
			if newBtn.Hovered() {
				newBtn.Background(th.SidebarItemBg)
			}
			newBtn.Children(func() {
				gui.IconView(c, gui.IconCompose, 15, th.TextSecondary)
				ui.Text(c, "New Task").TextColor(th.Text).FontSize(13).FontWeight(500).SingleLine()
			})
			if newBtn.Clicked() {
				newTask = true
			}

			searchBtn := ui.ButtonBase(c).
				FillWidth().
				Height(32).
				Padding(0, 10, 0, 10).
				Radius(8).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Cursor(ui.CursorPointer).
				Label("Search")
			if searchBtn.Hovered() {
				searchBtn.Background(th.SidebarItemBg)
			}
			searchBtn.Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(9).Children(func() {
					gui.IconView(c, gui.IconSearch, 15, th.TextSecondary)
					ui.Text(c, "Search").TextColor(th.Text).FontSize(13).FontWeight(500).SingleLine()
				})
				ui.Text(c, "⌘K").TextColor(th.TextGhost).FontSize(11).SingleLine()
			})
			if searchBtn.Clicked() {
				openSearch = true
			}
		})

		// 3. Sessions section header
		ui.Row(c).
			FillWidth().
			Padding(8, 12, 4, 14).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween).
			Children(func() {
				ui.Text(c, "Sessions").TextColor(th.TextTertiary).FontSize(11).FontWeight(600).SingleLine()
				groupSvg := gui.IconFilter
				groupLbl := "Group by Project"
				if groupBy == "project" {
					groupSvg = gui.IconFolder
					groupLbl = "Group by Date"
				}
				grpBtn := ui.ButtonBase(c).
					Size(22, 22).
					Radius(6).
					AlignItems(ui.Center).
					Justify(ui.Center).
					Cursor(ui.CursorPointer).
					Label(groupLbl)
				if grpBtn.Hovered() {
					grpBtn.Background(th.Overlay)
				}
				grpBtn.Children(func() {
					gui.IconView(c, groupSvg, 13, th.TextTertiary)
				})
				if grpBtn.Clicked() {
					toggleGroup = true
				}
			})

		// 4. Scrollable Session Cards
		groups := wakuGroupSessions(sessions, pinned, groupBy)
		scroll := ui.Scroll(c).FillWidth().Grow(1).Padding(2, 8, 8, 8)
		scroll.Children(func() {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				if len(groups) == 0 {
					ui.Box(c).FillWidth().Padding(14, 10).Children(func() {
						ui.Text(c, "No sessions yet").TextColor(th.TextTertiary).FontSize(12)
					})
					return
				}
				itemIdx := 0
				for gIdx, grp := range groups {
					if grp.label != "" {
						ui.Box(c).
							Key(fmt.Sprintf("sb-grp-%d-%s", gIdx, grp.label)).
							FillWidth().
							Padding(8, 6, 3, 6).
							Children(func() {
								ui.Text(c, grp.label).TextColor(th.TextGhost).FontSize(11).FontWeight(600).SingleLine()
							})
					}
					for _, info := range grp.items {
						s := info
						key := wakuSessionPinKey(s)
						isActive := (activeFile != "" && s.Path == activeFile) || (activeID != "" && s.Id == activeID)
						isPinned := pinned[key]
						title := wakuSessionTitle(s)
						age := wakuFormatShortAge(s.Modified)
						proj := filepath.Base(s.Cwd)
						if proj == "" || proj == "." {
							proj = "workspace"
						}

						row := ui.ButtonBase(c).
							Key(fmt.Sprintf("sb-item-%d-%s", itemIdx, key)).
							FillWidth().
							MinHeight(50).
							Padding(7, 10, 7, 10).
							Radius(8).
							Cursor(ui.CursorPointer).
							Label(title)
						itemIdx++

						if isActive {
							row.Background(th.SidebarItemBg)
						} else if row.Hovered() {
							row.Background(th.Overlay)
						}

						row.Children(func() {
							ui.Column(c).FillWidth().Gap(3).Children(func() {
								ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
									ui.Row(c).Shrink(1).AlignItems(ui.Center).Gap(6).Children(func() {
										if isActive && isStreaming {
											ui.Spinner(c).TextColor(th.Accent).Label("Running")
										} else if isActive {
											ui.Box(c).Size(6, 6).Radius(999).Background(th.Accent)
										}
										col := th.TextSecondary
										weight := 500
										if isActive {
											col = th.Text
											weight = 600
										}
										ui.Text(c, title).TextColor(col).FontSize(13).FontWeight(weight).SingleLine()
									})
									if age != "" {
										ui.Text(c, age).TextColor(th.TextGhost).FontSize(11).SingleLine()
									}
								})
								ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
									ui.Text(c, proj).TextColor(th.TextTertiary).FontSize(11).SingleLine()
									if s.MessageCount > 0 {
										ui.Textf(c, "%d msgs", s.MessageCount).TextColor(th.TextGhost).FontSize(11).SingleLine()
									}
								})
							})
						})

						if row.Clicked() && s.Path != "" {
							switchPath = s.Path
						}

						row.PopoverMenu(func(menu *ui.Menu) {
							pinLbl := "Pin Session"
							if isPinned {
								pinLbl = "Unpin Session"
							}
							if menu.Item(pinLbl).Chosen() {
								togglePinKey = key
							}
							if s.Path != "" && menu.Item("Rename…").Chosen() {
								cp := s
								renameTarget = &cp
							}
							if isActive && menu.Item("Fork Session…").Chosen() {
								forkActive = true
							}
							if s.Path != "" && menu.Item("Copy Session Path").Chosen() {
								copyPathTarget = s.Path
							}
							if s.Path != "" && s.Path != activeFile {
								menu.Separator()
								if menu.Item("Delete Session").Chosen() {
									cp := s
									deleteTarget = &cp
								}
							}
						})
					}
				}
			})
		})

		// 5. Bottom footer strip
		ui.Box(c).FillWidth().Height(1).Background(th.SidebarBorder)
		ui.Row(c).
			FillWidth().
			Height(40).
			Padding(0, 10, 0, 10).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween).
			Children(func() {
				modeLbl := "Dark"
				if !th.Dark {
					modeLbl = "Light"
				}
				themeBtn := ui.ButtonBase(c).
					Height(26).
					Padding(0, 8, 0, 8).
					Radius(6).
					AlignItems(ui.Center).
					Gap(6).
					Cursor(ui.CursorPointer).
					Label("Toggle Appearance")
				if themeBtn.Hovered() {
					themeBtn.Background(th.Overlay)
				}
				themeBtn.Children(func() {
					gui.IconView(c, gui.IconAppearance, 13, th.TextSecondary)
					ui.Text(c, modeLbl).TextColor(th.TextSecondary).FontSize(12).SingleLine()
				})
				if themeBtn.Clicked() {
					toggleTheme = true
				}

				if wakuIconButton(c, th, gui.IconSettings, "Settings", false, th.TextSecondary) {
					openSettings = true
				}
			})
	})

	sb.split.view(
		c,
		"waku-sidebar-split",
		th,
		th.SidebarBorder,
		width,
		wakuSidebarMinWidth,
		wakuSidebarMaxWidth,
		false,
		func(next float32) {
			sb.mu.Lock()
			sb.width = next
			sb.mu.Unlock()
		},
		func() { m.saveLayoutPrefs() },
	)

	if toggleSidebar {
		m.toggleLeftSidebar()
	}
	if newTask {
		go m.newSession(m.currentCtx())
	}
	if openSearch {
		m.ShowCommandPalette()
	}
	if toggleGroup {
		sb.mu.Lock()
		if sb.groupBy == "project" {
			sb.groupBy = "date"
		} else {
			sb.groupBy = "project"
		}
		sb.mu.Unlock()
		m.saveLayoutPrefs()
	}
	if toggleTheme {
		m.toggleQuickThemeDarkLight()
	}
	if openSettings {
		m.ShowSettingsSelector()
	}
	if switchPath != "" {
		m.switchToSessionPath(switchPath, true)
	}
	if togglePinKey != "" {
		sb.mu.Lock()
		if sb.pinned[togglePinKey] {
			delete(sb.pinned, togglePinKey)
		} else {
			sb.pinned[togglePinKey] = true
		}
		sb.mu.Unlock()
		m.saveLayoutPrefs()
	}
	if renameTarget != nil {
		m.promptRenameSession(*renameTarget)
	}
	if forkActive {
		m.ShowForkSelector()
	}
	if copyPathTarget != "" {
		_ = gmWriteClipboardText(m.host, copyPathTarget)
		m.ShowInfo("Copied", "Session path copied to clipboard")
	}
	if deleteTarget != nil {
		target := *deleteTarget
		go func() {
			if err := os.Remove(target.Path); err != nil {
				m.postUI(func() { m.ShowError("Failed to delete session: " + err.Error()) })
				return
			}
			m.refreshSidebarSessions()
		}()
	}
}

func (m *GuiMode) promptRenameSession(target core.SessionInfo) {
	if m == nil || m.host == nil || target.Path == "" {
		return
	}
	curTitle := wakuSessionTitle(target)
	dlg := NewExtensionInputDialog(ExtensionInputConfig{
		Title:        "Rename Session",
		Message:      filepath.Base(target.Path),
		Placeholder:  "Session name...",
		InitialValue: curTitle,
		Theme:        m.currentGuiTheme(),
		OnSubmit: func(newName string) {
			trimmed := strings.TrimSpace(newName)
			if trimmed == "" {
				return
			}
			go func() {
				sm, err := core.SessionManagerOpen(target.Path)
				if err != nil {
					m.postUI(func() { m.ShowError("Failed to rename session: " + err.Error()) })
					return
				}
				if _, err := sm.AppendSessionInfo(trimmed); err != nil {
					m.postUI(func() { m.ShowError("Failed to rename session: " + err.Error()) })
					return
				}
				m.postUI(func() {
					_, activeFile, _, _, _ := m.cachedSessionMeta()
					if activeFile != "" && activeFile == target.Path {
						sess := m.Session()
						if sess != nil && !sess.IsStreaming() && !sess.IsCompacting() && m.sessionMutationDepth.Load() == 0 {
							sess.SetSessionName(trimmed)
						}
						m.setCachedSessionName(trimmed)
						m.updateFooterAndTitle()
					}
					m.refreshSidebarSessions()
				})
			}()
		},
	})
	m.setActiveDialog(dlg.Show(m.host))
}

func wakuSessionPinKey(s core.SessionInfo) string {
	if s.Path != "" {
		return s.Path
	}
	if s.Id != "" {
		return s.Id
	}
	return "active"
}

func wakuSessionTitle(s core.SessionInfo) string {
	if s.Name != nil && strings.TrimSpace(*s.Name) != "" {
		return selTruncateSingleLine(*s.Name, 48)
	}
	if strings.TrimSpace(s.FirstMessage) != "" {
		return selTruncateSingleLine(s.FirstMessage, 48)
	}
	return "New task"
}

type wakuSessionGroup struct {
	label string
	items []core.SessionInfo
}

func wakuGroupSessions(sessions []core.SessionInfo, pinned map[string]bool, groupBy string) []wakuSessionGroup {
	if len(sessions) == 0 {
		return nil
	}
	var pinnedList, rest []core.SessionInfo
	for _, s := range sessions {
		if pinned[wakuSessionPinKey(s)] {
			pinnedList = append(pinnedList, s)
		} else {
			rest = append(rest, s)
		}
	}

	var groups []wakuSessionGroup
	if len(pinnedList) > 0 {
		groups = append(groups, wakuSessionGroup{label: "Pinned", items: pinnedList})
	}

	if groupBy == "project" {
		order := make([]string, 0)
		buckets := make(map[string][]core.SessionInfo)
		for _, s := range rest {
			proj := filepath.Base(s.Cwd)
			if proj == "" || proj == "." {
				proj = "Other"
			}
			if _, ok := buckets[proj]; !ok {
				order = append(order, proj)
			}
			buckets[proj] = append(buckets[proj], s)
		}
		for _, proj := range order {
			groups = append(groups, wakuSessionGroup{label: proj, items: buckets[proj]})
		}
		return groups
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterdayStart := todayStart.Add(-24 * time.Hour)
	weekStart := todayStart.Add(-7 * 24 * time.Hour)

	var today, yesterday, prev7, older []core.SessionInfo
	for _, s := range rest {
		t := s.Modified
		if t.IsZero() {
			t = s.Created
		}
		switch {
		case !t.Before(todayStart):
			today = append(today, s)
		case !t.Before(yesterdayStart):
			yesterday = append(yesterday, s)
		case !t.Before(weekStart):
			prev7 = append(prev7, s)
		default:
			older = append(older, s)
		}
	}
	if len(today) > 0 {
		groups = append(groups, wakuSessionGroup{label: "Today", items: today})
	}
	if len(yesterday) > 0 {
		groups = append(groups, wakuSessionGroup{label: "Yesterday", items: yesterday})
	}
	if len(prev7) > 0 {
		groups = append(groups, wakuSessionGroup{label: "Previous 7 Days", items: prev7})
	}
	if len(older) > 0 {
		groups = append(groups, wakuSessionGroup{label: "Older", items: older})
	}
	return groups
}
