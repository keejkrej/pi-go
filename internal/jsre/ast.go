package jsre

import "math"

// infinity is V8's RegExpTree::kInfinity.
const infinity = math.MaxInt32

// modFlags are the flags that pattern modifiers ((?ims-ims:...)) can change.
type modFlags uint8

const (
	flagIgnoreCase modFlags = 1 << iota
	flagMultiline
	flagDotAll
)

type nodeKind uint8

const (
	nEmpty   nodeKind = iota
	nChar             // ch
	nClass            // cls
	nDot              // fl&flagDotAll
	nSeq              // kids
	nAlt              // kids
	nCapture          // index, kids[0]
	nGroup            // kids[0]
	nLook             // neg, behind, kids[0]
	nBackref          // refs (patched for named references), name
	nAssert           // assert
	nQuant            // min, max, greedy, kids[0]
)

type assertKind uint8

const (
	assertStartOfInput assertKind = iota
	assertStartOfLine
	assertEndOfInput
	assertEndOfLine
	assertBoundary
	assertNonBoundary
)

// node is the parsed pattern tree. It is close to V8's RegExpTree: characters
// are individual nodes, and every node records the modifier flags in effect
// where it was parsed.
type node struct {
	kind   nodeKind
	fl     modFlags
	ch     rune
	cls    *classNode
	kids   []*node
	index  int // capture index (1-based)
	name   string
	refs   []int // backreference targets (capture indices)
	neg    bool
	behind bool
	assert assertKind
	min    int
	max    int
	greedy bool
}

// classItem is one element of a character class: a literal range, a class
// escape (\d \D \s \S \w \W), or a property escape (\p{...} \P{...}).
type classItem struct {
	lo, hi rune
	esc    byte   // 0 for a literal range; 'd' 'D' 's' 'S' 'w' 'W' 'p'
	neg    bool   // \P
	prop   string // property name for esc == 'p'
	foldW  bool   // \w/\W under /iu: include the case closure of \w
}

type classNode struct {
	negated bool
	items   []classItem
}

func satAdd(a, b int) int {
	if a > infinity-b {
		return infinity
	}
	return a + b
}

func satMul(n, body int) int {
	if n > 0 && body > infinity/n {
		return infinity
	}
	return n * body
}

// minMatch and maxMatch mirror RegExpTree::min_match/max_match (in characters
// of the matching domain; only zero versus non-zero matters for the callers).
func minMatch(n *node) int {
	switch n.kind {
	case nChar, nClass, nDot:
		return 1
	case nEmpty, nAssert, nLook, nBackref:
		return 0
	case nCapture, nGroup:
		return minMatch(n.kids[0])
	case nSeq:
		total := 0
		for _, k := range n.kids {
			total = satAdd(total, minMatch(k))
		}
		return total
	case nAlt:
		least := infinity
		for _, k := range n.kids {
			least = min(least, minMatch(k))
		}
		return least
	case nQuant:
		return satMul(n.min, minMatch(n.kids[0]))
	}
	return 0
}

func maxMatch(n *node) int {
	switch n.kind {
	case nChar, nClass, nDot:
		return 1
	case nEmpty, nAssert, nLook:
		return 0
	case nBackref:
		return infinity
	case nCapture, nGroup:
		return maxMatch(n.kids[0])
	case nSeq:
		total := 0
		for _, k := range n.kids {
			total = satAdd(total, maxMatch(k))
		}
		return total
	case nAlt:
		most := 0
		for _, k := range n.kids {
			most = max(most, maxMatch(k))
		}
		return most
	case nQuant:
		return satMul(n.max, maxMatch(n.kids[0]))
	}
	return 0
}

// capturesIn appends the capture indices contained in n, in pattern order.
func capturesIn(n *node, out []int) []int {
	if n.kind == nCapture {
		out = append(out, n.index)
	}
	for _, k := range n.kids {
		out = capturesIn(k, out)
	}
	return out
}
