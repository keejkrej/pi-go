// Ported from packages/coding-agent/src/extensions/index.ts (pi v1.5.2).

package extensions

import (
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/extensions/codemodeext"
	"github.com/keejkrej/pi-go/codingagent/extensions/llamaext"
	"github.com/keejkrej/pi-go/codingagent/extensions/mcpext"
	"github.com/keejkrej/pi-go/codingagent/extensions/toolsearchext"
)

// BuiltInInlineExtensions defines all built-in coding-agent extensions paired with
// their canonical names and replaceability flags.
var BuiltInInlineExtensions = []core.InlineExtension{
	{
		Name:    "llama.cpp",
		Factory: llamaext.Extension,
		Builtin: true,
	},
	{
		Name:        "codemode",
		Factory:     codemodeext.Extension,
		Builtin:     true,
		Replaceable: true,
	},
	{
		Name:        "tool-search",
		Factory:     toolsearchext.Extension,
		Builtin:     true,
		Replaceable: true,
	},
	{
		Name:        "mcp",
		Factory:     mcpext.Extension,
		Builtin:     true,
		Replaceable: true,
	},
}

// BuiltInExtensions lists built-in extension factories registered at startup.
var BuiltInExtensions = []core.ExtensionFactory{
	llamaext.Extension,
	codemodeext.Extension,
	toolsearchext.Extension,
	mcpext.Extension,
}

func init() {
	core.SetDefaultBuiltinExtensions(BuiltInInlineExtensions)
}
