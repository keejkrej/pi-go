package mermaid

import "strings"

type classOp struct {
	op               string
	headFrom, headTo Head
	line             LineKind
}

// Longest-first so `--|>` wins over `--`.
var classOps = []classOp{
	{"<|--", headTriangle, headNone, lineSolid},
	{"--|>", headNone, headTriangle, lineSolid},
	{"<|..", headTriangle, headNone, lineDotted},
	{"..|>", headNone, headTriangle, lineDotted},
	{"*--", headDiamondFill, headNone, lineSolid},
	{"--*", headNone, headDiamondFill, lineSolid},
	{"o--", headDiamondOpen, headNone, lineSolid},
	{"--o", headNone, headDiamondOpen, lineSolid},
	{"<--", headArrow, headNone, lineSolid},
	{"-->", headNone, headArrow, lineSolid},
	{"<..", headArrow, headNone, lineDotted},
	{"..>", headNone, headArrow, lineDotted},
	{"--", headNone, headNone, lineSolid},
	{"..", headNone, headNone, lineDotted},
}

const maxClassOp = 4

type classParsed struct {
	graph *Graph
	infos []ClassInfo
}

func parseClass(src string) *classParsed {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !strings.HasPrefix(kind, "classdiagram") {
		return nil
	}
	graph := newGraph(dirDown)
	var infos []ClassInfo
	sync := func() {
		for len(infos) < len(graph.nodes) {
			infos = append(infos, emptyClassInfo())
		}
	}
	declare := func(name string) (int, bool) {
		idx, ok := graph.nodeIndex(name, nil, shapeRect)
		sync()
		return idx, ok
	}
	curClass := -1
	for _, st := range statements[1:] {
		if curClass != -1 {
			if st == "}" {
				curClass = -1
			} else {
				pushMember(&infos[curClass], st)
			}
			continue
		}
		st = dropStyleTags(st)
		first := asciiLower(firstWord(st))
		if first == "direction" {
			tok := ""
			if ws := words(st); len(ws) > 1 {
				tok = ws[1]
			}
			graph.dir = parseDir(tok)
			continue
		}
		switch first {
		case "note", "callback", "click", "link", "style", "cssclass", "classdef", "namespace", "}":
			continue
		}
		if first == "class" {
			rest := jsTrim(st[len("class"):])
			open := strings.HasSuffix(rest, "{")
			name := rest
			if open {
				name = jsTrim(rest[:len(rest)-1])
			}
			if name == "" || hasSpace(name) {
				return nil
			}
			idx, ok := declare(name)
			if !ok {
				return nil
			}
			if open {
				curClass = idx
			}
			continue
		}
		if strings.HasPrefix(st, "<<") {
			lhs, rhs, ok := splitOnce(st[2:], ">>")
			if !ok {
				return nil
			}
			name := jsTrim(rhs)
			if name == "" || hasSpace(name) {
				return nil
			}
			idx, ok := declare(name)
			if !ok {
				return nil
			}
			ann := jsTrim(lhs)
			infos[idx].Annotation = &ann
			continue
		}
		if rel := parseClassRelation(st); rel != nil {
			f, ok := declare(rel.from)
			if !ok {
				return nil
			}
			t, ok := declare(rel.to)
			if !ok {
				return nil
			}
			if len(graph.edges) >= maxEdges {
				return nil
			}
			graph.edges = append(graph.edges, Edge{
				From: f, To: t, Label: rel.label,
				HeadTo: rel.headTo, HeadFrom: rel.headFrom, Line: rel.line,
			})
			continue
		}
		if lhs, rhs, ok := splitOnce(st, ":"); ok {
			id := jsTrim(lhs)
			text := jsTrim(rhs)
			if id == "" || hasSpace(id) || text == "" {
				return nil
			}
			idx, ok := declare(id)
			if !ok {
				return nil
			}
			pushMember(&infos[idx], text)
			continue
		}
		return nil
	}
	if len(graph.nodes) == 0 {
		return nil
	}
	sync()
	return &classParsed{graph: graph, infos: infos}
}

func pushMember(info *ClassInfo, raw string) {
	if strings.HasPrefix(raw, "<<") {
		if lhs, _, ok := splitOnce(raw[2:], ">>"); ok {
			ann := jsTrim(lhs)
			info.Annotation = &ann
		}
		return
	}
	member := decodeHTMLEntities(displayGenerics(jsTrim(raw)))
	list := &info.Attrs
	if strings.Contains(member, "(") {
		list = &info.Methods
	}
	if len(*list) < maxMembers {
		*list = append(*list, member)
	} else if len(*list) == maxMembers {
		*list = append(*list, "…")
	}
}

type classRelation struct {
	from, to         string
	headFrom, headTo Head
	line             LineKind
	label            *string
}

func parseClassRelation(st string) *classRelation {
	chars := []rune(st)
	type foundOp struct {
		pos              int
		op               string
		headFrom, headTo Head
		line             LineKind
	}
	var found *foundOp
outer:
	for pos := 0; pos < len(chars); pos++ {
		tail := string(chars[pos:min(pos+maxClassOp, len(chars))])
		for _, op := range classOps {
			if !strings.HasPrefix(tail, op.op) {
				continue
			}
			if strings.HasPrefix(op.op, "o") && pos > 0 && isIDChar(chars[pos-1]) {
				continue
			}
			opRunes := []rune(op.op)
			after := runeAt(chars, pos+len(opRunes))
			if strings.HasSuffix(op.op, "o") && after != -1 && isIDChar(after) {
				continue
			}
			found = &foundOp{pos, op.op, op.headFrom, op.headTo, op.line}
			break outer
		}
	}
	if found == nil {
		return nil
	}
	opRunes := []rune(found.op)
	lhsRaw := jsTrim(string(chars[:found.pos]))
	rhsRaw := jsTrim(string(chars[found.pos+len(opRunes):]))
	lhs, cardFrom := stripCardinalitySuffix(lhsRaw)
	rhs, cardTo := stripCardinalityPrefix(rhsRaw)
	toID := rhs
	var relLabel *string
	if l, r, ok := splitOnce(rhs, ":"); ok {
		toID = jsTrim(l)
		relLabel = nonEmpty(decodeHTMLEntities(jsTrim(r)))
	} else {
		toID = jsTrim(rhs)
	}
	if lhs == "" || toID == "" || hasSpace(lhs) || hasSpace(toID) {
		return nil
	}
	parts := make([]string, 0, 3)
	if cardFrom != "" {
		parts = append(parts, cardFrom)
	}
	if relLabel != nil {
		parts = append(parts, *relLabel)
	}
	if cardTo != "" {
		parts = append(parts, cardTo)
	}
	return &classRelation{
		from: lhs, to: toID,
		headFrom: found.headFrom, headTo: found.headTo, line: found.line,
		label: nonEmpty(strings.Join(parts, " ")),
	}
}

func stripCardinalitySuffix(s string) (string, string) {
	t := jsTrimRight(s)
	if strings.HasSuffix(t, `"`) {
		rest := t[:len(t)-1]
		if q := strings.LastIndex(rest, `"`); q != -1 {
			return jsTrimRight(rest[:q]), rest[q+1:]
		}
	}
	return t, ""
}

func stripCardinalityPrefix(s string) (string, string) {
	t := jsTrimLeft(s)
	if strings.HasPrefix(t, `"`) {
		rest := t[1:]
		if q := strings.Index(rest, `"`); q != -1 {
			return jsTrimLeft(rest[q+1:]), rest[:q]
		}
	}
	return t, ""
}

func displayGenerics(s string) string {
	var b strings.Builder
	open := false
	for _, c := range s {
		if c == '~' {
			if open {
				b.WriteByte('>')
			} else {
				b.WriteByte('<')
			}
			open = !open
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}
