// Ported from packages/coding-agent/src/core/tools/renderers/index.ts (pi v1.5.2).

package core

// CreateAllToolRenderers returns the built-in renderer set for every built-in tool, keyed by tool name.
func CreateAllToolRenderers() map[ToolName]ToolRenderers {
	return map[ToolName]ToolRenderers{
		ToolNameRead:       ReadToolRenderers(),
		ToolNameBash:       CreateShellRenderers("$"),
		ToolNamePowershell: CreateShellRenderers("PS>"),
		ToolNameEdit:       EditToolRenderers(),
		ToolNameWrite:      WriteToolRenderers(),
		ToolNameGrep:       GrepToolRenderers(),
		ToolNameFind:       FindToolRenderers(),
		ToolNameLs:         LsToolRenderers(),
	}
}

// WithBuiltInRenderers merges built-in renderers into definition when RenderCall or RenderResult is unset.
func WithBuiltInRenderers(toolName string, definition *ToolRenderers) *ToolRenderers {
	all := CreateAllToolRenderers()
	builtIn, hasBuiltIn := all[ToolName(toolName)]
	if definition == nil {
		if !hasBuiltIn {
			return nil
		}
		cp := builtIn
		return &cp
	}
	if !hasBuiltIn {
		cp := *definition
		return &cp
	}
	merged := *definition
	if merged.RenderCall == nil {
		merged.RenderCall = builtIn.RenderCall
	}
	if merged.RenderResult == nil {
		merged.RenderResult = builtIn.RenderResult
	}
	return &merged
}
