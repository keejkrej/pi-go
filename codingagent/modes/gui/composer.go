package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
)

func wakuThinkingLabel(lvl agent.ThinkingLevel) string {
	switch strings.ToLower(strings.TrimSpace(string(lvl))) {
	case "", "off":
		return "Off"
	case "minimal":
		return "Minimal"
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "xhigh":
		return "Extra High"
	case "max":
		return "Max"
	default:
		return string(lvl)
	}
}

func wakuModelChipLabel(sess *core.AgentSession, st FooterState) string {
	if sess != nil {
		if mdl := sess.Model(); mdl != nil {
			if strings.TrimSpace(mdl.Name) != "" {
				return strings.TrimSpace(mdl.Name)
			}
			if strings.TrimSpace(mdl.Id) != "" {
				return strings.TrimSpace(mdl.Id)
			}
		}
	}
	if strings.TrimSpace(st.ModelName) != "" {
		return strings.TrimSpace(st.ModelName)
	}
	if strings.TrimSpace(st.ModelId) != "" {
		return strings.TrimSpace(st.ModelId)
	}
	return "Select model"
}

func wakuProviderIconAndColor(provider string, th *gui.GuiTheme) (*ui.SVG, ui.Color) {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch {
	case strings.Contains(p, "anthropic") || strings.Contains(p, "claude"):
		return gui.IconProviderClaude, th.Accent
	case strings.Contains(p, "openai") || strings.Contains(p, "codex"):
		if th != nil && th.Dark {
			return gui.IconProviderOpenAI, ui.RGB(0x10, 0xA3, 0x7F)
		}
		return gui.IconProviderOpenAI, ui.RGB(0x0E, 0x8A, 0x6B)
	case p == "xai" || strings.Contains(p, "grok"):
		return gui.IconProviderGrok, th.Text
	case p == "pi":
		return gui.IconPiLogo, th.Accent
	default:
		return gui.IconBot, th.TextSecondary
	}
}

func wakuProviderDisplayName(sess *core.AgentSession, provider string) string {
	if sess != nil && sess.ModelRegistry() != nil {
		if dn := strings.TrimSpace(sess.ModelRegistry().GetProviderDisplayName(provider)); dn != "" {
			return dn
		}
	}
	if strings.TrimSpace(provider) == "" {
		return "Provider"
	}
	return provider
}

func wakuProviderRank(provider string) int {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic":
		return 0
	case "openai", "openai-codex":
		return 1
	case "google", "google-gemini-cli":
		return 2
	case "xai":
		return 3
	case "deepseek":
		return 4
	case "moonshot":
		return 5
	case "groq":
		return 6
	case "mistral":
		return 7
	case "openrouter":
		return 8
	default:
		return 100
	}
}

func wakuCollectPickerCatalog(sess *core.AgentSession) ([]*ai.Model, []string) {
	if sess == nil || sess.ModelRegistry() == nil {
		return nil, nil
	}
	reg := sess.ModelRegistry()
	raw := reg.GetAvailable()
	if len(raw) == 0 {
		raw = reg.GetAll()
	}
	models := make([]*ai.Model, 0, len(raw))
	provSeen := make(map[string]bool)
	providers := make([]string, 0, 8)
	for _, mdl := range raw {
		if mdl == nil {
			continue
		}
		models = append(models, mdl)
		p := string(mdl.Provider)
		if !provSeen[p] {
			provSeen[p] = true
			providers = append(providers, p)
		}
	}
	sort.SliceStable(providers, func(i, j int) bool {
		ri, rj := wakuProviderRank(providers[i]), wakuProviderRank(providers[j])
		if ri != rj {
			return ri < rj
		}
		return providers[i] < providers[j]
	})
	return models, providers
}

func wakuFilterPickerModels(models []*ai.Model, favs map[string]bool, tab, search string) []*ai.Model {
	q := strings.ToLower(strings.TrimSpace(search))
	tokens := strings.Fields(q)
	out := make([]*ai.Model, 0, len(models))
	for _, mdl := range models {
		if mdl == nil {
			continue
		}
		if len(tokens) > 0 {
			haystack := strings.ToLower(mdl.Name + " " + mdl.Id + " " + string(mdl.Provider))
			matched := true
			for _, tok := range tokens {
				if !strings.Contains(haystack, tok) {
					matched = false
					break
				}
			}
			if matched {
				out = append(out, mdl)
			}
			continue
		}
		if tab == "favorites" {
			if favs[selModelKey(string(mdl.Provider), mdl.Id)] {
				out = append(out, mdl)
			}
			continue
		}
		if tab == "" || string(mdl.Provider) == tab {
			out = append(out, mdl)
		}
	}
	return out
}

func wakuFormatModelSubtitle(sess *core.AgentSession, mdl *ai.Model) string {
	if mdl == nil {
		return ""
	}
	parts := []string{wakuProviderDisplayName(sess, string(mdl.Provider))}
	if mdl.ContextWindow > 0 {
		parts = append(parts, FormatTokens(mdl.ContextWindow)+" ctx")
	}
	if mdl.Reasoning {
		parts = append(parts, "reasoning")
	}
	if slices.Contains(mdl.Input, ai.ModelModalityImage) {
		parts = append(parts, "vision")
	}
	return strings.Join(parts, " · ")
}

// wakuModelPickerState tracks the anchored Composer Model Picker Popover's active
// provider rail tab, query draft, keyboard highlight index, and persisted favorites.
type wakuModelPickerState struct {
	mu                 sync.Mutex
	favorites          map[string]bool
	selectedTab        string
	highlightIdx       int
	pendingResetSearch bool

	// UI-thread only fields consumed by TextInputBase during View frames.
	searchDraft string
	lastSearch  string
}

func newWakuModelPickerState(prefs *wakuLayoutPrefs) *wakuModelPickerState {
	st := &wakuModelPickerState{
		favorites: make(map[string]bool),
	}
	if prefs != nil {
		for _, k := range prefs.FavoriteModels {
			k = strings.TrimSpace(k)
			if k != "" {
				st.favorites[k] = true
			}
		}
	}
	return st
}

func (p *wakuModelPickerState) favoriteKeys() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.favorites) == 0 {
		return nil
	}
	keys := make([]string, 0, len(p.favorites))
	for k, on := range p.favorites {
		if on {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

func (p *wakuModelPickerState) isFavorite(key string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.favorites[key]
}

func (p *wakuModelPickerState) toggleFavorite(key string) {
	if p == nil || strings.TrimSpace(key) == "" {
		return
	}
	p.mu.Lock()
	if p.favorites[key] {
		delete(p.favorites, key)
	} else {
		p.favorites[key] = true
	}
	p.mu.Unlock()
}

func (p *wakuModelPickerState) prepareOnOpen(sess *core.AgentSession) {
	if p == nil {
		return
	}
	models, providers := wakuCollectPickerCatalog(sess)
	curProv, curID := "", ""
	if sess != nil {
		if mdl := sess.Model(); mdl != nil {
			curProv = string(mdl.Provider)
			curID = mdl.Id
		}
	}
	tab := curProv
	if tab == "" || !slices.Contains(providers, tab) {
		if len(providers) > 0 {
			tab = providers[0]
		} else {
			tab = "favorites"
		}
	}

	hl := 0
	tabModels := wakuFilterPickerModels(models, nil, tab, "")
	for i, mdl := range tabModels {
		if mdl != nil && string(mdl.Provider) == curProv && mdl.Id == curID {
			hl = i
			break
		}
	}

	p.mu.Lock()
	p.selectedTab = tab
	p.highlightIdx = hl
	p.pendingResetSearch = true
	p.mu.Unlock()
}

func (p *wakuModelPickerState) snapshot() (tab string, hl int, favs map[string]bool, resetSearch bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tab = p.selectedTab
	hl = p.highlightIdx
	resetSearch = p.pendingResetSearch
	p.pendingResetSearch = false
	favs = make(map[string]bool, len(p.favorites))
	for k, on := range p.favorites {
		if on {
			favs[k] = true
		}
	}
	return tab, hl, favs, resetSearch
}

func (p *wakuModelPickerState) selectTab(tab string) {
	p.mu.Lock()
	p.selectedTab = tab
	p.highlightIdx = 0
	p.mu.Unlock()
}

func (p *wakuModelPickerState) setHighlight(idx int) {
	p.mu.Lock()
	p.highlightIdx = idx
	p.mu.Unlock()
}

func (p *wakuModelPickerState) stepHighlight(delta, count int) {
	if count <= 0 {
		return
	}
	p.mu.Lock()
	next := (p.highlightIdx + delta) % count
	if next < 0 {
		next += count
	}
	p.highlightIdx = next
	p.mu.Unlock()
}

func (p *wakuModelPickerState) cycleTab(providers []string, dir int) {
	tabs := make([]string, 0, 1+len(providers))
	tabs = append(tabs, "favorites")
	tabs = append(tabs, providers...)
	if len(tabs) == 0 {
		return
	}
	p.mu.Lock()
	cur := slices.Index(tabs, p.selectedTab)
	if cur < 0 {
		cur = 0
	}
	next := (cur + dir) % len(tabs)
	if next < 0 {
		next += len(tabs)
	}
	p.selectedTab = tabs[next]
	p.highlightIdx = 0
	p.mu.Unlock()
}

func (p *wakuModelPickerState) highlightedOrFirst(models []*ai.Model) *ai.Model {
	if len(models) == 0 {
		return nil
	}
	p.mu.Lock()
	idx := p.highlightIdx
	p.mu.Unlock()
	if idx < 0 || idx >= len(models) {
		idx = 0
	}
	return models[idx]
}

const (
	wakuFooterProjectChipMaxWidth   float32 = 190
	wakuFooterWorkspaceChipMaxWidth float32 = 180
	wakuFooterBranchChipMaxWidth    float32 = 210
	wakuProjectPickerPopoverWidth   float32 = 300
	wakuProjectPickerRowHeight      float32 = 26
	wakuWorkspacePickerPopoverWidth float32 = 236
	wakuBranchPickerPopoverWidth    float32 = 360
)

type wakuWorkspacePickerState struct {
	mu                  sync.Mutex
	recentProjects      []string
	cachedProjects      []string
	projectSearch       string
	lastProjectSearch   string
	projectHighlightIdx int
	branchSearch        string
	lastBranchSearch    string
	branchHighlightIdx  int
	branchCreating      bool
	newBranchName       string
}

func newWakuWorkspacePickerState(prefs *wakuLayoutPrefs) *wakuWorkspacePickerState {
	wp := &wakuWorkspacePickerState{}
	if prefs != nil && len(prefs.RecentProjects) > 0 {
		seen := make(map[string]bool, len(prefs.RecentProjects))
		for _, p := range prefs.RecentProjects {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			p = filepath.Clean(p)
			norm := wakuResolvePath(p)
			if !seen[norm] {
				seen[norm] = true
				wp.recentProjects = append(wp.recentProjects, p)
			}
		}
	}
	return wp
}

func (w *wakuWorkspacePickerState) recentProjectsCopy() []string {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.recentProjects...)
}

func (w *wakuWorkspacePickerState) recordProject(dir string) {
	if w == nil {
		return
	}
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		return
	}
	norm := wakuResolvePath(dir)
	w.mu.Lock()
	defer w.mu.Unlock()
	next := make([]string, 0, len(w.recentProjects)+1)
	next = append(next, dir)
	for _, prev := range w.recentProjects {
		if wakuResolvePath(prev) == norm {
			continue
		}
		next = append(next, prev)
		if len(next) >= 24 {
			break
		}
	}
	w.recentProjects = next
}

func (w *wakuWorkspacePickerState) prepareProjectPopoverOpen(activeProject string, sessions []core.SessionInfo) {
	if w == nil {
		return
	}
	w.mu.Lock()
	recentCopy := append([]string(nil), w.recentProjects...)
	w.projectSearch = ""
	w.lastProjectSearch = ""
	w.projectHighlightIdx = 0
	w.mu.Unlock()

	seen := make(map[string]bool)
	var valid []string
	addCand := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		cleaned := filepath.Clean(raw)
		norm := wakuResolvePath(cleaned)
		if seen[norm] {
			return
		}
		if st, err := os.Stat(cleaned); err != nil || !st.IsDir() {
			return
		}
		seen[norm] = true
		valid = append(valid, cleaned)
	}

	if activeProject != "" {
		addCand(activeProject)
	}
	for _, r := range recentCopy {
		addCand(r)
	}
	for _, s := range sessions {
		cwd := strings.TrimSpace(s.Cwd)
		if cwd == "" {
			continue
		}
		if isLinked, mainRoot, _ := wakuDetectLinkedWorktreeQuick(cwd); isLinked && mainRoot != "" {
			cwd = mainRoot
		}
		addCand(cwd)
	}

	w.mu.Lock()
	w.cachedProjects = valid
	w.mu.Unlock()
}

func (w *wakuWorkspacePickerState) prepareBranchPopoverOpen() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.branchSearch = ""
	w.lastBranchSearch = ""
	w.branchHighlightIdx = 0
	w.branchCreating = false
	w.newBranchName = ""
	w.mu.Unlock()
}

func wakuStepCircularIndex(cur, delta, count int) int {
	if count <= 0 {
		return 0
	}
	next := (cur + delta) % count
	if next < 0 {
		next += count
	}
	return next
}

func (w *wakuWorkspacePickerState) stepProjectHighlight(delta, count int) {
	w.mu.Lock()
	w.projectHighlightIdx = wakuStepCircularIndex(w.projectHighlightIdx, delta, count)
	w.mu.Unlock()
}

func (w *wakuWorkspacePickerState) stepBranchHighlight(delta, count int) {
	w.mu.Lock()
	w.branchHighlightIdx = wakuStepCircularIndex(w.branchHighlightIdx, delta, count)
	w.mu.Unlock()
}

func (w *wakuWorkspacePickerState) enterBranchCreateMode(prefill string) {
	w.mu.Lock()
	w.branchCreating = true
	if strings.TrimSpace(prefill) != "" {
		w.newBranchName = strings.TrimSpace(prefill)
	}
	w.mu.Unlock()
}

func (w *wakuWorkspacePickerState) exitBranchCreateMode() {
	w.mu.Lock()
	w.branchCreating = false
	w.mu.Unlock()
}

func wakuProjectDisplayFolderName(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "workspace"
	}
	base := filepath.Base(filepath.Clean(dir))
	if base != "" && base != "." && base != "/" {
		return base
	}
	return utils.ShortenHomePath(dir)
}

func wakuFilterProjectEntries(projects []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return append([]string(nil), projects...)
	}
	tokens := strings.Fields(q)
	var out []string
	for _, p := range projects {
		base := strings.ToLower(wakuProjectDisplayFolderName(p))
		short := strings.ToLower(utils.ShortenHomePath(p))
		matched := true
		for _, tok := range tokens {
			if !strings.Contains(base, tok) && !strings.Contains(short, tok) {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, p)
		}
	}
	return out
}

func wakuFilterBranchNames(branches []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return append([]string(nil), branches...)
	}
	var out []string
	for _, br := range branches {
		if strings.Contains(strings.ToLower(br), q) {
			out = append(out, br)
		}
	}
	return out
}

func wakuCloseOtherPopovers(keep *bool, all ...*bool) {
	for _, p := range all {
		if p != nil && p != keep {
			*p = false
		}
	}
}

// ToggleModelPickerPopover opens or closes Waku's anchored 460x390 Model Picker Popover
// on the main workspace (or falls back to ShowModelSelector while the full-window Settings view is open).
func (m *GuiMode) ToggleModelPickerPopover() {
	if m == nil {
		return
	}
	if m.SettingsOpen() {
		m.ShowModelSelector()
		return
	}
	m.ensureWakuState()
	sess := m.Session()
	m.mu.Lock()
	next := !m.modelPopoverOpen
	m.modelPopoverOpen = next
	if next {
		m.thinkingPopoverOpen = false
		m.projectPopoverOpen = false
		m.worktreePopoverOpen = false
		m.branchPopoverOpen = false
		m.contextPopoverOpen = false
	}
	m.mu.Unlock()
	if next {
		m.modelPicker.prepareOnOpen(sess)
	}
	if m.host != nil {
		m.host.RequestRender()
	}
}

type wakuQueueRowAction struct {
	kind       string // "edit", "steer", "remove"
	isSteering bool
	index      int
}

type wakuComposerFrameActions struct {
	queueAct       wakuQueueRowAction
	pickedModel    *ai.Model
	favToggled     bool
	attachClicked  bool
	stopClicked    bool
	sendClicked    bool
	followClicked  bool
	pickedThinking *agent.ThinkingLevel
	compactClicked bool
	switchProject  string
	addNewProject  bool
	createWorktree bool
	switchBranch   string
	createBranch   string
}

// renderWakuComposerZone renders the centered MaxWidth(720) bottom composer zone:
// above-editor extension widgets, optional inset docked queue tray, 13px floating Waku
// composer card (chromeless gui.Editor + bottom inline model/thinking/attach/stop-send
// controls), 28px workspace footer with upward Project, Workspace, Git Branch & Context
// Usage popovers, and below-editor widgets.
func (m *GuiMode) renderWakuComposerZone(c *ui.Context, th *gui.GuiTheme) {
	if m == nil {
		return
	}
	m.ensureWakuState()

	sess := m.Session()
	st := m.buildSafeFooterState(sess)

	m.mu.Lock()
	steering := append([]string(nil), m.steeringQueue...)
	followUp := append([]string(nil), m.followUpQueue...)
	widgets := append([]gmWidgetItem(nil), m.widgets...)
	bashRunning := m.bashRunning
	ctxPopoverOpen := m.contextPopoverOpen
	projectPopoverOpen := m.projectPopoverOpen
	worktreePopoverOpen := m.worktreePopoverOpen
	branchPopoverOpen := m.branchPopoverOpen
	thinkingPopoverOpen := m.thinkingPopoverOpen
	modelPopoverOpen := m.modelPopoverOpen
	m.mu.Unlock()

	if m.editor != nil && !m.editor.Chromeless() {
		m.editor.SetChromeless(true)
	}

	hasQueue := len(steering) > 0 || len(followUp) > 0
	isBusy := st.IsStreaming || st.IsCompacting || st.IsRetrying || bashRunning

	var act wakuComposerFrameActions

	// Outer 20px window gutter sits outside MaxWidth(720) so card & footer align flush at 720px.
	outer := ui.Column(c).
		Key("waku-composer-outer").
		FillWidth().
		Padding(0, 20, 0, 20).
		AlignItems(ui.Center)

	outer.Children(func() {
		col := ui.Column(c).
			FillWidth().
			MaxWidth(wakuContentMaxWidth).
			Gap(0)

		col.Children(func() {
			if wakuHasWidgets(widgets, core.WidgetPlacementAboveEditor) {
				ui.Column(c).Key("waku-widgets-above").FillWidth().Gap(4).Margin(0, 0, 6, 0).Children(func() {
					gmRenderWidgets(c, th, widgets, core.WidgetPlacementAboveEditor)
				})
			}

			if hasQueue {
				act.queueAct = m.renderWakuQueueTray(c, th, steering, followUp)
			}

			// Floating 13px Waku Composer Card (all 4 corners stay 13px rounded, zero drop shadow)
			borderCol := th.Border
			if m.editor != nil && m.editor.BashMode() {
				borderCol = th.Warning.Alpha(0.75)
			}
			card := ui.Column(c).
				Key("waku-composer-card").
				FillWidth().
				Background(th.ComposerBg).
				Border(1, borderCol).
				Radius(wakuComposerRadius).
				Padding(10, 10, 10, 10).
				Gap(8).
				Role(ui.RoleRegion).
				Label("Composer Card")

			card.Children(func() {
				if m.editor != nil {
					ui.Box(c).FillWidth().Padding(0, 4, 0, 4).Children(func() {
						m.editor.View(c)
					})
				}

				ui.Row(c).
					FillWidth().
					Height(26).
					AlignItems(ui.Center).
					Justify(ui.SpaceBetween).
					Gap(8).
					Children(func() {
						// Left controls: Model Picker chip, Reasoning chip, Clipboard Attach, Bash indicator
						ui.Row(c).Shrink(1).AlignItems(ui.Center).Gap(4).Children(func() {
							modelLbl := wakuModelChipLabel(sess, st)
							provSvg, provCol := wakuProviderIconAndColor(st.Provider, th)

							modelBtn := ui.ButtonBase(c).
								Key("composer-model-chip").
								Height(24).
								Padding(0, 7, 0, 7).
								Radius(6).
								AlignItems(ui.Center).
								Gap(6).
								Cursor(ui.CursorPointer).
								Label("Select Model Chip")
							modelFg := th.TextSecondary
							if modelPopoverOpen {
								modelBtn.Background(th.OverlayStrong)
								modelFg = th.Text
							} else if modelBtn.Hovered() {
								modelBtn.Background(th.Overlay)
								modelFg = th.Text
							}
							modelBtn.Children(func() {
								gui.IconView(c, provSvg, 13, provCol)
								ui.Text(c, modelLbl).TextColor(modelFg).FontSize(12.5).SingleLine()
							})
							if modelBtn.Clicked() {
								modelPopoverOpen = !modelPopoverOpen
								if modelPopoverOpen {
									m.modelPicker.prepareOnOpen(sess)
									wakuCloseOtherPopovers(&modelPopoverOpen, &modelPopoverOpen, &thinkingPopoverOpen, &projectPopoverOpen, &worktreePopoverOpen, &branchPopoverOpen, &ctxPopoverOpen)
								}
							}

							act.pickedModel, act.favToggled = m.renderWakuModelPickerPopover(c, th, sess, modelBtn, &modelPopoverOpen)

							thinkLbl := wakuThinkingLabel(st.ThinkingLevel)
							thinkBtn := ui.ButtonBase(c).
								Key("composer-thinking-chip").
								Height(24).
								Padding(0, 7, 0, 7).
								Radius(6).
								AlignItems(ui.Center).
								Cursor(ui.CursorPointer).
								Label("Select Thinking Chip")
							thinkFg := th.TextSecondary
							if thinkingPopoverOpen {
								thinkBtn.Background(th.OverlayStrong)
								thinkFg = th.Text
							} else if thinkBtn.Hovered() {
								thinkBtn.Background(th.Overlay)
								thinkFg = th.Text
							}
							thinkBtn.Children(func() {
								ui.Text(c, thinkLbl).TextColor(thinkFg).FontSize(12.5).SingleLine()
							})
							if thinkBtn.Clicked() {
								thinkingPopoverOpen = !thinkingPopoverOpen
								if thinkingPopoverOpen {
									wakuCloseOtherPopovers(&thinkingPopoverOpen, &modelPopoverOpen, &thinkingPopoverOpen, &projectPopoverOpen, &worktreePopoverOpen, &branchPopoverOpen, &ctxPopoverOpen)
								}
							}

							act.pickedThinking = m.renderWakuThinkingPopover(c, th, sess, st, thinkBtn, &thinkingPopoverOpen)

							attBtn := ui.ButtonBase(c).
								Key("composer-attach-btn").
								Size(24, 24).
								Radius(6).
								AlignItems(ui.Center).
								Justify(ui.Center).
								Cursor(ui.CursorPointer).
								Label("Attach clipboard image")
							attFg := th.TextTertiary
							if attBtn.Hovered() {
								attBtn.Background(th.Overlay)
								attFg = th.TextSecondary
							}
							attBtn.Children(func() {
								gui.IconView(c, gui.IconPlus, 13, attFg)
							})
							if attBtn.Clicked() {
								act.attachClicked = true
							}

							if m.editor != nil && m.editor.BashMode() {
								modeTxt := "! bash"
								if m.editor.BashExcludedFromContext() {
									modeTxt = "!! bash"
								}
								ui.Box(c).
									Background(th.Warning.Alpha(0.15)).
									Border(1, th.Warning.Alpha(0.5)).
									Radius(999).
									Padding(2, 8).
									Children(func() {
										ui.Text(c, modeTxt).TextColor(th.Warning).FontSize(11).FontWeight(600).SingleLine()
									})
							}
						})

						// Right circular Stop / Queue Follow-up / Send actions
						hasDraft := m.editor != nil && (strings.TrimSpace(m.editor.Text()) != "" || len(m.editor.Attachments()) > 0)
						ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Children(func() {
							if isBusy {
								stopBtn := ui.ButtonBase(c).
									Key("composer-stop-btn").
									Size(26, 26).
									Radius(999).
									Background(th.OverlayStrong).
									AlignItems(ui.Center).
									Justify(ui.Center).
									Cursor(ui.CursorPointer).
									Label("Stop")
								stopFg := th.Text
								if stopBtn.Hovered() {
									stopBtn.Background(th.Error.Alpha(0.18))
									stopFg = th.Error
								}
								stopBtn.Children(func() {
									gui.IconView(c, gui.IconStop, 11, stopFg)
								})
								if stopBtn.Clicked() {
									act.stopClicked = true
								}

								if hasDraft {
									qBtn := ui.ButtonBase(c).
										Key("composer-queue-btn").
										Size(26, 26).
										Radius(999).
										Background(th.Inverse).
										AlignItems(ui.Center).
										Justify(ui.Center).
										Cursor(ui.CursorPointer).
										Label("Queue Follow-up")
									qBtn.Children(func() {
										gui.IconView(c, gui.IconArrowUp, 15, th.OnInverse)
									})
									if qBtn.Clicked() {
										act.followClicked = true
									}
								}
							} else {
								sendBg := th.OverlayStrong
								sendFg := th.TextGhost
								if hasDraft {
									sendBg = th.Inverse
									sendFg = th.OnInverse
								}
								sendBtn := ui.ButtonBase(c).
									Key("composer-send-btn").
									Size(26, 26).
									Radius(999).
									Background(sendBg).
									AlignItems(ui.Center).
									Justify(ui.Center).
									Cursor(ui.CursorPointer).
									Label("Send")
								sendBtn.Children(func() {
									gui.IconView(c, gui.IconArrowUp, 15, sendFg)
								})
								if sendBtn.Clicked() {
									act.sendClicked = true
								}
							}
						})
					})
			})

			m.renderWakuWorkspaceFooter(
				c, th, st, sess != nil, isBusy,
				&projectPopoverOpen, &worktreePopoverOpen, &branchPopoverOpen,
				&ctxPopoverOpen, &modelPopoverOpen, &thinkingPopoverOpen,
				&act,
			)

			if wakuHasWidgets(widgets, core.WidgetPlacementBelowEditor) {
				ui.Column(c).Key("waku-widgets-below").FillWidth().Gap(4).Margin(6, 0, 0, 0).Children(func() {
					gmRenderWidgets(c, th, widgets, core.WidgetPlacementBelowEditor)
				})
			}
		})
	})

	m.mu.Lock()
	m.contextPopoverOpen = ctxPopoverOpen
	m.projectPopoverOpen = projectPopoverOpen
	m.worktreePopoverOpen = worktreePopoverOpen
	m.branchPopoverOpen = branchPopoverOpen
	m.thinkingPopoverOpen = thinkingPopoverOpen
	m.modelPopoverOpen = modelPopoverOpen
	m.mu.Unlock()

	if act.pickedModel != nil {
		m.applySelectedModel(act.pickedModel, nil)
	}
	if act.favToggled {
		m.saveLayoutPrefs()
	}
	if act.pickedThinking != nil && sess != nil {
		sess.SetThinkingLevel(*act.pickedThinking)
		m.updateFooterAndTitle()
	}
	switch act.queueAct.kind {
	case "remove":
		m.removeQueuedMessage(act.queueAct.isSteering, act.queueAct.index)
	case "edit":
		m.editQueuedMessage(act.queueAct.isSteering, act.queueAct.index)
	case "steer":
		m.steerQueuedFollowUp(act.queueAct.index)
	}
	if act.attachClicked {
		m.pasteImageFromClipboard()
	}
	if act.stopClicked {
		m.handleEscapeAbort()
	}
	if act.followClicked && m.editor != nil {
		m.handleEditorSubmit(gui.EditorSubmitEvent{
			Text:               m.editor.Text(),
			Attachments:        m.editor.Attachments(),
			BashMode:           m.editor.BashMode(),
			ExcludeFromContext: m.editor.BashExcludedFromContext(),
		}, true)
	}
	if act.sendClicked && m.editor != nil {
		m.handleEditorSubmit(gui.EditorSubmitEvent{
			Text:               m.editor.Text(),
			Attachments:        m.editor.Attachments(),
			BashMode:           m.editor.BashMode(),
			ExcludeFromContext: m.editor.BashExcludedFromContext(),
		}, false)
	}
	if act.compactClicked {
		go m.runCompactCommand(m.currentCtx(), "")
	}
	if act.addNewProject {
		m.promptAddNewProject()
	}
	if act.createWorktree {
		m.createAndSwitchGitWorktree()
	}
	if act.switchProject != "" {
		m.switchProjectDirectory(act.switchProject)
	}
	if act.createBranch != "" {
		m.createAndCheckoutGitBranch(act.createBranch)
	} else if act.switchBranch != "" {
		m.checkoutGitBranch(act.switchBranch)
	}
}

func (m *GuiMode) renderWakuQueueTray(c *ui.Context, th *gui.GuiTheme, steering, followUp []string) wakuQueueRowAction {
	var action wakuQueueRowAction
	ui.Box(c).FillWidth().Padding(0, 14, 0, 14).Children(func() {
		tray := ui.Column(c).
			Key("waku-queue-tray").
			FillWidth().
			Background(th.ComposerBg).
			Border(1, th.Border).
			Radius(12, 12, 0, 0).
			Clip().
			Gap(0).
			Role(ui.RoleRegion).
			Label("Queued Messages Tray")

		tray.Children(func() {
			rowNum := 0
			renderItem := func(isSteering bool, idx int, text string) {
				rowNum++
				n := rowNum
				row := ui.Row(c).
					Key(fmt.Sprintf("queue-row-%t-%d", isSteering, idx)).
					FillWidth().
					Height(30).
					Padding(0, 6, 0, 12).
					AlignItems(ui.Center).
					Gap(9)

				var editBtn, steerBtn, remBtn ui.Element
				row.Children(func() {
					gui.IconView(c, gui.IconQueue, 12, th.TextTertiary)

					editBtn = ui.ButtonBase(c).
						Key(fmt.Sprintf("queue-edit-%d", n)).
						Grow(1).
						Shrink(1).
						MinWidth(0).
						FillHeight().
						Padding(0, 4, 0, 0).
						Justify(ui.Start).
						AlignItems(ui.Center).
						Cursor(ui.CursorPointer).
						Label(fmt.Sprintf("Edit queued message %d", n))
					editBtn.Children(func() {
						ui.Text(c, msgSingleLinePreview(text, 92)).
							TextColor(th.Text).
							FontSize(12.5).
							SingleLine()
					})
					if editBtn.Clicked() {
						action = wakuQueueRowAction{kind: "edit", isSteering: isSteering, index: idx}
					}

					ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(2).Children(func() {
						if !isSteering {
							steerBtn = ui.ButtonBase(c).
								Key(fmt.Sprintf("queue-steer-%d", n)).
								Height(24).
								Padding(0, 7, 0, 7).
								Radius(6).
								AlignItems(ui.Center).
								Gap(5).
								Cursor(ui.CursorPointer).
								Label(fmt.Sprintf("Steer queued message %d", n))
							if steerBtn.Hovered() {
								steerBtn.Background(th.OverlayStrong)
							}
							steerBtn.Children(func() {
								gui.IconView(c, gui.IconCornerDownRight, 11, th.TextSecondary)
								ui.Text(c, "Steer").TextColor(th.TextSecondary).FontSize(12).SingleLine()
							})
							if steerBtn.Clicked() {
								action = wakuQueueRowAction{kind: "steer", isSteering: false, index: idx}
							}
						} else {
							ui.Box(c).Padding(0, 6, 0, 6).Children(func() {
								ui.Text(c, "Steer").TextColor(th.TextTertiary).FontSize(11).SingleLine()
							})
						}

						remBtn = ui.ButtonBase(c).
							Key(fmt.Sprintf("queue-rem-%d", n)).
							Size(24, 24).
							Radius(6).
							AlignItems(ui.Center).
							Justify(ui.Center).
							Cursor(ui.CursorPointer).
							Label(fmt.Sprintf("Remove queued message %d", n))
						if remBtn.Hovered() {
							remBtn.Background(th.OverlayStrong)
						}
						remBtn.Children(func() {
							gui.IconView(c, gui.IconTrash, 12, th.TextSecondary)
						})
						if remBtn.Clicked() {
							action = wakuQueueRowAction{kind: "remove", isSteering: isSteering, index: idx}
						}
					})
				})

				if editBtn.Hovered() || steerBtn.Hovered() || remBtn.Hovered() {
					row.Background(th.Overlay)
				}
			}

			for idx, text := range steering {
				renderItem(true, idx, text)
			}
			for idx, text := range followUp {
				renderItem(false, idx, text)
			}
		})
	})
	return action
}

func (m *GuiMode) renderWakuModelPickerPopover(
	c *ui.Context,
	th *gui.GuiTheme,
	sess *core.AgentSession,
	modelBtn ui.Element,
	open *bool,
) (picked *ai.Model, favToggled bool) {
	mp := m.modelPicker
	if mp == nil || !*open {
		return nil, false
	}

	ui.PopoverBase(c, modelBtn, open, func(panel ui.Element) {
		panel.AttachTo(modelBtn, ui.AnchorTopStart, ui.AnchorBottomStart).
			Margin(0, 0, 4, 0).
			Width(460).
			Height(390).
			Radius(13).
			Clip().
			Border(1, th.BorderStrong).
			Background(th.Raised).
			Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.38)).
			Role(ui.RoleDialog).
			Label("Model Picker Popover")

		panel.Children(func() {
			selectedTab, hlIdx, favSnap, resetSearch := mp.snapshot()
			if resetSearch {
				mp.searchDraft = ""
				mp.lastSearch = ""
			}

			models, providers := wakuCollectPickerCatalog(sess)
			curProv, curID := "", ""
			if sess != nil {
				if cur := sess.Model(); cur != nil {
					curProv = string(cur.Provider)
					curID = cur.Id
				}
			}

			ui.Row(c).Fill().Gap(0).Children(func() {
				searching := strings.TrimSpace(mp.searchDraft) != ""

				// Left 50px Provider & Favorites rail (49px canvas + 1px border)
				ui.Row(c).
					Width(50).
					FillHeight().
					Shrink(0).
					Gap(0).
					Role(ui.RoleToolbar).
					Label("Model Provider Rail").
					Children(func() {
						ui.Column(c).
							Width(49).
							FillHeight().
							Padding(5).
							Gap(4).
							AlignItems(ui.Center).
							Background(th.Background).
							Children(func() {
								favSelected := selectedTab == "favorites" && !searching
								favTabBtn := ui.ButtonBase(c).
									Key("mp-tab-favorites").
									Size(38, 38).
									Radius(7).
									AlignItems(ui.Center).
									Justify(ui.Center).
									Cursor(ui.CursorPointer).
									Label("Filter Favorite Models")
								favIconCol := th.TextTertiary
								if favSelected {
									favTabBtn.Background(th.OverlayStrong)
									favIconCol = th.Text
								} else if favTabBtn.Hovered() {
									favTabBtn.Background(th.Overlay)
								}
								favTabBtn.Children(func() {
									gui.IconView(c, gui.IconStar, 17, favIconCol)
								})
								if favTabBtn.Clicked() {
									mp.selectTab("favorites")
									selectedTab = "favorites"
									hlIdx = 0
								}

								ui.Box(c).Size(34, 1).Shrink(0).Margin(3, 0, 3, 0).Background(th.Border)

								ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
									ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
										for _, prov := range providers {
											pName := prov
											pSelected := selectedTab == pName && !searching
											pBtn := ui.ButtonBase(c).
												Key("mp-tab-"+pName).
												Size(38, 38).
												Radius(7).
												AlignItems(ui.Center).
												Justify(ui.Center).
												Cursor(ui.CursorPointer).
												Label("Provider " + pName)
											pSvg, pCol := wakuProviderIconAndColor(pName, th)
											if pSelected {
												pBtn.Background(th.OverlayStrong)
											} else if pBtn.Hovered() {
												pBtn.Background(th.Overlay)
											} else {
												pCol = pCol.Alpha(0.82)
											}
											pBtn.Children(func() {
												gui.IconView(c, pSvg, 18, pCol)
											})
											if pBtn.Clicked() {
												mp.selectTab(pName)
												selectedTab = pName
												hlIdx = 0
											}
										}
									})
								})
							})

						ui.Box(c).Width(1).FillHeight().Shrink(0).Background(th.Border)
					})

				// Right Surface pane: 52px search bar + scrollable 58px model rows
				ui.Column(c).
					Grow(1).
					Shrink(1).
					MinWidth(0).
					FillHeight().
					Background(th.Surface).
					Gap(0).
					Children(func() {
						var searchIn ui.Element
						ui.Row(c).
							FillWidth().
							Height(52).
							Shrink(0).
							Padding(10, 12, 8, 12).
							AlignItems(ui.Center).
							Children(func() {
								ui.Row(c).
									Key("mp-search-pill").
									FillWidth().
									Height(34).
									Padding(0, 10, 0, 10).
									Radius(9).
									Background(th.Raised).
									AlignItems(ui.Center).
									Gap(8).
									Children(func() {
										gui.IconView(c, gui.IconSearch, 15, th.TextSecondary)
										searchIn = ui.TextInputBase(c, &mp.searchDraft).
											Grow(1).
											Shrink(1).
											MinWidth(0).
											FontSize(13).
											TextColor(th.Text).
											Placeholder("Search models…").
											Label("Search Models").
											AutoFocus()
									})
							})

						if mp.searchDraft != mp.lastSearch {
							mp.lastSearch = mp.searchDraft
							hlIdx = 0
							mp.setHighlight(0)
						}

						visible := wakuFilterPickerModels(models, favSnap, selectedTab, mp.searchDraft)
						if len(visible) > 0 && (hlIdx < 0 || hlIdx >= len(visible)) {
							hlIdx = 0
							mp.setHighlight(0)
						}

						searchIn.OnTextKey(func(k ui.TextKey) bool {
							switch {
							case k.Key == ui.KeyDown && k.Mods == 0:
								mp.stepHighlight(1, len(visible))
								if m.host != nil {
									m.host.RequestRender()
								}
								return true
							case k.Key == ui.KeyUp && k.Mods == 0:
								mp.stepHighlight(-1, len(visible))
								if m.host != nil {
									m.host.RequestRender()
								}
								return true
							case k.Key == ui.KeyTab && (k.Mods == 0 || k.Mods == ui.Shift):
								if strings.TrimSpace(k.Text()) == "" {
									dir := 1
									if k.Mods&ui.Shift != 0 {
										dir = -1
									}
									mp.cycleTab(providers, dir)
									if m.host != nil {
										m.host.RequestRender()
									}
								}
								return true
							case k.Key == ui.KeyEnter && k.Mods == 0:
								if chosen := mp.highlightedOrFirst(visible); chosen != nil {
									m.mu.Lock()
									m.modelPopoverOpen = false
									m.mu.Unlock()
									m.applySelectedModel(chosen, nil)
									if m.host != nil {
										m.host.RequestRender()
									}
								}
								return true
							case k.Key == ui.KeyEscape && k.Mods == 0:
								if strings.TrimSpace(k.Text()) != "" {
									k.SetText("")
									mp.mu.Lock()
									mp.lastSearch = ""
									mp.highlightIdx = 0
									mp.mu.Unlock()
									if m.host != nil {
										m.host.RequestRender()
									}
									return true
								}
								m.mu.Lock()
								m.modelPopoverOpen = false
								m.mu.Unlock()
								if m.host != nil {
									m.host.RequestRender()
								}
								return true
							}
							return false
						})

						ui.Scroll(c).
							Key("model-picker-list").
							FillWidth().
							Grow(1).
							MinHeight(0).
							Padding(9).
							Children(func() {
								if len(visible) == 0 {
									emptyTxt := "No models available for this provider"
									if strings.TrimSpace(mp.searchDraft) != "" {
										emptyTxt = "No matching models"
									} else if selectedTab == "favorites" {
										emptyTxt = "Star models to pin them to Favorites"
									}
									ui.Box(c).FillWidth().Padding(32, 16, 32, 16).Center().Children(func() {
										ui.Text(c, emptyTxt).TextColor(th.TextGhost).FontSize(12.5).SingleLine()
									})
									return
								}

								ui.Column(c).FillWidth().Gap(2).Children(func() {
									for idx, mdl := range visible {
										mItem := mdl
										key := selModelKey(string(mItem.Provider), mItem.Id)
										dispName := strings.TrimSpace(mItem.Name)
										if dispName == "" {
											dispName = mItem.Id
										}
										subtitle := wakuFormatModelSubtitle(sess, mItem)
										isCurrent := string(mItem.Provider) == curProv && mItem.Id == curID
										isHighlighted := len(visible) > 0 && idx == hlIdx
										isFav := favSnap[key]

										row := ui.Row(c).
											Key("mp-row-"+key).
											FillWidth().
											Height(58).
											Padding(0, 12, 0, 12).
											Radius(9).
											AlignItems(ui.Center).
											Gap(10)

										var selBtn, favBtn ui.Element
										row.Children(func() {
											selBtn = ui.ButtonBase(c).
												Key("mp-sel-" + key).
												Grow(1).
												Shrink(1).
												MinWidth(0).
												FillHeight().
												Justify(ui.Start).
												AlignItems(ui.Center).
												Cursor(ui.CursorPointer).
												Label("Choose Model " + dispName)
											selBtn.Children(func() {
												ui.Column(c).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Start).Justify(ui.Center).Gap(4).Children(func() {
													ui.Text(c, dispName).
														TextColor(th.Text).
														FontSize(13).
														FontWeight(600).
														SingleLine()
													ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(6).Children(func() {
														rSvg, rCol := wakuProviderIconAndColor(string(mItem.Provider), th)
														gui.IconView(c, rSvg, 10.5, rCol.Alpha(0.85))
														ui.Text(c, subtitle).
															TextColor(th.TextTertiary).
															FontSize(12.5).
															SingleLine()
													})
												})
											})
											if selBtn.Clicked() {
												picked = mItem
												*open = false
											}

											favBtn = ui.ButtonBase(c).
												Key("mp-fav-"+key).
												Size(28, 28).
												Shrink(0).
												Radius(6).
												AlignItems(ui.Center).
												Justify(ui.Center).
												Cursor(ui.CursorPointer).
												Label("Favorite Model " + key)
											if favBtn.Hovered() {
												favBtn.Background(th.OverlayStrong)
											}
											starSvg := gui.IconStar
											starCol := th.TextGhost
											if isFav {
												starSvg = gui.IconStarFilled
												starCol = ui.RGB(0xE0, 0xB3, 0x6A)
											}
											favBtn.Children(func() {
												gui.IconView(c, starSvg, 14, starCol)
											})
											if favBtn.Clicked() {
												mp.toggleFavorite(key)
												favToggled = true
											}
										})

										if isHighlighted {
											bg := th.Overlay
											if isCurrent {
												bg = th.OverlayStrong
											}
											row.Background(bg).Border(1, th.Accent)
										} else if isCurrent {
											row.Background(th.OverlayStrong).Border(1, ui.Color{})
										} else if selBtn.Hovered() || favBtn.Hovered() {
											row.Background(th.Overlay).Border(1, ui.Color{})
										} else {
											row.Border(1, ui.Color{})
										}
									}
								})
							})
					})
			})
		})
	})

	return picked, favToggled
}

func (m *GuiMode) renderWakuThinkingPopover(
	c *ui.Context,
	th *gui.GuiTheme,
	sess *core.AgentSession,
	st FooterState,
	thinkBtn ui.Element,
	open *bool,
) *agent.ThinkingLevel {
	var picked *agent.ThinkingLevel
	ui.PopoverBase(c, thinkBtn, open, func(panel ui.Element) {
		panel.AttachTo(thinkBtn, ui.AnchorTopStart, ui.AnchorBottomStart).
			Margin(0, 0, 4, 0).
			MinWidth(212).
			Background(th.Raised).
			Border(1, th.BorderStrong).
			Radius(10).
			Padding(6).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.34))
		panel.Children(func() {
			levels := core.ThinkingLevelOptions
			if sess != nil {
				if avail := sess.GetAvailableThinkingLevels(); len(avail) > 0 {
					levels = avail
				}
			}
			defaultLvl := core.DefaultThinkingLevel
			if sess != nil && sess.SettingsManager != nil {
				if tl := sess.SettingsManager.GetDefaultThinkingLevel(); tl != nil && *tl != "" {
					defaultLvl = *tl
				}
			}
			hasDefault := false
			for _, l := range levels {
				if l == defaultLvl {
					hasDefault = true
					break
				}
			}
			if !hasDefault && len(levels) > 0 {
				defaultLvl = levels[0]
			}

			ui.Column(c).FillWidth().Gap(2).Children(func() {
				ui.Box(c).Padding(4, 8, 4, 8).Children(func() {
					ui.Text(c, "Reasoning").TextColor(th.TextTertiary).FontSize(11.5).FontWeight(500).SingleLine()
				})
				for _, lvl := range levels {
					lvlCopy := lvl
					dispLbl := wakuThinkingLabel(lvlCopy)
					isSelected := lvlCopy == st.ThinkingLevel
					isDefault := lvlCopy == defaultLvl
					itemBtn := ui.ButtonBase(c).
						FillWidth().
						Height(28).
						Padding(0, 8, 0, 8).
						Radius(6).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween).
						Gap(10).
						Cursor(ui.CursorPointer).
						Label(dispLbl)
					if itemBtn.Hovered() {
						itemBtn.Background(th.Overlay)
					}
					itemFg := th.TextSecondary
					if isSelected {
						itemFg = th.Text
					}
					itemBtn.Children(func() {
						ui.Text(c, dispLbl).TextColor(itemFg).FontSize(12.5).SingleLine()
						ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Children(func() {
							if isDefault {
								ui.Row(c).
									Height(18).
									Padding(0, 5, 0, 5).
									Radius(4).
									Border(1, th.BorderStrong).
									Background(th.Overlay).
									AlignItems(ui.Center).
									Children(func() {
										ui.Text(c, "Default").TextColor(th.TextTertiary).FontSize(11).FontWeight(600).SingleLine()
									})
							}
							if isSelected {
								gui.IconView(c, gui.IconCheck, 11, th.TextTertiary)
							}
						})
					})
					if itemBtn.Clicked() {
						picked = &lvlCopy
						*open = false
					}
				}
			})
		})
	})
	return picked
}

func renderWakuFooterMenuChip(
	c *ui.Context,
	th *gui.GuiTheme,
	key, accessLabel, text string,
	icon *ui.SVG,
	maxW float32,
	isOpen bool,
) ui.Element {
	btn := ui.ButtonBase(c).
		Key(key).
		Height(26).
		MaxWidth(maxW).
		Padding(0, 7, 0, 7).
		Radius(6).
		AlignItems(ui.Center).
		Gap(6).
		Cursor(ui.CursorPointer).
		Label(accessLabel)
	fg := th.TextSecondary
	if isOpen {
		btn.Background(th.OverlayStrong)
		fg = th.Text
	} else if btn.Hovered() {
		btn.Background(th.Overlay)
		fg = th.Text
	}
	btn.Children(func() {
		ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
			gui.IconView(c, icon, 12, th.TextTertiary)
		})
		ui.Text(c, text).TextColor(fg).FontSize(13).SingleLine().Shrink(1).MinWidth(0)
	})
	return btn
}

func (m *GuiMode) renderWakuProjectPickerPopover(
	c *ui.Context,
	th *gui.GuiTheme,
	anchor ui.Element,
	activeProject string,
	open *bool,
	act *wakuComposerFrameActions,
) {
	wp := m.workspacePicker
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).
			Key("project-picker-popover").
			Margin(0, 0, 6, 0).
			Width(wakuProjectPickerPopoverWidth).
			Background(th.Raised).
			Border(1, th.BorderStrong).
			Radius(10).
			Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.36)).
			Padding(6).
			Role(ui.RoleDialog).
			Label("Project Picker Popover")

		panel.Children(func() {
			wp.mu.Lock()
			cached := append([]string(nil), wp.cachedProjects...)
			lastQ := wp.lastProjectSearch
			hlIdx := wp.projectHighlightIdx
			wp.mu.Unlock()

			ui.Column(c).FillWidth().Gap(4).Children(func() {
				var searchIn ui.Element
				ui.Row(c).
					Key("proj-picker-search-pill").
					FillWidth().
					Height(32).
					Padding(0, 10, 0, 10).
					Radius(8).
					Background(th.Surface).
					AlignItems(ui.Center).
					Gap(8).
					Children(func() {
						gui.IconView(c, gui.IconSearch, 14, th.TextSecondary)
						searchIn = ui.TextInputBase(c, &wp.projectSearch).
							Grow(1).
							Shrink(1).
							MinWidth(0).
							FontSize(12.5).
							TextColor(th.Text).
							Placeholder("Search projects…").
							Label("Search Projects").
							AutoFocus()
					})

				if wp.projectSearch != lastQ {
					hlIdx = 0
					wp.mu.Lock()
					wp.lastProjectSearch = wp.projectSearch
					wp.projectHighlightIdx = 0
					wp.mu.Unlock()
				}

				filtered := wakuFilterProjectEntries(cached, wp.projectSearch)
				if len(filtered) > 0 && (hlIdx < 0 || hlIdx >= len(filtered)) {
					hlIdx = 0
					wp.mu.Lock()
					wp.projectHighlightIdx = 0
					wp.mu.Unlock()
				}

				searchIn.OnTextKey(func(k ui.TextKey) bool {
					switch {
					case k.Key == ui.KeyDown && k.Mods == 0:
						wp.stepProjectHighlight(1, len(filtered))
						if m.host != nil {
							m.host.RequestRender()
						}
						return true
					case k.Key == ui.KeyUp && k.Mods == 0:
						wp.stepProjectHighlight(-1, len(filtered))
						if m.host != nil {
							m.host.RequestRender()
						}
						return true
					case k.Key == ui.KeyEnter && k.Mods == 0:
						if len(filtered) > 0 {
							wp.mu.Lock()
							pickIdx := wp.projectHighlightIdx
							wp.mu.Unlock()
							if pickIdx < 0 || pickIdx >= len(filtered) {
								pickIdx = 0
							}
							chosen := filtered[pickIdx]
							m.mu.Lock()
							m.projectPopoverOpen = false
							m.mu.Unlock()
							m.switchProjectDirectory(chosen)
							if m.host != nil {
								m.host.RequestRender()
							}
						}
						return true
					case k.Key == ui.KeyEscape && k.Mods == 0:
						if strings.TrimSpace(k.Text()) != "" {
							k.SetText("")
							wp.mu.Lock()
							wp.projectSearch = ""
							wp.lastProjectSearch = ""
							wp.projectHighlightIdx = 0
							wp.mu.Unlock()
							if m.host != nil {
								m.host.RequestRender()
							}
							return true
						}
						m.mu.Lock()
						m.projectPopoverOpen = false
						m.mu.Unlock()
						if m.host != nil {
							m.host.RequestRender()
						}
						return true
					}
					return false
				})

				baseCounts := make(map[string]int, len(cached))
				for _, p := range cached {
					baseCounts[wakuProjectDisplayFolderName(p)]++
				}

				activeNorm := wakuResolvePath(activeProject)
				ui.Scroll(c).Key("proj-picker-scroll").FillWidth().MaxHeight(220).Children(func() {
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						if len(filtered) == 0 {
							ui.Box(c).FillWidth().Padding(14, 8, 14, 8).Center().Children(func() {
								ui.Text(c, "No matching projects").TextColor(th.TextGhost).FontSize(12).SingleLine()
							})
							return
						}
						for idx, dir := range filtered {
							dirCopy := dir
							name := wakuProjectDisplayFolderName(dirCopy)
							isCur := activeNorm != "" && wakuResolvePath(dirCopy) == activeNorm
							isHL := idx == hlIdx
							showParent := baseCounts[name] > 1

							rowBtn := ui.ButtonBase(c).
								Key(fmt.Sprintf("proj-row-%d-%s", idx, wakuResolvePath(dirCopy))).
								FillWidth().
								Height(wakuProjectPickerRowHeight).
								Padding(0, 8, 0, 8).
								Radius(6).
								AlignItems(ui.Center).
								Justify(ui.SpaceBetween).
								Gap(8).
								Cursor(ui.CursorPointer).
								Label("Open project " + utils.ShortenHomePath(dirCopy))
							if isHL {
								bg := th.Overlay
								if isCur {
									bg = th.OverlayStrong
								}
								rowBtn.Background(bg).Border(1, th.Accent)
							} else if isCur {
								rowBtn.Background(th.OverlayStrong).Border(1, ui.Color{})
							} else if rowBtn.Hovered() {
								rowBtn.Background(th.Overlay).Border(1, ui.Color{})
							} else {
								rowBtn.Border(1, ui.Color{})
							}
							rowBtn.Children(func() {
								ui.Row(c).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Center).Gap(7).Children(func() {
									ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
										gui.IconView(c, gui.IconFolder, 12, th.TextSecondary)
									})
									ui.Text(c, name).TextColor(th.Text).FontSize(12.5).SingleLine().Shrink(1).MinWidth(0)
									if showParent {
										ui.Text(c, utils.ShortenHomePath(filepath.Dir(dirCopy))).
											TextColor(th.TextTertiary).FontSize(11).SingleLine().Shrink(1).MinWidth(0)
									}
								})
								if isCur {
									ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
										gui.IconView(c, gui.IconCheck, 12, th.Accent)
									})
								}
							})
							if rowBtn.Clicked() {
								act.switchProject = dirCopy
								*open = false
							}
						}
					})
				})

				ui.Box(c).FillWidth().Height(1).Background(th.Border).Margin(2, 0, 2, 0)

				newProjBtn := ui.ButtonBase(c).
					Key("proj-picker-new-project").
					FillWidth().
					Height(28).
					Padding(0, 8, 0, 8).
					Radius(6).
					AlignItems(ui.Center).
					Gap(7).
					Cursor(ui.CursorPointer).
					Label("New project…")
				if newProjBtn.Hovered() {
					newProjBtn.Background(th.Overlay)
				}
				newProjBtn.Children(func() {
					ui.Box(c).Size(13, 13).Shrink(0).Center().Children(func() {
						gui.IconView(c, gui.IconFolderNew, 13, th.TextSecondary)
					})
					ui.Text(c, "New project…").TextColor(th.Text).FontSize(12.5).SingleLine()
				})
				if newProjBtn.Clicked() {
					act.addNewProject = true
					*open = false
				}
			})
		})
	})
}

func (m *GuiMode) renderWakuWorkspacePickerPopover(
	c *ui.Context,
	th *gui.GuiTheme,
	anchor ui.Element,
	cwd, mainRepoRoot string,
	isGitRepo, isLinkedWT bool,
	worktrees []wakuGitWorktree,
	open *bool,
	act *wakuComposerFrameActions,
) {
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).
			Key("workspace-picker-popover").
			Margin(0, 0, 6, 0).
			Width(wakuWorkspacePickerPopoverWidth).
			Background(th.Raised).
			Border(1, th.BorderStrong).
			Radius(10).
			Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.36)).
			Padding(6).
			Role(ui.RoleDialog).
			Label("Workspace Picker Popover")

		panel.Children(func() {
			curNorm := wakuResolvePath(cwd)
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				ui.Box(c).Padding(2, 8, 4, 8).Children(func() {
					ui.Text(c, "Work in").TextColor(th.TextTertiary).FontSize(11.5).FontWeight(600).SingleLine()
				})

				localBtn := ui.ButtonBase(c).
					Key("ws-picker-local").
					FillWidth().
					Height(28).
					Padding(0, 8, 0, 8).
					Radius(6).
					AlignItems(ui.Center).
					Justify(ui.SpaceBetween).
					Gap(8).
					Cursor(ui.CursorPointer).
					Label("Local workspace")
				if !isLinkedWT {
					localBtn.Background(th.OverlayStrong)
				} else if localBtn.Hovered() {
					localBtn.Background(th.Overlay)
				}
				localBtn.Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
						ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
							gui.IconView(c, gui.IconLaptop, 12, th.TextSecondary)
						})
						ui.Text(c, "Local").TextColor(th.Text).FontSize(12.5).SingleLine()
					})
					if !isLinkedWT {
						ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
							gui.IconView(c, gui.IconCheck, 12, th.Accent)
						})
					}
				})
				if localBtn.Clicked() {
					if isLinkedWT && mainRepoRoot != "" {
						act.switchProject = mainRepoRoot
					}
					*open = false
				}

				for idx, wt := range worktrees {
					if wt.IsMain || wt.Path == "" {
						continue
					}
					wtCopy := wt
					isCurWT := curNorm != "" && wakuResolvePath(wtCopy.Path) == curNorm
					label := wakuProjectDisplayFolderName(wtCopy.Path)
					wtBtn := ui.ButtonBase(c).
						Key(fmt.Sprintf("ws-picker-wt-%d-%s", idx, wakuResolvePath(wtCopy.Path))).
						FillWidth().
						Height(28).
						Padding(0, 8, 0, 8).
						Radius(6).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween).
						Gap(8).
						Cursor(ui.CursorPointer).
						Label("Worktree " + label)
					if isCurWT {
						wtBtn.Background(th.OverlayStrong)
					} else if wtBtn.Hovered() {
						wtBtn.Background(th.Overlay)
					}
					wtBtn.Children(func() {
						ui.Row(c).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Center).Gap(7).Children(func() {
							ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
								gui.IconView(c, gui.IconFork, 12, th.TextSecondary)
							})
							ui.Text(c, label).TextColor(th.Text).FontSize(12.5).SingleLine().Shrink(1).MinWidth(0)
						})
						if isCurWT {
							ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
								gui.IconView(c, gui.IconCheck, 12, th.Accent)
							})
						}
					})
					if wtBtn.Clicked() {
						if !isCurWT {
							act.switchProject = wtCopy.Path
						}
						*open = false
					}
				}

				if isGitRepo {
					ui.Box(c).FillWidth().Height(1).Background(th.Border).Margin(2, 0, 2, 0)
					newWTBtn := ui.ButtonBase(c).
						Key("ws-picker-new-worktree").
						FillWidth().
						Height(28).
						Padding(0, 8, 0, 8).
						Radius(6).
						AlignItems(ui.Center).
						Gap(7).
						Cursor(ui.CursorPointer).
						Label("New worktree")
					if newWTBtn.Hovered() {
						newWTBtn.Background(th.Overlay)
					}
					newWTBtn.Children(func() {
						ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
							gui.IconView(c, gui.IconFork, 12, th.Accent)
						})
						ui.Text(c, "New worktree").TextColor(th.Text).FontSize(12.5).SingleLine()
					})
					if newWTBtn.Clicked() {
						act.createWorktree = true
						*open = false
					}
				}
			})
		})
	})
}

func (m *GuiMode) renderWakuBranchPickerPopover(
	c *ui.Context,
	th *gui.GuiTheme,
	st FooterState,
	anchor ui.Element,
	open *bool,
	act *wakuComposerFrameActions,
) {
	wp := m.workspacePicker
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).
			Key("branch-picker-popover").
			Margin(0, 0, 6, 0).
			Width(wakuBranchPickerPopoverWidth).
			Background(th.Raised).
			Border(1, th.BorderStrong).
			Radius(10).
			Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.36)).
			Padding(8).
			Role(ui.RoleDialog).
			Label("Branch Picker Popover")

		panel.Children(func() {
			branches, curBr := m.rightPanel.gitBranchesSnapshot()
			wp.mu.Lock()
			creating := wp.branchCreating
			lastQ := wp.lastBranchSearch
			hlIdx := wp.branchHighlightIdx
			wp.mu.Unlock()

			if creating {
				ui.Column(c).FillWidth().Gap(8).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
						backBtn := ui.ButtonBase(c).
							Key("br-create-back").
							Height(24).
							Padding(0, 7, 0, 7).
							Radius(6).
							AlignItems(ui.Center).
							Cursor(ui.CursorPointer).
							Label("Back to branches")
						if backBtn.Hovered() {
							backBtn.Background(th.Overlay)
						}
						backBtn.Children(func() {
							ui.Text(c, "← Back").TextColor(th.TextSecondary).FontSize(12).SingleLine()
						})
						if backBtn.Clicked() {
							wp.exitBranchCreateMode()
						}
						ui.Text(c, "Create & checkout branch").TextColor(th.Text).FontSize(12.5).FontWeight(600).SingleLine()
					})

					var nameIn ui.Element
					ui.Row(c).
						Key("br-create-input-pill").
						FillWidth().
						Height(34).
						Padding(0, 10, 0, 10).
						Radius(8).
						Background(th.Surface).
						Border(1, th.Accent).
						AlignItems(ui.Center).
						Children(func() {
							nameIn = ui.TextInputBase(c, &wp.newBranchName).
								Grow(1).
								Shrink(1).
								MinWidth(0).
								FontSize(12.5).
								TextColor(th.Text).
								Placeholder("feature/new-branch").
								Label("New Branch Name").
								AutoFocus()
						})

					nameIn.OnTextKey(func(k ui.TextKey) bool {
						switch {
						case k.Key == ui.KeyEnter && k.Mods == 0:
							trimmed := strings.TrimSpace(k.Text())
							if trimmed != "" && !strings.ContainsAny(trimmed, " \t\r\n") {
								k.SetText("")
								wp.mu.Lock()
								wp.branchCreating = false
								wp.newBranchName = ""
								wp.mu.Unlock()
								m.mu.Lock()
								m.branchPopoverOpen = false
								m.mu.Unlock()
								m.createAndCheckoutGitBranch(trimmed)
								if m.host != nil {
									m.host.RequestRender()
								}
							}
							return true
						case k.Key == ui.KeyEscape && k.Mods == 0:
							wp.exitBranchCreateMode()
							if m.host != nil {
								m.host.RequestRender()
							}
							return true
						}
						return false
					})

					trimmed := strings.TrimSpace(wp.newBranchName)
					valid := trimmed != "" && !strings.ContainsAny(trimmed, " \t\r\n")
					ui.Row(c).FillWidth().Justify(ui.End).AlignItems(ui.Center).Gap(6).Children(func() {
						cancelBtn := ui.ButtonBase(c).
							Key("br-create-cancel").
							Height(28).
							Padding(0, 10, 0, 10).
							Radius(6).
							AlignItems(ui.Center).
							Cursor(ui.CursorPointer).
							Label("Cancel branch creation")
						if cancelBtn.Hovered() {
							cancelBtn.Background(th.Overlay)
						}
						cancelBtn.Children(func() {
							ui.Text(c, "Cancel").TextColor(th.TextSecondary).FontSize(12.5).SingleLine()
						})
						if cancelBtn.Clicked() {
							wp.exitBranchCreateMode()
						}

						submitBtn := ui.ButtonBase(c).
							Key("br-create-submit").
							Height(28).
							Padding(0, 12, 0, 12).
							Radius(6).
							AlignItems(ui.Center).
							Cursor(ui.CursorPointer).
							Label("Create and checkout branch")
						if valid {
							submitBtn.Background(th.Accent)
						} else {
							submitBtn.Background(th.OverlayStrong).Disabled(true)
						}
						submitBtn.Children(func() {
							fg := th.Text
							if !valid {
								fg = th.TextTertiary
							}
							ui.Text(c, "Create & Checkout").TextColor(fg).FontSize(12.5).FontWeight(500).SingleLine()
						})
						if submitBtn.Clicked() && valid {
							act.createBranch = trimmed
							wp.mu.Lock()
							wp.branchCreating = false
							wp.newBranchName = ""
							wp.mu.Unlock()
							*open = false
						}
					})
				})
				return
			}

			ui.Column(c).FillWidth().Gap(6).Children(func() {
				var searchIn ui.Element
				ui.Row(c).
					Key("br-picker-search-pill").
					FillWidth().
					Height(34).
					Padding(0, 10, 0, 10).
					Radius(8).
					Background(th.Surface).
					AlignItems(ui.Center).
					Gap(8).
					Children(func() {
						gui.IconView(c, gui.IconSearch, 14, th.TextSecondary)
						searchIn = ui.TextInputBase(c, &wp.branchSearch).
							Grow(1).
							Shrink(1).
							MinWidth(0).
							FontSize(12.5).
							TextColor(th.Text).
							Placeholder("Search branches…").
							Label("Search Git Branches").
							AutoFocus()
					})

				if wp.branchSearch != lastQ {
					hlIdx = 0
					wp.mu.Lock()
					wp.lastBranchSearch = wp.branchSearch
					wp.branchHighlightIdx = 0
					wp.mu.Unlock()
				}

				filtered := wakuFilterBranchNames(branches, wp.branchSearch)
				if len(filtered) > 0 && (hlIdx < 0 || hlIdx >= len(filtered)) {
					hlIdx = 0
					wp.mu.Lock()
					wp.branchHighlightIdx = 0
					wp.mu.Unlock()
				}

				searchIn.OnTextKey(func(k ui.TextKey) bool {
					switch {
					case k.Key == ui.KeyDown && k.Mods == 0:
						wp.stepBranchHighlight(1, len(filtered))
						if m.host != nil {
							m.host.RequestRender()
						}
						return true
					case k.Key == ui.KeyUp && k.Mods == 0:
						wp.stepBranchHighlight(-1, len(filtered))
						if m.host != nil {
							m.host.RequestRender()
						}
						return true
					case k.Key == ui.KeyEnter && k.Mods == 0:
						if len(filtered) > 0 {
							wp.mu.Lock()
							pickIdx := wp.branchHighlightIdx
							wp.mu.Unlock()
							if pickIdx < 0 || pickIdx >= len(filtered) {
								pickIdx = 0
							}
							chosen := filtered[pickIdx]
							m.mu.Lock()
							m.branchPopoverOpen = false
							m.mu.Unlock()
							m.checkoutGitBranch(chosen)
							if m.host != nil {
								m.host.RequestRender()
							}
						}
						return true
					case k.Key == ui.KeyEscape && k.Mods == 0:
						if strings.TrimSpace(k.Text()) != "" {
							k.SetText("")
							wp.mu.Lock()
							wp.branchSearch = ""
							wp.lastBranchSearch = ""
							wp.branchHighlightIdx = 0
							wp.mu.Unlock()
							if m.host != nil {
								m.host.RequestRender()
							}
							return true
						}
						m.mu.Lock()
						m.branchPopoverOpen = false
						m.mu.Unlock()
						if m.host != nil {
							m.host.RequestRender()
						}
						return true
					}
					return false
				})

				ui.Text(c, "Branches").TextColor(th.TextTertiary).FontSize(11.5).FontWeight(600).Padding(0, 4, 0, 4).SingleLine()

				ui.Scroll(c).Key("br-picker-scroll").FillWidth().MaxHeight(210).Children(func() {
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						if len(filtered) == 0 {
							emptyMsg := "No local branches found"
							if strings.TrimSpace(wp.branchSearch) != "" {
								emptyMsg = "No matching branches"
							}
							ui.Box(c).FillWidth().Padding(10, 6, 10, 6).Children(func() {
								ui.Text(c, emptyMsg).TextColor(th.TextTertiary).FontSize(12).SingleLine()
							})
							return
						}
						for bIdx, br := range filtered {
							brName := br
							isCur := brName == curBr || (curBr == "" && st.GitBranch != nil && brName == *st.GitBranch)
							isHL := bIdx == hlIdx
							bBtn := ui.ButtonBase(c).
								Key(fmt.Sprintf("git-br-%d-%s", bIdx, brName)).
								FillWidth().
								Height(26).
								Padding(0, 8, 0, 8).
								Radius(6).
								AlignItems(ui.Center).
								Justify(ui.SpaceBetween).
								Gap(8).
								Cursor(ui.CursorPointer).
								Label("Checkout branch " + brName)
							if isHL {
								bg := th.Overlay
								if isCur {
									bg = th.OverlayStrong
								}
								bBtn.Background(bg).Border(1, th.Accent)
							} else if isCur {
								bBtn.Background(th.OverlayStrong).Border(1, ui.Color{})
							} else if bBtn.Hovered() {
								bBtn.Background(th.Overlay).Border(1, ui.Color{})
							} else {
								bBtn.Border(1, ui.Color{})
							}
							bBtn.Children(func() {
								ui.Row(c).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Center).Gap(6).Children(func() {
									ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
										gui.IconView(c, gui.IconGitBranch, 12, th.TextSecondary)
									})
									ui.Text(c, brName).TextColor(th.Text).FontSize(12.5).SingleLine().Shrink(1).MinWidth(0)
								})
								if isCur {
									ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
										gui.IconView(c, gui.IconCheck, 12, th.Accent)
									})
								}
							})
							if bBtn.Clicked() {
								if !isCur {
									act.switchBranch = brName
								}
								*open = false
							}
						}
					})
				})

				ui.Box(c).FillWidth().Height(1).Background(th.Border)

				createBtn := ui.ButtonBase(c).
					Key("br-picker-create-open").
					FillWidth().
					Height(28).
					Padding(0, 8, 0, 8).
					Radius(6).
					AlignItems(ui.Center).
					Gap(6).
					Cursor(ui.CursorPointer).
					Label("Create and checkout new branch…")
				if createBtn.Hovered() {
					createBtn.Background(th.Overlay)
				}
				createBtn.Children(func() {
					ui.Box(c).Size(12, 12).Shrink(0).Center().Children(func() {
						gui.IconView(c, gui.IconPlus, 12, th.Accent)
					})
					ui.Text(c, "Create and checkout new branch…").TextColor(th.Text).FontSize(12.5).SingleLine()
				})
				if createBtn.Clicked() {
					wp.enterBranchCreateMode(wp.branchSearch)
				}
			})
		})
	})
}

func (m *GuiMode) renderWakuWorkspaceFooter(
	c *ui.Context,
	th *gui.GuiTheme,
	st FooterState,
	hasSess, isBusy bool,
	projectPopoverOpen, worktreePopoverOpen, branchPopoverOpen,
	ctxPopoverOpen, modelPopoverOpen, thinkingPopoverOpen *bool,
	act *wakuComposerFrameActions,
) {
	cwd := strings.TrimSpace(st.Cwd)
	if cwd == "" {
		cwd = m.activeWorkspaceCwd()
	}

	loaded, isGitRepo, isLinkedWT, mainRoot, rpBranch, worktrees, _ := m.rightPanel.gitWorkspaceSnapshot(cwd)
	if !loaded {
		if qLinked, qMain, _ := wakuDetectLinkedWorktreeQuick(cwd); qLinked && qMain != "" {
			isLinkedWT = true
			mainRoot = qMain
			isGitRepo = true
		}
	}
	if !isGitRepo && st.GitBranch != nil && strings.TrimSpace(*st.GitBranch) != "" {
		isGitRepo = true
	}

	activeProjectDir := cwd
	if isLinkedWT && mainRoot != "" {
		activeProjectDir = mainRoot
	}
	projectLabel := wakuProjectDisplayFolderName(activeProjectDir)

	wsIcon := gui.IconLaptop
	wsLabel := "Local"
	if isLinkedWT {
		wsIcon = gui.IconFork
		wsLabel = wakuProjectDisplayFolderName(cwd)
	}

	var branchLabel string
	if st.GitBranch != nil && strings.TrimSpace(*st.GitBranch) != "" {
		branchLabel = strings.TrimSpace(*st.GitBranch)
	} else if loaded && strings.TrimSpace(rpBranch) != "" {
		branchLabel = strings.TrimSpace(rpBranch)
	}

	ui.Row(c).
		Key("waku-workspace-footer").
		FillWidth().
		Height(wakuWorkspaceFooterHeight).
		Margin(2, 0, 6, 0).
		Padding(0, 10, 0, 10).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Gap(8).
		Role(ui.RoleRegion).
		Label("Workspace Footer").
		Children(func() {
			// Left Project, Workspace (Local / worktree), Git Branch pickers, and extension statuses
			ui.Row(c).Shrink(1).MinWidth(0).AlignItems(ui.Center).Gap(2).Children(func() {
				projAnchor := renderWakuFooterMenuChip(
					c, th, "footer-project-picker", "Select Project",
					projectLabel, gui.IconFolder, wakuFooterProjectChipMaxWidth, *projectPopoverOpen,
				)
				if projAnchor.Clicked() {
					*projectPopoverOpen = !*projectPopoverOpen
					if *projectPopoverOpen {
						wakuCloseOtherPopovers(projectPopoverOpen, projectPopoverOpen, worktreePopoverOpen, branchPopoverOpen, ctxPopoverOpen, modelPopoverOpen, thinkingPopoverOpen)
						m.workspacePicker.prepareProjectPopoverOpen(activeProjectDir, m.sidebar.sessionsSnapshot())
					}
				}
				m.renderWakuProjectPickerPopover(c, th, projAnchor, activeProjectDir, projectPopoverOpen, act)

				wsAnchor := renderWakuFooterMenuChip(
					c, th, "footer-workspace-picker", "Select Workspace",
					wsLabel, wsIcon, wakuFooterWorkspaceChipMaxWidth, *worktreePopoverOpen,
				)
				if wsAnchor.Clicked() {
					*worktreePopoverOpen = !*worktreePopoverOpen
					if *worktreePopoverOpen {
						wakuCloseOtherPopovers(worktreePopoverOpen, projectPopoverOpen, worktreePopoverOpen, branchPopoverOpen, ctxPopoverOpen, modelPopoverOpen, thinkingPopoverOpen)
						m.refreshGitBranchesAsync()
					}
				}
				m.renderWakuWorkspacePickerPopover(c, th, wsAnchor, cwd, mainRoot, isGitRepo, isLinkedWT, worktrees, worktreePopoverOpen, act)

				if branchLabel != "" {
					brAnchor := renderWakuFooterMenuChip(
						c, th, "footer-git-branch", "Switch Git Branch",
						branchLabel, gui.IconGitBranch, wakuFooterBranchChipMaxWidth, *branchPopoverOpen,
					)
					if brAnchor.Clicked() {
						*branchPopoverOpen = !*branchPopoverOpen
						if *branchPopoverOpen {
							wakuCloseOtherPopovers(branchPopoverOpen, projectPopoverOpen, worktreePopoverOpen, branchPopoverOpen, ctxPopoverOpen, modelPopoverOpen, thinkingPopoverOpen)
							m.workspacePicker.prepareBranchPopoverOpen()
							m.refreshGitBranchesAsync()
						}
					}
					m.renderWakuBranchPickerPopover(c, th, st, brAnchor, branchPopoverOpen, act)
				}

				if len(st.ExtensionStatuses) > 0 {
					keys := make([]string, 0, len(st.ExtensionStatuses))
					for k := range st.ExtensionStatuses {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for idx, k := range keys {
						val := strings.TrimSpace(st.ExtensionStatuses[k])
						if val != "" {
							ui.Box(c).Padding(0, 6, 0, 6).Children(func() {
								ui.Text(c, val).
									Key(fmt.Sprintf("ext-st-%d-%s", idx, k)).
									TextColor(th.TextTertiary).
									FontSize(11).
									SingleLine()
							})
						}
					}
				}
			})

			// Right cost + circular Context Usage gauge opening Context Window popover above-right
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Children(func() {
				if st.TotalCost > 0 {
					ui.Text(c, FormatCost(st.TotalCost)).TextColor(th.TextTertiary).FontSize(11).SingleLine()
				}

				ctxAnchor := ui.ButtonBase(c).
					Key("footer-context-gauge").
					Height(24).
					Padding(0, 7, 0, 7).
					Radius(6).
					AlignItems(ui.Center).
					Gap(5).
					Cursor(ui.CursorPointer).
					Label("Context Window Usage")
				if *ctxPopoverOpen {
					ctxAnchor.Background(th.OverlayStrong)
				} else if ctxAnchor.Hovered() {
					ctxAnchor.Background(th.Overlay)
				}
				ctxAnchor.Children(func() {
					gui.RenderContextGauge(c, st.ContextPercent, st.HasContextPercent, 14, 2.0, th)
					if st.HasContextPercent {
						ui.Textf(c, "%.0f%%", st.ContextPercent).TextColor(th.TextSecondary).FontSize(11).SingleLine()
					}
				})
				if ctxAnchor.Clicked() {
					*ctxPopoverOpen = !*ctxPopoverOpen
					if *ctxPopoverOpen {
						wakuCloseOtherPopovers(ctxPopoverOpen, projectPopoverOpen, worktreePopoverOpen, branchPopoverOpen, ctxPopoverOpen, modelPopoverOpen, thinkingPopoverOpen)
					}
				}

				ui.PopoverBase(c, ctxAnchor, ctxPopoverOpen, func(panel ui.Element) {
					panel.AttachTo(ctxAnchor, ui.AnchorTopEnd, ui.AnchorBottomEnd).
						Margin(0, 0, 6, 0).
						Width(320).
						Background(th.Raised).
						Border(1, th.BorderStrong).
						Radius(12).
						Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.36)).
						Padding(12, 14)
					panel.Children(func() {
						ui.Column(c).FillWidth().Gap(10).Children(func() {
							ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
								ui.Text(c, "Context Window").TextColor(th.Text).FontSize(13).FontWeight(600)
								if st.ContextWindow > 0 && st.HasContextTokens {
									ui.Textf(c, "%.1f%% of %s", st.ContextPercent, FormatTokens(st.ContextWindow)).
										TextColor(th.TextSecondary).FontSize(12)
								} else if st.ContextWindow > 0 {
									ui.Textf(c, "%s max", FormatTokens(st.ContextWindow)).
										TextColor(th.TextSecondary).FontSize(12)
								}
							})

							const trackW float32 = 290
							pct := st.ContextPercent
							if pct < 0 {
								pct = 0
							}
							if pct > 100 {
								pct = 100
							}
							fillW := trackW * float32(pct/100.0)
							barCol := th.Gauge
							if pct >= 90 {
								barCol = th.Error
							} else if pct >= 70 {
								barCol = th.Warning
							}
							ui.Row(c).Width(trackW).Height(6).Radius(999).Background(th.Inset).Children(func() {
								if fillW > 0 {
									ui.Box(c).Width(fillW).Height(6).Radius(999).Background(barCol)
								}
							})

							ui.Column(c).FillWidth().Gap(4).Children(func() {
								wakuPopoverStatRow(c, th, "Context tokens", wakuOptTokens(st.HasContextTokens, st.ContextTokens))
								wakuPopoverStatRow(c, th, "Session input", FormatTokens(st.InputTokens))
								wakuPopoverStatRow(c, th, "Session output", FormatTokens(st.OutputTokens))
								if st.CacheReadTokens > 0 || st.CacheWriteTokens > 0 {
									wakuPopoverStatRow(c, th, "Cache read / write",
										fmt.Sprintf("%s / %s", FormatTokens(st.CacheReadTokens), FormatTokens(st.CacheWriteTokens)))
								}
								wakuPopoverStatRow(c, th, "Session cost", FormatCost(st.TotalCost))
								acStatus := "off"
								if st.AutoCompactEnabled {
									acStatus = "auto"
								}
								wakuPopoverStatRow(c, th, "Auto-compact", acStatus)
							})

							if !isBusy && hasSess {
								ui.Box(c).FillWidth().Height(1).Background(th.Border)
								cmpBtn := ui.ButtonBase(c).
									FillWidth().
									Height(28).
									Radius(7).
									Background(th.Overlay).
									Border(1, th.BorderStrong).
									AlignItems(ui.Center).
									Justify(ui.Center).
									Cursor(ui.CursorPointer).
									Label("Compact Now")
								if cmpBtn.Hovered() {
									cmpBtn.Background(th.OverlayStrong)
								}
								cmpBtn.Children(func() {
									ui.Text(c, "Compact Now").TextColor(th.Text).FontSize(12).FontWeight(500)
								})
								if cmpBtn.Clicked() {
									act.compactClicked = true
									*ctxPopoverOpen = false
								}
							}
						})
					})
				})
			})
		})
}

func wakuHasWidgets(widgets []gmWidgetItem, placement core.WidgetPlacement) bool {
	for _, w := range widgets {
		if w.placement == placement {
			return true
		}
	}
	return false
}

func wakuOptTokens(has bool, n int) string {
	if !has {
		return "—"
	}
	return FormatTokens(n)
}

func wakuPopoverStatRow(c *ui.Context, th *gui.GuiTheme, label, value string) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
		ui.Text(c, label).TextColor(th.TextSecondary).FontSize(12)
		ui.Text(c, value).TextColor(th.Text).Font("monospace").FontSize(12)
	})
}
