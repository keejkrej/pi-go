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
	if text == "" {
		return
	}
	accumulate := opts != nil && opts.Accumulate != nil && *opts.Accumulate
	if accumulate && len(k.ring) > 0 {
		last := k.ring[len(k.ring)-1]
		k.ring = k.ring[:len(k.ring)-1]
		if opts.Prepend {
			k.ring = append(k.ring, text+last)
		} else {
			k.ring = append(k.ring, last+text)
		}
	} else {
		k.ring = append(k.ring, text)
	}
}

// Peek returns the most recent entry without changing the ring.
// Nil means the ring is empty.
func (k *KillRing) Peek() *string {
	if len(k.ring) == 0 {
		return nil
	}
	v := k.ring[len(k.ring)-1]
	return &v
}

// Rotate moves the most recent entry to the front for yank-pop.
func (k *KillRing) Rotate() {
	if len(k.ring) > 1 {
		last := k.ring[len(k.ring)-1]
		copy(k.ring[1:], k.ring[:len(k.ring)-1])
		k.ring[0] = last
	}
}

// Length returns the number of entries.
func (k *KillRing) Length() int {
	return len(k.ring)
}
