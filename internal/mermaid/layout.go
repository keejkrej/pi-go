package mermaid

import (
	"math"
	"sort"
)

const (
	padX           = 1
	gapX           = 3
	gapY           = 2
	maxCanvasCells = 1 << 21
)

type Placed struct {
	x, y, w, h int
	cx, cy     int
	rank       int
}

type nodeSizes struct {
	boxW, boxH []int
	layW, layH []int
	extraH     []int
	selfLabelW []int
}

type nodeExtra struct {
	kind     string // plain, frame, compartments
	sub      *Canvas
	sections [][]string
}

type routePlan struct {
	canvasW, canvasH int
	bandEnd          []int
	edgeBus          []int
	laneBase         int
	edgeLane         []int
}

func computeRanks(graph *Graph) []int {
	n := len(graph.nodes)
	children := make([][]int, n)
	indeg := make([]int, n)
	for _, e := range graph.edges {
		if e.From != e.To {
			children[e.From] = append(children[e.From], e.To)
			indeg[e.To]++
		}
	}
	color := make([]byte, n)
	dag := make([][]int, n)
	var order []int
	var roots []int
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			roots = append(roots, i)
		}
	}
	starts := append(append([]int{}, roots...), seq(n)...)
	for _, start := range starts {
		if color[start] == 0 {
			dfsDag(start, children, color, dag, &order)
		}
	}
	rank := make([]int, n)
	for i := len(order) - 1; i >= 0; i-- {
		u := order[i]
		for _, v := range dag[u] {
			if rank[u]+1 > rank[v] {
				rank[v] = rank[u] + 1
			}
		}
	}
	return rank
}

func seq(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

type dfsFrame struct{ u, i int }

func dfsDag(start int, children [][]int, color []byte, dag [][]int, order *[]int) {
	stack := []dfsFrame{{u: start}}
	color[start] = 1
	for len(stack) > 0 {
		frame := &stack[len(stack)-1]
		u := frame.u
		if frame.i < len(children[u]) {
			v := children[u][frame.i]
			frame.i++
			if color[v] == 1 {
				continue
			}
			dag[u] = append(dag[u], v)
			if color[v] == 0 {
				color[v] = 1
				stack = append(stack, dfsFrame{u: v})
			}
		} else {
			color[u] = 2
			*order = append(*order, u)
			stack = stack[:len(stack)-1]
		}
	}
}

func orderRanks(byRank [][]int, edges []Edge, ranks []int) {
	n := len(ranks)
	if len(byRank) < 2 || n < 3 {
		return
	}
	parents := make([][]int, n)
	children := make([][]int, n)
	for _, e := range edges {
		if e.From != e.To && ranks[e.To] > ranks[e.From] {
			parents[e.To] = append(parents[e.To], e.From)
			children[e.From] = append(children[e.From], e.To)
		}
	}
	pos := make([]int, n)
	reindex := func(row []int) {
		for i, v := range row {
			pos[v] = i
		}
	}
	for _, row := range byRank {
		reindex(row)
	}
	best := cloneRows(byRank)
	bestCrossings := countCrossings(edges, ranks, pos)
	if bestCrossings == 0 {
		return
	}
	for it := 0; it < 8; it++ {
		var rows [][]int
		var neigh [][]int
		if it%2 == 0 {
			rows = byRank[1:]
			neigh = parents
		} else {
			rows = reversedRows(byRank[:len(byRank)-1])
			neigh = children
		}
		for _, row := range rows {
			sortByBarycenter(row, neigh, pos)
			reindex(row)
		}
		crossings := countCrossings(edges, ranks, pos)
		if crossings < bestCrossings {
			bestCrossings = crossings
			best = cloneRows(byRank)
		}
		if bestCrossings == 0 {
			break
		}
	}
	for i := range byRank {
		byRank[i] = append(byRank[i][:0], best[i]...)
	}
}

func cloneRows(rows [][]int) [][]int {
	out := make([][]int, len(rows))
	for i, row := range rows {
		out[i] = append([]int(nil), row...)
	}
	return out
}

func reversedRows(rows [][]int) [][]int {
	out := make([][]int, len(rows))
	for i := range rows {
		out[len(rows)-1-i] = rows[i]
	}
	return out
}

type baryKey struct {
	key float64
	v   int
}

func sortByBarycenter(row []int, neigh [][]int, pos []int) {
	keyed := make([]baryKey, len(row))
	for i, v := range row {
		key := float64(pos[v])
		if len(neigh[v]) > 0 {
			sum := 0.0
			for _, u := range neigh[v] {
				sum += float64(pos[u])
			}
			key = sum / float64(len(neigh[v]))
		}
		keyed[i] = baryKey{key, v}
	}
	sort.SliceStable(keyed, func(i, j int) bool { return keyed[i].key < keyed[j].key })
	for i := range keyed {
		row[i] = keyed[i].v
	}
}

type adjEdge struct{ rank, from, to int }

func countCrossings(edges []Edge, ranks, pos []int) int {
	var adjacent []adjEdge
	for _, e := range edges {
		if e.From != e.To && ranks[e.To] == ranks[e.From]+1 {
			adjacent = append(adjacent, adjEdge{ranks[e.From], pos[e.From], pos[e.To]})
		}
	}
	crossings := 0
	for i, a := range adjacent {
		for _, b := range adjacent[i+1:] {
			if a.rank == b.rank && ((a.from < b.from && a.to > b.to) || (a.from > b.from && a.to < b.to)) {
				crossings++
			}
		}
	}
	return crossings
}

func assignPositions(byRank [][]int, size []int, sep int, edges []Edge, ranks []int) []int {
	n := len(size)
	parents := make([][]int, n)
	children := make([][]int, n)
	for _, e := range edges {
		if e.From != e.To && ranks[e.To] > ranks[e.From] {
			parents[e.To] = append(parents[e.To], e.From)
			children[e.From] = append(children[e.From], e.To)
		}
	}
	pos := make([]float64, n)
	sepF := float64(sep)
	for _, row := range byRank {
		x := 0.0
		for _, v := range row {
			h := float64(size[v]) / 2
			x += h
			pos[v] = x
			x += h + sepF
		}
	}
	for it := 0; it < 10; it++ {
		rows := byRank
		neigh := parents
		if it%2 != 0 {
			rows = reversedRows(byRank)
			neigh = children
		}
		for _, row := range rows {
			relaxRank(row, neigh, pos, size, sepF)
		}
	}
	minLeft := math.Inf(1)
	for v := 0; v < n; v++ {
		minLeft = math.Min(minLeft, pos[v]-float64(size[v])/2)
	}
	if math.IsInf(minLeft, 0) || math.IsNaN(minLeft) {
		minLeft = 0
	}
	out := make([]int, n)
	for v := 0; v < n; v++ {
		r := jsRound(pos[v] - minLeft)
		if r < 0 {
			r = 0
		}
		out[v] = r
	}
	return out
}

func jsRound(x float64) int { return int(math.Floor(x + 0.5)) }

func relaxRank(nodes []int, neigh [][]int, pos []float64, size []int, sep float64) {
	n := len(nodes)
	if n == 0 {
		return
	}
	desired := make([]float64, n)
	for i, v := range nodes {
		if len(neigh[v]) == 0 {
			desired[i] = pos[v]
			continue
		}
		sum := 0.0
		for _, u := range neigh[v] {
			sum += pos[u]
		}
		desired[i] = sum / float64(len(neigh[v]))
	}
	halfOf := func(i int) float64 { return float64(size[nodes[i]]) / 2 }
	left := make([]float64, n)
	for i := 0; i < n; i++ {
		if i == 0 {
			left[i] = desired[i]
		} else {
			left[i] = math.Max(desired[i], left[i-1]+halfOf(i-1)+sep+halfOf(i))
		}
	}
	right := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		if i == n-1 {
			right[i] = desired[i]
		} else {
			right[i] = math.Min(desired[i], right[i+1]-halfOf(i+1)-sep-halfOf(i))
		}
	}
	for i := 0; i < n; i++ {
		pos[nodes[i]] = (left[i] + right[i]) / 2
	}
	for i := 1; i < n; i++ {
		minP := pos[nodes[i-1]] + halfOf(i-1) + sep + halfOf(i)
		if pos[nodes[i]] < minP {
			pos[nodes[i]] = minP
		}
	}
}

type span5 struct{ s, e, f, t, idx int }

func assignTracks(spans []span5) (assigned [][2]int, count int) {
	sorted := append([]span5(nil), spans...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.s != b.s {
			return a.s < b.s
		}
		if a.e != b.e {
			return a.e < b.e
		}
		if a.f != b.f {
			return a.f < b.f
		}
		if a.t != b.t {
			return a.t < b.t
		}
		return a.idx < b.idx
	})
	type mem struct{ s, e, f, t int }
	var tracks [][]mem
	for _, sp := range sorted {
		slot := -1
		for i, members := range tracks {
			ok := true
			for _, m := range members {
				if !(m.e+2 <= sp.s || sp.e+2 <= m.s || m.f == sp.f || m.t == sp.t) {
					ok = false
					break
				}
			}
			if ok {
				slot = i
				break
			}
		}
		if slot < 0 {
			tracks = append(tracks, nil)
			slot = len(tracks) - 1
		}
		tracks[slot] = append(tracks[slot], mem{sp.s, sp.e, sp.f, sp.t})
		assigned = append(assigned, [2]int{sp.idx, slot})
	}
	return assigned, len(tracks)
}

func busSpans(graph *Graph, ranks, centers []int, r int, exact bool) []span5 {
	var out []span5
	for i, e := range graph.edges {
		jogs := abs(centers[e.From]-centers[e.To]) > 1
		if exact {
			jogs = centers[e.From] != centers[e.To]
		}
		if e.From != e.To && ranks[e.From] == r && ranks[e.To] == r+1 && jogs {
			out = append(out, span5{
				s: min(centers[e.From], centers[e.To]), e: max(centers[e.From], centers[e.To]),
				f: e.From, t: e.To, idx: i,
			})
		}
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func laneSpans(graph *Graph, ranks []int, placed []Placed, vertical bool) []span5 {
	var out []span5
	for i, e := range graph.edges {
		if e.From == e.To || ranks[e.To] == ranks[e.From]+1 {
			continue
		}
		pf, pt := placed[e.From], placed[e.To]
		a, b := pf.cx, pt.cx
		if vertical {
			a, b = pf.cy, pt.cy
		}
		if a > b {
			a, b = b, a
		}
		out = append(out, span5{s: a, e: b, f: e.From, t: e.To, idx: i})
	}
	return out
}
