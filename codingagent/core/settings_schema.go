// Ported from packages/coding-agent/src/core/settings-schema.ts (pi v1.5.2).

package core

import (
	"fmt"
	"math"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/tui"
)

const sschMaxSafeInteger = float64(9007199254740991)

// TransportSetting controls the wire transport preference.
type TransportSetting = ai.Transport

const (
	TransportAuto            TransportSetting = ai.TransportAuto
	TransportSse             TransportSetting = ai.TransportSse
	TransportWebsocket       TransportSetting = ai.TransportWebsocket
	TransportWebsocketCached TransportSetting = ai.TransportWebsocketCached
)

// SessionShareDestination selects the /share destination.
type SessionShareDestination string

const (
	SessionShareDestinationAuto   SessionShareDestination = "auto"
	SessionShareDestinationGist   SessionShareDestination = "gist"
	SessionShareDestinationRadius SessionShareDestination = "radius"
)

// DefaultProjectTrust sets the default trust policy when prompting isn't skipped.
type DefaultProjectTrust string

const (
	DefaultProjectTrustAsk    DefaultProjectTrust = "ask"
	DefaultProjectTrustAlways DefaultProjectTrust = "always"
	DefaultProjectTrustNever  DefaultProjectTrust = "never"
)

// DoubleEscapeAction chooses what double-escape with an empty editor triggers.
type DoubleEscapeAction string

const (
	DoubleEscapeActionFork DoubleEscapeAction = "fork"
	DoubleEscapeActionTree DoubleEscapeAction = "tree"
	DoubleEscapeActionNone DoubleEscapeAction = "none"
)

// TreeFilterMode selects the default filter when opening /tree.
type TreeFilterMode string

const (
	TreeFilterModeDefault     TreeFilterMode = "default"
	TreeFilterModeNoTools     TreeFilterMode = "no-tools"
	TreeFilterModeUserOnly    TreeFilterMode = "user-only"
	TreeFilterModeLabeledOnly TreeFilterMode = "labeled-only"
	TreeFilterModeAll         TreeFilterMode = "all"
)

// MermaidRenderingMode selects how markdown mermaid diagrams are rendered.
type MermaidRenderingMode string

const (
	MermaidRenderingOff       MermaidRenderingMode = "off"
	MermaidRenderingFinal     MermaidRenderingMode = "final"
	MermaidRenderingStreaming MermaidRenderingMode = "streaming"
)

// CodemodeMode controls how codemode presents tools to the model.
type CodemodeMode string

const (
	CodemodeModeOn   CodemodeMode = "on"
	CodemodeModeOnly CodemodeMode = "only"
)

// CacheWarmingMode controls prompt cache warming cadence.
type CacheWarmingMode string

const (
	CacheWarmingOff       CacheWarmingMode = "off"
	CacheWarmingStreaming CacheWarmingMode = "streaming"
	CacheWarmingIdle      CacheWarmingMode = "idle"
)

// TuiMode selects regular or fullscreen TUI mode.
type TuiMode = tui.TuiMode

// FullscreenExitOutput controls what fullscreen mode outputs on exit.
type FullscreenExitOutput string

const (
	FullscreenExitOutputTranscript FullscreenExitOutput = "transcript"
	FullscreenExitOutputResumeHint FullscreenExitOutput = "resume-hint"
)

// QuietStartup models boolean | "header" in settings.json.
// Zero value marshals as false.
type QuietStartup struct {
	Silent bool
	Header bool
}

// QuietStartupBool returns a boolean QuietStartup value.
func QuietStartupBool(enabled bool) QuietStartup {
	return QuietStartup{Silent: enabled}
}

// QuietStartupHeader returns the "header" QuietStartup value.
func QuietStartupHeader() QuietStartup {
	return QuietStartup{Header: true}
}

// MarshalJSON encodes QuietStartup as false, true, or "header".
func (q QuietStartup) MarshalJSON() ([]byte, error) {
	if q.Header {
		return jsonx.Marshal("header")
	}
	return jsonx.Marshal(q.Silent)
}

// UnmarshalJSON decodes false, true, or "header" into QuietStartup.
func (q *QuietStartup) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	switch val := v.(type) {
	case bool:
		*q = QuietStartup{Silent: val}
		return nil
	case string:
		if val == "header" {
			*q = QuietStartup{Header: true}
			return nil
		}
		return fmt.Errorf("invalid quietStartup value %q", val)
	default:
		return fmt.Errorf("invalid quietStartup type %T", v)
	}
}

// TimeoutSetting models non-negative milliseconds or "disabled".
type TimeoutSetting struct {
	Disabled bool
	Ms       float64
}

// TimeoutSettingDisabled returns the "disabled" TimeoutSetting value.
func TimeoutSettingDisabled() TimeoutSetting {
	return TimeoutSetting{Disabled: true}
}

// TimeoutSettingMs returns a numeric TimeoutSetting value in milliseconds.
func TimeoutSettingMs(ms float64) TimeoutSetting {
	return TimeoutSetting{Ms: ms}
}

// MarshalJSON encodes TimeoutSetting as a number or "disabled".
func (t TimeoutSetting) MarshalJSON() ([]byte, error) {
	if t.Disabled {
		return jsonx.Marshal("disabled")
	}
	return jsonx.Marshal(t.Ms)
}

// UnmarshalJSON decodes a number or "disabled" into TimeoutSetting.
func (t *TimeoutSetting) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	switch val := v.(type) {
	case string:
		if val == "disabled" {
			*t = TimeoutSetting{Disabled: true}
			return nil
		}
		return fmt.Errorf("invalid timeout setting %q", val)
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) || val < 0 {
			return fmt.Errorf("invalid timeout number %v", val)
		}
		*t = TimeoutSetting{Ms: val}
		return nil
	default:
		return fmt.Errorf("invalid timeout type %T", v)
	}
}

// TerminalAutoBool models boolean | "auto" for terminal capability settings.
type TerminalAutoBool struct {
	Auto    bool
	Enabled bool
}

// MarshalJSON encodes TerminalAutoBool as a boolean or "auto".
func (t TerminalAutoBool) MarshalJSON() ([]byte, error) {
	if t.Auto {
		return jsonx.Marshal("auto")
	}
	return jsonx.Marshal(t.Enabled)
}

// UnmarshalJSON decodes a boolean or "auto" into TerminalAutoBool.
func (t *TerminalAutoBool) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	switch val := v.(type) {
	case string:
		if val == "auto" {
			*t = TerminalAutoBool{Auto: true}
			return nil
		}
		return fmt.Errorf("invalid terminal auto/bool setting %q", val)
	case bool:
		*t = TerminalAutoBool{Enabled: val}
		return nil
	default:
		return fmt.Errorf("invalid terminal auto/bool type %T", v)
	}
}

// TerminalImageSetting models "kitty" | "iterm2" | "auto" | false.
type TerminalImageSetting struct {
	Protocol string
	Disabled bool
}

// MarshalJSON encodes TerminalImageSetting as false or a protocol string.
func (t TerminalImageSetting) MarshalJSON() ([]byte, error) {
	if t.Disabled {
		return jsonx.Marshal(false)
	}
	return jsonx.Marshal(t.Protocol)
}

// UnmarshalJSON decodes "kitty" | "iterm2" | "auto" | false into TerminalImageSetting.
func (t *TerminalImageSetting) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	switch val := v.(type) {
	case bool:
		if !val {
			*t = TerminalImageSetting{Disabled: true}
			return nil
		}
		return fmt.Errorf("invalid terminal images value true")
	case string:
		if val == "kitty" || val == "iterm2" || val == "auto" {
			*t = TerminalImageSetting{Protocol: val}
			return nil
		}
		return fmt.Errorf("invalid terminal images value %q", val)
	default:
		return fmt.Errorf("invalid terminal images type %T", v)
	}
}

// PackageSourceObject is the filtered object form of PackageSource.
type PackageSourceObject struct {
	Source     string   `json:"source"`
	Autoload   *bool    `json:"autoload,omitzero"`
	Extensions []string `json:"extensions,omitzero"`
	Skills     []string `json:"skills,omitzero"`
	Prompts    []string `json:"prompts,omitzero"`
	Themes     []string `json:"themes,omitzero"`
}

// PackageSource models a package source string or resource-filtered object.
type PackageSource struct {
	String *string
	Object *PackageSourceObject
}

// PackageSourceFromString constructs a string-form PackageSource.
func PackageSourceFromString(source string) PackageSource {
	return PackageSource{String: &source}
}

// PackageSourceFromObject constructs an object-form PackageSource.
func PackageSourceFromObject(obj PackageSourceObject) PackageSource {
	return PackageSource{Object: &obj}
}

// MarshalJSON encodes PackageSource as a string or object.
func (p PackageSource) MarshalJSON() ([]byte, error) {
	if p.String != nil {
		return jsonx.Marshal(*p.String)
	}
	if p.Object != nil {
		return jsonx.Marshal(p.Object)
	}
	return []byte("null"), nil
}

// UnmarshalJSON decodes a string or object into PackageSource.
func (p *PackageSource) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	switch v.(type) {
	case string:
		s := v.(string)
		*p = PackageSource{String: &s}
		return nil
	case *jsonx.Object:
		var obj PackageSourceObject
		if err := jsonx.Decode(v, &obj); err != nil {
			return err
		}
		*p = PackageSource{Object: &obj}
		return nil
	default:
		return fmt.Errorf("invalid package source type %T", v)
	}
}

// CompactionModelOverride holds per-model compaction token overrides.
type CompactionModelOverride struct {
	ReserveTokens    *float64 `json:"reserveTokens,omitzero"`
	KeepRecentTokens *float64 `json:"keepRecentTokens,omitzero"`
}

// SettingsCompactionSettings holds the compaction block in settings.json.
type SettingsCompactionSettings struct {
	Enabled          *bool                                       `json:"enabled,omitzero"`
	ReserveTokens    *float64                                    `json:"reserveTokens,omitzero"`
	KeepRecentTokens *float64                                    `json:"keepRecentTokens,omitzero"`
	ModelOverrides   *omap.Map[string, *CompactionModelOverride] `json:"modelOverrides,omitzero"`
}

// BranchSummarySettings holds the branchSummary block in settings.json.
type BranchSummarySettings struct {
	ReserveTokens *float64 `json:"reserveTokens,omitzero"`
	SkipPrompt    *bool    `json:"skipPrompt,omitzero"`
}

// SettingsBranchSummarySettings aliases BranchSummarySettings.
type SettingsBranchSummarySettings = BranchSummarySettings

// ProviderRetrySettings holds provider SDK retry/timeout settings.
type ProviderRetrySettings struct {
	TimeoutMs       *float64 `json:"timeoutMs,omitzero"`
	MaxRetries      *int     `json:"maxRetries,omitzero"`
	MaxRetryDelayMs *float64 `json:"maxRetryDelayMs,omitzero"`
}

// RetrySettings holds agent and provider retry configuration.
type RetrySettings struct {
	Enabled         *bool                  `json:"enabled,omitzero"`
	MaxRetries      *int                   `json:"maxRetries,omitzero"`
	BaseDelayMs     *float64               `json:"baseDelayMs,omitzero"`
	MaxAgentDelayMs *float64               `json:"maxAgentDelayMs,omitzero"`
	Provider        *ProviderRetrySettings `json:"provider,omitzero"`
}

// TerminalSettings configures terminal image rendering and progress output.
type TerminalSettings struct {
	ShowImages           *bool                 `json:"showImages,omitzero"`
	ImageWidthCells      *int                  `json:"imageWidthCells,omitzero"`
	ClearOnShrink        *bool                 `json:"clearOnShrink,omitzero"`
	ShowTerminalProgress *bool                 `json:"showTerminalProgress,omitzero"`
	Hyperlinks           *TerminalAutoBool     `json:"hyperlinks,omitzero"`
	Images               *TerminalImageSetting `json:"images,omitzero"`
	TrueColor            *TerminalAutoBool     `json:"trueColor,omitzero"`
}

// ImageSettings configures image handling before sending to providers.
type ImageSettings struct {
	AutoResize  *bool `json:"autoResize,omitzero"`
	BlockImages *bool `json:"blockImages,omitzero"`
}

// ImagesSettings aliases ImageSettings.
type ImagesSettings = ImageSettings

// ThinkingBudgetsSettings configures custom token budgets for thinking levels.
type ThinkingBudgetsSettings = ai.ThinkingBudgets

// MarkdownSettings configures markdown rendering in the TUI.
type MarkdownSettings struct {
	CodeBlockIndent *string               `json:"codeBlockIndent,omitzero"`
	Mermaid         *MermaidRenderingMode `json:"mermaid,omitzero"`
}

// WarningSettings toggles optional user-facing warnings.
type WarningSettings struct {
	AnthropicExtraUsage *bool `json:"anthropicExtraUsage,omitzero"`
}

// CodemodeSettings configures codemode tool declaration mode and budget.
type CodemodeSettings struct {
	Mode         *CodemodeMode `json:"mode,omitzero"`
	InlineBudget *float64      `json:"inlineBudget,omitzero"`
}

// Settings models migrated settings.json contents across global and project scopes.
// Property order matches SettingsSchema in TS settings-schema.ts.
type Settings struct {
	Schema                     *string                                `json:"$schema,omitzero"`
	LastChangelogVersion       *string                                `json:"lastChangelogVersion,omitzero"`
	ExtensionChangelogVersions *omap.Map[string, string]              `json:"extensionChangelogVersions,omitzero"`
	DefaultProvider            *string                                `json:"defaultProvider,omitzero"`
	DefaultModel               *string                                `json:"defaultModel,omitzero"`
	DefaultThinkingLevel       *agent.ThinkingLevel                   `json:"defaultThinkingLevel,omitzero"`
	ModelThinkingLevels        *omap.Map[string, agent.ThinkingLevel] `json:"modelThinkingLevels,omitzero"`
	Transport                  *TransportSetting                      `json:"transport,omitzero"`
	SessionShareDestination    *SessionShareDestination               `json:"sessionShareDestination,omitzero"`
	SteeringMode               *agent.QueueMode                       `json:"steeringMode,omitzero"`
	FollowUpMode               *agent.QueueMode                       `json:"followUpMode,omitzero"`
	Theme                      *string                                `json:"theme,omitzero"`
	Compaction                 *SettingsCompactionSettings            `json:"compaction,omitzero"`
	BranchSummary              *BranchSummarySettings                 `json:"branchSummary,omitzero"`
	Retry                      *RetrySettings                         `json:"retry,omitzero"`
	HideThinkingBlock          *bool                                  `json:"hideThinkingBlock,omitzero"`
	ShowCacheMissNotices       *bool                                  `json:"showCacheMissNotices,omitzero"`
	ExternalEditor             *string                                `json:"externalEditor,omitzero"`
	ShellPath                  *string                                `json:"shellPath,omitzero"`
	QuietStartup               *QuietStartup                          `json:"quietStartup,omitzero"`
	DefaultProjectTrust        *DefaultProjectTrust                   `json:"defaultProjectTrust,omitzero"`
	ShellCommandPrefix         *string                                `json:"shellCommandPrefix,omitzero"`
	NpmCommand                 []string                               `json:"npmCommand,omitzero"`
	SelfUpdateCommand          []string                               `json:"selfUpdateCommand,omitzero"`
	CollapseChangelog          *bool                                  `json:"collapseChangelog,omitzero"`
	EnableInstallTelemetry     *bool                                  `json:"enableInstallTelemetry,omitzero"`
	EnableAnalytics            *bool                                  `json:"enableAnalytics,omitzero"`
	TrackingId                 *string                                `json:"trackingId,omitzero"`
	Packages                   []PackageSource                        `json:"packages,omitzero"`
	Extensions                 []string                               `json:"extensions,omitzero"`
	DeviceId                   *string                                `json:"deviceId,omitzero"`
	Skills                     []string                               `json:"skills,omitzero"`
	Prompts                    []string                               `json:"prompts,omitzero"`
	Themes                     []string                               `json:"themes,omitzero"`
	EnableSkillCommands        *bool                                  `json:"enableSkillCommands,omitzero"`
	Terminal                   *TerminalSettings                      `json:"terminal,omitzero"`
	Images                     *ImageSettings                         `json:"images,omitzero"`
	EnabledModels              []string                               `json:"enabledModels,omitzero"`
	DefaultTools               []string                               `json:"defaultTools,omitzero"`
	DoubleEscapeAction         *DoubleEscapeAction                    `json:"doubleEscapeAction,omitzero"`
	TreeFilterMode             *TreeFilterMode                        `json:"treeFilterMode,omitzero"`
	ThinkingBudgets            *ThinkingBudgetsSettings               `json:"thinkingBudgets,omitzero"`
	EditorPaddingX             *int                                   `json:"editorPaddingX,omitzero"`
	OutputPad                  *int                                   `json:"outputPad,omitzero"`
	AutocompleteMaxVisible     *int                                   `json:"autocompleteMaxVisible,omitzero"`
	ShowHardwareCursor         *bool                                  `json:"showHardwareCursor,omitzero"`
	Markdown                   *MarkdownSettings                      `json:"markdown,omitzero"`
	Warnings                   *WarningSettings                       `json:"warnings,omitzero"`
	Codemode                   *CodemodeSettings                      `json:"codemode,omitzero"`
	SessionDir                 *string                                `json:"sessionDir,omitzero"`
	HttpProxy                  *string                                `json:"httpProxy,omitzero"`
	HttpIdleTimeoutMs          *TimeoutSetting                        `json:"httpIdleTimeoutMs,omitzero"`
	CacheWarming               *CacheWarmingMode                      `json:"cacheWarming,omitzero"`
	WebsocketConnectTimeoutMs  *TimeoutSetting                        `json:"websocketConnectTimeoutMs,omitzero"`
	TuiMode                    *TuiMode                               `json:"tuiMode,omitzero"`
	FullscreenExitOutput       *FullscreenExitOutput                  `json:"fullscreenExitOutput,omitzero"`
	FullscreenScrollbar        *tui.ScrollViewScrollbar               `json:"fullscreenScrollbar,omitzero"`
	FullscreenCopyOnSelect     *bool                                  `json:"fullscreenCopyOnSelect,omitzero"`
	FullscreenWheelScrollLines *tui.WheelScrollLines                  `json:"fullscreenWheelScrollLines,omitzero"`
}

func sschNonNegativeSafeInteger(opts ...*jsonx.Object) *typebox.Schema {
	base := jsonx.ObjectOf("minimum", 0, "maximum", sschMaxSafeInteger)
	for _, o := range opts {
		if o != nil {
			base.Assign(o)
		}
	}
	return typebox.Integer(base)
}

func sschTimeoutSetting(opts ...*jsonx.Object) *typebox.Schema {
	return typebox.Union([]*typebox.Schema{
		typebox.Number(jsonx.ObjectOf("minimum", 0)),
		typebox.Literal("disabled"),
	}, opts...)
}

func sschThinkingBudgetsSettings(opts ...*jsonx.Object) *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("minimal", typebox.Optional(typebox.Number()))
	props.Set("low", typebox.Optional(typebox.Number()))
	props.Set("medium", typebox.Optional(typebox.Number()))
	props.Set("high", typebox.Optional(typebox.Number()))
	return typebox.Object(props, opts...)
}

func sschCodemodeMode(opts ...*jsonx.Object) *typebox.Schema {
	return typebox.Union([]*typebox.Schema{
		typebox.Literal("on"),
		typebox.Literal("only"),
	}, opts...)
}

var (
	sschCompactionModelOverrideSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("reserveTokens", typebox.Optional(sschNonNegativeSafeInteger()))
		props.Set("keepRecentTokens", typebox.Optional(sschNonNegativeSafeInteger()))
		return typebox.Object(props)
	}()

	sschCompactionSettingsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("enabled", typebox.Optional(typebox.Boolean(jsonx.ObjectOf("default", SettingsDefaults.Compaction.Enabled))))
		props.Set("reserveTokens", typebox.Optional(sschNonNegativeSafeInteger(jsonx.ObjectOf("default", SettingsDefaults.Compaction.ReserveTokens))))
		props.Set("keepRecentTokens", typebox.Optional(sschNonNegativeSafeInteger(jsonx.ObjectOf("default", SettingsDefaults.Compaction.KeepRecentTokens))))
		props.Set("modelOverrides", typebox.Optional(typebox.Record(
			typebox.String(),
			sschCompactionModelOverrideSchema,
			jsonx.ObjectOf("description", `Per-model overrides keyed by exact "provider/modelId" strings.`),
		)))
		return typebox.Object(props)
	}()

	sschBranchSummarySettingsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("reserveTokens", typebox.Optional(typebox.Number(jsonx.ObjectOf(
			"description", "Tokens reserved for the prompt and LLM response.",
			"default", SettingsDefaults.BranchSummary.ReserveTokens,
		))))
		props.Set("skipPrompt", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
			"description", `When true, skips the "Summarize branch?" prompt and defaults to no summary.`,
			"default", SettingsDefaults.BranchSummary.SkipPrompt,
		))))
		return typebox.Object(props)
	}()

	sschProviderRetrySettingsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("timeoutMs", typebox.Optional(typebox.Number(jsonx.ObjectOf(
			"description", "SDK or provider request timeout in milliseconds.",
		))))
		props.Set("maxRetries", typebox.Optional(typebox.Number(jsonx.ObjectOf(
			"description", "SDK or provider retry attempts.",
		))))
		props.Set("maxRetryDelayMs", typebox.Optional(typebox.Number(jsonx.ObjectOf(
			"description", "Maximum server-requested delay before failing.",
			"default", SettingsDefaults.Retry.Provider.MaxRetryDelayMs,
		))))
		return typebox.Object(props)
	}()

	sschRetrySettingsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("enabled", typebox.Optional(typebox.Boolean(jsonx.ObjectOf("default", SettingsDefaults.Retry.Enabled))))
		props.Set("maxRetries", typebox.Optional(typebox.Number(jsonx.ObjectOf("default", SettingsDefaults.Retry.MaxRetries))))
		props.Set("baseDelayMs", typebox.Optional(typebox.Number(jsonx.ObjectOf(
			"description", "Exponential backoff base delay in milliseconds: 2s, 4s, 8s.",
			"default", SettingsDefaults.Retry.BaseDelayMs,
		))))
		props.Set("maxAgentDelayMs", typebox.Optional(typebox.Number(jsonx.ObjectOf("default", SettingsDefaults.Retry.MaxAgentDelayMs))))
		props.Set("provider", typebox.Optional(sschProviderRetrySettingsSchema))
		props.Set("maxDelayMs", typebox.Optional(typebox.Number(jsonx.ObjectOf(
			"description", "Legacy retry delay setting. Use provider.maxRetryDelayMs instead.",
			"deprecated", true,
		))))
		return typebox.Object(props)
	}()

	sschTerminalSettingsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("showImages", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
			"description", "Show images when the terminal supports them.",
			"default", SettingsDefaults.Terminal.ShowImages,
		))))
		props.Set("imageWidthCells", typebox.Optional(typebox.Number(jsonx.ObjectOf(
			"description", "Preferred inline image width in terminal cells.",
			"default", SettingsDefaults.Terminal.ImageWidthCells,
		))))
		props.Set("clearOnShrink", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
			"description", "Clear empty rows when content shrinks.",
			"default", SettingsDefaults.Terminal.ClearOnShrink,
		))))
		props.Set("showTerminalProgress", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
			"description", "Show OSC 9;4 terminal progress indicators.",
			"default", SettingsDefaults.Terminal.ShowTerminalProgress,
		))))
		props.Set("hyperlinks", typebox.Optional(typebox.Union([]*typebox.Schema{
			typebox.Boolean(),
			typebox.Literal("auto"),
		})))
		props.Set("images", typebox.Optional(typebox.Union([]*typebox.Schema{
			typebox.Literal("kitty"),
			typebox.Literal("iterm2"),
			typebox.Literal("auto"),
			typebox.Literal(false),
		})))
		props.Set("trueColor", typebox.Optional(typebox.Union([]*typebox.Schema{
			typebox.Boolean(),
			typebox.Literal("auto"),
		})))
		return typebox.Object(props)
	}()

	sschImageSettingsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("autoResize", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
			"description", "Resize images to 2000x2000 maximum for better model compatibility.",
			"default", SettingsDefaults.Images.AutoResize,
		))))
		props.Set("blockImages", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
			"description", "When true, prevents all images from being sent to LLM providers.",
			"default", SettingsDefaults.Images.BlockImages,
		))))
		return typebox.Object(props)
	}()

	sschMarkdownSettingsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("codeBlockIndent", typebox.Optional(typebox.String(jsonx.ObjectOf(
			"default", SettingsDefaults.Markdown.CodeBlockIndent,
		))))
		props.Set("mermaid", typebox.Optional(typebox.Union([]*typebox.Schema{
			typebox.Literal("off"),
			typebox.Literal("final"),
			typebox.Literal("streaming"),
		}, jsonx.ObjectOf("default", string(SettingsDefaults.Markdown.Mermaid)))))
		return typebox.Object(props)
	}()

	sschWarningSettingsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("anthropicExtraUsage", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
			"default", SettingsDefaults.Warnings.AnthropicExtraUsage,
		))))
		return typebox.Object(props)
	}()

	sschCodemodeSettingsSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("mode", typebox.Optional(sschCodemodeMode(jsonx.ObjectOf(
			"description", `How codemode presents tools. "on" keeps direct tools declared and annotates tools callable from scripts; codemode lists only tools without direct exposure. "only" lists every script-callable tool in codemode and does not declare active direct tools.`,
			"default", string(SettingsDefaults.Codemode.Mode),
		))))
		props.Set("inlineBudget", typebox.Optional(typebox.Number(jsonx.ObjectOf(
			"minimum", 0,
			"description", "Estimated tokens available for inline codemode tool declarations.",
			"default", SettingsDefaults.Codemode.InlineBudget,
		))))
		return typebox.Object(props)
	}()

	sschPackageSourceSchema = func() *typebox.Schema {
		objProps := omap.NewMap[string, *typebox.Schema]()
		objProps.Set("source", typebox.String())
		objProps.Set("autoload", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
			"description", "When false, start empty and only apply explicit resource patterns.",
		))))
		objProps.Set("extensions", typebox.Optional(typebox.Array(typebox.String())))
		objProps.Set("skills", typebox.Optional(typebox.Array(typebox.String())))
		objProps.Set("prompts", typebox.Optional(typebox.Array(typebox.String())))
		objProps.Set("themes", typebox.Optional(typebox.Array(typebox.String())))
		return typebox.Union([]*typebox.Schema{
			typebox.String(jsonx.ObjectOf("description", "Load all resources from the package.")),
			typebox.Object(objProps),
		}, jsonx.ObjectOf(
			"description", "Package source for npm or git packages. Use the string form to load all resources or the object form to filter resources.",
		))
	}()

	sschStringArraySchema = typebox.Array(typebox.String())

	sschSkillsInputSchema = func() *typebox.Schema {
		legacyProps := omap.NewMap[string, *typebox.Schema]()
		legacyProps.Set("enableSkillCommands", typebox.Optional(typebox.Boolean()))
		legacyProps.Set("customDirectories", typebox.Optional(sschStringArraySchema))
		return typebox.Union([]*typebox.Schema{
			sschStringArraySchema,
			typebox.Object(legacyProps, jsonx.ObjectOf("deprecated", true)),
		}, jsonx.ObjectOf("description", "Local skill file paths or directories."))
	}()
)

// SettingsSchema is the TypeBox JSON Schema for settings.json.
var SettingsSchema = func() *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("$schema", typebox.Optional(typebox.String(jsonx.ObjectOf("description", "JSON Schema reference."))))
	props.Set("lastChangelogVersion", typebox.Optional(typebox.String()))
	props.Set("extensionChangelogVersions", typebox.Optional(typebox.Record(
		typebox.String(),
		typebox.String(),
		jsonx.ObjectOf("description", "Last seen changelog version per extension package."),
	)))
	props.Set("defaultProvider", typebox.Optional(typebox.String()))
	props.Set("defaultModel", typebox.Optional(typebox.String()))
	props.Set("defaultThinkingLevel", typebox.Optional(providers.ModelThinkingLevelSchema))
	props.Set("modelThinkingLevels", typebox.Optional(typebox.Record(
		typebox.String(),
		providers.ModelThinkingLevelSchema,
		jsonx.ObjectOf("description", `Per-model default thinking level overrides keyed by "provider/modelId".`),
	)))
	props.Set("transport", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("auto"),
		typebox.Literal("sse"),
		typebox.Literal("websocket"),
		typebox.Literal("websocket-cached"),
	}, jsonx.ObjectOf("default", string(SettingsDefaults.Transport)))))
	props.Set("sessionShareDestination", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("auto"),
		typebox.Literal("gist"),
		typebox.Literal("radius"),
	}, jsonx.ObjectOf(
		"description", `Destination used by /share. "auto" uses Radius when authenticated and otherwise creates a GitHub gist.`,
		"default", string(SettingsDefaults.SessionShareDestination),
	))))
	props.Set("steeringMode", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("all"),
		typebox.Literal("one-at-a-time"),
	}, jsonx.ObjectOf("default", string(SettingsDefaults.SteeringMode)))))
	props.Set("followUpMode", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("all"),
		typebox.Literal("one-at-a-time"),
	}, jsonx.ObjectOf("default", string(SettingsDefaults.FollowUpMode)))))
	props.Set("theme", typebox.Optional(typebox.String()))
	props.Set("compaction", typebox.Optional(sschCompactionSettingsSchema))
	props.Set("branchSummary", typebox.Optional(sschBranchSummarySettingsSchema))
	props.Set("retry", typebox.Optional(sschRetrySettingsSchema))
	props.Set("hideThinkingBlock", typebox.Optional(typebox.Boolean(jsonx.ObjectOf("default", SettingsDefaults.HideThinkingBlock))))
	props.Set("showCacheMissNotices", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
		"description", "Show cache cost and provider recovery notices.",
		"default", SettingsDefaults.ShowCacheMissNotices,
	))))
	props.Set("externalEditor", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Command for Ctrl+G external editor; takes precedence over VISUAL and EDITOR.",
	))))
	props.Set("shellPath", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Custom shell path, for example for Cygwin on Windows, with support for leading ~ expansion.",
	))))
	props.Set("quietStartup", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Boolean(),
		typebox.Literal("header"),
	}, jsonx.ObjectOf(
		"description", `When true, hide all startup output. When "header", keep only the startup header.`,
		"default", SettingsDefaults.QuietStartup,
	))))
	props.Set("defaultProjectTrust", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("ask"),
		typebox.Literal("always"),
		typebox.Literal("never"),
	}, jsonx.ObjectOf(
		"description", "Global setting only.",
		"default", string(SettingsDefaults.DefaultProjectTrust),
	))))
	props.Set("shellCommandPrefix", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Prefix prepended to every bash command, for example to enable shell aliases.",
	))))
	props.Set("npmCommand", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
		"description", `Command used for npm package lookup and installation, in argv form such as ["mise", "exec", "node@20", "--", "npm"].`,
	))))
	props.Set("selfUpdateCommand", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
		"minItems", 1,
		"description", `Command `+"`pi update`"+` runs to update Pi itself, in argv form such as ["mise", "use", "-g", "npm:{packageName}@{version}"]. Placeholders: {packageName}, {version}, {installSpec}. PI_SELF_UPDATE_COMMAND takes precedence. Global setting only.`,
	))))
	props.Set("collapseChangelog", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
		"description", "Show the condensed changelog after update; use /changelog for the full changelog.",
		"default", SettingsDefaults.CollapseChangelog,
	))))
	props.Set("enableInstallTelemetry", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
		"description", "Send an anonymous version and update ping after changelog-detected updates.",
		"default", SettingsDefaults.EnableInstallTelemetry,
	))))
	props.Set("enableAnalytics", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
		"description", "Opt in to analytics data sharing.",
		"default", SettingsDefaults.EnableAnalytics,
	))))
	props.Set("trackingId", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Analytics tracking identifier, generated when analytics is enabled.",
	))))
	props.Set("packages", typebox.Optional(typebox.Array(sschPackageSourceSchema, jsonx.ObjectOf(
		"description", "npm or git package sources, as strings or objects with resource filtering.",
	))))
	props.Set("extensions", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
		"description", "Local extension file paths or directories.",
	))))
	props.Set("deviceId", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Stable installation UUID, generated when authentication first needs it. Global only.",
	))))
	props.Set("skills", typebox.Optional(sschSkillsInputSchema))
	props.Set("prompts", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
		"description", "Local prompt template file paths or directories.",
	))))
	props.Set("themes", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
		"description", "Local theme file paths or directories.",
	))))
	props.Set("enableSkillCommands", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
		"description", "Register skills as /skill:name commands.",
		"default", SettingsDefaults.EnableSkillCommands,
	))))
	props.Set("terminal", typebox.Optional(sschTerminalSettingsSchema))
	props.Set("images", typebox.Optional(sschImageSettingsSchema))
	props.Set("enabledModels", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
		"description", "Model patterns for cycling, in the same format as the --models CLI flag.",
	))))
	props.Set("defaultTools", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
		"description", "Initial tool selection. Plain names replace the inherited selection; +name and -name entries add or remove tools.",
	))))
	props.Set("doubleEscapeAction", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("fork"),
		typebox.Literal("tree"),
		typebox.Literal("none"),
	}, jsonx.ObjectOf(
		"description", "Action for double-escape with an empty editor.",
		"default", string(SettingsDefaults.DoubleEscapeAction),
	))))
	props.Set("treeFilterMode", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("default"),
		typebox.Literal("no-tools"),
		typebox.Literal("user-only"),
		typebox.Literal("labeled-only"),
		typebox.Literal("all"),
	}, jsonx.ObjectOf(
		"description", "Default filter when opening /tree.",
		"default", string(SettingsDefaults.TreeFilterMode),
	))))
	props.Set("thinkingBudgets", typebox.Optional(sschThinkingBudgetsSettings(jsonx.ObjectOf(
		"description", "Custom token budgets for thinking levels.",
	))))
	props.Set("editorPaddingX", typebox.Optional(typebox.Number(jsonx.ObjectOf(
		"description", "Horizontal padding for the input editor.",
		"default", SettingsDefaults.EditorPaddingX,
	))))
	props.Set("outputPad", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal(0),
		typebox.Literal(1),
	}, jsonx.ObjectOf(
		"description", "Horizontal padding for transcript content.",
		"default", SettingsDefaults.OutputPad,
	))))
	props.Set("autocompleteMaxVisible", typebox.Optional(typebox.Number(jsonx.ObjectOf(
		"description", "Maximum visible items in the autocomplete dropdown.",
		"default", SettingsDefaults.AutocompleteMaxVisible,
	))))
	props.Set("showHardwareCursor", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
		"description", "Show the terminal cursor while still positioning it for IME.",
	))))
	props.Set("markdown", typebox.Optional(sschMarkdownSettingsSchema))
	props.Set("warnings", typebox.Optional(sschWarningSettingsSchema))
	props.Set("codemode", typebox.Optional(sschCodemodeSettingsSchema))
	props.Set("sessionDir", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Custom session storage directory, in the same format as the --session-dir CLI flag.",
	))))
	props.Set("httpProxy", typebox.Optional(typebox.String(jsonx.ObjectOf(
		"description", "Proxy URL applied as HTTP_PROXY and HTTPS_PROXY for Pi-managed HTTP clients.",
	))))
	props.Set("httpIdleTimeoutMs", typebox.Optional(sschTimeoutSetting(jsonx.ObjectOf(
		"description", `HTTP header or body idle timeout in milliseconds; 0 or "disabled" disables it.`,
	))))
	props.Set("cacheWarming", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("off"),
		typebox.Literal("streaming"),
		typebox.Literal("idle"),
	}, jsonx.ObjectOf(
		"description", `Cache-warming profile. "idle" also warms between agent runs. Global only because each refresh costs money.`,
		"default", string(SettingsDefaults.CacheWarming),
	))))
	props.Set("websocketConnectTimeoutMs", typebox.Optional(sschTimeoutSetting(jsonx.ObjectOf(
		"description", `WebSocket connect or open handshake timeout in milliseconds; 0 or "disabled" disables it.`,
	))))
	props.Set("tuiMode", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("regular"),
		typebox.Literal("fullscreen"),
	}, jsonx.ObjectOf("default", string(SettingsDefaults.TuiMode)))))
	props.Set("fullscreenExitOutput", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("transcript"),
		typebox.Literal("resume-hint"),
	}, jsonx.ObjectOf(
		"description", "No effect in regular TUI mode.",
		"default", string(SettingsDefaults.FullscreenExitOutput),
	))))
	props.Set("fullscreenScrollbar", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("auto"),
		typebox.Literal("always"),
		typebox.Literal("hidden"),
	}, jsonx.ObjectOf(
		"description", "No effect in regular TUI mode.",
		"default", string(SettingsDefaults.FullscreenScrollbar),
	))))
	props.Set("fullscreenCopyOnSelect", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
		"description", "No effect in regular TUI mode.",
		"default", SettingsDefaults.FullscreenCopyOnSelect,
	))))
	props.Set("fullscreenWheelScrollLines", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Number(),
		typebox.Literal("auto"),
	}, jsonx.ObjectOf(
		"description", "Lines scrolled per wheel event in fullscreen mode; numeric values are clamped from 1 to 100.",
		"default", SettingsDefaults.FullscreenWheelScrollLines,
	))))
	props.Set("queueMode", typebox.Optional(typebox.Union([]*typebox.Schema{
		typebox.Literal("all"),
		typebox.Literal("one-at-a-time"),
	}, jsonx.ObjectOf(
		"description", "Legacy setting migrated to steeringMode.",
		"deprecated", true,
	))))
	props.Set("websockets", typebox.Optional(typebox.Boolean(jsonx.ObjectOf(
		"description", "Legacy setting migrated to transport.",
		"deprecated", true,
	))))
	return typebox.Object(props, jsonx.ObjectOf("additionalProperties", true))
}()
