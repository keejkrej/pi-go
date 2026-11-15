// Ported from packages/coding-agent/src/modes/interactive/theme/theme-tokens.ts (pi v1.5.2).

package theme

// Foreground theme color token constants.
const (
	FgAccent             = "accent"
	FgBorder             = "border"
	FgBorderAccent       = "borderAccent"
	FgBorderMuted        = "borderMuted"
	FgSuccess            = "success"
	FgError              = "error"
	FgWarning            = "warning"
	FgMuted              = "muted"
	FgDim                = "dim"
	FgText               = "text"
	FgThinkingText       = "thinkingText"
	FgScrollbarTrack     = "scrollbarTrack"
	FgScrollbarThumb     = "scrollbarThumb"
	FgSearchMatchText    = "searchMatchText"
	FgUserMessageText    = "userMessageText"
	FgCustomMessageText  = "customMessageText"
	FgCustomMessageLabel = "customMessageLabel"
	FgToolTitle          = "toolTitle"
	FgToolOutput         = "toolOutput"
	FgMdHeading          = "mdHeading"
	FgMdLink             = "mdLink"
	FgMdLinkUrl          = "mdLinkUrl"
	FgMdCode             = "mdCode"
	FgMdCodeBlock        = "mdCodeBlock"
	FgMdCodeBlockBorder  = "mdCodeBlockBorder"
	FgMdQuote            = "mdQuote"
	FgMdQuoteBorder      = "mdQuoteBorder"
	FgMdHr               = "mdHr"
	FgMdListBullet       = "mdListBullet"
	FgToolDiffAdded      = "toolDiffAdded"
	FgToolDiffRemoved    = "toolDiffRemoved"
	FgToolDiffContext    = "toolDiffContext"
	FgSyntaxComment      = "syntaxComment"
	FgSyntaxKeyword      = "syntaxKeyword"
	FgSyntaxFunction     = "syntaxFunction"
	FgSyntaxVariable     = "syntaxVariable"
	FgSyntaxString       = "syntaxString"
	FgSyntaxNumber       = "syntaxNumber"
	FgSyntaxType         = "syntaxType"
	FgSyntaxOperator     = "syntaxOperator"
	FgSyntaxPunctuation  = "syntaxPunctuation"
	FgThinkingOff        = "thinkingOff"
	FgThinkingMinimal    = "thinkingMinimal"
	FgThinkingLow        = "thinkingLow"
	FgThinkingMedium     = "thinkingMedium"
	FgThinkingHigh       = "thinkingHigh"
	FgThinkingXhigh      = "thinkingXhigh"
	FgThinkingMax        = "thinkingMax"
	FgBashMode           = "bashMode"
)

// Background theme color token constants.
const (
	BgSelected      = "selectedBg"
	BgSearchMatch   = "searchMatchBg"
	BgUserMessage   = "userMessageBg"
	BgCustomMessage = "customMessageBg"
	BgToolPending   = "toolPendingBg"
	BgToolSuccess   = "toolSuccessBg"
	BgToolError     = "toolErrorBg"
)

// ThemeColorFgTokens lists all foreground color tokens in canonical order.
var ThemeColorFgTokens = []string{
	FgAccent,
	FgBorder,
	FgBorderAccent,
	FgBorderMuted,
	FgSuccess,
	FgError,
	FgWarning,
	FgMuted,
	FgDim,
	FgText,
	FgThinkingText,
	FgScrollbarTrack,
	FgScrollbarThumb,
	FgSearchMatchText,
	FgUserMessageText,
	FgCustomMessageText,
	FgCustomMessageLabel,
	FgToolTitle,
	FgToolOutput,
	FgMdHeading,
	FgMdLink,
	FgMdLinkUrl,
	FgMdCode,
	FgMdCodeBlock,
	FgMdCodeBlockBorder,
	FgMdQuote,
	FgMdQuoteBorder,
	FgMdHr,
	FgMdListBullet,
	FgToolDiffAdded,
	FgToolDiffRemoved,
	FgToolDiffContext,
	FgSyntaxComment,
	FgSyntaxKeyword,
	FgSyntaxFunction,
	FgSyntaxVariable,
	FgSyntaxString,
	FgSyntaxNumber,
	FgSyntaxType,
	FgSyntaxOperator,
	FgSyntaxPunctuation,
	FgThinkingOff,
	FgThinkingMinimal,
	FgThinkingLow,
	FgThinkingMedium,
	FgThinkingHigh,
	FgThinkingXhigh,
	FgThinkingMax,
	FgBashMode,
}

// ThemeColorBgTokens lists all background color tokens in canonical order.
var ThemeColorBgTokens = []string{
	BgSelected,
	BgSearchMatch,
	BgUserMessage,
	BgCustomMessage,
	BgToolPending,
	BgToolSuccess,
	BgToolError,
}

// ThemeRequiredColorTokens lists color keys required by custom theme files.
// Optional fallback tokens: scrollbarTrack, scrollbarThumb, searchMatchBg, searchMatchText, thinkingMax.
var ThemeRequiredColorTokens = []string{
	FgAccent,
	FgBorder,
	FgBorderAccent,
	FgBorderMuted,
	FgSuccess,
	FgError,
	FgWarning,
	FgMuted,
	FgDim,
	FgText,
	FgThinkingText,
	BgSelected,
	BgUserMessage,
	FgUserMessageText,
	BgCustomMessage,
	FgCustomMessageText,
	FgCustomMessageLabel,
	BgToolPending,
	BgToolSuccess,
	BgToolError,
	FgToolTitle,
	FgToolOutput,
	FgMdHeading,
	FgMdLink,
	FgMdLinkUrl,
	FgMdCode,
	FgMdCodeBlock,
	FgMdCodeBlockBorder,
	FgMdQuote,
	FgMdQuoteBorder,
	FgMdHr,
	FgMdListBullet,
	FgToolDiffAdded,
	FgToolDiffRemoved,
	FgToolDiffContext,
	FgSyntaxComment,
	FgSyntaxKeyword,
	FgSyntaxFunction,
	FgSyntaxVariable,
	FgSyntaxString,
	FgSyntaxNumber,
	FgSyntaxType,
	FgSyntaxOperator,
	FgSyntaxPunctuation,
	FgThinkingOff,
	FgThinkingMinimal,
	FgThinkingLow,
	FgThinkingMedium,
	FgThinkingHigh,
	FgThinkingXhigh,
	FgBashMode,
}

// ThemeOptionalFallbacks maps optional tokens omitted by legacy themes to their compatible fallback token.
var ThemeOptionalFallbacks = map[string]string{
	FgScrollbarTrack:  FgMuted,
	FgScrollbarThumb:  FgText,
	BgSearchMatch:     BgSelected,
	FgSearchMatchText: FgText,
	FgThinkingMax:     FgThinkingXhigh,
}

var (
	ttokFgSet      map[string]struct{}
	ttokBgSet      map[string]struct{}
	ttokAllowedSet map[string]struct{}
)

func init() {
	ttokFgSet = make(map[string]struct{}, len(ThemeColorFgTokens))
	ttokBgSet = make(map[string]struct{}, len(ThemeColorBgTokens))
	ttokAllowedSet = make(map[string]struct{}, len(ThemeColorFgTokens)+len(ThemeColorBgTokens))
	for _, k := range ThemeColorFgTokens {
		ttokFgSet[k] = struct{}{}
		ttokAllowedSet[k] = struct{}{}
	}
	for _, k := range ThemeColorBgTokens {
		ttokBgSet[k] = struct{}{}
		ttokAllowedSet[k] = struct{}{}
	}
}

// IsFgToken reports whether name is a valid foreground theme color token.
func IsFgToken(name string) bool {
	_, ok := ttokFgSet[name]
	return ok
}

// IsBgToken reports whether name is a valid background theme color token.
func IsBgToken(name string) bool {
	_, ok := ttokBgSet[name]
	return ok
}

// IsValidColorToken reports whether name is a known foreground or background theme color token.
func IsValidColorToken(name string) bool {
	_, ok := ttokAllowedSet[name]
	return ok
}
