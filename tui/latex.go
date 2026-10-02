// Ported from packages/tui/src/latex.ts (pi v1.0.0).

package tui

import (
	"regexp"

	"github.com/keejkrej/pi-go/internal/jsre"
)

var latexSymbols = map[string]string{
	"alpha":              "α",
	"beta":               "β",
	"gamma":              "γ",
	"delta":              "δ",
	"epsilon":            "ϵ",
	"varepsilon":         "ε",
	"zeta":               "ζ",
	"eta":                "η",
	"theta":              "θ",
	"vartheta":           "ϑ",
	"iota":               "ι",
	"kappa":              "κ",
	"varkappa":           "ϰ",
	"lambda":             "λ",
	"mu":                 "μ",
	"nu":                 "ν",
	"xi":                 "ξ",
	"pi":                 "π",
	"varpi":              "ϖ",
	"rho":                "ρ",
	"varrho":             "ϱ",
	"sigma":              "σ",
	"varsigma":           "ς",
	"tau":                "τ",
	"upsilon":            "υ",
	"phi":                "ϕ",
	"varphi":             "φ",
	"chi":                "χ",
	"psi":                "ψ",
	"omega":              "ω",
	"Gamma":              "Γ",
	"Delta":              "Δ",
	"Theta":              "Θ",
	"Lambda":             "Λ",
	"Xi":                 "Ξ",
	"Pi":                 "Π",
	"Sigma":              "Σ",
	"Upsilon":            "Υ",
	"Phi":                "Φ",
	"Psi":                "Ψ",
	"Omega":              "Ω",
	"pm":                 "±",
	"mp":                 "∓",
	"times":              "×",
	"div":                "÷",
	"cdot":               "·",
	"ast":                "∗",
	"star":               "⋆",
	"circ":               "∘",
	"bullet":             "•",
	"oplus":              "⊕",
	"ominus":             "⊖",
	"otimes":             "⊗",
	"oslash":             "⊘",
	"odot":               "⊙",
	"bigcirc":            "○",
	"dagger":             "†",
	"ddagger":            "‡",
	"amalg":              "⨿",
	"uplus":              "⊎",
	"sqcap":              "⊓",
	"sqcup":              "⊔",
	"bowtie":             "⋈",
	"Join":               "⋈",
	"ltimes":             "⋉",
	"rtimes":             "⋊",
	"leftouterjoin":      "⟕",
	"rightouterjoin":     "⟖",
	"fullouterjoin":      "⟗",
	"triangleleft":       "◁",
	"triangleright":      "▷",
	"wr":                 "≀",
	"cap":                "∩",
	"cup":                "∪",
	"bigcap":             "⋂",
	"bigcup":             "⋃",
	"bigwedge":           "⋀",
	"bigvee":             "⋁",
	"bigsqcup":           "⨆",
	"biguplus":           "⨄",
	"bigoplus":           "⨁",
	"bigotimes":          "⨂",
	"bigodot":            "⨀",
	"setminus":           "∖",
	"in":                 "∈",
	"notin":              "∉",
	"ni":                 "∋",
	"subset":             "⊂",
	"supset":             "⊃",
	"subseteq":           "⊆",
	"supseteq":           "⊇",
	"sqsubset":           "⊏",
	"sqsupset":           "⊐",
	"sqsubseteq":         "⊑",
	"sqsupseteq":         "⊒",
	"prec":               "≺",
	"preceq":             "≼",
	"succ":               "≻",
	"succeq":             "≽",
	"ll":                 "≪",
	"gg":                 "≫",
	"le":                 "≤",
	"leq":                "≤",
	"leqslant":           "≤",
	"ge":                 "≥",
	"geq":                "≥",
	"geqslant":           "≥",
	"ne":                 "≠",
	"neq":                "≠",
	"equiv":              "≡",
	"approx":             "≈",
	"sim":                "∼",
	"simeq":              "≃",
	"cong":               "≅",
	"asymp":              "≍",
	"doteq":              "≐",
	"propto":             "∝",
	"parallel":           "∥",
	"perp":               "⊥",
	"mid":                "∣",
	"vdash":              "⊢",
	"dashv":              "⊣",
	"models":             "⊨",
	"Vdash":              "⊩",
	"Vvdash":             "⊪",
	"nvdash":             "⊬",
	"nvDash":             "⊭",
	"forall":             "∀",
	"exists":             "∃",
	"nexists":            "∄",
	"neg":                "¬",
	"land":               "∧",
	"wedge":              "∧",
	"lor":                "∨",
	"vee":                "∨",
	"to":                 "→",
	"rightarrow":         "→",
	"longrightarrow":     "→",
	"leftarrow":          "←",
	"longleftarrow":      "←",
	"gets":               "←",
	"leftrightarrow":     "↔",
	"longleftrightarrow": "↔",
	"hookleftarrow":      "↩",
	"hookrightarrow":     "↪",
	"twoheadleftarrow":   "↞",
	"twoheadrightarrow":  "↠",
	"leftharpoonup":      "↼",
	"leftharpoondown":    "↽",
	"rightharpoonup":     "⇀",
	"rightharpoondown":   "⇁",
	"rightleftharpoons":  "⇌",
	"leftrightharpoons":  "⇋",
	"nearrow":            "↗",
	"searrow":            "↘",
	"swarrow":            "↙",
	"nwarrow":            "↖",
	"rightsquigarrow":    "⇝",
	"leadsto":            "⇝",
	"Rightarrow":         "⇒",
	"Longrightarrow":     "⇒",
	"Leftarrow":          "⇐",
	"Longleftarrow":      "⇐",
	"Leftrightarrow":     "⇔",
	"Longleftrightarrow": "⇔",
	"implies":            "⇒",
	"iff":                "⇔",
	"mapsto":             "↦",
	"longmapsto":         "↦",
	"uparrow":            "↑",
	"downarrow":          "↓",
	"partial":            "∂",
	"nabla":              "∇",
	"int":                "∫",
	"iint":               "∬",
	"iiint":              "∭",
	"oint":               "∮",
	"sum":                "∑",
	"prod":               "∏",
	"coprod":             "∐",
	"infty":              "∞",
	"emptyset":           "∅",
	"varnothing":         "∅",
	"angle":              "∠",
	"therefore":          "∴",
	"because":            "∵",
	"aleph":              "ℵ",
	"beth":               "ℶ",
	"gimel":              "ℷ",
	"daleth":             "ℸ",
	"top":                "⊤",
	"bot":                "⊥",
	"triangle":           "△",
	"square":             "□",
	"lozenge":            "◊",
	"checkmark":          "✓",
	"complement":         "∁",
	"wp":                 "℘",
	"prime":              "′",
	"ldots":              "…",
	"dots":               "…",
	"cdots":              "⋯",
	"vdots":              "⋮",
	"ddots":              "⋱",
	"ell":                "ℓ",
	"hbar":               "ℏ",
	"Im":                 "ℑ",
	"Re":                 "ℜ",
	"langle":             "⟨",
	"rangle":             "⟩",
	"vert":               "|",
	"lvert":              "|",
	"rvert":              "|",
	"Vert":               "‖",
	"lVert":              "‖",
	"rVert":              "‖",
	"lbrace":             "{",
	"rbrace":             "}",
	"backslash":          "\\",
	"lfloor":             "⌊",
	"rfloor":             "⌋",
	"lceil":              "⌈",
	"rceil":              "⌉",
	"colon":              ":",
}

var latexNamedOperators = map[string]struct{}{
	"arccos": {},
	"arcsin": {},
	"arctan": {},
	"arg":    {},
	"cos":    {},
	"cosh":   {},
	"cot":    {},
	"coth":   {},
	"csc":    {},
	"deg":    {},
	"det":    {},
	"dim":    {},
	"exp":    {},
	"gcd":    {},
	"hom":    {},
	"inf":    {},
	"ker":    {},
	"lg":     {},
	"lim":    {},
	"liminf": {},
	"limsup": {},
	"ln":     {},
	"log":    {},
	"max":    {},
	"min":    {},
	"Pr":     {},
	"sec":    {},
	"sin":    {},
	"sinh":   {},
	"sup":    {},
	"tan":    {},
	"tanh":   {},
}

var latexLimitOperators = map[string]struct{}{
	"argmax":  {},
	"argmin":  {},
	"inf":     {},
	"injlim":  {},
	"lim":     {},
	"liminf":  {},
	"limsup":  {},
	"max":     {},
	"min":     {},
	"projlim": {},
	"sup":     {},
}

var latexDisplayLimitSymbols = map[string]struct{}{
	"bigcap":    {},
	"bigcup":    {},
	"bigodot":   {},
	"bigoplus":  {},
	"bigotimes": {},
	"bigsqcup":  {},
	"biguplus":  {},
	"bigvee":    {},
	"bigwedge":  {},
	"coprod":    {},
	"int":       {},
	"iint":      {},
	"iiint":     {},
	"oint":      {},
	"prod":      {},
	"sum":       {},
}

var latexRelationCommands = map[string]struct{}{
	"Leftarrow":          {},
	"Leftrightarrow":     {},
	"Longleftarrow":      {},
	"Longleftrightarrow": {},
	"Longrightarrow":     {},
	"Rightarrow":         {},
	"Join":               {},
	"Vdash":              {},
	"Vvdash":             {},
	"approx":             {},
	"asymp":              {},
	"bowtie":             {},
	"cong":               {},
	"dashv":              {},
	"fullouterjoin":      {},
	"doteq":              {},
	"downarrow":          {},
	"equiv":              {},
	"ge":                 {},
	"geq":                {},
	"geqslant":           {},
	"gets":               {},
	"gg":                 {},
	"hookleftarrow":      {},
	"hookrightarrow":     {},
	"iff":                {},
	"implies":            {},
	"in":                 {},
	"leadsto":            {},
	"le":                 {},
	"leftarrow":          {},
	"leftharpoondown":    {},
	"leftharpoonup":      {},
	"leftrightarrow":     {},
	"leftrightharpoons":  {},
	"leftouterjoin":      {},
	"leq":                {},
	"leqslant":           {},
	"ll":                 {},
	"longleftarrow":      {},
	"longleftrightarrow": {},
	"longmapsto":         {},
	"longrightarrow":     {},
	"ltimes":             {},
	"mapsto":             {},
	"mid":                {},
	"models":             {},
	"ne":                 {},
	"nearrow":            {},
	"neq":                {},
	"ni":                 {},
	"notin":              {},
	"nvdash":             {},
	"nvDash":             {},
	"nwarrow":            {},
	"parallel":           {},
	"perp":               {},
	"prec":               {},
	"preceq":             {},
	"propto":             {},
	"rightharpoondown":   {},
	"rightharpoonup":     {},
	"rightleftharpoons":  {},
	"rightouterjoin":     {},
	"rightarrow":         {},
	"rightsquigarrow":    {},
	"rtimes":             {},
	"searrow":            {},
	"sim":                {},
	"simeq":              {},
	"sqsubset":           {},
	"sqsubseteq":         {},
	"sqsupset":           {},
	"sqsupseteq":         {},
	"subset":             {},
	"subseteq":           {},
	"succ":               {},
	"succeq":             {},
	"supset":             {},
	"supseteq":           {},
	"swarrow":            {},
	"to":                 {},
	"triangleleft":       {},
	"triangleright":      {},
	"twoheadleftarrow":   {},
	"twoheadrightarrow":  {},
	"uparrow":            {},
	"vdash":              {},
}

var latexNegatedSymbols = map[string]string{
	"<": "≮",
	">": "≯",
	"=": "≠",
	"∈": "∉",
	"∋": "∌",
	"∣": "∤",
	"∥": "∦",
	"∼": "≁",
	"≃": "≄",
	"≅": "≇",
	"≈": "≉",
	"≡": "≢",
	"≤": "≰",
	"≥": "≱",
	"≺": "⊀",
	"≻": "⊁",
	"⊂": "⊄",
	"⊃": "⊅",
	"⊆": "⊈",
	"⊇": "⊉",
	"⊢": "⊬",
	"⊨": "⊭",
	"↔": "↮",
	"←": "↚",
	"→": "↛",
	"⇒": "⇏",
	"⇐": "⇍",
	"⇔": "⇎",
	"≼": "⋠",
	"≽": "⋡",
}

var latexBlackboard = map[string]string{
	"C": "ℂ",
	"H": "ℍ",
	"N": "ℕ",
	"P": "ℙ",
	"Q": "ℚ",
	"R": "ℝ",
	"Z": "ℤ",
}

var latexSuperscripts = map[string]string{
	"0": "⁰",
	"1": "¹",
	"2": "²",
	"3": "³",
	"4": "⁴",
	"5": "⁵",
	"6": "⁶",
	"7": "⁷",
	"8": "⁸",
	"9": "⁹",
	"+": "⁺",
	"-": "⁻",
	"=": "⁼",
	"(": "⁽",
	")": "⁾",
	"a": "ᵃ",
	"b": "ᵇ",
	"c": "ᶜ",
	"d": "ᵈ",
	"e": "ᵉ",
	"f": "ᶠ",
	"g": "ᵍ",
	"h": "ʰ",
	"i": "ⁱ",
	"j": "ʲ",
	"k": "ᵏ",
	"l": "ˡ",
	"m": "ᵐ",
	"n": "ⁿ",
	"o": "ᵒ",
	"p": "ᵖ",
	"r": "ʳ",
	"s": "ˢ",
	"t": "ᵗ",
	"u": "ᵘ",
	"v": "ᵛ",
	"w": "ʷ",
	"x": "ˣ",
	"y": "ʸ",
	"z": "ᶻ",
}

var latexSubscripts = map[string]string{
	"0": "₀",
	"1": "₁",
	"2": "₂",
	"3": "₃",
	"4": "₄",
	"5": "₅",
	"6": "₆",
	"7": "₇",
	"8": "₈",
	"9": "₉",
	"+": "₊",
	"-": "₋",
	"=": "₌",
	"(": "₍",
	")": "₎",
	"a": "ₐ",
	"e": "ₑ",
	"h": "ₕ",
	"i": "ᵢ",
	"j": "ⱼ",
	"k": "ₖ",
	"l": "ₗ",
	"m": "ₘ",
	"n": "ₙ",
	"o": "ₒ",
	"p": "ₚ",
	"r": "ᵣ",
	"s": "ₛ",
	"t": "ₜ",
	"u": "ᵤ",
	"v": "ᵥ",
	"x": "ₓ",
}

var latexSpacingCommands = map[string]struct{}{
	",":          {},
	":":          {},
	";":          {},
	" ":          {},
	">":          {},
	"enspace":    {},
	"enskip":     {},
	"medspace":   {},
	"quad":       {},
	"qquad":      {},
	"thickspace": {},
	"thinspace":  {},
}

const latexNegativeSpace = "\u0000"

var latexNegativeSpacingCommands = map[string]struct{}{
	"!":             {},
	"negmedspace":   {},
	"negthickspace": {},
	"negthinspace":  {},
}

var latexFontSwitchCommands = map[string]struct{}{
	"bf":  {},
	"cal": {},
	"it":  {},
	"rm":  {},
	"sf":  {},
	"sl":  {},
	"tt":  {},
}

var latexIgnoredCommands = map[string]struct{}{
	"displaystyle":      {},
	"limits":            {},
	"nolimits":          {},
	"scriptstyle":       {},
	"scriptscriptstyle": {},
	"textstyle":         {},
}

var latexSizeCommands = map[string]struct{}{
	"big":   {},
	"Big":   {},
	"bigg":  {},
	"Bigg":  {},
	"bigl":  {},
	"Bigl":  {},
	"biggl": {},
	"Biggl": {},
	"bigr":  {},
	"Bigr":  {},
	"biggr": {},
	"Biggr": {},
}

var latexPlainWrappers = map[string]struct{}{
	"emph":       {},
	"mathcal":    {},
	"mathbf":     {},
	"mathfrak":   {},
	"mathit":     {},
	"mathrm":     {},
	"mathnormal": {},
	"mathscr":    {},
	"mathsf":     {},
	"mathtt":     {},
	"mathup":     {},
	"mbox":       {},
	"overbrace":  {},
	"pmb":        {},
	"smash":      {},
	"substack":   {},
	"text":       {},
	"textbf":     {},
	"textit":     {},
	"textmd":     {},
	"textnormal": {},
	"textrm":     {},
	"textsc":     {},
	"textsf":     {},
	"textsl":     {},
	"texttt":     {},
	"textup":     {},
	"underbrace": {},
	"bm":         {},
	"boldsymbol": {},
}

var latexAccents = map[string]string{
	"acute":              "\u0301",
	"bar":                "\u0305",
	"breve":              "\u0306",
	"check":              "\u030c",
	"ddot":               "\u0308",
	"dot":                "\u0307",
	"grave":              "\u0300",
	"hat":                "\u0302",
	"mathring":           "\u030a",
	"overleftarrow":      "\u20d6",
	"overleftrightarrow": "\u20e1",
	"overline":           "\u0305",
	"overrightarrow":     "\u20d7",
	"tilde":              "\u0303",
	"underline":          "\u0332",
	"vec":                "\u20d7",
	"widehat":            "\u0302",
	"widetilde":          "\u0303",
}

// latexScriptKind is the TS script kind "sub" | "sup".
type latexScriptKind string

const (
	latexScriptKindSub latexScriptKind = "sub"
	latexScriptKindSup latexScriptKind = "sup"
)

// latexInlineLowerStyle is the TS inline lower-limit style "bracket" | "script".
type latexInlineLowerStyle string

const (
	latexInlineLowerStyleBracket latexInlineLowerStyle = "bracket"
	latexInlineLowerStyleScript  latexInlineLowerStyle = "script"
)

func latexReplaceCharacters(value string, replacements map[string]string) *string {
	panic("unported: latexReplaceCharacters")
}

func latexNormalizeScriptValue(value string) string {
	panic("unported: latexNormalizeScriptValue")
}

func latexFormatUnicodeScript(value string, kind latexScriptKind) *string {
	panic("unported: latexFormatUnicodeScript")
}

func latexFormatScript(value string, kind latexScriptKind) string {
	panic("unported: latexFormatScript")
}

func latexFormatFraction(numerator string, denominator string) string {
	panic("unported: latexFormatFraction")
}

// latexFormatRoot formats a radical. A nil symbol means "√".
func latexFormatRoot(value string, symbol *string) string {
	panic("unported: latexFormatRoot")
}

const (
	latexNamedOperatorStart = "\U000F0004"
	latexNamedOperatorEnd   = "\U000F0005"
)

var latexNamedOperatorLeftSpacingPattern = jsre.MustCompile(`(?<=[\p{L}\p{N})\]}\u{f0001}])\u{f0004}`, "gu")

var latexNamedOperatorRightSpacingPattern = jsre.MustCompile(`\u{f0005}(?=[\p{L}\p{N}√\u{f0000}])`, "gu")

func latexNormalizeOutput(value string) string {
	panic("unported: latexNormalizeOutput")
}

// latexFractionNode is a stacked fraction (TS type "fraction").
type latexFractionNode struct {
	Type        string
	Numerator   string
	Denominator string
}

// latexOperatorNode is an operator with optional limits (TS type "operator").
type latexOperatorNode struct {
	Type     string
	Operator string
	Lower    *string
	Upper    *string
}

// latexScriptNode is a stacked subscript and superscript (TS type "script").
type latexScriptNode struct {
	Type  string
	Lower *string
	Upper *string
}

// latexMatrixNode is a multi-line matrix or cases block (TS type "matrix").
type latexMatrixNode struct {
	Type     string
	Lines    []string
	Baseline int
}

// latexLayoutNode is FractionNode | OperatorNode | ScriptNode | MatrixNode.
// Variants are pointers. The concrete type is the discriminator.
type latexLayoutNode interface {
	isLatexLayoutNode()
}

func (*latexFractionNode) isLatexLayoutNode() {}

func (*latexOperatorNode) isLatexLayoutNode() {}

func (*latexScriptNode) isLatexLayoutNode() {}

func (*latexMatrixNode) isLatexLayoutNode() {}

var (
	_ latexLayoutNode = (*latexFractionNode)(nil)
	_ latexLayoutNode = (*latexOperatorNode)(nil)
	_ latexLayoutNode = (*latexScriptNode)(nil)
	_ latexLayoutNode = (*latexMatrixNode)(nil)
)

// latexLayout is one rendered block and the row its baseline sits on.
type latexLayout struct {
	Lines    []string
	Width    int
	Baseline int
}

const (
	latexLayoutMarkerStart = "\U000F0000"
	latexLayoutMarkerEnd   = "\U000F0001"
	latexProtectedSpace    = "\U000F0002"
)

var latexLayoutMarkerPattern = regexp.MustCompile(`\x{F0000}(\d+)\x{F0001}`)

var latexTrailingLayoutMarkerPattern = regexp.MustCompile(`\x{F0000}(\d+)\x{F0001}$`)

func latexPadLayoutLine(line string, width int, centered bool) string {
	panic("unported: latexPadLayoutLine")
}

func latexJoinLayouts(layouts []latexLayout) latexLayout {
	panic("unported: latexJoinLayouts")
}

func latexRenderLayout(source string, nodes []latexLayoutNode) latexLayout {
	panic("unported: latexRenderLayout")
}

// latexLatexParser is the TS LatexParser.
// layoutNodes is shared with nested parsers, so pushes stay visible to the parent.
// supported and stackFractions both start true.
type latexLatexParser struct {
	source         string
	layoutNodes    *[]latexLayoutNode
	display        bool
	position       int
	supported      bool
	stackFractions bool
	scriptDepth    int
}

func latexNewLatexParser(source string, layoutNodes *[]latexLayoutNode, display bool) *latexLatexParser {
	return &latexLatexParser{
		source:         source,
		layoutNodes:    layoutNodes,
		display:        display,
		supported:      true,
		stackFractions: true,
	}
}

func (p *latexLatexParser) render() *string {
	panic("unported: latexLatexParser.render")
}

// endCharacter nil means parse until the source is consumed.
func (p *latexLatexParser) parseSequence(endCharacter *string) string {
	panic("unported: latexLatexParser.parseSequence")
}

func (p *latexLatexParser) parseScripts(initialMarker string) string {
	panic("unported: latexLatexParser.parseScripts")
}

func (p *latexLatexParser) parseWhitespace() string {
	panic("unported: latexLatexParser.parseWhitespace")
}

func (p *latexLatexParser) parseCommand() string {
	panic("unported: latexLatexParser.parseCommand")
}

func (p *latexLatexParser) parseOperator(operator string, inlineLowerStyle latexInlineLowerStyle, displayLimits bool, spaced bool) string {
	panic("unported: latexLatexParser.parseOperator")
}

// stackFractions defaults to true at TS call sites that omit it.
func (p *latexLatexParser) parseRequiredArgument(stackFractions bool) string {
	panic("unported: latexLatexParser.parseRequiredArgument")
}

func (p *latexLatexParser) parseRequiredArgumentValue() string {
	panic("unported: latexLatexParser.parseRequiredArgumentValue")
}

func (p *latexLatexParser) parseOptionalArgument() *string {
	panic("unported: latexLatexParser.parseOptionalArgument")
}

func (p *latexLatexParser) readRawGroup() *string {
	panic("unported: latexLatexParser.readRawGroup")
}

func (p *latexLatexParser) splitEnvironmentRows(body string) []string {
	panic("unported: latexLatexParser.splitEnvironmentRows")
}

func (p *latexLatexParser) parseEnvironment() string {
	panic("unported: latexLatexParser.parseEnvironment")
}

func (p *latexLatexParser) renderCases(body string) string {
	panic("unported: latexLatexParser.renderCases")
}

func (p *latexLatexParser) renderMatrix(environment string, body string) string {
	panic("unported: latexLatexParser.renderMatrix")
}

// stackFractions defaults to true at TS call sites that omit it.
func (p *latexLatexParser) renderNested(source string, stackFractions bool) string {
	panic("unported: latexLatexParser.renderNested")
}

// RenderLatexOptions configures RenderLatex.
// A nil *RenderLatexOptions means the defaults.
type RenderLatexOptions struct {
	// Display stacks fractions and operator limits vertically for display math (default: false).
	Display *bool `json:"display,omitzero"`
}

// RenderLatex renders a basic LaTeX math expression as terminal-friendly Unicode text.
// It returns nil when the expression contains unsupported or malformed syntax.
func RenderLatex(source string, options *RenderLatexOptions) *string {
	panic("unported: RenderLatex")
}
