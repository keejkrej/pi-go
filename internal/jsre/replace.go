package jsre

import "strings"

// collect runs the RegExp.prototype[Symbol.replace] match loop: all matches
// with the g flag, otherwise the first one (from index 0).
func (r *Regexp) collect(t *subject) []*Match {
	if !r.global {
		if m := r.matchAt(t, 0); m != nil {
			return []*Match{m}
		}
		return nil
	}
	var out []*Match
	for pos := 0; pos <= len(t.runes); {
		m := r.matchAt(t, pos)
		if m == nil {
			break
		}
		out = append(out, m)
		if m.endRune == m.startRune {
			pos = t.advance(m.endRune)
		} else {
			pos = m.endRune
		}
	}
	return out
}

func (r *Regexp) replace(s string, sub func(t *subject, m *Match) string) string {
	t := r.subject(s)
	matches := r.collect(t)
	if len(matches) == 0 {
		return s
	}
	var sb strings.Builder
	next := 0
	for _, m := range matches {
		if m.startRune < next {
			continue
		}
		sb.WriteString(t.slice(next, m.startRune))
		sb.WriteString(sub(t, m))
		next = m.endRune
	}
	sb.WriteString(t.slice(next, len(t.runes)))
	return sb.String()
}

// Replace implements String.prototype.replace (and replaceAll for a global
// regexp) with a replacement template. The template supports $$, $&, $`,
// $', $n, $nn and $<name> exactly as V8's GetSubstitution does.
func (r *Regexp) Replace(s, repl string) string {
	if !strings.Contains(repl, "$") {
		return r.replace(s, func(*subject, *Match) string { return repl })
	}
	return r.replace(s, func(t *subject, m *Match) string { return r.substitution(t, m, repl) })
}

// ReplaceFunc is Replace with a replacement function, like passing a
// function to String.prototype.replace.
func (r *Regexp) ReplaceFunc(s string, f func(*Match) string) string {
	return r.replace(s, func(_ *subject, m *Match) string { return f(m) })
}

// substitution implements V8's String::GetSubstitution.
func (r *Regexp) substitution(t *subject, m *Match, repl string) string {
	var sb strings.Builder
	capturesLength := r.captures + 1
	i := 0
	for i < len(repl) {
		c := repl[i]
		if c != '$' || i+1 >= len(repl) {
			sb.WriteByte(c)
			i++
			continue
		}
		peekIx := i + 1
		peek := repl[peekIx]
		switch {
		case peek == '$':
			sb.WriteByte('$')
			i = peekIx + 1
		case peek == '&':
			sb.WriteString(m.values[0])
			i = peekIx + 1
		case peek == '`':
			sb.WriteString(t.slice(0, m.startRune))
			i = peekIx + 1
		case peek == '\'':
			sb.WriteString(t.slice(m.endRune, len(t.runes)))
			i = peekIx + 1
		case peek >= '0' && peek <= '9':
			// Valid indices are $1 .. $9, $01 .. $09 and $10 .. $99.
			index := int(peek - '0')
			advance := 1
			if peekIx+1 < len(repl) {
				if next := repl[peekIx+1]; next >= '0' && next <= '9' {
					if two := index*10 + int(next-'0'); two < capturesLength {
						index = two
						advance = 2
					}
				}
			}
			if index == 0 || index >= capturesLength {
				sb.WriteByte('$')
				i = peekIx
				break
			}
			sb.WriteString(m.values[index])
			i = peekIx + advance
		case peek == '<':
			if len(r.order) == 0 {
				sb.WriteByte('$')
				i = peekIx
				break
			}
			end := strings.IndexByte(repl[peekIx+1:], '>')
			if end < 0 {
				sb.WriteByte('$')
				i = peekIx
				break
			}
			name := repl[peekIx+1 : peekIx+1+end]
			if v, ok := m.NamedOK(name); ok {
				sb.WriteString(v)
			}
			i = peekIx + 1 + end + 1
		default:
			sb.WriteByte('$')
			i = peekIx
		}
	}
	return sb.String()
}

// Split implements String.prototype.split with a regexp separator
// (RegExp.prototype[Symbol.split]). A negative limit means no limit.
// Captures are spliced into the result; captures that did not participate
// (undefined in JS) are "".
func (r *Regexp) Split(s string, limit int) []string {
	out := []string{}
	if limit == 0 {
		return out
	}
	t := r.subject(s)
	size := len(t.runes)
	full := func() bool { return limit > 0 && len(out) >= limit }
	if size == 0 {
		if r.matchAt(t, 0) != nil {
			return out
		}
		return append(out, s)
	}
	p := 0
	q := 0
	for q < size {
		m := r.matchAt(t, q)
		if r.sticky {
			// The splitter is sticky in JS too: try each position in turn.
			if m == nil {
				q = t.advance(q)
				continue
			}
		} else {
			// A forward search finds the first position where the sticky
			// splitter would match, and the same match.
			if m == nil {
				break
			}
			q = m.startRune
			if q >= size {
				break
			}
		}
		e := min(m.endRune, size)
		if e == p {
			q = t.advance(q)
			continue
		}
		out = append(out, t.slice(p, q))
		if full() {
			return out
		}
		p = e
		for i := 1; i <= r.captures; i++ {
			out = append(out, m.values[i])
			if full() {
				return out
			}
		}
		q = p
	}
	return append(out, t.slice(p, size))
}
