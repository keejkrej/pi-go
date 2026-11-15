package gui

import (
	"fmt"
	"maps"
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

// FooterState holds the complete snapshot rendered by FooterBar.
type FooterState struct {
	Cwd                string
	GitBranch          *string
	SessionName        string
	Provider           string
	ModelId            string
	ModelName          string
	RoutedProvider     string
	RoutedModelId      string
	ThinkingLevel      agent.ThinkingLevel
	UsingSubscription  bool
	AvailableProviders int
	InputTokens        int
	OutputTokens       int
	CacheReadTokens    int
	CacheWriteTokens   int
	TotalCost          float64
	ContextTokens      int
	HasContextTokens   bool
	ContextWindow      int
	ContextPercent     float64
	HasContextPercent  bool
	AutoCompactEnabled bool
	SteeringCount      int
	FollowUpCount      int
	IsStreaming        bool
	IsCompacting       bool
	IsRetrying         bool
	RetryAttempt       int
	WorkingMessage     string
	CacheWarmingLabel  string
	ExtensionStatuses  map[string]string
}

// FooterOption configures a FooterBar.
type FooterOption func(*FooterBar)

// WithFooterDataProvider binds a core.ReadonlyFooterDataProvider for git branch and
// extension status updates.
func WithFooterDataProvider(provider core.ReadonlyFooterDataProvider) FooterOption {
	return func(f *FooterBar) { f.initialProvider = provider }
}

// WithFooterTheme applies a custom *gui.GuiTheme override.
func WithFooterTheme(th *gui.GuiTheme) FooterOption {
	return func(f *FooterBar) { f.theme = th }
}

// WithFooterOnModelClick registers a handler invoked when the user clicks the model pill.
func WithFooterOnModelClick(fn func()) FooterOption {
	return func(f *FooterBar) { f.onModelClick = fn }
}

// WithFooterOnThinkingClick registers a handler invoked when the user clicks the thinking pill.
func WithFooterOnThinkingClick(fn func()) FooterOption {
	return func(f *FooterBar) { f.onThinkingClick = fn }
}

// WithFooterOnSessionClick registers a handler invoked when the user clicks the session/branch chip.
func WithFooterOnSessionClick(fn func()) FooterOption {
	return func(f *FooterBar) { f.onSessionClick = fn }
}

// FooterBar renders the desktop bottom status bar containing cwd, git branch,
// active working/retrying/compacting spinner, queue counts, context usage meter,
// session token/cost summary, thinking pill, and model pill.
type FooterBar struct {
	mu              sync.Mutex
	initialProvider core.ReadonlyFooterDataProvider
	provider        core.ReadonlyFooterDataProvider
	unsubBranch     func()
	state           FooterState
	theme           *gui.GuiTheme
	onModelClick    func()
	onThinkingClick func()
	onSessionClick  func()
}

// NewFooterBar creates a FooterBar with optional data provider, theme, and click handlers.
func NewFooterBar(opts ...FooterOption) *FooterBar {
	f := &FooterBar{}
	for _, opt := range opts {
		if opt != nil {
			opt(f)
		}
	}
	if f.initialProvider != nil {
		f.SetDataProvider(f.initialProvider)
	}
	return f
}

// SetDataProvider attaches provider (subscribing to branch changes) and detaches
// any previously subscribed provider.
func (f *FooterBar) SetDataProvider(provider core.ReadonlyFooterDataProvider) {
	f.mu.Lock()
	prevUnsub := f.unsubBranch
	f.unsubBranch = nil
	f.provider = provider
	f.mu.Unlock()

	if prevUnsub != nil {
		prevUnsub()
	}
	if provider == nil {
		return
	}

	unsub := provider.OnBranchChange(func() {
		f.refreshProviderState()
	})
	f.mu.Lock()
	if f.provider == provider {
		f.unsubBranch = unsub
	} else {
		f.mu.Unlock()
		if unsub != nil {
			unsub()
		}
		return
	}
	f.mu.Unlock()
	f.refreshProviderState()
}

func (f *FooterBar) refreshProviderState() {
	f.mu.Lock()
	provider := f.provider
	f.mu.Unlock()
	if provider == nil {
		return
	}

	branch := ftCloneStrPtr(provider.GetGitBranch())
	statuses := maps.Clone(provider.GetExtensionStatuses())
	avail := provider.GetAvailableProviderCount()

	f.mu.Lock()
	if f.provider == provider {
		f.state.GitBranch = branch
		f.state.ExtensionStatuses = statuses
		f.state.AvailableProviders = avail
	}
	f.mu.Unlock()
}

func ftCloneStrPtr(p *string) *string {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

func ftCloneFooterState(st FooterState) FooterState {
	cp := st
	cp.GitBranch = ftCloneStrPtr(st.GitBranch)
	cp.ExtensionStatuses = maps.Clone(st.ExtensionStatuses)
	return cp
}

// Dispose detaches any active branch-change subscription on the data provider.
func (f *FooterBar) Dispose() {
	f.mu.Lock()
	unsub := f.unsubBranch
	f.unsubBranch = nil
	f.provider = nil
	f.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}

// UpdateFromSession refreshes FooterBar state directly from a live *core.AgentSession.
func (f *FooterBar) UpdateFromSession(session *core.AgentSession) {
	if session == nil {
		return
	}
	f.refreshProviderState()

	f.mu.Lock()
	prevBranch := ftCloneStrPtr(f.state.GitBranch)
	prevStatuses := maps.Clone(f.state.ExtensionStatuses)
	prevAvail := f.state.AvailableProviders
	workingMsg := f.state.WorkingMessage
	provider := f.provider
	f.mu.Unlock()

	if provider != nil {
		prevBranch = ftCloneStrPtr(provider.GetGitBranch())
		prevStatuses = maps.Clone(provider.GetExtensionStatuses())
		prevAvail = provider.GetAvailableProviderCount()
	}

	var st FooterState
	st.Cwd = session.Cwd()
	st.GitBranch = prevBranch
	st.ExtensionStatuses = prevStatuses
	st.AvailableProviders = prevAvail
	st.WorkingMessage = workingMsg
	if name := session.SessionName(); name != nil {
		st.SessionName = *name
	}
	st.ThinkingLevel = session.ThinkingLevel()
	st.AutoCompactEnabled = session.AutoCompactionEnabled()
	st.SteeringCount = len(session.GetSteeringMessages())
	st.FollowUpCount = len(session.GetFollowUpMessages())
	st.IsStreaming = session.IsStreaming()
	st.IsCompacting = session.IsCompacting()
	st.IsRetrying = session.IsRetrying()
	st.RetryAttempt = session.RetryAttempt()

	if m := session.Model(); m != nil {
		st.Provider = string(m.Provider)
		st.ModelId = m.Id
		st.ModelName = m.Name
		st.ContextWindow = m.ContextWindow
		if reg := session.ModelRegistry(); reg != nil {
			st.UsingSubscription = reg.IsUsingOAuth(m)
		}
	}
	if routed := session.RoutedModel(); routed != nil && routed.Model != nil {
		st.RoutedProvider = string(routed.Model.Provider)
		st.RoutedModelId = routed.Model.Id
	}

	totals := core.CreateUsageTotals()
	for _, msg := range session.Messages() {
		if asst, ok := msg.(*ai.AssistantMessage); ok && asst != nil {
			core.AddUsageToTotals(&totals, asst.Usage)
		}
	}
	st.InputTokens = totals.Input
	st.OutputTokens = totals.Output
	st.CacheReadTokens = totals.CacheRead
	st.CacheWriteTokens = totals.CacheWrite
	st.TotalCost = totals.Cost

	if cu := session.GetContextUsage(); cu != nil {
		if cu.ContextWindow > 0 {
			st.ContextWindow = cu.ContextWindow
		}
		if tok, ok := cu.Tokens.Get(); ok {
			st.ContextTokens = tok
			st.HasContextTokens = true
		}
		if pct, ok := cu.Percent.Get(); ok {
			st.ContextPercent = pct
			st.HasContextPercent = true
		}
	}

	if cw := session.CacheWarmingStatus(); cw != nil && cw.State != "" && cw.State != "idle" && cw.State != "disabled" {
		st.CacheWarmingLabel = "cache:" + cw.State
	}

	f.mu.Lock()
	f.state = st
	f.mu.Unlock()
}

// SetState replaces the rendered FooterState snapshot.
func (f *FooterBar) SetState(state FooterState) {
	f.mu.Lock()
	f.state = ftCloneFooterState(state)
	f.mu.Unlock()
}

// State returns a snapshot copy of the current FooterState.
func (f *FooterBar) State() FooterState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ftCloneFooterState(f.state)
}

// SetWorkingMessage sets a custom message displayed alongside the active working spinner.
func (f *FooterBar) SetWorkingMessage(msg string) {
	f.mu.Lock()
	f.state.WorkingMessage = msg
	f.mu.Unlock()
}

// SetTheme overrides the GuiTheme used to style the footer.
func (f *FooterBar) SetTheme(th *gui.GuiTheme) {
	f.mu.Lock()
	f.theme = th
	f.mu.Unlock()
}

func ftShortModelName(st FooterState) string {
	id := st.ModelId
	if id == "" {
		id = st.ModelName
	}
	if id == "" {
		return "no-model"
	}
	if st.AvailableProviders > 1 && st.Provider != "" {
		return fmt.Sprintf("(%s) %s", st.Provider, id)
	}
	return id
}

// View builds the immediate-mode MyGo UI element tree for the footer bar.
func (f *FooterBar) View(c *ui.Context) ui.Element {
	f.mu.Lock()
	st := ftCloneFooterState(f.state)
	th := gui.ResolveGuiTheme(c, f.theme)
	onModel := f.onModelClick
	onThinking := f.onThinkingClick
	onSession := f.onSessionClick
	f.mu.Unlock()

	var modelClicked, thinkingClicked, sessionClicked bool

	bar := ui.Row(c).
		FillWidth().
		Background(th.Surface).
		BorderWidth(1, 0, 0, 0).
		BorderColor(th.BorderMuted).
		Padding(6, 12).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Gap(12)

	bar.Children(func() {
		// Left section: cwd + git branch + session name + extension statuses
		ui.Row(c).Shrink(1).Grow(1).AlignItems(ui.Center).Wrap().Gap(8).Children(func() {
			if st.Cwd != "" {
				ui.Text(c, utils.ShortenHomePath(st.Cwd)).
					TextColor(th.Muted).
					Font("monospace").
					FontSize(12).
					SingleLine()
			}
			if st.GitBranch != nil && *st.GitBranch != "" {
				ui.Text(c, "⎇ "+*st.GitBranch).
					TextColor(th.Accent).
					FontSize(12).
					SingleLine()
			}
			if st.SessionName != "" {
				chip := ui.Text(c, "#"+st.SessionName).TextColor(th.Text).FontSize(12).SingleLine()
				if onSession != nil {
					chip.Cursor(ui.CursorPointer)
					if chip.Clicked() {
						sessionClicked = true
					}
				}
			}
			if len(st.ExtensionStatuses) > 0 {
				keys := make([]string, 0, len(st.ExtensionStatuses))
				for k := range st.ExtensionStatuses {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					raw := strings.TrimSpace(utils.StripAnsi(st.ExtensionStatuses[k]))
					if raw == "" {
						continue
					}
					ui.Text(c, raw).TextColor(th.Dim).FontSize(11).SingleLine()
				}
			}
			if st.CacheWarmingLabel != "" {
				ui.Badge(c, st.CacheWarmingLabel)
			}
		})

		// Middle section: active activity spinner + steering/follow-up queue counts
		if st.IsCompacting || st.IsRetrying || st.IsStreaming || st.WorkingMessage != "" || st.SteeringCount > 0 || st.FollowUpCount > 0 {
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(8).Children(func() {
				switch {
				case st.IsCompacting:
					ui.Spinner(c).TextColor(th.Accent).Label("Compacting")
					ui.Text(c, "Compacting...").TextColor(th.Accent).FontSize(12)
				case st.IsRetrying:
					ui.Spinner(c).TextColor(th.Warning).Label("Retrying")
					ui.Textf(c, "Retrying (#%d)...", max(1, st.RetryAttempt)).TextColor(th.Warning).FontSize(12)
				case st.IsStreaming || st.WorkingMessage != "":
					lbl := st.WorkingMessage
					if lbl == "" {
						lbl = "Working..."
					}
					ui.Spinner(c).TextColor(th.Accent).Label(lbl)
					ui.Text(c, lbl).TextColor(th.Muted).FontSize(12)
				}
				if st.SteeringCount > 0 {
					ui.Badge(c, fmt.Sprintf("%d steering", st.SteeringCount))
				}
				if st.FollowUpCount > 0 {
					ui.Badge(c, fmt.Sprintf("%d follow-up", st.FollowUpCount))
				}
			})
		}

		// Right section: tokens/cost + context meter + thinking level pill + model pill
		ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(10).Children(func() {
			if st.InputTokens > 0 || st.OutputTokens > 0 || st.TotalCost > 0 || st.UsingSubscription {
				var parts []string
				if st.InputTokens > 0 {
					parts = append(parts, "↑"+FormatTokens(st.InputTokens))
				}
				if st.OutputTokens > 0 {
					parts = append(parts, "↓"+FormatTokens(st.OutputTokens))
				}
				if st.CacheReadTokens > 0 {
					parts = append(parts, "R"+FormatTokens(st.CacheReadTokens))
				}
				if st.CacheWriteTokens > 0 {
					parts = append(parts, "W"+FormatTokens(st.CacheWriteTokens))
				}
				if st.UsingSubscription {
					parts = append(parts, "$(sub)")
				} else if st.TotalCost > 0 {
					parts = append(parts, FormatCost(st.TotalCost))
				}
				if len(parts) > 0 {
					ui.Text(c, strings.Join(parts, " ")).TextColor(th.Dim).FontSize(11).SingleLine()
				}
			}

			// Context usage meter + percentage label
			if st.ContextWindow > 0 {
				var pct float64
				if st.HasContextPercent {
					pct = st.ContextPercent
				}
				ctxColor := th.Muted
				if st.HasContextPercent {
					switch {
					case pct > 90:
						ctxColor = th.Error
					case pct > 70:
						ctxColor = th.Warning
					}
				}
				var ctxText string
				if st.HasContextPercent {
					ctxText = fmt.Sprintf("%.1f%%/%s", pct, FormatTokens(st.ContextWindow))
				} else {
					ctxText = fmt.Sprintf("?/%s", FormatTokens(st.ContextWindow))
				}
				if st.AutoCompactEnabled {
					ctxText += " (auto)"
				}
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Meter(c, pct, 0, 100, &ui.MeterLevels{
						Warning:       70,
						Critical:      90,
						NormalColor:   th.Accent,
						WarningColor:  th.Warning,
						CriticalColor: th.Error,
					}).Width(64).Label("Context Usage")
					ui.Text(c, ctxText).TextColor(ctxColor).FontSize(11).SingleLine()
				})
			}

			// Thinking level pill
			if st.ThinkingLevel != "" {
				tColor := ThinkingLevelColor(th, st.ThinkingLevel)
				thinkPill := ui.Box(c).
					Background(th.SurfaceElevated).
					Border(1, tColor.Alpha(0.55)).
					Radius(th.Radius-2).
					Padding(2, 8).
					Children(func() {
						ui.Textf(c, "thinking: %s", st.ThinkingLevel).TextColor(tColor).FontSize(11).SingleLine()
					})
				if onThinking != nil {
					thinkPill.Cursor(ui.CursorPointer)
					if thinkPill.Clicked() {
						thinkingClicked = true
					}
				}
			}

			// Active model pill (plus optional routed sub-model)
			modelLbl := ftShortModelName(st)
			if st.RoutedModelId != "" && st.RoutedModelId != st.ModelId {
				modelLbl += " → " + st.RoutedModelId
			}
			modelPill := ui.Box(c).
				Background(th.SurfaceElevated).
				Border(1, th.BorderMuted).
				Radius(th.Radius-2).
				Padding(2, 8).
				Children(func() {
					ui.Text(c, modelLbl).TextColor(th.Text).FontSize(11).SingleLine()
				})
			if onModel != nil {
				modelPill.Cursor(ui.CursorPointer)
				if modelPill.Clicked() {
					modelClicked = true
				}
			}
		})
	})

	if modelClicked && onModel != nil {
		onModel()
	}
	if thinkingClicked && onThinking != nil {
		onThinking()
	}
	if sessionClicked && onSession != nil {
		onSession()
	}

	return bar
}
