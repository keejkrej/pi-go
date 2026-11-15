// Ported from packages/coding-agent/src/core/client-identity.ts (pi v1.5.2).

package core

import (
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// GetClientIdentity returns how pi's requests identify it to providers: a detailed User-Agent with
// pi's version, and attribution to pi as a CLI agent while install telemetry is enabled.
func GetClientIdentity(settingsManager *SettingsManager) ai.ClientIdentity {
	attribution := jsonx.Null[ai.ClientAttribution]()
	if IsInstallTelemetryEnabled(settingsManager) {
		attribution = jsonx.Some(ai.PiAttribution)
	}
	version := utils.VERSION
	return ai.ClientIdentity{
		UserAgent:   ai.UserAgentModeValue{Val: ai.UserAgentModeDetailed},
		Attribution: attribution,
		Version:     &version,
	}
}
