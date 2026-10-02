// Ported from packages/tui/src/kill-ring.ts (pi v1.0.0).

package tui

// KillRing is an Emacs-style kill/yank ring.
// Consecutive kills can accumulate into the most recent entry.
type KillRing struct {
	ring []string
}

// NewKillRing returns an empty ring.
func NewKillRing() *KillRing {
	return &KillRing{ring: []string{}}
}

// KillRingPushOptions controls KillRing.Push.
// Accumulate nil or false creates a new entry. When accumulating, Prepend inserts
// text in front of the previous entry (backward delete) and false appends it.
type KillRingPushOptions struct {
	Prepend    bool  `json:"prepend"`
	Accumulate *bool `json:"accumulate,omitzero"`
}

// Push adds text to the ring. Empty text is ignored.
// opts is the required TS options object; nil is not a valid call.
func (k *KillRing) Push(text string, opts *KillRingPushOptions) {
	panic("unported: KillRing.Push")
}

// Peek returns the most recent entry without changing the ring.
// Nil means the ring is empty.
func (k *KillRing) Peek() *string {
	panic("unported: KillRing.Peek")
}

// Rotate moves the most recent entry to the front for yank-pop.
func (k *KillRing) Rotate() {
	panic("unported: KillRing.Rotate")
}

// Length returns the number of entries.
func (k *KillRing) Length() int {
	panic("unported: KillRing.Length")
}
