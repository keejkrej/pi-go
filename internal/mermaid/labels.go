package mermaid

import "strings"

const (
	wrapWidth = 24
	maxLines  = 4
	maxLabel  = 28
)

var labelBreakChars = []string{"_", "-", ".", "/"}

// stripControls removes C0/C1 controls other than tab, LF and CR.
func stripControls(src string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		if r >= 0x7f && r <= 0x9f {
			return -1
		}
		return r
	}, src)
}

// srcLines splits like Rust str::lines: on \n, stripping a trailing \r, and
// without a final empty line when the input ends in a newline.
func srcLines(src string) []string {
	out := strings.Split(src, "\n")
	for i, l := range out {
		if strings.HasSuffix(l, "\r") {
			out[i] = l[:len(l)-1]
		}
	}
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func isIDChar(r rune) bool { return r == '_' || isAlnumRune(r) }

const entityLookahead = 10

var namedEntities = map[string]string{
	"lt":   "<",
	"gt":   ">",
	"amp":  "&",
	"quot": `"`,
	"apos": "'",
}

func decodeEntityBody(body string) (string, bool) {
	if named, ok := namedEntities[body]; ok {
		return named, true
	}
	if !strings.HasPrefix(body, "#") {
		return "", false
	}
	num := body[1:]
	hex := strings.HasPrefix(num, "x") || strings.HasPrefix(num, "X")
	digits := num
	if hex {
		digits = num[1:]
	}
	if digits == "" {
		return "", false
	}
	base := 10
	if hex {
		base = 16
		for _, r := range digits {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return "", false
			}
		}
	} else {
		for _, r := range digits {
			if r < '0' || r > '9' {
				return "", false
			}
		}
	}
	code := 0
	for _, r := range digits {
		code *= base
		switch {
		case r >= '0' && r <= '9':
			code += int(r - '0')
		case r >= 'a' && r <= 'f':
			code += int(r-'a') + 10
		default:
			code += int(r-'A') + 10
		}
		if code > 0x10ffff {
			return "", false
		}
	}
	if code > 0x10ffff || (code >= 0xd800 && code <= 0xdfff) {
		return "", false
	}
	if code < 0x20 || (code >= 0x7f && code <= 0x9f) {
		return "", false
	}
	return string(rune(code)), true
}

func decodeHTMLEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	chars := []rune(s)
	var b strings.Builder
	for i := 0; i < len(chars); {
		if chars[i] != '&' {
			b.WriteRune(chars[i])
			i++
			continue
		}
		hi := i + 1 + entityLookahead
		if hi > len(chars) {
			hi = len(chars)
		}
		semi := -1
		for j := i + 1; j < hi; j++ {
			if chars[j] == ';' {
				semi = j
				break
			}
		}
		if semi < 0 {
			b.WriteByte('&')
			i++
			continue
		}
		decoded, ok := decodeEntityBody(string(chars[i+1 : semi]))
		if !ok {
			b.WriteByte('&')
			i++
			continue
		}
		b.WriteString(decoded)
		i = semi + 1
	}
	return b.String()
}

func stripMarkdown(s string) string {
	chars := make([]rune, 0, len(s))
	for _, r := range s {
		if r != '`' {
			chars = append(chars, r)
		}
	}
	noCode := string(chars)
	noStrong := strings.ReplaceAll(strings.ReplaceAll(noCode, "**", ""), "__", "")
	strong := []rune(noStrong)
	var b strings.Builder
	for i, c := range strong {
		inWord := i > 0 && isAlnumRune(strong[i-1]) && i+1 < len(strong) && isAlnumRune(strong[i+1])
		if (c == '*' || c == '_') && !inWord {
			continue
		}
		b.WriteRune(c)
	}
	return jsTrim(b.String())
}

var htmlFormatTags = map[string]struct{}{
	"b": {}, "strong": {}, "i": {}, "em": {}, "u": {}, "s": {}, "strike": {},
	"del": {}, "ins": {}, "mark": {}, "small": {}, "big": {}, "sub": {}, "sup": {},
	"code": {}, "kbd": {}, "samp": {}, "var": {}, "tt": {}, "span": {}, "font": {},
	"q": {}, "abbr": {}, "cite": {}, "pre": {},
}

func asciiAlnum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func htmlTagAt(chars []rune, start int) (name string, end int, ok bool) {
	i := start + 1
	if i < len(chars) && chars[i] == '/' {
		i++
	}
	nameStart := i
	for i < len(chars) && asciiAlnum(chars[i]) {
		i++
	}
	if i == nameStart {
		return "", 0, false
	}
	name = string(chars[nameStart:i])
	for i < len(chars) && chars[i] != '>' {
		if chars[i] == '<' {
			return "", 0, false
		}
		i++
	}
	if i >= len(chars) || chars[i] != '>' {
		return "", 0, false
	}
	return name, i + 1, true
}

func stripHTMLTags(s string) string {
	chars := []rune(s)
	var b strings.Builder
	for i := 0; i < len(chars); {
		if chars[i] == '<' {
			if name, end, ok := htmlTagAt(chars, i); ok {
				lower := asciiLower(name)
				if lower == "br" {
					b.WriteByte(' ')
					i = end
					continue
				}
				if _, format := htmlFormatTags[lower]; format {
					i = end
					continue
				}
			}
		}
		b.WriteRune(chars[i])
		i++
	}
	return b.String()
}

func unwrap(s, open, close string) (string, bool) {
	if len(s) >= len(open)+len(close) && strings.HasPrefix(s, open) && strings.HasSuffix(s, close) {
		return s[len(open) : len(s)-len(close)], true
	}
	return "", false
}

func cleanLabel(raw string) string {
	trimmed := jsTrim(stripHTMLTags(jsTrim(raw)))
	unquoted := trimmed
	if inner, ok := unwrap(trimmed, `"`, `"`); ok {
		unquoted = inner
	} else if inner, ok := unwrap(trimmed, "'", "'"); ok {
		unquoted = inner
	}
	unquoted = jsTrim(unquoted)
	if inner, ok := unwrap(unquoted, "`", "`"); ok {
		return decodeHTMLEntities(stripMarkdown(jsTrim(inner)))
	}
	return decodeHTMLEntities(unquoted)
}

func lastBreak(s string) int {
	best := -1
	for _, c := range labelBreakChars {
		if i := strings.LastIndex(s, c); i > best {
			best = i
		}
	}
	return best
}

func wrapLabel(label string, width, maxN int) []string {
	if width < 1 {
		width = 1
	}
	var lines []string
	cur := ""
	curW := 0
	for _, word := range words(label) {
		ww := stringWidth(word)
		if ww > width {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			chunk := ""
			chunkW := 0
			for _, ch := range measured(word) {
				if chunkW+ch.w > width && chunk != "" {
					p := lastBreak(chunk)
					carry := ""
					if p != -1 {
						carry = chunk[p+1:]
						lines = append(lines, chunk[:p+1])
					} else {
						lines = append(lines, chunk)
					}
					chunk = carry
					chunkW = stringWidth(carry)
				}
				chunk += ch.s
				chunkW += ch.w
			}
			cur = chunk
			curW = chunkW
		} else if cur == "" {
			cur = word
			curW = ww
		} else if curW+1+ww <= width {
			cur += " " + word
			curW += 1 + ww
		} else {
			lines = append(lines, cur)
			cur = word
			curW = ww
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	if len(lines) > maxN {
		lines = lines[:maxN]
		target := width - 1
		if target < 1 {
			target = 1
		}
		s := ""
		sw := 0
		for _, ch := range measured(lines[len(lines)-1]) {
			if sw+ch.w > target {
				break
			}
			s += ch.s
			sw += ch.w
		}
		lines[len(lines)-1] = s + "…"
	}
	return lines
}

func fitLabel(label string, inner int) string {
	if stringWidth(label) <= inner {
		return label
	}
	out := ""
	used := 0
	for _, c := range measured(label) {
		if used+c.w+1 > inner {
			break
		}
		out += c.s
		used += c.w
	}
	return out + "…"
}
