// Ported from packages/coding-agent/src/core/telemetry.ts (pi v1.5.2).

package core

import (
	"os"
	"strings"
)

func teleIsTruthyEnvFlag(value string) bool {
	if value == "" {
		return false
	}
	return value == "1" || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes")
}

// IsInstallTelemetryEnabled reports whether anonymous install telemetry is enabled,
// preferring an explicit PI_TELEMETRY override when present.
func IsInstallTelemetryEnabled(settingsManager *SettingsManager, telemetryEnv ...*string) bool {
	if len(telemetryEnv) > 0 {
		if telemetryEnv[0] == nil {
			return settingsManager.GetEnableInstallTelemetry()
		}
		return teleIsTruthyEnvFlag(*telemetryEnv[0])
	}
	if env, ok := os.LookupEnv("PI_TELEMETRY"); ok {
		return teleIsTruthyEnvFlag(env)
	}
	return settingsManager.GetEnableInstallTelemetry()
}
