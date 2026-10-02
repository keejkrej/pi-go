package mermaid

func drawBox(canvas *Canvas, p Placed, lines []string, shape Shape) {
	x, y, w, h := p.x, p.y, p.w, p.h
	right := x + w - 1
	bottom := y + h - 1
	rounded := shape == shapeRound || shape == shapeDiamond
	tl, tr, bl, br := "┌", "┐", "└", "┘"
	if rounded {
		tl, tr, bl, br = "╭", "╮", "╰", "╯"
	}
	canvas.set(x, y, tl, ClsBorder)
	canvas.set(right, y, tr, ClsBorder)
	canvas.set(x, bottom, bl, ClsBorder)
	canvas.set(right, bottom, br, ClsBorder)
	for cx := x + 1; cx < right; cx++ {
		canvas.addBits(cx, y, bitL|bitR, ClsBorder)
		canvas.addBits(cx, bottom, bitL|bitR, ClsBorder)
	}
	for cy := y + 1; cy < bottom; cy++ {
		canvas.addBits(x, cy, bitU|bitD, ClsBorder)
		canvas.addBits(right, cy, bitU|bitD, ClsBorder)
	}
	for cy := y; cy <= bottom; cy++ {
		for cx := x; cx <= right; cx++ {
			if canvas.in(cx, cy) {
				canvas.occupied[canvas.idx(cx, cy)] = 1
			}
		}
	}
	inner := max(1, sat(w, 2*padX+2))
	for li, line := range lines {
		text := fitLabel(line, inner)
		textX := x + 1 + padX + half(sat(inner, stringWidth(text)))
		drawText(canvas, text, textX, y+1+li, ClsText)
	}
}

func drawClassBox(canvas *Canvas, p Placed, sections [][]string) {
	drawBox(canvas, p, nil, shapeRect)
	inner := max(1, sat(p.w, 2*padX+2))
	row := p.y + 1
	first := true
	for si, section := range sections {
		if len(section) == 0 {
			continue
		}
		if !first {
			canvas.set(p.x, row, "├", ClsBorder)
			for x := p.x + 1; x < p.x+p.w-1; x++ {
				canvas.set(x, row, "─", ClsBorder)
			}
			canvas.set(p.x+p.w-1, row, "┤", ClsBorder)
			row++
		}
		first = false
		for _, line := range section {
			text := fitLabel(line, inner)
			tx := p.x + 1 + padX
			if si == 0 {
				tx = p.x + 1 + padX + half(sat(inner, stringWidth(text)))
			}
			drawTextOverEdges(canvas, text, tx, row, ClsText)
			row++
		}
	}
}

func drawFrame(canvas *Canvas, p Placed, title string, sub *Canvas) {
	drawBox(canvas, p, nil, shapeRect)
	t := fitLabel(title, sat(p.w, 4))
	drawTextOverEdges(canvas, " "+t+" ", p.x+1, p.y, ClsText)
	canvas.blit(sub, p.x+1+half(p.w-2-sub.w), p.y+1+half(p.h-2-sub.h))
}

func headGlyph(head Head, arrow string) string {
	switch head {
	case headCircle:
		return "o"
	case headCross:
		return "×"
	case headDiamondFill:
		return "◆"
	case headDiamondOpen:
		return "◇"
	case headTriangle:
		switch arrow {
		case "▼":
			return "▽"
		case "▲":
			return "△"
		case "◄":
			return "◁"
		case "▶":
			return "▷"
		default:
			return arrow
		}
	default:
		return arrow
	}
}

func routeForward(canvas *Canvas, from, to Placed, edge Edge, bus int) {
	tx := to.cx
	bx := from.cx
	if abs(from.cx-tx) <= 1 {
		bx = tx
	}
	by := from.y + from.h - 1
	headRow := to.y - 1
	canvas.junction(bx, by, bitD)
	canvas.segV(bx, by, bus)
	if bx == tx {
		canvas.segV(bx, bus, headRow)
	} else {
		canvas.segH(bus, bx, tx)
		canvas.segV(tx, bus, headRow)
	}
	if edge.HeadTo == headNone {
		canvas.addBits(tx, headRow, bitU, ClsEdge)
	} else {
		canvas.set(tx, headRow, headGlyph(edge.HeadTo, "▼"), ClsEdge)
	}
	if edge.HeadFrom != headNone {
		canvas.set(bx, by, headGlyph(edge.HeadFrom, "▲"), ClsEdge)
	}
	if edge.Label != nil {
		placeLabel(canvas, *edge.Label, headRow, tx+1)
	}
}

func routeSelf(canvas *Canvas, p Placed, edge Edge) {
	bottom := p.y + p.h - 1
	exitX := p.cx + 1
	retX := p.x + p.w - 2
	if retX <= exitX || bottom+2 >= canvas.h {
		return
	}
	v, h, bl, br := "│", "─", "╰", "╯"
	switch edge.Line {
	case lineDotted:
		v, h, bl, br = "╎", "╌", "╰", "╯"
	case lineThick:
		v, h, bl, br = "┃", "━", "┗", "┛"
	}
	canvas.junction(exitX, bottom, bitD)
	canvas.set(exitX, bottom+1, v, ClsEdge)
	canvas.set(exitX, bottom+2, bl, ClsEdge)
	for x := exitX + 1; x < retX; x++ {
		canvas.set(x, bottom+2, h, ClsEdge)
	}
	canvas.set(retX, bottom+2, br, ClsEdge)
	canvas.set(retX, bottom+1, headGlyph(edge.HeadTo, "▲"), ClsEdge)
	if edge.Label != nil {
		placeLabel(canvas, *edge.Label, bottom+1, p.x+p.w+1)
	}
}

func routeBack(canvas *Canvas, from, to Placed, edge Edge, laneX int) {
	sx := from.x + from.w - 1
	sy := from.cy
	tx := to.x + to.w - 1
	tyc := to.cy
	canvas.junction(sx, sy, bitR)
	canvas.segH(sy, sx, laneX)
	canvas.segV(laneX, sy, tyc)
	canvas.segH(tyc, tx+1, laneX)
	if edge.HeadTo == headNone {
		canvas.addBits(tx+1, tyc, bitR, ClsEdge)
	} else {
		canvas.set(tx+1, tyc, headGlyph(edge.HeadTo, "◄"), ClsEdge)
	}
	if edge.HeadFrom != headNone {
		canvas.set(sx, sy, headGlyph(edge.HeadFrom, "◄"), ClsEdge)
	}
	if edge.Label != nil {
		placeLabel(canvas, *edge.Label, sat(tyc, 1), sat(laneX, stringWidth(*edge.Label)+1))
	}
}

func routeForwardLr(canvas *Canvas, from, to Placed, edge Edge, bus int) {
	rx := from.x + from.w - 1
	ry := from.cy
	ly := to.cy
	headCol := to.x - 1
	canvas.junction(rx, ry, bitR)
	canvas.segH(ry, rx, bus)
	if ry == ly {
		canvas.segH(ry, bus, headCol)
	} else {
		canvas.segV(bus, ry, ly)
		canvas.segH(ly, bus, headCol)
	}
	if edge.HeadTo == headNone {
		canvas.addBits(headCol, ly, bitR, ClsEdge)
	} else {
		canvas.set(headCol, ly, headGlyph(edge.HeadTo, "▶"), ClsEdge)
	}
	if edge.HeadFrom != headNone {
		canvas.set(rx, ry, headGlyph(edge.HeadFrom, "◄"), ClsEdge)
	}
	if edge.Label != nil {
		placeLabel(canvas, *edge.Label, sat(ly, 1), bus+1)
	}
}

func routeBackLr(canvas *Canvas, from, to Placed, edge Edge, laneY int) {
	sx := from.cx
	sy := from.y + from.h - 1
	tx := to.cx
	ty := to.y + to.h - 1
	canvas.junction(sx, sy, bitD)
	canvas.segV(sx, sy, laneY)
	canvas.segH(laneY, sx, tx)
	canvas.segV(tx, laneY, ty+1)
	if edge.HeadTo == headNone {
		canvas.addBits(tx, ty+1, bitD, ClsEdge)
	} else {
		canvas.set(tx, ty+1, headGlyph(edge.HeadTo, "▲"), ClsEdge)
	}
	if edge.HeadFrom != headNone {
		canvas.set(sx, sy, headGlyph(edge.HeadFrom, "▲"), ClsEdge)
	}
	if edge.Label != nil {
		placeLabel(canvas, *edge.Label, sat(laneY, 1), half(sx+tx))
	}
}

func placeLabel(canvas *Canvas, label string, row, startX int) {
	if row < 0 || row >= canvas.h {
		return
	}
	text := fitLabel(label, maxLabel)
	x := startX
	for _, c := range measured(text) {
		if c.w == 0 {
			continue
		}
		if x < 0 || x+c.w > canvas.w {
			break
		}
		blocked := false
		for k := 0; k < c.w; k++ {
			i := canvas.idx(x+k, row)
			if canvas.ch[i] != " " || canvas.mask[i] != 0 || canvas.occupied[i] != 0 {
				blocked = true
			}
		}
		if blocked {
			break
		}
		canvas.set(x, row, c.s, ClsEdgeLabel)
		for k := 1; k < c.w; k++ {
			canvas.set(x+k, row, cont, ClsEdgeLabel)
		}
		x += c.w
	}
}

type itemKey struct {
	group bool
	idx   int
}

type scopeEdge struct {
	f, t itemKey
	ei   int
}

func layoutGrouped(graph *Graph) *Canvas {
	proxy := map[int]int{}
	for gi, g := range graph.groups {
		if ni, ok := graph.index[g.ID]; ok {
			proxy[ni] = gi
		}
	}
	groupChain := func(gidx int) []int {
		var chain []int
		for gidx != -1 {
			chain = append(chain, gidx)
			gidx = graph.groups[gidx].Parent
		}
		for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
			chain[i], chain[j] = chain[j], chain[i]
		}
		return chain
	}
	endpoint := func(n int) (itemKey, []int) {
		if gi, ok := proxy[n]; ok {
			return itemKey{true, gi}, groupChain(graph.groups[gi].Parent)
		}
		return itemKey{false, n}, groupChain(graph.nodeGroup[n])
	}
	scopeEdges := map[int][]scopeEdge{}
	referenced := make([]bool, len(graph.groups))
	for ei, e := range graph.edges {
		fKey, fChain := endpoint(e.From)
		tKey, tChain := endpoint(e.To)
		k := 0
		for k < len(fChain) && k < len(tChain) && fChain[k] == tChain[k] {
			k++
		}
		scope := -1
		if k != 0 {
			scope = fChain[k-1]
		}
		if len(fChain) > k {
			fKey = itemKey{true, fChain[k]}
		}
		if len(tChain) > k {
			tKey = itemKey{true, tChain[k]}
		}
		if fKey.group {
			referenced[fKey.idx] = true
		}
		if tKey.group {
			referenced[tKey.idx] = true
		}
		scopeEdges[scope] = append(scopeEdges[scope], scopeEdge{fKey, tKey, ei})
	}
	directNodes := map[int][]int{}
	for ni, gidx := range graph.nodeGroup {
		if _, ok := proxy[ni]; ok {
			continue
		}
		directNodes[gidx] = append(directNodes[gidx], ni)
	}
	keep := make([]bool, len(graph.groups))
	for gi := len(graph.groups) - 1; gi >= 0; gi-- {
		hasNodes := len(directNodes[gi]) > 0
		hasChildren := false
		for c, g := range graph.groups {
			if g.Parent == gi && keep[c] {
				hasChildren = true
				break
			}
		}
		keep[gi] = hasNodes || hasChildren || referenced[gi]
	}
	canvas := buildScope(graph, -1, scopeEdges, directNodes, keep)
	if canvas == nil {
		return nil
	}
	return orient(canvas, graph)
}

func buildScope(graph *Graph, scope int, scopeEdges map[int][]scopeEdge, directNodes map[int][]int, keep []bool) *Canvas {
	var items []itemKey
	for _, ni := range directNodes[scope] {
		items = append(items, itemKey{false, ni})
	}
	for gi, g := range graph.groups {
		if g.Parent == scope && keep[gi] {
			items = append(items, itemKey{true, gi})
		}
	}
	if len(items) == 0 {
		return newCanvas(1, 1)
	}
	indexOf := map[itemKey]int{}
	var nodes []Node
	var extras []nodeExtra
	for _, item := range items {
		indexOf[item] = len(nodes)
		if !item.group {
			i := item.idx
			nodes = append(nodes, Node{Label: graph.nodes[i].Label, Shape: graph.nodes[i].Shape})
			extras = append(extras, nodeExtra{kind: "plain"})
		} else {
			sub := buildScope(graph, item.idx, scopeEdges, directNodes, keep)
			if sub == nil {
				return nil
			}
			nodes = append(nodes, Node{Label: graph.groups[item.idx].Label, Shape: shapeRect})
			extras = append(extras, nodeExtra{kind: "frame", sub: sub})
		}
	}
	var edges []Edge
	for _, se := range scopeEdges[scope] {
		fi, fok := indexOf[se.f]
		ti, tok := indexOf[se.t]
		if !fok || !tok {
			continue
		}
		e := graph.edges[se.ei]
		edges = append(edges, Edge{
			From: fi, To: ti, Label: e.Label, HeadTo: e.HeadTo, HeadFrom: e.HeadFrom, Line: e.Line,
		})
	}
	synth := newGraph(graph.dir)
	synth.nodes = nodes
	synth.edges = edges
	return layoutCanvas(synth, extras)
}
