package gui

import (
	"context"
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
	wakuSidebarDefaultWidth      float32 = 252
	wakuSidebarMinWidth          float32 = 180
	wakuSidebarMaxWidth          float32 = 420
	wakuSidebarActionRowHeight   float32 = 32
	wakuSidebarSearchBottomGap   float32 = 10
	wakuSidebarGroupHeaderHeight float32 = 28
	wakuSidebarGroupChildPadding float32 = 28
	wakuSidebarSessionCardHeight float32 = 51
	wakuSidebarFooterHeight      float32 = 40
)

type wakuLayoutPrefs struct {
	SidebarOpen     bool     `json:"sidebarOpen"`
	SidebarWidth    float32  `json:"sidebarWidth"`
	SidebarGroupBy  string   `json:"sidebarGroupBy,omitzero"`
	SidebarOrderBy  string   `json:"sidebarOrderBy,omitzero"`
	RightPanelOpen  bool     `json:"rightPanelOpen"`
	RightPanelWidth float32  `json:"rightPanelWidth"`
	RightPanelTab   string   `json:"rightPanelTab,omitzero"`
	PinnedSessions  []string `json:"pinnedSessions,omitzero"`
}

func wakuNormalizeGroupBy(v string) string {
	if v == "project" {
		return "project"
	}
	// Accept legacy "date" and empty values as "updated".
	return "updated"
}

func wakuNormalizeOrderBy(v string) string {
	if v == "oldest" {
		return "oldest"
	}
	return "newest"
}

func wakuLayoutPrefsPath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "pi-go", "gui-layout.json")
}

type wakuSidebarState struct {
	mu          sync.Mutex
	open        bool
	width       float32
	groupBy     string // "updated" or "project"
	orderBy     string // "newest" or "oldest"
	collapsed   map[string]bool
	optionsOpen bool
	sessions    []core.SessionInfo
	loading     bool
	pinned      map[string]bool
	navBack     []string
	navFwd      []string
	split       wakuSplitDragTracker
	loadSeq     atomic.Uint64
	persist     bool
	initDone    bool
}

func newWakuSidebarState(skipPersist bool) *wakuSidebarState {
	st := &wakuSidebarState{
		open:      true,
		width:     wakuSidebarDefaultWidth,
		groupBy:   "updated",
		orderBy:   "newest",
		collapsed: make(map[string]bool),
		pinned:    make(map[string]bool),
		persist:   !skipPersist,
		initDone:  true,
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
	s.groupBy = wakuNormalizeGroupBy(prefs.SidebarGroupBy)
	s.orderBy = wakuNormalizeOrderBy(prefs.SidebarOrderBy)
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
	prefs.SidebarOrderBy = sb.orderBy
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

func wakuResolvePath(p string) string {
	p = filepath.Clean(strings.TrimSpace(p))
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// wakuIsManagedDefaultSessionDir reports whether dir is inside utils.GetSessionsDir()
// (where per-project session folders live) or equal to it. External --session-dir
// locations outside utils.GetSessionsDir() return false.
func wakuIsManagedDefaultSessionDir(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return true
	}
	sessionsRoot := strings.TrimSpace(utils.GetSessionsDir())
	if sessionsRoot == "" {
		return false
	}
	if utils.PathsEqual(dir, sessionsRoot) {
		return true
	}
	root := wakuResolvePath(sessionsRoot)
	target := wakuResolvePath(dir)
	if utils.PathsEqual(target, root) {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func wakuSessionDedupeKey(s core.SessionInfo) string {
	if strings.TrimSpace(s.Path) != "" {
		return wakuResolvePath(s.Path)
	}
	return "id:" + s.Id
}

func wakuSessionSortTime(s core.SessionInfo) time.Time {
	if !s.Modified.IsZero() {
		return s.Modified
	}
	return s.Created
}

func wakuSessionHasStarted(s core.SessionInfo) bool {
	if s.MessageCount > 0 {
		return true
	}
	first := strings.TrimSpace(s.FirstMessage)
	if first != "" && first != "(no messages)" {
		return true
	}
	return s.Name != nil && strings.TrimSpace(*s.Name) != ""
}

func (m *GuiMode) hasDispatchedTranscriptEntries() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		switch e.kind {
		case "user", "assistant", "bash", "tool", "skill", "compaction", "branch", "custom":
			return true
		}
	}
	return false
}

func (m *GuiMode) loadSidebarSessionsSnapshot(ctx context.Context) []core.SessionInfo {
	if m == nil {
		return nil
	}
	sess := m.Session()

	var curFile, curId, curCwd, curDir, curName, curFirst string
	var curCount int
	var curCreated time.Time
	if sess != nil {
		curId, curFile, curDir, curName, curCreated = m.cachedSessionMeta()
		curCwd = sess.Cwd()
		curFirst = m.firstTranscriptUserTitle(80)
		curCount = len(sess.Messages())
	}

	// Always list all project sessions under the managed sessions root so switching to
	// a session in another project never collapses the sidebar to a single folder.
	var baseListed []core.SessionInfo
	if strings.TrimSpace(utils.GetSessionsDir()) != "" {
		if items, err := core.SessionManagerListAll(ctx, ""); err == nil {
			baseListed = items
		} else if curCwd != "" {
			baseListed, _ = core.SessionManagerList(ctx, curCwd, "")
		}
	}

	var extListed []core.SessionInfo
	if curDir != "" && !wakuIsManagedDefaultSessionDir(curDir) {
		items, err := core.SessionManagerListAll(ctx, curDir)
		if err != nil && curCwd != "" {
			items, _ = core.SessionManagerList(ctx, curCwd, curDir)
		}
		extListed = items
	}

	mergedByKey := make(map[string]core.SessionInfo, len(baseListed)+len(extListed))
	order := make([]string, 0, len(baseListed)+len(extListed))
	addItems := func(items []core.SessionInfo) {
		for _, item := range items {
			key := wakuSessionDedupeKey(item)
			if existing, ok := mergedByKey[key]; ok {
				if wakuSessionSortTime(item).After(wakuSessionSortTime(existing)) {
					mergedByKey[key] = item
				}
				continue
			}
			mergedByKey[key] = item
			order = append(order, key)
		}
	}
	addItems(baseListed)
	addItems(extListed)

	listed := make([]core.SessionInfo, 0, len(order))
	for _, key := range order {
		listed = append(listed, mergedByKey[key])
	}

	// Preserve on-disk Modified timestamps on active sessions instead of bumping to time.Now().
	// Only show started (dispatched) sessions in the sidebar, matching Waku's sidebar.
	curFileResolved := ""
	if curFile != "" {
		curFileResolved = wakuResolvePath(curFile)
	}
	activeStarted := curCount > 0 ||
		strings.TrimSpace(curFirst) != "" ||
		strings.TrimSpace(curName) != "" ||
		(sess != nil && (sess.IsStreaming() || sess.IsCompacting() || sess.IsBashRunning())) ||
		m.hasDispatchedTranscriptEntries()

	foundActive := false
	filtered := listed[:0]
	for _, item := range listed {
		pathMatch := curFileResolved != "" && wakuResolvePath(item.Path) == curFileResolved
		idMatch := curId != "" && item.Id == curId
		if pathMatch || idMatch {
			foundActive = true
			if curName != "" {
				cp := curName
				item.Name = &cp
			}
			if curFirst != "" && (strings.TrimSpace(item.FirstMessage) == "" || item.FirstMessage == "(no messages)") {
				item.FirstMessage = curFirst
			}
			if curCount > item.MessageCount {
				item.MessageCount = curCount
			}
			if !activeStarted && !wakuSessionHasStarted(item) {
				continue
			}
		} else if !wakuSessionHasStarted(item) {
			continue
		}
		filtered = append(filtered, item)
	}
	listed = filtered
	if !foundActive && sess != nil && activeStarted {
		curModified := curCreated
		if curFile != "" {
			if st, err := os.Stat(curFile); err == nil {
				curModified = st.ModTime()
			}
		}
		if curCreated.IsZero() {
			curCreated = curModified
			if curCreated.IsZero() {
				curCreated = time.Now()
			}
		}
		if curModified.IsZero() {
			curModified = curCreated
		}
		var namePtr *string
		if curName != "" {
			cp := curName
			namePtr = &cp
		}
		listed = append(listed, core.SessionInfo{
			Path:         curFile,
			Id:           curId,
			Cwd:          curCwd,
			Name:         namePtr,
			Created:      curCreated,
			Modified:     curModified,
			MessageCount: curCount,
			FirstMessage: curFirst,
		})
	}

	slices.SortStableFunc(listed, func(a, b core.SessionInfo) int {
		tb, ta := wakuSessionSortTime(b), wakuSessionSortTime(a)
		if c := tb.Compare(ta); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return listed
}

func (m *GuiMode) refreshSidebarSessionsSync(ctx context.Context) []core.SessionInfo {
	if m == nil {
		return nil
	}
	m.ensureWakuState()
	sb := m.sidebar
	seq := sb.loadSeq.Add(1)
	listed := m.loadSidebarSessionsSnapshot(ctx)
	sb.mu.Lock()
	if sb.loadSeq.Load() == seq {
		sb.sessions = listed
		sb.loading = false
	}
	sb.mu.Unlock()
	return listed
}

func (m *GuiMode) refreshSidebarSessions() {
	if m == nil {
		return
	}
	m.ensureWakuState()
	sb := m.sidebar

	seq := sb.loadSeq.Add(1)
	sb.mu.Lock()
	sb.loading = true
	sb.mu.Unlock()

	ctx := m.currentCtx()
	go func() {
		listed := m.loadSidebarSessionsSnapshot(ctx)
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

func wakuSidebarOptionRow(c *ui.Context, th *gui.GuiTheme, label string, selected bool) bool {
	btn := ui.ButtonBase(c).
		FillWidth().
		Height(26).
		Padding(0, 8, 0, 8).
		Radius(5).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Gap(8).
		Cursor(ui.CursorPointer).
		Label(label)
	if btn.Hovered() {
		btn.Background(th.Overlay)
	}
	btn.Children(func() {
		ui.Text(c, label).TextColor(th.Text).FontSize(12).SingleLine()
		if selected {
			gui.IconView(c, gui.IconCheck, 12, th.Accent)
		}
	})
	return btn.Clicked()
}

func (m *GuiMode) renderWakuSidebar(c *ui.Context, th *gui.GuiTheme) {
	if m == nil {
		return
	}
	m.ensureWakuState()
	sb := m.sidebar

	sb.mu.Lock()
	width := sb.width
	groupBy := wakuNormalizeGroupBy(sb.groupBy)
	orderBy := wakuNormalizeOrderBy(sb.orderBy)
	optionsOpen := sb.optionsOpen
	canBack := len(sb.navBack) > 0
	canForward := len(sb.navFwd) > 0
	collapsed := make(map[string]bool, len(sb.collapsed))
	for k, v := range sb.collapsed {
		collapsed[k] = v
	}
	sessions := append([]core.SessionInfo(nil), sb.sessions...)
	pinned := make(map[string]bool, len(sb.pinned))
	for k, v := range sb.pinned {
		pinned[k] = v
	}
	sb.mu.Unlock()

	sess := m.Session()
	activeID, activeFile, _, _, _ := m.cachedSessionMeta()
	_, activeBranch := m.rightPanel.gitBranchesSnapshot()
	var isStreaming bool
	if sess != nil {
		isStreaming = sess.IsStreaming() || sess.IsCompacting() || sess.IsRetrying()
	}

	bg := th.SidebarBg
	if c != nil && c.Vibrancy() {
		bg = th.SidebarBg.Alpha(0.76)
	}

	var toggleSidebar, goBack, goForward, newTask, openSearch, openSettings bool
	var toggleCollapseKey, nextGroupBy, nextOrderBy string
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
		// 1. Top 48px sidebar-titlebar: macOS traffic-light clearance + Toggle Sidebar + history nav + drag spacer
		topH := wakuHeaderHeight(c)
		inset := wakuTrafficLightInset(c)
		titlePadLeft := float32(0)
		if inset <= 0 {
			titlePadLeft = 8
		}
		ui.Row(c).
			Key("waku-sidebar-titlebar").
			FillWidth().
			Height(topH).
			AlignItems(ui.Center).
			Padding(0, 8, 0, titlePadLeft).
			Children(func() {
				if inset > 0 {
					ui.Box(c).Width(inset).FillHeight().DragWindow()
				}
				ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(2).Children(func() {
					if wakuIconButton(c, th, gui.IconPanelLeft, "Toggle Sidebar", false, th.TextSecondary) {
						toggleSidebar = true
					}
					goBack, goForward = renderWakuHistoryNavButtons(c, th, canBack, canForward)
				})
				ui.Box(c).Grow(1).FillHeight().DragWindow()
			})

		// 2. Pinned New Task action row (32px, regular weight)
		ui.Box(c).FillWidth().Padding(0, 8, 0, 8).Children(func() {
			newBtn := ui.ButtonBase(c).
				FillWidth().
				Height(wakuSidebarActionRowHeight).
				Padding(0, 8, 0, 8).
				Radius(6).
				AlignItems(ui.Center).
				Justify(ui.Start).
				Gap(8).
				Cursor(ui.CursorPointer).
				Label("New Task")
			if newBtn.Hovered() {
				newBtn.Background(th.SidebarItemBg)
			}
			newBtn.Children(func() {
				gui.IconView(c, gui.IconCompose, 15, th.TextSecondary)
				ui.Text(c, "New Task").TextColor(th.Text).FontSize(13).SingleLine()
			})
			if newBtn.Clicked() {
				newTask = true
			}
		})

		// 3. Scrollable Search row + Session Groups & Cards
		groups := wakuGroupSessions(sessions, pinned, groupBy, orderBy)
		scroll := ui.Scroll(c).FillWidth().Grow(1).Clip().Padding(0, 8, 8, 8)
		scroll.Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				// Search row inside scroll (32px + 10px bottom margin, no shortcut badge)
				ui.Box(c).FillWidth().Margin(0, 0, wakuSidebarSearchBottomGap, 0).Children(func() {
					searchBtn := ui.ButtonBase(c).
						FillWidth().
						Height(wakuSidebarActionRowHeight).
						Padding(0, 8, 0, 8).
						Radius(6).
						AlignItems(ui.Center).
						Justify(ui.Start).
						Gap(8).
						Cursor(ui.CursorPointer).
						Label("Search")
					if searchBtn.Hovered() {
						searchBtn.Background(th.SidebarItemBg)
					}
					searchBtn.Children(func() {
						gui.IconView(c, gui.IconSearch, 15, th.TextSecondary)
						ui.Text(c, "Search").TextColor(th.Text).FontSize(13).SingleLine()
					})
					if searchBtn.Clicked() {
						openSearch = true
					}
				})

				renderGroupHeader := func(collapseKey, label string, isProjectGroup, isCollapsed, hasItems, showOptions bool) {
					ui.Row(c).
						FillWidth().
						Height(wakuSidebarGroupHeaderHeight).
						Margin(0, 0, 2, 0).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween).
						Gap(4).
						Children(func() {
							if isProjectGroup {
								hdrBtn := ui.ButtonBase(c).
									Grow(1).
									Shrink(1).
									FillHeight().
									Padding(0, 6, 0, 6).
									Radius(6).
									AlignItems(ui.Center).
									Justify(ui.Start).
									Gap(6).
									Cursor(ui.CursorPointer).
									Label(label)
								if hasItems && hdrBtn.Hovered() {
									hdrBtn.Background(th.Overlay)
								}
								hdrBtn.Children(func() {
									folderIcon := gui.IconFolderOpen
									if isCollapsed {
										folderIcon = gui.IconFolder
									}
									gui.IconView(c, folderIcon, 14, th.TextSecondary)
									lblCol := th.TextSecondary
									if hdrBtn.Hovered() {
										lblCol = th.Text
									}
									ui.Text(c, label).TextColor(lblCol).FontSize(12).FontWeight(500).SingleLine()
								})
								if hasItems && hdrBtn.Clicked() {
									toggleCollapseKey = collapseKey
								}
							} else {
								var hdrBox ui.Element
								if hasItems {
									hdrBox = ui.ButtonBase(c).
										Grow(1).
										Shrink(1).
										FillHeight().
										Padding(0, 8, 0, 8).
										AlignItems(ui.Center).
										Justify(ui.Start).
										Cursor(ui.CursorPointer).
										Label(label)
								} else {
									hdrBox = ui.Row(c).
										Grow(1).
										Shrink(1).
										FillHeight().
										Padding(0, 8, 0, 8).
										AlignItems(ui.Center).
										Justify(ui.Start).
										Label(label)
								}
								lblCol := th.TextTertiary
								if hasItems && hdrBox.Hovered() {
									lblCol = th.TextSecondary
								}
								hdrBox.Children(func() {
									ui.Text(c, label).TextColor(lblCol).FontSize(12).FontWeight(500).SingleLine()
								})
								if hasItems && hdrBox.Clicked() {
									toggleCollapseKey = collapseKey
								}
							}

							if showOptions {
								optAnchor := ui.ButtonBase(c).
									Size(24, 24).
									Radius(6).
									AlignItems(ui.Center).
									Justify(ui.Center).
									Cursor(ui.CursorPointer).
									Label("Sidebar options")
								if optionsOpen || optAnchor.Hovered() {
									optAnchor.Background(th.Overlay)
								}
								optAnchor.Children(func() {
									gui.IconView(c, gui.IconFilter, 13, th.TextTertiary)
								})
								if optAnchor.Clicked() {
									optionsOpen = !optionsOpen
								}

								ui.PopoverBase(c, optAnchor, &optionsOpen, func(popPanel ui.Element) {
									popPanel.Background(th.Surface).
										Border(1, th.Border).
										Radius(8).
										Padding(6).
										MinWidth(164).
										Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.28)).
										Children(func() {
											ui.Column(c).FillWidth().Gap(2).Children(func() {
												ui.Box(c).Padding(4, 8, 2, 8).Children(func() {
													ui.Text(c, "Group by").TextColor(th.TextTertiary).FontSize(11).FontWeight(500).SingleLine()
												})
												for _, opt := range []struct {
													key   string
													label string
												}{
													{key: "updated", label: "Updated"},
													{key: "project", label: "Project"},
												} {
													if wakuSidebarOptionRow(c, th, opt.label, groupBy == opt.key) {
														nextGroupBy = opt.key
														optionsOpen = false
													}
												}
												ui.Box(c).FillWidth().Height(1).Background(th.Border).Margin(4, 0, 4, 0)
												ui.Box(c).Padding(2, 8, 2, 8).Children(func() {
													ui.Text(c, "Order by").TextColor(th.TextTertiary).FontSize(11).FontWeight(500).SingleLine()
												})
												for _, opt := range []struct {
													key   string
													label string
												}{
													{key: "newest", label: "Newest"},
													{key: "oldest", label: "Oldest"},
												} {
													if wakuSidebarOptionRow(c, th, opt.label, orderBy == opt.key) {
														nextOrderBy = opt.key
														optionsOpen = false
													}
												}
											})
										})
								})
							}
						})
				}

				if len(groups) == 0 {
					// Match Waku: keep Today / current project header + Sidebar options visible with no undispatched session card.
					if groupBy == "project" {
						proj := "workspace"
						if sess != nil && strings.TrimSpace(sess.Cwd()) != "" {
							base := filepath.Base(sess.Cwd())
							if base != "" && base != "." {
								proj = base
							}
						}
						renderGroupHeader("project:"+proj, proj, true, false, false, true)
					} else {
						renderGroupHeader("updated:today", "Today", false, false, false, true)
					}
					return
				}

				itemIdx := 0
				for gIdx, grp := range groups {
					isCollapsed := collapsed[grp.collapseKey]
					isProjectGroup := groupBy == "project" && grp.label != "Pinned"
					grpCol := ui.Column(c).
						Key(fmt.Sprintf("sb-grp-%d-%s", gIdx, grp.collapseKey)).
						FillWidth()
					if gIdx < len(groups)-1 {
						grpCol.Margin(0, 0, 10, 0)
					}
					grpCol.Children(func() {
						// Collapsible group header (28px height + 2px bottom margin)
						renderGroupHeader(grp.collapseKey, grp.label, isProjectGroup, isCollapsed, true, gIdx == 0)

						if isCollapsed {
							return
						}

						renderGroupCards := func() {
							for _, info := range grp.items {
								s := info
								key := wakuSessionPinKey(s)
								isActive := (activeFile != "" && wakuResolvePath(s.Path) == wakuResolvePath(activeFile)) || (activeID != "" && s.Id == activeID)
								isPinned := pinned[key]
								title := wakuSessionTitle(s)
								age := wakuFormatShortAge(wakuSessionSortTime(s))
								proj := filepath.Base(s.Cwd)
								if proj == "" || proj == "." {
									proj = "workspace"
								}

								row := ui.ButtonBase(c).
									Key(fmt.Sprintf("sb-item-%d-%s", itemIdx, key)).
									FillWidth().
									Height(wakuSidebarSessionCardHeight).
									Padding(6, 10, 6, 10).
									Radius(8).
									AlignItems(ui.Center).
									Justify(ui.Start).
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
										// Line 1: title + busy spinner only (no status dot)
										ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
											ui.Text(c, title).TextColor(th.Text).FontSize(13).FontWeight(500).SingleLine().Shrink(1)
											if isActive && isStreaming {
												ui.Spinner(c).TextColor(th.Accent).Label("Running")
											}
										})
										// Line 2: folder/branch + workspace/branch + relative age / "Working" (no "N msgs")
										ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
											ui.Row(c).Shrink(1).AlignItems(ui.Center).Gap(5).Children(func() {
												metaIcon := gui.IconFolder
												metaText := proj
												if isProjectGroup && isActive && activeBranch != "" {
													metaIcon = gui.IconGitBranch
													metaText = activeBranch
												}
												gui.IconView(c, metaIcon, 11, th.TextTertiary)
												ui.Text(c, metaText).TextColor(th.TextTertiary).FontSize(11).SingleLine().Shrink(1)
											})
											if isActive && isStreaming {
												ui.Text(c, "Working").TextColor(th.Accent).FontSize(11).SingleLine()
											} else if age != "" {
												ui.Text(c, age).TextColor(th.TextGhost).FontSize(11).SingleLine()
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

						if isProjectGroup {
							ui.Box(c).FillWidth().Children(func() {
								ui.Box(c).
									Absolute().
									Left(15).
									Top(0).
									Bottom(0).
									Width(1).
									Background(th.Border)
								ui.Box(c).FillWidth().Padding(0, 0, 0, wakuSidebarGroupChildPadding).Children(func() {
									ui.Column(c).FillWidth().Gap(1).Children(renderGroupCards)
								})
							})
						} else {
							ui.Column(c).FillWidth().Gap(1).Children(renderGroupCards)
						}
					})
				}
			})
		})

		// 4. Borderless 40px bottom footer with only Open Settings on the left
		ui.Row(c).
			FillWidth().
			Height(wakuSidebarFooterHeight).
			Padding(0, 10, 0, 10).
			AlignItems(ui.Center).
			Children(func() {
				if wakuIconButton(c, th, gui.IconSettings, "Open Settings", false, th.TextSecondary) {
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

	prefsChanged := false
	sb.mu.Lock()
	sb.optionsOpen = optionsOpen
	if toggleCollapseKey != "" {
		if sb.collapsed == nil {
			sb.collapsed = make(map[string]bool)
		}
		sb.collapsed[toggleCollapseKey] = !sb.collapsed[toggleCollapseKey]
	}
	if nextGroupBy != "" && sb.groupBy != nextGroupBy {
		sb.groupBy = nextGroupBy
		prefsChanged = true
	}
	if nextOrderBy != "" && sb.orderBy != nextOrderBy {
		sb.orderBy = nextOrderBy
		prefsChanged = true
	}
	sb.mu.Unlock()
	if prefsChanged {
		m.saveLayoutPrefs()
	}

	if toggleSidebar {
		m.toggleLeftSidebar()
	}
	if goBack {
		m.navigateSessionHistory(-1)
	}
	if goForward {
		m.navigateSessionHistory(1)
	}
	if newTask {
		go m.newSession(m.currentCtx())
	}
	if openSearch {
		m.ShowCommandPalette()
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
	first := strings.TrimSpace(s.FirstMessage)
	if first != "" && first != "(no messages)" {
		return selTruncateSingleLine(first, 48)
	}
	return "New task"
}

type wakuSessionGroup struct {
	collapseKey string
	label       string
	items       []core.SessionInfo
}

func wakuSortSessionsByOrder(items []core.SessionInfo, orderBy string) {
	slices.SortStableFunc(items, func(a, b core.SessionInfo) int {
		ta, tb := wakuSessionSortTime(a), wakuSessionSortTime(b)
		var cmp int
		if orderBy == "oldest" {
			cmp = ta.Compare(tb)
		} else {
			cmp = tb.Compare(ta)
		}
		if cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Path, b.Path)
	})
}

func wakuGroupSessions(sessions []core.SessionInfo, pinned map[string]bool, groupBy, orderBy string) []wakuSessionGroup {
	if len(sessions) == 0 {
		return nil
	}
	groupBy = wakuNormalizeGroupBy(groupBy)
	orderBy = wakuNormalizeOrderBy(orderBy)

	var pinnedList, rest []core.SessionInfo
	for _, s := range sessions {
		if pinned[wakuSessionPinKey(s)] {
			pinnedList = append(pinnedList, s)
		} else {
			rest = append(rest, s)
		}
	}
	wakuSortSessionsByOrder(pinnedList, orderBy)
	wakuSortSessionsByOrder(rest, orderBy)

	var groups []wakuSessionGroup
	if len(pinnedList) > 0 {
		groups = append(groups, wakuSessionGroup{
			collapseKey: "pinned",
			label:       "Pinned",
			items:       pinnedList,
		})
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
			groups = append(groups, wakuSessionGroup{
				collapseKey: "project:" + proj,
				label:       proj,
				items:       buckets[proj],
			})
		}
		return groups
	}

	now := time.Now()
	loc := now.Location()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	weekStart := todayStart.AddDate(0, 0, -6)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc)

	var today, yesterday, thisWeek, thisMonth, thisYear, more []core.SessionInfo
	for _, s := range rest {
		t := wakuSessionSortTime(s)
		switch {
		case !t.Before(todayStart):
			today = append(today, s)
		case !t.Before(yesterdayStart):
			yesterday = append(yesterday, s)
		case !t.Before(weekStart):
			thisWeek = append(thisWeek, s)
		case !t.Before(monthStart):
			thisMonth = append(thisMonth, s)
		case !t.Before(yearStart):
			thisYear = append(thisYear, s)
		default:
			more = append(more, s)
		}
	}

	orderedBuckets := []struct {
		key   string
		label string
		items []core.SessionInfo
	}{
		{key: "updated:today", label: "Today", items: today},
		{key: "updated:yesterday", label: "Yesterday", items: yesterday},
		{key: "updated:week", label: "This Week", items: thisWeek},
		{key: "updated:month", label: "This Month", items: thisMonth},
		{key: "updated:year", label: "This Year", items: thisYear},
		{key: "updated:more", label: "More", items: more},
	}
	if orderBy == "oldest" {
		for i, j := 0, len(orderedBuckets)-1; i < j; i, j = i+1, j-1 {
			orderedBuckets[i], orderedBuckets[j] = orderedBuckets[j], orderedBuckets[i]
		}
	}
	for _, b := range orderedBuckets {
		if len(b.items) > 0 {
			groups = append(groups, wakuSessionGroup{
				collapseKey: b.key,
				label:       b.label,
				items:       b.items,
			})
		}
	}
	return groups
}
