package semver

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Satisfies reports whether version satisfies the npm range.
// An invalid version or range is false. includePrerelease is off:
// a prerelease satisfies a set only when some comparator in that set
// carries a prerelease on the same major.minor.patch.
func Satisfies(version, rangeStr string) bool {
	if version == "" {
		return false
	}
	v, ok := parseVersion(version)
	if !ok {
		return false
	}
	rg, ok := parseRange(rangeStr)
	if !ok {
		return false
	}
	return rg.test(v)
}

// ValidRange returns the normalized range. Empty and star-like ranges
// normalize to "*". The bool is false when the range is invalid.
func ValidRange(r string) (string, bool) {
	rg, ok := parseRange(r)
	if !ok {
		return "", false
	}
	if rg.formatted == "" {
		return "*", true
	}
	return rg.formatted, true
}

// MaxSatisfying returns the highest version in versions that satisfies
// range, as the original slice element. Ties keep the earlier element.
// An invalid range or no match yields "".
func MaxSatisfying(versions []string, rangeStr string) string {
	rg, ok := parseRange(rangeStr)
	if !ok {
		return ""
	}
	var max string
	var maxV version
	var have bool
	for _, raw := range versions {
		v, ok := parseVersion(raw)
		if !ok || !rg.test(v) {
			continue
		}
		if !have || maxV.cmp(v) < 0 {
			max = raw
			maxV = v
			have = true
		}
	}
	if !have {
		return ""
	}
	return max
}

type comparator struct {
	op  string
	ver version
	any bool
	val string
}

func (c comparator) test(v version) bool {
	if c.any {
		return true
	}
	cmp := v.cmp(c.ver)
	switch c.op {
	case "":
		return cmp == 0
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	default:
		return false
	}
}

type semRange struct {
	sets      [][]comparator
	formatted string
}

func (r semRange) test(v version) bool {
	for _, set := range r.sets {
		if testSet(set, v) {
			return true
		}
	}
	return false
}

func testSet(set []comparator, v version) bool {
	for _, c := range set {
		if !c.test(v) {
			return false
		}
	}
	if len(v.pre) == 0 {
		return true
	}
	for _, c := range set {
		if c.any || len(c.ver.pre) == 0 {
			continue
		}
		if c.ver.major == v.major && c.ver.minor == v.minor && c.ver.patch == v.patch {
			return true
		}
	}
	return false
}

func parseRange(rangeStr string) (semRange, bool) {
	raw := collapseWS(rangeStr)
	parts := strings.Split(raw, "||")
	var sets [][]comparator
	for _, p := range parts {
		set, ok := parseRangePart(strings.TrimSpace(p))
		if !ok {
			return semRange{}, false
		}
		if len(set) == 0 {
			continue
		}
		sets = append(sets, set)
	}
	if len(sets) == 0 {
		return semRange{}, false
	}
	if len(sets) > 1 {
		first := sets[0]
		filtered := sets[:0]
		for _, c := range sets {
			if isNullSet(c) {
				continue
			}
			filtered = append(filtered, c)
		}
		sets = filtered
		if len(sets) == 0 {
			sets = [][]comparator{first}
		} else if len(sets) > 1 {
			for _, c := range sets {
				if len(c) == 1 && c[0].any {
					sets = [][]comparator{c}
					break
				}
			}
		}
	}
	var b strings.Builder
	for i, set := range sets {
		if i > 0 {
			b.WriteString("||")
		}
		for k, c := range set {
			if k > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(strings.TrimSpace(c.val))
		}
	}
	return semRange{sets: sets, formatted: b.String()}, true
}

func isNullSet(set []comparator) bool {
	return len(set) > 0 && set[0].val == "<0.0.0-0"
}

func parseRangePart(part string) ([]comparator, bool) {
	part = stripBuild(part)
	if left, right, ok := splitHyphen(part); ok {
		part = hyphenJoin(left, right)
	}
	part = comparatorTrim(part)
	part = tildeTrim(part)
	part = caretTrim(part)

	tokens := strings.Split(part, " ")
	var expanded []string
	for _, tok := range tokens {
		expanded = append(expanded, parseComparator(tok))
	}
	joined := strings.Join(expanded, " ")
	var list []string
	for _, tok := range splitJSSpace(joined) {
		list = append(list, replaceGTE0(tok))
	}

	var comps []comparator
	seen := map[string]bool{}
	for _, tok := range list {
		c, ok := parseComparatorToken(tok)
		if !ok {
			return nil, false
		}
		if c.val == "<0.0.0-0" {
			return []comparator{c}, true
		}
		if !seen[c.val] {
			seen[c.val] = true
			comps = append(comps, c)
		}
	}
	if len(seen) > 1 && seen[""] {
		var kept []comparator
		for _, c := range comps {
			if c.val != "" {
				kept = append(kept, c)
			}
		}
		comps = kept
	}
	return comps, true
}

func parseComparator(comp string) string {
	comp = stripBuild(comp)
	comp = replaceCarets(comp)
	comp = replaceTildes(comp)
	comp = replaceXRanges(comp)
	comp = replaceStars(comp)
	return comp
}

func parseComparatorToken(comp string) (comparator, bool) {
	comp = strings.TrimSpace(comp)
	if comp == "" {
		return comparator{any: true, val: ""}, true
	}
	op, rest := splitOp(comp)
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return comparator{}, false
	}
	// FULLPLAIN allows a single leading v.
	v, ok := parseVersion(rest)
	if !ok {
		return comparator{}, false
	}
	if op == "=" {
		op = ""
	}
	return comparator{op: op, ver: v, val: op + v.raw}, true
}

func splitOp(s string) (op, rest string) {
	if strings.HasPrefix(s, ">=") || strings.HasPrefix(s, "<=") {
		return s[:2], s[2:]
	}
	if strings.HasPrefix(s, ">") || strings.HasPrefix(s, "<") || strings.HasPrefix(s, "=") {
		return s[:1], s[1:]
	}
	return "", s
}

func replaceGTE0(comp string) string {
	s := strings.TrimSpace(comp)
	// ^\s*>=\s*0\.0\.0\s*$
	if s == ">=0.0.0" {
		return ""
	}
	return s
}

func replaceStars(comp string) string {
	s := strings.TrimSpace(comp)
	// (<|>)?=?\s*\*  globally. After trim, a lone star token is the common case.
	var b strings.Builder
	for i := 0; i < len(s); {
		if j, ok := matchStar(s, i); ok {
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func matchStar(s string, i int) (int, bool) {
	j := i
	if j < len(s) && (s[j] == '<' || s[j] == '>') {
		j++
	}
	if j < len(s) && s[j] == '=' {
		j++
	}
	k := j
	for k < len(s) && isJSSpace(rune(s[k])) {
		k++
	}
	if k < len(s) && s[k] == '*' {
		return k + 1, true
	}
	return 0, false
}

func replaceCarets(comp string) string {
	return mapWSTokens(comp, replaceCaret)
}

func replaceTildes(comp string) string {
	return mapWSTokens(comp, replaceTilde)
}

func replaceXRanges(comp string) string {
	parts := splitJSSpace(comp)
	for i, p := range parts {
		parts[i] = replaceXRange(strings.TrimSpace(p))
	}
	return strings.Join(parts, " ")
}

func mapWSTokens(comp string, fn func(string) string) string {
	comp = strings.TrimSpace(comp)
	parts := splitJSSpace(comp)
	for i, p := range parts {
		parts[i] = fn(p)
	}
	return strings.Join(parts, " ")
}

type xpart struct {
	raw        string
	M, m, p    string
	hasM       bool
	hasm, hasp bool
	pr         string
	hasPr      bool
}

func (x xpart) isXM() bool { return !x.hasM || isXID(x.M) }
func (x xpart) isXm() bool { return !x.hasm || isXID(x.m) }
func (x xpart) isXp() bool { return !x.hasp || isXID(x.p) }

func isXID(id string) bool {
	return id == "" || strings.EqualFold(id, "x") || id == "*"
}

func replaceCaret(comp string) string {
	if len(comp) == 0 || comp[0] != '^' {
		return comp
	}
	x, ok := parseXRangePlain(comp[1:])
	if !ok {
		return comp
	}
	return caretOf(x)
}

func caretOf(x xpart) string {
	M, m, p, pr := x.M, x.m, x.p, x.pr
	if x.isXM() {
		return ""
	}
	if x.isXm() {
		return ">=" + M + ".0.0 <" + incComp(M) + ".0.0-0"
	}
	if x.isXp() {
		if M == "0" {
			return ">=" + M + "." + m + ".0 <" + M + "." + incComp(m) + ".0-0"
		}
		return ">=" + M + "." + m + ".0 <" + incComp(M) + ".0.0-0"
	}
	if x.hasPr {
		if M == "0" {
			if m == "0" {
				return ">=" + M + "." + m + "." + p + "-" + pr + " <" + M + "." + m + "." + incComp(p) + "-0"
			}
			return ">=" + M + "." + m + "." + p + "-" + pr + " <" + M + "." + incComp(m) + ".0-0"
		}
		return ">=" + M + "." + m + "." + p + "-" + pr + " <" + incComp(M) + ".0.0-0"
	}
	if M == "0" {
		if m == "0" {
			return ">=" + M + "." + m + "." + p + " <" + M + "." + m + "." + incComp(p) + "-0"
		}
		return ">=" + M + "." + m + "." + p + " <" + M + "." + incComp(m) + ".0-0"
	}
	return ">=" + M + "." + m + "." + p + " <" + incComp(M) + ".0.0-0"
}

func replaceTilde(comp string) string {
	body := comp
	switch {
	case strings.HasPrefix(comp, "~>"):
		body = comp[2:]
	case strings.HasPrefix(comp, "~"):
		body = comp[1:]
	default:
		return comp
	}
	x, ok := parseXRangePlain(body)
	if !ok {
		return comp
	}
	M, m, p, pr := x.M, x.m, x.p, x.pr
	if x.isXM() {
		return ""
	}
	if x.isXm() {
		return ">=" + M + ".0.0 <" + incComp(M) + ".0.0-0"
	}
	if x.isXp() {
		return ">=" + M + "." + m + ".0 <" + M + "." + incComp(m) + ".0-0"
	}
	if x.hasPr {
		return ">=" + M + "." + m + "." + p + "-" + pr + " <" + M + "." + incComp(m) + ".0-0"
	}
	return ">=" + M + "." + m + "." + p + " <" + M + "." + incComp(m) + ".0-0"
}

func replaceXRange(comp string) string {
	op, rest := splitOp(comp)
	// XRANGE allows whitespace between operator and version; callers trim tokens.
	rest = strings.TrimSpace(rest)
	// An operator with nothing after it is not an xrange (and not a comparator).
	x, ok := parseXRangePlain(rest)
	if !ok {
		return comp
	}
	// The plain match must consume rest entirely, which parseXRangePlain requires.
	if invalidXOrder(x) {
		return comp
	}
	xM, xm, xp := x.isXM(), x.isXm(), x.isXp()
	anyX := xp
	gtlt := op
	if gtlt == "=" && anyX {
		gtlt = ""
	}
	M, m, p := x.M, x.m, x.p
	pr := ""
	if xM {
		if gtlt == ">" || gtlt == "<" {
			return "<0.0.0-0"
		}
		return "*"
	}
	if gtlt != "" && anyX {
		if xm {
			m = "0"
		}
		p = "0"
		if gtlt == ">" {
			gtlt = ">="
			if xm {
				M = incComp(M)
				m = "0"
				p = "0"
			} else {
				m = incComp(m)
				p = "0"
			}
		} else if gtlt == "<=" {
			gtlt = "<"
			if xm {
				M = incComp(M)
			} else {
				m = incComp(m)
			}
		}
		if gtlt == "<" {
			pr = "-0"
		}
		return gtlt + M + "." + m + "." + p + pr
	}
	if xm {
		return ">=" + M + ".0.0" + pr + " <" + incComp(M) + ".0.0-0"
	}
	if xp {
		return ">=" + M + "." + m + ".0" + pr + " <" + M + "." + incComp(m) + ".0-0"
	}
	return comp
}

func invalidXOrder(x xpart) bool {
	mMissingOrX := x.isXm()
	// isX(M) && !isX(m): m present and not x, M is x
	if x.isXM() && x.hasm && !isXID(x.m) {
		return true
	}
	// isX(m) && p && !isX(p)
	if mMissingOrX && x.hasp && x.p != "" && !isXID(x.p) {
		return true
	}
	return false
}

func incComp(s string) string {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > maxSafeInteger {
		return s + "0"
	}
	return strconv.FormatInt(n+1, 10)
}

// parseXRangePlain matches [v=\s]* id ( . id ( . id ( -pre)? ( +build)? )? )?
// against the whole string.
func parseXRangePlain(s string) (xpart, bool) {
	i := 0
	for i < len(s) {
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == 'v' || r == '=' || isJSSpace(r) {
			i += w
			continue
		}
		break
	}
	id, ni, ok := scanXID(s, i)
	if !ok {
		return xpart{}, false
	}
	x := xpart{raw: s, M: id, hasM: true}
	i = ni
	if i < len(s) && s[i] == '.' {
		id, ni, ok = scanXID(s, i+1)
		if !ok {
			return xpart{}, false
		}
		x.m, x.hasm = id, true
		i = ni
		if i < len(s) && s[i] == '.' {
			id, ni, ok = scanXID(s, i+1)
			if !ok {
				return xpart{}, false
			}
			x.p, x.hasp = id, true
			i = ni
			if i < len(s) && s[i] == '-' {
				pr, ni, ok := scanPrerelease(s, i+1)
				if !ok {
					return xpart{}, false
				}
				x.pr, x.hasPr = pr, true
				i = ni
			}
			if i < len(s) && s[i] == '+' {
				_, ni, ok := scanBuild(s, i+1)
				if !ok {
					return xpart{}, false
				}
				i = ni
			}
		}
	}
	if i != len(s) {
		return xpart{}, false
	}
	return x, true
}

func scanXID(s string, i int) (string, int, bool) {
	if i >= len(s) {
		return "", 0, false
	}
	if s[i] == '*' || s[i] == 'x' || s[i] == 'X' {
		return s[i : i+1], i + 1, true
	}
	if s[i] == '0' && (i+1 == len(s) || s[i+1] < '0' || s[i+1] > '9') {
		return "0", i + 1, true
	}
	// Also a lone 0 followed by non-digit is handled. "0" as start of "0..." no, next is digit would be leading zero which is NOT a numeric id.
	if s[i] == '0' {
		return "", 0, false
	}
	if s[i] < '1' || s[i] > '9' {
		return "", 0, false
	}
	j := i + 1
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return s[i:j], j, true
}

func scanPrerelease(s string, i int) (string, int, bool) {
	start := i
	for {
		id, ni, ok := scanPreID(s, i)
		if !ok || id == "" {
			return "", 0, false
		}
		i = ni
		if i < len(s) && s[i] == '.' {
			i++
			continue
		}
		break
	}
	if i == start {
		return "", 0, false
	}
	return s[start:i], i, true
}

func scanPreID(s string, i int) (string, int, bool) {
	if i >= len(s) {
		return "", 0, false
	}
	j := i
	// NONNUMERIC or NUMERIC. Try to take [0-9A-Za-z-]+ then validate.
	for j < len(s) {
		c := s[j]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			j++
			continue
		}
		break
	}
	if j == i {
		return "", 0, false
	}
	id := s[i:j]
	if !isPreIdent(id) {
		return "", 0, false
	}
	// safe-regex caps the trailing letter-run; a single ident longer than
	// the version max cannot appear inside a legal comparator anyway.
	if len(id) > maxLen {
		return "", 0, false
	}
	return id, j, true
}

func scanBuild(s string, i int) (string, int, bool) {
	start := i
	for {
		if i >= len(s) {
			return "", 0, false
		}
		j := i
		for j < len(s) {
			c := s[j]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
				j++
				continue
			}
			break
		}
		if j == i || j-i > maxSafeBuildLen {
			return "", 0, false
		}
		i = j
		if i < len(s) && s[i] == '.' {
			i++
			continue
		}
		break
	}
	if i == start {
		return "", 0, false
	}
	return s[start:i], i, true
}

func splitHyphen(s string) (left, right string, ok bool) {
	i := strings.Index(s, " - ")
	if i < 0 {
		return "", "", false
	}
	left, right = s[:i], s[i+3:]
	if _, ok := parseXRangePlain(left); !ok {
		return "", "", false
	}
	if _, ok := parseXRangePlain(right); !ok {
		return "", "", false
	}
	return left, right, true
}

func hyphenJoin(left, right string) string {
	lf, _ := parseXRangePlain(left)
	rt, _ := parseXRangePlain(right)
	var from, to string
	switch {
	case lf.isXM():
		from = ""
	case lf.isXm():
		from = ">=" + lf.M + ".0.0"
	case lf.isXp():
		from = ">=" + lf.M + "." + lf.m + ".0"
	default:
		from = ">=" + lf.raw
	}
	switch {
	case rt.isXM():
		to = ""
	case rt.isXm():
		to = "<" + incComp(rt.M) + ".0.0-0"
	case rt.isXp():
		to = "<" + rt.M + "." + incComp(rt.m) + ".0-0"
	case rt.hasPr:
		to = "<=" + rt.M + "." + rt.m + "." + rt.p + "-" + rt.pr
	default:
		to = "<=" + rt.raw
	}
	return strings.TrimSpace(from + " " + to)
}

func comparatorTrim(s string) string {
	// Drop whitespace between an operator and its operand.
	var b strings.Builder
	for i := 0; i < len(s); {
		if op, n := opAt(s, i); n > 0 {
			j := i + n
			k := j
			for k < len(s) && s[k] == ' ' {
				k++
			}
			if k > j && k < len(s) && looksLikeVersionStart(s[k:]) {
				b.WriteString(op)
				i = k
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func opAt(s string, i int) (string, int) {
	if strings.HasPrefix(s[i:], ">=") || strings.HasPrefix(s[i:], "<=") {
		return s[i : i+2], 2
	}
	if i < len(s) && (s[i] == '>' || s[i] == '<' || s[i] == '=') {
		return s[i : i+1], 1
	}
	return "", 0
}

func looksLikeVersionStart(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c == 'v' || c == '=' || c == 'x' || c == 'X' || c == '*' || (c >= '0' && c <= '9') || isJSSpace(rune(c))
}

func tildeTrim(s string) string {
	return trimOpSpace(s, "~>", "~")
}

func caretTrim(s string) string {
	return trimOpSpace(s, "", "^")
}

// trimOpSpace rewrites "<spaces><op><spaces>" to "<spaces><repl>" when op is
// followed by whitespace. longer is tried before short (for "~>" vs "~").
func trimOpSpace(s, longer, short string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if longer != "" && strings.HasPrefix(s[i:], longer) && i+len(longer) < len(s) && s[i+len(longer)] == ' ' {
			// keep no extra; the spaces BEFORE the op were already written
			b.WriteString(short)
			j := i + len(longer)
			for j < len(s) && s[j] == ' ' {
				j++
			}
			i = j
			continue
		}
		if strings.HasPrefix(s[i:], short) && i+len(short) < len(s) && s[i+len(short)] == ' ' {
			// do not treat "~>" as "~" plus garbage: longer already handled.
			if longer != "" && strings.HasPrefix(s[i:], longer) {
				b.WriteByte(s[i])
				i++
				continue
			}
			b.WriteString(short)
			j := i + len(short)
			for j < len(s) && s[j] == ' ' {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func stripBuild(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '+' {
			if _, ni, ok := scanBuild(s, i+1); ok {
				i = ni
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func splitJSSpace(s string) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	start := 0
	i := 0
	for i < len(s) {
		r, w := utf8.DecodeRuneInString(s[i:])
		if isJSSpace(r) {
			out = append(out, s[start:i])
			i += w
			for i < len(s) {
				r, w = utf8.DecodeRuneInString(s[i:])
				if !isJSSpace(r) {
					break
				}
				i += w
			}
			start = i
			continue
		}
		i += w
	}
	out = append(out, s[start:])
	return out
}
