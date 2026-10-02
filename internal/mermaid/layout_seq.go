package mermaid

import "sort"

const seqGap = 5

func layoutSequence(seq *Sequence) *Canvas {
	n := len(seq.labels)
	if n == 0 {
		return nil
	}
	labels := make([]string, n)
	boxW := make([]int, n)
	for i, l := range seq.labels {
		labels[i] = fitLabel(l, wrapWidth)
		boxW[i] = max(1, stringWidth(labels[i])) + 2*padX + 2
	}
	boxH := 3
	gaps := make([]int, sat(n, 1))
	for i := range gaps {
		gaps[i] = max(seqGap, ceilDiv2(boxW[i])+ceilDiv2(boxW[i+1])+1)
	}
	type req struct{ l, r, need int }
	var reqs []req
	for _, item := range seq.items {
		switch item.Kind {
		case "message":
			tw := 0
			if item.Text != nil {
				tw = stringWidth(*item.Text)
			}
			if item.From != item.To {
				reqs = append(reqs, req{min(item.From, item.To), max(item.From, item.To), max(tw+2, 4)})
			} else if item.From+1 < n {
				reqs = append(reqs, req{item.From, item.From + 1, 5 + tw + 2})
			}
		case "note":
			tw := stringWidth(deref(item.Text))
			a := item.Anchor
			switch a.Kind {
			case "over":
				if a.From < a.To {
					reqs = append(reqs, req{a.From, a.To, sat(tw, 1)})
				} else {
					need := ceilDiv2(tw+4) + 2
					if a.From > 0 {
						reqs = append(reqs, req{a.From - 1, a.From, need})
					}
					if a.From+1 < n {
						reqs = append(reqs, req{a.From, a.From + 1, need})
					}
				}
			case "left":
				if a.At > 0 {
					reqs = append(reqs, req{a.At - 1, a.At, tw + 7})
				}
			case "right":
				if a.At+1 < n {
					reqs = append(reqs, req{a.At, a.At + 1, tw + 7})
				}
			}
		}
	}
	sort.SliceStable(reqs, func(i, j int) bool {
		return reqs[i].r-reqs[i].l < reqs[j].r-reqs[j].l
	})
	for _, rq := range reqs {
		cur := 0
		for i := rq.l; i < rq.r; i++ {
			cur += gaps[i]
		}
		if cur < rq.need {
			gaps[rq.r-1] += rq.need - cur
		}
	}
	xs := make([]int, n)
	xs[0] = half(boxW[0])
	for i := 1; i < n; i++ {
		xs[i] = xs[i-1] + gaps[i-1]
	}
	canvasW := xs[n-1] + ceilDiv2(boxW[n-1]) + 1
	for _, item := range seq.items {
		switch item.Kind {
		case "message":
			if item.From == item.To {
				tw := 0
				if item.Text != nil {
					tw = stringWidth(*item.Text)
				}
				if w := xs[item.From] + 5 + tw + 1; w > canvasW {
					canvasW = w
				}
			}
		case "note":
			g := noteGeometry(xs, item.Anchor, stringWidth(deref(item.Text)))
			if w := g.x + g.w + 1; w > canvasW {
				canvasW = w
			}
		case "divider":
			if w := stringWidth(deref(item.Text)) + 4; w > canvasW {
				canvasW = w
			}
		}
	}
	var rows []int
	y := boxH + 1
	for _, item := range seq.items {
		rows = append(rows, y)
		y += seqRowHeight(item)
	}
	bottomTop := y
	canvasH := bottomTop + boxH
	if int64(canvasW)*int64(canvasH) > maxCanvasCells {
		return nil
	}
	canvas := newCanvas(canvasW, canvasH)
	for i := 0; i < n; i++ {
		for _, by := range []int{0, bottomTop} {
			drawBox(canvas, seqBox(sat(xs[i], half(boxW[i])), by, boxW[i], boxH), []string{labels[i]}, shapeRect)
		}
	}
	for k, item := range seq.items {
		if item.Kind != "note" {
			continue
		}
		g := noteGeometry(xs, item.Anchor, stringWidth(deref(item.Text)))
		drawBox(canvas, seqBox(g.x, rows[k], g.w, 3), []string{deref(item.Text)}, shapeRect)
	}
	for _, x := range xs {
		canvas.junction(x, boxH-1, bitD)
		canvas.segV(x, boxH, bottomTop-1)
		canvas.junction(x, bottomTop, bitU)
	}
	for k, item := range seq.items {
		r := rows[k]
		switch item.Kind {
		case "message":
			drawMessage(canvas, item, xs, r)
		case "divider":
			drawDivider(canvas, deref(item.Text), r, canvasW)
		}
	}
	canvas.finalizeMask()
	return canvas
}

type noteGeo struct{ x, w int }

func noteGeometry(xs []int, anchor NoteAnchor, textW int) noteGeo {
	if anchor.Kind == "over" {
		center := half(xs[anchor.From] + xs[anchor.To])
		w := max(xs[anchor.To]-xs[anchor.From]+5, textW+2*padX+2)
		return noteGeo{sat(center, half(w)), w}
	}
	w := textW + 2*padX + 2
	if anchor.Kind == "left" {
		return noteGeo{sat(xs[anchor.At], 2+w-1), w}
	}
	return noteGeo{xs[anchor.At] + 2, w}
}

func seqRowHeight(item SeqItem) int {
	if item.Kind == "note" {
		return 4
	}
	if item.Kind == "divider" {
		return 2
	}
	if item.From == item.To {
		return 4
	}
	if item.Text != nil {
		return 3
	}
	return 2
}

func seqBox(x, y, w, h int) Placed {
	return Placed{x: x, y: y, w: w, h: h, cx: x + half(w), cy: y + 1}
}

func drawMessage(canvas *Canvas, item SeqItem, xs []int, r int) {
	lineCh := "─"
	if item.Dashed {
		lineCh = "╌"
	}
	if item.From == item.To {
		x := xs[item.From]
		canvas.junction(x, r, bitR)
		canvas.set(x+1, r, lineCh, ClsEdge)
		canvas.set(x+2, r, lineCh, ClsEdge)
		canvas.set(x+3, r, "╮", ClsEdge)
		canvas.set(x+3, r+1, "│", ClsEdge)
		head := "◄"
		if item.Head == seqHeadCross {
			head = "×"
		}
		canvas.set(x+1, r+2, head, ClsEdge)
		canvas.set(x+2, r+2, lineCh, ClsEdge)
		canvas.set(x+3, r+2, "╯", ClsEdge)
		if item.Text != nil {
			drawTextOverEdges(canvas, *item.Text, x+5, r+1, ClsText)
		}
		return
	}
	x0, x1 := xs[item.From], xs[item.To]
	rightward := x1 > x0
	arrowRow := r
	if item.Text != nil {
		arrowRow = r + 1
	}
	lo, hi := x0, x1
	if lo > hi {
		lo, hi = hi, lo
	}
	if rightward {
		canvas.junction(x0, arrowRow, bitR)
	} else {
		canvas.junction(x0, arrowRow, bitL)
	}
	for x := lo + 1; x < hi; x++ {
		canvas.set(x, arrowRow, lineCh, ClsEdge)
	}
	headCh := "×"
	if item.Head != seqHeadCross {
		if rightward {
			headCh = "▶"
		} else {
			headCh = "◄"
		}
	}
	hx := x1 + 1
	if rightward {
		hx = x1 - 1
	}
	canvas.set(hx, arrowRow, headCh, ClsEdge)
	if item.Text != nil {
		span := hi - lo - 1
		t := fitLabel(*item.Text, max(1, span))
		drawTextOverEdges(canvas, t, lo+1+half(sat(span, stringWidth(t))), r, ClsText)
	}
}

func drawDivider(canvas *Canvas, text string, r, canvasW int) {
	for x := 0; x < canvasW; x++ {
		canvas.set(x, r, "─", ClsEdge)
	}
	drawTextOverEdges(canvas, " "+fitLabel(text, sat(canvasW, 4))+" ", 2, r, ClsEdgeLabel)
}
