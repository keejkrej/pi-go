// Ported from packages/coding-agent/src/modes/interactive/components/tree-selector.ts (pi v1.5.2).

package components

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

// FilterMode selects which entries remain visible in the session tree selector.
type FilterMode = core.TreeFilterMode

const (
	tselTreeGutterWidth              = 2 // "› " / "  "
	tselMinVisibleAnchorContentWidth = 4
	tselMaxVisibleAnchorContentWidth = 20
	tselMinAnchorContextWidth        = 2
	tselMaxAnchorContextWidth        = 12
	tselEmptyTreeCancelDelay         = 100 * time.Millisecond
)

var tselFilterCycle = []FilterMode{
	core.TreeFilterModeDefault,
	core.TreeFilterModeNoTools,
	core.TreeFilterModeUserOnly,
	core.TreeFilterModeLabeledOnly,
	core.TreeFilterModeAll,
}

type tselGutterInfo struct {
	position int // visual indent depth (0-based, matches parent indent - 1)
	show     bool
}

type tselFlatNode struct {
	node          core.SessionTreeNode
	indent        int
	showConnector bool
	isLast        bool
	gutters       []tselGutterInfo
	fromExtension bool
}

type tselRenderedRow struct {
	cursor    string
	body      string
	anchorCol int
}

func tselRenderHorizontalViewport(rows []tselRenderedRow, selectedIndex int, width int) []string {
	viewportWidth := max(0, width-tselTreeGutterWidth)
	maxBodyWidth := 0
	for _, row := range rows {
		if w := tui.VisibleWidth(row.body); w > maxBodyWidth {
			maxBodyWidth = w
		}
	}
	maxHorizontalScroll := max(0, maxBodyWidth-viewportWidth)

	horizontalScroll := 0
	if selectedIndex >= 0 && selectedIndex < len(rows) && viewportWidth > 0 {
		selectedRow := rows[selectedIndex]
		anchorContextWidth := max(
			tselMinAnchorContextWidth,
			min(tselMaxAnchorContextWidth, viewportWidth/4),
		)
		minVisibleAnchorContentWidth := max(
			tselMinVisibleAnchorContentWidth,
			min(tselMaxVisibleAnchorContentWidth, viewportWidth-anchorContextWidth),
		)
		if selectedRow.anchorCol > viewportWidth-minVisibleAnchorContentWidth {
			horizontalScroll = min(maxHorizontalScroll, max(0, selectedRow.anchorCol-anchorContextWidth))
		}
	}

	result := make([]string, len(rows))
	for i, row := range rows {
		clippedBody := ""
		if viewportWidth > 0 {
			clippedBody = tui.SliceByColumn(row.body, horizontalScroll, viewportWidth, true) + "\x1b[0m"
		}
		result[i] = tui.TruncateToWidth(row.cursor+clippedBody, width, "", false)
	}
	return result
}

func tselParseTimestamp(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999Z07:00"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Local(), true
		}
	}
	return time.Time{}, false
}

func tselFormatLabelTimestamp(timestamp *string) *string {
	if timestamp == nil || *timestamp == "" {
		return nil
	}
	dt, ok := tselParseTimestamp(*timestamp)
	if !ok {
		return nil
	}
	now := time.Now().Local()
	hours := fmt.Sprintf("%02d", dt.Hour())
	minutes := fmt.Sprintf("%02d", dt.Minute())
	timeStr := hours + ":" + minutes

	var formatted string
	if dt.Year() == now.Year() && dt.Month() == now.Month() && dt.Day() == now.Day() {
		formatted = timeStr
	} else if dt.Year() == now.Year() {
		formatted = fmt.Sprintf("%d/%d %s", int(dt.Month()), dt.Day(), timeStr)
	} else {
		formatted = fmt.Sprintf("%02d/%d/%d %s", dt.Year()%100, int(dt.Month()), dt.Day(), timeStr)
	}
	return &formatted
}

func tselExtractUserMessageText(c ai.UserMessageContent) string {
	if c.Text != nil {
		return *c.Text
	}
	return tselExtractContentListText(c.Blocks)
}

func tselExtractContentListText(blocks ai.ContentList) string {
	var b strings.Builder
	for _, block := range blocks {
		if tc, ok := block.(*ai.TextContent); ok && tc != nil {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func tselNormalizeInline(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func tselGetArgString(args *jsonx.Object, keys ...string) (string, bool) {
	if args == nil {
		return "", false
	}
	for _, k := range keys {
		if v, ok := args.Get(k); ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func tselGetArgAny(args *jsonx.Object, key string) any {
	if args == nil {
		return nil
	}
	v, _ := args.Get(key)
	return v
}

func tselFormatToolCall(name string, args *jsonx.Object) string {
	home := ""
	switch name {
	case "read":
		rawPath, _ := tselGetArgString(args, "path", "file_path")
		path := utils.ShortenHomePath(rawPath, home)
		offset := tselGetArgAny(args, "offset")
		limit := tselGetArgAny(args, "limit")
		return fmt.Sprintf("[read: %s%s]", path, core.FormatReadLineRange(offset, limit))
	case "write":
		rawPath, _ := tselGetArgString(args, "path", "file_path")
		return fmt.Sprintf("[write: %s]", utils.ShortenHomePath(rawPath, home))
	case "edit":
		rawPath, _ := tselGetArgString(args, "path", "file_path")
		return fmt.Sprintf("[edit: %s]", utils.ShortenHomePath(rawPath, home))
	case "bash":
		cmd, _ := tselGetArgString(args, "command")
		if len([]rune(cmd)) > 50 {
			cmd = string([]rune(cmd)[:50]) + "..."
		}
		return fmt.Sprintf("[bash: %s]", cmd)
	case "grep":
		pattern, _ := tselGetArgString(args, "pattern")
		rawPath, hasPath := tselGetArgString(args, "path")
		if !hasPath || rawPath == "" {
			rawPath = "."
		}
		return fmt.Sprintf("[grep: /%s/ in %s]", pattern, utils.ShortenHomePath(rawPath, home))
	case "find":
		pattern, _ := tselGetArgString(args, "pattern")
		rawPath, hasPath := tselGetArgString(args, "path")
		if !hasPath || rawPath == "" {
			rawPath = "."
		}
		return fmt.Sprintf("[find: %s in %s]", pattern, utils.ShortenHomePath(rawPath, home))
	case "ls":
		rawPath, hasPath := tselGetArgString(args, "path")
		if !hasPath || rawPath == "" {
			rawPath = "."
		}
		return fmt.Sprintf("[ls: %s]", utils.ShortenHomePath(rawPath, home))
	default:
		argsStr := "{}"
		if args != nil {
			if raw, err := jsonx.Marshal(args); err == nil {
				argsStr = string(raw)
			}
		}
		if len([]rune(argsStr)) > 40 {
			argsStr = string([]rune(argsStr)[:40]) + "..."
		}
		return fmt.Sprintf("[%s: %s]", name, argsStr)
	}
}

func tselIsSettingsEntry(entry core.SessionEntry) bool {
	switch entry.(type) {
	case *core.LabelEntry,
		*core.ContextEditEntry,
		*core.CustomEntry,
		*core.ModelChangeEntry,
		*core.ThinkingLevelChangeEntry,
		*core.SessionInfoEntry:
		return true
	default:
		return false
	}
}

func tselIsExtensionEntry(entry core.SessionEntry) bool {
	switch entry.(type) {
	case *core.CustomEntry, *core.CustomMessageEntry:
		return true
	default:
		return false
	}
}

// TreeList manages the interactive filtered session tree view and navigation state.
type TreeList struct {
	mu                  sync.Mutex
	flatNodes           []*tselFlatNode
	filteredNodes       []*tselFlatNode
	nodeById            map[string]*tselFlatNode
	selectedIndex       int
	currentLeafId       string
	maxVisibleLines     int
	filterMode          FilterMode
	searchQuery         string
	foldedNodes         map[string]bool
	visibleChildren     map[string]bool
	showLabelTimestamps bool
	multipleRoots       bool
	activePathIds       map[string]bool
	toolCallMap         map[string]*ai.ToolCall

	OnSelect    func(entryId string)
	OnCancel    func()
	OnLabelEdit func(entryId string, currentLabel *string)
	OnCopy      func(text *string)
}

// NewTreeList constructs a TreeList from a session tree, leaf pointer, viewport line cap, and filter options.
func NewTreeList(
	tree []core.SessionTreeNode,
	currentLeafId *string,
	maxVisibleLines int,
	initialSelectedId *string,
	initialFilterMode ...FilterMode,
) *TreeList {
	leaf := ""
	if currentLeafId != nil {
		leaf = *currentLeafId
	}
	mode := core.TreeFilterModeDefault
	if len(initialFilterMode) > 0 && initialFilterMode[0] != "" {
		mode = initialFilterMode[0]
	}
	if maxVisibleLines < 1 {
		maxVisibleLines = 1
	}

	tl := &TreeList{
		nodeById:        make(map[string]*tselFlatNode),
		currentLeafId:   leaf,
		maxVisibleLines: maxVisibleLines,
		filterMode:      mode,
		foldedNodes:     make(map[string]bool),
		visibleChildren: make(map[string]bool),
		multipleRoots:   len(tree) > 1,
		activePathIds:   make(map[string]bool),
		toolCallMap:     make(map[string]*ai.ToolCall),
	}

	tl.flattenTree(tree)
	tl.buildActivePath()

	if initialSelectedId != nil && *initialSelectedId != "" {
		tl.applyFilter(*initialSelectedId)
	} else if tl.currentLeafId != "" {
		tl.applyFilter(tl.currentLeafId)
	} else {
		tl.applyFilter("")
		if len(tl.filteredNodes) > 0 {
			tl.selectedIndex = len(tl.filteredNodes) - 1
		}
	}

	return tl
}

func (t *TreeList) flattenTree(roots []core.SessionTreeNode) {
	// Post-order computation of active-branch containment.
	containsActive := make(map[string]bool)
	var computeActive func(node *core.SessionTreeNode) bool
	computeActive = func(node *core.SessionTreeNode) bool {
		id := node.Entry.GetId()
		active := t.currentLeafId != "" && id == t.currentLeafId
		for i := range node.Children {
			if computeActive(&node.Children[i]) {
				active = true
			}
		}
		containsActive[id] = active
		return active
	}
	for i := range roots {
		computeActive(&roots[i])
	}

	sortedRoots := slices.Clone(roots)
	slices.SortStableFunc(sortedRoots, func(a, b core.SessionTreeNode) int {
		aAct := containsActive[a.Entry.GetId()]
		bAct := containsActive[b.Entry.GetId()]
		if aAct && !bAct {
			return -1
		}
		if !aAct && bAct {
			return 1
		}
		return 0
	})

	var walk func(node core.SessionTreeNode, fromExtension bool)
	walk = func(node core.SessionTreeNode, fromExtension bool) {
		nodeFromExt := fromExtension || tselIsExtensionEntry(node.Entry)
		if msgEntry, ok := node.Entry.(*core.SessionMessageEntry); ok {
			if asst, ok := msgEntry.Message.(*ai.AssistantMessage); ok {
				for _, block := range asst.Content {
					if tc, ok := block.(*ai.ToolCall); ok && tc != nil && tc.Id != "" {
						t.toolCallMap[tc.Id] = tc
					}
				}
			}
		}

		fn := &tselFlatNode{
			node:          node,
			fromExtension: nodeFromExt,
		}
		t.flatNodes = append(t.flatNodes, fn)
		t.nodeById[node.Entry.GetId()] = fn

		children := slices.Clone(node.Children)
		slices.SortStableFunc(children, func(a, b core.SessionTreeNode) int {
			aAct := containsActive[a.Entry.GetId()]
			bAct := containsActive[b.Entry.GetId()]
			if aAct && !bAct {
				return -1
			}
			if !aAct && bAct {
				return 1
			}
			return 0
		})
		for _, child := range children {
			walk(child, nodeFromExt)
		}
	}

	for _, root := range sortedRoots {
		walk(root, false)
	}
}

func (t *TreeList) buildActivePath() {
	clear(t.activePathIds)
	curId := t.currentLeafId
	for curId != "" {
		t.activePathIds[curId] = true
		fn, ok := t.nodeById[curId]
		if !ok {
			break
		}
		p := fn.node.Entry.GetParentId()
		if p == nil {
			break
		}
		curId = *p
	}
}

func (t *TreeList) shouldShowBaseEntry(fn *tselFlatNode) bool {
	entry := fn.node.Entry
	if _, ok := entry.(*core.UsageEntry); ok {
		return false
	}
	if msgEntry, ok := entry.(*core.SessionMessageEntry); ok {
		if asst, ok := msgEntry.Message.(*ai.AssistantMessage); ok {
			hasText := tselNormalizeInline(tselExtractContentListText(asst.Content)) != ""
			isEmptyToolOrStop := !hasText && (asst.StopReason == ai.StopReasonStop ||
				asst.StopReason == ai.StopReasonToolUse ||
				asst.StopReason == "")
			if isEmptyToolOrStop && entry.GetId() != t.currentLeafId {
				return false
			}
		}
	}
	return true
}

func (t *TreeList) matchesFilterMode(fn *tselFlatNode) bool {
	if !t.shouldShowBaseEntry(fn) {
		return false
	}
	entry := fn.node.Entry
	switch t.filterMode {
	case core.TreeFilterModeAll:
		return true
	case core.TreeFilterModeNoTools:
		if tselIsSettingsEntry(entry) {
			return false
		}
		if msgEntry, ok := entry.(*core.SessionMessageEntry); ok {
			if _, isToolRes := msgEntry.Message.(*ai.ToolResultMessage); isToolRes {
				return false
			}
		}
		return true
	case core.TreeFilterModeUserOnly:
		if msgEntry, ok := entry.(*core.SessionMessageEntry); ok {
			if _, isUser := msgEntry.Message.(*ai.UserMessage); isUser {
				return true
			}
		}
		return false
	case core.TreeFilterModeLabeledOnly:
		return fn.node.Label != nil && *fn.node.Label != ""
	default: // TreeFilterModeDefault
		return !tselIsSettingsEntry(entry)
	}
}

func (t *TreeList) isFoldedOrAncestorFolded(fn *tselFlatNode) bool {
	p := fn.node.Entry.GetParentId()
	for p != nil && *p != "" {
		if t.foldedNodes[*p] {
			return true
		}
		parentNode, ok := t.nodeById[*p]
		if !ok {
			break
		}
		p = parentNode.node.Entry.GetParentId()
	}
	return false
}

func (t *TreeList) applyFilter(preferredId ...string) {
	lastSelectedId := ""
	if len(preferredId) > 0 && preferredId[0] != "" {
		lastSelectedId = preferredId[0]
	} else if t.selectedIndex >= 0 && t.selectedIndex < len(t.filteredNodes) {
		lastSelectedId = t.filteredNodes[t.selectedIndex].node.Entry.GetId()
	}

	searchTokens := strings.Fields(strings.ToLower(t.searchQuery))
	var visible []*tselFlatNode
	for _, fn := range t.flatNodes {
		if !t.matchesFilterMode(fn) {
			continue
		}
		if len(searchTokens) > 0 {
			searchable := strings.ToLower(t.getSearchableText(fn))
			matched := true
			for _, tok := range searchTokens {
				if !strings.Contains(searchable, tok) {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
		}
		if t.isFoldedOrAncestorFolded(fn) {
			continue
		}
		visible = append(visible, fn)
	}

	t.filteredNodes = visible
	t.recalculateVisualStructure()

	if len(t.filteredNodes) == 0 {
		t.selectedIndex = 0
		return
	}
	if lastSelectedId != "" {
		if idx := t.findNearestVisibleIndex(lastSelectedId); idx >= 0 {
			t.selectedIndex = idx
			return
		}
	}
	if t.selectedIndex >= len(t.filteredNodes) {
		t.selectedIndex = len(t.filteredNodes) - 1
	}
	if t.selectedIndex < 0 {
		t.selectedIndex = 0
	}
}

func (t *TreeList) findNearestVisibleIndex(targetId string) int {
	visibleIdx := make(map[string]int, len(t.filteredNodes))
	for i, fn := range t.filteredNodes {
		visibleIdx[fn.node.Entry.GetId()] = i
	}
	curId := targetId
	for curId != "" {
		if idx, ok := visibleIdx[curId]; ok {
			return idx
		}
		fn, ok := t.nodeById[curId]
		if !ok {
			break
		}
		p := fn.node.Entry.GetParentId()
		if p == nil {
			break
		}
		curId = *p
	}
	return -1
}

func (t *TreeList) recalculateVisualStructure() {
	clear(t.visibleChildren)
	if len(t.filteredNodes) == 0 {
		return
	}

	visibleMap := make(map[string]*tselFlatNode, len(t.filteredNodes))
	for _, fn := range t.filteredNodes {
		visibleMap[fn.node.Entry.GetId()] = fn
		fn.indent = 0
		fn.showConnector = false
		fn.isLast = false
		fn.gutters = nil
	}

	// Find nearest visible ancestor for each filtered node.
	visualParent := make(map[string]string, len(t.filteredNodes))
	childrenByParent := make(map[string][]*tselFlatNode)
	for _, fn := range t.filteredNodes {
		id := fn.node.Entry.GetId()
		vp := ""
		p := fn.node.Entry.GetParentId()
		for p != nil && *p != "" {
			if _, ok := visibleMap[*p]; ok {
				vp = *p
				break
			}
			ancestor, ok := t.nodeById[*p]
			if !ok {
				break
			}
			p = ancestor.node.Entry.GetParentId()
		}
		visualParent[id] = vp
		childrenByParent[vp] = append(childrenByParent[vp], fn)
		if vp != "" {
			t.visibleChildren[vp] = true
		}
	}

	// Nodes that have folded children are also considered foldable (their hidden children count when folded).
	for id := range t.foldedNodes {
		if fn, ok := visibleMap[id]; ok && len(fn.node.Children) > 0 {
			t.visibleChildren[id] = true
		}
	}

	roots := childrenByParent[""]
	multipleRoots := len(roots) > 1

	var assign func(fn *tselFlatNode, indent int, showConnector bool, isLast bool, gutters []tselGutterInfo)
	assign = func(fn *tselFlatNode, indent int, showConnector bool, isLast bool, gutters []tselGutterInfo) {
		fn.indent = indent
		fn.showConnector = showConnector
		fn.isLast = isLast
		fn.gutters = slices.Clone(gutters)

		childGutters := slices.Clone(gutters)
		if showConnector && indent > 0 {
			childGutters = append(childGutters, tselGutterInfo{
				position: indent - 1,
				show:     !isLast,
			})
		}

		children := childrenByParent[fn.node.Entry.GetId()]
		multipleChildren := len(children) > 1
		for i, child := range children {
			childLast := i == len(children)-1
			if multipleChildren {
				assign(child, indent+1, true, childLast, childGutters)
			} else {
				assign(child, indent, false, childLast, childGutters)
			}
		}
	}

	for i, root := range roots {
		rootLast := i == len(roots)-1
		if multipleRoots {
			assign(root, 1, true, rootLast, nil)
		} else {
			assign(root, 0, false, rootLast, nil)
		}
	}
}

func (t *TreeList) getSearchableText(fn *tselFlatNode) string {
	var parts []string
	if fn.node.Label != nil && *fn.node.Label != "" {
		parts = append(parts, *fn.node.Label)
	}
	entry := fn.node.Entry
	switch e := entry.(type) {
	case *core.SessionMessageEntry:
		switch m := e.Message.(type) {
		case *ai.UserMessage:
			parts = append(parts, "user:", tselExtractUserMessageText(m.Content))
		case *ai.AssistantMessage:
			parts = append(parts, "assistant:", tselExtractContentListText(m.Content))
			if m.ErrorMessage != nil {
				parts = append(parts, *m.ErrorMessage)
			}
		case *ai.ToolResultMessage:
			parts = append(parts, m.ToolName)
			if tc, ok := t.toolCallMap[m.ToolCallId]; ok {
				parts = append(parts, tselFormatToolCall(tc.Name, tc.Arguments))
			}
		case *core.BashExecutionMessage:
			parts = append(parts, "bash:", m.Command)
		case *core.CustomMessage:
			parts = append(parts, m.CustomType, tselExtractUserMessageText(m.Content))
		case *core.BranchSummaryMessage:
			parts = append(parts, "branch summary:", m.Summary)
		case *core.CompactionSummaryMessage:
			parts = append(parts, "compaction:", m.Summary)
		default:
			parts = append(parts, m.GetRole())
		}
	case *core.CustomMessageEntry:
		parts = append(parts, e.CustomType, tselExtractUserMessageText(e.Content))
	case *core.CompactionEntry:
		parts = append(parts, "compaction", e.Summary)
	case *core.BranchSummaryEntry:
		parts = append(parts, "branch summary", e.Summary)
	case *core.ModelChangeEntry:
		parts = append(parts, "model:", e.ModelId)
	case *core.ThinkingLevelChangeEntry:
		parts = append(parts, "thinking:", e.ThinkingLevel)
	case *core.CustomEntry:
		parts = append(parts, "custom:", e.CustomType)
	case *core.ContextEditEntry:
		action := "omit"
		if e.Replacement != nil {
			action = "replace"
		}
		parts = append(parts, "context", action, e.TargetId)
	case *core.LabelEntry:
		if e.Label != nil {
			parts = append(parts, "label:", *e.Label)
		} else {
			parts = append(parts, "label: (cleared)")
		}
	case *core.SessionInfoEntry:
		if e.Name != nil {
			parts = append(parts, "title:", *e.Name)
		} else {
			parts = append(parts, "title:")
		}
	}
	return strings.Join(parts, " ")
}

func (t *TreeList) getEntryDisplayText(th *theme.Theme, fn *tselFlatNode, isSelected bool) string {
	entry := fn.node.Entry
	style := func(s string) string {
		if isSelected {
			return th.Bold(s)
		}
		return s
	}

	var result string
	switch e := entry.(type) {
	case *core.SessionMessageEntry:
		switch m := e.Message.(type) {
		case *ai.UserMessage:
			content := tselNormalizeInline(tselExtractUserMessageText(m.Content))
			result = th.Fg(theme.FgAccent, "user: ") + content
		case *ai.AssistantMessage:
			content := tselNormalizeInline(tselExtractContentListText(m.Content))
			if content != "" {
				result = th.Fg(theme.FgSuccess, "assistant: ") + content
			} else if m.StopReason == ai.StopReasonAborted {
				result = th.Fg(theme.FgSuccess, "assistant: ") + th.Fg(theme.FgMuted, "(aborted)")
			} else if m.ErrorMessage != nil && strings.TrimSpace(*m.ErrorMessage) != "" {
				errMsg := tselNormalizeInline(*m.ErrorMessage)
				if len([]rune(errMsg)) > 80 {
					errMsg = string([]rune(errMsg)[:80])
				}
				result = th.Fg(theme.FgSuccess, "assistant: ") + th.Fg(theme.FgError, errMsg)
			} else {
				result = th.Fg(theme.FgSuccess, "assistant: ") + th.Fg(theme.FgMuted, "(no content)")
			}
		case *ai.ToolResultMessage:
			if tc, ok := t.toolCallMap[m.ToolCallId]; ok {
				result = th.Fg(theme.FgMuted, tselFormatToolCall(tc.Name, tc.Arguments))
			} else {
				toolName := m.ToolName
				if toolName == "" {
					toolName = "tool"
				}
				result = th.Fg(theme.FgMuted, fmt.Sprintf("[%s]", toolName))
			}
		case *core.BashExecutionMessage:
			result = th.Fg(theme.FgDim, "[bash]: "+tselNormalizeInline(m.Command))
		case *core.CustomMessage:
			content := tselNormalizeInline(tselExtractUserMessageText(m.Content))
			result = th.Fg(theme.FgCustomMessageLabel, "["+m.CustomType+"]: ") + content
		case *core.BranchSummaryMessage:
			result = th.Fg(theme.FgWarning, "[branch summary]: ") + tselNormalizeInline(m.Summary)
		case *core.CompactionSummaryMessage:
			tokens := int(js.Round(float64(m.TokensBefore) / 1000.0))
			result = th.Fg(theme.FgBorderAccent, fmt.Sprintf("[compaction: %dk tokens]", tokens))
		default:
			result = th.Fg(theme.FgDim, "["+m.GetRole()+"]")
		}
	case *core.CustomMessageEntry:
		content := tselNormalizeInline(tselExtractUserMessageText(e.Content))
		result = th.Fg(theme.FgCustomMessageLabel, "["+e.CustomType+"]: ") + content
	case *core.CompactionEntry:
		tokens := int(js.Round(float64(e.TokensBefore) / 1000.0))
		result = th.Fg(theme.FgBorderAccent, fmt.Sprintf("[compaction: %dk tokens]", tokens))
	case *core.BranchSummaryEntry:
		result = th.Fg(theme.FgWarning, "[branch summary]: ") + tselNormalizeInline(e.Summary)
	case *core.ModelChangeEntry:
		result = th.Fg(theme.FgDim, fmt.Sprintf("[model: %s]", e.ModelId))
	case *core.ThinkingLevelChangeEntry:
		result = th.Fg(theme.FgDim, fmt.Sprintf("[thinking: %s]", e.ThinkingLevel))
	case *core.CustomEntry:
		result = th.Fg(theme.FgDim, fmt.Sprintf("[custom: %s]", e.CustomType))
	case *core.ContextEditEntry:
		action := "omit"
		if e.Replacement != nil {
			action = "replace"
		}
		result = th.Fg(theme.FgDim, fmt.Sprintf("[context %s: %s]", action, e.TargetId))
	case *core.LabelEntry:
		if e.Label != nil && *e.Label != "" {
			result = th.Fg(theme.FgDim, fmt.Sprintf("[label: %s]", *e.Label))
		} else {
			result = th.Fg(theme.FgDim, "[label: (cleared)]")
		}
	case *core.SessionInfoEntry:
		if e.Name != nil && *e.Name != "" {
			result = th.Fg(theme.FgDim, fmt.Sprintf("[title: %s]", *e.Name))
		} else {
			result = th.Fg(theme.FgDim, "[title: ") + th.Italic(th.Fg(theme.FgMuted, "empty")) + th.Fg(theme.FgDim, "]")
		}
	default:
		result = th.Fg(theme.FgDim, fmt.Sprintf("[%s]", entry.GetType()))
	}

	return style(result)
}

func (t *TreeList) getEntryCopyText(fn *tselFlatNode) *string {
	if fn == nil {
		return nil
	}
	trimOpt := func(s string) *string {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return nil
		}
		return &trimmed
	}
	switch e := fn.node.Entry.(type) {
	case *core.SessionMessageEntry:
		switch m := e.Message.(type) {
		case *core.BashExecutionMessage:
			return trimOpt(m.Command)
		case *ai.UserMessage:
			return trimOpt(tselExtractUserMessageText(m.Content))
		case *ai.AssistantMessage:
			if txt := trimOpt(tselExtractContentListText(m.Content)); txt != nil {
				return txt
			}
			if m.ErrorMessage != nil {
				return trimOpt(*m.ErrorMessage)
			}
			return nil
		case *core.CustomMessage:
			return trimOpt(tselExtractUserMessageText(m.Content))
		case *core.BranchSummaryMessage:
			return trimOpt(m.Summary)
		case *core.CompactionSummaryMessage:
			return trimOpt(m.Summary)
		default:
			return nil
		}
	case *core.CustomMessageEntry:
		return trimOpt(tselExtractUserMessageText(e.Content))
	case *core.CompactionEntry:
		return trimOpt(e.Summary)
	case *core.BranchSummaryEntry:
		return trimOpt(e.Summary)
	default:
		return nil
	}
}

func (t *TreeList) formatPrefix(th *theme.Theme, fn *tselFlatNode) string {
	gutterByPos := make(map[int]bool, len(fn.gutters))
	for _, g := range fn.gutters {
		gutterByPos[g.position] = g.show
	}

	var chars strings.Builder
	isFolded := t.foldedNodes[fn.node.Entry.GetId()]
	isFoldable := t.visibleChildren[fn.node.Entry.GetId()]

	if fn.indent > 0 {
		for i := 0; i < fn.indent; i++ {
			isConnectorLevel := fn.showConnector && i == fn.indent-1
			if isConnectorLevel {
				base := "├─"
				if fn.isLast {
					base = "└─"
				}
				if isFolded {
					if fn.isLast {
						base = "└⊞"
					} else {
						base = "├⊞"
					}
				} else if isFoldable {
					if fn.isLast {
						base = "└⊟"
					} else {
						base = "├⊟"
					}
				}
				chars.WriteString(base)
				chars.WriteString(" ")
			} else if gutterByPos[i] {
				chars.WriteString("│  ")
			} else {
				chars.WriteString("   ")
			}
		}
	}

	prefixStr := chars.String()
	if prefixStr != "" {
		return th.Fg(theme.FgDim, prefixStr)
	}
	return ""
}

// Invalidate clears cached state.
func (t *TreeList) Invalidate() {}

// Render renders visible filtered tree nodes and status footer to fit within width.
func (t *TreeList) Render(width int) []string {
	t.mu.Lock()
	filtered := slices.Clone(t.filteredNodes)
	selectedIdx := t.selectedIndex
	maxVis := t.maxVisibleLines
	filterMode := t.filterMode
	showTs := t.showLabelTimestamps
	activePathIds := make(map[string]bool, len(t.activePathIds))
	for k, v := range t.activePathIds {
		activePathIds[k] = v
	}
	folded := make(map[string]bool, len(t.foldedNodes))
	for k, v := range t.foldedNodes {
		folded[k] = v
	}
	t.mu.Unlock()

	th := theme.CurrentTheme()
	var lines []string

	var statusLabels []string
	if filterMode != core.TreeFilterModeDefault {
		statusLabels = append(statusLabels, fmt.Sprintf("[%s]", filterMode))
	}
	if showTs {
		statusLabels = append(statusLabels, "[timestamps]")
	}
	statusSuffix := ""
	if len(statusLabels) > 0 {
		statusSuffix = " " + strings.Join(statusLabels, " ")
	}

	if len(filtered) == 0 {
		lines = append(lines, tui.TruncateToWidth(th.Fg(theme.FgMuted, "  No entries found"), width, "…", false))
		lines = append(lines, tui.TruncateToWidth(th.Fg(theme.FgMuted, "  (0/0)"+statusSuffix), width, "…", false))
		return lines
	}

	startIndex := max(0, min(selectedIdx-maxVis/2, len(filtered)-maxVis))
	endIndex := min(startIndex+maxVis, len(filtered))

	rows := make([]tselRenderedRow, 0, endIndex-startIndex)
	windowSelectedIdx := selectedIdx - startIndex

	for i := startIndex; i < endIndex; i++ {
		fn := filtered[i]
		isSelected := i == selectedIdx
		entryId := fn.node.Entry.GetId()

		cursor := "  "
		if isSelected {
			cursor = th.Fg(theme.FgAccent, "› ")
		}

		prefix := t.formatPrefix(th, fn)
		foldMarker := ""
		if !fn.showConnector && folded[entryId] {
			foldMarker = th.Fg(theme.FgDim, "⊞ ")
		}
		activeMarker := ""
		if activePathIds[entryId] {
			activeMarker = th.Fg(theme.FgAccent, "• ")
		}
		labelText := ""
		if fn.node.Label != nil && *fn.node.Label != "" {
			labelText = th.Fg(theme.FgWarning, "["+*fn.node.Label+"] ")
		}
		tsText := ""
		if showTs && fn.node.LabelTimestamp != nil {
			if formatted := tselFormatLabelTimestamp(fn.node.LabelTimestamp); formatted != nil {
				tsText = th.Fg(theme.FgMuted, *formatted+" ")
			}
		}

		anchorStr := prefix + foldMarker
		anchorCol := tui.VisibleWidth(anchorStr)
		displayText := t.getEntryDisplayText(th, fn, isSelected)
		body := anchorStr + activeMarker + labelText + tsText + displayText

		rows = append(rows, tselRenderedRow{
			cursor:    cursor,
			body:      body,
			anchorCol: anchorCol,
		})
	}

	lines = append(lines, tselRenderHorizontalViewport(rows, windowSelectedIdx, width)...)

	counter := fmt.Sprintf("  (%d/%d)%s", selectedIdx+1, len(filtered), statusSuffix)
	lines = append(lines, tui.TruncateToWidth(th.Fg(theme.FgMuted, counter), width, "…", false))
	return lines
}

func (t *TreeList) findBranchSegmentStartLocked(direction string) int {
	n := len(t.filteredNodes)
	if n == 0 {
		return 0
	}
	cur := t.filteredNodes[t.selectedIndex]
	if direction == "up" {
		if t.selectedIndex == 0 {
			return 0
		}
		for i := t.selectedIndex - 1; i >= 0; i-- {
			cand := t.filteredNodes[i]
			if i == 0 || cand.showConnector || cand.indent != cur.indent {
				return i
			}
		}
		return 0
	}
	if t.selectedIndex >= n-1 {
		return n - 1
	}
	for i := t.selectedIndex + 1; i < n; i++ {
		cand := t.filteredNodes[i]
		if i == n-1 || cand.showConnector || cand.indent != cur.indent {
			return i
		}
	}
	return n - 1
}

// HandleInput processes tree navigation, folding, filtering, labeling, copy, and search keystrokes.
func (t *TreeList) HandleInput(keyData string) {
	kb := tui.GetKeybindings()

	if kb.Matches(keyData, tui.KeybindingTuiSelectUp) {
		t.mu.Lock()
		if len(t.filteredNodes) > 0 {
			if t.selectedIndex == 0 {
				t.selectedIndex = len(t.filteredNodes) - 1
			} else {
				t.selectedIndex--
			}
		}
		t.mu.Unlock()
		return
	}

	if kb.Matches(keyData, tui.KeybindingTuiSelectDown) {
		t.mu.Lock()
		if len(t.filteredNodes) > 0 {
			if t.selectedIndex == len(t.filteredNodes)-1 {
				t.selectedIndex = 0
			} else {
				t.selectedIndex++
			}
		}
		t.mu.Unlock()
		return
	}

	if kb.Matches(keyData, core.KeybindingAppTreeFoldOrUp) {
		t.mu.Lock()
		if len(t.filteredNodes) > 0 {
			sel := t.filteredNodes[t.selectedIndex]
			selId := sel.node.Entry.GetId()
			if t.visibleChildren[selId] && !t.foldedNodes[selId] {
				t.foldedNodes[selId] = true
				t.applyFilter(selId)
			} else {
				t.selectedIndex = t.findBranchSegmentStartLocked("up")
			}
		}
		t.mu.Unlock()
		return
	}

	if kb.Matches(keyData, core.KeybindingAppTreeUnfoldOrDown) {
		t.mu.Lock()
		if len(t.filteredNodes) > 0 {
			sel := t.filteredNodes[t.selectedIndex]
			selId := sel.node.Entry.GetId()
			if t.foldedNodes[selId] {
				delete(t.foldedNodes, selId)
				t.applyFilter(selId)
			} else {
				t.selectedIndex = t.findBranchSegmentStartLocked("down")
			}
		}
		t.mu.Unlock()
		return
	}

	if kb.Matches(keyData, tui.KeybindingTuiEditorCursorLeft) || kb.Matches(keyData, tui.KeybindingTuiSelectPageUp) {
		t.mu.Lock()
		if len(t.filteredNodes) > 0 {
			t.selectedIndex = max(0, t.selectedIndex-t.maxVisibleLines)
		}
		t.mu.Unlock()
		return
	}

	if kb.Matches(keyData, tui.KeybindingTuiEditorCursorRight) || kb.Matches(keyData, tui.KeybindingTuiSelectPageDown) {
		t.mu.Lock()
		if len(t.filteredNodes) > 0 {
			t.selectedIndex = min(len(t.filteredNodes)-1, t.selectedIndex+t.maxVisibleLines)
		}
		t.mu.Unlock()
		return
	}

	if kb.Matches(keyData, tui.KeybindingTuiSelectConfirm) {
		t.mu.Lock()
		var selId string
		hasSel := false
		if len(t.filteredNodes) > 0 && t.selectedIndex >= 0 && t.selectedIndex < len(t.filteredNodes) {
			selId = t.filteredNodes[t.selectedIndex].node.Entry.GetId()
			hasSel = true
		}
		onSel := t.OnSelect
		t.mu.Unlock()
		if hasSel && onSel != nil {
			onSel(selId)
		}
		return
	}

	if kb.Matches(keyData, core.KeybindingAppMessageCopy) {
		t.mu.Lock()
		var sel *tselFlatNode
		if len(t.filteredNodes) > 0 && t.selectedIndex >= 0 && t.selectedIndex < len(t.filteredNodes) {
			sel = t.filteredNodes[t.selectedIndex]
		}
		copyText := t.getEntryCopyText(sel)
		onCopy := t.OnCopy
		t.mu.Unlock()
		if onCopy != nil {
			onCopy(copyText)
		}
		return
	}

	if kb.Matches(keyData, tui.KeybindingTuiSelectCancel) {
		t.mu.Lock()
		hasQuery := t.searchQuery != ""
		if hasQuery {
			t.searchQuery = ""
			clear(t.foldedNodes)
			t.applyFilter()
		}
		onCancel := t.OnCancel
		t.mu.Unlock()
		if !hasQuery && onCancel != nil {
			onCancel()
		}
		return
	}

	toggleFilter := func(target FilterMode) {
		t.mu.Lock()
		clear(t.foldedNodes)
		if t.filterMode == target {
			t.filterMode = core.TreeFilterModeDefault
		} else {
			t.filterMode = target
		}
		t.applyFilter()
		t.mu.Unlock()
	}

	if kb.Matches(keyData, core.KeybindingAppTreeFilterDefault) {
		t.mu.Lock()
		clear(t.foldedNodes)
		t.filterMode = core.TreeFilterModeDefault
		t.applyFilter()
		t.mu.Unlock()
		return
	}
	if kb.Matches(keyData, core.KeybindingAppTreeFilterNoTools) {
		toggleFilter(core.TreeFilterModeNoTools)
		return
	}
	if kb.Matches(keyData, core.KeybindingAppTreeFilterUserOnly) {
		toggleFilter(core.TreeFilterModeUserOnly)
		return
	}
	if kb.Matches(keyData, core.KeybindingAppTreeFilterLabeledOnly) {
		toggleFilter(core.TreeFilterModeLabeledOnly)
		return
	}
	if kb.Matches(keyData, core.KeybindingAppTreeFilterAll) {
		toggleFilter(core.TreeFilterModeAll)
		return
	}
	if kb.Matches(keyData, core.KeybindingAppTreeFilterCycleForward) {
		t.mu.Lock()
		clear(t.foldedNodes)
		idx := slices.Index(tselFilterCycle, t.filterMode)
		if idx < 0 {
			idx = 0
		}
		t.filterMode = tselFilterCycle[(idx+1)%len(tselFilterCycle)]
		t.applyFilter()
		t.mu.Unlock()
		return
	}
	if kb.Matches(keyData, core.KeybindingAppTreeFilterCycleBack) {
		t.mu.Lock()
		clear(t.foldedNodes)
		idx := slices.Index(tselFilterCycle, t.filterMode)
		if idx < 0 {
			idx = 0
		}
		t.filterMode = tselFilterCycle[(idx-1+len(tselFilterCycle))%len(tselFilterCycle)]
		t.applyFilter()
		t.mu.Unlock()
		return
	}

	if kb.Matches(keyData, core.KeybindingAppTreeEditLabel) {
		t.mu.Lock()
		var selId string
		var curLabel *string
		hasSel := false
		if len(t.filteredNodes) > 0 && t.selectedIndex >= 0 && t.selectedIndex < len(t.filteredNodes) {
			sel := t.filteredNodes[t.selectedIndex]
			selId = sel.node.Entry.GetId()
			curLabel = sel.node.Label
			hasSel = true
		}
		onEdit := t.OnLabelEdit
		t.mu.Unlock()
		if hasSel && onEdit != nil {
			onEdit(selId, curLabel)
		}
		return
	}

	if kb.Matches(keyData, core.KeybindingAppTreeToggleLabelTs) {
		t.mu.Lock()
		t.showLabelTimestamps = !t.showLabelTimestamps
		t.mu.Unlock()
		return
	}

	if kb.Matches(keyData, tui.KeybindingTuiEditorDeleteCharBackward) {
		t.mu.Lock()
		if len(t.searchQuery) > 0 {
			runes := []rune(t.searchQuery)
			t.searchQuery = string(runes[:len(runes)-1])
			clear(t.foldedNodes)
			t.applyFilter()
		}
		t.mu.Unlock()
		return
	}

	if keyData != "" {
		first := []rune(keyData)[0]
		if len([]rune(keyData)) == 1 && first >= 32 && first != 0x7f {
			t.mu.Lock()
			t.searchQuery += keyData
			clear(t.foldedNodes)
			t.applyFilter()
			t.mu.Unlock()
		}
	}
}

// GetSelectedNode returns the currently highlighted tree node, or nil if none is visible.
func (t *TreeList) GetSelectedNode() *core.SessionTreeNode {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.selectedIndex < 0 || t.selectedIndex >= len(t.filteredNodes) {
		return nil
	}
	copyNode := t.filteredNodes[t.selectedIndex].node
	return &copyNode
}

// GetFilterMode returns the active tree filter mode.
func (t *TreeList) GetFilterMode() FilterMode {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.filterMode
}

// SetFilterMode switches the active filter mode and re-evaluates visible entries.
func (t *TreeList) SetFilterMode(mode FilterMode) {
	t.mu.Lock()
	defer t.mu.Unlock()
	clear(t.foldedNodes)
	t.filterMode = mode
	t.applyFilter()
}

// GetSearchQuery returns the current search string.
func (t *TreeList) GetSearchQuery() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.searchQuery
}

// UpdateNodeLabel updates the label and timestamp of an entry in place and refreshes filtering.
func (t *TreeList) UpdateNodeLabel(entryId string, label *string, labelTimestamp ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fn, ok := t.nodeById[entryId]
	if !ok {
		return
	}
	fn.node.Label = label
	if label == nil || *label == "" {
		fn.node.Label = nil
		fn.node.LabelTimestamp = nil
	} else if len(labelTimestamp) > 0 && labelTimestamp[0] != "" {
		ts := labelTimestamp[0]
		fn.node.LabelTimestamp = &ts
	} else {
		ts := time.Now().UTC().Format(time.RFC3339Nano)
		fn.node.LabelTimestamp = &ts
	}
	t.applyFilter(entryId)
}

type tselSearchLine struct {
	treeList *TreeList
	focused  bool
}

func (s *tselSearchLine) Focused() bool     { return s.focused }
func (s *tselSearchLine) SetFocused(f bool) { s.focused = f }
func (s *tselSearchLine) Invalidate()       {}

func (s *tselSearchLine) Render(width int) []string {
	th := theme.CurrentTheme()
	query := s.treeList.GetSearchQuery()
	prefix := th.Fg(theme.FgMuted, "  Type to search: ")
	if !s.focused {
		return []string{tui.TruncateToWidth(prefix+query, width, "…", false)}
	}
	avail := max(0, width-1)
	truncated := tui.TruncateToWidth(prefix+query, avail, "…", false)
	return []string{truncated + tui.CursorMarker + "\x1b[7m \x1b[27m"}
}

type tselHelpItem struct {
	action tui.Keybinding
	label  string
}

var tselHelpItems = []tselHelpItem{
	{tui.KeybindingTuiSelectConfirm, "select"},
	{core.KeybindingAppTreeFoldOrUp, "fold/up"},
	{core.KeybindingAppTreeUnfoldOrDown, "unfold/down"},
	{core.KeybindingAppTreeEditLabel, "label"},
	{core.KeybindingAppTreeToggleLabelTs, "timestamps"},
	{core.KeybindingAppMessageCopy, "copy"},
	{core.KeybindingAppTreeFilterCycleForward, "filter"},
	{core.KeybindingAppTreeFilterNoTools, "no-tools"},
	{core.KeybindingAppTreeFilterUserOnly, "user"},
	{core.KeybindingAppTreeFilterLabeledOnly, "labeled"},
	{core.KeybindingAppTreeFilterAll, "all"},
	{core.KeybindingAppTreeFilterDefault, "default"},
}

func tselCompactRawKeys(keys []tui.KeyId) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		r := strings.ReplaceAll(string(k), "pageUp", "pgup")
		r = strings.ReplaceAll(r, "pageDown", "pgdn")
		r = strings.ReplaceAll(r, "up", "↑")
		r = strings.ReplaceAll(r, "down", "↓")
		r = strings.ReplaceAll(r, "left", "←")
		r = strings.ReplaceAll(r, "right", "→")
		r = strings.ReplaceAll(r, "escape", "esc")
		out[i] = r
	}
	return strings.Join(out, "/")
}

type tselTreeHelp struct{}

func (h *tselTreeHelp) Invalidate() {}
func (h *tselTreeHelp) Render(width int) []string {
	th := theme.CurrentTheme()
	kb := tui.GetKeybindings()
	var parts []string
	for _, item := range tselHelpItems {
		keys := kb.GetKeys(item.action)
		if len(keys) == 0 {
			continue
		}
		parts = append(parts, theme.RawKeyHint(th, tselCompactRawKeys(keys), item.label))
	}
	text := "  " + strings.Join(parts, th.Fg(theme.FgMuted, " · "))
	if width <= 0 {
		return []string{text}
	}
	wrapped := tui.WrapTextWithAnsi(text, width)
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}

type tselLabelInput struct {
	input    *tui.Input
	entryId  string
	focused  bool
	onSubmit func(entryId string, value string)
	onCancel func()
}

func tselNewLabelInput(entryId string, currentLabel *string, onSubmit func(string, string), onCancel func()) *tselLabelInput {
	inp := tui.NewInput(nil)
	if currentLabel != nil {
		inp.SetValue(*currentLabel)
	}
	li := &tselLabelInput{
		input:    inp,
		entryId:  entryId,
		onSubmit: onSubmit,
		onCancel: onCancel,
	}
	inp.OnSubmit = func(val string) {
		if li.onSubmit != nil {
			li.onSubmit(li.entryId, val)
		}
	}
	inp.OnEscape = func() {
		if li.onCancel != nil {
			li.onCancel()
		}
	}
	return li
}

func (l *tselLabelInput) Focused() bool { return l.focused }
func (l *tselLabelInput) SetFocused(f bool) {
	l.focused = f
	l.input.SetFocused(f)
}
func (l *tselLabelInput) Invalidate() {}
func (l *tselLabelInput) HandleInput(keyData string) {
	l.input.HandleInput(keyData)
}

func (l *tselLabelInput) Render(width int) []string {
	th := theme.CurrentTheme()
	innerWidth := max(1, width-2)
	inputLines := l.input.Render(innerWidth)
	lines := make([]string, 0, len(inputLines)+2)
	lines = append(lines, tui.TruncateToWidth(th.Fg(theme.FgAccent, "  Label (empty to remove):"), width, "…", false))
	for _, line := range inputLines {
		lines = append(lines, tui.TruncateToWidth("  "+line, width, "", false))
	}
	hint := "  " + theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, "save") +
		th.Fg(theme.FgMuted, "  ") +
		theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "cancel")
	lines = append(lines, tui.TruncateToWidth(hint, width, "…", false))
	return lines
}

// TreeSelectorComponent presents the interactive session tree browser with filtering, search, folding, and label editing.
type TreeSelectorComponent struct {
	*tui.Container

	// OnCopy is invoked when the copy shortcut is pressed with either the trimmed entry text or nil.
	OnCopy func(*string)

	treeList            *TreeList
	searchLine          *tselSearchLine
	treeContainer       *tui.Container
	labelInputContainer *tui.Container

	mu            sync.Mutex
	labelInput    *tselLabelInput
	focused       bool
	onLabelChange func(entryId string, label *string)
	emptyTimer    *time.Timer
	disposed      bool
}

// NewTreeSelectorComponent creates a TreeSelectorComponent centered around the current leaf or initialSelectedId.
func NewTreeSelectorComponent(
	tree []core.SessionTreeNode,
	currentLeafId *string,
	terminalHeight int,
	onSelect func(string),
	onCancel func(),
	onLabelChange func(string, *string),
	initialSelectedId *string,
	initialFilterMode ...FilterMode,
) *TreeSelectorComponent {
	maxVisibleLines := max(5, terminalHeight/2)
	tl := NewTreeList(tree, currentLeafId, maxVisibleLines, initialSelectedId, initialFilterMode...)
	tl.OnSelect = onSelect
	tl.OnCancel = onCancel

	searchLine := &tselSearchLine{treeList: tl}
	treeContainer := tui.NewContainer()
	treeContainer.AddChild(tl)
	labelInputContainer := tui.NewContainer()

	comp := &TreeSelectorComponent{
		Container:           tui.NewContainer(),
		treeList:            tl,
		searchLine:          searchLine,
		treeContainer:       treeContainer,
		labelInputContainer: labelInputContainer,
		onLabelChange:       onLabelChange,
	}

	tl.OnCopy = func(text *string) {
		if comp.OnCopy != nil {
			comp.OnCopy(text)
		}
	}
	tl.OnLabelEdit = func(entryId string, currentLabel *string) {
		comp.showLabelInput(entryId, currentLabel)
	}

	th := theme.CurrentTheme()
	comp.AddChild(tui.NewSpacer(1))
	comp.AddChild(NewDynamicBorder())
	comp.AddChild(tui.NewText(th.Bold("  Session Tree"), 1, 0, nil))
	comp.AddChild(&tselTreeHelp{})
	comp.AddChild(searchLine)
	comp.AddChild(NewDynamicBorder())
	comp.AddChild(tui.NewSpacer(1))
	comp.AddChild(treeContainer)
	comp.AddChild(labelInputContainer)
	comp.AddChild(tui.NewSpacer(1))
	comp.AddChild(NewDynamicBorder())

	if len(tree) == 0 && onCancel != nil {
		comp.emptyTimer = time.AfterFunc(tselEmptyTreeCancelDelay, func() {
			comp.mu.Lock()
			disposed := comp.disposed
			comp.mu.Unlock()
			if !disposed {
				onCancel()
			}
		})
	}

	return comp
}

func (c *TreeSelectorComponent) showLabelInput(entryId string, currentLabel *string) {
	c.mu.Lock()
	li := tselNewLabelInput(
		entryId,
		currentLabel,
		func(id string, value string) {
			trimmed := strings.TrimSpace(value)
			var newLabel *string
			if trimmed != "" {
				newLabel = &trimmed
			}
			c.treeList.UpdateNodeLabel(id, newLabel)
			if c.onLabelChange != nil {
				c.onLabelChange(id, newLabel)
			}
			c.hideLabelInput()
		},
		func() {
			c.hideLabelInput()
		},
	)
	li.SetFocused(c.focused)
	c.labelInput = li
	c.mu.Unlock()

	c.treeContainer.Clear()
	c.labelInputContainer.Clear()
	c.labelInputContainer.AddChild(li)
	c.searchLine.SetFocused(false)
}

func (c *TreeSelectorComponent) hideLabelInput() {
	c.mu.Lock()
	c.labelInput = nil
	focused := c.focused
	c.mu.Unlock()

	c.labelInputContainer.Clear()
	c.treeContainer.Clear()
	c.treeContainer.AddChild(c.treeList)
	c.searchLine.SetFocused(focused)
}

// CapturesViewportInput reports false while inline label editing is active so the host keeps standard input focus.
func (c *TreeSelectorComponent) CapturesViewportInput() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.labelInput == nil
}

// Focused reports whether the tree selector currently holds focus.
func (c *TreeSelectorComponent) Focused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focused
}

// SetFocused propagates focus state to either the inline label input or search line.
func (c *TreeSelectorComponent) SetFocused(focused bool) {
	c.mu.Lock()
	c.focused = focused
	li := c.labelInput
	c.mu.Unlock()

	if li != nil {
		li.SetFocused(focused)
		c.searchLine.SetFocused(false)
	} else {
		c.searchLine.SetFocused(focused)
	}
}

// HandleInput routes input to the active label editor or the underlying TreeList.
func (c *TreeSelectorComponent) HandleInput(keyData string) {
	c.mu.Lock()
	li := c.labelInput
	c.mu.Unlock()
	if li != nil {
		li.HandleInput(keyData)
		return
	}
	c.treeList.HandleInput(keyData)
}

// GetTreeList returns the underlying TreeList instance.
func (c *TreeSelectorComponent) GetTreeList() *TreeList {
	return c.treeList
}

// Dispose cancels any pending empty-tree auto-dismiss timer.
func (c *TreeSelectorComponent) Dispose() {
	c.mu.Lock()
	c.disposed = true
	timer := c.emptyTimer
	c.emptyTimer = nil
	c.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}
