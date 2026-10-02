package marked

import "strings"

func rtrim(str, c string, invert bool) string {
	rs := []rune(str)
	l := len(rs)
	if l == 0 || c == "" {
		return str
	}
	ch := []rune(c)[0]
	suff := 0
	for suff < l {
		curr := rs[l-suff-1]
		if curr == ch && !invert {
			suff++
		} else if curr != ch && invert {
			suff++
		} else {
			break
		}
	}
	return string(rs[:l-suff])
}

func trimTrailingBlankLines(str string, blank *jsRE) string {
	lines := strings.Split(str, "\n")
	end := len(lines) - 1
	for end >= 0 && blank.test(lines[end]) {
		end--
	}
	if len(lines)-end <= 2 {
		return str
	}
	return strings.Join(lines[:end+1], "\n")
}

func findClosingBracket(str, b string) int {
	br := []rune(b)
	if len(br) < 2 {
		return -1
	}
	rs := []rune(str)
	found := false
	for _, r := range rs {
		if r == br[1] {
			found = true
			break
		}
	}
	if !found {
		return -1
	}
	level := 0
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' {
			i++
		} else if rs[i] == br[0] {
			level++
		} else if rs[i] == br[1] {
			level--
			if level < 0 {
				return i
			}
		}
	}
	if level > 0 {
		return -2
	}
	return -1
}

func expandTabs(line string, indent int) string {
	col := indent
	var b strings.Builder
	for _, ch := range line {
		if ch == '\t' {
			added := 4 - (col % 4)
			b.WriteString(strings.Repeat(" ", added))
			col += added
			continue
		}
		b.WriteRune(ch)
		col++
	}
	return b.String()
}

func splitCells(tableRow string, count int, o *otherRules) []string {
	rs := []rune(tableRow)
	row := o.findPipe.replaceFunc(tableRow, func(c *caps) string {
		escaped := false
		curr := c.index
		for {
			curr--
			if curr < 0 || curr >= len(rs) || rs[curr] != '\\' {
				break
			}
			escaped = !escaped
		}
		if escaped {
			return "|"
		}
		return " |"
	})
	cells := strings.Split(row, " |")
	if len(cells) > 0 && jsTrim(cells[0]) == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && jsTrim(cells[len(cells)-1]) == "" {
		cells = cells[:len(cells)-1]
	}
	if count > 0 {
		if len(cells) > count {
			cells = cells[:count]
		}
		for len(cells) < count {
			cells = append(cells, "")
		}
	}
	for i := range cells {
		cells[i] = o.slashPipe.replace(jsTrim(cells[i]), "|")
	}
	return cells
}

func unescapePunct(s string, anyPunct *jsRE) string {
	if s == "" || anyPunct == nil {
		return s
	}
	return anyPunct.replace(s, "$1")
}
