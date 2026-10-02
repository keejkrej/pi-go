// Ported from packages/chord/src/delta/draft.ts (pi v1.0.0).

package chord

// Draft is a mutable transaction-scoped view of a JSON value, preserving tuple positions.
// TypeScript's recursive mapped type, including its depth limit of 8, has no runtime
// representation. Go rejects `type Draft[T any] = T`, so Value holds that root.
type Draft[T any] struct {
	Value T
}
