// Ported from packages/coding-agent/src/modes/interactive/components/settings-selector.ts (pi v1.5.2).

package components

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/tui"
)

func sseIntPtr(v int) *int       { return &v }
func sseStrPtr(v string) *string { return &v }
func sseBoolPtr(v bool) *bool    { return &v }

var sseModelPickerLayout = &tui.SelectListLayoutOptions{
	MinPrimaryColumnWidth: sseIntPtr(12),
	MaxPrimaryColumnWidth: sseIntPtr(46),
}

var sseThinkingDescriptions = map[ai.ModelThinkingLevel]string{
	ai.ModelThinkingLevelOff:     "No reasoning",
	ai.ModelThinkingLevelMinimal: "Very brief reasoning (~1k tokens)",
	ai.ModelThinkingLevelLow:     "Light reasoning (~2k tokens)",
	ai.ModelThinkingLevelMedium:  "Moderate reasoning (~8k tokens)",
	ai.ModelThinkingLevelHigh:    "Deep reasoning (~16k tokens)",
	ai.ModelThinkingLevelXhigh:   "Extra-high reasoning (~32k tokens)",
	ai.ModelThinkingLevelMax:     "Maximum reasoning",
}

type sseDefaultProjectTrustChoice struct {
	value core.DefaultProjectTrust
	label string
}

var sseDefaultProjectTrustChoices = []sseDefaultProjectTrustChoice{
	{value: core.DefaultProjectTrustAsk, label: "Ask"},
	{value: core.DefaultProjectTrustAlways, label: "Always trust"},
	{value: core.DefaultProjectTrustNever, label: "Never trust"},
}

func sseDefaultProjectTrustLabel(trust core.DefaultProjectTrust) string {
	for _, choice := range sseDefaultProjectTrustChoices {
		if choice.value == trust {
			return choice.label
		}
	}
	return ""
}

func sseDefaultProjectTrustValues() []string {
	out := make([]string, 0, len(sseDefaultProjectTrustChoices))
	for _, choice := range sseDefaultProjectTrustChoices {
		out = append(out, choice.label)
	}
	return out
}

func sseDefaultProjectTrustByLabel(label string) (core.DefaultProjectTrust, bool) {
	for _, choice := range sseDefaultProjectTrustChoices {
		if choice.label == label {
			return choice.value, true
		}
	}
	return "", false
}

const (
	sseClearOverrideValue  = "__clear__"
	sseAutomaticThemeValue = "/"
)

// SettingsConfig supplies initial values and available choices for SettingsSelectorComponent.
type SettingsConfig struct {
	AutoCompact                bool
	DefaultModel               string
	CurrentModel               *ai.Model
	AvailableDefaultModels     []*ai.Model
	ShowImages                 bool
	ImageWidthCells            int
	AutoResizeImages           bool
	BlockImages                bool
	EnableSkillCommands        bool
	SteeringMode               agent.QueueMode
	FollowUpMode               agent.QueueMode
	Transport                  core.TransportSetting
	SessionShareDestination    core.SessionShareDestination
	HttpIdleTimeoutMs          int64
	CacheWarmingMode           core.CacheWarmingMode
	ThinkingLevel              agent.ThinkingLevel
	AvailableThinkingLevels    []agent.ThinkingLevel
	ModelThinkingLevels        map[string]agent.ThinkingLevel
	CurrentTheme               string
	TerminalTheme              tui.TerminalColorScheme
	AvailableThemes            []string
	HideThinkingBlock          bool
	MermaidRenderingMode       core.MermaidRenderingMode
	ShowCacheMissNotices       bool
	CollapseChangelog          bool
	EnableInstallTelemetry     bool
	DoubleEscapeAction         core.DoubleEscapeAction
	TreeFilterMode             core.TreeFilterMode
	ShowHardwareCursor         bool
	EditorPaddingX             int
	OutputPad                  int
	AutocompleteMaxVisible     int
	QuietStartup               core.QuietStartup
	DefaultProjectTrust        core.DefaultProjectTrust
	ClearOnShrink              bool
	ShowTerminalProgress       bool
	TuiMode                    tui.TuiMode
	FullscreenExitOutput       core.FullscreenExitOutput
	FullscreenScrollbar        tui.ScrollViewScrollbar
	FullscreenCopyOnSelect     bool
	FullscreenWheelScrollLines tui.WheelScrollLines
	Warnings                   core.WarningSettings
}

// SettingsCallbacks receives setting updates and cancellation notifications from SettingsSelectorComponent.
type SettingsCallbacks struct {
	OnAutoCompactChange                func(enabled bool)
	OnShowImagesChange                 func(enabled bool)
	OnImageWidthCellsChange            func(width int)
	OnAutoResizeImagesChange           func(enabled bool)
	OnBlockImagesChange                func(blocked bool)
	OnEnableSkillCommandsChange        func(enabled bool)
	OnSteeringModeChange               func(mode agent.QueueMode)
	OnFollowUpModeChange               func(mode agent.QueueMode)
	OnTransportChange                  func(transport core.TransportSetting)
	OnSessionShareDestinationChange    func(destination core.SessionShareDestination)
	OnHttpIdleTimeoutMsChange          func(timeoutMs int64)
	OnCacheWarmingModeChange           func(mode core.CacheWarmingMode)
	OnModelThinkingLevelChange         func(provider, modelId string, level agent.ThinkingLevel)
	OnModelThinkingLevelRemove         func(provider, modelId string)
	OnThemeChange                      func(theme string)
	OnThemePreview                     func(theme string)
	OnHideThinkingBlockChange          func(hidden bool)
	OnMermaidRenderingModeChange       func(mode core.MermaidRenderingMode)
	OnShowCacheMissNoticesChange       func(shown bool)
	OnCollapseChangelogChange          func(collapsed bool)
	OnEnableInstallTelemetryChange     func(enabled bool)
	OnDoubleEscapeActionChange         func(action core.DoubleEscapeAction)
	OnTreeFilterModeChange             func(mode core.TreeFilterMode)
	OnShowHardwareCursorChange         func(enabled bool)
	OnEditorPaddingXChange             func(padding int)
	OnOutputPadChange                  func(padding int)
	OnAutocompleteMaxVisibleChange     func(maxVisible int)
	OnQuietStartupChange               func(quiet core.QuietStartup)
	OnDefaultProjectTrustChange        func(defaultProjectTrust core.DefaultProjectTrust)
	OnClearOnShrinkChange              func(enabled bool)
	OnShowTerminalProgressChange       func(enabled bool)
	OnTuiModeChange                    func(mode tui.TuiMode)
	OnFullscreenExitOutputChange       func(output core.FullscreenExitOutput)
	OnFullscreenScrollbarChange        func(mode tui.ScrollViewScrollbar)
	OnFullscreenCopyOnSelectChange     func(enabled bool)
	OnFullscreenWheelScrollLinesChange func(lines tui.WheelScrollLines)
	OnWarningsChange                   func(warnings core.WarningSettings)
	OnCancel                           func()
}

type sseWarningSettingsSubmenu struct {
	*tui.Container
	settingsList *tui.SettingsList
	state        core.WarningSettings
}

func sseNewWarningSettingsSubmenu(
	warnings core.WarningSettings,
	onChange func(core.WarningSettings),
	onCancel func(),
) *sseWarningSettingsSubmenu {
	state := core.WarningSettings{}
	if warnings.AnthropicExtraUsage != nil {
		v := *warnings.AnthropicExtraUsage
		state.AnthropicExtraUsage = &v
	}

	extraUsage := core.SettingsDefaults.Warnings.AnthropicExtraUsage
	if state.AnthropicExtraUsage != nil {
		extraUsage = *state.AnthropicExtraUsage
	}

	items := []tui.SettingItem{
		{
			Id:           "anthropic-extra-usage",
			Label:        "Anthropic extra usage",
			Description:  sseStrPtr("Warn when Anthropic subscription auth may use paid extra usage"),
			CurrentValue: strconv.FormatBool(extraUsage),
			Values:       []string{"true", "false"},
		},
	}

	w := &sseWarningSettingsSubmenu{
		Container: tui.NewContainer(),
		state:     state,
	}

	w.settingsList = tui.NewSettingsList(
		items,
		min(len(items), 10),
		theme.GetSettingsListTheme(),
		func(id, newValue string) {
			if id == "anthropic-extra-usage" {
				b := newValue == "true"
				w.state.AnthropicExtraUsage = &b
				if onChange != nil {
					cp := core.WarningSettings{}
					if w.state.AnthropicExtraUsage != nil {
						v := *w.state.AnthropicExtraUsage
						cp.AnthropicExtraUsage = &v
					}
					onChange(cp)
				}
			}
		},
		onCancel,
		nil,
	)

	w.AddChild(w.settingsList)
	return w
}

func (w *sseWarningSettingsSubmenu) HandleInput(data string) {
	w.settingsList.HandleInput(data)
}

func sseModelSettingKey(model *ai.Model) string {
	return string(model.Provider) + "/" + model.Id
}

func sseModelDisplayLabel(model *ai.Model) string {
	return model.Id + " [" + string(model.Provider) + "]"
}

func sseModelThinkingOverridesSummary(overrides map[string]agent.ThinkingLevel) string {
	count := len(overrides)
	if count == 0 {
		return "none"
	}
	return fmt.Sprintf("%d configured", count)
}

func sseModelItemLabel(model *ai.Model) string {
	return model.Id + " " + theme.CurrentTheme().Fg(theme.FgMuted, "["+string(model.Provider)+"]")
}

func sseThemeItems(availableThemes []string, currentTheme string) []tui.SelectItem {
	items := make([]tui.SelectItem, 0, len(availableThemes))
	for _, name := range availableThemes {
		prefix := "  "
		if name == currentTheme {
			prefix = "\u2713 "
		}
		var desc *string
		if name == theme.SystemThemeName {
			desc = sseStrPtr("Theme created from your terminal's colors")
		}
		items = append(items, tui.SelectItem{
			Value:       name,
			Label:       prefix + name,
			Description: desc,
		})
	}
	return items
}

func sseSingleModeThemeItems(availableThemes []string, currentTheme string) []tui.SelectItem {
	items := sseThemeItems(availableThemes, currentTheme)
	systemIndex := slices.IndexFunc(items, func(item tui.SelectItem) bool {
		return item.Value == theme.SystemThemeName
	})

	out := make([]tui.SelectItem, 0, len(items)+1)
	if systemIndex != -1 {
		out = append(out, items[systemIndex])
		items = append(items[:systemIndex], items[systemIndex+1:]...)
	}
	out = append(out, tui.SelectItem{
		Value:       sseAutomaticThemeValue,
		Label:       "  automatic",
		Description: sseStrPtr("Use separate themes for light and dark terminal appearance"),
	})
	out = append(out, items...)
	return out
}

func ssePreferredTheme(availableThemes []string, preferred *string, fallback string) string {
	if preferred != nil && slices.Contains(availableThemes, *preferred) {
		return *preferred
	}
	if slices.Contains(availableThemes, fallback) {
		return fallback
	}
	if len(availableThemes) > 0 {
		return availableThemes[0]
	}
	return fallback
}

func sseDefaultAutomaticThemes(currentThemeSetting string, availableThemes []string) (lightTheme, darkTheme string) {
	if autoTheme := theme.ParseAutoThemeSetting(currentThemeSetting); autoTheme != nil {
		return autoTheme.LightTheme, autoTheme.DarkTheme
	}
	var currentFixed *string
	if !strings.Contains(currentThemeSetting, "/") {
		currentFixed = &currentThemeSetting
	}
	name := ssePreferredTheme(availableThemes, currentFixed, theme.SystemThemeName)
	return name, name
}

type sseThemeSubmenu struct {
	*tui.Container
	inputComponent       tui.Component
	callbacks            SettingsCallbacks
	availableThemes      []string
	terminalTheme        tui.TerminalColorScheme
	onDone               func(selectedValue *string)
	originalThemeSetting string
	mode                 string // "single" | "automatic"
	singleTheme          string
	lightTheme           string
	darkTheme            string
}

func sseNewThemeSubmenu(
	currentThemeSetting string,
	terminalTheme tui.TerminalColorScheme,
	availableThemes []string,
	callbacks SettingsCallbacks,
	onDone func(selectedValue *string),
) *sseThemeSubmenu {
	autoTheme := theme.ParseAutoThemeSetting(currentThemeSetting)
	lightTheme, darkTheme := sseDefaultAutomaticThemes(currentThemeSetting, availableThemes)

	var fixedTheme *string
	if autoTheme == nil && !strings.Contains(currentThemeSetting, "/") && currentThemeSetting != "" {
		fixedTheme = &currentThemeSetting
	}

	s := &sseThemeSubmenu{
		Container:            tui.NewContainer(),
		callbacks:            callbacks,
		availableThemes:      slices.Clone(availableThemes),
		terminalTheme:        terminalTheme,
		onDone:               onDone,
		originalThemeSetting: currentThemeSetting,
		mode:                 "single",
		lightTheme:           lightTheme,
		darkTheme:            darkTheme,
	}
	if autoTheme != nil {
		s.mode = "automatic"
	}

	preferredSingle := fixedTheme
	if preferredSingle == nil && autoTheme != nil {
		active := s.getActiveAutomaticTheme()
		preferredSingle = &active
	}
	s.singleTheme = ssePreferredTheme(s.availableThemes, preferredSingle, theme.SystemThemeName)

	if s.mode == "automatic" {
		s.showAutomaticMenu()
	} else {
		s.showSingleMenu()
	}
	return s
}

func (s *sseThemeSubmenu) HandleInput(data string) {
	if handler, ok := s.inputComponent.(interface{ HandleInput(string) }); ok {
		handler.HandleInput(data)
	}
}

func (s *sseThemeSubmenu) setContent(renderComponent, inputComponent tui.Component) {
	s.Clear()
	s.AddChild(renderComponent)
	if inputComponent != nil {
		s.inputComponent = inputComponent
	} else {
		s.inputComponent = renderComponent
	}
}

func (s *sseThemeSubmenu) preview(themeSetting string) {
	if s.callbacks.OnThemePreview != nil {
		s.callbacks.OnThemePreview(themeSetting)
	}
}

func (s *sseThemeSubmenu) showSingleMenu() {
	s.mode = "single"
	menu := NewSelectSubmenu(
		"Theme",
		"Select a theme, or choose automatic to follow terminal appearance.",
		sseSingleModeThemeItems(s.availableThemes, s.singleTheme),
		s.singleTheme,
		func(value string) {
			if value == sseAutomaticThemeValue {
				s.mode = "automatic"
				s.preview(s.getThemeSetting())
				s.showAutomaticMenu()
				return
			}
			s.singleTheme = value
			s.apply(value)
		},
		func() { s.cancel() },
		func(value string) {
			if value == sseAutomaticThemeValue {
				s.preview(s.getAutomaticThemeSetting())
				return
			}
			s.preview(value)
		},
	)
	s.setContent(menu, menu)
}

func (s *sseThemeSubmenu) showAutomaticMenu() {
	s.mode = "automatic"
	t := theme.CurrentTheme()
	content := tui.NewContainer()
	content.AddChild(tui.NewText(t.Bold(t.Fg(theme.FgAccent, "Automatic Theme")), 0, 0, nil))
	content.AddChild(tui.NewSpacer(1))
	content.AddChild(tui.NewText(t.Fg(theme.FgMuted, "Choose themes for terminal light and dark appearance."), 0, 0, nil))
	content.AddChild(tui.NewText(t.Fg(theme.FgMuted, "Light/dark detection requires terminal support."), 0, 0, nil))
	content.AddChild(tui.NewSpacer(1))

	items := []tui.SettingItem{
		{
			Id:           "light-theme",
			Label:        "Light theme",
			Description:  sseStrPtr("Theme to use in automatic mode when the terminal is light"),
			CurrentValue: s.lightTheme,
			Submenu: func(currentValue string, done func(selectedValue *string, options *tui.SettingsListSubmenuOptions)) tui.Component {
				return s.createThemeSelect(
					"Light Theme",
					"Select the theme to use for light terminal appearance",
					currentValue,
					done,
					func(value string) {
						s.lightTheme = value
						s.preview(s.getThemeSetting())
						done(&value, nil)
					},
				)
			},
		},
		{
			Id:           "dark-theme",
			Label:        "Dark theme",
			Description:  sseStrPtr("Theme to use in automatic mode when the terminal is dark"),
			CurrentValue: s.darkTheme,
			Submenu: func(currentValue string, done func(selectedValue *string, options *tui.SettingsListSubmenuOptions)) tui.Component {
				return s.createThemeSelect(
					"Dark Theme",
					"Select the theme to use for dark terminal appearance",
					currentValue,
					done,
					func(value string) {
						s.darkTheme = value
						s.preview(s.getThemeSetting())
						done(&value, nil)
					},
				)
			},
		},
		{
			Id:           "apply",
			Label:        "Apply",
			Description:  sseStrPtr("Save and go back"),
			CurrentValue: "save and go back",
			Values:       []string{"save and go back"},
		},
		{
			Id:           "single-mode",
			Label:        "Change mode",
			Description:  sseStrPtr("Switch to one theme for light and dark"),
			CurrentValue: "switch to single theme",
			Values:       []string{"switch to single theme"},
		},
	}

	settingsList := tui.NewSettingsList(
		items,
		min(len(items), 10),
		theme.GetSettingsListTheme(),
		func(id, _ string) {
			switch id {
			case "single-mode":
				s.mode = "single"
				s.singleTheme = s.getActiveAutomaticTheme()
				s.preview(s.singleTheme)
				s.showSingleMenu()
			case "apply":
				s.apply(s.getAutomaticThemeSetting())
			}
		},
		func() { s.cancel() },
		nil,
	)
	content.AddChild(settingsList)
	s.setContent(content, settingsList)
}

func (s *sseThemeSubmenu) createThemeSelect(
	title string,
	description string,
	currentValue string,
	done func(selectedValue *string, options *tui.SettingsListSubmenuOptions),
	onSelect func(value string),
) *SelectSubmenu {
	return NewSelectSubmenu(
		title,
		description,
		sseThemeItems(s.availableThemes, currentValue),
		currentValue,
		onSelect,
		func() {
			s.preview(s.getThemeSetting())
			done(nil, nil)
		},
		func(value string) {
			s.preview(value)
		},
	)
}

func (s *sseThemeSubmenu) getThemeSetting() string {
	if s.mode == "automatic" {
		return s.getAutomaticThemeSetting()
	}
	return s.singleTheme
}

func (s *sseThemeSubmenu) getActiveAutomaticTheme() string {
	if s.terminalTheme == tui.TerminalColorSchemeLight {
		return s.lightTheme
	}
	return s.darkTheme
}

func (s *sseThemeSubmenu) getAutomaticThemeSetting() string {
	return s.lightTheme + "/" + s.darkTheme
}

func (s *sseThemeSubmenu) apply(themeSetting string) {
	if s.onDone != nil {
		s.onDone(&themeSetting)
	}
}

func (s *sseThemeSubmenu) cancel() {
	s.preview(s.originalThemeSetting)
	if s.onDone != nil {
		s.onDone(nil)
	}
}

func sseFormatQuietStartup(q core.QuietStartup) string {
	if q.Header {
		return "header"
	}
	return strconv.FormatBool(q.Silent)
}

func sseFormatWheelScrollLines(w tui.WheelScrollLines) string {
	if w.Auto || w.Lines <= 0 {
		return "auto"
	}
	return strconv.FormatFloat(w.Lines, 'f', -1, 64)
}

func sseWheelScrollValues(w tui.WheelScrollLines) []string {
	nums := []float64{1, 2, 3, 5, 10}
	if !w.Auto && w.Lines > 0 && !slices.Contains(nums, w.Lines) {
		nums = append(nums, w.Lines)
	}
	slices.Sort(nums)

	out := make([]string, 0, len(nums)+1)
	out = append(out, "auto")
	for _, n := range nums {
		out = append(out, strconv.FormatFloat(n, 'f', -1, 64))
	}
	return out
}

func sseHttpIdleTimeoutLabels() []string {
	out := make([]string, 0, len(core.HttpIdleTimeoutChoices))
	for _, c := range core.HttpIdleTimeoutChoices {
		out = append(out, c.Label)
	}
	return out
}

func sseCacheWarmingValues() []string {
	out := make([]string, 0, len(core.CacheWarmingModes))
	for _, m := range core.CacheWarmingModes {
		out = append(out, string(m))
	}
	return out
}

// SettingsSelectorComponent renders the interactive /settings menu with inline value cycling and submenus.
type SettingsSelectorComponent struct {
	*tui.Container
	settingsList *tui.SettingsList
}

// NewSettingsSelectorComponent constructs the bordered settings selector UI.
func NewSettingsSelectorComponent(config SettingsConfig, callbacks SettingsCallbacks) *SettingsSelectorComponent {
	supportsImages := tui.GetCapabilities().Images != ""
	followUpKey := theme.KeyDisplayText(core.KeybindingAppMessageFollowUp)
	cycleThinkingKey := theme.KeyDisplayText(core.KeybindingAppThinkingCycle)

	currentWarnings := core.WarningSettings{}
	if config.Warnings.AnthropicExtraUsage != nil {
		v := *config.Warnings.AnthropicExtraUsage
		currentWarnings.AnthropicExtraUsage = &v
	}

	currentModelThinkingLevels := make(map[string]agent.ThinkingLevel, len(config.ModelThinkingLevels))
	maps.Copy(currentModelThinkingLevels, config.ModelThinkingLevels)

	defaultModelByValue := make(map[string]*ai.Model, len(config.AvailableDefaultModels))
	for _, model := range config.AvailableDefaultModels {
		if model == nil {
			continue
		}
		defaultModelByValue[sseModelSettingKey(model)] = model
	}

	var currentDefaultModelKey string
	if _, ok := defaultModelByValue[config.DefaultModel]; ok {
		currentDefaultModelKey = config.DefaultModel
	}
	var currentModelKey string
	if config.CurrentModel != nil {
		currentModelKey = sseModelSettingKey(config.CurrentModel)
	}

	items := []tui.SettingItem{
		{
			Id:           "autocompact",
			Label:        "Auto-compact",
			Description:  sseStrPtr("Automatically compact context when it gets too large"),
			CurrentValue: strconv.FormatBool(config.AutoCompact),
			Values:       []string{"true", "false"},
		},
	}

	if supportsImages {
		items = append(items,
			tui.SettingItem{
				Id:           "show-images",
				Label:        "Show images",
				Description:  sseStrPtr("Render images inline in terminal"),
				CurrentValue: strconv.FormatBool(config.ShowImages),
				Values:       []string{"true", "false"},
			},
			tui.SettingItem{
				Id:           "image-width-cells",
				Label:        "Image width",
				Description:  sseStrPtr("Preferred inline image width in terminal cells"),
				CurrentValue: strconv.Itoa(config.ImageWidthCells),
				Values:       []string{"60", "80", "120"},
			},
		)
	}

	items = append(items,
		tui.SettingItem{
			Id:           "auto-resize-images",
			Label:        "Auto-resize images",
			Description:  sseStrPtr("Resize large images to 2000x2000 max for better model compatibility"),
			CurrentValue: strconv.FormatBool(config.AutoResizeImages),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "block-images",
			Label:        "Block images",
			Description:  sseStrPtr("Prevent images from being sent to LLM providers"),
			CurrentValue: strconv.FormatBool(config.BlockImages),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "skill-commands",
			Label:        "Skill commands",
			Description:  sseStrPtr("Register skills as /skill:name commands"),
			CurrentValue: strconv.FormatBool(config.EnableSkillCommands),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "show-hardware-cursor",
			Label:        "Show hardware cursor",
			Description:  sseStrPtr("Use the terminal cursor instead of Pi's drawn cursor"),
			CurrentValue: strconv.FormatBool(config.ShowHardwareCursor),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "editor-padding",
			Label:        "Editor padding",
			Description:  sseStrPtr("Horizontal padding for input editor (0-3)"),
			CurrentValue: strconv.Itoa(config.EditorPaddingX),
			Values:       []string{"0", "1", "2", "3"},
		},
		tui.SettingItem{
			Id:           "output-padding",
			Label:        "Output padding",
			Description:  sseStrPtr("Horizontal padding for messages, tool output, and command output"),
			CurrentValue: strconv.Itoa(config.OutputPad),
			Values:       []string{"0", "1"},
		},
		tui.SettingItem{
			Id:           "autocomplete-max-visible",
			Label:        "Autocomplete max items",
			Description:  sseStrPtr("Max visible items in autocomplete dropdown (3-20)"),
			CurrentValue: strconv.Itoa(config.AutocompleteMaxVisible),
			Values:       []string{"3", "5", "7", "10", "15", "20"},
		},
		tui.SettingItem{
			Id:           "clear-on-shrink",
			Label:        "Clear on shrink",
			Description:  sseStrPtr("Clear empty rows when content shrinks (may cause flicker)"),
			CurrentValue: strconv.FormatBool(config.ClearOnShrink),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "terminal-progress",
			Label:        "Terminal progress",
			Description:  sseStrPtr("Show OSC 9;4 progress indicators in the terminal tab bar"),
			CurrentValue: strconv.FormatBool(config.ShowTerminalProgress),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "steering-mode",
			Label:        "Steering mode",
			Description:  sseStrPtr("Enter while streaming queues steering messages. 'one-at-a-time': deliver one, wait for response. 'all': deliver all at once."),
			CurrentValue: string(config.SteeringMode),
			Values:       []string{"one-at-a-time", "all"},
		},
		tui.SettingItem{
			Id:           "follow-up-mode",
			Label:        "Follow-up mode",
			Description:  sseStrPtr(followUpKey + " queues follow-up messages until agent stops. 'one-at-a-time': deliver one, wait for response. 'all': deliver all at once."),
			CurrentValue: string(config.FollowUpMode),
			Values:       []string{"one-at-a-time", "all"},
		},
		tui.SettingItem{
			Id:           "transport",
			Label:        "Transport",
			Description:  sseStrPtr("Preferred transport for providers that support multiple transports"),
			CurrentValue: string(config.Transport),
			Values:       []string{"sse", "websocket", "websocket-cached", "auto"},
		},
		tui.SettingItem{
			Id:           "share-destination",
			Label:        "Session share destination",
			Description:  sseStrPtr("Where to upload sessions with /share (auto prefers Radius when authenticated, otherwise GitHub Gist)"),
			CurrentValue: string(config.SessionShareDestination),
			Values:       []string{"auto", "gist", "radius"},
		},
		tui.SettingItem{
			Id:           "http-idle-timeout",
			Label:        "HTTP idle timeout",
			Description:  sseStrPtr("Maximum idle gap while waiting for HTTP headers or body chunks. Disable for local models that pause longer than five minutes."),
			CurrentValue: core.FormatHttpIdleTimeoutMs(config.HttpIdleTimeoutMs),
			Values:       sseHttpIdleTimeoutLabels(),
		},
		tui.SettingItem{
			Id:           "cache-warming-mode",
			Label:        "Cache warming",
			Description:  sseStrPtr("off; streaming while the agent runs; idle also between runs while continuation stays profitable"),
			CurrentValue: string(config.CacheWarmingMode),
			Values:       sseCacheWarmingValues(),
		},
		tui.SettingItem{
			Id:           "hide-thinking",
			Label:        "Hide thinking",
			Description:  sseStrPtr("Hide thinking blocks in assistant responses"),
			CurrentValue: strconv.FormatBool(config.HideThinkingBlock),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "mermaid-rendering",
			Label:        "Mermaid diagrams",
			Description:  sseStrPtr("Render Mermaid code blocks as Unicode diagrams"),
			CurrentValue: string(config.MermaidRenderingMode),
			Values:       []string{"off", "final", "streaming"},
		},
		tui.SettingItem{
			Id:           "cache-miss-notices",
			Label:        "Cache miss notices",
			Description:  sseStrPtr("Show transcript notices for cache costs and provider recovery diagnostics"),
			CurrentValue: strconv.FormatBool(config.ShowCacheMissNotices),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "collapse-changelog",
			Label:        "Collapse changelog",
			Description:  sseStrPtr("Show condensed changelog after updates"),
			CurrentValue: strconv.FormatBool(config.CollapseChangelog),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "quiet-startup",
			Label:        "Quiet startup",
			Description:  sseStrPtr("Disable verbose printing at startup (header: keep only the startup header)"),
			CurrentValue: sseFormatQuietStartup(config.QuietStartup),
			Values:       []string{"true", "header", "false"},
		},
		tui.SettingItem{
			Id:           "install-telemetry",
			Label:        "Install telemetry",
			Description:  sseStrPtr("Send an anonymous version/update ping after changelog-detected updates"),
			CurrentValue: strconv.FormatBool(config.EnableInstallTelemetry),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "default-project-trust",
			Label:        "Default project trust",
			Description:  sseStrPtr("Fallback behavior when no extension or saved trust decision decides project trust"),
			CurrentValue: sseDefaultProjectTrustLabel(config.DefaultProjectTrust),
			Values:       sseDefaultProjectTrustValues(),
		},
		tui.SettingItem{
			Id:           "double-escape-action",
			Label:        "Double-escape action",
			Description:  sseStrPtr("Action when pressing Escape twice with empty editor"),
			CurrentValue: string(config.DoubleEscapeAction),
			Values:       []string{"tree", "fork", "none"},
		},
		tui.SettingItem{
			Id:           "tree-filter-mode",
			Label:        "Tree filter mode",
			Description:  sseStrPtr("Default filter when opening /tree"),
			CurrentValue: string(config.TreeFilterMode),
			Values:       []string{"default", "no-tools", "user-only", "labeled-only", "all"},
		},
		tui.SettingItem{
			Id:           "warnings",
			Label:        "Warnings",
			Description:  sseStrPtr("Enable or disable individual warnings"),
			CurrentValue: "configure",
			Submenu: func(_ string, done func(selectedValue *string, options *tui.SettingsListSubmenuOptions)) tui.Component {
				return sseNewWarningSettingsSubmenu(
					currentWarnings,
					func(warnings core.WarningSettings) {
						currentWarnings = warnings
						if callbacks.OnWarningsChange != nil {
							callbacks.OnWarningsChange(warnings)
						}
					},
					func() {
						done(nil, nil)
					},
				)
			},
		},
		tui.SettingItem{
			Id:           "model-thinking",
			Label:        "Default thinking level per model",
			Description:  sseStrPtr("Override the default thinking level for specific models. " + cycleThinkingKey + " cycles in-session."),
			CurrentValue: sseModelThinkingOverridesSummary(currentModelThinkingLevels),
			Submenu: func(_ string, done func(selectedValue *string, options *tui.SettingsListSubmenuOptions)) tui.Component {
				steps := []SteppedSubmenuStep{
					{
						Key:         "model",
						Title:       "Per-Model Thinking Level",
						Description: "Select a model to configure",
						Options: func(map[string]string) []tui.SelectItem {
							sorted := make([]*ai.Model, 0, len(config.AvailableDefaultModels))
							for _, m := range config.AvailableDefaultModels {
								if m != nil {
									sorted = append(sorted, m)
								}
							}
							slices.SortStableFunc(sorted, func(a, b *ai.Model) int {
								aKey := sseModelSettingKey(a)
								bKey := sseModelSettingKey(b)
								if currentModelKey != "" {
									if aKey == currentModelKey && bKey != currentModelKey {
										return -1
									}
									if bKey == currentModelKey && aKey != currentModelKey {
										return 1
									}
								}
								if currentDefaultModelKey != "" {
									if aKey == currentDefaultModelKey && bKey != currentDefaultModelKey {
										return -1
									}
									if bKey == currentDefaultModelKey && aKey != currentDefaultModelKey {
										return 1
									}
								}
								return js.LocaleCompare(string(a.Provider), string(b.Provider))
							})
							modelItems := make([]tui.SelectItem, 0, len(sorted))
							for _, model := range sorted {
								key := sseModelSettingKey(model)
								var desc *string
								if override, ok := currentModelThinkingLevels[key]; ok {
									desc = sseStrPtr(string(override))
								}
								modelItems = append(modelItems, tui.SelectItem{
									Value:       key,
									Label:       sseModelItemLabel(model),
									Description: desc,
								})
							}
							if len(modelItems) == 0 {
								modelItems = append(modelItems, tui.SelectItem{
									Value:       "__none__",
									Label:       "No models available",
									Description: sseStrPtr("Log in to a provider or configure an API key first"),
								})
							}
							return modelItems
						},
						Preselect: func(map[string]string) *string {
							if currentModelKey != "" {
								return sseStrPtr(currentModelKey)
							}
							if currentDefaultModelKey != "" {
								return sseStrPtr(currentDefaultModelKey)
							}
							return nil
						},
						Searchable: true,
						Layout:     sseModelPickerLayout,
					},
					{
						Key: "level",
						TitleFn: func(ctx map[string]string) string {
							modelKey := ctx["model"]
							if m, ok := defaultModelByValue[modelKey]; ok {
								return "Thinking Level for " + sseModelDisplayLabel(m)
							}
							return "Thinking Level for " + modelKey
						},
						Description: "Select default thinking level for this model",
						Options: func(ctx map[string]string) []tui.SelectItem {
							modelKey := ctx["model"]
							model, ok := defaultModelByValue[modelKey]
							if !ok || model == nil {
								return nil
							}
							var levels []ai.ModelThinkingLevel
							if model.Reasoning {
								levels = ai.GetSupportedThinkingLevels(model)
							} else {
								levels = []ai.ModelThinkingLevel{ai.ModelThinkingLevelOff}
							}
							activeLevel, hasOverride := currentModelThinkingLevels[modelKey]
							levelItems := make([]tui.SelectItem, 0, len(levels)+1)
							for _, level := range levels {
								prefix := "  "
								if hasOverride && level == activeLevel {
									prefix = "\u2713 "
								}
								var desc *string
								if text, ok := sseThinkingDescriptions[level]; ok {
									desc = sseStrPtr(text)
								}
								levelItems = append(levelItems, tui.SelectItem{
									Value:       string(level),
									Label:       prefix + string(level),
									Description: desc,
								})
							}
							if hasOverride {
								levelItems = append(levelItems, tui.SelectItem{
									Value:       sseClearOverrideValue,
									Label:       "  (clear override)",
									Description: sseStrPtr("Revert to global default (" + string(config.ThinkingLevel) + ")"),
								})
							}
							return levelItems
						},
						Preselect: func(ctx map[string]string) *string {
							if level, ok := currentModelThinkingLevels[ctx["model"]]; ok {
								return sseStrPtr(string(level))
							}
							return nil
						},
					},
				}

				return NewSteppedSubmenu(
					steps,
					func(selections map[string]string) {
						modelKey := selections["model"]
						model, ok := defaultModelByValue[modelKey]
						if !ok || model == nil {
							return
						}
						if selections["level"] == sseClearOverrideValue {
							if callbacks.OnModelThinkingLevelRemove != nil {
								callbacks.OnModelThinkingLevelRemove(string(model.Provider), model.Id)
							}
							delete(currentModelThinkingLevels, modelKey)
						} else {
							level := agent.ThinkingLevel(selections["level"])
							if callbacks.OnModelThinkingLevelChange != nil {
								callbacks.OnModelThinkingLevelChange(string(model.Provider), model.Id, level)
							}
							currentModelThinkingLevels[modelKey] = level
						}
					},
					func() {
						done(sseStrPtr(sseModelThinkingOverridesSummary(currentModelThinkingLevels)), nil)
					},
					&SteppedSubmenuOptions{Loop: true},
				)
			},
		},
		tui.SettingItem{
			Id:           "tui-mode",
			Label:        "TUI mode",
			Description:  sseStrPtr("Interface layout; regular mode uses the terminal's normal scrollback"),
			CurrentValue: string(config.TuiMode),
			Values:       []string{"regular", "fullscreen"},
		},
		tui.SettingItem{
			Id:           "fullscreen-exit-output",
			Label:        "Fullscreen exit output",
			Description:  sseStrPtr("Print the transcript or only a session resume hint when exiting fullscreen mode"),
			CurrentValue: string(config.FullscreenExitOutput),
			Values:       []string{"transcript", "resume-hint"},
		},
		tui.SettingItem{
			Id:           "fullscreen-scrollbar",
			Label:        "Fullscreen scrollbar",
			Description:  sseStrPtr("Scrollbar behavior in fullscreen mode; has no effect in regular mode"),
			CurrentValue: string(config.FullscreenScrollbar),
			Values:       []string{"auto", "always", "hidden"},
		},
		tui.SettingItem{
			Id:           "fullscreen-copy-on-select",
			Label:        "Fullscreen copy on select",
			Description:  sseStrPtr("Automatically copy selected text in fullscreen mode; disable to copy selections with Ctrl+X"),
			CurrentValue: strconv.FormatBool(config.FullscreenCopyOnSelect),
			Values:       []string{"true", "false"},
		},
		tui.SettingItem{
			Id:           "fullscreen-wheel-scroll-lines",
			Label:        "Fullscreen wheel scrolling",
			Description:  sseStrPtr("Lines per mouse-wheel event in fullscreen mode; 'auto' speeds up fast wheel spins where the terminal does not"),
			CurrentValue: sseFormatWheelScrollLines(config.FullscreenWheelScrollLines),
			Values:       sseWheelScrollValues(config.FullscreenWheelScrollLines),
		},
		tui.SettingItem{
			Id:           "theme",
			Label:        "Theme",
			Description:  sseStrPtr("Color theme for the interface"),
			CurrentValue: config.CurrentTheme,
			Submenu: func(currentValue string, done func(selectedValue *string, options *tui.SettingsListSubmenuOptions)) tui.Component {
				return sseNewThemeSubmenu(
					currentValue,
					config.TerminalTheme,
					config.AvailableThemes,
					callbacks,
					func(selectedValue *string) {
						done(selectedValue, nil)
					},
				)
			},
		},
	)

	s := &SettingsSelectorComponent{
		Container: tui.NewContainer(),
	}

	s.AddChild(NewDynamicBorder())

	s.settingsList = tui.NewSettingsList(
		items,
		10,
		theme.GetSettingsListTheme(),
		func(id, newValue string) {
			switch id {
			case "autocompact":
				if callbacks.OnAutoCompactChange != nil {
					callbacks.OnAutoCompactChange(newValue == "true")
				}
			case "show-images":
				if callbacks.OnShowImagesChange != nil {
					callbacks.OnShowImagesChange(newValue == "true")
				}
			case "image-width-cells":
				if callbacks.OnImageWidthCellsChange != nil {
					if v, err := strconv.Atoi(newValue); err == nil {
						callbacks.OnImageWidthCellsChange(v)
					}
				}
			case "auto-resize-images":
				if callbacks.OnAutoResizeImagesChange != nil {
					callbacks.OnAutoResizeImagesChange(newValue == "true")
				}
			case "block-images":
				if callbacks.OnBlockImagesChange != nil {
					callbacks.OnBlockImagesChange(newValue == "true")
				}
			case "skill-commands":
				if callbacks.OnEnableSkillCommandsChange != nil {
					callbacks.OnEnableSkillCommandsChange(newValue == "true")
				}
			case "steering-mode":
				if callbacks.OnSteeringModeChange != nil {
					callbacks.OnSteeringModeChange(agent.QueueMode(newValue))
				}
			case "follow-up-mode":
				if callbacks.OnFollowUpModeChange != nil {
					callbacks.OnFollowUpModeChange(agent.QueueMode(newValue))
				}
			case "transport":
				if callbacks.OnTransportChange != nil {
					callbacks.OnTransportChange(core.TransportSetting(newValue))
				}
			case "share-destination":
				if callbacks.OnSessionShareDestinationChange != nil {
					callbacks.OnSessionShareDestinationChange(core.SessionShareDestination(newValue))
				}
			case "http-idle-timeout":
				if callbacks.OnHttpIdleTimeoutMsChange != nil {
					for _, choice := range core.HttpIdleTimeoutChoices {
						if choice.Label == newValue {
							callbacks.OnHttpIdleTimeoutMsChange(choice.TimeoutMs)
							break
						}
					}
				}
			case "cache-warming-mode":
				if callbacks.OnCacheWarmingModeChange != nil {
					callbacks.OnCacheWarmingModeChange(core.CacheWarmingMode(newValue))
				}
			case "hide-thinking":
				if callbacks.OnHideThinkingBlockChange != nil {
					callbacks.OnHideThinkingBlockChange(newValue == "true")
				}
			case "mermaid-rendering":
				if callbacks.OnMermaidRenderingModeChange != nil {
					callbacks.OnMermaidRenderingModeChange(core.MermaidRenderingMode(newValue))
				}
			case "cache-miss-notices":
				if callbacks.OnShowCacheMissNoticesChange != nil {
					callbacks.OnShowCacheMissNoticesChange(newValue == "true")
				}
			case "collapse-changelog":
				if callbacks.OnCollapseChangelogChange != nil {
					callbacks.OnCollapseChangelogChange(newValue == "true")
				}
			case "quiet-startup":
				if callbacks.OnQuietStartupChange != nil {
					switch newValue {
					case "header":
						callbacks.OnQuietStartupChange(core.QuietStartupHeader())
					case "true":
						callbacks.OnQuietStartupChange(core.QuietStartupBool(true))
					default:
						callbacks.OnQuietStartupChange(core.QuietStartupBool(false))
					}
				}
			case "install-telemetry":
				if callbacks.OnEnableInstallTelemetryChange != nil {
					callbacks.OnEnableInstallTelemetryChange(newValue == "true")
				}
			case "default-project-trust":
				if callbacks.OnDefaultProjectTrustChange != nil {
					if trust, ok := sseDefaultProjectTrustByLabel(newValue); ok {
						callbacks.OnDefaultProjectTrustChange(trust)
					}
				}
			case "double-escape-action":
				if callbacks.OnDoubleEscapeActionChange != nil {
					callbacks.OnDoubleEscapeActionChange(core.DoubleEscapeAction(newValue))
				}
			case "tree-filter-mode":
				if callbacks.OnTreeFilterModeChange != nil {
					callbacks.OnTreeFilterModeChange(core.TreeFilterMode(newValue))
				}
			case "show-hardware-cursor":
				if callbacks.OnShowHardwareCursorChange != nil {
					callbacks.OnShowHardwareCursorChange(newValue == "true")
				}
			case "editor-padding":
				if callbacks.OnEditorPaddingXChange != nil {
					if v, err := strconv.Atoi(newValue); err == nil {
						callbacks.OnEditorPaddingXChange(v)
					}
				}
			case "output-padding":
				if callbacks.OnOutputPadChange != nil {
					if newValue == "0" {
						callbacks.OnOutputPadChange(0)
					} else {
						callbacks.OnOutputPadChange(1)
					}
				}
			case "autocomplete-max-visible":
				if callbacks.OnAutocompleteMaxVisibleChange != nil {
					if v, err := strconv.Atoi(newValue); err == nil {
						callbacks.OnAutocompleteMaxVisibleChange(v)
					}
				}
			case "clear-on-shrink":
				if callbacks.OnClearOnShrinkChange != nil {
					callbacks.OnClearOnShrinkChange(newValue == "true")
				}
			case "terminal-progress":
				if callbacks.OnShowTerminalProgressChange != nil {
					callbacks.OnShowTerminalProgressChange(newValue == "true")
				}
			case "tui-mode":
				if callbacks.OnTuiModeChange != nil {
					callbacks.OnTuiModeChange(tui.TuiMode(newValue))
				}
			case "fullscreen-exit-output":
				if callbacks.OnFullscreenExitOutputChange != nil {
					callbacks.OnFullscreenExitOutputChange(core.FullscreenExitOutput(newValue))
				}
			case "fullscreen-scrollbar":
				if callbacks.OnFullscreenScrollbarChange != nil {
					callbacks.OnFullscreenScrollbarChange(tui.ScrollViewScrollbar(newValue))
				}
			case "fullscreen-copy-on-select":
				if callbacks.OnFullscreenCopyOnSelectChange != nil {
					callbacks.OnFullscreenCopyOnSelectChange(newValue == "true")
				}
			case "fullscreen-wheel-scroll-lines":
				if callbacks.OnFullscreenWheelScrollLinesChange != nil {
					if newValue == "auto" {
						callbacks.OnFullscreenWheelScrollLinesChange(tui.WheelScrollLines{Auto: true})
					} else if v, err := strconv.Atoi(newValue); err == nil {
						callbacks.OnFullscreenWheelScrollLinesChange(tui.WheelScrollLines{Lines: float64(v)})
					}
				}
			case "theme":
				if callbacks.OnThemeChange != nil {
					callbacks.OnThemeChange(newValue)
				}
			}
		},
		callbacks.OnCancel,
		&tui.SettingsListOptions{EnableSearch: sseBoolPtr(true)},
	)

	s.AddChild(s.settingsList)
	s.AddChild(NewDynamicBorder())

	return s
}

// GetSettingsList returns the underlying SettingsList for focus and input delegation.
func (s *SettingsSelectorComponent) GetSettingsList() *tui.SettingsList {
	return s.settingsList
}

// HandleInput delegates input to the underlying SettingsList.
func (s *SettingsSelectorComponent) HandleInput(data string) {
	s.settingsList.HandleInput(data)
}
