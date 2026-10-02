package mermaid

import "strings"

func parseEr(src string) *classParsed {
	statements := statementsOf(src)
	if headerKind(statements) != "erdiagram" {
		return nil
	}
	graph := newGraph(dirDown)
	var infos []ClassInfo
	curEntity := -1
	for _, st := range statements[1:] {
		if curEntity != -1 {
			if st == "}" {
				curEntity = -1
			} else {
				pushErAttribute(&infos[curEntity], st)
			}
			continue
		}
		if rel := splitErRelationship(st); rel != nil {
			tokens := words(rel.rel)
			if len(tokens) != 3 {
				return nil
			}
			op := parseErOp(tokens[1])
			if op == nil {
				return nil
			}
			f, ok := erEntity(graph, &infos, tokens[0])
			if !ok {
				return nil
			}
			t, ok := erEntity(graph, &infos, tokens[2])
			if !ok {
				return nil
			}
			if len(graph.edges) >= maxEdges {
				return nil
			}
			relLabel := ""
			if rel.label != nil {
				relLabel = cleanLabel(*rel.label)
			}
			parts := make([]string, 0, 3)
			if op.cardL != "" {
				parts = append(parts, op.cardL)
			}
			if relLabel != "" {
				parts = append(parts, relLabel)
			}
			if op.cardR != "" {
				parts = append(parts, op.cardR)
			}
			graph.edges = append(graph.edges, Edge{
				From: f, To: t, Label: nonEmpty(strings.Join(parts, " ")),
				HeadTo: headNone, HeadFrom: headNone, Line: op.line,
			})
			continue
		}
		open := strings.HasSuffix(st, "{")
		decl := st
		if open {
			decl = jsTrim(st[:len(st)-1])
		}
		if decl == "" || len(words(decl)) != 1 {
			return nil
		}
		idx, ok := erEntity(graph, &infos, decl)
		if !ok {
			return nil
		}
		if open {
			curEntity = idx
		}
	}
	if len(graph.nodes) == 0 {
		return nil
	}
	for len(infos) < len(graph.nodes) {
		infos = append(infos, emptyClassInfo())
	}
	return &classParsed{graph: graph, infos: infos}
}

func erEntity(graph *Graph, infos *[]ClassInfo, token string) (int, bool) {
	var idx int
	var ok bool
	if open := strings.Index(token, "["); open != -1 {
		id := token[:open]
		label := cleanLabel(trimTrailing(token[open+1:], ']'))
		if id == "" || label == "" {
			return 0, false
		}
		idx, ok = graph.nodeLabel(id, label)
	} else {
		idx, ok = graph.nodeIndex(token, nil, shapeRect)
	}
	if !ok {
		return 0, false
	}
	for len(*infos) < len(graph.nodes) {
		*infos = append(*infos, emptyClassInfo())
	}
	return idx, true
}

type erRel struct {
	rel   string
	label *string
}

func splitErRelationship(st string) *erRel {
	rel := st
	var label *string
	if l, r, ok := splitOnce(st, ":"); ok {
		rel = l
		trimmed := jsTrim(r)
		label = &trimmed
	}
	for _, t := range words(rel) {
		if parseErOp(t) != nil {
			return &erRel{rel: rel, label: label}
		}
	}
	return nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}

type erOp struct {
	cardL, cardR string
	line         LineKind
}

func parseErOp(tok string) *erOp {
	if len(tok) != 6 || !isASCII(tok) {
		return nil
	}
	mid := tok[2:4]
	var line LineKind
	switch mid {
	case "--":
		line = lineSolid
	case "..":
		line = lineDotted
	default:
		return nil
	}
	cardL := erCard(tok[:2])
	cardR := erCard(tok[4:])
	if cardL == "" || cardR == "" {
		return nil
	}
	return &erOp{cardL: cardL, cardR: cardR, line: line}
}

func erCard(tok string) string {
	switch tok {
	case "|o", "o|":
		return "0..1"
	case "||":
		return "1"
	case "}o", "o{":
		return "0..*"
	case "}|", "|{":
		return "1..*"
	default:
		return ""
	}
}

func pushErAttribute(info *ClassInfo, raw string) {
	var parts []string
	for _, tok := range words(raw) {
		if strings.HasPrefix(tok, `"`) {
			break
		}
		parts = append(parts, tok)
	}
	if len(parts) == 0 {
		return
	}
	line := decodeHTMLEntities(strings.Join(parts, " "))
	if len(info.Attrs) < maxMembers {
		info.Attrs = append(info.Attrs, line)
	} else if len(info.Attrs) == maxMembers {
		info.Attrs = append(info.Attrs, "…")
	}
}
