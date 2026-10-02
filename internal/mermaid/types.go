package mermaid

import "errors"

// Cls is the semantic class of a run of cells. The renderer does not pick
// colours; callers map these the way grok-mermaid's Cls is mapped.
type Cls string

const (
	ClsBorder    Cls = "border"
	ClsText      Cls = "text"
	ClsEdge      Cls = "edge"
	ClsEdgeLabel Cls = "edgeLabel"
	ClsTitle     Cls = "title"
	ClsNone      Cls = "none"
)

// Span is a run of adjacent cells sharing one semantic class (TS Span).
type Span struct {
	Text string `json:"text"`
	Cls  Cls    `json:"cls"`
}

// MermaidArt is a rendered diagram. Plain[i] and the join of Styled[i] are the
// same row; Width is the display columns of the widest row.
type MermaidArt struct {
	Plain    []string `json:"plain"`
	Styled   [][]Span `json:"styled"`
	Width    int      `json:"width"`
	Warnings []string `json:"warnings"`
}

// Options configures Render. grok-mermaid 0.2.3's render(src) takes no
// options; the zero value reproduces that call.
type Options struct{}

// ErrNoDiagram is returned when render would return null: blank input, a
// diagram type this renderer does not draw, a syntax error the grammar
// refuses, or a diagram too large to lay out.
var ErrNoDiagram = errors.New("mermaid: no diagram")

// Diagram kinds recognised by DiagramKind. Empty string means none.
const (
	KindFlowchart = "flowchart"
	KindState     = "state"
	KindClass     = "class"
	KindER        = "er"
	KindSequence  = "sequence"
)
