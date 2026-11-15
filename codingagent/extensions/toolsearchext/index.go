// Ported from packages/coding-agent/src/extensions/tool-search/index.ts (pi v1.5.2).

package toolsearchext

import "github.com/keejkrej/pi-go/codingagent/core"

func indexBoolPtr(v bool) *bool { return &v }

// CreateToolSearchExtension builds the extension factory that registers the tool_search tool
// (inactive by default).
func CreateToolSearchExtension() core.ExtensionFactory {
	return func(pi core.ExtensionAPI) error {
		def := CreateToolSearchToolDefinition(&ToolSearchToolOptions{Tools: pi})
		def.DefaultActive = indexBoolPtr(false)
		pi.RegisterTool(def)
		return nil
	}
}

// Extension is the default built-in tool_search extension entrypoint.
func Extension(pi core.ExtensionAPI) error {
	return CreateToolSearchExtension()(pi)
}
