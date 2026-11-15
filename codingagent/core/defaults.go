// Ported from packages/coding-agent/src/core/defaults.ts (pi v1.5.2).

package core

import "github.com/keejkrej/pi-go/ai"

// DefaultThinkingLevel is the default reasoning effort level when none is configured.
const DefaultThinkingLevel ai.ModelThinkingLevel = ai.ModelThinkingLevelMedium

// ThinkingLevelOptions lists all supported session thinking levels in UI order.
var ThinkingLevelOptions = []ai.ModelThinkingLevel{
	ai.ModelThinkingLevelOff,
	ai.ModelThinkingLevelMinimal,
	ai.ModelThinkingLevelLow,
	ai.ModelThinkingLevelMedium,
	ai.ModelThinkingLevelHigh,
	ai.ModelThinkingLevelXhigh,
	ai.ModelThinkingLevelMax,
}
