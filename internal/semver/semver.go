// Package semver is a hand port of the strict (non-loose) surface of npm
// semver 7.8.5 used by pi: Valid, Compare, RCompare, Gt, Satisfies,
// MaxSatisfying and ValidRange.
//
// includePrerelease and loose are the npm defaults (both off). A prerelease
// satisfies a range only when some comparator in the matching set carries a
// prerelease on the same major.minor.patch. Build metadata is ignored when
// ordering and is stripped from Valid.
package semver

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxLen          = 256
	maxSafeInteger  = 9007199254740991
	maxSafeBuildLen = maxLen - 6
)

// Version is a parsed semantic version. The zero value is not valid.
type version struct {
	major, minor, patch int64
	pre                 []ident
	raw                 string // formatted, without build
}

type ident struct {
	n   int64
	s   string
	num bool
}

func (a ident) cmp(b ident) int {
	// Numeric ids that are not safe integers stay strings. npm still coerces
	// both sides with Number() when both look like digit strings.
	aNum := a.num || isAllDigits(a.s)
	bNum := b.num || isAllDigits(b.s)
	if a.num && b.num {
		switch {
		case a.n == b.n:
			return 0
		case a.n < b.n:
			return -1
		default:
			return 1
		}
	}
	if aNum && bNum {
		af, bf := a.asFloat(), b.asFloat()
		switch {
		case af == bf:
			return 0
		case af < bf:
			return -1
		default:
			return 1
		}
	}
	if aNum {
		return -1
	}
	if bNum {
		return 1
	}
	as, bs := a.s, b.s
	switch {
	case as == bs:
		return 0
	case as < bs:
		return -1
	default:
		return 1
	}
}

func (a ident) asFloat() float64 {
	if a.num {
		return float64(a.n)
	}
	f, err := strconv.ParseFloat(a.s, 64)
	if err != nil {
		return 0
	}
	return f
}

func (a ident) String() string {
	if a.num {
		return strconv.FormatInt(a.n, 10)
	}
	return a.s
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ',
		'\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func jsTrim(s string) string {
	i, j := 0, len(s)
	for i < j {
		r, w := utf8.DecodeRuneInString(s[i:])
		if !isJSSpace(r) {
			break
		}
		i += w
	}
	for j > i {
		r, w := utf8.DecodeLastRuneInString(s[:j])
		if !isJSSpace(r) {
			break
		}
		j -= w
	}
	return s[i:j]
}

func collapseWS(s string) string {
	s = jsTrim(s)
	var b strings.Builder
	b.Grow(len(s))
	prev := false
	for _, r := range s {
		if isJSSpace(r) {
			if !prev {
				b.WriteByte(' ')
				prev = true
			}
			continue
		}
		prev = false
		b.WriteRune(r)
	}
	return b.String()
}

// numeric identifier: 0 or a non-zero digit then digits. No leading zeros.
func parseSafeNum(s string) (int64, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	if len(s) > 16 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > maxSafeInteger {
		return 0, false
	}
	return n, true
}

// parseIdentNum is the prerelease numeric rule: a digit string becomes a
// number only when its value is strictly less than MAX_SAFE_INTEGER.
func parseIdentNum(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	if len(s) > 16 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n >= maxSafeInteger {
		return 0, false
	}
	return n, true
}

// isNumericID is NUMERICIDENTIFIER: 0 or a non-zero digit then any digits.
// Length is not capped here; a too-big id stays a string identifier.
func isNumericID(s string) bool {
	if s == "0" {
		return true
	}
	if s == "" || s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isPreIdent(s string) bool {
	if s == "" {
		return false
	}
	if isNumericID(s) {
		return true
	}
	// NONNUMERIC: \d*[a-zA-Z-][a-zA-Z0-9-]*
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i >= len(s) {
		return false
	}
	c := s[i]
	if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-') {
		return false
	}
	for ; i < len(s); i++ {
		c = s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func isBuildIdent(s string) bool {
	if s == "" || len(s) > maxSafeBuildLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func parseVersion(raw string) (version, bool) {
	// npm rejects on UTF-16 length before trim.
	if utf16Len(raw) > maxLen {
		return version{}, false
	}
	s := jsTrim(raw)
	if s == "" || utf16Len(s) > maxLen {
		return version{}, false
	}
	if s[0] == 'v' || s[0] == 'V' {
		if s[0] != 'v' {
			return version{}, false
		}
		s = s[1:]
	}
	main, rest, ok := cutMain(s)
	if !ok {
		return version{}, false
	}
	var pre, build string
	sawBuild := false
	if rest != "" {
		switch rest[0] {
		case '-':
			rest = rest[1:]
			plus := strings.IndexByte(rest, '+')
			if plus >= 0 {
				pre, build = rest[:plus], rest[plus+1:]
				sawBuild = true
			} else {
				pre = rest
			}
			if pre == "" {
				return version{}, false
			}
		case '+':
			build = rest[1:]
			sawBuild = true
		default:
			return version{}, false
		}
	}
	if sawBuild && build == "" {
		return version{}, false
	}
	maj, min, pat, ok := splitMain(main)
	if !ok {
		return version{}, false
	}
	v := version{major: maj, minor: min, patch: pat}
	if pre != "" {
		parts := strings.Split(pre, ".")
		v.pre = make([]ident, len(parts))
		for i, p := range parts {
			if !isPreIdent(p) {
				return version{}, false
			}
			if isNumericID(p) {
				if n, ok := parseIdentNum(p); ok {
					v.pre[i] = ident{n: n, num: true}
				} else {
					v.pre[i] = ident{s: p}
				}
			} else {
				v.pre[i] = ident{s: p}
			}
		}
	}
	if build != "" {
		parts := strings.Split(build, ".")
		for _, p := range parts {
			if !isBuildIdent(p) {
				return version{}, false
			}
		}
	}
	v.raw = v.format()
	return v, true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func cutMain(s string) (main, rest string, ok bool) {
	i := 0
	dots := 0
	for i < len(s) {
		c := s[i]
		if c == '.' {
			dots++
			i++
			continue
		}
		if c == '-' || c == '+' {
			break
		}
		if c < '0' || c > '9' {
			return "", "", false
		}
		i++
	}
	if dots != 2 {
		return "", "", false
	}
	return s[:i], s[i:], true
}

func splitMain(main string) (maj, min, pat int64, ok bool) {
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var ns [3]int64
	for i, p := range parts {
		n, ok := parseSafeNum(p)
		if !ok {
			return 0, 0, 0, false
		}
		ns[i] = n
	}
	return ns[0], ns[1], ns[2], true
}

func (v version) format() string {
	s := strconv.FormatInt(v.major, 10) + "." + strconv.FormatInt(v.minor, 10) + "." + strconv.FormatInt(v.patch, 10)
	if len(v.pre) == 0 {
		return s
	}
	s += "-"
	for i, id := range v.pre {
		if i > 0 {
			s += "."
		}
		s += id.String()
	}
	return s
}

func (v version) cmpMain(o version) int {
	if v.major != o.major {
		if v.major < o.major {
			return -1
		}
		return 1
	}
	if v.minor != o.minor {
		if v.minor < o.minor {
			return -1
		}
		return 1
	}
	if v.patch != o.patch {
		if v.patch < o.patch {
			return -1
		}
		return 1
	}
	return 0
}

func (v version) cmpPre(o version) int {
	if len(v.pre) == 0 && len(o.pre) == 0 {
		return 0
	}
	if len(v.pre) > 0 && len(o.pre) == 0 {
		return -1
	}
	if len(v.pre) == 0 && len(o.pre) > 0 {
		return 1
	}
	n := len(v.pre)
	if len(o.pre) > n {
		n = len(o.pre)
	}
	for i := 0; i < n; i++ {
		if i >= len(v.pre) {
			return -1
		}
		if i >= len(o.pre) {
			return 1
		}
		if c := v.pre[i].cmp(o.pre[i]); c != 0 {
			return c
		}
	}
	return 0
}

func (v version) cmp(o version) int {
	if v.raw == o.raw {
		return 0
	}
	if c := v.cmpMain(o); c != 0 {
		return c
	}
	return v.cmpPre(o)
}

// Valid reports the canonical version string, without build metadata.
// The second result is false when v is not strict semver.
func Valid(v string) (string, bool) {
	parsed, ok := parseVersion(v)
	if !ok {
		return "", false
	}
	return parsed.raw, true
}

// Compare orders a and b. Invalid versions compare equal (0); npm throws.
func Compare(a, b string) int {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return 0
	}
	return av.cmp(bv)
}

// RCompare is Compare(b, a).
func RCompare(a, b string) int { return Compare(b, a) }

// Gt reports whether a > b. Invalid versions are not greater.
func Gt(a, b string) bool {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return false
	}
	return av.cmp(bv) > 0
}
