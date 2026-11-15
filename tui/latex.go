// Ported from packages/tui/src/latex.ts (pi v1.0.0).

package tui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
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

var (
	latexWhitespacePattern                = jsre.MustCompile(`\s`, "")
	latexScriptEqSpacingPattern           = jsre.MustCompile(`\s*([=+-])\s*`, "g")
	latexSubSimpleIdentPattern            = regexp.MustCompile(`^[A-Za-z]+$`)
	latexSimpleNumIdentPattern            = jsre.MustCompile(`^[\p{L}\p{N}.]+$`, "u")
	latexSimpleDenomPattern               = jsre.MustCompile(`^[\p{N}.]+$`, "u")
	latexMultiSpacePattern                = regexp.MustCompile(`[ \t]+`)
	latexLeadingWsPattern                 = jsre.MustCompile(`^\s`, "")
	latexTrailingWsPattern                = jsre.MustCompile(`\s$`, "")
	latexTrailingCommaWsPattern           = regexp.MustCompile(`,\s*$`)
	latexModifierLimitsPattern            = jsre.MustCompile(`^\\(limits|nolimits)(?![A-Za-z])`, "")
	latexBracedArgPrefixPattern           = jsre.MustCompile(`^\s*\{[^}]*\}`, "")
	latexConditionPrefixPattern           = regexp.MustCompile(`(?i)^(?:if|when|for|otherwise)\b`)
	latexScriptUpperAsteriskPattern       = regexp.MustCompile(`[A-Z*∗]`)
	latexSplitEnvRowsPattern              = regexp.MustCompile(`\\\\(?:\[[^\]\n]*\])?`)
	latexNamedOperatorLeftSpacingPattern  = jsre.MustCompile(`(?<=[\p{L}\p{N})\]}\u{f0001}])\u{f0004}`, "gu")
	latexNamedOperatorRightSpacingPattern = jsre.MustCompile(`\u{f0005}(?=[\p{L}\p{N}√\u{f0000}])`, "gu")
)

func latexReplaceCharacters(value string, replacements map[string]string) *string {
	var b strings.Builder
	for _, r := range value {
		rep, ok := replacements[string(r)]
		if !ok {
			return nil
		}
		b.WriteString(rep)
	}
	res := b.String()
	return &res
}

func latexNormalizeScriptValue(value string) string {
	return latexScriptEqSpacingPattern.Replace(js.Trim(value), "$1")
}

func latexFormatUnicodeScript(value string, kind latexScriptKind) *string {
	norm := latexNormalizeScriptValue(value)
	if kind == latexScriptKindSub {
		return latexReplaceCharacters(norm, latexSubscripts)
	}
	return latexReplaceCharacters(norm, latexSuperscripts)
}

func latexFormatScript(value string, kind latexScriptKind) string {
	value = latexNormalizeScriptValue(value)
	if unicode := latexFormatUnicodeScript(value, kind); unicode != nil {
		return *unicode
	}
	prefix := "^"
	if kind == latexScriptKindSub {
		prefix = "_"
	}
	if utf8.RuneCountInString(value) == 1 || (kind == latexScriptKindSub && latexSubSimpleIdentPattern.MatchString(value)) {
		return prefix + value
	}
	return prefix + "(" + value + ")"
}

func latexFormatFraction(numerator string, denominator string) string {
	numerator = js.Trim(numerator)
	denominator = js.Trim(denominator)
	simpleNum := latexSimpleNumIdentPattern.Test(numerator)
	simpleDenom := latexSimpleDenomPattern.Test(denominator) || utf8.RuneCountInString(denominator) == 1
	numStr := "(" + numerator + ")"
	if simpleNum {
		numStr = numerator
	}
	denomStr := "(" + denominator + ")"
	if simpleDenom {
		denomStr = denominator
	}
	return numStr + "/" + denomStr
}

// latexFormatRoot formats a radical. A nil symbol means "√".
func latexFormatRoot(value string, symbol *string) string {
	sym := "√"
	if symbol != nil {
		sym = *symbol
	}
	value = js.Trim(value)
	if latexSimpleNumIdentPattern.Test(value) {
		return sym + value
	}
	return sym + "(" + value + ")"
}

const (
	latexNamedOperatorStart = "\U000F0004"
	latexNamedOperatorEnd   = "\U000F0005"
)

func latexNormalizeOutput(value string) string {
	s := latexNamedOperatorLeftSpacingPattern.Replace(value, " ")
	s = strings.ReplaceAll(s, latexNamedOperatorStart, "")
	s = latexNamedOperatorRightSpacingPattern.Replace(s, " ")
	s = strings.ReplaceAll(s, latexNamedOperatorEnd, "")
	rawLines := strings.Split(s, "\n")
	mapped := make([]string, len(rawLines))
	for i, line := range rawLines {
		mapped[i] = js.Trim(latexMultiSpacePattern.ReplaceAllString(line, " "))
	}
	filtered := make([]string, 0, len(mapped))
	for i, line := range mapped {
		if len(line) > 0 || (i > 0 && i < len(mapped)-1) {
			filtered = append(filtered, line)
		}
	}
	return js.Trim(strings.Join(filtered, "\n"))
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
	padding := max(0, width-VisibleWidth(line))
	left := 0
	if centered {
		left = padding / 2
	}
	return strings.Repeat(" ", left) + line + strings.Repeat(" ", padding-left)
}

func latexJoinLayouts(layouts []latexLayout) latexLayout {
	if len(layouts) == 0 {
		return latexLayout{Lines: []string{""}, Width: 0, Baseline: 0}
	}
	baseline := 0
	below := 0
	totalWidth := 0
	for _, l := range layouts {
		if l.Baseline > baseline {
			baseline = l.Baseline
		}
		if b := len(l.Lines) - l.Baseline - 1; b > below {
			below = b
		}
		totalWidth += l.Width
	}
	lines := make([]string, 0, baseline+below+1)
	for row := 0; row <= baseline+below; row++ {
		var line strings.Builder
		for _, l := range layouts {
			sourceRow := row - baseline + l.Baseline
			if sourceRow >= 0 && sourceRow < len(l.Lines) {
				line.WriteString(latexPadLayoutLine(l.Lines[sourceRow], l.Width, false))
			} else {
				line.WriteString(strings.Repeat(" ", l.Width))
			}
		}
		lines = append(lines, js.TrimEnd(line.String()))
	}
	return latexLayout{
		Lines:    lines,
		Width:    totalWidth,
		Baseline: baseline,
	}
}

func latexRenderLayout(source string, nodes []latexLayoutNode) latexLayout {
	renderedLines := make([]string, 0)
	firstBaseline := 0
	for _, sourceLine := range strings.Split(source, "\n") {
		layouts := make([]latexLayout, 0)
		position := 0
		var previousNode latexLayoutNode
		for _, match := range latexLayoutMarkerPattern.FindAllStringSubmatchIndex(sourceLine, -1) {
			index := match[0]
			matchEnd := match[1]
			nodeIdx, err := strconv.Atoi(sourceLine[match[2]:match[3]])
			if err != nil || nodeIdx < 0 || nodeIdx >= len(nodes) || nodes[nodeIdx] == nil {
				continue
			}
			node := nodes[nodeIdx]
			if index > position {
				sliced := sourceLine[position:index]
				trimmed := sliced
				if previousNode != nil {
					trimmed = js.TrimStart(trimmed)
				}
				trimmed = js.TrimEnd(trimmed)
				_, prevMatrix := previousNode.(*latexMatrixNode)
				_, currMatrix := node.(*latexMatrixNode)
				preserveLeadingSpace := prevMatrix && latexLeadingWsPattern.Test(sliced)
				preserveTrailingSpace := currMatrix && latexTrailingWsPattern.Test(sliced)
				var text string
				if trimmed != "" {
					if preserveLeadingSpace {
						text = " "
					}
					text += trimmed
					if preserveTrailingSpace {
						text += " "
					}
				} else if preserveLeadingSpace || preserveTrailingSpace {
					text = " "
				}
				layouts = append(layouts, latexLayout{Lines: []string{text}, Width: VisibleWidth(text), Baseline: 0})
			}

			switch n := node.(type) {
			case *latexFractionNode:
				numerator := latexRenderLayout(n.Numerator, nodes)
				denominator := latexRenderLayout(n.Denominator, nodes)
				contentWidth := max(numerator.Width, max(denominator.Width, 1))
				width := contentWidth + 2
				fracLines := make([]string, 0, len(numerator.Lines)+1+len(denominator.Lines))
				for _, line := range numerator.Lines {
					fracLines = append(fracLines, latexPadLayoutLine(line, width, true))
				}
				fracLines = append(fracLines, " "+strings.Repeat("─", contentWidth)+" ")
				for _, line := range denominator.Lines {
					fracLines = append(fracLines, latexPadLayoutLine(line, width, true))
				}
				layouts = append(layouts, latexLayout{
					Lines:    fracLines,
					Width:    width,
					Baseline: len(numerator.Lines),
				})
			case *latexOperatorNode:
				contentWidth := VisibleWidth(n.Operator)
				if n.Lower != nil {
					contentWidth = max(contentWidth, VisibleWidth(*n.Lower))
				}
				if n.Upper != nil {
					contentWidth = max(contentWidth, VisibleWidth(*n.Upper))
				}
				opLines := make([]string, 0, 3)
				baseline := 0
				if n.Upper != nil {
					opLines = append(opLines, latexPadLayoutLine(*n.Upper, contentWidth, true)+" ")
					baseline = 1
				}
				opLines = append(opLines, latexPadLayoutLine(n.Operator, contentWidth, true)+" ")
				if n.Lower != nil {
					opLines = append(opLines, latexPadLayoutLine(*n.Lower, contentWidth, true)+" ")
				}
				layouts = append(layouts, latexLayout{
					Lines:    opLines,
					Width:    contentWidth + 1,
					Baseline: baseline,
				})
			case *latexScriptNode:
				var upper, lower *latexLayout
				width := 0
				if n.Upper != nil {
					u := latexRenderLayout(*n.Upper, nodes)
					upper = &u
					width = max(width, u.Width)
				}
				if n.Lower != nil {
					l := latexRenderLayout(*n.Lower, nodes)
					lower = &l
					width = max(width, l.Width)
				}
				scriptLines := make([]string, 0, 4)
				baseline := 0
				if upper != nil {
					for _, line := range upper.Lines {
						scriptLines = append(scriptLines, latexPadLayoutLine(line, width, false))
					}
					baseline = len(upper.Lines)
				}
				scriptLines = append(scriptLines, strings.Repeat(" ", width))
				if lower != nil {
					for _, line := range lower.Lines {
						scriptLines = append(scriptLines, latexPadLayoutLine(line, width, false))
					}
				}
				layouts = append(layouts, latexLayout{
					Lines:    scriptLines,
					Width:    width,
					Baseline: baseline,
				})
			case *latexMatrixNode:
				width := 0
				for _, line := range n.Lines {
					width = max(width, VisibleWidth(line))
				}
				matLines := make([]string, len(n.Lines))
				for i, line := range n.Lines {
					matLines[i] = latexPadLayoutLine(line, width, false)
				}
				layouts = append(layouts, latexLayout{
					Lines:    matLines,
					Width:    width,
					Baseline: n.Baseline,
				})
			}

			position = matchEnd
			previousNode = node
		}
		if position < len(sourceLine) {
			sliced := sourceLine[position:]
			trimmed := sliced
			if previousNode != nil {
				trimmed = js.TrimStart(trimmed)
			}
			_, prevMatrix := previousNode.(*latexMatrixNode)
			text := trimmed
			if prevMatrix && latexLeadingWsPattern.Test(sliced) {
				text = " " + trimmed
			}
			layouts = append(layouts, latexLayout{Lines: []string{text}, Width: VisibleWidth(text), Baseline: 0})
		}
		lineLayout := latexJoinLayouts(layouts)
		if len(renderedLines) == 0 {
			firstBaseline = lineLayout.Baseline
		}
		renderedLines = append(renderedLines, lineLayout.Lines...)
	}
	maxWidth := 0
	for _, line := range renderedLines {
		maxWidth = max(maxWidth, VisibleWidth(line))
	}
	return latexLayout{
		Lines:    renderedLines,
		Width:    maxWidth,
		Baseline: firstBaseline,
	}
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
	rendered := p.parseSequence(nil)
	if !p.supported || p.position != len(p.source) {
		return nil
	}
	out := latexNormalizeOutput(rendered)
	return &out
}

func (p *latexLatexParser) peekRune() (rune, int) {
	return utf8.DecodeRuneInString(p.source[p.position:])
}

// endCharacter nil means parse until the source is consumed.
func (p *latexLatexParser) parseSequence(endCharacter *string) string {
	var result strings.Builder
	for p.position < len(p.source) {
		r, size := p.peekRune()
		ch := string(r)
		if endCharacter != nil && ch == *endCharacter {
			p.position += size
			return result.String()
		}
		if ch == "}" {
			p.supported = false
			return result.String()
		}
		if ch == "{" {
			p.position += size
			closeBrace := "}"
			result.WriteString(p.parseSequence(&closeBrace))
			continue
		}
		if ch == "\\" {
			command := p.parseCommand()
			if command == latexNegativeSpace {
				s := js.TrimEnd(result.String())
				s = strings.TrimSuffix(s, latexNamedOperatorEnd)
				result.Reset()
				result.WriteString(s)
			} else {
				result.WriteString(command)
			}
			continue
		}
		if ch == "^" || ch == "_" {
			p.position += size
			s := js.TrimEnd(result.String())
			result.Reset()
			script := p.parseScripts(ch)
			if strings.HasSuffix(s, latexNamedOperatorEnd) {
				s = strings.TrimSuffix(s, latexNamedOperatorEnd) + script + latexNamedOperatorEnd
			} else {
				s += script
			}
			result.WriteString(s)
			continue
		}
		if latexWhitespacePattern.Test(ch) {
			result.WriteString(p.parseWhitespace())
			continue
		}
		if ch == "=" || ch == "<" || ch == ">" {
			s := js.TrimEnd(result.String()) + " " + ch + " "
			result.Reset()
			result.WriteString(s)
			p.position += size
			continue
		}
		if ch == "&" {
			p.position += size
			continue
		}
		if ch == "~" {
			p.position += size
			result.WriteByte(' ')
			continue
		}
		if ch == "." {
			s := result.String()
			if marker := latexTrailingLayoutMarkerPattern.FindStringSubmatch(s); marker != nil {
				if idx, err := strconv.Atoi(marker[1]); err == nil && idx >= 0 && idx < len(*p.layoutNodes) {
					if mat, ok := (*p.layoutNodes)[idx].(*latexMatrixNode); ok && len(mat.Lines) > 0 {
						lastIdx := len(mat.Lines) - 1
						mat.Lines[lastIdx] += ch
						p.position += size
						continue
					}
				}
			}
		}
		result.WriteString(ch)
		p.position += size
	}
	if endCharacter != nil {
		p.supported = false
	}
	return result.String()
}

func (p *latexLatexParser) parseScripts(initialMarker string) string {
	var subVal, supVal *string
	order := make([]latexScriptKind, 0, 2)
	parse := func(marker string) {
		kind := latexScriptKindSup
		if marker == "_" {
			kind = latexScriptKindSub
		}
		p.scriptDepth++
		val := p.parseRequiredArgument(false)
		p.scriptDepth--
		if kind == latexScriptKindSub {
			subVal = &val
		} else {
			supVal = &val
		}
		order = append(order, kind)
	}

	parse(initialMarker)
	nextPosition := p.position
	for nextPosition < len(p.source) {
		r, size := utf8.DecodeRuneInString(p.source[nextPosition:])
		if !latexWhitespacePattern.Test(string(r)) {
			break
		}
		nextPosition += size
	}
	if nextPosition < len(p.source) {
		r, size := utf8.DecodeRuneInString(p.source[nextPosition:])
		nextMarker := string(r)
		if (nextMarker == "^" || nextMarker == "_") && nextMarker != initialMarker {
			p.position = nextPosition + size
			parse(nextMarker)
		}
	}

	var subUnicode, supUnicode *string
	if subVal != nil {
		subUnicode = latexFormatUnicodeScript(*subVal, latexScriptKindSub)
	}
	if supVal != nil {
		supUnicode = latexFormatUnicodeScript(*supVal, latexScriptKindSup)
	}

	canUseLayout := true
	for _, val := range []*string{subVal, supVal} {
		if val == nil {
			continue
		}
		v := *val
		if strings.Contains(v, "/") ||
			(!strings.Contains(v, latexLayoutMarkerStart) &&
				utf8.RuneCountInString(v) > 1 &&
				!latexScriptUpperAsteriskPattern.MatchString(v)) {
			canUseLayout = false
			break
		}
	}

	needsLayout := p.display && canUseLayout &&
		(p.scriptDepth > 0 || (subVal != nil && subUnicode == nil) || (supVal != nil && supUnicode == nil))
	if !needsLayout {
		var out strings.Builder
		for _, kind := range order {
			if kind == latexScriptKindSub {
				if subUnicode != nil {
					out.WriteString(*subUnicode)
				} else if subVal != nil {
					out.WriteString(latexFormatScript(*subVal, kind))
				}
			} else {
				if supUnicode != nil {
					out.WriteString(*supUnicode)
				} else if supVal != nil {
					out.WriteString(latexFormatScript(*supVal, kind))
				}
			}
		}
		return out.String()
	}

	var lower, upper *string
	if subVal != nil {
		n := latexNormalizeOutput(*subVal)
		lower = &n
	}
	if supVal != nil {
		n := latexNormalizeOutput(*supVal)
		upper = &n
	}
	*p.layoutNodes = append(*p.layoutNodes, &latexScriptNode{
		Type:  "script",
		Lower: lower,
		Upper: upper,
	})
	idx := len(*p.layoutNodes) - 1
	return fmt.Sprintf("%s%d%s", latexLayoutMarkerStart, idx, latexLayoutMarkerEnd)
}

func (p *latexLatexParser) parseWhitespace() string {
	for p.position < len(p.source) {
		r, size := p.peekRune()
		if !latexWhitespacePattern.Test(string(r)) {
			break
		}
		p.position += size
	}
	return " "
}

func (p *latexLatexParser) parseCommand() string {
	p.position++
	if p.position >= len(p.source) {
		p.supported = false
		return ""
	}

	r, size := p.peekRune()
	if r == '\n' || r == '\r' {
		p.position += size
		if r == '\r' && p.position < len(p.source) && p.source[p.position] == '\n' {
			p.position++
		}
		return " "
	}

	var command string
	if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
		start := p.position
		for p.position < len(p.source) {
			c := p.source[p.position]
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
				break
			}
			p.position++
		}
		command = p.source[start:p.position]
	} else {
		command = string(r)
		p.position += size
	}

	if command == `\` {
		return "\n"
	}
	if _, ok := latexSpacingCommands[command]; ok {
		return " "
	}
	if _, ok := latexNegativeSpacingCommands[command]; ok {
		return latexNegativeSpace
	}
	if _, ok := latexFontSwitchCommands[command]; ok {
		p.parseWhitespace()
		return ""
	}
	if _, ok := latexIgnoredCommands[command]; ok {
		return ""
	}
	switch command {
	case "{", "}", "$", "%", "#", "_", "&":
		return command
	case "|":
		return "‖"
	case "not":
		value := js.Trim(p.parseRequiredArgument(false))
		if negated, ok := latexNegatedSymbols[value]; ok {
			return " " + negated + " "
		}
		runes := []rune(value)
		if len(runes) == 0 {
			p.supported = false
			return ""
		}
		return " " + string(runes[0]) + "\u0338" + string(runes[1:]) + " "
	}
	if _, ok := latexLimitOperators[command]; ok {
		return p.parseOperator(command, latexInlineLowerStyleBracket, true, true)
	}
	if sym, ok := latexSymbols[command]; ok {
		if _, isDisplayLimit := latexDisplayLimitSymbols[command]; isDisplayLimit {
			return p.parseOperator(sym, latexInlineLowerStyleScript, true, false)
		}
		if _, isRel := latexRelationCommands[command]; isRel || command == "cdot" || command == "times" {
			return " " + sym + " "
		}
		return sym
	}
	if _, ok := latexNamedOperators[command]; ok {
		return latexNamedOperatorStart + command + latexNamedOperatorEnd
	}
	if _, ok := latexSizeCommands[command]; ok {
		return ""
	}
	if command == "left" || command == "middle" || command == "right" {
		if p.position < len(p.source) && p.source[p.position] == '.' {
			p.position++
		}
		return ""
	}
	if command == "frac" || command == "dfrac" || command == "tfrac" {
		shouldStack := p.display && p.stackFractions && command != "tfrac"
		numerator := p.parseRequiredArgument(!shouldStack)
		denominator := p.parseRequiredArgument(!shouldStack)
		if shouldStack {
			*p.layoutNodes = append(*p.layoutNodes, &latexFractionNode{
				Type:        "fraction",
				Numerator:   latexNormalizeOutput(numerator),
				Denominator: latexNormalizeOutput(denominator),
			})
			idx := len(*p.layoutNodes) - 1
			return fmt.Sprintf("%s%d%s", latexLayoutMarkerStart, idx, latexLayoutMarkerEnd)
		}
		return latexFormatFraction(numerator, denominator)
	}
	if command == "sqrt" {
		var degree *string
		if opt := p.parseOptionalArgument(); opt != nil {
			trimmed := js.Trim(*opt)
			degree = &trimmed
		}
		value := p.parseRequiredArgument(true)
		if degree == nil || *degree == "2" {
			return latexFormatRoot(value, nil)
		}
		if *degree == "3" {
			s := "∛"
			return latexFormatRoot(value, &s)
		}
		if *degree == "4" {
			s := "∜"
			return latexFormatRoot(value, &s)
		}
		return latexFormatScript(*degree, latexScriptKindSup) + latexFormatRoot(value, nil)
	}
	if command == "boxed" || command == "fbox" {
		return "[" + js.Trim(p.parseRequiredArgument(true)) + "]"
	}
	if command == "binom" || command == "dbinom" || command == "tbinom" {
		return "(" + p.parseRequiredArgument(true) + " choose " + p.parseRequiredArgument(true) + ")"
	}
	if accent, ok := latexAccents[command]; ok {
		value := p.parseRequiredArgument(true)
		if utf8.RuneCountInString(value) == 1 {
			return value + accent
		}
		return command + "(" + value + ")"
	}
	if command == "mathbb" {
		value := p.parseRequiredArgument(true)
		var b strings.Builder
		for _, ch := range value {
			if mapped, ok := latexBlackboard[string(ch)]; ok {
				b.WriteString(mapped)
			} else {
				b.WriteRune(ch)
			}
		}
		return b.String()
	}
	if command == "operatorname" {
		starred := p.position < len(p.source) && p.source[p.position] == '*'
		if starred {
			p.position++
		}
		operator := js.Trim(latexNormalizeOutput(p.parseRequiredArgument(true)))
		return p.parseOperator(operator, latexInlineLowerStyleBracket, starred, true)
	}
	if command == "mod" || command == "bmod" {
		return " mod "
	}
	if command == "pmod" || command == "pod" {
		value := js.Trim(p.parseRequiredArgument(true))
		if command == "pmod" {
			return " (mod " + value + ")"
		}
		return " (" + value + ")"
	}
	if command == "overset" || command == "stackrel" {
		upper := p.parseRequiredArgument(true)
		value := js.Trim(p.parseRequiredArgument(true))
		return value + latexFormatScript(upper, latexScriptKindSup)
	}
	if command == "underset" {
		lower := p.parseRequiredArgument(true)
		value := js.Trim(p.parseRequiredArgument(true))
		return value + latexFormatScript(lower, latexScriptKindSub)
	}
	if _, ok := latexPlainWrappers[command]; ok {
		value := p.parseRequiredArgument(true)
		if strings.HasPrefix(command, "text") || command == "mbox" {
			return value
		}
		return js.Trim(value)
	}
	if command == "begin" {
		return p.parseEnvironment()
	}
	if command == "end" {
		p.supported = false
		return ""
	}

	p.supported = false
	return `\` + command
}

func (p *latexLatexParser) parseOperator(operator string, inlineLowerStyle latexInlineLowerStyle, displayLimits bool, spaced bool) string {
	useDisplayLimits := displayLimits
	modifierPosition := p.position
	for modifierPosition < len(p.source) && (p.source[modifierPosition] == ' ' || p.source[modifierPosition] == '\t') {
		modifierPosition++
	}
	if modifier := latexModifierLimitsPattern.Exec(p.source[modifierPosition:], 0); modifier != nil {
		useDisplayLimits = modifier.Group(1) == "limits"
		p.position = modifierPosition + len(modifier.String())
	}

	var lower, upper *string
	for {
		scriptPosition := p.position
		for scriptPosition < len(p.source) && (p.source[scriptPosition] == ' ' || p.source[scriptPosition] == '\t') {
			scriptPosition++
		}
		if scriptPosition >= len(p.source) {
			break
		}
		kind := p.source[scriptPosition]
		if kind != '_' && kind != '^' {
			break
		}
		p.position = scriptPosition + 1
		value := strings.ReplaceAll(latexNormalizeOutput(p.parseRequiredArgument(false)), " ", "")
		if kind == '_' {
			if lower != nil {
				p.supported = false
			}
			v := value
			lower = &v
		} else {
			if upper != nil {
				p.supported = false
			}
			v := value
			upper = &v
		}
	}

	if p.display && useDisplayLimits && (lower != nil || upper != nil) {
		*p.layoutNodes = append(*p.layoutNodes, &latexOperatorNode{
			Type:     "operator",
			Operator: operator,
			Lower:    lower,
			Upper:    upper,
		})
		idx := len(*p.layoutNodes) - 1
		return fmt.Sprintf("%s%d%s", latexLayoutMarkerStart, idx, latexLayoutMarkerEnd)
	}

	rendered := operator
	if lower != nil {
		if inlineLowerStyle == latexInlineLowerStyleBracket {
			rendered += "[" + *lower + "]"
		} else {
			rendered += latexFormatScript(*lower, latexScriptKindSub)
		}
	}
	if upper != nil {
		rendered += latexFormatScript(*upper, latexScriptKindSup)
	}
	if spaced {
		return " " + rendered + " "
	}
	return rendered
}

// stackFractions defaults to true at TS call sites that omit it.
func (p *latexLatexParser) parseRequiredArgument(stackFractions bool) string {
	previousStackFractions := p.stackFractions
	p.stackFractions = previousStackFractions && stackFractions
	value := p.parseRequiredArgumentValue()
	p.stackFractions = previousStackFractions
	return value
}

func (p *latexLatexParser) parseRequiredArgumentValue() string {
	p.parseWhitespace()
	if p.position >= len(p.source) {
		p.supported = false
		return ""
	}
	r, size := p.peekRune()
	if r == '{' {
		p.position += size
		closeBrace := "}"
		return p.parseSequence(&closeBrace)
	}
	if r == '\\' {
		return p.parseCommand()
	}
	p.position += size
	return string(r)
}

func (p *latexLatexParser) parseOptionalArgument() *string {
	for p.position < len(p.source) && (p.source[p.position] == ' ' || p.source[p.position] == '\t') {
		p.position++
	}
	if p.position >= len(p.source) || p.source[p.position] != '[' {
		return nil
	}
	endRel := strings.IndexByte(p.source[p.position+1:], ']')
	if endRel < 0 {
		p.supported = false
		return nil
	}
	end := p.position + 1 + endRel
	value := p.source[p.position+1 : end]
	p.position = end + 1
	res := p.renderNested(value, true)
	return &res
}

func (p *latexLatexParser) readRawGroup() *string {
	for p.position < len(p.source) && (p.source[p.position] == ' ' || p.source[p.position] == '\t') {
		p.position++
	}
	if p.position >= len(p.source) || p.source[p.position] != '{' {
		p.supported = false
		return nil
	}
	p.position++
	start := p.position
	depth := 1
	for p.position < len(p.source) {
		r, size := p.peekRune()
		if r == '\\' {
			p.position += size
			if p.position < len(p.source) {
				_, nextSize := p.peekRune()
				p.position += nextSize
			}
			continue
		}
		if r == '{' {
			depth++
		}
		if r == '}' {
			depth--
		}
		if depth == 0 {
			value := p.source[start:p.position]
			p.position += size
			return &value
		}
		p.position += size
	}
	p.supported = false
	return nil
}

func (p *latexLatexParser) splitEnvironmentRows(body string) []string {
	return latexSplitEnvRowsPattern.Split(body, -1)
}

func (p *latexLatexParser) parseEnvironment() string {
	envPtr := p.readRawGroup()
	if envPtr == nil || *envPtr == "" {
		return ""
	}
	environment := *envPtr
	endMarker := `\end{` + environment + `}`
	endRel := strings.Index(p.source[p.position:], endMarker)
	if endRel < 0 {
		p.supported = false
		return ""
	}
	end := p.position + endRel
	body := p.source[p.position:end]
	p.position = end + len(endMarker)

	switch environment {
	case "equation", "equation*", "displaymath":
		return js.Trim(p.renderNested(body, true))
	case "aligned", "align", "align*", "alignedat", "alignat", "alignat*", "gather", "gathered", "multline", "multline*", "split":
		alignedAt := environment == "alignedat" || environment == "alignat" || environment == "alignat*"
		alignedBody := body
		if alignedAt {
			alignedBody = latexBracedArgPrefixPattern.Replace(body, "")
		}
		rows := p.splitEnvironmentRows(alignedBody)
		renderedRows := make([]string, 0, len(rows))
		for _, row := range rows {
			cells := strings.Split(row, "&")
			var src string
			if alignedAt {
				pairs := make([]string, 0, (len(cells)+1)/2)
				for i := 0; i < len(cells); i += 2 {
					second := ""
					if i+1 < len(cells) {
						second = cells[i+1]
					}
					pairs = append(pairs, cells[i]+second)
				}
				src = strings.Join(pairs, " ")
			} else {
				src = strings.Join(cells, "")
			}
			if rendered := js.Trim(p.renderNested(src, true)); rendered != "" {
				renderedRows = append(renderedRows, rendered)
			}
		}
		return strings.Join(renderedRows, "\n")
	case "cases", "cases*":
		return p.renderCases(body)
	case "array", "matrix", "smallmatrix", "pmatrix", "bmatrix", "Bmatrix", "vmatrix", "Vmatrix":
		matrixBody := body
		if environment == "array" {
			matrixBody = latexBracedArgPrefixPattern.Replace(body, "")
		}
		return p.renderMatrix(environment, matrixBody)
	default:
		p.supported = false
		return body
	}
}

func (p *latexLatexParser) renderCases(body string) string {
	rawRows := p.splitEnvironmentRows(body)
	rows := make([][]string, 0, len(rawRows))
	for _, rawRow := range rawRows {
		parts := strings.Split(rawRow, "&")
		cells := make([]string, len(parts))
		anyNonEmpty := false
		for i, cell := range parts {
			cells[i] = js.Trim(p.renderNested(cell, false))
			if cells[i] != "" {
				anyNonEmpty = true
			}
		}
		if anyNonEmpty {
			rows = append(rows, cells)
		}
	}

	valueWidth := 0
	for _, row := range rows {
		val := ""
		if len(row) > 0 {
			val = latexTrailingCommaWsPattern.ReplaceAllString(row[0], "")
		}
		if w := VisibleWidth(val); w > valueWidth {
			valueWidth = w
		}
	}

	contents := make([]string, len(rows))
	for i, row := range rows {
		val := ""
		if len(row) > 0 {
			val = latexTrailingCommaWsPattern.ReplaceAllString(row[0], "")
		}
		condition := ""
		if len(row) > 1 {
			condition = row[1]
		}
		if condition == "" {
			contents[i] = val
			continue
		}
		conditionPrefix := " if "
		if latexConditionPrefixPattern.MatchString(condition) {
			conditionPrefix = " "
		}
		padCount := max(0, valueWidth-VisibleWidth(val))
		contents[i] = val + strings.Repeat(latexProtectedSpace, padCount) + conditionPrefix + condition
	}

	if len(contents) <= 1 {
		if len(contents) == 0 {
			return ""
		}
		return "⎧ " + contents[0]
	}

	middle := len(contents) / 2
	visualRows := make([]*string, 0, len(contents)+1)
	for i := range contents {
		c := contents[i]
		visualRows = append(visualRows, &c)
	}
	if len(contents)%2 == 0 {
		visualRows = append(visualRows, nil)
		copy(visualRows[middle+1:], visualRows[middle:])
		visualRows[middle] = nil
	}

	lines := make([]string, len(visualRows))
	for i, content := range visualRows {
		delimiter := "⎨"
		if i == 0 {
			delimiter = "⎧"
		} else if i == len(visualRows)-1 {
			delimiter = "⎩"
		}
		if content == nil {
			lines[i] = delimiter
		} else {
			lines[i] = delimiter + " " + *content
		}
	}
	*p.layoutNodes = append(*p.layoutNodes, &latexMatrixNode{
		Type:     "matrix",
		Lines:    lines,
		Baseline: middle,
	})
	idx := len(*p.layoutNodes) - 1
	return fmt.Sprintf("%s%d%s", latexLayoutMarkerStart, idx, latexLayoutMarkerEnd)
}

func (p *latexLatexParser) renderMatrix(environment string, body string) string {
	rawRows := p.splitEnvironmentRows(body)
	matrix := make([][]string, 0, len(rawRows))
	colCount := 0
	for _, rawRow := range rawRows {
		parts := strings.Split(rawRow, "&")
		row := make([]string, len(parts))
		anyNonEmpty := false
		for i, cell := range parts {
			row[i] = js.Trim(p.renderNested(cell, false))
			if row[i] != "" {
				anyNonEmpty = true
			}
		}
		if anyNonEmpty {
			matrix = append(matrix, row)
			if len(row) > colCount {
				colCount = len(row)
			}
		}
	}

	colWidths := make([]int, colCount)
	for col := 0; col < colCount; col++ {
		maxW := 0
		for _, row := range matrix {
			cell := ""
			if col < len(row) {
				cell = row[col]
			}
			if w := VisibleWidth(cell); w > maxW {
				maxW = w
			}
		}
		colWidths[col] = maxW
	}

	rows := make([]string, len(matrix))
	for r, row := range matrix {
		cells := make([]string, colCount)
		for c := 0; c < colCount; c++ {
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			padCount := max(0, colWidths[c]-VisibleWidth(cell))
			cells[c] = cell + strings.Repeat(latexProtectedSpace, padCount)
		}
		rows[r] = strings.Join(cells, " │ ")
	}

	var lines []string
	if environment == "array" || environment == "matrix" || environment == "smallmatrix" {
		lines = rows
	} else {
		delimiters := map[string][6]string{
			"pmatrix": {"⎛", "⎞", "⎜", "⎟", "⎝", "⎠"},
			"bmatrix": {"⎡", "⎤", "⎢", "⎥", "⎣", "⎦"},
			"Bmatrix": {"⎧", "⎫", "⎨", "⎬", "⎩", "⎭"},
			"vmatrix": {"│", "│", "│", "│", "│", "│"},
			"Vmatrix": {"║", "║", "║", "║", "║", "║"},
		}
		delim, ok := delimiters[environment]
		if !ok {
			p.supported = false
			return strings.Join(rows, "\n")
		}
		lines = make([]string, len(rows))
		for i, row := range rows {
			left := delim[2]
			right := delim[3]
			if i == 0 {
				left = delim[0]
				right = delim[1]
			}
			if i == len(rows)-1 {
				left = delim[4]
				right = delim[5]
			}
			lines[i] = left + " " + row + " " + right
		}
	}

	if len(lines) <= 1 {
		if len(lines) == 0 {
			return ""
		}
		return lines[0]
	}
	*p.layoutNodes = append(*p.layoutNodes, &latexMatrixNode{
		Type:     "matrix",
		Lines:    lines,
		Baseline: 0,
	})
	idx := len(*p.layoutNodes) - 1
	return fmt.Sprintf("%s%d%s", latexLayoutMarkerStart, idx, latexLayoutMarkerEnd)
}

// stackFractions defaults to true at TS call sites that omit it.
func (p *latexLatexParser) renderNested(source string, stackFractions bool) string {
	rendered := latexNewLatexParser(source, p.layoutNodes, p.display && stackFractions).render()
	if rendered == nil {
		p.supported = false
		return source
	}
	return *rendered
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
	display := options != nil && options.Display != nil && *options.Display
	layoutNodes := make([]latexLayoutNode, 0)
	rendered := latexNewLatexParser(source, &layoutNodes, display).render()
	if rendered == nil {
		return nil
	}
	if len(layoutNodes) == 0 {
		out := strings.ReplaceAll(*rendered, latexProtectedSpace, " ")
		return &out
	}
	lines := latexRenderLayout(*rendered, layoutNodes).Lines
	minIndent := math.MaxInt
	hasNonEmpty := false
	for _, line := range lines {
		if js.Trim(line) == "" {
			continue
		}
		hasNonEmpty = true
		indent := js.Len(line) - js.Len(js.TrimStart(line))
		if indent < minIndent {
			minIndent = indent
		}
	}
	trimmedLines := make([]string, len(lines))
	for i, line := range lines {
		if !hasNonEmpty {
			trimmedLines[i] = ""
		} else {
			trimmedLines[i] = js.TrimEnd(js.Slice(line, minIndent, js.Len(line)))
		}
	}
	out := strings.ReplaceAll(js.TrimEnd(strings.Join(trimmedLines, "\n")), latexProtectedSpace, " ")
	return &out
}
