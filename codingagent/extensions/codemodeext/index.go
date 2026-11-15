// Ported from packages/coding-agent/src/extensions/codemode/index.ts (pi v1.5.2).

package codemodeext

import (
	"math"

	"github.com/keejkrej/pi-go/codingagent/core"
)

// CodemodeExtensionOptions configures the codemode built-in/SDK extension.
type CodemodeExtensionOptions struct {
	Mode         *core.CodemodeMode
	InlineBudget *float64
	Models       *bool
}

func indexReadMode(pi core.ExtensionAPI) core.CodemodeMode {
	if cm := pi.GetSettings().Codemode; cm != nil && cm.Mode != nil && *cm.Mode == core.CodemodeModeOnly {
		return core.CodemodeModeOnly
	}
	return core.SettingsDefaults.Codemode.Mode
}

func indexReadInlineBudget(pi core.ExtensionAPI) *float64 {
	cm := pi.GetSettings().Codemode
	if cm == nil || cm.InlineBudget == nil {
		return nil
	}
	b := *cm.InlineBudget
	if math.IsNaN(b) || math.IsInf(b, 0) || b < 0 {
		return nil
	}
	return &b
}

// CreateCodemodeExtension constructs the ExtensionFactory registering the codemode tool (inactive by default).
func CreateCodemodeExtension(options ...*CodemodeExtensionOptions) core.ExtensionFactory {
	var opts *CodemodeExtensionOptions
	if len(options) > 0 {
		opts = options[0]
	}

	return func(pi core.ExtensionAPI) error {
		modelsEnabled := true
		if opts != nil && opts.Models != nil {
			modelsEnabled = *opts.Models
		}
		defActive := false

		def := CreateCodemodeToolDefinition(&CodemodeToolOptions{
			AppendEntry: func(customType string, data CodemodeStoreEntryData) {
				pi.AppendEntry(customType, data)
			},
			Models: &modelsEnabled,
			GetToolNamespace: func(name string) *core.ToolNamespace {
				for _, tool := range pi.GetAllTools() {
					if tool.Name == name {
						return tool.Namespace
					}
				}
				return nil
			},
			GetToolGuidelines: func() map[string][]string {
				all := pi.GetAllTools()
				out := make(map[string][]string, len(all))
				for _, tool := range all {
					out[tool.Name] = tool.PromptGuidelines
				}
				return out
			},
			GetMode: func() core.CodemodeMode {
				if opts != nil && opts.Mode != nil {
					return *opts.Mode
				}
				return indexReadMode(pi)
			},
			GetInlineBudget: func() *float64 {
				if opts != nil && opts.InlineBudget != nil {
					return opts.InlineBudget
				}
				return indexReadInlineBudget(pi)
			},
		})
		def.DefaultActive = &defActive
		pi.RegisterTool(def)
		return nil
	}
}

// Extension initializes the codemode built-in extension with default options.
func Extension(pi core.ExtensionAPI) error {
	return CreateCodemodeExtension()(pi)
}
