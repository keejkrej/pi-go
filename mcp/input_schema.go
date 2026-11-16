// Ported from packages/mcp/src/input-schema.ts (pi v1.5.2).

package mcp

import "github.com/keejkrej/pi-go/internal/jsonx"

// NormalizeMcpInputSchema returns a shallow copy of schema with top-level "type"
// defaulting to "object" (when missing or null) and "properties" defaulting to an
// empty JSON object (when missing), matching Anthropic/OpenAI tool schema rules.
func NormalizeMcpInputSchema(schema *jsonx.Object) *jsonx.Object {
	var out *jsonx.Object
	if schema == nil {
		out = jsonx.NewObject()
	} else {
		out = schema.Clone()
	}
	if typ, ok := out.Get("type"); !ok || typ == nil {
		out.Set("type", "object")
	}
	if !out.Has("properties") {
		out.Set("properties", jsonx.NewObject())
	}
	return out
}
