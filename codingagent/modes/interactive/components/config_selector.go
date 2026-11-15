// Ported from packages/coding-agent/src/modes/interactive/components/config-selector.ts (pi v1.5.2).

package components

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/tui"
)

// ConfigWriteScope chooses which settings file (/config scope tab) toggles write into.
type ConfigWriteScope string

const (
	ConfigWriteScopeGlobal  ConfigWriteScope = "global"
	ConfigWriteScopeProject ConfigWriteScope = "project"
)

// ScopedResolvedPaths pairs global and project resolved resource sets for /config.
type ScopedResolvedPaths struct {
	Global  core.ResolvedPaths
	Project core.ResolvedPaths
}

type csResourceType string

const (
	csResourceExtensions csResourceType = "extensions"
	csResourceSkills     csResourceType = "skills"
	csResourcePrompts    csResourceType = "prompts"
	csResourceThemes     csResourceType = "themes"
)

var csSubgroupOrder = []csResourceType{
	csResourceExtensions,
	csResourceSkills,
	csResourcePrompts,
	csResourceThemes,
}

type csResourceItem struct {
	Path         string
	DisplayName  string
	Enabled      bool
	ResourceType csResourceType
	Metadata     utils.PathMetadata
}

type csResourceGroup struct {
	Label     string
	Scope     utils.SourceScope
	Origin    utils.SourceOrigin
	Source    string
	Subgroups map[csResourceType][]*csResourceItem
	Items     []*csResourceItem
}

type csFlatEntry struct {
	isGroup    bool
	groupIndex int
	itemIndex  int // relative within filteredGroups[groupIndex].Items when !isGroup
}

func csGetDisplayName(resType csResourceType, p string) string {
	if strings.HasPrefix(p, utils.BuiltinPathPrefix) {
		return p
	}
	fileName := filepath.Base(p)
	parentFolder := filepath.Base(filepath.Dir(p))
	switch resType {
	case csResourceExtensions:
		if parentFolder != "" && parentFolder != "." && parentFolder != "/" && parentFolder != "extensions" {
			return parentFolder + "/" + fileName
		}
		return fileName
	case csResourceSkills:
		if strings.EqualFold(fileName, "SKILL.md") && parentFolder != "" && parentFolder != "." && parentFolder != "/" {
			return parentFolder
		}
		return fileName
	default:
		return fileName
	}
}

func csGetGroupLabel(meta utils.PathMetadata, cwd, agentDir string) string {
	if meta.Scope == utils.SourceScopeTemporary {
		return "temporary (cli)"
	}
	if meta.Origin == utils.SourceOriginPackage {
		if meta.Source != "" {
			return meta.Source
		}
		return "package"
	}
	switch meta.Scope {
	case utils.SourceScopeUser:
		if agentDir != "" {
			return utils.ShortenHomePath(agentDir)
		}
		return "~/" + utils.ConfigDirName + "/agent"
	case utils.SourceScopeProject:
		if cwd != "" {
			return filepath.Base(cwd) + "/" + utils.ConfigDirName
		}
		return utils.ConfigDirName
	default:
		return string(meta.Scope)
	}
}

func csScopeRank(s utils.SourceScope) int {
	switch s {
	case utils.SourceScopeUser:
		return 0
	case utils.SourceScopeProject:
		return 1
	default:
		return 2
	}
}

func csOriginRank(o utils.SourceOrigin) int {
	if o == utils.SourceOriginPackage {
		return 0
	}
	return 1
}

// ResourceList renders grouped extension/skill/prompt/theme resources and toggles their enabled state.
type ResourceList struct {
	mu                  sync.Mutex
	scopedResolvedPaths ScopedResolvedPaths
	settingsManager     *core.SettingsManager
	cwd                 string
	agentDir            string
	writeScope          ConfigWriteScope
	searchQuery         string
	maxVisibleLines     int

	groups         []*csResourceGroup
	filteredGroups []*csResourceGroup
	flatEntries    []csFlatEntry
	selectedFlat   int
	scrollOffset   int
	focused        bool

	OnCancel             func()
	OnExit               func()
	OnProjectToggleError func(message string)
	OnChange             func()
}

func csNewResourceList(
	scopedResolvedPaths ScopedResolvedPaths,
	settingsManager *core.SettingsManager,
	cwd string,
	agentDir string,
	writeScope ConfigWriteScope,
	terminalHeight int,
) *ResourceList {
	if writeScope != ConfigWriteScopeProject {
		writeScope = ConfigWriteScopeGlobal
	}
	maxVisible := max(5, (terminalHeight - 10))
	rl := &ResourceList{
		scopedResolvedPaths: scopedResolvedPaths,
		settingsManager:     settingsManager,
		cwd:                 cwd,
		agentDir:            agentDir,
		writeScope:          writeScope,
		maxVisibleLines:     maxVisible,
	}
	rl.mu.Lock()
	rl.buildGroupsLocked()
	rl.applyFilterLocked()
	rl.mu.Unlock()
	return rl
}

func (r *ResourceList) buildGroupsLocked() {
	resolved := r.scopedResolvedPaths.Global
	if r.writeScope == ConfigWriteScopeProject {
		resolved = r.scopedResolvedPaths.Project
	}

	groupMap := make(map[string]*csResourceGroup)
	addBucket := func(resType csResourceType, items []core.ResolvedResource) {
		for _, res := range items {
			key := string(res.Metadata.Origin) + ":" + string(res.Metadata.Scope) + ":" + res.Metadata.Source
			grp, exists := groupMap[key]
			if !exists {
				grp = &csResourceGroup{
					Label:     csGetGroupLabel(res.Metadata, r.cwd, r.agentDir),
					Scope:     res.Metadata.Scope,
					Origin:    res.Metadata.Origin,
					Source:    res.Metadata.Source,
					Subgroups: make(map[csResourceType][]*csResourceItem),
				}
				groupMap[key] = grp
			}
			item := &csResourceItem{
				Path:         res.Path,
				DisplayName:  csGetDisplayName(resType, res.Path),
				Enabled:      res.Enabled,
				ResourceType: resType,
				Metadata:     res.Metadata,
			}
			grp.Subgroups[resType] = append(grp.Subgroups[resType], item)
		}
	}

	addBucket(csResourceExtensions, resolved.Extensions)
	addBucket(csResourceSkills, resolved.Skills)
	addBucket(csResourcePrompts, resolved.Prompts)
	addBucket(csResourceThemes, resolved.Themes)

	groups := make([]*csResourceGroup, 0, len(groupMap))
	for _, grp := range groupMap {
		var flatItems []*csResourceItem
		for _, subType := range csSubgroupOrder {
			subItems := grp.Subgroups[subType]
			if len(subItems) == 0 {
				continue
			}
			slices.SortStableFunc(subItems, func(a, b *csResourceItem) int {
				return js.LocaleCompare(a.DisplayName, b.DisplayName)
			})
			flatItems = append(flatItems, subItems...)
		}
		grp.Items = flatItems
		groups = append(groups, grp)
	}

	slices.SortStableFunc(groups, func(a, b *csResourceGroup) int {
		if oa, ob := csOriginRank(a.Origin), csOriginRank(b.Origin); oa != ob {
			return oa - ob
		}
		if sa, sb := csScopeRank(a.Scope), csScopeRank(b.Scope); sa != sb {
			return sa - sb
		}
		return js.LocaleCompare(a.Label, b.Label)
	})

	r.groups = groups
}

func (r *ResourceList) applyFilterLocked() {
	query := strings.ToLower(strings.TrimSpace(r.searchQuery))
	r.filteredGroups = nil
	r.flatEntries = nil

	for _, grp := range r.groups {
		var matched []*csResourceItem
		for _, item := range grp.Items {
			if query == "" ||
				strings.Contains(strings.ToLower(item.DisplayName), query) ||
				strings.Contains(strings.ToLower(item.Path), query) ||
				strings.Contains(strings.ToLower(grp.Label), query) {
				matched = append(matched, item)
			}
		}
		if len(matched) == 0 {
			continue
		}
		filteredGrp := &csResourceGroup{
			Label:  grp.Label,
			Scope:  grp.Scope,
			Origin: grp.Origin,
			Source: grp.Source,
			Items:  matched,
		}
		gIdx := len(r.filteredGroups)
		r.filteredGroups = append(r.filteredGroups, filteredGrp)
		r.flatEntries = append(r.flatEntries, csFlatEntry{isGroup: true, groupIndex: gIdx})
		for itemIdx := range matched {
			r.flatEntries = append(r.flatEntries, csFlatEntry{isGroup: false, groupIndex: gIdx, itemIndex: itemIdx})
		}
	}

	r.ensureValidSelectionLocked()
}

func (r *ResourceList) ensureValidSelectionLocked() {
	if len(r.flatEntries) == 0 {
		r.selectedFlat = 0
		r.scrollOffset = 0
		return
	}
	if r.selectedFlat < 0 {
		r.selectedFlat = 0
	}
	if r.selectedFlat >= len(r.flatEntries) {
		r.selectedFlat = len(r.flatEntries) - 1
	}
	if r.flatEntries[r.selectedFlat].isGroup {
		found := false
		for i := r.selectedFlat + 1; i < len(r.flatEntries); i++ {
			if !r.flatEntries[i].isGroup {
				r.selectedFlat = i
				found = true
				break
			}
		}
		if !found {
			for i := 0; i < len(r.flatEntries); i++ {
				if !r.flatEntries[i].isGroup {
					r.selectedFlat = i
					break
				}
			}
		}
	}
	r.adjustScrollLocked()
}

func (r *ResourceList) adjustScrollLocked() {
	maxVis := max(1, r.maxVisibleLines)
	if len(r.flatEntries) <= maxVis {
		r.scrollOffset = 0
		return
	}
	if r.selectedFlat < r.scrollOffset {
		r.scrollOffset = r.selectedFlat
	} else if r.selectedFlat >= r.scrollOffset+maxVis {
		r.scrollOffset = r.selectedFlat - maxVis + 1
	}
	if r.scrollOffset > len(r.flatEntries)-maxVis {
		r.scrollOffset = len(r.flatEntries) - maxVis
	}
	if r.scrollOffset < 0 {
		r.scrollOffset = 0
	}
}

func (r *ResourceList) moveSelectionLocked(delta int) {
	if len(r.flatEntries) == 0 {
		return
	}
	var itemIndices []int
	curPos := -1
	for i, e := range r.flatEntries {
		if !e.isGroup {
			if i == r.selectedFlat {
				curPos = len(itemIndices)
			}
			itemIndices = append(itemIndices, i)
		}
	}
	if len(itemIndices) == 0 {
		return
	}
	if curPos < 0 {
		curPos = 0
	}
	nextPos := (curPos + delta + len(itemIndices)) % len(itemIndices)
	r.selectedFlat = itemIndices[nextPos]
	r.adjustScrollLocked()
}

// Focused reports whether the resource list holds input focus.
func (r *ResourceList) Focused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.focused
}

// SetFocused sets focus state on the resource list.
func (r *ResourceList) SetFocused(focused bool) {
	r.mu.Lock()
	r.focused = focused
	r.mu.Unlock()
}

// SetSearchQuery updates the search filter and refreshes visible rows.
func (r *ResourceList) SetSearchQuery(query string) {
	r.mu.Lock()
	r.searchQuery = query
	r.applyFilterLocked()
	r.mu.Unlock()
}

// GetSearchQuery returns the active search filter text.
func (r *ResourceList) GetSearchQuery() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.searchQuery
}

// SetWriteScope switches between global and project scope views and reloads groups.
func (r *ResourceList) SetWriteScope(scope ConfigWriteScope) {
	if scope != ConfigWriteScopeProject {
		scope = ConfigWriteScopeGlobal
	}
	r.mu.Lock()
	if r.writeScope == scope {
		r.mu.Unlock()
		return
	}
	r.writeScope = scope
	r.buildGroupsLocked()
	r.applyFilterLocked()
	r.mu.Unlock()
}

// GetWriteScope returns the active settings write scope.
func (r *ResourceList) GetWriteScope() ConfigWriteScope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeScope
}

// UpdateData replaces the scoped resolved paths and rebuilds the displayed list.
func (r *ResourceList) UpdateData(scoped ScopedResolvedPaths) {
	r.mu.Lock()
	r.scopedResolvedPaths = scoped
	r.buildGroupsLocked()
	r.applyFilterLocked()
	r.mu.Unlock()
}

// Invalidate resets any cached layout state.
func (r *ResourceList) Invalidate() {}

// Render formats visible group headers and selectable resource toggles constrained to width.
func (r *ResourceList) Render(width int) []string {
	r.mu.Lock()
	flat := slices.Clone(r.flatEntries)
	groups := slices.Clone(r.filteredGroups)
	selected := r.selectedFlat
	scroll := r.scrollOffset
	maxVis := max(1, r.maxVisibleLines)
	r.mu.Unlock()

	th := theme.CurrentTheme()
	if len(flat) == 0 {
		return []string{tui.TruncateToWidth(th.Fg(theme.FgMuted, "  No matching resources found."), width, "…", false)}
	}

	end := min(len(flat), scroll+maxVis)
	lines := make([]string, 0, end-scroll)
	for i := scroll; i < end; i++ {
		entry := flat[i]
		grp := groups[entry.groupIndex]
		if entry.isGroup {
			header := fmt.Sprintf("  %s", grp.Label)
			lines = append(lines, tui.TruncateToWidth(th.Bold(th.Fg(theme.FgAccent, header)), width, "…", false))
			continue
		}
		item := grp.Items[entry.itemIndex]
		isSelected := i == selected

		cursor := "  "
		if isSelected {
			cursor = th.Fg(theme.FgAccent, "› ")
		}
		check := th.Fg(theme.FgDim, "[ ]")
		if item.Enabled {
			check = th.Fg(theme.FgSuccess, "[✓]")
		}
		typeBadge := th.Fg(theme.FgMuted, fmt.Sprintf("(%s)", item.ResourceType))
		name := item.DisplayName
		if isSelected {
			name = th.Bold(name)
		}
		line := cursor + "  " + check + " " + name + " " + typeBadge
		lines = append(lines, tui.TruncateToWidth(line, width, "…", false))
	}
	return lines
}

// HandleInput processes up/down navigation and Space/Enter toggle keys.
func (r *ResourceList) HandleInput(keyData string) {
	kb := tui.GetKeybindings()

	if kb.Matches(keyData, tui.KeybindingTuiSelectUp) {
		r.mu.Lock()
		r.moveSelectionLocked(-1)
		r.mu.Unlock()
		return
	}
	if kb.Matches(keyData, tui.KeybindingTuiSelectDown) {
		r.mu.Lock()
		r.moveSelectionLocked(1)
		r.mu.Unlock()
		return
	}
	if kb.Matches(keyData, tui.KeybindingTuiSelectConfirm) || keyData == " " {
		r.toggleSelected()
		return
	}
	if kb.Matches(keyData, tui.KeybindingTuiSelectCancel) {
		r.mu.Lock()
		onCancel := r.OnCancel
		r.mu.Unlock()
		if onCancel != nil {
			onCancel()
		}
		return
	}
	if tui.MatchesKey(keyData, tui.KeyCtrl("c")) {
		r.mu.Lock()
		onExit := r.OnExit
		r.mu.Unlock()
		if onExit != nil {
			onExit()
		}
	}
}

func (r *ResourceList) toggleSelected() {
	r.mu.Lock()
	if r.selectedFlat < 0 || r.selectedFlat >= len(r.flatEntries) {
		r.mu.Unlock()
		return
	}
	entry := r.flatEntries[r.selectedFlat]
	if entry.isGroup || entry.groupIndex >= len(r.filteredGroups) {
		r.mu.Unlock()
		return
	}
	grp := r.filteredGroups[entry.groupIndex]
	if entry.itemIndex >= len(grp.Items) {
		r.mu.Unlock()
		return
	}
	filteredItem := grp.Items[entry.itemIndex]
	itemPath := filteredItem.Path
	resType := filteredItem.ResourceType
	meta := filteredItem.Metadata
	nextEnabled := !filteredItem.Enabled
	sm := r.settingsManager
	writeScope := r.writeScope
	cwd := r.cwd
	agentDir := r.agentDir
	onErr := r.OnProjectToggleError
	onChange := r.OnChange
	r.mu.Unlock()

	if sm != nil && meta.Scope != utils.SourceScopeTemporary {
		if err := csApplyToggleSetting(sm, writeScope, cwd, agentDir, itemPath, resType, meta, nextEnabled); err != nil {
			if onErr != nil {
				onErr(err.Error())
			}
			return
		}
	}

	r.mu.Lock()
	// Sync enabled state back into underlying scopedResolvedPaths and rebuilt groups.
	r.updateItemEnabledLocked(writeScope, itemPath, resType, meta, nextEnabled)
	r.buildGroupsLocked()
	r.applyFilterLocked()
	r.mu.Unlock()

	if onChange != nil {
		onChange()
	}
}

func (r *ResourceList) updateItemEnabledLocked(scope ConfigWriteScope, targetPath string, resType csResourceType, meta utils.PathMetadata, enabled bool) {
	targetKey := utils.GetResourcePathKey(targetPath)
	updateSlice := func(items []core.ResolvedResource) {
		for i := range items {
			if utils.GetResourcePathKey(items[i].Path) == targetKey &&
				items[i].Metadata.Origin == meta.Origin &&
				items[i].Metadata.Scope == meta.Scope &&
				items[i].Metadata.Source == meta.Source {
				items[i].Enabled = enabled
			}
		}
	}
	var rp *core.ResolvedPaths
	if scope == ConfigWriteScopeProject {
		rp = &r.scopedResolvedPaths.Project
	} else {
		rp = &r.scopedResolvedPaths.Global
	}
	switch resType {
	case csResourceExtensions:
		updateSlice(rp.Extensions)
	case csResourceSkills:
		updateSlice(rp.Skills)
	case csResourcePrompts:
		updateSlice(rp.Prompts)
	case csResourceThemes:
		updateSlice(rp.Themes)
	}
}

func csResolveBaseDir(meta utils.PathMetadata, scope ConfigWriteScope, cwd, agentDir string) string {
	if meta.BaseDir != "" {
		return meta.BaseDir
	}
	if scope == ConfigWriteScopeProject || meta.Scope == utils.SourceScopeProject {
		if cwd != "" {
			return filepath.Join(cwd, utils.ConfigDirName)
		}
		return utils.ConfigDirName
	}
	if agentDir != "" {
		return agentDir
	}
	return cwd
}

func csPatternForPath(targetPath, baseDir string) string {
	if strings.HasPrefix(targetPath, utils.BuiltinPathPrefix) || baseDir == "" {
		return targetPath
	}
	rel, err := filepath.Rel(baseDir, targetPath)
	if err == nil && rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel)
	}
	return targetPath
}

func csPatternMatchesTarget(pattern, baseDir, targetKey string) (isExcluded bool, matches bool) {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return false, false
	}
	if strings.HasPrefix(trimmed, "!") {
		isExcluded = true
		trimmed = strings.TrimSpace(trimmed[1:])
	} else if strings.HasPrefix(trimmed, "+") {
		trimmed = strings.TrimSpace(trimmed[1:])
	}
	if strings.HasPrefix(trimmed, utils.BuiltinPathPrefix) {
		return isExcluded, trimmed == targetKey
	}
	resolved, err := utils.ResolvePath(trimmed, baseDir, nil)
	if err != nil {
		return isExcluded, trimmed == targetKey
	}
	return isExcluded, utils.GetResourcePathKey(resolved) == targetKey
}

func csTogglePatternSlice(current []string, targetPath, baseDir string, enable bool) []string {
	targetKey := utils.GetResourcePathKey(targetPath)
	relPattern := csPatternForPath(targetPath, baseDir)

	var out []string
	foundPositive := false
	for _, entry := range current {
		_, matches := csPatternMatchesTarget(entry, baseDir, targetKey)
		if matches {
			if !strings.HasPrefix(strings.TrimSpace(entry), "!") && enable {
				if !foundPositive {
					out = append(out, entry)
					foundPositive = true
				}
			}
			continue
		}
		out = append(out, entry)
	}

	if enable {
		if !foundPositive {
			out = append(out, relPattern)
		}
	} else {
		out = append(out, "!"+relPattern)
	}
	return out
}

func csApplyToggleSetting(
	sm *core.SettingsManager,
	writeScope ConfigWriteScope,
	cwd string,
	agentDir string,
	targetPath string,
	resType csResourceType,
	meta utils.PathMetadata,
	enable bool,
) error {
	baseDir := csResolveBaseDir(meta, writeScope, cwd, agentDir)
	isProject := writeScope == ConfigWriteScopeProject

	var settings core.Settings
	if isProject {
		settings = sm.GetProjectSettings()
	} else {
		settings = sm.GetGlobalSettings()
	}

	if meta.Origin == utils.SourceOriginPackage && meta.Source != "" {
		pkgBase := meta.BaseDir
		if pkgBase == "" {
			pkgBase = baseDir
		}
		packages := slices.Clone(settings.Packages)
		idx := -1
		for i, pkg := range packages {
			if (pkg.String != nil && *pkg.String == meta.Source) ||
				(pkg.Object != nil && pkg.Object.Source == meta.Source) {
				idx = i
				break
			}
		}
		var obj core.PackageSourceObject
		if idx >= 0 && packages[idx].Object != nil {
			obj = *packages[idx].Object
		} else {
			obj = core.PackageSourceObject{Source: meta.Source}
		}

		switch resType {
		case csResourceExtensions:
			obj.Extensions = csTogglePatternSlice(obj.Extensions, targetPath, pkgBase, enable)
		case csResourceSkills:
			obj.Skills = csTogglePatternSlice(obj.Skills, targetPath, pkgBase, enable)
		case csResourcePrompts:
			obj.Prompts = csTogglePatternSlice(obj.Prompts, targetPath, pkgBase, enable)
		case csResourceThemes:
			obj.Themes = csTogglePatternSlice(obj.Themes, targetPath, pkgBase, enable)
		}

		updated := core.PackageSourceFromObject(obj)
		if idx >= 0 {
			packages[idx] = updated
		} else {
			packages = append(packages, updated)
		}
		if isProject {
			return sm.SetProjectPackages(packages)
		}
		sm.SetPackages(packages)
		return nil
	}

	switch resType {
	case csResourceExtensions:
		updated := csTogglePatternSlice(settings.Extensions, targetPath, baseDir, enable)
		if isProject {
			return sm.SetProjectExtensionPaths(updated)
		}
		sm.SetExtensionPaths(updated)
	case csResourceSkills:
		updated := csTogglePatternSlice(settings.Skills, targetPath, baseDir, enable)
		if isProject {
			return sm.SetProjectSkillPaths(updated)
		}
		sm.SetSkillPaths(updated)
	case csResourcePrompts:
		updated := csTogglePatternSlice(settings.Prompts, targetPath, baseDir, enable)
		if isProject {
			return sm.SetProjectPromptTemplatePaths(updated)
		}
		sm.SetPromptTemplatePaths(updated)
	case csResourceThemes:
		updated := csTogglePatternSlice(settings.Themes, targetPath, baseDir, enable)
		if isProject {
			return sm.SetProjectThemePaths(updated)
		}
		sm.SetThemePaths(updated)
	}
	return nil
}

// ConfigSelectorComponent displays local extensions, skills, prompts, and themes for /config.
type ConfigSelectorComponent struct {
	*tui.Container

	resourceList *ResourceList
	searchInput  *tui.Input
	headerText   *tui.Text
	statusText   *tui.Text

	mu                   sync.Mutex
	writeScope           ConfigWriteScope
	projectModeAvailable bool
	focused              bool
	onClose              func()
	onExit               func()
	requestRender        func()
}

// NewConfigSelectorComponent constructs the /config resource selector panel.
func NewConfigSelectorComponent(
	resolvedPaths ScopedResolvedPaths,
	settingsManager *core.SettingsManager,
	cwd string,
	agentDir string,
	onClose func(),
	onExit func(),
	requestRender func(),
	terminalHeight int,
	writeScope ConfigWriteScope,
	projectModeAvailable bool,
) *ConfigSelectorComponent {
	if writeScope != ConfigWriteScopeProject || !projectModeAvailable {
		writeScope = ConfigWriteScopeGlobal
	}

	rl := csNewResourceList(resolvedPaths, settingsManager, cwd, agentDir, writeScope, terminalHeight)
	searchInput := tui.NewInput(nil)
	headerText := tui.NewText("", 1, 0, nil)
	statusText := tui.NewText("", 1, 0, nil)

	comp := &ConfigSelectorComponent{
		Container:            tui.NewContainer(),
		resourceList:         rl,
		searchInput:          searchInput,
		headerText:           headerText,
		statusText:           statusText,
		writeScope:           writeScope,
		projectModeAvailable: projectModeAvailable,
		onClose:              onClose,
		onExit:               onExit,
		requestRender:        requestRender,
	}

	rl.OnCancel = onClose
	rl.OnExit = onExit
	rl.OnProjectToggleError = func(msg string) {
		th := theme.CurrentTheme()
		statusText.SetText(th.Fg(theme.FgError, msg))
		if comp.requestRender != nil {
			comp.requestRender()
		}
	}
	rl.OnChange = func() {
		statusText.SetText("")
		if comp.requestRender != nil {
			comp.requestRender()
		}
	}

	comp.updateHeaderText()

	th := theme.CurrentTheme()
	comp.AddChild(NewDynamicBorder())
	comp.AddChild(headerText)
	comp.AddChild(searchInput)
	comp.AddChild(tui.NewText(
		"  "+theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, "toggle")+
			th.Fg(theme.FgMuted, " · ")+
			theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "close"),
		0, 0, nil,
	))
	comp.AddChild(NewDynamicBorder())
	comp.AddChild(tui.NewSpacer(1))
	comp.AddChild(rl)
	comp.AddChild(statusText)
	comp.AddChild(tui.NewSpacer(1))
	comp.AddChild(NewDynamicBorder())

	return comp
}

func (c *ConfigSelectorComponent) updateHeaderText() {
	th := theme.CurrentTheme()
	c.mu.Lock()
	scope := c.writeScope
	projAvail := c.projectModeAvailable
	c.mu.Unlock()

	title := th.Bold(th.Fg(theme.FgAccent, "Resources"))
	if projAvail {
		globalLabel := "Global"
		projectLabel := "Project"
		if scope == ConfigWriteScopeGlobal {
			globalLabel = th.Fg(theme.FgAccent, "◉ Global")
			projectLabel = th.Fg(theme.FgMuted, "○ Project")
		} else {
			globalLabel = th.Fg(theme.FgMuted, "○ Global")
			projectLabel = th.Fg(theme.FgAccent, "◉ Project")
		}
		c.headerText.SetText(title + "  " + globalLabel + th.Fg(theme.FgMuted, " | ") + projectLabel +
			"  " + th.Fg(theme.FgDim, "(Tab to switch scope)"))
	} else {
		c.headerText.SetText(title + "  " + th.Fg(theme.FgMuted, "(Global)"))
	}
}

// GetResourceList returns the underlying ResourceList widget.
func (c *ConfigSelectorComponent) GetResourceList() *ResourceList {
	return c.resourceList
}

// Focused reports whether the config selector holds focus.
func (c *ConfigSelectorComponent) Focused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focused
}

// SetFocused propagates focus to the search input and resource list.
func (c *ConfigSelectorComponent) SetFocused(focused bool) {
	c.mu.Lock()
	c.focused = focused
	c.mu.Unlock()
	c.searchInput.SetFocused(focused)
	c.resourceList.SetFocused(focused)
}

// HandleInput processes global shortcuts (Esc/Ctrl+C/Tab/Up/Down/Enter) and routes typing to search.
func (c *ConfigSelectorComponent) HandleInput(data string) {
	kb := tui.GetKeybindings()

	if tui.MatchesKey(data, tui.KeyCtrl("c")) {
		c.mu.Lock()
		onExit := c.onExit
		c.mu.Unlock()
		if onExit != nil {
			onExit()
		}
		return
	}

	if kb.Matches(data, tui.KeybindingTuiSelectCancel) {
		if c.searchInput.GetValue() != "" {
			c.searchInput.SetValue("")
			c.resourceList.SetSearchQuery("")
			return
		}
		c.mu.Lock()
		onClose := c.onClose
		c.mu.Unlock()
		if onClose != nil {
			onClose()
		}
		return
	}

	if kb.Matches(data, tui.KeybindingTuiInputTab) {
		c.mu.Lock()
		canSwitch := c.projectModeAvailable
		if canSwitch {
			if c.writeScope == ConfigWriteScopeGlobal {
				c.writeScope = ConfigWriteScopeProject
			} else {
				c.writeScope = ConfigWriteScopeGlobal
			}
		}
		nextScope := c.writeScope
		c.mu.Unlock()
		if canSwitch {
			c.resourceList.SetWriteScope(nextScope)
			c.updateHeaderText()
		}
		return
	}

	if kb.Matches(data, tui.KeybindingTuiSelectUp) ||
		kb.Matches(data, tui.KeybindingTuiSelectDown) ||
		kb.Matches(data, tui.KeybindingTuiSelectConfirm) {
		c.resourceList.HandleInput(data)
		return
	}

	c.searchInput.HandleInput(data)
	c.resourceList.SetSearchQuery(c.searchInput.GetValue())
}
