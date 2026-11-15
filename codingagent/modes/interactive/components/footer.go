package components

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// FormatTokens formats a token count compactly (e.g. 950, 1.2k, 50k, 1.5M).
func FormatTokens(count int) string {
	if count < 1000 {
		return strconv.Itoa(count)
	}
	if count < 10000 {
		return fmt.Sprintf("%.1fk", float64(count)/1000.0)
	}
	if count < 1000000 {
		return fmt.Sprintf("%.0fk", math.Round(float64(count)/1000.0))
	}
	if count < 10000000 {
		return fmt.Sprintf("%.1fM", float64(count)/1000000.0)
	}
	return fmt.Sprintf("%.0fM", math.Round(float64(count)/1000000.0))
}

// FormatCwdForFooter shortens cwd by substituting the user's home directory prefix with "~".
func FormatCwdForFooter(cwd string, home *string) string {
	var h string
	if home != nil {
		h = *home
	} else {
		if userHome, err := os.UserHomeDir(); err == nil {
			h = userHome
		}
	}
	if h == "" {
		return cwd
	}
	if cwd == h {
		return "~"
	}
	if strings.HasPrefix(cwd, h+"/") || (filepath.Separator != '/' && strings.HasPrefix(cwd, h+string(filepath.Separator))) {
		return "~" + cwd[len(h):]
	}
	return cwd
}

// FooterComponent renders session working directory, git branch, token/cost totals,
// context usage, active/routed model info, and sorted extension statuses.
type FooterComponent struct {
	mu                 sync.Mutex
	session            *core.AgentSession
	footerData         core.ReadonlyFooterDataProvider
	autoCompactEnabled bool
	branchUnsubscribe  func()

	cachedTotals     core.UsageTotals
	cachedEntryCount int
	cachedSessionId  string
	cachedLeafId     string
	statsValid       bool
}

// NewFooterComponent constructs a FooterComponent subscribed to branch updates.
func NewFooterComponent(session *core.AgentSession, footerData core.ReadonlyFooterDataProvider) *FooterComponent {
	f := &FooterComponent{
		session:            session,
		footerData:         footerData,
		autoCompactEnabled: true,
	}
	if footerData != nil {
		f.branchUnsubscribe = footerData.OnBranchChange(func() {
			f.Invalidate()
		})
	}
	return f
}

// SetSession swaps the bound AgentSession and invalidates cached stats.
func (f *FooterComponent) SetSession(session *core.AgentSession) {
	f.mu.Lock()
	f.session = session
	f.statsValid = false
	f.mu.Unlock()
}

// SetAutoCompactEnabled controls whether "(auto)" appears next to context window capacity.
func (f *FooterComponent) SetAutoCompactEnabled(enabled bool) {
	f.mu.Lock()
	f.autoCompactEnabled = enabled
	f.mu.Unlock()
}

// Invalidate clears cached usage statistics so the next Render recomputes them.
func (f *FooterComponent) Invalidate() {
	f.mu.Lock()
	f.statsValid = false
	f.mu.Unlock()
}

// Dispose releases the footer's git branch change listener.
func (f *FooterComponent) Dispose() {
	f.mu.Lock()
	unsub := f.branchUnsubscribe
	f.branchUnsubscribe = nil
	f.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}

// Render builds footer lines constrained to width terminal columns.
func (f *FooterComponent) Render(width int) []string {
	if width <= 0 {
		return []string{}
	}

	f.mu.Lock()
	session := f.session
	footerData := f.footerData
	autoCompact := f.autoCompactEnabled
	f.mu.Unlock()

	if session == nil || session.SessionManager == nil {
		return []string{}
	}

	th := theme.CurrentTheme()
	sm := session.SessionManager
	totals := f.footerSessionTotals(sm)

	// Line 1: pwd (git branch) • session name
	pwd := FormatCwdForFooter(sm.GetCwd(), nil)
	if footerData != nil {
		if branch := footerData.GetGitBranch(); branch != nil && *branch != "" {
			pwd += fmt.Sprintf(" (%s)", *branch)
		}
	}
	if name := sm.GetSessionName(); name != nil && *name != "" {
		pwd += fmt.Sprintf(" • %s", *name)
	}
	if tui.VisibleWidth(pwd) > width {
		pwd = tui.TruncateToWidth(pwd, width, "...", false)
	}
	lines := []string{th.Fg(theme.FgDim, pwd)}

	// Line 2: left usage stats + right model/thinking/provider/experimental
	var leftParts []string
	if totals.Input > 0 {
		leftParts = append(leftParts, fmt.Sprintf("↑%s", FormatTokens(totals.Input)))
	}
	if totals.Output > 0 {
		leftParts = append(leftParts, fmt.Sprintf("↓%s", FormatTokens(totals.Output)))
	}
	if totals.CacheRead > 0 {
		leftParts = append(leftParts, fmt.Sprintf("R%s", FormatTokens(totals.CacheRead)))
	}
	if totals.CacheWrite > 0 {
		leftParts = append(leftParts, fmt.Sprintf("W%s", FormatTokens(totals.CacheWrite)))
	}

	activeModel := session.Model()
	routed := session.RoutedModel()
	usingSub := false
	if rt := session.ModelRuntime(); rt != nil && activeModel != nil {
		usingSub = rt.IsUsingSubscription(string(activeModel.Provider))
	}
	if totals.Cost > 0 || usingSub {
		costStr := fmt.Sprintf("$%.3f", totals.Cost)
		if usingSub {
			costStr += " (sub)"
		}
		leftParts = append(leftParts, costStr)
	}

	autoSuffix := ""
	if autoCompact {
		autoSuffix = " (auto)"
	}
	contextUsage := session.GetContextUsage()
	contextWindow := 0
	if contextUsage != nil && contextUsage.ContextWindow > 0 {
		contextWindow = contextUsage.ContextWindow
	} else if activeModel != nil {
		contextWindow = activeModel.ContextWindow
	}

	contextColor := theme.FgDim
	contextDisplay := ""
	if contextUsage != nil && contextWindow > 0 {
		if pct, ok := contextUsage.Percent.Get(); ok {
			contextDisplay = fmt.Sprintf("%.1f%%/%s%s", pct, FormatTokens(contextWindow), autoSuffix)
			if pct > 90 {
				contextColor = theme.FgError
			} else if pct > 70 {
				contextColor = theme.FgWarning
			}
		} else {
			contextDisplay = fmt.Sprintf("?/%s%s", FormatTokens(contextWindow), autoSuffix)
		}
	} else if contextWindow > 0 {
		contextDisplay = fmt.Sprintf("?/%s%s", FormatTokens(contextWindow), autoSuffix)
	}
	if contextDisplay != "" {
		leftParts = append(leftParts, contextDisplay)
	}

	var statsLeftBuilder strings.Builder
	for i, part := range leftParts {
		if i > 0 {
			statsLeftBuilder.WriteString(th.Fg(theme.FgDim, " "))
		}
		if contextDisplay != "" && i == len(leftParts)-1 {
			statsLeftBuilder.WriteString(th.Fg(contextColor, part))
		} else {
			statsLeftBuilder.WriteString(th.Fg(theme.FgDim, part))
		}
	}
	statsLeft := statsLeftBuilder.String()

	statsRight := f.footerBuildRightStats(session, footerData, activeModel, routed, th)

	lines = append(lines, f.footerLayoutStatsLine(statsLeft, statsRight, width)...)

	// Line 3: extension statuses (sorted by key)
	if footerData != nil {
		statuses := footerData.GetExtensionStatuses()
		if len(statuses) > 0 {
			keys := make([]string, 0, len(statuses))
			for k := range statuses {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var statusParts []string
			for _, k := range keys {
				cleaned := footerSanitizeStatus(statuses[k])
				if cleaned != "" {
					statusParts = append(statusParts, cleaned)
				}
			}
			if len(statusParts) > 0 {
				statusLine := strings.Join(statusParts, " ")
				if tui.VisibleWidth(statusLine) > width {
					statusLine = tui.TruncateToWidth(statusLine, width, "...", false)
				}
				lines = append(lines, statusLine)
			}
		}
	}

	return lines
}

func (f *FooterComponent) footerSessionTotals(sm *core.SessionManager) core.UsageTotals {
	entryCount := sm.GetEntryCount()
	sessionId := sm.GetSessionId()
	leafId := ""
	if lid := sm.GetLeafId(); lid != nil {
		leafId = *lid
	}

	f.mu.Lock()
	if f.statsValid && f.cachedEntryCount == entryCount && f.cachedSessionId == sessionId && f.cachedLeafId == leafId {
		cached := f.cachedTotals
		f.mu.Unlock()
		return cached
	}
	f.mu.Unlock()

	totals := core.CreateUsageTotals()
	for _, entry := range sm.GetEntries() {
		switch e := entry.(type) {
		case *core.UsageEntry:
			if e != nil {
				core.AddUsageToTotals(&totals, e.Usage)
			}
		case *core.SessionMessageEntry:
			if e == nil {
				continue
			}
			if msg, ok := e.Message.(*ai.AssistantMessage); ok && msg != nil {
				core.AddUsageToTotals(&totals, msg.Usage)
			} else if msg, ok := e.Message.(*ai.ToolResultMessage); ok && msg != nil && msg.Usage != nil {
				core.AddUsageToTotals(&totals, *msg.Usage)
			}
		case *core.BranchSummaryEntry:
			if e != nil && e.Usage != nil {
				core.AddUsageToTotals(&totals, *e.Usage)
			}
		case *core.CompactionEntry:
			if e != nil && e.Usage != nil {
				core.AddUsageToTotals(&totals, *e.Usage)
			}
		}
	}

	f.mu.Lock()
	f.cachedTotals = totals
	f.cachedEntryCount = entryCount
	f.cachedSessionId = sessionId
	f.cachedLeafId = leafId
	f.statsValid = true
	f.mu.Unlock()

	return totals
}

func (f *FooterComponent) footerBuildRightStats(
	session *core.AgentSession,
	footerData core.ReadonlyFooterDataProvider,
	activeModel *ai.Model,
	routed *core.RoutedModelResult,
	th *theme.Theme,
) string {
	modelLabel := "no-model"
	displayModel := activeModel
	if activeModel != nil {
		modelLabel = activeModel.Id
	}
	if routed != nil && routed.Model != nil && routed.Model.Id != "" {
		displayModel = routed.Model
		if activeModel != nil && routed.Model.Id != activeModel.Id {
			modelLabel = fmt.Sprintf("%s → %s", activeModel.Id, routed.Model.Id)
		} else {
			modelLabel = routed.Model.Id
		}
	}

	var rightRaw string
	if displayModel != nil && displayModel.Reasoning {
		tl := string(session.ThinkingLevel())
		if routed != nil && routed.ThinkingLevel != nil {
			tl = string(*routed.ThinkingLevel)
		}
		if tl == "" || tl == "off" {
			rightRaw = fmt.Sprintf("%s • thinking off", modelLabel)
		} else {
			rightRaw = fmt.Sprintf("%s • %s", modelLabel, tl)
		}
	} else {
		rightRaw = modelLabel
	}

	providerCount := 0
	if footerData != nil {
		providerCount = footerData.GetAvailableProviderCount()
	}
	if providerCount > 1 && displayModel != nil && displayModel.Provider != "" {
		rightRaw = fmt.Sprintf("(%s) %s", displayModel.Provider, rightRaw)
	}

	statsRight := th.Fg(theme.FgDim, rightRaw)
	if core.AreExperimentalFeaturesEnabled() {
		statsRight += " " + th.Fg(theme.FgDim, "•") + " " + th.Bold(th.Fg(theme.FgWarning, "xp"))
	}
	return statsRight
}

func (f *FooterComponent) footerLayoutStatsLine(statsLeft, statsRight string, width int) []string {
	leftW := tui.VisibleWidth(statsLeft)
	rightW := tui.VisibleWidth(statsRight)
	const minPadding = 2

	if leftW == 0 {
		if rightW > width {
			return []string{tui.TruncateToWidth(statsRight, width, "...", false)}
		}
		return []string{strings.Repeat(" ", width-rightW) + statsRight}
	}
	if rightW == 0 {
		if leftW > width {
			return []string{tui.TruncateToWidth(statsLeft, width, "...", false)}
		}
		return []string{statsLeft}
	}

	if leftW+minPadding+rightW <= width {
		pad := width - leftW - rightW
		return []string{statsLeft + strings.Repeat(" ", pad) + statsRight}
	}

	availForRight := width - leftW - minPadding
	if availForRight > 4 {
		truncatedRight := tui.TruncateToWidth(statsRight, availForRight, "...", false)
		rightTruncW := tui.VisibleWidth(truncatedRight)
		pad := max(minPadding, width-leftW-rightTruncW)
		return []string{tui.TruncateToWidth(statsLeft+strings.Repeat(" ", pad)+truncatedRight, width, "", false)}
	}

	// Not enough horizontal room for both on one line; wrap cleanly within width.
	lineLeft := statsLeft
	if leftW > width {
		lineLeft = tui.TruncateToWidth(statsLeft, width, "...", false)
	}
	lineRight := statsRight
	if rightW > width {
		lineRight = tui.TruncateToWidth(statsRight, width, "...", false)
	} else {
		lineRight = strings.Repeat(" ", width-rightW) + statsRight
	}
	return []string{lineLeft, lineRight}
}

func footerSanitizeStatus(status string) string {
	cleaned := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(status)
	return strings.TrimSpace(cleaned)
}
