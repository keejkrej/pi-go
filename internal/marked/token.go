package marked

// Token is one node in a marked token tree. Which fields are set depends on
// Type, matching marked's Tokens.* objects. Nested Tokens and list Items keep
// source order.
//
// Title is nil when marked stores null or omits the property. Lang is nil when
// the token has no lang (indented code). Start is nil for unordered lists,
// which marked stores as "". Checked is nil when the property is absent.
type Token struct {
	Type           string
	Raw            string
	Text           string
	Tokens         []*Token
	Href           string
	Title          *string
	Depth          int
	Lang           *string
	CodeBlockStyle string
	Escaped        bool
	Ordered        bool
	Start          *int
	Loose          bool
	Items          []*Token
	Task           bool
	Checked        *bool
	Align          []*string
	Header         []TableCell
	Rows           [][]TableCell
	Pre            bool
	Block          bool
	InLink         bool
	InRawBlock     bool
	Tag            string
	// Pending is set by extensions such as pi's streamed LaTeX tokenizer.
	Pending bool
	// Extra holds extension fields walkTokens should visit when a
	// TokenizerExtension lists them in ChildTokens and they are not Tokens or Items.
	Extra map[string][]*Token
}

// TableCell is a GFM table header or body cell.
type TableCell struct {
	Text   string
	Tokens []*Token
	Header bool
	Align  *string
}

// Link is a link reference definition (marked's links map entry).
type Link struct {
	Href  string
	Title *string
}

func strPtr(s string) *string { return &s }

func boolPtr(b bool) *bool { return &b }

func intPtr(n int) *int { return &n }
