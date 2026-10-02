package marked

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
)

// rules.json is Lexer.rules plus the shared helpers from marked 18.0.11.
// Regenerate with node testdata/genrules.mjs.
//
//go:embed rules.json
var rulesJSON []byte

type pattern struct {
	Source string `json:"source"`
	Flags  string `json:"flags"`
	Noop   bool   `json:"noop"`
}

type rulesDoc struct {
	Block  map[string]map[string]pattern `json:"block"`
	Inline map[string]map[string]pattern `json:"inline"`
	Other  map[string]pattern            `json:"other"`
}

type otherRules struct {
	codeRemoveIndent         *jsRE
	outputLinkReplace        *jsRE
	indentCodeCompensation   *jsRE
	beginningSpace           *jsRE
	endingHash               *jsRE
	startingSpaceChar        *jsRE
	endingSpaceChar          *jsRE
	nonSpaceChar             *jsRE
	newLineCharGlobal        *jsRE
	tabCharGlobal            *jsRE
	multipleSpaceGlobal      *jsRE
	blankLine                *jsRE
	doubleBlankLine          *jsRE
	blockquoteStart          *jsRE
	blockquoteSetextReplace  *jsRE
	blockquoteSetextReplace2 *jsRE
	listReplaceNesting       *jsRE
	listIsTask               *jsRE
	listReplaceTask          *jsRE
	listTaskCheckbox         *jsRE
	anyLine                  *jsRE
	hrefBrackets             *jsRE
	tableDelimiter           *jsRE
	tableAlignChars          *jsRE
	tableRowBlankLine        *jsRE
	tableAlignRight          *jsRE
	tableAlignCenter         *jsRE
	tableAlignLeft           *jsRE
	startATag                *jsRE
	endATag                  *jsRE
	startPreScriptTag        *jsRE
	endPreScriptTag          *jsRE
	startAngleBracket        *jsRE
	endAngleBracket          *jsRE
	pedanticHrefTitle        *jsRE
	unicodeAlphaNumeric      *jsRE
	findPipe                 *jsRE
	slashPipe                *jsRE
	carriageReturn           *jsRE
	spaceLine                *jsRE
}

type blockRules struct {
	blockquote *jsRE
	code       *jsRE
	def        *jsRE
	fences     *jsRE
	heading    *jsRE
	hr         *jsRE
	html       *jsRE
	lheading   *jsRE
	list       *jsRE
	newline    *jsRE
	paragraph  *jsRE
	table      *jsRE
	text       *jsRE
}

type inlineRules struct {
	backpedal         *jsRE
	anyPunctuation    *jsRE
	autolink          *jsRE
	blockSkip         *jsRE
	br                *jsRE
	code              *jsRE
	del               *jsRE
	delLDelim         *jsRE
	delRDelim         *jsRE
	emStrongLDelim    *jsRE
	emStrongRDelimAst *jsRE
	emStrongRDelimUnd *jsRE
	escape            *jsRE
	link              *jsRE
	nolink            *jsRE
	punctuation       *jsRE
	reflink           *jsRE
	reflinkSearch     *jsRE
	tag               *jsRE
	text              *jsRE
	url               *jsRE
}

type ruleSet struct {
	other  *otherRules
	block  *blockRules
	inline *inlineRules
}

var (
	rulesOther    *otherRules
	rulesBlock    map[string]*blockRules
	rulesInline   map[string]*inlineRules
	rulesNormal   *ruleSet
	rulesGFM      *ruleSet
	rulesBreaks   *ruleSet
	rulesPedantic *ruleSet
)

func init() {
	var doc rulesDoc
	if err := json.Unmarshal(rulesJSON, &doc); err != nil {
		panic(err)
	}
	rulesOther = loadOther(doc.Other)
	rulesBlock = map[string]*blockRules{
		"normal":   loadBlock("block.normal", doc.Block["normal"]),
		"gfm":      loadBlock("block.gfm", doc.Block["gfm"]),
		"pedantic": loadBlock("block.pedantic", doc.Block["pedantic"]),
	}
	rulesInline = map[string]*inlineRules{
		"normal":   loadInline("inline.normal", doc.Inline["normal"]),
		"gfm":      loadInline("inline.gfm", doc.Inline["gfm"]),
		"breaks":   loadInline("inline.breaks", doc.Inline["breaks"]),
		"pedantic": loadInline("inline.pedantic", doc.Inline["pedantic"]),
	}
	rulesNormal = &ruleSet{other: rulesOther, block: rulesBlock["normal"], inline: rulesInline["normal"]}
	rulesGFM = &ruleSet{other: rulesOther, block: rulesBlock["gfm"], inline: rulesInline["gfm"]}
	rulesBreaks = &ruleSet{other: rulesOther, block: rulesBlock["gfm"], inline: rulesInline["breaks"]}
	rulesPedantic = &ruleSet{other: rulesOther, block: rulesBlock["pedantic"], inline: rulesInline["pedantic"]}
}

func mustRule(name string, p pattern) *jsRE {
	if p.Noop {
		return &jsRE{noop: true}
	}
	if p.Source == "" {
		panic("marked: missing rule " + name)
	}
	re, err := compileRE(p.Source, p.Flags)
	if err != nil {
		panic(fmt.Errorf("marked: rule %s: %w", name, err))
	}
	return re
}

func loadOther(m map[string]pattern) *otherRules {
	g := func(name string) *jsRE { return mustRule("other."+name, m[name]) }
	return &otherRules{
		codeRemoveIndent:         g("codeRemoveIndent"),
		outputLinkReplace:        g("outputLinkReplace"),
		indentCodeCompensation:   g("indentCodeCompensation"),
		beginningSpace:           g("beginningSpace"),
		endingHash:               g("endingHash"),
		startingSpaceChar:        g("startingSpaceChar"),
		endingSpaceChar:          g("endingSpaceChar"),
		nonSpaceChar:             g("nonSpaceChar"),
		newLineCharGlobal:        g("newLineCharGlobal"),
		tabCharGlobal:            g("tabCharGlobal"),
		multipleSpaceGlobal:      g("multipleSpaceGlobal"),
		blankLine:                g("blankLine"),
		doubleBlankLine:          g("doubleBlankLine"),
		blockquoteStart:          g("blockquoteStart"),
		blockquoteSetextReplace:  g("blockquoteSetextReplace"),
		blockquoteSetextReplace2: g("blockquoteSetextReplace2"),
		listReplaceNesting:       g("listReplaceNesting"),
		listIsTask:               g("listIsTask"),
		listReplaceTask:          g("listReplaceTask"),
		listTaskCheckbox:         g("listTaskCheckbox"),
		anyLine:                  g("anyLine"),
		hrefBrackets:             g("hrefBrackets"),
		tableDelimiter:           g("tableDelimiter"),
		tableAlignChars:          g("tableAlignChars"),
		tableRowBlankLine:        g("tableRowBlankLine"),
		tableAlignRight:          g("tableAlignRight"),
		tableAlignCenter:         g("tableAlignCenter"),
		tableAlignLeft:           g("tableAlignLeft"),
		startATag:                g("startATag"),
		endATag:                  g("endATag"),
		startPreScriptTag:        g("startPreScriptTag"),
		endPreScriptTag:          g("endPreScriptTag"),
		startAngleBracket:        g("startAngleBracket"),
		endAngleBracket:          g("endAngleBracket"),
		pedanticHrefTitle:        g("pedanticHrefTitle"),
		unicodeAlphaNumeric:      g("unicodeAlphaNumeric"),
		findPipe:                 g("findPipe"),
		slashPipe:                g("slashPipe"),
		carriageReturn:           g("carriageReturn"),
		spaceLine:                g("spaceLine"),
	}
}

func loadBlock(prefix string, m map[string]pattern) *blockRules {
	g := func(name string) *jsRE { return mustRule(prefix+"."+name, m[name]) }
	return &blockRules{
		blockquote: g("blockquote"),
		code:       g("code"),
		def:        g("def"),
		fences:     g("fences"),
		heading:    g("heading"),
		hr:         g("hr"),
		html:       g("html"),
		lheading:   g("lheading"),
		list:       g("list"),
		newline:    g("newline"),
		paragraph:  g("paragraph"),
		table:      g("table"),
		text:       g("text"),
	}
}

func loadInline(prefix string, m map[string]pattern) *inlineRules {
	g := func(name string) *jsRE { return mustRule(prefix+"."+name, m[name]) }
	return &inlineRules{
		backpedal:         g("_backpedal"),
		anyPunctuation:    g("anyPunctuation"),
		autolink:          g("autolink"),
		blockSkip:         g("blockSkip"),
		br:                g("br"),
		code:              g("code"),
		del:               g("del"),
		delLDelim:         g("delLDelim"),
		delRDelim:         g("delRDelim"),
		emStrongLDelim:    g("emStrongLDelim"),
		emStrongRDelimAst: g("emStrongRDelimAst"),
		emStrongRDelimUnd: g("emStrongRDelimUnd"),
		escape:            g("escape"),
		link:              g("link"),
		nolink:            g("nolink"),
		punctuation:       g("punctuation"),
		reflink:           g("reflink"),
		reflinkSearch:     g("reflinkSearch"),
		tag:               g("tag"),
		text:              g("text"),
		url:               g("url"),
	}
}

func (o *otherRules) listItemRegex(bull string) *jsRE {
	return compileCached(`^( {0,3}`+bull+`)((?:[\t ][^\n]*)?(?:\n|$))`, "")
}

func indentIndex(indent int) int {
	idx := indent - 1
	if idx < 0 {
		idx = 0
	}
	if idx > 3 {
		idx = 3
	}
	return idx
}

func (o *otherRules) nextBulletRegex(indent int) *jsRE {
	n := strconv.Itoa(indentIndex(indent))
	return compileCached(`^ {0,`+n+`}(?:[*+-]|\d{1,9}[.)])((?:[ \t][^\n]*)?(?:\n|$))`, "")
}

func (o *otherRules) hrRegex(indent int) *jsRE {
	n := strconv.Itoa(indentIndex(indent))
	return compileCached(`^ {0,`+n+`}((?:- *){3,}|(?:_ *){3,}|(?:\* *){3,})(?:\n+|$)`, "")
}

func (o *otherRules) fencesBeginRegex(indent int) *jsRE {
	n := strconv.Itoa(indentIndex(indent))
	return compileCached(`^ {0,`+n+"}(?:```|~~~)", "")
}

func (o *otherRules) headingBeginRegex(indent int) *jsRE {
	n := strconv.Itoa(indentIndex(indent))
	return compileCached(`^ {0,`+n+`}#`, "")
}

func (o *otherRules) htmlBeginRegex(indent int) *jsRE {
	n := strconv.Itoa(indentIndex(indent))
	return compileCached(`^ {0,`+n+`}<(?:[a-z].*>|!--)`, "i")
}

func (o *otherRules) blockquoteBeginRegex(indent int) *jsRE {
	n := strconv.Itoa(indentIndex(indent))
	return compileCached(`^ {0,`+n+`}>`, "")
}

func pickRules(pedantic, gfm, breaks bool) *ruleSet {
	if pedantic {
		return rulesPedantic
	}
	if gfm {
		if breaks {
			return rulesBreaks
		}
		return rulesGFM
	}
	return rulesNormal
}
