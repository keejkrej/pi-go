// Ported from packages/chord/src/json.ts (pi v1.0.0).

package chord

// CopyJsonOptions controls strict-JSON copying.
type CopyJsonOptions struct {
	// OmitUndefinedProperties omits undefined object properties while preserving strict array semantics.
	OmitUndefinedProperties *bool `json:"omitUndefinedProperties,omitzero"`
}

// CopyJson copies a value into an alias-free strict-JSON tree owned by the caller.
// A nil options pointer means the defaults.
func CopyJson(value any, options *CopyJsonOptions) (JsonValue, error) {
	panic("unported: CopyJson")
}

// IsJsonValue reports whether value is finite strict JSON with plain objects and no cycles.
func IsJsonValue(value any) bool {
	panic("unported: IsJsonValue")
}
