package mermaid

func placeTd(ranks []int, maxRank int, byRank [][]int, sizes nodeSizes, graph *Graph, placed []Placed) routePlan {
	centers := assignPositions(byRank, sizes.layW, gapX, graph.edges, ranks)
	edgeBus := make([]int, len(graph.edges))
	busTracks := make([]int, maxRank+1)
	for r := 0; r < maxRank; r++ {
		spans := busSpans(graph, ranks, centers, r, false)
		if len(spans) == 0 {
			continue
		}
		assigned, count := assignTracks(spans)
		for _, as := range assigned {
			edgeBus[as[0]] = as[1]
		}
		busTracks[r] = count
	}
	rankH := make([]int, maxRank+1)
	for r, row := range byRank {
		if len(row) == 0 {
			rankH[r] = 3
			continue
		}
		m := 0
		for _, i := range row {
			h := sizes.boxH[i] + sizes.extraH[i]
			if h > m {
				m = h
			}
		}
		rankH[r] = m
	}
	rankY := make([]int, maxRank+1)
	for r := 1; r <= maxRank; r++ {
		gap := busTracks[r-1] + 1
		if gap < gapY {
			gap = gapY
		}
		rankY[r] = rankY[r-1] + rankH[r-1] + gap
	}
	canvasH := rankY[maxRank] + rankH[maxRank]
	bandEnd := make([]int, maxRank+1)
	for r := 0; r <= maxRank; r++ {
		bandEnd[r] = rankY[r] + rankH[r]
	}
	diagramW := 1
	for r, row := range byRank {
		for _, idx := range row {
			w := sizes.boxW[idx]
			h := sizes.boxH[idx]
			cx := centers[idx]
			x := sat(cx, half(w))
			y := rankY[r] + half(rankH[r]-h-sizes.extraH[idx])
			placed[idx] = Placed{x: x, y: y, w: w, h: h, cx: cx, cy: y + half(h), rank: r}
			if x+w > diagramW {
				diagramW = x + w
			}
			if sizes.extraH[idx] > 0 && sizes.selfLabelW[idx] > 0 {
				tail := x + w + 2 + sizes.selfLabelW[idx]
				if tail > diagramW {
					diagramW = tail
				}
			}
		}
	}
	contentW := diagramW
	for _, e := range graph.edges {
		if e.From == e.To || e.Label == nil {
			continue
		}
		lw := stringWidth(*e.Label)
		if lw > maxLabel {
			lw = maxLabel
		}
		var need int
		if ranks[e.To] == ranks[e.From]+1 {
			need = placed[e.To].cx + 2 + lw
		} else {
			need = diagramW + lw + 1
		}
		if need > contentW {
			contentW = need
		}
	}
	edgeLane := make([]int, len(graph.edges))
	lanes := laneSpans(graph, ranks, placed, true)
	canvasW := contentW
	laneBase := 0
	if len(lanes) > 0 {
		assigned, count := assignTracks(lanes)
		for _, as := range assigned {
			edgeLane[as[0]] = as[1]
		}
		canvasW = contentW + 1 + count
		laneBase = contentW + 1
	}
	return routePlan{canvasW, canvasH, bandEnd, edgeBus, laneBase, edgeLane}
}

func placeLr(ranks []int, maxRank int, byRank [][]int, sizes nodeSizes, graph *Graph, placed []Placed) routePlan {
	colW := make([]int, len(byRank))
	for r, row := range byRank {
		if len(row) == 0 {
			continue
		}
		m := 0
		for _, i := range row {
			if sizes.boxW[i] > m {
				m = sizes.boxW[i]
			}
		}
		colW[r] = m
	}
	maxLab := 0
	for _, e := range graph.edges {
		if !(e.From == e.To || ranks[e.To] == ranks[e.From]+1) || e.Label == nil {
			continue
		}
		lw := stringWidth(*e.Label)
		if lw > maxLabel {
			lw = maxLabel
		}
		if lw > maxLab {
			maxLab = lw
		}
	}
	baseGap := gapX + 1
	if maxLab+3 > baseGap {
		baseGap = maxLab + 3
	}
	centers := assignPositions(byRank, sizes.layH, 1, graph.edges, ranks)
	edgeBus := make([]int, len(graph.edges))
	busTracks := make([]int, maxRank+1)
	for r := 0; r < maxRank; r++ {
		spans := busSpans(graph, ranks, centers, r, true)
		if len(spans) == 0 {
			continue
		}
		assigned, count := assignTracks(spans)
		for _, as := range assigned {
			edgeBus[as[0]] = as[1]
		}
		busTracks[r] = count
	}
	rankX := make([]int, maxRank+1)
	for r := 1; r <= maxRank; r++ {
		gap := busTracks[r-1] + 1
		if gap < baseGap {
			gap = baseGap
		}
		rankX[r] = rankX[r-1] + colW[r-1] + gap
	}
	selfTail := 0
	for _, i := range byRank[maxRank] {
		if sizes.extraH[i] > 0 && sizes.selfLabelW[i] > 0 {
			t := 2 + sizes.selfLabelW[i]
			if t > selfTail {
				selfTail = t
			}
		}
	}
	canvasW := rankX[maxRank] + colW[maxRank] + selfTail
	bandEnd := make([]int, maxRank+1)
	for r := 0; r <= maxRank; r++ {
		bandEnd[r] = rankX[r] + colW[r]
	}
	diagramH := 1
	for r, row := range byRank {
		x := rankX[r]
		for _, idx := range row {
			w := sizes.boxW[idx]
			h := sizes.boxH[idx]
			cy := centers[idx]
			y := sat(cy, half(h+sizes.extraH[idx]))
			placed[idx] = Placed{x: x, y: y, w: w, h: h, cx: x + half(w), cy: y + half(h), rank: r}
			bottom := y + h + sizes.extraH[idx]
			if bottom > diagramH {
				diagramH = bottom
			}
		}
	}
	edgeLane := make([]int, len(graph.edges))
	lanes := laneSpans(graph, ranks, placed, false)
	canvasH := diagramH
	laneBase := 0
	if len(lanes) > 0 {
		assigned, count := assignTracks(lanes)
		for _, as := range assigned {
			edgeLane[as[0]] = as[1]
		}
		canvasH = diagramH + 1 + count
		laneBase = diagramH + 1
	}
	return routePlan{canvasW, canvasH, bandEnd, edgeBus, laneBase, edgeLane}
}

func layoutCanvas(graph *Graph, extras []nodeExtra) *Canvas {
	n := len(graph.nodes)
	if n == 0 {
		return nil
	}
	ranks := computeRanks(graph)
	maxRank := 0
	for _, r := range ranks {
		if r > maxRank {
			maxRank = r
		}
	}
	byRank := make([][]int, maxRank+1)
	for idx, r := range ranks {
		byRank[r] = append(byRank[r], idx)
	}
	orderRanks(byRank, graph.edges, ranks)

	wrapped := make([][]string, n)
	for i, node := range graph.nodes {
		wrapped[i] = wrapLabel(node.Label, wrapWidth, maxLines)
	}
	widest := func(lines []string) int {
		m := 1
		if len(lines) == 0 {
			return 1
		}
		for _, l := range lines {
			if w := stringWidth(l); w > m {
				m = w
			}
		}
		if m < 1 {
			m = 1
		}
		return m
	}
	boxW := make([]int, n)
	boxH := make([]int, n)
	for i, extra := range extras {
		switch extra.kind {
		case "frame":
			titleW := stringWidth(fitLabel(graph.nodes[i].Label, wrapWidth)) + 4
			boxW[i] = extra.sub.w + 2
			if titleW > boxW[i] {
				boxW[i] = titleW
			}
			boxH[i] = extra.sub.h + 2
		case "compartments":
			boxW[i] = widest(flatten(extra.sections)) + 2*padX + 2
			filled := 0
			lines := 0
			for _, sec := range extra.sections {
				if len(sec) > 0 {
					filled++
					lines += len(sec)
				}
			}
			boxH[i] = lines + sat(filled, 1) + 2
		default:
			boxW[i] = widest(wrapped[i]) + 2*padX + 2
			boxH[i] = len(wrapped[i]) + 2
		}
	}
	extraH := make([]int, n)
	selfLabelW := make([]int, n)
	for _, e := range graph.edges {
		if e.From != e.To {
			continue
		}
		extraH[e.From] = 2
		if e.Label != nil {
			lw := stringWidth(*e.Label)
			if lw > maxLabel {
				lw = maxLabel
			}
			if lw > selfLabelW[e.From] {
				selfLabelW[e.From] = lw
			}
		}
	}
	for i := 0; i < n; i++ {
		if extraH[i] > 0 && boxW[i] < 7 {
			boxW[i] = 7
		}
	}
	sizes := nodeSizes{boxW: boxW, boxH: boxH, extraH: extraH, selfLabelW: selfLabelW}
	sizes.layW = make([]int, n)
	sizes.layH = make([]int, n)
	for i := 0; i < n; i++ {
		sizes.layW[i] = boxW[i]
		if selfLabelW[i] > 0 {
			sizes.layW[i] = boxW[i] + 2*(selfLabelW[i]+3)
		}
		sizes.layH[i] = boxH[i] + extraH[i]
	}
	placed := make([]Placed, n)
	vertical := graph.dir == dirDown || graph.dir == dirUp
	var plan routePlan
	if vertical {
		plan = placeTd(ranks, maxRank, byRank, sizes, graph, placed)
	} else {
		plan = placeLr(ranks, maxRank, byRank, sizes, graph, placed)
	}
	if int64(plan.canvasW)*int64(plan.canvasH) > maxCanvasCells {
		return nil
	}
	canvas := newCanvas(plan.canvasW, plan.canvasH)
	for idx := 0; idx < n; idx++ {
		extra := extras[idx]
		switch extra.kind {
		case "frame":
			drawFrame(canvas, placed[idx], graph.nodes[idx].Label, extra.sub)
		case "compartments":
			drawClassBox(canvas, placed[idx], extra.sections)
		default:
			drawBox(canvas, placed[idx], wrapped[idx], graph.nodes[idx].Shape)
		}
	}
	for i, edge := range graph.edges {
		switch edge.Line {
		case lineDotted:
			canvas.curStyle = styDot
		case lineThick:
			canvas.curStyle = styThick
		default:
			canvas.curStyle = stySolid
		}
		if edge.From == edge.To {
			routeSelf(canvas, placed[edge.From], edge)
			continue
		}
		from := placed[edge.From]
		to := placed[edge.To]
		adjacent := to.rank == from.rank+1
		bus := plan.bandEnd[from.rank] + plan.edgeBus[i]
		lane := plan.laneBase + plan.edgeLane[i]
		if vertical {
			if adjacent {
				routeForward(canvas, from, to, edge, bus)
			} else {
				routeBack(canvas, from, to, edge, lane)
			}
		} else if adjacent {
			routeForwardLr(canvas, from, to, edge, bus)
		} else {
			routeBackLr(canvas, from, to, edge, lane)
		}
	}
	canvas.finalizeMask()
	return canvas
}

func flatten(sections [][]string) []string {
	var out []string
	for _, sec := range sections {
		out = append(out, sec...)
	}
	return out
}

func orient(canvas *Canvas, graph *Graph) *Canvas {
	if graph.dir == dirUp {
		canvas.flipVertical()
	} else if graph.dir == dirLeft {
		canvas.flipHorizontal()
	}
	return canvas
}

func layoutFlowchart(graph *Graph) *Canvas {
	extras := make([]nodeExtra, len(graph.nodes))
	for i := range extras {
		extras[i].kind = "plain"
	}
	canvas := layoutCanvas(graph, extras)
	if canvas == nil {
		return nil
	}
	return orient(canvas, graph)
}

func layoutClass(graph *Graph, infos []ClassInfo) *Canvas {
	extras := make([]nodeExtra, len(graph.nodes))
	for i, node := range graph.nodes {
		var info ClassInfo
		if i < len(infos) {
			info = infos[i]
		}
		title := make([]string, 0, 2)
		if info.Annotation != nil {
			title = append(title, "«"+*info.Annotation+"»")
		}
		title = append(title, displayGenerics(node.Label))
		extras[i] = nodeExtra{kind: "compartments", sections: [][]string{title, info.Attrs, info.Methods}}
	}
	canvas := layoutCanvas(graph, extras)
	if canvas == nil {
		return nil
	}
	return orient(canvas, graph)
}
