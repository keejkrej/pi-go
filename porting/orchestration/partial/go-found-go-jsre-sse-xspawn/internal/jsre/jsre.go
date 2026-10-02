package jsre

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf16"

	"github.com/dlclark/regexp2/v2"
)

// ErrUnicodeSets is returned by Compile for the v flag, which is valid in JS
// but not implemented by this package.
var ErrUnicodeSets = errors.New("jsre: the v (unicodeSets) flag is not supported")

// Regexp is a compiled JS regular expression. It is safe for concurrent use.
// Unlike a JS RegExp it has no lastIndex state: Exec takes the start index
// explicitly, and Test, Replace, Split and the other helpers behave as if
// lastIndex were 0.
type Regexp struct {
	source     string
	flags      string
	hasIndices bool
	global     bool
	ignoreCase bool
	multiline  bool
	dotAll     bool
	unicode    bool
	sticky     bool

	parsed   parseResult
	captures int
	names    []string // capture index-1 -> name ("" if unnamed)
	order    []string // distinct group names in pattern order

	program func() *regexp2.Regexp
	last    atomic.Pointer[subject]
}

// Compile parses a JS regular expression with the given flags (any of
// "dgimsuy"; "v" is rejected with ErrUnicodeSets). Syntax errors are
// *SyntaxError values carrying V8's exact message.
func Compile(pattern, flags string) (*Regexp, error) {
	r := &Regexp{source: pattern}
	var seen [128]bool
	for _, c := range flags {
		if c >= 128 || seen[c] {
			return nil, invalidFlagsError(flags)
		}
		seen[c] = true
		switch c {
		case 'd':
			r.hasIndices = true
		case 'g':
			r.global = true
		case 'i':
			r.ignoreCase = true
		case 'm':
			r.multiline = true
		case 's':
			r.dotAll = true
		case 'u':
			r.unicode = true
		case 'v':
		case 'y':
			r.sticky = true
		default:
			return nil, invalidFlagsError(flags)
		}
	}
	if seen['u'] && seen['v'] {
		return nil, invalidFlagsError(flags)
	}
	var canonical strings.Builder
	for _, c := range "dgimsuvy" {
		if seen[c] {
			canonical.WriteRune(c)
		}
	}
	r.flags = canonical.String()

	var mods modFlags
	if r.ignoreCase {
		mods |= flagIgnoreCase
	}
	if r.multiline {
		mods |= flagMultiline
	}
	if r.dotAll {
		mods |= flagDotAll
	}
	if seen['v'] {
		return nil, ErrUnicodeSets
	}
	res, code, failed := parsePattern(utf16.Encode([]rune(pattern)), r.unicode, mods)
	if failed {
		return nil, patternError(pattern, r.flags, code)
	}
	r.parsed = res
	r.captures = res.captures
	r.names = res.names
	seenName := make(map[string]bool)
	for _, n := range res.names {
		if n != "" && !seenName[n] {
			seenName[n] = true
			r.order = append(r.order, n)
		}
	}
	r.program = sync.OnceValue(func() *regexp2.Regexp {
		prog := emitProgram(r.parsed, r.unicode, r.sticky)
		re, err := regexp2.Compile(prog, regexp2.OptionMaxBacktrackingStackSize(-1))
		if err != nil {
			panic("jsre: internal error translating /" + r.source + "/" + r.flags + ": " + err.Error())
		}
		return re
	})
	return r, nil
}

// MustCompile is like Compile but panics on error.
func MustCompile(pattern, flags string) *Regexp {
	r, err := Compile(pattern, flags)
	if err != nil {
		panic("jsre: Compile(" + pattern + ", " + flags + "): " + err.Error())
	}
	return r
}

// Source returns the RegExp.prototype.source text: the pattern with
// unescaped '/' and line terminators escaped, or "(?:)" when empty.
func (r *Regexp) Source() string {
	if r.source == "" {
		return "(?:)"
	}
	src := []rune(r.source)
	var sb strings.Builder
	inClass := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\\' && i+1 < len(src) && isLineTerminator(src[i+1]):
			// The next character is escaped on its own.
			continue
		case c == '\\':
			sb.WriteRune(c)
			i++
			if i < len(src) {
				sb.WriteRune(src[i])
			}
			continue
		case c == '/' && !inClass:
			sb.WriteByte('\\')
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '\n':
			sb.WriteString(`\n`)
			continue
		case c == '\r':
			sb.WriteString(`\r`)
			continue
		case c == 0x2028:
			sb.WriteString(`\u2028`)
			continue
		case c == 0x2029:
			sb.WriteString(`\u2029`)
			continue
		}
		sb.WriteRune(c)
	}
	return sb.String()
}

func isLineTerminator(c rune) bool {
	return c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029
}

// Flags returns the flags in canonical order ("dgimsuvy").
func (r *Regexp) Flags() string { return r.flags }

// String returns "/source/flags", like RegExp.prototype.toString.
func (r *Regexp) String() string { return "/" + r.Source() + "/" + r.flags }

func (r *Regexp) Global() bool     { return r.global }
func (r *Regexp) IgnoreCase() bool { return r.ignoreCase }
func (r *Regexp) Multiline() bool  { return r.multiline }
func (r *Regexp) DotAll() bool     { return r.dotAll }
func (r *Regexp) Unicode() bool    { return r.unicode }
func (r *Regexp) Sticky() bool     { return r.sticky }
func (r *Regexp) HasIndices() bool { return r.hasIndices }

// NumCaptures returns the number of capture groups.
func (r *Regexp) NumCaptures() int { return r.captures }

// GroupNames returns the distinct capture group names in pattern order, or
// nil when the pattern has no named groups.
func (r *Regexp) GroupNames() []string { return r.order }

func (r *Regexp) subject(s string) *subject {
	if t := r.last.Load(); t != nil && sameString(t.s, s) {
		return t
	}
	t := newSubject(s, r.unicode)
	r.last.Store(t)
	return t
}

// matchAt runs the program from rune index start (0 <= start <= len).
func (r *Regexp) matchAt(t *subject, start int) *Match {
	m, err := r.program().FindRunesMatchStartingAt(t.runes, start)
	if err != nil || m == nil {
		return nil
	}
	return r.newMatch(t, m)
}

// Exec implements RegExp.prototype.exec with lastIndex = from (a UTF-16
// index) and returns the match or nil. As in JS, from is only used by global
// or sticky regexps; others always search from 0. With the y flag the match
// must start at from. A from beyond the end of s yields nil, and with /u an
// index inside a surrogate pair moves back to the start of the pair. The new
// lastIndex of a global or sticky regexp is Match.End.
func (r *Regexp) Exec(s string, from int) *Match {
	t := r.subject(s)
	if from < 0 || (!r.global && !r.sticky) {
		from = 0
	}
	if from > t.n16 {
		return nil
	}
	return r.matchAt(t, t.runeIndex(from))
}

// Test reports whether s contains a match (searching from index 0).
func (r *Regexp) Test(s string) bool { return r.Exec(s, 0) != nil }

// Search returns the UTF-16 index of the first match, or -1, like
// String.prototype.search.
func (r *Regexp) Search(s string) int {
	if m := r.Exec(s, 0); m != nil {
		return m.Index
	}
	return -1
}

// MatchAll returns every match, like [...s.matchAll(re)] for a global
// regexp (empty matches advance by one code unit, or one code point with
// /u). It ignores the g flag. With the y flag, matching stops at the first
// position that does not match.
func (r *Regexp) MatchAll(s string) []*Match {
	t := r.subject(s)
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

// MatchStrings implements String.prototype.match: with the g flag it returns
// every matched substring (nil when there is none); without it, the
// captures of the first match (index 0 is the whole match; groups that did
// not participate are ""), or nil.
func (r *Regexp) MatchStrings(s string) []string {
	if !r.global {
		m := r.Exec(s, 0)
		if m == nil {
			return nil
		}
		out := make([]string, m.NumGroups())
		for i := range out {
			out[i] = m.Group(i)
		}
		return out
	}
	var out []string
	for _, m := range r.MatchAll(s) {
		out = append(out, m.String())
	}
	return out
}

// AdvanceIndex implements AdvanceStringIndex: the UTF-16 index after the
// code unit at index, or after the surrogate pair at index with /u.
func (r *Regexp) AdvanceIndex(s string, index int) int {
	if !r.unicode {
		return index + 1
	}
	t := r.subject(s)
	if index+1 >= t.n16 {
		return index + 1
	}
	ri := t.runeIndex(index)
	if t.index16(ri) == index && t.index16(ri+1) == index+2 {
		return index + 2
	}
	return index + 1
}
