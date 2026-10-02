// Ported from packages/chord/src/delta/revision-validator.ts (pi v1.0.0).

package chord

import "sync"

// JsonRevisionValidator validates immutable replica revisions, skipping containers
// validated in earlier revisions. The validated-container set is a TS WeakSet;
// the implementation owns that storage. The validator is safe for shared use.
type JsonRevisionValidator struct {
	mu sync.Mutex
}

// NewJsonRevisionValidator returns an empty validator.
func NewJsonRevisionValidator() *JsonRevisionValidator {
	panic("unported: NewJsonRevisionValidator")
}

// Validate checks value as strict JSON without cycles and returns it unchanged.
func (v *JsonRevisionValidator) Validate(value JsonValue) (JsonValue, error) {
	panic("unported: JsonRevisionValidator.Validate")
}
