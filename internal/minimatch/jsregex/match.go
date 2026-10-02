package jsregex

// matcher runs one match attempt with backtracking. Every node match takes a
// continuation k that receives the end position; a node returns true as soon
// as some way of matching it lets k succeed (ECMA-262 Matcher semantics).
type matcher struct {
	re   *Regexp
	in   []uint16
	caps []int // 2*(ncap+1) entries: start, end; -1 when not participating
}

// read returns the character at i and its width in units (0 at the end).
func (m *matcher) read(i int) (rune, int) {
	if i >= len(m.in) {
		return 0, 0
	}
	c := rune(m.in[i])
	if m.re.unicode && c >= 0xD800 && c <= 0xDBFF && i+1 < len(m.in) {
		d := rune(m.in[i+1])
		if d >= 0xDC00 && d <= 0xDFFF {
			return 0x10000 + (c-0xD800)<<10 + (d - 0xDC00), 2
		}
	}
	return c, 1
}

// readBack returns the character ending at i (for \b and multiline ^).
func (m *matcher) readBack(i int) (rune, bool) {
	if i <= 0 {
		return 0, false
	}
	c := rune(m.in[i-1])
	if m.re.unicode && c >= 0xDC00 && c <= 0xDFFF && i >= 2 {
		h := rune(m.in[i-2])
		if h >= 0xD800 && h <= 0xDBFF {
			return 0x10000 + (h-0xD800)<<10 + (c - 0xDC00), true
		}
	}
	return c, true
}

func (m *matcher) canon(c rune) rune {
	if m.re.unicode {
		return canonFold(c)
	}
	return canonUnit(c)
}

func (cls *nClass) has(c rune) bool {
	for i := range cls.items {
		it := &cls.items[i]
		if it.pred != nil {
			if it.pred(c) {
				return true
			}
		} else if c >= it.lo && c <= it.hi {
			return true
		}
	}
	return false
}

func (m *matcher) classMatches(cls *nClass, c rune) bool {
	var found bool
	if m.re.ignoreCase {
		found = equivalents(c, m.re.unicode, cls.has)
	} else {
		found = cls.has(c)
	}
	return found != cls.neg
}

func (m *matcher) isWordAt(i int) bool {
	if i < 0 || i >= len(m.in) {
		return false
	}
	c := rune(m.in[i])
	if m.re.unicode && m.re.ignoreCase {
		return isWordUI(c)
	}
	return isWord(c)
}

func (m *matcher) saveCaps(lo, hi int) []int {
	if hi <= lo {
		return nil
	}
	s := make([]int, 2*(hi-lo))
	copy(s, m.caps[2*(lo+1):2*(hi+1)])
	return s
}

func (m *matcher) restoreCaps(lo int, s []int) {
	copy(m.caps[2*(lo+1):], s)
}

func (m *matcher) clearCaps(lo, hi int) {
	for j := 2 * (lo + 1); j < 2*(hi+1); j++ {
		m.caps[j] = -1
	}
}

func (m *matcher) match(n node, i int, k func(int) bool) bool {
	switch n := n.(type) {
	case *nChar:
		c, w := m.read(i)
		if w == 0 {
			return false
		}
		if c != n.c && (!m.re.ignoreCase || m.canon(c) != n.canon) {
			return false
		}
		return k(i + w)
	case *nClass:
		c, w := m.read(i)
		if w == 0 || !m.classMatches(n, c) {
			return false
		}
		return k(i + w)
	case *nSeq:
		return m.seq(n.items, 0, i, k)
	case *nAlt:
		for _, a := range n.alts {
			if m.match(a, i, k) {
				return true
			}
		}
		return false
	case *nGroup:
		return m.match(n.sub, i, func(j int) bool {
			o0, o1 := m.caps[2*n.idx], m.caps[2*n.idx+1]
			m.caps[2*n.idx], m.caps[2*n.idx+1] = i, j
			if k(j) {
				return true
			}
			m.caps[2*n.idx], m.caps[2*n.idx+1] = o0, o1
			return false
		})
	case *nRepeat:
		return m.repeat(n, 0, i, k)
	case *nLook:
		saved := m.saveCaps(n.capLo, n.capHi)
		matched := m.match(n.sub, i, func(int) bool { return true })
		if n.neg {
			m.restoreCaps(n.capLo, saved)
			if matched {
				return false
			}
			return k(i)
		}
		if !matched {
			m.restoreCaps(n.capLo, saved)
			return false
		}
		if k(i) {
			return true
		}
		m.restoreCaps(n.capLo, saved)
		return false
	case *nBackref:
		s, e := m.caps[2*n.idx], m.caps[2*n.idx+1]
		if s < 0 || e < 0 {
			return k(i)
		}
		ln := e - s
		if i+ln > len(m.in) {
			return false
		}
		for j := 0; j < ln; j++ {
			a, b := rune(m.in[s+j]), rune(m.in[i+j])
			if a != b && (!m.re.ignoreCase || m.canon(a) != m.canon(b)) {
				return false
			}
		}
		return k(i + ln)
	case *nAssert:
		switch n.kind {
		case '^':
			if i == 0 {
				return k(i)
			}
			if m.re.multiline {
				if c, ok := m.readBack(i); ok && !isNotLineTerminator(c) {
					return k(i)
				}
			}
			return false
		case '$':
			if i == len(m.in) {
				return k(i)
			}
			if m.re.multiline && !isNotLineTerminator(rune(m.in[i])) {
				return k(i)
			}
			return false
		case 'b', 'B':
			a := m.isWordAt(i - 1)
			b := m.isWordAt(i)
			if (a != b) == (n.kind == 'b') {
				return k(i)
			}
			return false
		}
	case nil:
		return k(i)
	}
	return false
}

func (m *matcher) seq(items []node, idx, i int, k func(int) bool) bool {
	if idx == len(items) {
		return k(i)
	}
	if idx == len(items)-1 {
		return m.match(items[idx], i, k)
	}
	return m.match(items[idx], i, func(j int) bool { return m.seq(items, idx+1, j, k) })
}

// repeat implements RepeatMatcher: count iterations done so far.
func (m *matcher) repeat(n *nRepeat, count, i int, k func(int) bool) bool {
	if n.max >= 0 && count >= n.max {
		return k(i)
	}
	iterate := func() bool {
		saved := m.saveCaps(n.capLo, n.capHi)
		m.clearCaps(n.capLo, n.capHi)
		ok := m.match(n.sub, i, func(j int) bool {
			if j == i && count >= n.min {
				return false
			}
			return m.repeat(n, count+1, j, k)
		})
		if !ok {
			m.restoreCaps(n.capLo, saved)
		}
		return ok
	}
	if count < n.min {
		return iterate()
	}
	if n.greedy {
		if iterate() {
			return true
		}
		return k(i)
	}
	if k(i) {
		return true
	}
	return iterate()
}
