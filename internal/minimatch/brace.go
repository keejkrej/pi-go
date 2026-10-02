package minimatch

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Brace expansion follows brace-expansion 5.0.9. Sentinels are fixed (npm
// draws them at random) so a pattern that already contains one of these
// byte sequences can expand differently. Sequence endpoints that do not fit
// in int64 are left unexpanded. Length caps use UTF-16 code units.

const (
	expansionMax    = 100_000
	expansionMaxLen = 4_000_000

	escSlash  = "\x00SLASH\x00"
	escOpen   = "\x00OPEN\x00"
	escClose  = "\x00CLOSE\x00"
	escComma  = "\x00COMMA\x00"
	escPeriod = "\x00PERIOD\x00"
)

func jsLen(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

func hasBraces(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		for j := i + 1; j < len(s); j++ {
			// JS /./ does not cross a line terminator, so the has-brace
			// shortcut ignores a '}' on a later line.
			if s[j] == '\n' || s[j] == '\r' || s[j] == '{' {
				break
			}
			if s[j] == '}' {
				return true
			}
		}
	}
	return false
}

func braceExpand(pattern string, nobrace bool) []string {
	if nobrace || !hasBraces(pattern) {
		return []string{pattern}
	}
	return dedupe(expandBraces(pattern))
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func expandBraces(str string) []string {
	if str == "" {
		return nil
	}
	if strings.HasPrefix(str, "{}") {
		str = `\{\}` + str[2:]
	}
	out := expandInto(escapeBraces(str), expansionMax, expansionMaxLen, true)
	for i, s := range out {
		out[i] = unescapeBraces(s)
	}
	return out
}

func escapeBraces(str string) string {
	var b strings.Builder
	for i := 0; i < len(str); i++ {
		if str[i] == '\\' && i+1 < len(str) {
			switch str[i+1] {
			case '\\':
				b.WriteString(escSlash)
				i++
				continue
			case '{':
				b.WriteString(escOpen)
				i++
				continue
			case '}':
				b.WriteString(escClose)
				i++
				continue
			case ',':
				b.WriteString(escComma)
				i++
				continue
			case '.':
				b.WriteString(escPeriod)
				i++
				continue
			}
		}
		b.WriteByte(str[i])
	}
	return b.String()
}

func unescapeBraces(str string) string {
	r := strings.NewReplacer(
		escSlash, `\`,
		escOpen, `{`,
		escClose, `}`,
		escComma, `,`,
		escPeriod, `.`,
	)
	return r.Replace(str)
}

type braceSpan struct {
	pre, body, post string
}

func findBrace(str string) (braceSpan, bool) {
	start, end, ok := braceRange(str)
	if !ok {
		return braceSpan{}, false
	}
	return braceSpan{
		pre:  str[:start],
		body: str[start+1 : end],
		post: str[end+1:],
	}, true
}

// braceRange is balanced-match's range() for the single-character pair { }.
func braceRange(str string) (int, int, bool) {
	ai := strings.IndexByte(str, '{')
	if ai < 0 {
		return 0, 0, false
	}
	bi := indexByteFrom(str, '}', ai+1)
	i := ai
	if ai >= 0 && bi > 0 {
		begs := make([]int, 0, 4)
		left := len(str)
		right := 0
		hasRight := false
		var resultS, resultE int
		resultSet := false
		for i >= 0 && !resultSet {
			if i == ai {
				begs = append(begs, i)
				ai = indexByteFrom(str, '{', i+1)
			} else if len(begs) == 1 {
				r := begs[len(begs)-1]
				begs = begs[:len(begs)-1]
				resultS, resultE = r, bi
				resultSet = true
			} else {
				if len(begs) > 0 {
					beg := begs[len(begs)-1]
					begs = begs[:len(begs)-1]
					if beg < left {
						left = beg
						right = bi
						hasRight = true
					}
				}
				bi = indexByteFrom(str, '}', i+1)
			}
			if ai < bi && ai >= 0 {
				i = ai
			} else {
				i = bi
			}
		}
		if len(begs) > 0 && hasRight {
			resultS, resultE = left, right
			resultSet = true
		}
		if resultSet {
			return resultS, resultE, true
		}
	}
	return 0, 0, false
}

func indexByteFrom(s string, c byte, i int) int {
	if i < 0 {
		i = 0
	}
	if i > len(s) {
		return -1
	}
	j := strings.IndexByte(s[i:], c)
	if j < 0 {
		return -1
	}
	return i + j
}

func parseCommaParts(str string) []string {
	if str == "" {
		return []string{""}
	}
	m, ok := findBrace(str)
	if !ok {
		return strings.Split(str, ",")
	}
	p := strings.Split(m.pre, ",")
	p[len(p)-1] += "{" + m.body + "}"
	postParts := parseCommaParts(m.post)
	if m.post != "" {
		p[len(p)-1] += postParts[0]
		p = append(p, postParts[1:]...)
	}
	return p
}

func combine(acc []string, pre string, values []string, max, maxLength int, drop bool) []string {
	out := make([]string, 0, len(acc))
	length := 0
	for _, a := range acc {
		for _, v := range values {
			if len(out) >= max {
				return out
			}
			expansion := a + pre + v
			if drop && expansion == "" {
				continue
			}
			n := jsLen(expansion)
			if length+n > maxLength {
				return out
			}
			out = append(out, expansion)
			length += n
		}
	}
	return out
}

func isNumSeq(s string) bool {
	rest, ok := scanSignedDigits(s)
	if !ok || !strings.HasPrefix(rest, "..") {
		return false
	}
	rest, ok = scanSignedDigits(rest[2:])
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	if !strings.HasPrefix(rest, "..") {
		return false
	}
	rest, ok = scanSignedDigits(rest[2:])
	return ok && rest == ""
}

func scanSignedDigits(s string) (string, bool) {
	if s == "" {
		return s, false
	}
	i := 0
	if s[0] == '-' {
		i++
	}
	if i >= len(s) || s[i] < '0' || s[i] > '9' {
		return s, false
	}
	i++
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[i:], true
}

func isAlphaSeq(s string) bool {
	if len(s) < 4 || !isAlphaByte(s[0]) || s[1] != '.' || s[2] != '.' || !isAlphaByte(s[3]) {
		return false
	}
	rest := s[4:]
	if rest == "" {
		return true
	}
	if !strings.HasPrefix(rest, "..") {
		return false
	}
	rest = rest[2:]
	if strings.HasPrefix(rest, "-") {
		rest = rest[1:]
	}
	if rest == "" {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return false
		}
	}
	return true
}

func isAlphaByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isPadded(el string) bool {
	if strings.HasPrefix(el, "-") {
		el = el[1:]
	}
	return len(el) >= 2 && el[0] == '0' && el[1] >= '0' && el[1] <= '9'
}

func expandSequence(body string, alpha bool, max, maxLength int) ([]string, bool) {
	n := strings.Split(body, "..")
	if len(n) < 2 || n[0] == "" || n[1] == "" {
		return nil, false
	}
	x, ok1 := seqEndpoint(n[0], alpha)
	y, ok2 := seqEndpoint(n[1], alpha)
	if !ok1 || !ok2 {
		return nil, false
	}
	incr := int64(1)
	if len(n) >= 3 && n[2] != "" {
		step, err := strconv.ParseInt(n[2], 10, 64)
		if err != nil {
			return nil, false
		}
		if step < 0 {
			step = -step
		}
		if step < 1 {
			step = 1
		}
		incr = step
	}
	lte := true
	if y < x {
		incr = -incr
		lte = false
	}
	pad := false
	for _, el := range n {
		if isPadded(el) {
			pad = true
			break
		}
	}
	width := jsLen(n[0])
	if w := jsLen(n[1]); w > width {
		width = w
	}
	var N []string
	length := 0
	i := x
	for len(N) < max {
		if lte {
			if i > y {
				break
			}
		} else if i < y {
			break
		}
		var c string
		if alpha {
			c = string(rune(uint16(i)))
			if c == `\` {
				c = ""
			}
		} else {
			c = strconv.FormatInt(i, 10)
			if pad {
				need := width - jsLen(c)
				if need > 0 {
					z := strings.Repeat("0", need)
					if i < 0 {
						c = "-" + z + c[1:]
					} else {
						c = z + c
					}
				}
			}
		}
		if length+jsLen(c) > maxLength {
			break
		}
		N = append(N, c)
		length += jsLen(c)
		if incr > 0 {
			if i > math.MaxInt64-incr {
				break
			}
		} else if incr < 0 && i < math.MinInt64-incr {
			break
		}
		i += incr
	}
	return N, true
}

func seqEndpoint(s string, alpha bool) (int64, bool) {
	if alpha {
		r, w := utf8.DecodeRuneInString(s)
		if w != len(s) {
			return 0, false
		}
		return int64(r), true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func postRewrites(post string) bool {
	for i := 0; i < len(post); i++ {
		if post[i] != ',' {
			continue
		}
		if i+1 < len(post) && post[i+1] == ',' {
			continue
		}
		if strings.IndexByte(post[i+1:], '}') >= 0 {
			return true
		}
	}
	return false
}

func expandInto(str string, max, maxLength int, isTop bool) []string {
	acc := []string{""}
	dropEmpties := false
	firstGroup := true
	for {
		m, ok := findBrace(str)
		if !ok {
			return combine(acc, str, []string{""}, max, maxLength, dropEmpties)
		}
		pre := m.pre
		if strings.HasSuffix(pre, "$") {
			acc = combine(acc, pre+"{"+m.body+"}", []string{""}, max, maxLength, dropEmpties && m.post == "")
			firstGroup = false
			if m.post == "" {
				break
			}
			str = m.post
			continue
		}
		isNumeric := isNumSeq(m.body)
		isAlpha := !isNumeric && isAlphaSeq(m.body)
		if isNumeric || isAlpha {
			if _, ok := expandSequence(m.body, isAlpha, max, maxLength); !ok {
				isNumeric = false
				isAlpha = false
			}
		}
		isSequence := isNumeric || isAlpha
		isOptions := strings.Contains(m.body, ",")
		if !isSequence && !isOptions {
			if postRewrites(m.post) {
				str = m.pre + "{" + m.body + escClose + m.post
				isTop = true
				continue
			}
			return combine(acc, pre+"{"+m.body+"}"+m.post, []string{""}, max, maxLength, dropEmpties)
		}
		if firstGroup {
			dropEmpties = isTop && !isSequence
			firstGroup = false
		}
		var values []string
		if isSequence {
			values, _ = expandSequence(m.body, isAlpha, max, maxLength)
		} else {
			n := parseCommaParts(m.body)
			if len(n) == 1 {
				expanded := expandInto(n[0], max, maxLength, false)
				embraced := make([]string, len(expanded))
				for i, e := range expanded {
					embraced[i] = "{" + e + "}"
				}
				n = embraced
				if len(n) == 1 {
					acc = combine(acc, pre+n[0], []string{""}, max, maxLength, dropEmpties && m.post == "")
					if m.post == "" {
						break
					}
					str = m.post
					continue
				}
			}
			dropsEmpties := dropEmpties && m.post == "" && pre == ""
			for d := 0; dropsEmpties && d < len(acc); d++ {
				if acc[d] != "" {
					dropsEmpties = false
				}
			}
			valuesLen := 0
			outer := false
			for j := 0; j < len(n) && !outer; j++ {
				expanded := expandInto(n[j], max, maxLength, false)
				for _, v := range expanded {
					if dropsEmpties && v == "" {
						continue
					}
					if len(values) >= max || valuesLen+jsLen(v) > maxLength {
						outer = true
						break
					}
					values = append(values, v)
					valuesLen += jsLen(v)
				}
			}
		}
		acc = combine(acc, pre, values, max, maxLength, dropEmpties && m.post == "")
		if m.post == "" {
			break
		}
		str = m.post
	}
	return acc
}
