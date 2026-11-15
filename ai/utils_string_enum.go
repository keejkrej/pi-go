// Ported from packages/ai/src/utils/string-enum.ts (pi v1.5.2).

package ai

import (
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// StringEnumOptions is the optional metadata for StringEnum.
// Nil options mean no description and no default.
// A nil field is omitted. An empty string is also omitted: the schema
// includes the field only when the string is truthy.
type StringEnumOptions struct {
	Description *string `json:"description,omitzero"`
	Default     *string `json:"default,omitzero"`
}

// StringEnum creates a string enum schema compatible with Google's API and
// other providers that don't support anyOf/const patterns:
// `{ "type": "string", "enum": [...] }`.
func StringEnum(values []string, options *StringEnumOptions) *typebox.Schema {
	enumValues := make([]any, len(values))
	for i, v := range values {
		enumValues[i] = v
	}
	raw := jsonx.ObjectOf("type", "string", "enum", enumValues)
	if options != nil {
		if options.Description != nil && *options.Description != "" {
			raw.Set("description", *options.Description)
		}
		if options.Default != nil && *options.Default != "" {
			raw.Set("default", *options.Default)
		}
	}
	return typebox.Unsafe(raw)
}
