// Ported from packages/coding-agent/src/modes/interactive/components/session-selector.ts (pi v1.5.2).

package components

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/tui"
)

// SessionsLoader loads sessions asynchronously with optional progress callbacks.
type SessionsLoader = func(onProgress core.SessionListProgress, ctx context.Context) ([]core.SessionInfo, error)

// SessionSelectorOptions configures optional rename, hint, and keybinding behavior.
type SessionSelectorOptions struct {
	RenameSession  func(ctx context.Context, sessionPath string, nextName *string) error
	ShowRenameHint *bool
	Keybindings    *core.KeybindingsManager
}

type ssScope string

const (
	ssScopeCurrent ssScope = "current"
	ssScopeAll     ssScope = "all"
)

type ssStatusMessage struct {
	msgType string // "info" | "error"
	message string
}

var ssCtrlCharStripRe = jsre.MustCompile(`[\x00-\x1f\x7f]`, "g")

func ssFormatSessionDate(date time.Time) string {
	diffMs := time.Since(date).Seconds() * 1000
	diffMins := int(math.Floor(diffMs / 60000))
	diffHours := int(math.Floor(diffMs / 3600000))
	diffDays := int(math.Floor(diffMs / 86400000))

	if diffMins < 1 {
		return "now"
	}
	if diffMins < 60 {
		return fmt.Sprintf("%dm", diffMins)
	}
	if diffHours < 24 {
		return fmt.Sprintf("%dh", diffHours)
	}
	if diffDays < 7 {
		return fmt.Sprintf("%dd", diffDays)
	}
	if diffDays < 30 {
		return fmt.Sprintf("%dw", diffDays/7)
	}
	if diffDays < 365 {
		return fmt.Sprintf("%dmo", diffDays/30)
	}
	return fmt.Sprintf("%dy", diffDays/365)
}

func ssGetSessionPathKey(path string) string {
	return utils.GetPathComparisonKey(utils.CanonicalizePath(path))
}

type ssHeader struct {
	mu                   sync.Mutex
	scope                ssScope
	sortMode             SortMode
	nameFilter           NameFilter
	requestRender        func()
	loading              bool
	loadProgress         *[2]int
	showPath             bool
	confirmingDeletePath *string
	statusMessage        *ssStatusMessage
	statusTimer          *time.Timer
	statusGen            uint64
	showRenameHint       bool
}

func ssNewHeader(scope ssScope, sortMode SortMode, nameFilter NameFilter, requestRender func()) *ssHeader {
	return &ssHeader{
		scope:         scope,
		sortMode:      sortMode,
		nameFilter:    nameFilter,
		requestRender: requestRender,
	}
}

func (h *ssHeader) setScope(scope ssScope) {
	h.mu.Lock()
	h.scope = scope
	h.mu.Unlock()
}

func (h *ssHeader) setSortMode(sortMode SortMode) {
	h.mu.Lock()
	h.sortMode = sortMode
	h.mu.Unlock()
}

func (h *ssHeader) setNameFilter(nameFilter NameFilter) {
	h.mu.Lock()
	h.nameFilter = nameFilter
	h.mu.Unlock()
}

func (h *ssHeader) setLoading(loading bool) {
	h.mu.Lock()
	h.loading = loading
	h.loadProgress = nil
	h.mu.Unlock()
}

func (h *ssHeader) setProgress(loaded, total int) {
	h.mu.Lock()
	h.loadProgress = &[2]int{loaded, total}
	h.mu.Unlock()
}

func (h *ssHeader) setShowPath(showPath bool) {
	h.mu.Lock()
	h.showPath = showPath
	h.mu.Unlock()
}

func (h *ssHeader) setShowRenameHint(show bool) {
	h.mu.Lock()
	h.showRenameHint = show
	h.mu.Unlock()
}

func (h *ssHeader) setConfirmingDeletePath(path *string) {
	h.mu.Lock()
	if path == nil {
		h.confirmingDeletePath = nil
	} else {
		p := *path
		h.confirmingDeletePath = &p
	}
	h.mu.Unlock()
}

func (h *ssHeader) clearStatusTimeoutLocked() {
	h.statusGen++
	if h.statusTimer != nil {
		h.statusTimer.Stop()
		h.statusTimer = nil
	}
}

func (h *ssHeader) setStatusMessage(msg *ssStatusMessage, autoHideMs ...time.Duration) {
	h.mu.Lock()
	h.clearStatusTimeoutLocked()
	if msg == nil {
		h.statusMessage = nil
		h.mu.Unlock()
		return
	}
	cp := *msg
	h.statusMessage = &cp
	if len(autoHideMs) > 0 && autoHideMs[0] > 0 {
		gen := h.statusGen
		renderFn := h.requestRender
		h.statusTimer = time.AfterFunc(autoHideMs[0], func() {
			h.mu.Lock()
			if h.statusGen != gen {
				h.mu.Unlock()
				return
			}
			h.statusMessage = nil
			h.statusTimer = nil
			h.mu.Unlock()
			if renderFn != nil {
				renderFn()
			}
		})
	}
	h.mu.Unlock()
}

func (h *ssHeader) dispose() {
	h.mu.Lock()
	h.clearStatusTimeoutLocked()
	h.mu.Unlock()
}

func (h *ssHeader) Invalidate() {}

func (h *ssHeader) Render(width int) []string {
	h.mu.Lock()
	scope := h.scope
	sortMode := h.sortMode
	nameFilter := h.nameFilter
	loading := h.loading
	loadProgress := h.loadProgress
	showPath := h.showPath
	confirmingDeletePath := h.confirmingDeletePath
	var statusMsg *ssStatusMessage
	if h.statusMessage != nil {
		cp := *h.statusMessage
		statusMsg = &cp
	}
	showRenameHint := h.showRenameHint
	h.mu.Unlock()

	t := theme.CurrentTheme()

	title := "Resume Session (All)"
	if scope == ssScopeCurrent {
		title = "Resume Session (Current Folder)"
	}
	leftText := t.Bold(title)

	sortLabel := "Fuzzy"
	switch sortMode {
	case SortModeThreaded:
		sortLabel = "Threaded"
	case SortModeRecent:
		sortLabel = "Recent"
	}
	sortText := t.Fg(theme.FgMuted, "Sort: ") + t.Fg(theme.FgAccent, sortLabel)

	nameLabel := "Named"
	if nameFilter == NameFilterAll {
		nameLabel = "All"
	}
	nameText := t.Fg(theme.FgMuted, "Name: ") + t.Fg(theme.FgAccent, nameLabel)

	var scopeText string
	if loading {
		progressText := "..."
		if loadProgress != nil {
			progressText = fmt.Sprintf("%d/%d", loadProgress[0], loadProgress[1])
		}
		scopeText = t.Fg(theme.FgMuted, "○ Current Folder | ") + t.Fg(theme.FgAccent, "Loading "+progressText)
	} else if scope == ssScopeCurrent {
		scopeText = t.Fg(theme.FgAccent, "◉ Current Folder") + t.Fg(theme.FgMuted, " | ○ All")
	} else {
		scopeText = t.Fg(theme.FgMuted, "○ Current Folder | ") + t.Fg(theme.FgAccent, "◉ All")
	}

	rightText := tui.TruncateToWidth(scopeText+"  "+nameText+"  "+sortText, width, "", false)
	availableLeft := max(0, width-tui.VisibleWidth(rightText)-1)
	left := tui.TruncateToWidth(leftText, availableLeft, "", false)
	spacing := max(0, width-tui.VisibleWidth(left)-tui.VisibleWidth(rightText))

	var hintLine1, hintLine2 string
	if confirmingDeletePath != nil {
		confirmHint := "Delete session? " +
			theme.AppKeyHint(t, tui.KeybindingTuiSelectConfirm, "confirm") +
			" · " +
			theme.AppKeyHint(t, tui.KeybindingTuiSelectCancel, "cancel")
		hintLine1 = t.Fg(theme.FgError, tui.TruncateToWidth(confirmHint, width, "…", false))
	} else if statusMsg != nil {
		color := theme.FgAccent
		if statusMsg.msgType == "error" {
			color = theme.FgError
		}
		hintLine1 = t.Fg(color, tui.TruncateToWidth(statusMsg.message, width, "…", false))
	} else {
		pathState := "(off)"
		if showPath {
			pathState = "(on)"
		}
		sep := t.Fg(theme.FgMuted, " · ")
		hint1 := theme.AppKeyHint(t, tui.KeybindingTuiInputTab, "scope") +
			sep +
			t.Fg(theme.FgMuted, `re:<pattern> regex · "phrase" exact`)
		hint2Parts := []string{
			theme.AppKeyHint(t, core.KeybindingAppSessionToggleSort, "sort"),
			theme.AppKeyHint(t, core.KeybindingAppSessionToggleNamed, "named"),
			theme.AppKeyHint(t, core.KeybindingAppSessionDelete, "delete"),
			theme.AppKeyHint(t, core.KeybindingAppSessionTogglePath, "path "+pathState),
		}
		if showRenameHint {
			hint2Parts = append(hint2Parts, theme.AppKeyHint(t, core.KeybindingAppSessionRename, "rename"))
		}
		hint2 := strings.Join(hint2Parts, sep)
		hintLine1 = tui.TruncateToWidth(hint1, width, "…", false)
		hintLine2 = tui.TruncateToWidth(hint2, width, "…", false)
	}

	return []string{left + strings.Repeat(" ", spacing) + rightText, hintLine1, hintLine2}
}

type ssSessionTreeNode struct {
	session        core.SessionInfo
	children       []*ssSessionTreeNode
	latestActivity int64
}

type ssFlatSessionNode struct {
	session           core.SessionInfo
	depth             int
	isLast            bool
	ancestorContinues []bool
}

func ssBuildSessionTree(sessions []core.SessionInfo) []*ssSessionTreeNode {
	byPath := make(map[string]*ssSessionTreeNode, len(sessions))
	for _, session := range sessions {
		sessionPath := ssGetSessionPathKey(session.Path)
		byPath[sessionPath] = &ssSessionTreeNode{
			session:        session,
			latestActivity: session.Modified.UnixMilli(),
		}
	}

	var roots []*ssSessionTreeNode
	for _, session := range sessions {
		sessionPath := ssGetSessionPathKey(session.Path)
		node := byPath[sessionPath]
		var parentNode *ssSessionTreeNode
		if session.ParentSessionPath != nil && *session.ParentSessionPath != "" {
			parentNode = byPath[ssGetSessionPathKey(*session.ParentSessionPath)]
		}
		if parentNode != nil {
			parentNode.children = append(parentNode.children, node)
		} else {
			roots = append(roots, node)
		}
	}

	var updateLatest func(node *ssSessionTreeNode) int64
	updateLatest = func(node *ssSessionTreeNode) int64 {
		latest := node.session.Modified.UnixMilli()
		for _, child := range node.children {
			if c := updateLatest(child); c > latest {
				latest = c
			}
		}
		node.latestActivity = latest
		return latest
	}
	for _, root := range roots {
		updateLatest(root)
	}

	var sortNodes func(nodes []*ssSessionTreeNode)
	sortNodes = func(nodes []*ssSessionTreeNode) {
		slices.SortStableFunc(nodes, func(a, b *ssSessionTreeNode) int {
			if b.latestActivity > a.latestActivity {
				return 1
			}
			if b.latestActivity < a.latestActivity {
				return -1
			}
			return 0
		})
		for _, node := range nodes {
			sortNodes(node.children)
		}
	}
	sortNodes(roots)

	return roots
}

func ssFlattenSessionTree(roots []*ssSessionTreeNode) []ssFlatSessionNode {
	var result []ssFlatSessionNode

	var walk func(node *ssSessionTreeNode, depth int, ancestorContinues []bool, isLast bool)
	walk = func(node *ssSessionTreeNode, depth int, ancestorContinues []bool, isLast bool) {
		result = append(result, ssFlatSessionNode{
			session:           node.session,
			depth:             depth,
			isLast:            isLast,
			ancestorContinues: ancestorContinues,
		})
		for i, child := range node.children {
			childIsLast := i == len(node.children)-1
			continues := depth > 0 && !isLast
			nextAncestors := make([]bool, len(ancestorContinues)+1)
			copy(nextAncestors, ancestorContinues)
			nextAncestors[len(ancestorContinues)] = continues
			walk(child, depth+1, nextAncestors, childIsLast)
		}
	}

	for i, root := range roots {
		walk(root, 0, nil, i == len(roots)-1)
	}

	return result
}

// SessionList renders a searchable, threaded session list inside SessionSelectorComponent.
type SessionList struct {
	mu                   sync.Mutex
	allSessions          []core.SessionInfo
	filteredSessions     []ssFlatSessionNode
	selectedIndex        int
	selectionTouched     bool
	pendingSelectedPath  string
	searchInput          *tui.Input
	showCwd              bool
	sortMode             SortMode
	nameFilter           NameFilter
	keybindings          *core.KeybindingsManager
	showPath             bool
	confirmingDeletePath *string
	currentSessionKey    string
	maxVisible           int
	focused              bool

	OnSelect                   func(sessionPath string)
	OnCancel                   func()
	OnExit                     func()
	OnToggleScope              func()
	OnToggleSort               func()
	OnToggleNameFilter         func()
	OnTogglePath               func(showPath bool)
	OnDeleteConfirmationChange func(path *string)
	OnDeleteSession            func(sessionPath string)
	OnRenameSession            func(sessionPath string)
	OnError                    func(message string)
}

var _ tui.Focusable = (*SessionList)(nil)

func ssNewSessionList(
	sessions []core.SessionInfo,
	showCwd bool,
	sortMode SortMode,
	nameFilter NameFilter,
	keybindings *core.KeybindingsManager,
	currentSessionFilePath string,
) *SessionList {
	var currentKey string
	if currentSessionFilePath != "" {
		currentKey = ssGetSessionPathKey(currentSessionFilePath)
	}
	l := &SessionList{
		allSessions:       slices.Clone(sessions),
		searchInput:       tui.NewInput(nil),
		showCwd:           showCwd,
		sortMode:          sortMode,
		nameFilter:        nameFilter,
		keybindings:       keybindings,
		currentSessionKey: currentKey,
		maxVisible:        10,
	}
	l.filterSessionsLocked("")
	l.searchInput.OnSubmit = func(string) {
		l.mu.Lock()
		var selectedPath string
		if l.selectedIndex >= 0 && l.selectedIndex < len(l.filteredSessions) {
			selectedPath = l.filteredSessions[l.selectedIndex].session.Path
		}
		onSelect := l.OnSelect
		l.mu.Unlock()
		if selectedPath != "" && onSelect != nil {
			onSelect(selectedPath)
		}
	}
	return l
}

// CapturesViewportInput reports that SessionList handles scroll navigation keys.
func (s *SessionList) CapturesViewportInput() bool {
	return true
}

// Focused reports whether the search input is currently focused.
func (s *SessionList) Focused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.focused
}

// SetFocused propagates focus state to the search input.
func (s *SessionList) SetFocused(value bool) {
	s.mu.Lock()
	s.focused = value
	s.searchInput.SetFocused(value)
	s.mu.Unlock()
}

// GetSelectedSessionPath returns the currently highlighted session path, if any.
func (s *SessionList) GetSelectedSessionPath() *string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selectedIndex < 0 || s.selectedIndex >= len(s.filteredSessions) {
		return nil
	}
	p := s.filteredSessions[s.selectedIndex].session.Path
	return &p
}

// SetSortMode updates the active sort mode and refilters the session list.
func (s *SessionList) SetSortMode(sortMode SortMode) {
	s.mu.Lock()
	s.sortMode = sortMode
	s.filterSessionsLocked(s.searchInput.GetValue())
	s.mu.Unlock()
}

// SetNameFilter updates the named-session filter and refilters the session list.
func (s *SessionList) SetNameFilter(nameFilter NameFilter) {
	s.mu.Lock()
	s.nameFilter = nameFilter
	s.filterSessionsLocked(s.searchInput.GetValue())
	s.mu.Unlock()
}

// SetSessions replaces the displayed sessions while preserving user selection across progressive loads.
func (s *SessionList) SetSessions(sessions []core.SessionInfo, showCwd bool, partial ...bool) {
	isPartial := len(partial) > 0 && partial[0]
	s.mu.Lock()
	var selectedPath string
	if s.selectionTouched {
		if s.pendingSelectedPath != "" {
			selectedPath = s.pendingSelectedPath
		} else if s.selectedIndex >= 0 && s.selectedIndex < len(s.filteredSessions) {
			selectedPath = s.filteredSessions[s.selectedIndex].session.Path
		}
	}

	s.allSessions = slices.Clone(sessions)
	s.showCwd = showCwd
	s.filterSessionsLocked(s.searchInput.GetValue())

	if !s.selectionTouched {
		s.selectedIndex = 0
	} else if selectedPath != "" {
		idx := slices.IndexFunc(s.filteredSessions, func(node ssFlatSessionNode) bool {
			return node.session.Path == selectedPath
		})
		if idx >= 0 {
			s.selectedIndex = idx
			s.pendingSelectedPath = ""
		} else if isPartial {
			s.pendingSelectedPath = selectedPath
		} else {
			s.pendingSelectedPath = ""
		}
	}
	s.mu.Unlock()
}

func (s *SessionList) filterSessionsLocked(query string) {
	trimmed := js.Trim(query)
	nameFiltered := s.allSessions
	if s.nameFilter != NameFilterAll {
		filtered := make([]core.SessionInfo, 0, len(s.allSessions))
		for _, sess := range s.allSessions {
			if HasSessionName(sess) {
				filtered = append(filtered, sess)
			}
		}
		nameFiltered = filtered
	}

	if s.sortMode == SortModeThreaded && trimmed == "" {
		roots := ssBuildSessionTree(nameFiltered)
		s.filteredSessions = ssFlattenSessionTree(roots)
	} else {
		filtered := FilterAndSortSessions(nameFiltered, query, s.sortMode, NameFilterAll)
		s.filteredSessions = make([]ssFlatSessionNode, len(filtered))
		for i, sess := range filtered {
			s.filteredSessions[i] = ssFlatSessionNode{
				session: sess,
				depth:   0,
				isLast:  true,
			}
		}
	}

	if len(s.filteredSessions) == 0 {
		s.selectedIndex = 0
	} else if s.selectedIndex >= len(s.filteredSessions) {
		s.selectedIndex = len(s.filteredSessions) - 1
	} else if s.selectedIndex < 0 {
		s.selectedIndex = 0
	}
}

func (s *SessionList) isCurrentSessionPathLocked(path string) bool {
	if s.currentSessionKey == "" {
		return false
	}
	return ssGetSessionPathKey(path) == s.currentSessionKey
}

func (s *SessionList) Invalidate() {}

func (s *SessionList) Render(width int) []string {
	s.mu.Lock()
	searchLines := s.searchInput.Render(width)
	filtered := slices.Clone(s.filteredSessions)
	selectedIdx := s.selectedIndex
	maxVis := s.maxVisible
	showCwd := s.showCwd
	showPath := s.showPath
	nameFilter := s.nameFilter
	confirmingDeletePath := s.confirmingDeletePath
	currentSessionKey := s.currentSessionKey
	s.mu.Unlock()

	t := theme.CurrentTheme()
	lines := make([]string, 0, len(searchLines)+maxVis+3)
	lines = append(lines, searchLines...)
	lines = append(lines, "")

	if len(filtered) == 0 {
		var emptyMessage string
		if nameFilter == NameFilterNamed {
			toggleKey := theme.KeyText(core.KeybindingAppSessionToggleNamed)
			if showCwd {
				emptyMessage = "  No named sessions found. Press " + toggleKey + " to show all."
			} else {
				emptyMessage = "  No named sessions in current folder. Press " + toggleKey + " to show all, or Tab to view all."
			}
		} else if showCwd {
			emptyMessage = "  No sessions found"
		} else {
			emptyMessage = "  No sessions in current folder. Press Tab to view all."
		}
		lines = append(lines, t.Fg(theme.FgMuted, tui.TruncateToWidth(emptyMessage, width, "…", false)))
		return lines
	}

	startIndex := max(0, min(selectedIdx-maxVis/2, len(filtered)-maxVis))
	endIndex := min(startIndex+maxVis, len(filtered))

	for i := startIndex; i < endIndex; i++ {
		node := filtered[i]
		sess := node.session
		isSelected := i == selectedIdx
		isConfirmingDelete := confirmingDeletePath != nil && sess.Path == *confirmingDeletePath
		isCurrent := currentSessionKey != "" && ssGetSessionPathKey(sess.Path) == currentSessionKey

		prefix := ssBuildTreePrefix(node)

		hasName := sess.Name != nil && *sess.Name != ""
		displayText := sess.FirstMessage
		if hasName {
			displayText = *sess.Name
		}
		normalizedMsg := js.Trim(ssCtrlCharStripRe.Replace(displayText, " "))

		age := ssFormatSessionDate(sess.Modified)
		msgCount := strconv.Itoa(sess.MessageCount)
		rightPart := msgCount + " " + age
		if showCwd && sess.Cwd != "" {
			rightPart = utils.ShortenHomePath(sess.Cwd) + " " + rightPart
		}
		if showPath {
			rightPart = utils.ShortenHomePath(sess.Path) + " " + rightPart
		}

		cursor := "  "
		if isSelected {
			cursor = t.Fg(theme.FgAccent, "› ")
		}

		prefixWidth := tui.VisibleWidth(prefix)
		rightWidth := tui.VisibleWidth(rightPart) + 2
		availableForMsg := width - 2 - prefixWidth - rightWidth

		truncatedMsg := tui.TruncateToWidth(normalizedMsg, max(10, availableForMsg), "…", false)

		styledMsg := truncatedMsg
		if isConfirmingDelete {
			styledMsg = t.Fg(theme.FgError, truncatedMsg)
		} else if isCurrent {
			styledMsg = t.Fg(theme.FgAccent, truncatedMsg)
		} else if hasName {
			styledMsg = t.Fg(theme.FgWarning, truncatedMsg)
		}
		if isSelected {
			styledMsg = t.Bold(styledMsg)
		}

		leftPart := cursor + t.Fg(theme.FgDim, prefix) + styledMsg
		leftWidth := tui.VisibleWidth(leftPart)
		spacing := max(1, width-leftWidth-tui.VisibleWidth(rightPart))
		rightColor := theme.FgDim
		if isConfirmingDelete {
			rightColor = theme.FgError
		}
		styledRight := t.Fg(rightColor, rightPart)

		line := leftPart + strings.Repeat(" ", spacing) + styledRight
		if isSelected {
			line = t.Bg(theme.BgSelected, line)
		}
		lines = append(lines, tui.TruncateToWidth(line, width, "...", false))
	}

	if startIndex > 0 || endIndex < len(filtered) {
		scrollText := fmt.Sprintf("  (%d/%d)", selectedIdx+1, len(filtered))
		lines = append(lines, t.Fg(theme.FgMuted, tui.TruncateToWidth(scrollText, width, "", false)))
	}

	return lines
}

func ssBuildTreePrefix(node ssFlatSessionNode) string {
	if node.depth == 0 {
		return ""
	}
	var b strings.Builder
	for _, continues := range node.ancestorContinues {
		if continues {
			b.WriteString("│  ")
		} else {
			b.WriteString("   ")
		}
	}
	if node.isLast {
		b.WriteString("└─ ")
	} else {
		b.WriteString("├─ ")
	}
	return b.String()
}

// HandleInput processes navigation, scope/sort/filter toggles, deletion, rename, and search typing.
func (s *SessionList) HandleInput(keyData string) {
	kb := tui.GetKeybindings()

	s.mu.Lock()
	listKB := s.keybindings

	if s.confirmingDeletePath != nil {
		if kb.Matches(keyData, tui.KeybindingTuiSelectConfirm) {
			pathToDelete := *s.confirmingDeletePath
			s.confirmingDeletePath = nil
			onConfirmChange := s.OnDeleteConfirmationChange
			onDelete := s.OnDeleteSession
			s.mu.Unlock()
			if onConfirmChange != nil {
				onConfirmChange(nil)
			}
			if onDelete != nil {
				onDelete(pathToDelete)
			}
			return
		}
		if kb.Matches(keyData, tui.KeybindingTuiSelectCancel) {
			s.confirmingDeletePath = nil
			onConfirmChange := s.OnDeleteConfirmationChange
			s.mu.Unlock()
			if onConfirmChange != nil {
				onConfirmChange(nil)
			}
			return
		}
		s.mu.Unlock()
		return
	}

	if kb.Matches(keyData, tui.KeybindingTuiInputTab) {
		cb := s.OnToggleScope
		s.mu.Unlock()
		if cb != nil {
			cb()
		}
		return
	}

	if kb.Matches(keyData, core.KeybindingAppSessionToggleSort) {
		cb := s.OnToggleSort
		s.mu.Unlock()
		if cb != nil {
			cb()
		}
		return
	}

	if listKB != nil && listKB.Matches(keyData, core.KeybindingAppSessionToggleNamed) {
		cb := s.OnToggleNameFilter
		s.mu.Unlock()
		if cb != nil {
			cb()
		}
		return
	}

	if kb.Matches(keyData, core.KeybindingAppSessionTogglePath) {
		s.showPath = !s.showPath
		showPath := s.showPath
		cb := s.OnTogglePath
		s.mu.Unlock()
		if cb != nil {
			cb(showPath)
		}
		return
	}

	startDeleteLocked := func() {
		if s.selectedIndex < 0 || s.selectedIndex >= len(s.filteredSessions) {
			s.mu.Unlock()
			return
		}
		selectedPath := s.filteredSessions[s.selectedIndex].session.Path
		if s.isCurrentSessionPathLocked(selectedPath) {
			onErr := s.OnError
			s.mu.Unlock()
			if onErr != nil {
				onErr("Cannot delete the currently active session")
			}
			return
		}
		s.confirmingDeletePath = &selectedPath
		onConfirmChange := s.OnDeleteConfirmationChange
		s.mu.Unlock()
		if onConfirmChange != nil {
			onConfirmChange(&selectedPath)
		}
	}

	if kb.Matches(keyData, core.KeybindingAppSessionDelete) {
		startDeleteLocked()
		return
	}

	if kb.Matches(keyData, core.KeybindingAppSessionRename) {
		var selectedPath string
		if s.selectedIndex >= 0 && s.selectedIndex < len(s.filteredSessions) {
			selectedPath = s.filteredSessions[s.selectedIndex].session.Path
		}
		cb := s.OnRenameSession
		s.mu.Unlock()
		if selectedPath != "" && cb != nil {
			cb(selectedPath)
		}
		return
	}

	if kb.Matches(keyData, core.KeybindingAppSessionDeleteNoninv) {
		if s.searchInput.GetValue() != "" {
			s.searchInput.HandleInput(keyData)
			s.filterSessionsLocked(s.searchInput.GetValue())
			s.mu.Unlock()
			return
		}
		startDeleteLocked()
		return
	}

	s.selectionTouched = true
	s.pendingSelectedPath = ""

	switch {
	case kb.Matches(keyData, tui.KeybindingTuiSelectUp):
		s.selectedIndex = max(0, s.selectedIndex-1)
		s.mu.Unlock()
	case kb.Matches(keyData, tui.KeybindingTuiSelectDown):
		if len(s.filteredSessions) > 0 {
			s.selectedIndex = min(len(s.filteredSessions)-1, s.selectedIndex+1)
		}
		s.mu.Unlock()
	case kb.Matches(keyData, tui.KeybindingTuiSelectPageUp):
		s.selectedIndex = max(0, s.selectedIndex-s.maxVisible)
		s.mu.Unlock()
	case kb.Matches(keyData, tui.KeybindingTuiSelectPageDown):
		if len(s.filteredSessions) > 0 {
			s.selectedIndex = min(len(s.filteredSessions)-1, s.selectedIndex+s.maxVisible)
		}
		s.mu.Unlock()
	case kb.Matches(keyData, tui.KeybindingTuiSelectConfirm):
		var selectedPath string
		if s.selectedIndex >= 0 && s.selectedIndex < len(s.filteredSessions) {
			selectedPath = s.filteredSessions[s.selectedIndex].session.Path
		}
		cb := s.OnSelect
		s.mu.Unlock()
		if selectedPath != "" && cb != nil {
			cb(selectedPath)
		}
	case kb.Matches(keyData, tui.KeybindingTuiSelectCancel):
		cb := s.OnCancel
		s.mu.Unlock()
		if cb != nil {
			cb()
		}
	default:
		s.searchInput.HandleInput(keyData)
		s.filterSessionsLocked(s.searchInput.GetValue())
		s.mu.Unlock()
	}
}

type ssDeleteResult struct {
	ok     bool
	method string // "trash" | "unlink"
	errMsg string
}

func ssDeleteSessionFile(sessionPath string) ssDeleteResult {
	trashArgs := []string{sessionPath}
	if strings.HasPrefix(sessionPath, "-") {
		trashArgs = []string{"--", sessionPath}
	}
	cmd := exec.Command("trash", trashArgs...)
	stderrBytes, trashErr := cmd.CombinedOutput()

	getTrashHint := func() string {
		var parts []string
		if trashErr != nil {
			parts = append(parts, trashErr.Error())
		}
		stderr := js.Trim(string(stderrBytes))
		if stderr != "" {
			line := strings.Split(stderr, "\n")[0]
			parts = append(parts, line)
		}
		if len(parts) == 0 {
			return ""
		}
		joined := "trash: " + strings.Join(parts, " · ")
		if js.Len(joined) > 200 {
			return js.Slice(joined, 0, 200)
		}
		return joined
	}

	_, statErr := os.Stat(sessionPath)
	if trashErr == nil || errors.Is(statErr, os.ErrNotExist) {
		return ssDeleteResult{ok: true, method: "trash"}
	}

	if err := os.Remove(sessionPath); err == nil {
		return ssDeleteResult{ok: true, method: "unlink"}
	} else {
		msg := err.Error()
		if hint := getTrashHint(); hint != "" {
			msg = msg + " (" + hint + ")"
		}
		return ssDeleteResult{ok: false, method: "unlink", errMsg: msg}
	}
}

type ssLoadHandle struct {
	cancel context.CancelFunc
}

// SessionSelectorComponent renders the interactive /resume and --resume session picker dialog.
type SessionSelectorComponent struct {
	*tui.Container
	containerMu         sync.Mutex
	mu                  sync.Mutex
	canRename           bool
	sessionList         *SessionList
	header              *ssHeader
	keybindings         *core.KeybindingsManager
	scope               ssScope
	sortMode            SortMode
	nameFilter          NameFilter
	currentSessions     []core.SessionInfo
	currentLoaded       bool
	allSessions         []core.SessionInfo
	allLoaded           bool
	currentSessionsLoad SessionsLoader
	allSessionsLoad     SessionsLoader
	requestRender       func()
	renameSession       func(ctx context.Context, sessionPath string, nextName *string) error
	currentLoad         *ssLoadHandle
	allLoad             *ssLoadHandle
	mode                string // "list" | "rename"
	renameInput         *tui.Input
	renameTargetPath    string
	focused             bool
	disposed            bool
	renameCancel        context.CancelFunc
	deleteCancel        context.CancelFunc
	accentBorderColorFn func(string) string
}

var (
	_ tui.Focusable = (*SessionSelectorComponent)(nil)
)

// NewSessionSelectorComponent constructs and immediately starts loading the current folder's sessions.
func NewSessionSelectorComponent(
	currentLoader, allLoader SessionsLoader,
	onSelect func(sessionPath string),
	onCancel, onExit, requestRender func(),
	options *SessionSelectorOptions,
	currentSessionFilePath ...string,
) *SessionSelectorComponent {
	var kb *core.KeybindingsManager
	var renameFn func(ctx context.Context, sessionPath string, nextName *string) error
	var showRenameHint *bool
	if options != nil {
		kb = options.Keybindings
		renameFn = options.RenameSession
		showRenameHint = options.ShowRenameHint
	}
	if kb == nil {
		kb = core.KeybindingsManagerCreate()
	}
	var currentPath string
	if len(currentSessionFilePath) > 0 {
		currentPath = currentSessionFilePath[0]
	}

	s := &SessionSelectorComponent{
		Container:           tui.NewContainer(),
		canRename:           renameFn != nil,
		keybindings:         kb,
		scope:               ssScopeCurrent,
		sortMode:            SortModeThreaded,
		nameFilter:          NameFilterAll,
		currentSessionsLoad: currentLoader,
		allSessionsLoad:     allLoader,
		requestRender:       requestRender,
		renameSession:       renameFn,
		mode:                "list",
		renameInput:         tui.NewInput(nil),
		accentBorderColorFn: func(str string) string { return theme.CurrentTheme().Fg(theme.FgAccent, str) },
	}
	s.header = ssNewHeader(s.scope, s.sortMode, s.nameFilter, requestRender)
	effectiveRenameHint := s.canRename
	if showRenameHint != nil {
		effectiveRenameHint = *showRenameHint
	}
	s.header.setShowRenameHint(effectiveRenameHint)

	s.sessionList = ssNewSessionList(nil, false, s.sortMode, s.nameFilter, kb, currentPath)
	s.buildBaseLayout(s.sessionList, true)

	s.renameInput.OnSubmit = func(value string) {
		s.startConfirmRename(value)
	}

	clearStatus := func() { s.header.setStatusMessage(nil) }
	s.sessionList.OnSelect = func(sessionPath string) {
		clearStatus()
		s.cancelLoads()
		if onSelect != nil {
			onSelect(sessionPath)
		}
	}
	s.sessionList.OnCancel = func() {
		clearStatus()
		s.cancelLoads()
		if onCancel != nil {
			onCancel()
		}
	}
	s.sessionList.OnExit = func() {
		clearStatus()
		s.cancelLoads()
		if onExit != nil {
			onExit()
		}
	}
	s.sessionList.OnToggleScope = func() { s.toggleScope() }
	s.sessionList.OnToggleSort = func() { s.toggleSortMode() }
	s.sessionList.OnToggleNameFilter = func() { s.toggleNameFilter() }
	s.sessionList.OnRenameSession = func(sessionPath string) {
		if s.renameSession == nil {
			return
		}
		s.mu.Lock()
		isLoading := s.currentLoad != nil
		var sessions []core.SessionInfo
		if s.scope == ssScopeAll {
			isLoading = s.allLoad != nil
			sessions = s.allSessions
		} else {
			sessions = s.currentSessions
		}
		if isLoading {
			s.mu.Unlock()
			return
		}
		var currentName *string
		for _, sess := range sessions {
			if sess.Path == sessionPath {
				currentName = sess.Name
				break
			}
		}
		s.mu.Unlock()
		s.enterRenameMode(sessionPath, currentName)
	}

	s.sessionList.OnTogglePath = func(showPath bool) {
		s.header.setShowPath(showPath)
		s.emitRender()
	}
	s.sessionList.OnDeleteConfirmationChange = func(path *string) {
		s.header.setConfirmingDeletePath(path)
		s.emitRender()
	}
	s.sessionList.OnError = func(msg string) {
		s.header.setStatusMessage(&ssStatusMessage{msgType: "error", message: msg}, 3000*time.Millisecond)
		s.emitRender()
	}
	s.sessionList.OnDeleteSession = func(sessionPath string) {
		s.startDeleteSession(sessionPath)
	}

	s.startLoadScope(ssScopeCurrent)
	return s
}

func (s *SessionSelectorComponent) emitRender() {
	if s.requestRender != nil {
		s.requestRender()
	}
}

// CapturesViewportInput reports whether the selector intercepts viewport scrolling (false while renaming).
func (s *SessionSelectorComponent) CapturesViewportInput() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode != "rename"
}

// Focused reports whether the active input holds focus.
func (s *SessionSelectorComponent) Focused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.focused
}

// SetFocused propagates focus to the session list and rename input.
func (s *SessionSelectorComponent) SetFocused(value bool) {
	s.mu.Lock()
	s.focused = value
	s.sessionList.SetFocused(value)
	s.renameInput.SetFocused(value)
	s.mu.Unlock()
}

// Render renders the selector while synchronizing layout updates cleanly.
func (s *SessionSelectorComponent) Render(width int) []string {
	s.containerMu.Lock()
	defer s.containerMu.Unlock()
	return s.Container.Render(width)
}

// Invalidate clears cached layout state.
func (s *SessionSelectorComponent) Invalidate() {
	s.containerMu.Lock()
	s.Container.Invalidate()
	s.containerMu.Unlock()
}

// HandleInput routes keys to the rename input in rename mode or to SessionList otherwise.
func (s *SessionSelectorComponent) HandleInput(data string) {
	s.mu.Lock()
	isRename := s.mode == "rename"
	s.mu.Unlock()

	if isRename {
		kb := tui.GetKeybindings()
		if kb.Matches(data, tui.KeybindingTuiSelectCancel) {
			s.exitRenameMode()
			return
		}
		s.renameInput.HandleInput(data)
		return
	}
	s.sessionList.HandleInput(data)
}

// GetSessionList returns the underlying SessionList for event wiring and inspection.
func (s *SessionSelectorComponent) GetSessionList() *SessionList {
	return s.sessionList
}

// Dispose cancels active background session loads and timers.
func (s *SessionSelectorComponent) Dispose() {
	s.mu.Lock()
	s.disposed = true
	if s.renameCancel != nil {
		s.renameCancel()
		s.renameCancel = nil
	}
	if s.deleteCancel != nil {
		s.deleteCancel()
		s.deleteCancel = nil
	}
	s.cancelLoadsLocked()
	s.mu.Unlock()
	s.header.dispose()
}

func (s *SessionSelectorComponent) buildBaseLayout(content tui.Component, showHeader bool) {
	s.containerMu.Lock()
	defer s.containerMu.Unlock()
	s.Clear()
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(NewDynamicBorder(s.accentBorderColorFn))
	s.AddChild(tui.NewSpacer(1))
	if showHeader {
		s.AddChild(s.header)
		s.AddChild(tui.NewSpacer(1))
	}
	s.AddChild(content)
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(NewDynamicBorder(s.accentBorderColorFn))
}

func (s *SessionSelectorComponent) cancelLoadsLocked() {
	if s.currentLoad != nil {
		s.currentLoad.cancel()
		s.currentLoad = nil
		s.currentSessions = nil
		s.currentLoaded = false
	}
	if s.allLoad != nil {
		s.allLoad.cancel()
		s.allLoad = nil
		s.allSessions = nil
		s.allLoaded = false
	}
}

func (s *SessionSelectorComponent) cancelLoads() {
	s.mu.Lock()
	s.cancelLoadsLocked()
	s.mu.Unlock()
}

func (s *SessionSelectorComponent) enterRenameMode(sessionPath string, currentName *string) {
	s.mu.Lock()
	s.mode = "rename"
	s.renameTargetPath = sessionPath
	initial := ""
	if currentName != nil {
		initial = *currentName
	}
	s.renameInput.SetValue(initial)
	s.renameInput.SetFocused(true)
	s.mu.Unlock()

	t := theme.CurrentTheme()
	panel := tui.NewContainer()
	panel.AddChild(tui.NewText(t.Bold("Rename Session"), 1, 0, nil))
	panel.AddChild(tui.NewSpacer(1))
	panel.AddChild(s.renameInput)
	panel.AddChild(tui.NewSpacer(1))
	panel.AddChild(tui.NewText(
		t.Fg(theme.FgMuted, theme.KeyText(tui.KeybindingTuiSelectConfirm)+" to save · "+theme.KeyText(tui.KeybindingTuiSelectCancel)+" to cancel"),
		1,
		0,
		nil,
	))

	s.buildBaseLayout(panel, false)
	s.emitRender()
}

func (s *SessionSelectorComponent) exitRenameMode() {
	s.mu.Lock()
	s.mode = "list"
	s.renameTargetPath = ""
	s.mu.Unlock()

	s.buildBaseLayout(s.sessionList, true)
	s.emitRender()
}

func (s *SessionSelectorComponent) startConfirmRename(value string) {
	next := js.Trim(value)
	if next == "" {
		return
	}
	s.mu.Lock()
	target := s.renameTargetPath
	renameFn := s.renameSession
	if target == "" || renameFn == nil {
		s.mu.Unlock()
		s.exitRenameMode()
		return
	}
	if s.renameCancel != nil {
		s.renameCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.renameCancel = cancel
	s.mu.Unlock()

	go func() {
		defer cancel()
		_ = renameFn(ctx, target, &next)
		if ctx.Err() == nil {
			s.refreshSessionsAfterMutationSync()
		}
		s.exitRenameMode()
	}()
}

func (s *SessionSelectorComponent) startDeleteSession(sessionPath string) {
	s.mu.Lock()
	if s.deleteCancel != nil {
		s.deleteCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.deleteCancel = cancel
	s.mu.Unlock()

	go func() {
		defer cancel()
		res := ssDeleteSessionFile(sessionPath)
		if ctx.Err() != nil {
			return
		}
		if res.ok {
			s.mu.Lock()
			if s.currentLoaded {
				s.currentSessions = ssFilterOutPath(s.currentSessions, sessionPath)
			}
			if s.allLoaded {
				s.allSessions = ssFilterOutPath(s.allSessions, sessionPath)
			}
			var sessions []core.SessionInfo
			showCwd := s.scope == ssScopeAll
			if showCwd {
				sessions = s.allSessions
			} else {
				sessions = s.currentSessions
			}
			s.mu.Unlock()

			s.sessionList.SetSessions(sessions, showCwd)
			msg := "Session deleted"
			if res.method == "trash" {
				msg = "Session moved to trash"
			}
			s.header.setStatusMessage(&ssStatusMessage{msgType: "info", message: msg}, 2000*time.Millisecond)
			s.refreshSessionsAfterMutationSync()
		} else {
			errMsg := res.errMsg
			if errMsg == "" {
				errMsg = "Unknown error"
			}
			s.header.setStatusMessage(&ssStatusMessage{msgType: "error", message: "Failed to delete: " + errMsg}, 3000*time.Millisecond)
		}
		s.emitRender()
	}()
}

func ssFilterOutPath(sessions []core.SessionInfo, targetPath string) []core.SessionInfo {
	out := make([]core.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		if sess.Path != targetPath {
			out = append(out, sess)
		}
	}
	return out
}

func (s *SessionSelectorComponent) startLoadScope(scope ssScope) {
	go s.loadScopeSync(scope)
}

func (s *SessionSelectorComponent) loadScopeSync(scope ssScope) {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return
	}
	if scope == ssScopeCurrent && s.currentLoad != nil {
		s.mu.Unlock()
		return
	}
	if scope == ssScopeAll && s.allLoad != nil {
		s.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	handle := &ssLoadHandle{cancel: cancel}
	if scope == ssScopeCurrent {
		s.currentLoad = handle
	} else {
		s.allLoad = handle
	}
	activeScope := s.scope
	loader := s.currentSessionsLoad
	if scope == ssScopeAll {
		loader = s.allSessionsLoad
	}
	s.mu.Unlock()
	defer cancel()

	showCwd := scope == ssScopeAll
	if activeScope == scope {
		s.header.setScope(scope)
		s.header.setLoading(true)
		s.emitRender()
	}

	isActive := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		if scope == ssScopeCurrent {
			return s.currentLoad == handle
		}
		return s.allLoad == handle
	}

	onProgress := func(loaded, total int, partialSessions []core.SessionInfo) {
		if !isActive() {
			return
		}
		var activeMatches bool
		s.mu.Lock()
		if partialSessions != nil {
			copied := slices.Clone(partialSessions)
			if scope == ssScopeCurrent {
				s.currentSessions = copied
				s.currentLoaded = true
			} else {
				s.allSessions = copied
				s.allLoaded = true
			}
		}
		activeMatches = s.scope == scope
		s.mu.Unlock()

		if partialSessions != nil && activeMatches {
			s.sessionList.SetSessions(slices.Clone(partialSessions), showCwd, true)
		}
		if !activeMatches {
			return
		}
		s.header.setProgress(loaded, total)
		s.emitRender()
	}

	var sessions []core.SessionInfo
	var loadErr error
	if loader != nil {
		sessions, loadErr = loader(onProgress, ctx)
	}
	if !isActive() {
		return
	}

	s.mu.Lock()
	if !((scope == ssScopeCurrent && s.currentLoad == handle) || (scope == ssScopeAll && s.allLoad == handle)) {
		s.mu.Unlock()
		return
	}
	if loadErr != nil {
		if scope == ssScopeCurrent {
			s.currentLoad = nil
			s.currentSessions = nil
			s.currentLoaded = false
		} else {
			s.allLoad = nil
			s.allSessions = nil
			s.allLoaded = false
		}
		activeMatches := s.scope == scope
		s.mu.Unlock()
		if !activeMatches {
			return
		}
		s.header.setLoading(false)
		s.header.setStatusMessage(&ssStatusMessage{msgType: "error", message: "Failed to load sessions: " + loadErr.Error()}, 4000*time.Millisecond)
		s.sessionList.SetSessions(nil, showCwd)
		s.emitRender()
		return
	}

	copied := slices.Clone(sessions)
	if scope == ssScopeCurrent {
		s.currentSessions = copied
		s.currentLoaded = true
		s.currentLoad = nil
	} else {
		s.allSessions = copied
		s.allLoaded = true
		s.allLoad = nil
	}
	activeMatches := s.scope == scope
	s.mu.Unlock()

	if !activeMatches {
		return
	}
	s.header.setLoading(false)
	s.sessionList.SetSessions(copied, showCwd)
	s.emitRender()
}

func (s *SessionSelectorComponent) refreshSessionsAfterMutationSync() {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return
	}
	s.cancelLoadsLocked()
	scope := s.scope
	s.mu.Unlock()
	s.loadScopeSync(scope)
}

func (s *SessionSelectorComponent) toggleSortMode() {
	s.mu.Lock()
	switch s.sortMode {
	case SortModeThreaded:
		s.sortMode = SortModeRecent
	case SortModeRecent:
		s.sortMode = SortModeRelevance
	default:
		s.sortMode = SortModeThreaded
	}
	next := s.sortMode
	s.mu.Unlock()

	s.header.setSortMode(next)
	s.sessionList.SetSortMode(next)
	s.emitRender()
}

func (s *SessionSelectorComponent) toggleNameFilter() {
	s.mu.Lock()
	if s.nameFilter == NameFilterAll {
		s.nameFilter = NameFilterNamed
	} else {
		s.nameFilter = NameFilterAll
	}
	next := s.nameFilter
	s.mu.Unlock()

	s.header.setNameFilter(next)
	s.sessionList.SetNameFilter(next)
	s.emitRender()
}

func (s *SessionSelectorComponent) toggleScope() {
	s.mu.Lock()
	if s.scope == ssScopeCurrent {
		s.scope = ssScopeAll
	} else {
		s.scope = ssScopeCurrent
	}
	scope := s.scope
	var sessions []core.SessionInfo
	var loaded bool
	var loading bool
	if scope == ssScopeCurrent {
		sessions = s.currentSessions
		loaded = s.currentLoaded
		loading = s.currentLoad != nil
	} else {
		sessions = s.allSessions
		loaded = s.allLoaded
		loading = s.allLoad != nil
	}
	s.mu.Unlock()

	s.header.setScope(scope)
	s.header.setLoading(loading)
	s.sessionList.SetSessions(sessions, scope == ssScopeAll, loading || !loaded)
	s.emitRender()
	if !loaded && !loading {
		s.startLoadScope(scope)
	}
}
