// Ported from packages/ai/src/utils/typebox-helpers.ts (pi v1.0.0).

package ai

import "github.com/keejkrej/pi-go/internal/typebox"

// StringEnumOptions is the optional metadata for StringEnum.
// Nil options mean no description and no default.
// A nil field is omitted. An empty string is also omitted: the schema
// includes the field only when the string is truthy.
type StringEnumOptions struct {
	Description *string `json:"description,omitzero"`
	Default     *string `json:"default,omitzero"`
}

// StringEnum creates a string enum schema compatible with Google's API and
// other providers that don't support anyOf/const patterns.
// The result is Type.Unsafe. Enumerable keys are type, enum (values in order),
// then description and default when those strings are non-empty.
func StringEnum(values []string, options *StringEnumOptions) *typebox.Schema {
	panic("unported: StringEnum")
}
