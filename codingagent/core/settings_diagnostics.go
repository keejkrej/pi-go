// Ported from packages/coding-agent/src/core/settings-diagnostics.ts (pi v1.5.2).

package core

import "fmt"

// CollectSettingsDiagnostics drains accumulated errors from settingsManager into runtime warning diagnostics.
func CollectSettingsDiagnostics(settingsManager *SettingsManager) []AgentSessionRuntimeDiagnostic {
	errs := settingsManager.DrainErrors()
	out := make([]AgentSessionRuntimeDiagnostic, 0, len(errs))
	for _, se := range errs {
		var msg string
		if se.Path != nil && *se.Path != "" {
			msg = fmt.Sprintf("Invalid settings file %s: %s", *se.Path, se.Error.Error())
		} else {
			msg = fmt.Sprintf("Invalid %s settings: %s", se.Scope, se.Error.Error())
		}
		out = append(out, AgentSessionRuntimeDiagnostic{
			Type:    "warning",
			Message: msg,
		})
	}
	return out
}

// DeduplicateDiagnostics removes duplicate type/message diagnostics while preserving first-seen order.
func DeduplicateDiagnostics(diagnostics []AgentSessionRuntimeDiagnostic) []AgentSessionRuntimeDiagnostic {
	seen := make(map[string]struct{}, len(diagnostics))
	out := make([]AgentSessionRuntimeDiagnostic, 0, len(diagnostics))
	for _, d := range diagnostics {
		key := d.Type + "\x00" + d.Message
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, d)
	}
	return out
}
