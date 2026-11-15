// Ported from packages/coding-agent/src/core/settings-defaults.ts (pi v1.5.2).

package core

import (
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/tui"
)

// SettingsDefaultsValues mirrors the nested structure of SETTINGS_DEFAULTS.
type SettingsDefaultsValues struct {
	Transport                  TransportSetting
	SessionShareDestination    SessionShareDestination
	SteeringMode               agent.QueueMode
	FollowUpMode               agent.QueueMode
	Compaction                 SettingsCompactionDefaults
	BranchSummary              SettingsBranchSummaryDefaults
	Retry                      SettingsRetryDefaults
	HideThinkingBlock          bool
	ShowCacheMissNotices       bool
	QuietStartup               QuietStartup
	DefaultProjectTrust        DefaultProjectTrust
	CollapseChangelog          bool
	EnableInstallTelemetry     bool
	EnableAnalytics            bool
	EnableSkillCommands        bool
	Terminal                   SettingsTerminalDefaults
	Images                     SettingsImagesDefaults
	DoubleEscapeAction         DoubleEscapeAction
	TreeFilterMode             TreeFilterMode
	EditorPaddingX             int
	OutputPad                  int
	AutocompleteMaxVisible     int
	Markdown                   SettingsMarkdownDefaults
	Warnings                   SettingsWarningsDefaults
	Codemode                   SettingsCodemodeDefaults
	CacheWarming               CacheWarmingMode
	TuiMode                    TuiMode
	FullscreenExitOutput       FullscreenExitOutput
	FullscreenScrollbar        tui.ScrollViewScrollbar
	FullscreenCopyOnSelect     bool
	FullscreenWheelScrollLines tui.WheelScrollLines
}

// SettingsCompactionDefaults holds built-in compaction defaults.
type SettingsCompactionDefaults struct {
	Enabled          bool
	ReserveTokens    int
	KeepRecentTokens int
}

// SettingsBranchSummaryDefaults holds built-in branch summary defaults.
type SettingsBranchSummaryDefaults struct {
	ReserveTokens int
	SkipPrompt    bool
}

// SettingsRetryProviderDefaults holds built-in provider retry defaults.
type SettingsRetryProviderDefaults struct {
	MaxRetryDelayMs float64
}

// SettingsRetryDefaults holds built-in retry defaults.
type SettingsRetryDefaults struct {
	Enabled         bool
	MaxRetries      int
	BaseDelayMs     float64
	MaxAgentDelayMs float64
	Provider        SettingsRetryProviderDefaults
}

// SettingsTerminalDefaults holds built-in terminal rendering defaults.
type SettingsTerminalDefaults struct {
	ShowImages           bool
	ImageWidthCells      int
	ClearOnShrink        bool
	ShowTerminalProgress bool
}

// SettingsImagesDefaults holds built-in image preprocessing defaults.
type SettingsImagesDefaults struct {
	AutoResize  bool
	BlockImages bool
}

// SettingsMarkdownDefaults holds built-in markdown rendering defaults.
type SettingsMarkdownDefaults struct {
	CodeBlockIndent string
	Mermaid         MermaidRenderingMode
}

// SettingsWarningsDefaults holds built-in warning toggles.
type SettingsWarningsDefaults struct {
	AnthropicExtraUsage bool
}

// SettingsCodemodeDefaults holds built-in codemode defaults.
type SettingsCodemodeDefaults struct {
	Mode         CodemodeMode
	InlineBudget float64
}

// SettingsDefaults holds runtime defaults shared by settings accessors and the published JSON Schema.
var SettingsDefaults = SettingsDefaultsValues{
	Transport:               TransportAuto,
	SessionShareDestination: SessionShareDestinationAuto,
	SteeringMode:            agent.QueueModeOneAtATime,
	FollowUpMode:            agent.QueueModeOneAtATime,
	Compaction: SettingsCompactionDefaults{
		Enabled:          true,
		ReserveTokens:    16384,
		KeepRecentTokens: 20000,
	},
	BranchSummary: SettingsBranchSummaryDefaults{
		ReserveTokens: 16384,
		SkipPrompt:    false,
	},
	Retry: SettingsRetryDefaults{
		Enabled:         true,
		MaxRetries:      3,
		BaseDelayMs:     2000,
		MaxAgentDelayMs: float64(ai.DefaultMaxAgentRetryDelayMs),
		Provider: SettingsRetryProviderDefaults{
			MaxRetryDelayMs: 60000,
		},
	},
	HideThinkingBlock:      false,
	ShowCacheMissNotices:   false,
	QuietStartup:           QuietStartupBool(false),
	DefaultProjectTrust:    DefaultProjectTrustAsk,
	CollapseChangelog:      false,
	EnableInstallTelemetry: true,
	EnableAnalytics:        false,
	EnableSkillCommands:    true,
	Terminal: SettingsTerminalDefaults{
		ShowImages:           true,
		ImageWidthCells:      60,
		ClearOnShrink:        false,
		ShowTerminalProgress: false,
	},
	Images: SettingsImagesDefaults{
		AutoResize:  true,
		BlockImages: false,
	},
	DoubleEscapeAction:     DoubleEscapeActionTree,
	TreeFilterMode:         TreeFilterModeDefault,
	EditorPaddingX:         0,
	OutputPad:              1,
	AutocompleteMaxVisible: 5,
	Markdown: SettingsMarkdownDefaults{
		CodeBlockIndent: "  ",
		Mermaid:         MermaidRenderingStreaming,
	},
	Warnings: SettingsWarningsDefaults{
		AnthropicExtraUsage: true,
	},
	Codemode: SettingsCodemodeDefaults{
		Mode:         CodemodeModeOn,
		InlineBudget: 3000,
	},
	CacheWarming:               CacheWarmingIdle,
	TuiMode:                    tui.TuiModeFullscreen,
	FullscreenExitOutput:       FullscreenExitOutputTranscript,
	FullscreenScrollbar:        tui.ScrollViewScrollbarAuto,
	FullscreenCopyOnSelect:     true,
	FullscreenWheelScrollLines: tui.WheelScrollLines{Auto: true},
}
