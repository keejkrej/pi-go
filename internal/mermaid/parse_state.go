package mermaid

import "strings"

func parseState(src string) *Graph {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !strings.HasPrefix(kind, "statediagram") {
		return nil
	}
	graph := newGraph(dirDown)
	inNote := false
	for _, st := range statements[1:] {
		if inNote {
			if asciiLower(st) == "end note" {
				inNote = false
			}
			continue
		}
		st = dropStyleTags(st)
		first := asciiLower(firstWord(st))
		switch {
		case first == "direction":
			tok := ""
			if ws := words(st); len(ws) > 1 {
				tok = ws[1]
			}
			graph.dir = parseDir(tok)
		case first == "note":
			if !strings.Contains(st, ":") {
				inNote = true
			}
		case first == "state":
			if !parseStateDecl(st, graph) {
				return nil
			}
		case first == "classdef" || first == "class" || first == "hide" || first == "scale" || first == "}" || first == "--":
			// styling and composite-state punctuation
		case strings.Contains(st, "-->"):
			if !parseTransition(st, graph) {
				return nil
			}
		default:
			if !parseStateDesc(st, graph) {
				return nil
			}
		}
		if graph.overCap {
			return nil
		}
	}
	if len(graph.nodes) == 0 {
		return nil
	}
	return graph
}

func parseStateDecl(st string, graph *Graph) bool {
	// trim, drop one trailing `{`, trim again.
	rest := jsTrim(st[len("state"):])
	if strings.HasSuffix(rest, "{") {
		rest = jsTrim(rest[:len(rest)-1])
	}
	if rest == "" {
		return true
	}
	if strings.HasPrefix(rest, `"`) {
		close := strings.Index(rest[1:], `"`)
		if close < 0 {
			return false
		}
		label := rest[1 : 1+close]
		after := jsTrim(rest[1+close+1:])
		id := label
		if strings.HasPrefix(after, "as") {
			id = jsTrim(after[2:])
		}
		_, ok := graph.nodeLabel(id, decodeHTMLEntities(label))
		return ok
	}
	shape := shapeRound
	id := rest
	stereotyped := false
	if pos := strings.Index(rest, "<<"); pos != -1 {
		stereo := strings.TrimSuffix(rest[pos+2:], ">>")
		stereo = jsTrim(stereo)
		if stereo == "choice" {
			shape = shapeDiamond
		}
		id = jsTrim(rest[:pos])
		stereotyped = true
	}
	if id == "" || hasSpace(id) {
		return false
	}
	var label *string
	if stereotyped {
		label = &id
	}
	_, ok := graph.nodeIndex(id, label, shape)
	return ok
}

func parseTransition(st string, graph *Graph) bool {
	rest := st
	prev := -1
	hasPrev := false
	for {
		lhs, rhs, ok := splitOnce(rest, "-->")
		if !ok {
			break
		}
		fromID := stateFromID(lhs)
		var from int
		if hasPrev {
			if fromID != "" {
				return false
			}
			from = prev
		} else {
			if fromID == "" {
				return false
			}
			f, ok := stateEndpoint(graph, fromID, true)
			if !ok {
				return false
			}
			from = f
		}
		nextArrow := strings.Index(rhs, "-->")
		toPartRaw := rhs
		tail := ""
		if nextArrow != -1 {
			toPartRaw = rhs[:nextArrow]
			tail = rhs[nextArrow:]
		}
		var label *string
		toPart := toPartRaw
		if colonL, colonR, ok := splitOnce(toPartRaw, ":"); ok {
			toPart = colonL
			label = nonEmpty(decodeHTMLEntities(jsTrim(colonR)))
		}
		toID := stateToID(toPart)
		if toID == "" {
			return false
		}
		to, ok := stateEndpoint(graph, toID, false)
		if !ok {
			return false
		}
		if !graph.pushEdge(Edge{From: from, To: to, Label: label, HeadTo: headArrow, HeadFrom: headNone, Line: lineSolid}) {
			return true
		}
		prev = to
		hasPrev = true
		rest = tail
	}
	return true
}

func dropStyleTags(st string) string {
	chars := []rune(st)
	out := make([]rune, 0, len(chars))
	for i := 0; i < len(chars); {
		if runeAt(chars, i) == ':' && runeAt(chars, i+1) == ':' && runeAt(chars, i+2) == ':' {
			k := i + 3
			for k < len(chars) && (isIDChar(chars[k]) || chars[k] == '-') {
				k++
			}
			for k > i+3 && chars[k-1] == '-' {
				k--
			}
			if k > i+3 {
				i = k
				continue
			}
		}
		out = append(out, chars[i])
		i++
	}
	return string(out)
}

func stateEndpoint(graph *Graph, id string, isSource bool) (int, bool) {
	if id == "[*]" {
		key := "[*]end"
		if isSource {
			key = "[*]start"
		}
		dot := "●"
		return graph.nodeIndex(key, &dot, shapeRound)
	}
	return graph.nodeIndex(id, nil, shapeRound)
}

func parseStateDesc(st string, graph *Graph) bool {
	if lhs, rhs, ok := splitOnce(st, ":"); ok {
		id := jsTrim(lhs)
		desc := jsTrim(rhs)
		if id == "" || hasSpace(id) || desc == "" {
			return false
		}
		_, ok := graph.nodeLabel(id, decodeHTMLEntities(desc))
		return ok
	}
	if hasSpace(st) {
		return false
	}
	_, ok := graph.nodeIndex(st, nil, shapeRound)
	return ok
}

func stateFromID(s string) string {
	s = jsTrimRight(s)
	s = strings.TrimRight(s, "-")
	return jsTrim(s)
}

func stateToID(s string) string {
	s = jsTrimLeft(s)
	s = strings.TrimLeft(s, ">")
	s = jsTrimRight(s)
	s = strings.TrimRight(s, "-")
	return jsTrim(s)
}
