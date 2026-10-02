package mermaid

import "strings"

func flushStatement(cur string, out *[]string) string {
	trimmed := jsTrim(cur)
	if trimmed != "" {
		*out = append(*out, trimmed)
	}
	return ""
}

// splitStatements splits one source line on ';', stopping at a %% comment.
// Quoted spans are opaque.
func splitStatements(line string, out *[]string) {
	chars := []rune(line)
	cur := ""
	inQuotes := false
	for i := 0; i < len(chars); i++ {
		c := chars[i]
		if inQuotes {
			if c == '"' {
				inQuotes = false
			}
			cur += string(c)
		} else if c == '"' {
			inQuotes = true
			cur += string(c)
		} else if c == '%' && i+1 < len(chars) && chars[i+1] == '%' {
			break
		} else if c == ';' {
			cur = flushStatement(cur, out)
		} else {
			cur += string(c)
		}
	}
	flushStatement(cur, out)
}

func statementsOf(src string) []string {
	var out []string
	for _, line := range srcLines(src) {
		splitStatements(line, &out)
	}
	return out
}

func headerKind(statements []string) string {
	if len(statements) == 0 {
		return ""
	}
	kind := firstWord(statements[0])
	if kind == "" {
		return ""
	}
	return asciiLower(kind)
}

// DiagramKind reports the diagram type src declares, or "" if the header names
// no type this renderer draws. It reads the header only.
func DiagramKind(src string) string {
	kind := headerKind(statementsOf(src))
	switch {
	case kind == "":
		return ""
	case kind == "graph" || kind == "flowchart":
		return KindFlowchart
	case strings.HasPrefix(kind, "statediagram"):
		return KindState
	case strings.HasPrefix(kind, "classdiagram"):
		return KindClass
	case kind == "erdiagram":
		return KindER
	case kind == "sequencediagram":
		return KindSequence
	default:
		return ""
	}
}

func parseGraph(src string) *Graph {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind != "graph" && kind != "flowchart" {
		return nil
	}
	dirTok := "TB"
	if ws := words(statements[0]); len(ws) > 1 {
		dirTok = ws[1]
	}
	graph := newGraph(parseDir(dirTok))
	var stack []int
	for _, st := range statements[1:] {
		switch asciiLower(firstWord(st)) {
		case "subgraph":
			if len(graph.groups) >= maxGroups || len(stack) >= maxGroupDepth {
				return nil
			}
			id, label := parseSubgraphDecl(jsTrim(st[len("subgraph"):]))
			parent := -1
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			graph.groups = append(graph.groups, Group{ID: id, Label: label, Parent: parent})
			stack = append(stack, len(graph.groups)-1)
			graph.curGroup = stack[len(stack)-1]
			continue
		case "end":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				graph.curGroup = -1
			} else {
				graph.curGroup = stack[len(stack)-1]
			}
			continue
		case "classdef", "class", "style", "linkstyle", "click", "direction":
			continue
		}
		parseStatement(st, graph)
		if graph.overCap {
			return nil
		}
	}
	if len(graph.nodes) == 0 {
		return nil
	}
	return graph
}

func parseSubgraphDecl(rest string) (string, string) {
	if strings.HasPrefix(rest, `"`) {
		close := strings.Index(rest[1:], `"`)
		if close != -1 {
			label := rest[1 : 1+close]
			return label, decodeHTMLEntities(label)
		}
	}
	if open := strings.Index(rest, "["); open != -1 {
		id := jsTrim(rest[:open])
		// Strip a trailing run of ']' then trim, matching the TS replace/trim order.
		label := cleanLabel(jsTrim(trimTrailing(rest[open+1:], ']')))
		if id != "" && label != "" {
			return id, label
		}
	}
	return rest, rest
}

func trimTrailing(s string, r byte) string {
	for len(s) > 0 && s[len(s)-1] == r {
		s = s[:len(s)-1]
	}
	return s
}

func parseStatement(st string, graph *Graph) {
	chars := []rune(st)
	head := parseNodeGroup(chars, 0, graph)
	if head == nil {
		graph.warnings = append(graph.warnings, `dropped, does not start with a node: "`+st+`"`)
		return
	}
	prev := head.group
	i := head.next
	for {
		i = skipSpaces(chars, i)
		if i >= len(chars) {
			break
		}
		link := parseLink(chars, i)
		if link == nil {
			graph.warnings = append(graph.warnings, `dropped, expected a link: "`+string(chars[i:])+`"`)
			break
		}
		i = skipSpaces(chars, link.next)
		target := parseNodeGroup(chars, i, graph)
		if target == nil {
			graph.warnings = append(graph.warnings, `dropped, link has no target: "`+st+`"`)
			break
		}
		i = target.next
		for _, f := range prev {
			for _, t := range target.group {
				reversed := link.left == headArrow && link.right != headArrow
				edge := Edge{Label: link.label, Line: link.line}
				if reversed {
					edge.From, edge.To = t, f
					edge.HeadTo = headArrow
					edge.HeadFrom = link.right
				} else {
					edge.From, edge.To = f, t
					edge.HeadTo = link.right
					edge.HeadFrom = link.left
				}
				if !graph.pushEdge(edge) {
					return
				}
			}
		}
		prev = target.group
	}
}

type nodeGroup struct {
	group []int
	next  int
}

func parseNodeGroup(chars []rune, start int, graph *Graph) *nodeGroup {
	first := parseNode(chars, start, graph)
	if first == nil {
		return nil
	}
	group := []int{first.index}
	i := first.next
	for {
		j := skipSpaces(chars, i)
		if runeAt(chars, j) != '&' {
			break
		}
		next := parseNode(chars, j+1, graph)
		if next == nil {
			return nil
		}
		group = append(group, next.index)
		i = next.next
	}
	return &nodeGroup{group: group, next: i}
}

func skipSpaces(chars []rune, i int) int {
	for i < len(chars) && (chars[i] == ' ' || chars[i] == '\t') {
		i++
	}
	return i
}

func runeAt(chars []rune, i int) rune {
	if i < 0 || i >= len(chars) {
		return -1
	}
	return chars[i]
}

type parsedNode struct {
	index int
	next  int
}

func parseNode(chars []rune, start int, graph *Graph) *parsedNode {
	i := skipSpaces(chars, start)
	idStart := i
	for i < len(chars) && isIDChar(chars[i]) {
		i++
	}
	if i == idStart {
		return nil
	}
	id := string(chars[idStart:i])
	shaped := readShapeAt(chars, i)
	if shaped.unclosed != "" {
		graph.warnings = append(graph.warnings, "node \""+id+"\": label is missing its closing `"+shaped.unclosed+"`")
	}
	index, ok := graph.nodeIndex(id, shaped.label, shaped.shape)
	if !ok {
		return nil
	}
	next := shaped.after
	if runeAt(chars, next) == ':' && runeAt(chars, next+1) == ':' && runeAt(chars, next+2) == ':' {
		k := next + 3
		for k < len(chars) && (isIDChar(chars[k]) || chars[k] == '-') {
			k++
		}
		for k > next+3 && chars[k-1] == '-' {
			k--
		}
		if k > next+3 {
			next = k
		}
	}
	return &parsedNode{index: index, next: next}
}

type shaped struct {
	shape    Shape
	label    *string
	after    int
	unclosed string
}

func readShapeAt(chars []rune, i int) shaped {
	c := runeAt(chars, i)
	n := runeAt(chars, i+1)
	switch c {
	case '[':
		if n == '[' {
			return readShape(chars, i+2, "]]", shapeRect)
		}
		if n == '(' {
			return readShape(chars, i+2, ")]", shapeRound)
		}
		return readShape(chars, i+1, "]", shapeRect)
	case '(':
		if n == '(' {
			return readShape(chars, i+2, "))", shapeRound)
		}
		if n == '[' {
			return readShape(chars, i+2, "])", shapeRound)
		}
		return readShape(chars, i+1, ")", shapeRound)
	case '{':
		if n == '{' {
			return readShape(chars, i+2, "}}", shapeDiamond)
		}
		return readShape(chars, i+1, "}", shapeDiamond)
	case '>':
		return readShape(chars, i+1, "]", shapeRect)
	default:
		return shaped{shape: shapeRect, after: i}
	}
}

func readShape(chars []rune, start int, closer string, shape Shape) shaped {
	j := start
	for runeAt(chars, j) == ' ' || runeAt(chars, j) == '\t' {
		j++
	}
	quoted := runeAt(chars, j) == '"'
	i := start
	text := ""
	inQuotes := false
	closerR := []rune(closer)
	for i < len(chars) {
		c := chars[i]
		if quoted && c == '"' {
			inQuotes = !inQuotes
			text += string(c)
			i++
			continue
		}
		if !inQuotes && hasPrefixRunes(chars[i:], closerR) {
			lab := cleanLabel(text)
			return shaped{shape: shape, label: &lab, after: i + len(closerR)}
		}
		text += string(c)
		i++
	}
	lab := cleanLabel(text)
	return shaped{shape: shape, label: &lab, after: len(chars), unclosed: closer}
}

func hasPrefixRunes(chars, prefix []rune) bool {
	if len(chars) < len(prefix) {
		return false
	}
	for i, r := range prefix {
		if chars[i] != r {
			return false
		}
	}
	return true
}

func isLinkChar(c rune) bool {
	return c == '-' || c == '.' || c == '=' || c == '<' || c == '>'
}

type link struct {
	left, right Head
	line        LineKind
	label       *string
	next        int
}

func parseLink(chars []rune, start int) *link {
	i := skipSpaces(chars, start)
	left := headNone
	if (runeAt(chars, i) == 'o' || runeAt(chars, i) == 'x') &&
		(runeAt(chars, i+1) == '-' || runeAt(chars, i+1) == '.' || runeAt(chars, i+1) == '=') {
		if runeAt(chars, i) == 'o' {
			left = headCircle
		} else {
			left = headCross
		}
		i++
	}
	opStart := i
	for i < len(chars) && isLinkChar(chars[i]) {
		i++
	}
	if i == opStart {
		return nil
	}
	op1 := string(chars[opStart:i])
	if left == headNone && strings.HasPrefix(op1, "<") {
		left = headArrow
	}
	line := lineKind(op1)
	right := headNone
	if strings.Contains(op1, ">") {
		right = headArrow
	}
	if right == headNone {
		if tr := trailingHead(chars, i); tr != nil {
			right = tr.head
			i = tr.next
		}
	}
	if runeAt(chars, i) == '|' {
		i++
		lStart := i
		for i < len(chars) && chars[i] != '|' {
			i++
		}
		label := cleanLabel(string(chars[lStart:i]))
		if runeAt(chars, i) == '|' {
			i++
		}
		return &link{left: left, right: right, line: line, label: nonEmpty(label), next: i}
	}
	if right == headNone {
		textStart := skipSpaces(chars, i)
		j := textStart
		for j < len(chars) && !isLinkChar(chars[j]) {
			j++
		}
		if j < len(chars) && j > textStart && chars[j] != '<' {
			text := string(chars[textStart:j])
			op2Start := j
			for j < len(chars) && isLinkChar(chars[j]) {
				j++
			}
			op2 := string(chars[op2Start:j])
			if strings.Contains(op2, ">") {
				right = headArrow
			} else if tr := trailingHead(chars, j); tr != nil {
				right = tr.head
				j = tr.next
			}
			if line == lineSolid {
				line = lineKind(op2)
			}
			return &link{left: left, right: right, line: line, label: nonEmpty(cleanLabel(text)), next: j}
		}
	}
	return &link{left: left, right: right, line: line, next: i}
}

func lineKind(op string) LineKind {
	if strings.Contains(op, "=") {
		return lineThick
	}
	if strings.Contains(op, ".") {
		return lineDotted
	}
	return lineSolid
}

type trail struct {
	head Head
	next int
}

func trailingHead(chars []rune, i int) *trail {
	var head Head
	switch runeAt(chars, i) {
	case 'o':
		head = headCircle
	case 'x':
		head = headCross
	default:
		return nil
	}
	if i+1 >= len(chars) {
		return &trail{head: head, next: i + 1}
	}
	after := chars[i+1]
	if after == ' ' || after == '\t' || after == '|' || after == '&' || after == ';' {
		return &trail{head: head, next: i + 1}
	}
	return nil
}
