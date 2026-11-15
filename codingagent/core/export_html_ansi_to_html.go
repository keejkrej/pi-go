// Ported from packages/coding-agent/src/core/export-html/ansi-to-html.ts (pi v1.5.2).

package core

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// ehathAnsiColors defines the standard 16-color ANSI palette (indices 0-15).
var ehathAnsiColors = [16]string{
	"#000000", // 0: black
	"#800000", // 1: red
	"#008000", // 2: green
	"#808000", // 3: yellow
	"#000080", // 4: blue
	"#800080", // 5: magenta
	"#008080", // 6: cyan
	"#c0c0c0", // 7: white
	"#808080", // 8: bright black
	"#ff0000", // 9: bright red
	"#00ff00", // 10: bright green
	"#ffff00", // 11: bright yellow
	"#0000ff", // 12: bright blue
	"#ff00ff", // 13: bright magenta
	"#00ffff", // 14: bright cyan
	"#ffffff", // 15: bright white
}

var (
	// Match escape sequences: SGR (ESC[...m), other CSI sequences, OSC sequences, and APC sequences (terminated by BEL or ST).
	ehathAnsiRegex     = regexp.MustCompile(`\x1b\[([\d;]*)m|\x1b\[[0-?]*[ -/]*[@-~]|\x1b\]([^\x07\x1b]*)(?:\x07|\x1b\\)|\x1b_[^\x07\x1b]*(?:\x07|\x1b\\)`)
	ehathOsc8Regex     = regexp.MustCompile(`(?s)^8;[^;]*;(.*)$`)
	ehathSafeLinkRegex = regexp.MustCompile(`^(?i:https?:|mailto:)`)
	ehathControlChars  = regexp.MustCompile(`[\x00-\x1f\x7f]`)
)

func ehathColor256ToHex(index int) string {
	if index < 0 {
		index = 0
	}
	if index < 16 {
		return ehathAnsiColors[index]
	}
	if index < 232 {
		cubeIndex := index - 16
		r := cubeIndex / 36
		g := (cubeIndex % 36) / 6
		b := cubeIndex % 6
		toComponent := func(n int) int {
			if n == 0 {
				return 0
			}
			return 55 + n*40
		}
		return fmt.Sprintf("#%02x%02x%02x", toComponent(r), toComponent(g), toComponent(b))
	}
	if index > 255 {
		index = 255
	}
	gray := 8 + (index-232)*10
	return fmt.Sprintf("#%02x%02x%02x", gray, gray, gray)
}

// EscapeHtml escapes HTML special characters, matching TS escapeHtml (including &#039; for apostrophes).
func EscapeHtml(text string) string {
	// Note: html.EscapeString uses &#34; and &#39; whereas TS uses &quot; and &#039;.
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#039;",
	)
	_ = html.EscapeString
	return replacer.Replace(text)
}

type ehathTextStyle struct {
	fg        string
	bg        string
	bold      bool
	dim       bool
	italic    bool
	underline bool
}

func (s *ehathTextStyle) hasStyle() bool {
	return s.fg != "" || s.bg != "" || s.bold || s.dim || s.italic || s.underline
}

func (s *ehathTextStyle) toInlineCSS() string {
	var parts []string
	if s.fg != "" {
		parts = append(parts, "color:"+s.fg)
	}
	if s.bg != "" {
		parts = append(parts, "background-color:"+s.bg)
	}
	if s.bold {
		parts = append(parts, "font-weight:bold")
	}
	if s.dim {
		parts = append(parts, "opacity:0.6")
	}
	if s.italic {
		parts = append(parts, "font-style:italic")
	}
	if s.underline {
		parts = append(parts, "text-decoration:underline")
	}
	return strings.Join(parts, ";")
}

func ehathApplySgrCode(params []int, style *ehathTextStyle) {
	i := 0
	for i < len(params) {
		code := params[i]
		switch {
		case code == 0:
			*style = ehathTextStyle{}
		case code == 1:
			style.bold = true
		case code == 2:
			style.dim = true
		case code == 3:
			style.italic = true
		case code == 4:
			style.underline = true
		case code == 22:
			style.bold = false
			style.dim = false
		case code == 23:
			style.italic = false
		case code == 24:
			style.underline = false
		case code >= 30 && code <= 37:
			style.fg = ehathAnsiColors[code-30]
		case code == 38:
			if i+2 < len(params) && params[i+1] == 5 {
				style.fg = ehathColor256ToHex(params[i+2])
				i += 2
			} else if i+4 < len(params) && params[i+1] == 2 {
				style.fg = fmt.Sprintf("rgb(%d,%d,%d)", params[i+2], params[i+3], params[i+4])
				i += 4
			}
		case code == 39:
			style.fg = ""
		case code >= 40 && code <= 47:
			style.bg = ehathAnsiColors[code-40]
		case code == 48:
			if i+2 < len(params) && params[i+1] == 5 {
				style.bg = ehathColor256ToHex(params[i+2])
				i += 2
			} else if i+4 < len(params) && params[i+1] == 2 {
				style.bg = fmt.Sprintf("rgb(%d,%d,%d)", params[i+2], params[i+3], params[i+4])
				i += 4
			}
		case code == 49:
			style.bg = ""
		case code >= 90 && code <= 97:
			style.fg = ehathAnsiColors[code-90+8]
		case code >= 100 && code <= 107:
			style.bg = ehathAnsiColors[code-100+8]
		}
		i++
	}
}

// StripAnsiEscapes removes all escape sequences matched by the converter, plus stray ESC characters.
func StripAnsiEscapes(text string) string {
	stripped := ehathAnsiRegex.ReplaceAllString(text, "")
	return strings.ReplaceAll(stripped, "\x1b", "")
}

// AnsiToHtml converts ANSI-escaped text to HTML with inline styles.
func AnsiToHtml(text string) string {
	var style ehathTextStyle
	var b strings.Builder
	lastIndex := 0
	inSpan := false
	inLink := false

	closeSpan := func() {
		if inSpan {
			b.WriteString("</span>")
			inSpan = false
		}
	}
	openSpan := func() {
		if style.hasStyle() {
			b.WriteString(`<span style="`)
			b.WriteString(style.toInlineCSS())
			b.WriteString(`">`)
			inSpan = true
		}
	}

	matches := ehathAnsiRegex.FindAllStringSubmatchIndex(text, -1)
	for _, m := range matches {
		if m[0] > lastIndex {
			before := strings.ReplaceAll(text[lastIndex:m[0]], "\x1b", "")
			if before != "" {
				b.WriteString(EscapeHtml(before))
			}
		}

		if m[2] >= 0 {
			// SGR sequence (group 1)
			paramStr := text[m[2]:m[3]]
			var params []int
			if paramStr == "" {
				params = []int{0}
			} else {
				rawParts := strings.Split(paramStr, ";")
				params = make([]int, len(rawParts))
				for idx, p := range rawParts {
					n, err := strconv.Atoi(p)
					if err != nil {
						n = 0
					}
					params[idx] = n
				}
			}
			closeSpan()
			ehathApplySgrCode(params, &style)
			openSpan()
		} else if m[4] >= 0 {
			// OSC sequence (group 2)
			oscPayload := text[m[4]:m[5]]
			if linkMatch := ehathOsc8Regex.FindStringSubmatch(oscPayload); linkMatch != nil {
				url := ehathControlChars.ReplaceAllString(strings.TrimSpace(linkMatch[1]), "")
				closeSpan()
				if inLink {
					b.WriteString("</a>")
					inLink = false
				}
				if url != "" && ehathSafeLinkRegex.MatchString(url) {
					b.WriteString(`<a href="`)
					b.WriteString(EscapeHtml(url))
					b.WriteString(`">`)
					inLink = true
				}
				openSpan()
			}
		}

		lastIndex = m[1]
	}

	if lastIndex < len(text) {
		remaining := strings.ReplaceAll(text[lastIndex:], "\x1b", "")
		if remaining != "" {
			b.WriteString(EscapeHtml(remaining))
		}
	}

	closeSpan()
	if inLink {
		b.WriteString("</a>")
	}

	return b.String()
}

// AnsiLinesToHtml converts an array of ANSI-escaped lines to HTML, wrapping each line in <div class="ansi-line">.
func AnsiLinesToHtml(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(`<div class="ansi-line">`)
		converted := AnsiToHtml(line)
		if converted == "" {
			b.WriteString("&nbsp;")
		} else {
			b.WriteString(converted)
		}
		b.WriteString(`</div>`)
	}
	return b.String()
}
