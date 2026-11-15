// Ported from packages/coding-agent/src/modes/interactive/components/oauth-selector.ts (pi v1.5.2).

package components

import (
	"fmt"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/tui"
)

const oaselMaxVisible = 8

var oaselEnvVarsRegex = jsre.MustCompile("^[A-Z][A-Z0-9_]*(?:, [A-Z][A-Z0-9_]*)*$", "")

// AuthSelectorProvider describes one provider entry shown in OAuthSelectorComponent.
type AuthSelectorProvider struct {
	Id           string
	Name         string
	AuthType     ai.AuthType
	OAuth        *ai.OAuthAuth
	ApiKey       *ai.ApiKeyAuth
	Status       *ai.AuthCheck
	Subscription *bool
}

// FormatAuthSelectorProviderType formats an auth type as "API key", "account", or "subscription".
func FormatAuthSelectorProviderType(authType ai.AuthType, subscription ...bool) string {
	if authType == ai.AuthTypeApiKey {
		return "API key"
	}
	if len(subscription) > 0 && !subscription[0] {
		return "account"
	}
	return "subscription"
}

func oaselFormatTypeWithOpt(authType ai.AuthType, subscription *bool) string {
	if subscription != nil {
		return FormatAuthSelectorProviderType(authType, *subscription)
	}
	return FormatAuthSelectorProviderType(authType)
}

// FormatAuthSelectorProviderStatus renders the themed status suffix for a login provider option.
func FormatAuthSelectorProviderStatus(provider AuthSelectorProvider) string {
	th := theme.CurrentTheme()
	if provider.Status == nil {
		return th.Fg(theme.FgMuted, " • not configured")
	}
	if provider.Status.Type != provider.AuthType {
		label := oaselFormatTypeWithOpt(provider.Status.Type, provider.Subscription) + " configured"
		return th.Fg(theme.FgMuted, " • ") + th.Fg(theme.FgWarning, label)
	}
	if provider.Status.Source == nil ||
		*provider.Status.Source == "" ||
		*provider.Status.Source == "OAuth" ||
		*provider.Status.Source == "stored credential" {
		return th.Fg(theme.FgSuccess, " ✓ configured")
	}
	src := *provider.Status.Source
	if oaselEnvVarsRegex.Test(src) {
		src = "env: " + src
	}
	return th.Fg(theme.FgSuccess, " ✓ "+src)
}

// OAuthSelectorComponent renders a searchable login/logout provider selector with optional brand animation.
type OAuthSelectorComponent struct {
	*tui.Container
	mu                 sync.Mutex
	searchInput        *tui.Input
	listContainer      *tui.Container
	allProviders       []AuthSelectorProvider
	filteredProviders  []AuthSelectorProvider
	selectedIndex      int
	mode               string
	onSelectCallback   func(providerId string, authType ai.AuthType)
	onCancelCallback   func()
	showAuthTypeLabels bool
	animationStart     time.Time
	stopAnimation      func()
	focused            bool
}

var _ tui.Focusable = (*OAuthSelectorComponent)(nil)

// NewOAuthSelectorComponent constructs an OAuth/API-key provider selector for /login or /logout.
func NewOAuthSelectorComponent(
	mode string,
	providers []AuthSelectorProvider,
	onSelect func(providerId string, authType ai.AuthType),
	onCancel func(),
	initialSearchInput *string,
	ui ...tui.TUI,
) *OAuthSelectorComponent {
	copied := make([]AuthSelectorProvider, len(providers))
	copy(copied, providers)

	distinctAuthTypes := make(map[ai.AuthType]struct{}, 2)
	for _, p := range copied {
		distinctAuthTypes[p.AuthType] = struct{}{}
	}

	th := theme.CurrentTheme()
	c := &OAuthSelectorComponent{
		Container:          tui.NewContainer(),
		listContainer:      tui.NewContainer(),
		allProviders:       copied,
		filteredProviders:  copied,
		mode:               mode,
		onSelectCallback:   onSelect,
		onCancelCallback:   onCancel,
		showAuthTypeLabels: len(distinctAuthTypes) > 1,
		animationStart:     time.Now(),
	}

	c.AddChild(NewDynamicBorder())
	c.AddChild(tui.NewSpacer(1))

	title := "Select provider to logout:"
	if mode == "login" {
		title = "Select provider to configure:"
	}
	c.AddChild(tui.NewTruncatedText(th.Fg(theme.FgAccent, th.Bold(title)), 1, 0))
	c.AddChild(tui.NewSpacer(1))

	c.searchInput = tui.NewInput(nil)
	if initialSearchInput != nil && *initialSearchInput != "" {
		c.searchInput.SetValue(*initialSearchInput)
	}
	c.searchInput.OnSubmit = func(string) {
		c.mu.Lock()
		var selected *AuthSelectorProvider
		if c.selectedIndex >= 0 && c.selectedIndex < len(c.filteredProviders) {
			p := c.filteredProviders[c.selectedIndex]
			selected = &p
		}
		cb := c.onSelectCallback
		c.mu.Unlock()
		if selected != nil && cb != nil {
			cb(selected.Id, selected.AuthType)
		}
	}
	c.AddChild(c.searchInput)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(c.listContainer)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder())

	query := ""
	if initialSearchInput != nil {
		query = *initialSearchInput
	}
	c.mu.Lock()
	c.filterProvidersLocked(query)
	c.mu.Unlock()

	if len(ui) > 0 && ui[0] != nil {
		stop := StartBrandAnimation(ui[0], func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.getSelectedBrandStyleLocked() != nil
		})
		c.mu.Lock()
		c.stopAnimation = stop
		c.mu.Unlock()
	}

	return c
}

// CapturesViewportInput reports that this selector intercepts viewport scroll keys.
func (o *OAuthSelectorComponent) CapturesViewportInput() bool {
	return true
}

// Focused reports whether the search input holds focus.
func (o *OAuthSelectorComponent) Focused() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.focused
}

// SetFocused propagates focus state to the embedded search input.
func (o *OAuthSelectorComponent) SetFocused(focused bool) {
	o.mu.Lock()
	o.focused = focused
	in := o.searchInput
	o.mu.Unlock()
	in.SetFocused(focused)
}

// Invalidate drops cached rendering state.
func (o *OAuthSelectorComponent) Invalidate() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Container.Invalidate()
}

// Render rebuilds animated brand rows if needed and renders the provider selector.
func (o *OAuthSelectorComponent) Render(width int) []string {
	o.mu.Lock()
	if o.stopAnimation != nil && o.getSelectedBrandStyleLocked() != nil {
		o.updateListLocked()
	}
	lines := o.Container.Render(width)
	o.mu.Unlock()
	return lines
}

// Dispose stops any active provider brand animation timer.
func (o *OAuthSelectorComponent) Dispose() {
	o.mu.Lock()
	stop := o.stopAnimation
	o.stopAnimation = nil
	o.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func (o *OAuthSelectorComponent) getSelectedBrandStyleLocked() ProviderBrandStyle {
	if o.selectedIndex < 0 || o.selectedIndex >= len(o.filteredProviders) {
		return nil
	}
	return GetProviderBrandStyle(o.filteredProviders[o.selectedIndex].Id)
}

func (o *OAuthSelectorComponent) filterProvidersLocked(query string) {
	if query != "" {
		o.filteredProviders = tui.FuzzyFilter(o.allProviders, query, func(p AuthSelectorProvider) string {
			methodName := ""
			if p.AuthType == ai.AuthTypeOAuth && p.OAuth != nil {
				methodName = p.OAuth.Name
			} else if p.AuthType == ai.AuthTypeApiKey && p.ApiKey != nil {
				methodName = p.ApiKey.Name
			} else if p.OAuth != nil {
				methodName = p.OAuth.Name
			} else if p.ApiKey != nil {
				methodName = p.ApiKey.Name
			}
			return p.Name + " " + p.Id + " " + string(p.AuthType) + " " + methodName
		})
	} else {
		o.filteredProviders = o.allProviders
	}
	o.selectedIndex = max(0, min(o.selectedIndex, max(0, len(o.filteredProviders)-1)))
	o.updateListLocked()
}

func (o *OAuthSelectorComponent) updateListLocked() {
	th := theme.CurrentTheme()
	o.listContainer.Clear()

	startIndex := max(0, min(o.selectedIndex-oaselMaxVisible/2, len(o.filteredProviders)-oaselMaxVisible))
	endIndex := min(startIndex+oaselMaxVisible, len(o.filteredProviders))

	for i := startIndex; i < endIndex; i++ {
		provider := o.filteredProviders[i]
		isSelected := i == o.selectedIndex

		statusIndicator := FormatAuthSelectorProviderStatus(provider)
		// U+2726 is single-width in every terminal, unlike star emoji or ambiguous-width symbols such as U+2605.
		partnerMarker := ""
		if core.IsPreferredProvider(provider.Id) {
			partnerMarker = th.Fg(theme.FgMuted, " ✦")
		}
		authTypeLabel := ""
		if o.showAuthTypeLabels {
			authTypeLabel = th.Fg(theme.FgMuted, fmt.Sprintf(" [%s]", oaselFormatTypeWithOpt(provider.AuthType, provider.Subscription)))
		}

		var line string
		if isSelected {
			prefix := th.Fg(theme.FgAccent, "→ ")
			var brandStyle ProviderBrandStyle
			if o.stopAnimation != nil {
				brandStyle = GetProviderBrandStyle(provider.Id)
			}
			var text string
			if brandStyle != nil {
				elapsedMs := float64(time.Since(o.animationStart).Nanoseconds()) / 1e6
				text = brandStyle(provider.Name, elapsedMs)
			} else {
				text = th.Fg(theme.FgAccent, provider.Name)
			}
			line = prefix + text + partnerMarker + authTypeLabel + statusIndicator
		} else {
			text := "  " + th.Fg(theme.FgText, provider.Name)
			line = text + partnerMarker + authTypeLabel + statusIndicator
		}
		o.listContainer.AddChild(tui.NewTruncatedText(line, 1, 0))
	}

	if startIndex > 0 || endIndex < len(o.filteredProviders) {
		scrollInfo := th.Fg(theme.FgMuted, fmt.Sprintf("  (%d/%d)", o.selectedIndex+1, len(o.filteredProviders)))
		o.listContainer.AddChild(tui.NewTruncatedText(scrollInfo, 1, 0))
	}

	if len(o.filteredProviders) == 0 {
		message := "No matching providers"
		if len(o.allProviders) == 0 {
			if o.mode == "login" {
				message = "No providers available"
			} else {
				message = "No providers logged in. Use /login first."
			}
		}
		o.listContainer.AddChild(tui.NewTruncatedText(th.Fg(theme.FgMuted, "  "+message), 1, 0))
	}
}

// HandleInput processes provider list navigation, confirm, cancel, and search filtering.
func (o *OAuthSelectorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()

	o.mu.Lock()
	pageIndex := tui.GetSelectPageIndex(kb, keyData, o.selectedIndex, len(o.filteredProviders), oaselMaxVisible)
	switch {
	case kb.Matches(keyData, tui.KeybindingTuiSelectUp):
		if len(o.filteredProviders) == 0 {
			o.mu.Unlock()
			return
		}
		o.selectedIndex = max(0, o.selectedIndex-1)
		o.updateListLocked()
		o.mu.Unlock()
		return
	case kb.Matches(keyData, tui.KeybindingTuiSelectDown):
		if len(o.filteredProviders) == 0 {
			o.mu.Unlock()
			return
		}
		o.selectedIndex = min(len(o.filteredProviders)-1, o.selectedIndex+1)
		o.updateListLocked()
		o.mu.Unlock()
		return
	case pageIndex != nil:
		if len(o.filteredProviders) == 0 {
			o.mu.Unlock()
			return
		}
		o.selectedIndex = *pageIndex
		o.updateListLocked()
		o.mu.Unlock()
		return
	case kb.Matches(keyData, tui.KeybindingTuiSelectConfirm):
		var selected *AuthSelectorProvider
		if o.selectedIndex >= 0 && o.selectedIndex < len(o.filteredProviders) {
			p := o.filteredProviders[o.selectedIndex]
			selected = &p
		}
		cb := o.onSelectCallback
		o.mu.Unlock()
		if selected != nil && cb != nil {
			cb(selected.Id, selected.AuthType)
		}
		return
	case kb.Matches(keyData, tui.KeybindingTuiSelectCancel):
		cb := o.onCancelCallback
		o.mu.Unlock()
		if cb != nil {
			cb()
		}
		return
	}
	in := o.searchInput
	o.mu.Unlock()

	in.HandleInput(keyData)
	val := in.GetValue()
	o.mu.Lock()
	o.filterProvidersLocked(val)
	o.mu.Unlock()
}
