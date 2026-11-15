// Ported from packages/coding-agent/src/modes/interactive/components/diff.ts (pi v1.5.2).

package theme

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/jsdiff"
)

// RenderDiffOptions configures diff syntax/word highlighting.
type RenderDiffOptions struct {
	// FilePath is kept for API compatibility.
	FilePath string
	MaxLines int
}

type diffParsedLine struct {
	prefix  string
	lineNum string
	content string
}

var diffLinePrefixRe = regexp.MustCompile(`^([+\-\s])(\s*\d*)\s(.*)$`)

func diffParseLine(line string) (diffParsedLine, bool) {
	m := diffLinePrefixRe.FindStringSubmatch(line)
	if len(m) != 4 {
		return diffParsedLine{}, false
	}
	return diffParsedLine{
		prefix:  m[1],
		lineNum: m[2],
		content: m[3],
	}, true
}

func diffReplaceTabs(text string) string {
	return strings.ReplaceAll(text, "\t", "   ")
}

func diffSplitLeadingWs(s string) (ws, rest string) {
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return s[:i], s[i:]
}

func diffRenderIntraLineDiff(t *Theme, oldContent, newContent string) (removedLine, addedLine string) {
	commonIndentLen := 0
	for commonIndentLen < len(oldContent) && commonIndentLen < len(newContent) {
		ro, so := utf8.DecodeRuneInString(oldContent[commonIndentLen:])
		rn, sn := utf8.DecodeRuneInString(newContent[commonIndentLen:])
		if ro != rn || so != sn || !unicode.IsSpace(ro) {
			break
		}
		commonIndentLen += so
	}

	commonIndent := oldContent[:commonIndentLen]
	wordDiff := jsdiff.DiffWords(oldContent[commonIndentLen:], newContent[commonIndentLen:], nil)

	var remB, addB strings.Builder
	remB.WriteString(commonIndent)
	addB.WriteString(commonIndent)
	isFirstRemoved := true
	isFirstAdded := true

	for _, part := range wordDiff {
		if part.Removed {
			value := part.Value
			if isFirstRemoved {
				leadingWs, trimmed := diffSplitLeadingWs(value)
				remB.WriteString(leadingWs)
				value = trimmed
				isFirstRemoved = false
			}
			if value != "" {
				remB.WriteString(t.Inverse(value))
			}
		} else if part.Added {
			value := part.Value
			if isFirstAdded {
				leadingWs, trimmed := diffSplitLeadingWs(value)
				addB.WriteString(leadingWs)
				value = trimmed
				isFirstAdded = false
			}
			if value != "" {
				addB.WriteString(t.Inverse(value))
			}
		} else {
			remB.WriteString(part.Value)
			addB.WriteString(part.Value)
		}
	}

	return remB.String(), addB.String()
}

// RenderDiff renders a diff string with colored lines and intra-line change highlighting
// (only when a removed hunk has 1 line and its adjacent added hunk has 1 line).
func RenderDiff(diffText string, _ ...RenderDiffOptions) string {
	t := CurrentTheme()
	lines := strings.Split(diffText, "\n")
	result := make([]string, 0, len(lines))

	for i := 0; i < len(lines); {
		line := lines[i]
		parsed, ok := diffParseLine(line)
		if !ok {
			result = append(result, t.Fg(FgToolDiffContext, line))
			i++
			continue
		}

		if parsed.prefix == "-" {
			var removedLines []diffParsedLine
			for i < len(lines) {
				p, ok := diffParseLine(lines[i])
				if !ok || p.prefix != "-" {
					break
				}
				removedLines = append(removedLines, p)
				i++
			}

			var addedLines []diffParsedLine
			for i < len(lines) {
				p, ok := diffParseLine(lines[i])
				if !ok || p.prefix != "+" {
					break
				}
				addedLines = append(addedLines, p)
				i++
			}

			if len(removedLines) == 1 && len(addedLines) == 1 {
				removed := removedLines[0]
				added := addedLines[0]
				removedLine, addedLine := diffRenderIntraLineDiff(
					t,
					diffReplaceTabs(removed.content),
					diffReplaceTabs(added.content),
				)
				result = append(result, t.Fg(FgToolDiffRemoved, "-"+removed.lineNum+" "+removedLine))
				result = append(result, t.Fg(FgToolDiffAdded, "+"+added.lineNum+" "+addedLine))
			} else {
				for _, removed := range removedLines {
					result = append(result, t.Fg(FgToolDiffRemoved, "-"+removed.lineNum+" "+diffReplaceTabs(removed.content)))
				}
				for _, added := range addedLines {
					result = append(result, t.Fg(FgToolDiffAdded, "+"+added.lineNum+" "+diffReplaceTabs(added.content)))
				}
			}
		} else if parsed.prefix == "+" {
			result = append(result, t.Fg(FgToolDiffAdded, "+"+parsed.lineNum+" "+diffReplaceTabs(parsed.content)))
			i++
		} else {
			result = append(result, t.Fg(FgToolDiffContext, " "+parsed.lineNum+" "+diffReplaceTabs(parsed.content)))
			i++
		}
	}

	return strings.Join(result, "\n")
}
