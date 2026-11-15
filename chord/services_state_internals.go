// Ported from packages/chord/src/services/state-internals.ts (pi v1.0.0).

package chord

import (
	"reflect"
	"sync"
)

// ReplicatedStateInternals is the publication surface hidden on a replicated state value.
type ReplicatedStateInternals interface {
	// Snapshot atomically captures the immutable value and its matching publication sequence.
	Snapshot() (value any, sequence int)
	Subscribe(listener func(ops OpList, sequence int, call Context) error) func()
}

var (
	ssiSourcesMu sync.Mutex
	ssiSources   = map[any]ReplicatedStateInternals{}
)

func ssiComparableKey(value any) bool {
	if value == nil {
		return false
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || !rv.Type().Comparable() {
		return false
	}
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return false
	}
	return true
}

// RegisterReplicatedStateInternals records internals for a state object.
func RegisterReplicatedStateInternals(value any, internals ReplicatedStateInternals) {
	if !ssiComparableKey(value) {
		return
	}
	ssiSourcesMu.Lock()
	ssiSources[value] = internals
	ssiSourcesMu.Unlock()
}

// GetReplicatedStateInternals returns the internals registered for value.
// A nil result means the value is not a replicated state (TS undefined).
func GetReplicatedStateInternals(value any) ReplicatedStateInternals {
	if !ssiComparableKey(value) {
		return nil
	}
	ssiSourcesMu.Lock()
	internals := ssiSources[value]
	ssiSourcesMu.Unlock()
	return internals
}
