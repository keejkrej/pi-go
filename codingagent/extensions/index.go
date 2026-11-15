// Ported from packages/coding-agent/src/extensions/index.ts (pi v1.5.2 barebone).

package extensions

import "github.com/keejkrej/pi-go/codingagent/core"

// BuiltInExtensions lists built-in extension factories registered at startup.
// In barebone builds it stays nil with zero imports of mcpext, codemodeext,
// llamaext, or toolsearchext.
var BuiltInExtensions []core.ExtensionFactory = nil
