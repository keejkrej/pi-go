package ignore

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/minimatch/jsregex"
)

// Placeholder and trailing-star marker match node-ignore. A literal NUL in
// the pattern is held aside, so the placeholder cannot collide.
const (
	placeholder = "\x00"
	wildMarker  = "\uE000"
	neverMatch  = "[]"
)

// A quantified JS capture keeps only its last iteration. The intermediate
// star replacer is `(^|[^\\]+)(\\\*)+(?=.+)`, so every `\*` except the last
// in that match is consumed and discarded. `***` therefore compiles like `**`.

var posixClass = map[string]string{
	"alnum":  "0-9A-Za-z",
	"alpha":  "A-Za-z",
	"blank":  ` \t`,
	"cntrl":  `\x00-\x1f\x7f`,
	"digit":  "0-9",
	"graph":  "!-.0-~",
	"lower":  "a-z",
	"print":  " -.0-~",
	"punct":  "!-.:-@\\[-" + "`" + "{-~",
	"space":  ` \t\n\r`,
	"upper":  "A-Z",
	"xdigit": "0-9A-Fa-f",
}

type held struct {
	s     string
	undef bool
}

func literalSpecial(r rune) bool {
	switch r {
	case '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\', '/':
		return true
	default:
		return false
	}
}

func escapeMember(r rune) string {
	switch r {
	case '\\', ']', '^', '-', '[':
		return `\` + string(r)
	default:
		return string(r)
	}
}

func classMatchesSlash(source string) bool {
	re, err := jsregex.Compile(source, "")
	if err != nil {
		return false
	}
	return re.MatchString("/")
}

func classSource(negated bool, body string) string {
	if negated {
		return `[^\/` + body + `]`
	}
	source := "[" + body + "]"
	if classMatchesSlash(source) {
		return `(?!\/)` + source
	}
	return source
}

// scanBracket mirrors git wildmatch's member loop. ok is false when the
// expression is unterminated or names an unknown POSIX class.
func scanBracket(pattern []rune, start int) (end int, source string, ok bool) {
	index := start + 1
	negated := false
	if index < len(pattern) && (pattern[index] == '!' || pattern[index] == '^') {
		negated = true
		index++
	}
	body := ""
	var prev rune
	hasPrev := false
	for {
		if index >= len(pattern) {
			return 0, "", false
		}
		char := pattern[index]
		if char == '\\' {
			if index+1 >= len(pattern) {
				return 0, "", false
			}
			escaped := pattern[index+1]
			body += escapeMember(escaped)
			prev, hasPrev = escaped, true
			index++
		} else if char == '-' && hasPrev && index+1 < len(pattern) && pattern[index+1] != ']' {
			index++
			to := pattern[index]
			if to == '\\' {
				index++
				if index >= len(pattern) {
					return 0, "", false
				}
				to = pattern[index]
			}
			if prev <= to {
				body += "-" + escapeMember(to)
			}
			hasPrev = false
		} else if char == '[' && index+1 < len(pattern) && pattern[index+1] == ':' {
			nameStart := index + 2
			end := nameStart
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end == len(pattern) {
				return 0, "", false
			}
			if end > nameStart && pattern[end-1] == ':' {
				name := string(pattern[nameStart : end-1])
				expanded, known := posixClass[name]
				if !known {
					return 0, "", false
				}
				body += expanded
				hasPrev = false
				index = end
			} else {
				body += escapeMember('[')
				prev, hasPrev = '[', true
				index = nameStart - 2
			}
		} else {
			body += escapeMember(char)
			prev, hasPrev = char, true
		}
		index++
		if index < len(pattern) && pattern[index] == ']' {
			return index, classSource(negated, body), true
		}
	}
}

func extractBrackets(pattern string) (string, []held) {
	rs := []rune(pattern)
	var sources []held
	hold := func(h held) string {
		sources = append(sources, h)
		return placeholder + strconv.Itoa(len(sources)-1) + placeholder
	}
	var out strings.Builder
	for index := 0; index < len(rs); {
		char := rs[index]
		if char == '\\' {
			if index+1 >= len(rs) {
				// The missing operand is JS undefined. String.replace
				// later inserts the word "undefined".
				out.WriteString(hold(held{undef: true}))
			} else {
				escaped := rs[index+1]
				switch escaped {
				case '*', '[', ' ', '\\':
					out.WriteRune('\\')
					out.WriteRune(escaped)
				default:
					if literalSpecial(escaped) {
						out.WriteString(hold(held{s: `\` + string(escaped)}))
					} else {
						out.WriteString(hold(held{s: string(escaped)}))
					}
				}
			}
			index += 2
			continue
		}
		if char == 0 {
			out.WriteString(hold(held{s: "[\x00]"}))
			index++
			continue
		}
		if char == '[' {
			end, src, ok := scanBracket(rs, index)
			if !ok {
				out.WriteString(hold(held{s: neverMatch}))
				break
			}
			out.WriteString(hold(held{s: src}))
			index = end + 1
			continue
		}
		out.WriteRune(char)
		index++
	}
	return out.String(), sources
}

func trimBOM(s string) string {
	return strings.TrimPrefix(s, "\uFEFF")
}

func trimTrailingNewlines(s string) string {
	i := len(s)
	for i > 0 {
		if s[i-1] == '\n' || s[i-1] == '\r' {
			i--
			continue
		}
		break
	}
	return s[:i]
}

// Trailing spaces go away unless a backslash quotes them. An odd run of
// backslashes keeps one space; an even run keeps the backslashes only.
func trimTrailingSpaces(s string) string {
	i := len(s)
	for i > 0 && s[i-1] == ' ' {
		i--
	}
	if i == len(s) {
		return s
	}
	j := i
	for j > 0 && s[j-1] == '\\' {
		j--
	}
	n := i - j
	if n%2 == 1 {
		return s[:j] + strings.Repeat(`\`, n-1) + " "
	}
	return s[:i]
}

func foldEscapedSpaces(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			j := i
			for j < len(s) && s[j] == '\\' {
				j++
			}
			if j < len(s) && s[j] == ' ' {
				n := j - i
				b.WriteString(strings.Repeat(`\`, n-n%2))
				b.WriteByte(' ')
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func escapeMeta(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '$', '.', '|', '*', '+', '(', ')', '{', '^':
			b.WriteByte('\\')
			b.WriteByte(s[i])
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func replaceQuestions(s string) string {
	return strings.ReplaceAll(s, "?", "[^/]")
}

func replaceSlashes(s string) string {
	if strings.HasPrefix(s, "/") {
		s = "^" + s[1:]
	}
	return strings.ReplaceAll(s, "/", `\/`)
}

func replaceLeadingGlobstar(s string) string {
	i := 0
	for i < len(s) && s[i] == '^' {
		i++
	}
	start := i
	for strings.HasPrefix(s[i:], `\*\*\/`) {
		i += len(`\*\*\/`)
	}
	if i == start {
		return s
	}
	return `^(?:.*\/)?` + s[i:]
}

func hasInnerSlash(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '/' && i != len(pattern)-1 {
			return true
		}
	}
	return false
}

func anchorStart(source, pattern string) string {
	if source == "" || source[0] == '^' {
		return source
	}
	if !hasInnerSlash(pattern) {
		return `(?:^|\/)` + source
	}
	return "^" + source
}

func replaceGlobstar(s string) string {
	const needle = `\/\*\*`
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], needle) {
			after := i + len(needle)
			if after == len(s) || strings.HasPrefix(s[after:], `\/`) {
				rest := s[after:]
				switch {
				case after == len(s):
					b.WriteString(`\/.+`)
				case rest == `\/`:
					b.WriteString(`(?:\/[^\/]+)+`)
				default:
					b.WriteString(`(?:\/[^\/]+)*`)
				}
				i = after
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func dotPlus(s string, k int) bool {
	if k >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[k:])
	return r != '\n' && r != '\r' && r != '\u2028' && r != '\u2029'
}

func matchInterAt(s string, i int) (p1 string, end int, ok bool) {
	tryStars := func(p1end int) (string, int, bool) {
		j := p1end
		k := j
		for k+1 < len(s) && s[k] == '\\' && s[k+1] == '*' {
			k += 2
		}
		if k == j {
			return "", 0, false
		}
		for k-j >= 2 && !dotPlus(s, k) {
			k -= 2
		}
		if k == j || !dotPlus(s, k) {
			return "", 0, false
		}
		return s[i:p1end], k, true
	}
	if i == 0 {
		if p, e, ok := tryStars(i); ok {
			return p, e, true
		}
	}
	if i >= len(s) || s[i] == '\\' {
		return "", 0, false
	}
	j := i
	for j < len(s) && s[j] != '\\' {
		j++
	}
	return tryStars(j)
}

func replaceIntermediateStars(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		p1, end, ok := matchInterAt(s, i)
		if !ok {
			b.WriteByte(s[i])
			i++
			continue
		}
		b.WriteString(p1)
		b.WriteString(`[^\/]*`)
		i = end
	}
	return b.String()
}

func replaceTrailingWild(s string) string {
	if !strings.HasSuffix(s, `\*`) {
		return s
	}
	star := len(s) - 1
	bs := star
	for bs > 0 && s[bs-1] == '\\' {
		bs--
	}
	n := star - bs
	if n%2 == 0 {
		return s
	}
	p2len := n - 1
	if (p2len/2)%2 != 0 {
		return s
	}
	p2 := s[bs : bs+p2len]
	if bs == 0 {
		return p2 + wildMarker
	}
	return s[:bs-1] + s[bs-1:bs] + p2 + wildMarker
}

func isMetaSpecial(c byte) bool {
	switch c {
	case '$', '.', '|', '*', '+', '(', ')', '{', '^':
		return true
	default:
		return false
	}
}

func unescapeSpecials(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+3 < len(s) && s[i] == '\\' && s[i+1] == '\\' && s[i+2] == '\\' && isMetaSpecial(s[i+3]) {
			b.WriteByte('\\')
			i += 3
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func unescapePairs(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '\\' && s[i+1] == '\\' {
			b.WriteByte('\\')
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func cleanRangeBackSlash(slashes string) string {
	return slashes[:len(slashes)-len(slashes)%2]
}

func replaceLiteralBrackets(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		repl, end, ok := matchLitBracket(s, i)
		if !ok {
			b.WriteByte(s[i])
			i++
			continue
		}
		b.WriteString(repl)
		i = end
	}
	return b.String()
}

func matchLitBracket(s string, i int) (repl string, end int, ok bool) {
	if i+1 >= len(s) || s[i] != '\\' || s[i+1] != '[' {
		return "", 0, false
	}
	j := i + 2
	for k := j; ; k++ {
		if k > len(s) {
			return "", 0, false
		}
		if k > j {
			prev := s[k-1]
			if prev == ']' || prev == '/' {
				return "", 0, false
			}
		}
		t := k
		for t < len(s) && s[t] == '\\' {
			t++
		}
		if t == len(s) || (t < len(s) && s[t] == ']') {
			close := ""
			end = t
			if t < len(s) {
				close = "]"
				end = t + 1
			}
			repl = `\[]`[:2] + s[j:k] + cleanRangeBackSlash(s[k:t]) + close
			// `\[]`[:2] is `\[`
			return repl, end, true
		}
		if k == len(s) {
			return "", 0, false
		}
	}
}

func anchorEnd(s string) string {
	if s == "" || strings.HasSuffix(s, wildMarker) {
		return s
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	if r == '/' {
		return s + "$"
	}
	return s + `(?=$|\/$)`
}

func restoreHolders(s string, sources []held) string {
	if len(sources) == 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0 {
			j := i + 1
			k := j
			for k < len(s) && s[k] >= '0' && s[k] <= '9' {
				k++
			}
			if k > j && k < len(s) && s[k] == 0 {
				n, _ := strconv.Atoi(s[j:k])
				if n >= 0 && n < len(sources) {
					if sources[n].undef {
						b.WriteString("undefined")
					} else {
						b.WriteString(sources[n].s)
					}
				}
				i = k + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func makeRegexPrefix(pattern string) string {
	source, sources := extractBrackets(pattern)
	s := source
	s = trimBOM(s)
	s = trimTrailingNewlines(s)
	s = trimTrailingSpaces(s)
	s = foldEscapedSpaces(s)
	s = escapeMeta(s)
	s = replaceQuestions(s)
	s = replaceSlashes(s)
	s = replaceLeadingGlobstar(s)
	s = anchorStart(s, pattern)
	s = replaceGlobstar(s)
	s = replaceIntermediateStars(s)
	s = replaceTrailingWild(s)
	s = unescapeSpecials(s)
	s = unescapePairs(s)
	s = replaceLiteralBrackets(s)
	s = anchorEnd(s)
	s = restoreHolders(s, sources)
	return s
}

func expandMarker(s string) string {
	if !strings.HasSuffix(s, wildMarker) {
		return s
	}
	head := s[:len(s)-len(wildMarker)]
	if strings.HasSuffix(head, `\/`) {
		return head + `[^/]+(?=$|\/$)`
	}
	return head + `[^/]*(?=$|\/$)`
}

const wildcardTok = `[^\/]*`

type pinTok struct {
	kind int // 0 wildcard, 1 single, 2 boundary
	s    string
}

func pinWildcards(source string) string {
	if !strings.Contains(source, wildcardTok) {
		return source
	}
	var tokens []pinTok
	for i := 0; i < len(source); {
		if strings.HasPrefix(source[i:], wildcardTok) {
			tokens = append(tokens, pinTok{kind: 0})
			i += len(wildcardTok)
			continue
		}
		if source[i] == '[' {
			end := i + 1
			if end < len(source) && source[end] == '^' {
				end++
			}
			if end < len(source) && source[end] == ']' {
				end++
			}
			for end < len(source) && source[end] != ']' {
				if source[end] == '\\' {
					end += 2
					if end > len(source) {
						end = len(source)
					}
				} else {
					end++
				}
			}
			if end < len(source) {
				end++
			}
			tokens = append(tokens, pinTok{kind: 1, s: source[i:end]})
			i = end
			continue
		}
		if source[i] == '\\' {
			end := i + 2
			if end > len(source) {
				end = len(source)
			}
			tokens = append(tokens, pinTok{kind: 1, s: source[i:end]})
			i = end
			continue
		}
		if source[i] == '(' {
			end := i
			depth := 0
			for {
				if end < len(source) && source[end] == '\\' {
					end++
				} else if end < len(source) && source[end] == '(' {
					depth++
				} else if end < len(source) && source[end] == ')' {
					depth--
				}
				end++
				if end >= len(source) || depth <= 0 {
					break
				}
			}
			if end < len(source) && (source[end] == '*' || source[end] == '+' || source[end] == '?') {
				end++
			}
			if end > len(source) {
				end = len(source)
			}
			tokens = append(tokens, pinTok{kind: 2, s: source[i:end]})
			i = end
			continue
		}
		if source[i] == '^' || source[i] == '$' {
			tokens = append(tokens, pinTok{kind: 2, s: source[i : i+1]})
			i++
			continue
		}
		_, w := utf8.DecodeRuneInString(source[i:])
		tokens = append(tokens, pinTok{kind: 1, s: source[i : i+w]})
		i += w
	}
	var out strings.Builder
	var run []pinTok
	flush := func() {
		last := -1
		for at, t := range run {
			if t.kind == 0 {
				last = at
			}
		}
		for at, t := range run {
			if t.kind != 0 {
				out.WriteString(t.s)
				continue
			}
			if at == last {
				out.WriteString(wildcardTok)
				continue
			}
			next := "undefined"
			if at+1 < len(run) && run[at+1].kind == 1 {
				next = run[at+1].s
			}
			out.WriteString(`(?:(?!`)
			out.WriteString(next)
			out.WriteString(`)[^\/])*`)
		}
		run = run[:0]
	}
	for _, t := range tokens {
		if t.kind != 2 {
			run = append(run, t)
			continue
		}
		flush()
		out.WriteString(t.s)
	}
	flush()
	return out.String()
}

func compileBody(body string, ignoreCase bool) (src string, re *jsregex.Regexp) {
	src = pinWildcards(expandMarker(makeRegexPrefix(body)))
	flags := ""
	if ignoreCase {
		flags = "i"
	}
	re, err := jsregex.Compile(src, flags)
	if err != nil {
		panic("ignore: " + src + ": " + err.Error())
	}
	return src, re
}
