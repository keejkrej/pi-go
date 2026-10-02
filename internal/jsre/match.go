package jsre

import "github.com/dlclark/regexp2/v2"

// Match is the result of a successful match, like the array returned by
// RegExp.prototype.exec. Group 0 is the whole match; groups that did not
// participate are undefined (reported as "" and ok == false).
type Match struct {
	// Input is the subject string.
	Input string
	// Index is the UTF-16 index of the start of the match.
	Index int

	re        *Regexp
	startRune int
	endRune   int
	end       int
	starts    []int // UTF-16 start per group, -1 if undefined
	ends      []int
	values    []string
}

func (r *Regexp) newMatch(t *subject, m *regexp2.Match) *Match {
	n := r.captures + 1
	res := &Match{
		Input:  t.s,
		re:     r,
		starts: make([]int, n),
		ends:   make([]int, n),
		values: make([]string, n),
	}
	for i := range n {
		g := m.GroupByNumber(i)
		if g == nil || len(g.Captures) == 0 {
			res.starts[i], res.ends[i] = -1, -1
			continue
		}
		a, b := g.RuneIndex, g.RuneIndex+g.RuneLength
		if i == 0 {
			res.startRune, res.endRune = a, b
		}
		res.starts[i], res.ends[i] = t.index16(a), t.index16(b)
		res.values[i] = t.slice(a, b)
	}
	res.Index = res.starts[0]
	res.end = res.ends[0]
	return res
}

// String returns the matched text (group 0).
func (m *Match) String() string { return m.values[0] }

// End returns the UTF-16 index just past the match (the new lastIndex).
func (m *Match) End() int { return m.end }

// NumGroups returns the number of entries, including group 0 (JS m.length).
func (m *Match) NumGroups() int { return len(m.values) }

// Group returns group i, or "" if it is undefined or out of range.
func (m *Match) Group(i int) string {
	if i < 0 || i >= len(m.values) {
		return ""
	}
	return m.values[i]
}

// GroupOK returns group i and whether it participated in the match.
func (m *Match) GroupOK(i int) (string, bool) {
	if i < 0 || i >= len(m.values) || m.starts[i] < 0 {
		return "", false
	}
	return m.values[i], true
}

// GroupIndex returns the UTF-16 start and end of group i (the d flag's
// indices), or -1, -1 if it is undefined or out of range.
func (m *Match) GroupIndex(i int) (start, end int) {
	if i < 0 || i >= len(m.values) {
		return -1, -1
	}
	return m.starts[i], m.ends[i]
}

// Captures returns all groups as pointers; undefined groups are nil.
func (m *Match) Captures() []*string {
	out := make([]*string, len(m.values))
	for i := range m.values {
		if m.starts[i] >= 0 {
			v := m.values[i]
			out[i] = &v
		}
	}
	return out
}

func (m *Match) namedIndex(name string) int {
	// With duplicate names, at most one of the groups participates.
	found := -1
	for i, n := range m.re.names {
		if n != name {
			continue
		}
		if m.starts[i+1] >= 0 {
			return i + 1
		}
		found = i + 1
	}
	return found
}

// NamedOK returns the named group's value and whether it participated.
func (m *Match) NamedOK(name string) (string, bool) {
	i := m.namedIndex(name)
	if i < 0 {
		return "", false
	}
	return m.GroupOK(i)
}

// Named returns the named group's value, or "" if it is undefined.
func (m *Match) Named(name string) string {
	v, _ := m.NamedOK(name)
	return v
}

// NamedGroups returns the groups object (name to value, nil for undefined),
// or nil when the pattern has no named groups. GroupNames on the Regexp
// gives the key order.
func (m *Match) NamedGroups() map[string]*string {
	if len(m.re.order) == 0 {
		return nil
	}
	out := make(map[string]*string, len(m.re.order))
	for _, name := range m.re.order {
		if v, ok := m.NamedOK(name); ok {
			out[name] = &v
		} else {
			out[name] = nil
		}
	}
	return out
}
