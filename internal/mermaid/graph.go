package mermaid

const (
	maxNodes      = 128
	maxEdges      = 512
	maxGroups     = 24
	maxGroupDepth = 6
	maxMembers    = 8
)

type Shape string

const (
	shapeRect    Shape = "rect"
	shapeRound   Shape = "round"
	shapeDiamond Shape = "diamond"
)

type Head string

const (
	headNone        Head = "none"
	headArrow       Head = "arrow"
	headCircle      Head = "circle"
	headCross       Head = "cross"
	headTriangle    Head = "triangle"
	headDiamondFill Head = "diamondFill"
	headDiamondOpen Head = "diamondOpen"
)

type LineKind string

const (
	lineSolid  LineKind = "solid"
	lineDotted LineKind = "dotted"
	lineThick  LineKind = "thick"
)

type Dir string

const (
	dirDown  Dir = "down"
	dirUp    Dir = "up"
	dirRight Dir = "right"
	dirLeft  Dir = "left"
)

type Node struct {
	Label string
	Shape Shape
}

type Edge struct {
	From     int
	To       int
	Label    *string
	HeadTo   Head
	HeadFrom Head
	Line     LineKind
}

type Group struct {
	ID     string
	Label  string
	Parent int // -1 if none
}

type ClassInfo struct {
	Annotation *string
	Attrs      []string
	Methods    []string
}

func emptyClassInfo() ClassInfo { return ClassInfo{} }

func parseDir(token string) Dir {
	switch asciiUpper(token) {
	case "LR":
		return dirRight
	case "RL":
		return dirLeft
	case "BT":
		return dirUp
	default:
		return dirDown
	}
}

type Graph struct {
	nodes     []Node
	edges     []Edge
	index     map[string]int
	groups    []Group
	nodeGroup []int
	curGroup  int // -1 if none
	overCap   bool
	warnings  []string
	dir       Dir
}

func newGraph(dir Dir) *Graph {
	return &Graph{
		index:    map[string]int{},
		curGroup: -1,
		warnings: []string{},
		dir:      dir,
	}
}

func (g *Graph) nodeIndex(id string, label *string, shape Shape) (int, bool) {
	if existing, ok := g.index[id]; ok {
		if label != nil {
			g.nodes[existing].Label = *label
			g.nodes[existing].Shape = shape
		}
		return existing, true
	}
	if len(g.nodes) >= maxNodes {
		g.overCap = true
		return 0, false
	}
	g.index[id] = len(g.nodes)
	lab := id
	if label != nil {
		lab = *label
	}
	g.nodes = append(g.nodes, Node{Label: lab, Shape: shape})
	g.nodeGroup = append(g.nodeGroup, g.curGroup)
	return len(g.nodes) - 1, true
}

func (g *Graph) nodeLabel(id, label string) (int, bool) {
	if existing, ok := g.index[id]; ok {
		g.nodes[existing].Label = label
		return existing, true
	}
	return g.nodeIndex(id, &label, shapeRound)
}

func (g *Graph) pushEdge(edge Edge) bool {
	if len(g.edges) >= maxEdges {
		g.overCap = true
		return false
	}
	g.edges = append(g.edges, edge)
	return true
}
