package marked

import "strings"

// Tokenizer turns Markdown source into tokens. Nil func fields use marked's
// built-in tokenizers. A non-nil field replaces that tokenizer completely:
// a nil *Token means "not this construct" and does not fall back (the same as
// a Tokenizer subclass returning undefined). Return values are not the
// marked `false` fallback used by Marked.use({tokenizer}).
//
// Del is called as del(src, maskedSrc, prevChar). Pi's strict strikethrough
// override only reads src and may call Lexer.InlineTokens.
type Tokenizer struct {
	Lexer *Lexer
	rules *ruleSet

	Space      func(src string) *Token
	Code       func(src string) *Token
	Fences     func(src string) *Token
	Heading    func(src string) *Token
	Hr         func(src string) *Token
	Blockquote func(src string) *Token
	List       func(src string) *Token
	HTML       func(src string) *Token
	Def        func(src string) *Token
	Table      func(src string) *Token
	LHeading   func(src string) *Token
	Paragraph  func(src string) *Token
	Text       func(src string) *Token
	Escape     func(src string) *Token
	Tag        func(src string) *Token
	Link       func(src string) *Token
	Reflink    func(src string, links map[string]Link) *Token
	EmStrong   func(src, maskedSrc, prevChar string) *Token
	Codespan   func(src string) *Token
	Br         func(src string) *Token
	Del        func(src, maskedSrc, prevChar string) *Token
	Autolink   func(src string) *Token
	URL        func(src string) *Token
	InlineText func(src string) *Token
}

// NewTokenizer returns a tokenizer with every rule built in.
func NewTokenizer() *Tokenizer { return &Tokenizer{} }

func (t *Tokenizer) pedantic() bool { return t.Lexer != nil && t.Lexer.pedantic }
func (t *Tokenizer) gfmOn() bool    { return t.Lexer == nil || t.Lexer.gfm }

func (t *Tokenizer) callSpace(src string) *Token {
	if t.Space != nil {
		return t.Space(src)
	}
	return t.space(src)
}
func (t *Tokenizer) callCode(src string) *Token {
	if t.Code != nil {
		return t.Code(src)
	}
	return t.code(src)
}
func (t *Tokenizer) callFences(src string) *Token {
	if t.Fences != nil {
		return t.Fences(src)
	}
	return t.fences(src)
}
func (t *Tokenizer) callHeading(src string) *Token {
	if t.Heading != nil {
		return t.Heading(src)
	}
	return t.heading(src)
}
func (t *Tokenizer) callHr(src string) *Token {
	if t.Hr != nil {
		return t.Hr(src)
	}
	return t.hr(src)
}
func (t *Tokenizer) callBlockquote(src string) *Token {
	if t.Blockquote != nil {
		return t.Blockquote(src)
	}
	return t.blockquote(src)
}
func (t *Tokenizer) callList(src string) *Token {
	if t.List != nil {
		return t.List(src)
	}
	return t.list(src)
}
func (t *Tokenizer) callHTML(src string) *Token {
	if t.HTML != nil {
		return t.HTML(src)
	}
	return t.html(src)
}
func (t *Tokenizer) callDef(src string) *Token {
	if t.Def != nil {
		return t.Def(src)
	}
	return t.def(src)
}
func (t *Tokenizer) callTable(src string) *Token {
	if t.Table != nil {
		return t.Table(src)
	}
	return t.table(src)
}
func (t *Tokenizer) callLHeading(src string) *Token {
	if t.LHeading != nil {
		return t.LHeading(src)
	}
	return t.lheading(src)
}
func (t *Tokenizer) callParagraph(src string) *Token {
	if t.Paragraph != nil {
		return t.Paragraph(src)
	}
	return t.paragraph(src)
}
func (t *Tokenizer) callText(src string) *Token {
	if t.Text != nil {
		return t.Text(src)
	}
	return t.text(src)
}
func (t *Tokenizer) callEscape(src string) *Token {
	if t.Escape != nil {
		return t.Escape(src)
	}
	return t.escape(src)
}
func (t *Tokenizer) callTag(src string) *Token {
	if t.Tag != nil {
		return t.Tag(src)
	}
	return t.tag(src)
}
func (t *Tokenizer) callLink(src string) *Token {
	if t.Link != nil {
		return t.Link(src)
	}
	return t.link(src)
}
func (t *Tokenizer) callReflink(src string, links map[string]Link) *Token {
	if t.Reflink != nil {
		return t.Reflink(src, links)
	}
	return t.reflink(src, links)
}
func (t *Tokenizer) callEmStrong(src, maskedSrc, prevChar string) *Token {
	if t.EmStrong != nil {
		return t.EmStrong(src, maskedSrc, prevChar)
	}
	return t.emStrong(src, maskedSrc, prevChar)
}
func (t *Tokenizer) callCodespan(src string) *Token {
	if t.Codespan != nil {
		return t.Codespan(src)
	}
	return t.codespan(src)
}
func (t *Tokenizer) callBr(src string) *Token {
	if t.Br != nil {
		return t.Br(src)
	}
	return t.br(src)
}
func (t *Tokenizer) callDel(src, maskedSrc, prevChar string) *Token {
	if t.Del != nil {
		return t.Del(src, maskedSrc, prevChar)
	}
	return t.del(src, maskedSrc, prevChar)
}
func (t *Tokenizer) callAutolink(src string) *Token {
	if t.Autolink != nil {
		return t.Autolink(src)
	}
	return t.autolink(src)
}
func (t *Tokenizer) callURL(src string) *Token {
	if t.URL != nil {
		return t.URL(src)
	}
	return t.url(src)
}
func (t *Tokenizer) callInlineText(src string) *Token {
	if t.InlineText != nil {
		return t.InlineText(src)
	}
	return t.inlineText(src)
}

func (t *Tokenizer) space(src string) *Token {
	cap := t.rules.block.newline.exec(src)
	if cap != nil && cap.full != "" {
		return &Token{Type: "space", Raw: cap.full}
	}
	return nil
}

func (t *Tokenizer) code(src string) *Token {
	cap := t.rules.block.code.exec(src)
	if cap == nil {
		return nil
	}
	raw := cap.full
	if !t.pedantic() {
		raw = trimTrailingBlankLines(raw, t.rules.other.blankLine)
	}
	text := t.rules.other.codeRemoveIndent.replace(raw, "")
	return &Token{Type: "code", Raw: raw, CodeBlockStyle: "indented", Text: text}
}

func (t *Tokenizer) fences(src string) *Token {
	cap := t.rules.block.fences.exec(src)
	if cap == nil {
		return nil
	}
	raw := cap.full
	body := ""
	if cap.ok(3) {
		body = cap.group(3)
	}
	text := indentCodeCompensation(raw, body, t.rules)
	tok := &Token{Type: "code", Raw: raw, Text: text}
	lang := ""
	if cap.ok(2) {
		lang = cap.group(2)
	}
	if lang != "" {
		lang = unescapePunct(jsTrim(lang), t.rules.inline.anyPunctuation)
	}
	tok.Lang = &lang
	return tok
}

func indentCodeCompensation(raw, text string, rules *ruleSet) string {
	m := rules.other.indentCodeCompensation.exec(raw)
	if m == nil {
		return text
	}
	indentToCode := m.group(1)
	lines := strings.Split(text, "\n")
	for i, node := range lines {
		ind := rules.other.beginningSpace.exec(node)
		if ind == nil {
			continue
		}
		if runeCount(ind.full) >= runeCount(indentToCode) {
			lines[i] = runeCut(node, runeCount(indentToCode))
		}
	}
	return strings.Join(lines, "\n")
}

func (t *Tokenizer) heading(src string) *Token {
	cap := t.rules.block.heading.exec(src)
	if cap == nil {
		return nil
	}
	text := jsTrim(cap.group(2))
	if t.rules.other.endingHash.test(text) {
		trimmed := rtrim(text, "#", false)
		if t.pedantic() {
			text = jsTrim(trimmed)
		} else if trimmed == "" || t.rules.other.endingSpaceChar.test(trimmed) {
			text = jsTrim(trimmed)
		}
	}
	tok := &Token{
		Type:  "heading",
		Raw:   rtrim(cap.full, "\n", false),
		Depth: runeCount(cap.group(1)),
		Text:  text,
	}
	t.Lexer.queueInline(&tok.Tokens, text)
	return tok
}

func (t *Tokenizer) hr(src string) *Token {
	cap := t.rules.block.hr.exec(src)
	if cap == nil {
		return nil
	}
	return &Token{Type: "hr", Raw: rtrim(cap.full, "\n", false)}
}

func (t *Tokenizer) blockquote(src string) *Token {
	cap := t.rules.block.blockquote.exec(src)
	if cap == nil {
		return nil
	}
	lines := strings.Split(rtrim(cap.full, "\n", false), "\n")
	raw := ""
	text := ""
	var tokens []*Token
	for len(lines) > 0 {
		inBQ := false
		var current []string
		i := 0
		for ; i < len(lines); i++ {
			if t.rules.other.blockquoteStart.test(lines[i]) {
				current = append(current, lines[i])
				inBQ = true
			} else if !inBQ {
				current = append(current, lines[i])
			} else {
				break
			}
		}
		lines = lines[i:]
		currentRaw := strings.Join(current, "\n")
		currentText := t.rules.other.blockquoteSetextReplace.replace(currentRaw, "\n    $1")
		currentText = t.rules.other.blockquoteSetextReplace2.replace(currentText, "")
		if raw == "" {
			raw = currentRaw
		} else {
			raw = raw + "\n" + currentRaw
		}
		if text == "" {
			text = currentText
		} else {
			text = text + "\n" + currentText
		}
		top := t.Lexer.state.top
		t.Lexer.state.top = true
		t.Lexer.blockTokens(currentText, &tokens, true)
		t.Lexer.state.top = top
		if len(lines) == 0 {
			break
		}
		var last *Token
		if len(tokens) > 0 {
			last = tokens[len(tokens)-1]
		}
		if last != nil && last.Type == "code" {
			break
		} else if last != nil && last.Type == "blockquote" {
			continuation := strings.Join(lines, "\n")
			newText := last.Raw + "\n" + t.rules.other.blockquoteSetextReplace2.replace(continuation, "")
			newTok := t.blockquote(newText)
			tokens[len(tokens)-1] = newTok
			raw = raw + "\n" + continuation
			text = jsSlice(text, 0, runeCount(text)-runeCount(last.Text)) + newTok.Text
			break
		} else if last != nil && last.Type == "list" {
			oldRaw := last.Raw
			newText := oldRaw + "\n" + strings.Join(lines, "\n")
			newTok := t.list(newText)
			tokens[len(tokens)-1] = newTok
			raw = jsSlice(raw, 0, runeCount(raw)-runeCount(oldRaw)) + newTok.Raw
			text = jsSlice(text, 0, runeCount(text)-runeCount(oldRaw)) + newTok.Raw
			rest := runeCut(newText, runeCount(tokens[len(tokens)-1].Raw))
			lines = strings.Split(rest, "\n")
			continue
		}
	}
	return &Token{Type: "blockquote", Raw: raw, Tokens: tokens, Text: text}
}

func (t *Tokenizer) list(src string) *Token {
	cap := t.rules.block.list.exec(src)
	if cap == nil {
		return nil
	}
	bull := jsTrim(cap.group(1))
	isOrdered := runeCount(bull) > 1
	list := &Token{Type: "list", Ordered: isOrdered, Items: []*Token{}}
	if isOrdered {
		n := 0
		for _, r := range bull {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		list.Start = intPtr(n)
	}
	if isOrdered {
		bull = `\d{1,9}\` + lastChar(bull)
	} else {
		bull = `\` + bull
	}
	if t.pedantic() && !isOrdered {
		bull = `[*+-]`
	}
	itemRE := t.rules.other.listItemRegex(bull)
	endsWithBlank := false
	for src != "" {
		endEarly := false
		cap = itemRE.exec(src)
		if cap == nil {
			break
		}
		if t.rules.block.hr.test(src) {
			break
		}
		raw := cap.full
		src = runeCut(src, runeCount(raw))
		itemCap2 := ""
		if cap.ok(2) {
			itemCap2 = cap.group(2)
		}
		line := expandTabs(firstLine(itemCap2), runeCount(cap.group(1)))
		nextLine := firstLine(src)
		blankLine := jsTrim(line) == ""
		indent := 0
		itemContents := ""
		if t.pedantic() {
			indent = 2
			itemContents = jsTrimStart(line)
		} else if blankLine {
			indent = runeCount(cap.group(1)) + 1
		} else {
			indent = lineSearch(line, t.rules.other.nonSpaceChar)
			if indent > 4 {
				indent = 1
			}
			itemContents = jsSlice(line, indent, runeCount(line))
			indent += runeCount(cap.group(1))
		}
		if blankLine && t.rules.other.blankLine.test(nextLine) {
			raw += nextLine + "\n"
			src = runeCut(src, runeCount(nextLine)+1)
			endEarly = true
		}
		if !endEarly {
			nextBullet := t.rules.other.nextBulletRegex(indent)
			hrRE := t.rules.other.hrRegex(indent)
			fencesBegin := t.rules.other.fencesBeginRegex(indent)
			headingBegin := t.rules.other.headingBeginRegex(indent)
			htmlBegin := t.rules.other.htmlBeginRegex(indent)
			bqBegin := t.rules.other.blockquoteBeginRegex(indent)
			for src != "" {
				rawLine := firstLine(src)
				nextLine = rawLine
				var nextLineWithoutTabs string
				if t.pedantic() {
					nextLine = t.rules.other.listReplaceNesting.replace(nextLine, "  ")
					nextLineWithoutTabs = nextLine
				} else {
					nextLineWithoutTabs = t.rules.other.tabCharGlobal.replace(nextLine, "    ")
				}
				if fencesBegin.test(nextLine) || headingBegin.test(nextLine) || htmlBegin.test(nextLine) || bqBegin.test(nextLine) || nextBullet.test(nextLine) || hrRE.test(nextLine) {
					break
				}
				if lineSearch(nextLineWithoutTabs, t.rules.other.nonSpaceChar) >= indent || jsTrim(nextLine) == "" {
					itemContents += "\n" + runeCut(nextLineWithoutTabs, indent)
				} else {
					if blankLine {
						break
					}
					if lineSearch(t.rules.other.tabCharGlobal.replace(line, "    "), t.rules.other.nonSpaceChar) >= 4 {
						break
					}
					if fencesBegin.test(line) || headingBegin.test(line) || hrRE.test(line) {
						break
					}
					itemContents += "\n" + nextLine
				}
				blankLine = jsTrim(nextLine) == ""
				raw += rawLine + "\n"
				src = runeCut(src, runeCount(rawLine)+1)
				line = runeCut(nextLineWithoutTabs, indent)
			}
		}
		if !list.Loose {
			if endsWithBlank {
				list.Loose = true
			} else if t.rules.other.doubleBlankLine.test(raw) {
				endsWithBlank = true
			}
		}
		item := &Token{
			Type:   "list_item",
			Raw:    raw,
			Task:   t.gfmOn() && t.rules.other.listIsTask.test(itemContents),
			Loose:  false,
			Text:   itemContents,
			Tokens: []*Token{},
		}
		list.Items = append(list.Items, item)
		list.Raw += raw
	}
	if len(list.Items) == 0 {
		return nil
	}
	lastItem := list.Items[len(list.Items)-1]
	lastItem.Raw = jsTrimEnd(lastItem.Raw)
	lastItem.Text = jsTrimEnd(lastItem.Text)
	list.Raw = jsTrimEnd(list.Raw)

	for _, item := range list.Items {
		t.Lexer.state.top = false
		item.Tokens = t.Lexer.blockTokens(item.Text, nil, false)
		if !list.Loose {
			for _, sp := range item.Tokens {
				if sp.Type == "space" && t.rules.other.anyLine.test(sp.Raw) {
					list.Loose = true
					break
				}
			}
		}
	}
	for _, item := range list.Items {
		if len(item.Tokens) == 0 {
			if item.Task {
				item.Task = false
			}
			continue
		}
		itemToken := item.Tokens[0]
		if item.Task && (itemToken.Type == "text" || itemToken.Type == "paragraph") {
			item.Text = t.rules.other.listReplaceTask.replace(item.Text, "")
			itemToken.Raw = t.rules.other.listReplaceTask.replace(itemToken.Raw, "")
			itemToken.Text = t.rules.other.listReplaceTask.replace(itemToken.Text, "")
			for i := len(t.Lexer.inlineQueue) - 1; i >= 0; i-- {
				if t.rules.other.listIsTask.test(t.Lexer.inlineQueue[i].src) {
					t.Lexer.inlineQueue[i].src = t.rules.other.listReplaceTask.replace(t.Lexer.inlineQueue[i].src, "")
					break
				}
			}
			taskRaw := t.rules.other.listTaskCheckbox.exec(item.Raw)
			if taskRaw != nil {
				checked := taskRaw.full != "[ ]"
				cb := &Token{Type: "checkbox", Raw: taskRaw.full + " ", Checked: boolPtr(checked)}
				item.Checked = boolPtr(checked)
				if list.Loose {
					if (itemToken.Type == "paragraph" || itemToken.Type == "text") && itemToken.Tokens != nil {
						itemToken.Raw = cb.Raw + itemToken.Raw
						itemToken.Text = cb.Raw + itemToken.Text
						itemToken.Tokens = append([]*Token{cb}, itemToken.Tokens...)
					} else {
						para := &Token{Type: "paragraph", Raw: cb.Raw, Text: cb.Raw, Tokens: []*Token{cb}}
						item.Tokens = append([]*Token{para}, item.Tokens...)
					}
				} else {
					item.Tokens = append([]*Token{cb}, item.Tokens...)
				}
			}
		} else if item.Task {
			item.Task = false
		}
	}
	if list.Loose {
		for _, item := range list.Items {
			item.Loose = true
			for _, tok := range item.Tokens {
				if tok.Type == "text" {
					tok.Type = "paragraph"
				}
			}
		}
	}
	return list
}

func lineSearch(s string, re *jsRE) int {
	return re.search(s)
}

func (t *Tokenizer) html(src string) *Token {
	cap := t.rules.block.html.exec(src)
	if cap == nil {
		return nil
	}
	raw := trimTrailingBlankLines(cap.full, t.rules.other.blankLine)
	name := cap.group(1)
	return &Token{
		Type:  "html",
		Block: true,
		Raw:   raw,
		Pre:   name == "pre" || name == "script" || name == "style",
		Text:  raw,
	}
}

func (t *Tokenizer) def(src string) *Token {
	cap := t.rules.block.def.exec(src)
	if cap == nil {
		return nil
	}
	tag := strings.ToLower(cap.group(1))
	tag = t.rules.other.multipleSpaceGlobal.replace(tag, " ")
	href := ""
	if cap.group(2) != "" {
		href = t.rules.other.hrefBrackets.replace(cap.group(2), "$1")
		href = unescapePunct(href, t.rules.inline.anyPunctuation)
	}
	var title *string
	if cap.ok(3) && cap.group(3) != "" {
		inner := jsSlice(cap.group(3), 1, -1)
		inner = unescapePunct(inner, t.rules.inline.anyPunctuation)
		title = &inner
	} else if cap.ok(3) {
		empty := ""
		title = &empty
	}
	return &Token{Type: "def", Tag: tag, Raw: rtrim(cap.full, "\n", false), Href: href, Title: title}
}

func (t *Tokenizer) table(src string) *Token {
	cap := t.rules.block.table.exec(src)
	if cap == nil {
		return nil
	}
	if !t.rules.other.tableDelimiter.test(cap.group(2)) {
		return nil
	}
	headers := splitCells(cap.group(1), 0, t.rules.other)
	alignSrc := t.rules.other.tableAlignChars.replace(cap.group(2), "")
	alignsRaw := strings.Split(alignSrc, "|")
	var rows []string
	if cap.ok(3) && jsTrim(cap.group(3)) != "" {
		body := t.rules.other.tableRowBlankLine.replace(cap.group(3), "")
		rows = strings.Split(body, "\n")
	}
	if len(headers) != len(alignsRaw) {
		return nil
	}
	tok := &Token{Type: "table", Raw: rtrim(cap.full, "\n", false)}
	tok.Align = make([]*string, len(alignsRaw))
	for i, align := range alignsRaw {
		switch {
		case t.rules.other.tableAlignRight.test(align):
			tok.Align[i] = strPtr("right")
		case t.rules.other.tableAlignCenter.test(align):
			tok.Align[i] = strPtr("center")
		case t.rules.other.tableAlignLeft.test(align):
			tok.Align[i] = strPtr("left")
		}
	}
	tok.Header = make([]TableCell, len(headers))
	for i, h := range headers {
		tok.Header[i] = TableCell{Text: h, Header: true, Align: tok.Align[i]}
	}
	tok.Rows = make([][]TableCell, len(rows))
	for ri, row := range rows {
		cells := splitCells(row, len(tok.Header), t.rules.other)
		tok.Rows[ri] = make([]TableCell, len(cells))
		for i, cell := range cells {
			var align *string
			if i < len(tok.Align) {
				align = tok.Align[i]
			}
			tok.Rows[ri][i] = TableCell{Text: cell, Header: false, Align: align}
		}
	}
	for i := range tok.Header {
		t.Lexer.queueInline(&tok.Header[i].Tokens, tok.Header[i].Text)
	}
	for ri := range tok.Rows {
		for i := range tok.Rows[ri] {
			t.Lexer.queueInline(&tok.Rows[ri][i].Tokens, tok.Rows[ri][i].Text)
		}
	}
	return tok
}

func (t *Tokenizer) lheading(src string) *Token {
	cap := t.rules.block.lheading.exec(src)
	if cap == nil {
		return nil
	}
	text := jsTrim(cap.group(1))
	depth := 2
	if strings.HasPrefix(cap.group(2), "=") {
		depth = 1
	}
	tok := &Token{Type: "heading", Raw: rtrim(cap.full, "\n", false), Depth: depth, Text: text}
	t.Lexer.queueInline(&tok.Tokens, text)
	return tok
}

func (t *Tokenizer) paragraph(src string) *Token {
	cap := t.rules.block.paragraph.exec(src)
	if cap == nil {
		return nil
	}
	text := cap.group(1)
	if text != "" && lastChar(text) == "\n" {
		text = jsSlice(text, 0, -1)
	}
	tok := &Token{Type: "paragraph", Raw: cap.full, Text: text}
	t.Lexer.queueInline(&tok.Tokens, text)
	return tok
}

func (t *Tokenizer) text(src string) *Token {
	cap := t.rules.block.text.exec(src)
	if cap == nil {
		return nil
	}
	tok := &Token{Type: "text", Raw: cap.full, Text: cap.full}
	t.Lexer.queueInline(&tok.Tokens, cap.full)
	return tok
}

func (t *Tokenizer) escape(src string) *Token {
	cap := t.rules.inline.escape.exec(src)
	if cap == nil {
		return nil
	}
	return &Token{Type: "escape", Raw: cap.full, Text: cap.group(1)}
}

func (t *Tokenizer) tag(src string) *Token {
	cap := t.rules.inline.tag.exec(src)
	if cap == nil {
		return nil
	}
	st := &t.Lexer.state
	if !st.inLink && t.rules.other.startATag.test(cap.full) {
		st.inLink = true
	} else if st.inLink && t.rules.other.endATag.test(cap.full) {
		st.inLink = false
	}
	if !st.inRawBlock && t.rules.other.startPreScriptTag.test(cap.full) {
		st.inRawBlock = true
	} else if st.inRawBlock && t.rules.other.endPreScriptTag.test(cap.full) {
		st.inRawBlock = false
	}
	return &Token{
		Type:       "html",
		Raw:        cap.full,
		InLink:     st.inLink,
		InRawBlock: st.inRawBlock,
		Block:      false,
		Text:       cap.full,
	}
}

func (t *Tokenizer) link(src string) *Token {
	cap := t.rules.inline.link.exec(src)
	if cap == nil {
		return nil
	}
	raw := cap.full
	label := cap.group(1)
	hrefPart := cap.group(2)
	titlePart := ""
	if cap.ok(3) {
		titlePart = cap.group(3)
	}
	trimmedURL := jsTrim(hrefPart)
	if !t.pedantic() && t.rules.other.startAngleBracket.test(trimmedURL) {
		if !t.rules.other.endAngleBracket.test(trimmedURL) {
			return nil
		}
		rtrimSlash := rtrim(jsSlice(trimmedURL, 0, -1), "\\", false)
		if (runeCount(trimmedURL)-runeCount(rtrimSlash))%2 == 0 {
			return nil
		}
	} else {
		lastParen := findClosingBracket(hrefPart, "()")
		if lastParen == -2 {
			return nil
		}
		if lastParen > -1 {
			start := 4
			if strings.HasPrefix(raw, "!") {
				start = 5
			}
			linkLen := start + runeCount(label) + lastParen
			hrefPart = jsSlice(hrefPart, 0, lastParen)
			raw = jsTrim(jsSlice(raw, 0, linkLen))
			titlePart = ""
		}
	}
	href := hrefPart
	title := ""
	if t.pedantic() {
		link := t.rules.other.pedanticHrefTitle.exec(href)
		if link != nil {
			href = link.group(1)
			title = link.group(3)
		}
	} else if titlePart != "" {
		title = jsSlice(titlePart, 1, -1)
	}
	href = jsTrim(href)
	if t.rules.other.startAngleBracket.test(href) {
		if t.pedantic() && !t.rules.other.endAngleBracket.test(trimmedURL) {
			href = runeCut(href, 1)
		} else {
			href = jsSlice(href, 1, -1)
		}
	}
	if href != "" {
		href = unescapePunct(href, t.rules.inline.anyPunctuation)
	}
	if title != "" {
		title = unescapePunct(title, t.rules.inline.anyPunctuation)
	}
	var titlePtr *string
	if title != "" {
		titlePtr = &title
	}
	return outputLink(label, raw, href, titlePtr, t.Lexer, t.rules)
}

func outputLink(label, raw, href string, title *string, lx *Lexer, rules *ruleSet) *Token {
	text := rules.other.outputLinkReplace.replace(label, "$1")
	isImage := strings.HasPrefix(raw, "!")
	lx.state.inLink = true
	outerEmitted := lx.state.linkEmitted
	outerRaw := lx.state.inRawBlock
	lx.state.linkEmitted = false
	tokens := lx.inlineTokens(text, nil)
	textHasLink := lx.state.linkEmitted
	lx.state.linkEmitted = outerEmitted
	lx.state.inLink = false
	if !isImage {
		if textHasLink {
			lx.state.inRawBlock = outerRaw
			return nil
		}
		lx.state.linkEmitted = true
	}
	kind := "link"
	if isImage {
		kind = "image"
	}
	if title != nil && *title == "" {
		title = nil
	}
	return &Token{Type: kind, Raw: raw, Href: href, Title: title, Text: text, Tokens: tokens}
}

func (t *Tokenizer) reflink(src string, links map[string]Link) *Token {
	cap := t.rules.inline.reflink.exec(src)
	if cap == nil {
		cap = t.rules.inline.nolink.exec(src)
	}
	if cap == nil {
		return nil
	}
	linkString := cap.group(2)
	if linkString == "" {
		linkString = cap.group(1)
	}
	linkString = t.rules.other.multipleSpaceGlobal.replace(linkString, " ")
	link, ok := links[strings.ToLower(linkString)]
	if !ok {
		ch := charAt(cap.full, 0)
		return &Token{Type: "text", Raw: ch, Text: ch}
	}
	return outputLink(cap.group(1), cap.full, link.Href, link.Title, t.Lexer, t.rules)
}

func (t *Tokenizer) emStrong(src, maskedSrc, prevChar string) *Token {
	match := t.rules.inline.emStrongLDelim.exec(src)
	if match == nil {
		return nil
	}
	if !match.ok(1) && !match.ok(2) && !match.ok(3) && !match.ok(4) {
		return nil
	}
	if match.ok(4) && t.rules.other.unicodeAlphaNumeric.exec(prevChar) != nil {
		return nil
	}
	nextChar := match.group(1)
	if nextChar == "" {
		nextChar = match.group(3)
	}
	if nextChar != "" && prevChar != "" && t.rules.inline.punctuation.exec(prevChar) == nil {
		return nil
	}
	lLength := runeCount(match.full) - 1
	delimTotal := lLength
	midDelimTotal := 0
	delimChar := firstRune(match.full)
	midRun := prevChar == string(delimChar)
	endReg := t.rules.inline.emStrongRDelimUnd
	if delimChar == '*' {
		endReg = t.rules.inline.emStrongRDelimAst
	}
	maskedSrc = jsSlice(maskedSrc, -runeCount(src)+lLength, runeCount(maskedSrc))
	for _, match := range endReg.execAll(maskedSrc) {
		rDelim := firstNonEmpty(match.group(1), match.group(2), match.group(3), match.group(4), match.group(5), match.group(6))
		if rDelim == "" {
			continue
		}
		rLength := runeCount(rDelim)
		if match.ok(3) || match.ok(4) {
			delimTotal += rLength
			continue
		} else if match.ok(5) || match.ok(6) {
			if lLength%3 != 0 && (lLength+rLength)%3 == 0 {
				midDelimTotal += rLength
				continue
			}
			if midRun {
				break
			}
		}
		delimTotal -= rLength
		if delimTotal > 0 {
			continue
		}
		rLength = min(rLength, rLength+delimTotal+midDelimTotal)
		lastCharLength := 1
		if match.full == "" {
			lastCharLength = 0
		}
		raw := jsSlice(src, 0, lLength+match.index+lastCharLength+rLength)
		if min(lLength, rLength)%2 == 1 {
			text := jsSlice(raw, 1, -1)
			return &Token{Type: "em", Raw: raw, Text: text, Tokens: t.Lexer.inlineTokens(text, nil)}
		}
		text := jsSlice(raw, 2, -2)
		return &Token{Type: "strong", Raw: raw, Text: text, Tokens: t.Lexer.inlineTokens(text, nil)}
	}
	return nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func (t *Tokenizer) codespan(src string) *Token {
	cap := t.rules.inline.code.exec(src)
	if cap == nil {
		return nil
	}
	text := t.rules.other.newLineCharGlobal.replace(cap.group(2), " ")
	hasNonSpace := t.rules.other.nonSpaceChar.test(text)
	hasBoth := t.rules.other.startingSpaceChar.test(text) && t.rules.other.endingSpaceChar.test(text)
	if hasNonSpace && hasBoth {
		text = jsSlice(text, 1, -1)
	}
	return &Token{Type: "codespan", Raw: cap.full, Text: text}
}

func (t *Tokenizer) br(src string) *Token {
	cap := t.rules.inline.br.exec(src)
	if cap == nil {
		return nil
	}
	return &Token{Type: "br", Raw: cap.full}
}

func (t *Tokenizer) del(src, maskedSrc, prevChar string) *Token {
	match := t.rules.inline.delLDelim.exec(src)
	if match == nil {
		return nil
	}
	nextChar := match.group(1)
	if nextChar != "" && prevChar != "" && t.rules.inline.punctuation.exec(prevChar) == nil {
		return nil
	}
	lLength := runeCount(match.full) - 1
	delimTotal := lLength
	endReg := t.rules.inline.delRDelim
	maskedSrc = jsSlice(maskedSrc, -runeCount(src)+lLength, runeCount(maskedSrc))
	for _, match := range endReg.execAll(maskedSrc) {
		rDelim := firstNonEmpty(match.group(1), match.group(2), match.group(3), match.group(4), match.group(5), match.group(6))
		if rDelim == "" {
			continue
		}
		rLength := runeCount(rDelim)
		if rLength != lLength {
			continue
		}
		if match.ok(3) || match.ok(4) {
			delimTotal += rLength
			continue
		}
		delimTotal -= rLength
		if delimTotal > 0 {
			continue
		}
		rLength = min(rLength, rLength+delimTotal)
		lastCharLength := 1
		if match.full == "" {
			lastCharLength = 0
		}
		raw := jsSlice(src, 0, lLength+match.index+lastCharLength+rLength)
		text := jsSlice(raw, lLength, -lLength)
		return &Token{Type: "del", Raw: raw, Text: text, Tokens: t.Lexer.inlineTokens(text, nil)}
	}
	return nil
}

func (t *Tokenizer) autolink(src string) *Token {
	cap := t.rules.inline.autolink.exec(src)
	if cap == nil {
		return nil
	}
	text := cap.group(1)
	href := text
	if cap.group(2) == "@" {
		href = "mailto:" + text
	}
	return &Token{
		Type:   "link",
		Raw:    cap.full,
		Text:   text,
		Href:   href,
		Tokens: []*Token{{Type: "text", Raw: text, Text: text}},
	}
}

func (t *Tokenizer) url(src string) *Token {
	cap := t.rules.inline.url.exec(src)
	if cap == nil {
		return nil
	}
	var text, href string
	if cap.group(2) == "@" {
		text = cap.full
		href = "mailto:" + text
	} else {
		cur := cap.full
		for {
			prev := cur
			back := t.rules.inline.backpedal.exec(cur)
			if back != nil {
				cur = back.full
			} else {
				cur = ""
			}
			if prev == cur {
				break
			}
		}
		text = cur
		if cap.group(1) == "www." {
			href = "http://" + cur
		} else {
			href = cur
		}
	}
	return &Token{
		Type:   "link",
		Raw:    text,
		Text:   text,
		Href:   href,
		Tokens: []*Token{{Type: "text", Raw: text, Text: text}},
	}
}

func (t *Tokenizer) inlineText(src string) *Token {
	cap := t.rules.inline.text.exec(src)
	if cap == nil {
		return nil
	}
	return &Token{Type: "text", Raw: cap.full, Text: cap.full, Escaped: t.Lexer.state.inRawBlock}
}
