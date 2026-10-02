package mermaid

// cont occupies the trailing column of a wide glyph. It is never emitted.
const cont = "\x00"

const (
	bitU = 1
	bitD = 2
	bitL = 4
	bitR = 8
)

const (
	styDot   = 1
	styThick = 2
	stySolid = 4
)

// Canvas is a cell grid. Edges accumulate as direction bits so crossings
// resolve independently of draw order; finalizeMask turns bits into glyphs.
type Canvas struct {
	w, h     int
	ch       []string
	cls      []Cls
	mask     []byte
	style    []byte
	occupied []byte
	curStyle byte
}

func newCanvas(w, h int) *Canvas {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	n := w * h
	ch := make([]string, n)
	cls := make([]Cls, n)
	for i := range ch {
		ch[i] = " "
		cls[i] = ClsNone
	}
	return &Canvas{
		w: w, h: h,
		ch: ch, cls: cls,
		mask: make([]byte, n), style: make([]byte, n), occupied: make([]byte, n),
		curStyle: stySolid,
	}
}

func (c *Canvas) idx(x, y int) int { return y*c.w + x }

func (c *Canvas) in(x, y int) bool {
	return x >= 0 && y >= 0 && x < c.w && y < c.h
}

func (c *Canvas) set(x, y int, s string, cls Cls) {
	if !c.in(x, y) {
		return
	}
	i := c.idx(x, y)
	c.ch[i] = s
	c.cls[i] = cls
}

func (c *Canvas) addBits(x, y, bits int, cls Cls) {
	if !c.in(x, y) {
		return
	}
	i := c.idx(x, y)
	if c.occupied[i] != 0 {
		return
	}
	c.mask[i] |= byte(bits)
	c.style[i] |= c.curStyle
	if c.cls[i] != ClsBorder {
		c.cls[i] = cls
	}
}

func (c *Canvas) blit(sub *Canvas, ox, oy int) {
	for sy := 0; sy < sub.h; sy++ {
		for sx := 0; sx < sub.w; sx++ {
			x, y := ox+sx, oy+sy
			if !c.in(x, y) {
				continue
			}
			si := sub.idx(sx, sy)
			di := c.idx(x, y)
			c.ch[di] = sub.ch[si]
			c.cls[di] = sub.cls[si]
			c.style[di] = sub.style[si]
			c.occupied[di] = 1
		}
	}
}

func (c *Canvas) junction(x, y, bits int) {
	if !c.in(x, y) {
		return
	}
	i := c.idx(x, y)
	c.mask[i] |= byte(bits)
	if c.cls[i] != ClsBorder {
		c.cls[i] = ClsEdge
	}
}

func (c *Canvas) segV(x, y0, y1 int) {
	a, b := y0, y1
	if a > b {
		a, b = b, a
	}
	for y := a; y <= b; y++ {
		bits := 0
		if y > a {
			bits |= bitU
		}
		if y < b {
			bits |= bitD
		}
		c.addBits(x, y, bits, ClsEdge)
	}
}

func (c *Canvas) segH(y, x0, x1 int) {
	a, b := x0, x1
	if a > b {
		a, b = b, a
	}
	for x := a; x <= b; x++ {
		bits := 0
		if x > a {
			bits |= bitL
		}
		if x < b {
			bits |= bitR
		}
		c.addBits(x, y, bits, ClsEdge)
	}
}

func (c *Canvas) finalizeMask() {
	for i := range c.ch {
		if c.mask[i] != 0 && c.ch[i] == " " {
			g := maskChar(int(c.mask[i]))
			switch c.style[i] {
			case styDot:
				c.ch[i] = dottedChar(g)
			case styThick:
				c.ch[i] = thickChar(g)
			default:
				c.ch[i] = g
			}
		}
	}
}

func (c *Canvas) flipVertical() {
	for y := 0; y < c.h/2; y++ {
		y2 := c.h - 1 - y
		for x := 0; x < c.w; x++ {
			i, j := c.idx(x, y), c.idx(x, y2)
			c.ch[i], c.ch[j] = c.ch[j], c.ch[i]
			c.cls[i], c.cls[j] = c.cls[j], c.cls[i]
		}
	}
	for i := range c.ch {
		c.ch[i] = flipGlyphV(c.ch[i])
	}
}

func (c *Canvas) flipHorizontal() {
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w/2; x++ {
			x2 := c.w - 1 - x
			i, j := c.idx(x, y), c.idx(x2, y)
			c.ch[i], c.ch[j] = c.ch[j], c.ch[i]
			c.cls[i], c.cls[j] = c.cls[j], c.cls[i]
		}
	}
	for i := range c.ch {
		c.ch[i] = flipGlyphH(c.ch[i])
	}
	for y := 0; y < c.h; y++ {
		x := 0
		for x < c.w {
			cls := c.cls[c.idx(x, y)]
			if cls == ClsText || cls == ClsEdgeLabel {
				start := c.idx(x, y)
				for x < c.w && c.cls[c.idx(x, y)] == cls {
					x++
				}
				reverseSlice(c.ch, start, c.idx(x, y))
			} else {
				x++
			}
		}
	}
}

func reverseSlice(arr []string, start, end int) {
	for i, j := start, end-1; i < j; i, j = i+1, j-1 {
		arr[i], arr[j] = arr[j], arr[i]
	}
}

func (c *Canvas) toLines() MermaidArt {
	plain := make([]string, 0, c.h)
	styled := make([][]Span, 0, c.h)
	width := 0
	for y := 0; y < c.h; y++ {
		last := 0
		for x := c.w - 1; x >= 0; x-- {
			if c.ch[c.idx(x, y)] != " " {
				last = x + 1
				break
			}
		}
		if last > width {
			width = last
		}
		spans := make([]Span, 0)
		plainRow := ""
		run := ""
		runCls := ClsNone
		for x := 0; x < last; x++ {
			i := c.idx(x, y)
			ch := c.ch[i]
			if ch == cont {
				continue
			}
			cls := c.cls[i]
			plainRow += ch
			if cls != runCls && run != "" {
				spans = append(spans, Span{Text: run, Cls: runCls})
				run = ""
			}
			runCls = cls
			run += ch
		}
		if run != "" {
			spans = append(spans, Span{Text: run, Cls: runCls})
		}
		styled = append(styled, spans)
		plain = append(plain, trimRightASCIISpace(plainRow))
	}
	first := 0
	for first < len(plain) && plain[first] == "" {
		first++
	}
	end := len(plain)
	for end > first && plain[end-1] == "" {
		end--
	}
	return MermaidArt{
		Plain:    plain[first:end],
		Styled:   styled[first:end],
		Width:    width,
		Warnings: []string{},
	}
}

func trimRightASCIISpace(s string) string {
	i := len(s)
	for i > 0 && s[i-1] == ' ' {
		i--
	}
	return s[:i]
}

func drawText(canvas *Canvas, text string, x, y int, cls Cls) {
	cur := x
	for _, cl := range measured(text) {
		if cl.w == 0 {
			continue
		}
		canvas.set(cur, y, cl.s, cls)
		for k := 1; k < cl.w; k++ {
			canvas.set(cur+k, y, cont, cls)
		}
		cur += cl.w
	}
}

func drawTextOverEdges(canvas *Canvas, text string, x, y int, cls Cls) {
	cur := x
	for _, cl := range measured(text) {
		if cl.w == 0 {
			continue
		}
		for k := 0; k < cl.w; k++ {
			if canvas.in(cur+k, y) {
				canvas.mask[canvas.idx(cur+k, y)] = 0
			}
			glyph := cont
			if k == 0 {
				glyph = cl.s
			}
			canvas.set(cur+k, y, glyph, cls)
		}
		cur += cl.w
	}
}

func maskChar(mask int) string {
	switch mask {
	case 0:
		return " "
	case bitU, bitD, bitU | bitD:
		return "│"
	case bitL, bitR, bitL | bitR:
		return "─"
	case bitD | bitR:
		return "┌"
	case bitD | bitL:
		return "┐"
	case bitU | bitR:
		return "└"
	case bitU | bitL:
		return "┘"
	case bitU | bitD | bitR:
		return "├"
	case bitU | bitD | bitL:
		return "┤"
	case bitD | bitL | bitR:
		return "┬"
	case bitU | bitL | bitR:
		return "┴"
	default:
		return "┼"
	}
}

var (
	dottedGlyph = map[string]string{"─": "╌", "│": "╎"}
	thickGlyph  = map[string]string{
		"─": "━", "│": "┃", "┌": "┏", "┐": "┓", "└": "┗", "┘": "┛",
		"├": "┣", "┤": "┫", "┬": "┳", "┴": "┻", "┼": "╋",
	}
	flipV = map[string]string{
		"┌": "└", "└": "┌", "┐": "┘", "┘": "┐",
		"┏": "┗", "┗": "┏", "┓": "┛", "┛": "┓",
		"╭": "╰", "╰": "╭", "╮": "╯", "╯": "╮",
		"┬": "┴", "┴": "┬", "┳": "┻", "┻": "┳",
		"▼": "▲", "▲": "▼", "▽": "△", "△": "▽",
	}
	flipH = map[string]string{
		"┌": "┐", "┐": "┌", "└": "┘", "┘": "└",
		"┏": "┓", "┓": "┏", "┗": "┛", "┛": "┗",
		"╭": "╮", "╮": "╭", "╰": "╯", "╯": "╰",
		"├": "┤", "┤": "├", "┣": "┫", "┫": "┣",
		"▶": "◄", "◄": "▶", "▷": "◁", "◁": "▷",
	}
)

func lookupGlyph(m map[string]string, c string) string {
	if r, ok := m[c]; ok {
		return r
	}
	return c
}

func dottedChar(c string) string { return lookupGlyph(dottedGlyph, c) }
func thickChar(c string) string  { return lookupGlyph(thickGlyph, c) }
func flipGlyphV(c string) string { return lookupGlyph(flipV, c) }
func flipGlyphH(c string) string { return lookupGlyph(flipH, c) }
