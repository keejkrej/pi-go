package mermaid

import "strings"

type drawn struct {
	canvas   *Canvas
	warnings []string
}

// Render draws a Mermaid source block as Unicode box-drawing art.
//
// Supported: graph/flowchart (including subgraph), stateDiagram, classDiagram,
// erDiagram and sequenceDiagram. A non-nil error is the TS `null` result:
// blank input, an unsupported diagram type, a syntax error the stricter
// grammars refuse, or a diagram too large to lay out. Flowchart warnings are
// advisory and still return art.
func Render(src string, _ Options) (MermaidArt, error) {
	src = stripControls(src)
	if jsTrim(src) == "" {
		return MermaidArt{}, ErrNoDiagram
	}
	d := attempt(src)
	if d == nil {
		return MermaidArt{}, ErrNoDiagram
	}
	art := d.canvas.toLines()
	if d.warnings == nil {
		art.Warnings = []string{}
	} else {
		art.Warnings = d.warnings
	}
	return art, nil
}

func attempt(src string) *drawn {
	if d := draw(src); d != nil {
		return d
	}
	body := jsTrimRight(src)
	cut := strings.LastIndex(body, "\n")
	if cut < 0 {
		return nil
	}
	salvaged := draw(body[:cut])
	if salvaged == nil {
		return nil
	}
	dropped := jsTrim(body[cut+1:])
	warnings := append(append([]string{}, salvaged.warnings...), `dropped, unreadable final line: "`+dropped+`"`)
	return &drawn{canvas: salvaged.canvas, warnings: warnings}
}

func draw(src string) *drawn {
	plain := func(c *Canvas) *drawn {
		if c == nil {
			return nil
		}
		return &drawn{canvas: c, warnings: []string{}}
	}
	switch DiagramKind(src) {
	case KindFlowchart:
		graph := parseGraph(src)
		if graph == nil {
			return nil
		}
		var canvas *Canvas
		if len(graph.groups) == 0 {
			canvas = layoutFlowchart(graph)
		} else {
			canvas = layoutGrouped(graph)
		}
		if canvas == nil {
			return nil
		}
		return &drawn{canvas: canvas, warnings: graph.warnings}
	case KindState:
		state := parseState(src)
		if state == nil {
			return nil
		}
		return plain(layoutFlowchart(state))
	case KindClass:
		cls := parseClass(src)
		if cls == nil {
			return nil
		}
		return plain(layoutClass(cls.graph, cls.infos))
	case KindER:
		er := parseEr(src)
		if er == nil {
			return nil
		}
		return plain(layoutClass(er.graph, er.infos))
	case KindSequence:
		seq := parseSequence(src)
		if seq == nil {
			return nil
		}
		return plain(layoutSequence(seq))
	default:
		return nil
	}
}
