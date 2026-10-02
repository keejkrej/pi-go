package minimatch

import "strings"

// POSIX classes follow minimatch's brace-expressions.js. Only [:graph:] is
// stored negated; [:print:] is the unnegated \p{C} class that library emits.
type posixSpec struct {
	uni string
	u   bool
	neg bool
}

var posixMM = []struct {
	name string
	spec posixSpec
}{
	{"[:alnum:]", posixSpec{`\p{L}\p{Nl}\p{Nd}`, true, false}},
	{"[:alpha:]", posixSpec{`\p{L}\p{Nl}`, true, false}},
	{"[:ascii:]", posixSpec{`\x00-\x7f`, false, false}},
	{"[:blank:]", posixSpec{`\p{Zs}\t`, true, false}},
	{"[:cntrl:]", posixSpec{`\p{Cc}`, true, false}},
	{"[:digit:]", posixSpec{`\p{Nd}`, true, false}},
	{"[:graph:]", posixSpec{`\p{Z}\p{C}`, true, true}},
	{"[:lower:]", posixSpec{`\p{Ll}`, true, false}},
	{"[:print:]", posixSpec{`\p{C}`, true, false}},
	{"[:punct:]", posixSpec{`\p{P}`, true, false}},
	{"[:space:]", posixSpec{`\p{Z}\t\r\n\v\f`, true, false}},
	{"[:upper:]", posixSpec{`\p{Lu}`, true, false}},
	{"[:word:]", posixSpec{`\p{L}\p{Nl}\p{Nd}\p{Pc}`, true, false}},
	{"[:xdigit:]", posixSpec{"A-Fa-f0-9", false, false}},
}

func braceEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[', ']', '\\', '-':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isSingleAtom(r string) bool {
	return len(r) == 1 || (len(r) == 2 && r[0] == '\\')
}

// parseClass returns the regex source, whether /u is required, how many
// bytes of glob were consumed, and whether the class is magic. consumed 0
// means "not a class". A poisoned class returns "$.", which matches nothing.
func parseClass(glob string, pos int) (src string, uflag bool, consumed int, magic bool) {
	if pos >= len(glob) || glob[pos] != '[' {
		return "", false, 0, false
	}
	var ranges, negs []string
	i := pos + 1
	sawStart := false
	escaping := false
	negate := false
	endPos := pos
	rangeStart := ""
	for i < len(glob) {
		c := glob[i : i+1]
		if (c == "!" || c == "^") && i == pos+1 {
			negate = true
			i++
			continue
		}
		if c == "]" && sawStart && !escaping {
			endPos = i + 1
			break
		}
		sawStart = true
		if c == "\\" {
			if !escaping {
				escaping = true
				i++
				continue
			}
		}
		if c == "[" && !escaping {
			matched := false
			for _, pc := range posixMM {
				if strings.HasPrefix(glob[i:], pc.name) {
					if rangeStart != "" {
						return "$.", false, len(glob) - pos, true
					}
					i += len(pc.name)
					if pc.spec.neg {
						negs = append(negs, pc.spec.uni)
					} else {
						ranges = append(ranges, pc.spec.uni)
					}
					uflag = uflag || pc.spec.u
					matched = true
					break
				}
			}
			if matched {
				escaping = false
				continue
			}
		}
		escaping = false
		if rangeStart != "" {
			if c > rangeStart {
				ranges = append(ranges, braceEscape(rangeStart)+"-"+braceEscape(c))
			} else if c == rangeStart {
				ranges = append(ranges, braceEscape(c))
			}
			rangeStart = ""
			i++
			continue
		}
		if strings.HasPrefix(glob[i+1:], "-]") {
			ranges = append(ranges, braceEscape(c+"-"))
			i += 2
			continue
		}
		if i+1 < len(glob) && glob[i+1] == '-' {
			rangeStart = c
			i += 2
			continue
		}
		ranges = append(ranges, braceEscape(c))
		i++
	}
	if endPos < i {
		return "", false, 0, false
	}
	if len(ranges) == 0 && len(negs) == 0 {
		return "$.", false, len(glob) - pos, true
	}
	if len(negs) == 0 && len(ranges) == 1 && isSingleAtom(ranges[0]) && !negate {
		r := ranges[0]
		if len(r) == 2 {
			r = r[1:]
		}
		return regExpEscape(r), false, endPos - pos, false
	}
	sr := "[" + map[bool]string{true: "^", false: ""}[negate] + strings.Join(ranges, "") + "]"
	sn := "[" + map[bool]string{true: "", false: "^"}[negate] + strings.Join(negs, "") + "]"
	var comb string
	switch {
	case len(ranges) > 0 && len(negs) > 0:
		comb = "(" + sr + "|" + sn + ")"
	case len(ranges) > 0:
		comb = sr
	default:
		comb = sn
	}
	return comb, uflag, endPos - pos, true
}

func isRegSpecial(c byte) bool {
	switch c {
	case '(', ')', '.', '*', '{', '}', '+', '?', '[', ']', '^', '$', '\\', '!':
		return true
	default:
		return false
	}
}

func isJSSpaceByte(c byte) bool {
	switch c {
	case '\t', '\n', '\v', '\f', '\r', ' ':
		return true
	default:
		return false
	}
}

func regExpEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' || c == '[' || c == ']' || c == '{' || c == '}' || c == '(' || c == ')' ||
			c == '*' || c == '+' || c == '?' || c == '.' || c == ',' || c == '\\' ||
			c == '^' || c == '$' || c == '|' || c == '#' || isJSSpaceByte(c) {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// unescape removes minimatch escapes. magicalBraces is on and
// windowsPathsNoEscape is off, which are the npm defaults.
func unescape(s string) string {
	s = stripBracketEscape(s)
	return stripBackslash(s)
}

func stripBracketEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if repl, n, ok := matchBracketEsc(s, i); ok {
			b.WriteString(repl)
			i = n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func matchBracketEsc(s string, i int) (string, int, bool) {
	try := func(g1 string, j int) (string, int, bool) {
		if j+2 < len(s) && s[j] == '[' && s[j+2] == ']' && s[j+1] != '/' && s[j+1] != '\\' {
			return g1 + s[j+1:j+2], j + 3, true
		}
		return "", 0, false
	}
	if i < len(s) && s[i] != '\\' && s[i] != '\n' && s[i] != '\r' {
		if r, e, ok := try(s[i:i+1], i+1); ok {
			return r, e, true
		}
	}
	if i == 0 {
		return try("", 0)
	}
	return "", 0, false
}

func stripBackslash(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] != '/' {
			b.WriteByte(s[i+1])
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
