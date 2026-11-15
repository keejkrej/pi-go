// Ported from packages/ai/src/utils/streaming-json.ts (pi v1.5.2).

package ai

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/jsre"
)

// JsonPath is a path from the root value: string member keys and int array indices.
type JsonPath = []any

// JsonChangeKind identifies whether a streaming JSON change sets a value or extends an open string.
type JsonChangeKind string

const (
	JsonChangeKindSet    JsonChangeKind = "set"
	JsonChangeKindAppend JsonChangeKind = "append"
)

// JsonChange is one change a piece of text made to the parsed value.
// Implementations are *JsonChangeSet and *JsonChangeAppend.
type JsonChange interface {
	isJsonChange()
	ChangeKind() JsonChangeKind
	ChangePath() JsonPath
}

// JsonChangeSet records a newly appeared key, array element, or completed value.
type JsonChangeSet struct {
	Kind  JsonChangeKind `json:"kind"`
	Path  JsonPath       `json:"path"`
	Value any            `json:"value"`
}

func (*JsonChangeSet) isJsonChange()                {}
func (c *JsonChangeSet) ChangeKind() JsonChangeKind { return c.Kind }
func (c *JsonChangeSet) ChangePath() JsonPath       { return c.Path }

// JsonChangeAppend records text appended to an already-started string value.
type JsonChangeAppend struct {
	Kind JsonChangeKind `json:"kind"`
	Path JsonPath       `json:"path"`
	Text string         `json:"text"`
}

func (*JsonChangeAppend) isJsonChange()                {}
func (c *JsonChangeAppend) ChangeKind() JsonChangeKind { return c.Kind }
func (c *JsonChangeAppend) ChangePath() JsonPath       { return c.Path }

type usjSlotKind int

const (
	usjSlotRoot usjSlotKind = iota + 1
	usjSlotMember
	usjSlotElement
)

type usjSlot struct {
	kind  usjSlotKind
	key   string
	index int
}

type usjFramePhase int

const (
	usjPhaseKey usjFramePhase = iota + 1
	usjPhaseColon
	usjPhaseValue
	usjPhaseComma
)

type usjFrame struct {
	isArray       bool
	obj           *jsonx.Object
	arr           []any
	slot          *usjSlot
	path          JsonPath
	hasPath       bool
	reportsInside bool
	size          int
	key           string
	hasKey        bool
	phase         usjFramePhase
}

type usjOpenString struct {
	isKey   bool
	slot    *usjSlot
	text    string
	escape  string
	path    JsonPath
	hasPath bool
	change  JsonChange
}

const (
	usjMaxReportedDepth      = 32
	usjCreditPerAppend       = 256
	usjCreditPerCharacter    = 8
	usjMaxPendingScalarChars = 64
)

var (
	usjNumberRE                      = jsre.MustCompile(`^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`, "")
	usjNumberWithoutExponentDigitsRE = jsre.MustCompile(`^(-?(?:0|[1-9]\d*)(?:\.\d+)?)[eE][+-]?$`, "")
	usjLiterals                      = []struct {
		name  string
		value any
	}{
		{"true", true},
		{"false", false},
		{"null", nil},
	}
	usjSimpleEscapes = map[byte]string{
		'"':  `"`,
		'\\': `\`,
		'/':  `/`,
		'b':  "\b",
		'f':  "\f",
		'n':  "\n",
		'r':  "\r",
		't':  "\t",
	}
)

// StreamingJsonParser incrementally and tolerantly parses JSON text arriving in pieces.
type StreamingJsonParser struct {
	root        any
	hasRoot     bool
	done        bool
	stack       []*usjFrame
	openString  *usjOpenString
	scalar      string
	scalarSlot  *usjSlot
	pathCost    int
	credit      int
	dirty       bool
	snapshot    any
	hasSnapshot bool
	snapshotSeq uint64
	changes     []JsonChange
}

// NewStreamingJsonParser creates a ready-to-use StreamingJsonParser.
func NewStreamingJsonParser() *StreamingJsonParser {
	return &StreamingJsonParser{}
}

// Append reads the next piece of text and returns the changes it made in order.
func (p *StreamingJsonParser) Append(text string) []JsonChange {
	p.credit += usjCreditPerAppend + usjCreditPerCharacter*js.Len(text)
	p.changes = nil
	if p.openString != nil {
		p.openString.change = nil
	}
	index := 0
	for index < len(text) && !p.done {
		if p.openString != nil {
			index = p.readString(p.openString, text, index)
		} else if p.scalar != "" {
			index = p.readScalar(text, index)
		} else {
			ch := text[index]
			index++
			p.structural(ch)
		}
	}
	return p.changes
}

// Snapshot returns the value of the text read so far and true once a value has started,
// or (nil, false) before a value starts.
func (p *StreamingJsonParser) Snapshot() (any, bool) {
	if !p.dirty {
		return p.snapshot, p.hasSnapshot
	}
	pendingVal, pendingSlot, hasPending := p.pendingScalar()
	cost := p.pathCost
	if hasPending {
		cost++
	}
	if cost > p.credit {
		return p.snapshot, p.hasSnapshot
	}
	p.credit -= cost
	p.dirty = false
	p.snapshot, p.hasSnapshot = p.copyOpenPath(pendingVal, pendingSlot, hasPending)
	p.snapshotSeq++
	return p.snapshot, p.hasSnapshot
}

func (p *StreamingJsonParser) readString(open *usjOpenString, text string, start int) int {
	runStart := start
	for index := start; index < len(text); index++ {
		ch := text[index]
		if open.escape != "" {
			if p.continueEscape(open, ch) {
				runStart = index + 1
				continue
			}
			runStart = index
		}
		if ch == '"' {
			if index > runStart {
				p.appendString(open, text[runStart:index])
			}
			p.endString(open)
			return index + 1
		}
		if ch == '\\' {
			if index > runStart {
				p.appendString(open, text[runStart:index])
			}
			open.escape = `\`
			runStart = index + 1
		}
	}
	if len(text) > runStart {
		p.appendString(open, text[runStart:])
	}
	return len(text)
}

func (p *StreamingJsonParser) readScalar(text string, start int) int {
	end := start
	for end < len(text) && !usjIsDelimiter(text[end]) {
		end++
	}
	if end > start {
		p.scalar += text[start:end]
		if p.scalarSlot != nil {
			p.dirty = true
		}
	}
	if end < len(text) {
		p.endScalar()
	}
	return end
}

func (p *StreamingJsonParser) structural(ch byte) {
	switch ch {
	case ' ', '\t', '\n', '\r':
		return
	case '"':
		p.startString()
	case '{':
		p.openObjectContainer()
	case '[':
		p.openArrayContainer()
	case '}', ']':
		p.closeContainer()
	case ':':
		if n := len(p.stack); n > 0 {
			f := p.stack[n-1]
			if !f.isArray && f.phase == usjPhaseColon {
				f.phase = usjPhaseValue
			}
		}
	case ',':
		if n := len(p.stack); n > 0 {
			f := p.stack[n-1]
			if f.isArray {
				f.phase = usjPhaseValue
			} else {
				f.phase = usjPhaseKey
			}
		}
	default:
		p.scalar = string(ch)
		p.scalarSlot = p.valueSlot()
		if p.scalarSlot != nil {
			p.dirty = true
		}
	}
}

func (p *StreamingJsonParser) valueSlot() *usjSlot {
	n := len(p.stack)
	if n == 0 {
		if !p.hasRoot {
			return &usjSlot{kind: usjSlotRoot}
		}
		return nil
	}
	f := p.stack[n-1]
	if f.isArray {
		return &usjSlot{kind: usjSlotElement, index: len(f.arr)}
	}
	if (f.phase == usjPhaseValue || f.phase == usjPhaseColon) && f.hasKey {
		return &usjSlot{kind: usjSlotMember, key: f.key}
	}
	return nil
}

func (p *StreamingJsonParser) startString() {
	if n := len(p.stack); n > 0 {
		f := p.stack[n-1]
		if !f.isArray && (f.phase == usjPhaseKey || f.phase == usjPhaseComma) {
			p.openString = &usjOpenString{isKey: true}
			return
		}
	}
	slot := p.valueSlot()
	path, hasPath := p.reportedPath(slot)
	open := &usjOpenString{
		isKey:   false,
		slot:    slot,
		path:    path,
		hasPath: hasPath,
	}
	p.openString = open
	p.place(slot, "")
	if hasPath {
		open.change = p.report(&JsonChangeSet{
			Kind:  JsonChangeKindSet,
			Path:  path,
			Value: "",
		})
	}
}

func (p *StreamingJsonParser) reportedPath(slot *usjSlot) (JsonPath, bool) {
	if slot == nil {
		return nil, false
	}
	if slot.kind == usjSlotRoot {
		return JsonPath{}, true
	}
	f := p.stack[len(p.stack)-1]
	if !f.reportsInside {
		return nil, false
	}
	out := make(JsonPath, len(f.path)+1)
	copy(out, f.path)
	if slot.kind == usjSlotMember {
		out[len(f.path)] = slot.key
	} else {
		out[len(f.path)] = slot.index
	}
	return out, true
}

func (p *StreamingJsonParser) report(c JsonChange) JsonChange {
	p.changes = append(p.changes, c)
	return c
}

func (p *StreamingJsonParser) continueEscape(open *usjOpenString, ch byte) bool {
	if open.escape == `\` {
		if decoded, ok := usjSimpleEscapes[ch]; ok {
			open.escape = ""
			p.appendString(open, decoded)
			return true
		}
		if ch == 'u' {
			open.escape = `\u`
			return true
		}
		open.escape = ""
		p.appendString(open, `\`)
		return false
	}
	if usjIsHexDigit(ch) {
		open.escape += string(ch)
		if len(open.escape) < 6 {
			return true
		}
		code, err := strconv.ParseUint(open.escape[2:], 16, 16)
		open.escape = ""
		if err != nil {
			return false
		}
		p.appendUTF16Unit(open, uint16(code))
		return true
	}
	literal := open.escape
	open.escape = ""
	p.appendString(open, literal)
	return false
}

// appendUTF16Unit appends a decoded \uXXXX code unit to open.text using WTF-8,
// combining adjacent high + low surrogate units into UTF-8 just like jsonx.ParseString.
func (p *StreamingJsonParser) appendUTF16Unit(open *usjOpenString, unit uint16) {
	if hi, ok := usjTrailingWTF8HighSurrogate(open.text); ok && unit >= 0xDC00 && unit <= 0xDFFF {
		combined := string(utf16.DecodeRune(rune(hi), rune(unit)))
		open.text = open.text[:len(open.text)-3] + combined
		if !open.isKey {
			p.place(open.slot, open.text)
			if open.hasPath {
				if open.change != nil && len(p.changes) > 0 && p.changes[len(p.changes)-1] == open.change {
					switch c := open.change.(type) {
					case *JsonChangeSet:
						if s, ok := c.Value.(string); ok && len(s) >= 3 {
							c.Value = s[:len(s)-3] + combined
							return
						}
					case *JsonChangeAppend:
						if len(c.Text) >= 3 {
							c.Text = c.Text[:len(c.Text)-3] + combined
							return
						}
					}
				}
				// High surrogate arrived in an earlier Append; start an append change with the low unit.
				open.change = p.report(&JsonChangeAppend{
					Kind: JsonChangeKindAppend,
					Path: open.path,
					Text: usjEncodeWTF16Unit(unit),
				})
			}
		}
		return
	}
	p.appendString(open, usjEncodeWTF16Unit(unit))
}

func usjTrailingWTF8HighSurrogate(s string) (uint16, bool) {
	if len(s) < 3 {
		return 0, false
	}
	b0, b1, b2 := s[len(s)-3], s[len(s)-2], s[len(s)-1]
	if b0 != 0xED || b1 < 0xA0 || b1 > 0xAF || b2 < 0x80 || b2 > 0xBF {
		return 0, false
	}
	code := (uint16(b0&0x0F) << 12) | (uint16(b1&0x3F) << 6) | uint16(b2&0x3F)
	return code, true
}

func usjEncodeWTF16Unit(u uint16) string {
	if u >= 0xD800 && u <= 0xDFFF {
		return string([]byte{
			byte(0xE0 | (u >> 12)),
			byte(0x80 | ((u >> 6) & 0x3F)),
			byte(0x80 | (u & 0x3F)),
		})
	}
	return string(rune(u))
}

func (p *StreamingJsonParser) appendString(open *usjOpenString, text string) {
	open.text += text
	if open.isKey {
		return
	}
	p.place(open.slot, open.text)
	if !open.hasPath {
		return
	}
	if open.change != nil && len(p.changes) > 0 && p.changes[len(p.changes)-1] == open.change {
		switch c := open.change.(type) {
		case *JsonChangeSet:
			prev, _ := c.Value.(string)
			c.Value = prev + text
			return
		case *JsonChangeAppend:
			c.Text += text
			return
		}
	}
	open.change = p.report(&JsonChangeAppend{
		Kind: JsonChangeKindAppend,
		Path: open.path,
		Text: text,
	})
}

func (p *StreamingJsonParser) endString(open *usjOpenString) {
	p.openString = nil
	var top *usjFrame
	if len(p.stack) > 0 {
		top = p.stack[len(p.stack)-1]
	}
	if open.isKey {
		if top == nil {
			return
		}
		top.key = open.text
		top.hasKey = true
		top.phase = usjPhaseColon
		return
	}
	p.valueEnded(top)
}

func (p *StreamingJsonParser) endScalar() {
	token := p.scalar
	slot := p.scalarSlot
	p.scalar = ""
	p.scalarSlot = nil
	if value, ok := usjScalarValue(token); ok {
		p.place(slot, value)
		if path, hasPath := p.reportedPath(slot); hasPath {
			p.report(&JsonChangeSet{
				Kind:  JsonChangeKindSet,
				Path:  path,
				Value: value,
			})
		}
	}
	var top *usjFrame
	if len(p.stack) > 0 {
		top = p.stack[len(p.stack)-1]
	}
	p.valueEnded(top)
}

func (p *StreamingJsonParser) pendingScalar() (any, *usjSlot, bool) {
	slot := p.scalarSlot
	token := p.scalar
	if slot == nil || token == "" || js.Len(token) > usjMaxPendingScalarChars {
		return nil, nil, false
	}
	val, ok := usjPendingScalarValue(token)
	if !ok {
		return nil, nil, false
	}
	return val, slot, true
}

func (p *StreamingJsonParser) openObjectContainer() {
	slot := p.valueSlot()
	path, hasPath := p.reportedPath(slot)
	obj := jsonx.NewObject()
	p.place(slot, obj)
	p.stack = append(p.stack, &usjFrame{
		isArray:       false,
		obj:           obj,
		slot:          slot,
		path:          path,
		hasPath:       hasPath,
		reportsInside: hasPath && len(path) < usjMaxReportedDepth,
		phase:         usjPhaseKey,
	})
	p.pathCost++
	p.dirty = true
	if hasPath {
		p.report(&JsonChangeSet{
			Kind:  JsonChangeKindSet,
			Path:  path,
			Value: jsonx.NewObject(),
		})
	}
}

func (p *StreamingJsonParser) openArrayContainer() {
	slot := p.valueSlot()
	path, hasPath := p.reportedPath(slot)
	arr := []any{}
	p.place(slot, arr)
	p.stack = append(p.stack, &usjFrame{
		isArray:       true,
		arr:           arr,
		slot:          slot,
		path:          path,
		hasPath:       hasPath,
		reportsInside: hasPath && len(path) < usjMaxReportedDepth,
		phase:         usjPhaseValue,
	})
	p.pathCost++
	p.dirty = true
	if hasPath {
		p.report(&JsonChangeSet{
			Kind:  JsonChangeKindSet,
			Path:  path,
			Value: []any{},
		})
	}
}

func (p *StreamingJsonParser) closeContainer() {
	if len(p.stack) == 0 {
		return
	}
	frame := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	p.pathCost -= frame.size + 1
	p.dirty = true
	if frame.hasPath && !frame.reportsInside {
		var closedVal any
		if frame.isArray {
			closedVal = frame.arr
		} else {
			closedVal = frame.obj
		}
		p.report(&JsonChangeSet{
			Kind:  JsonChangeKindSet,
			Path:  frame.path,
			Value: closedVal,
		})
	}
	var top *usjFrame
	if len(p.stack) > 0 {
		top = p.stack[len(p.stack)-1]
	}
	p.valueEnded(top)
}

func (p *StreamingJsonParser) valueEnded(frame *usjFrame) {
	if frame == nil {
		if p.hasRoot {
			p.done = true
		}
		return
	}
	frame.phase = usjPhaseComma
}

func (p *StreamingJsonParser) place(slot *usjSlot, value any) {
	if slot == nil {
		return
	}
	p.dirty = true
	if slot.kind == usjSlotRoot {
		p.root = value
		p.hasRoot = true
		return
	}
	frame := p.stack[len(p.stack)-1]
	if slot.kind == usjSlotElement {
		if slot.index == len(frame.arr) {
			frame.arr = append(frame.arr, value)
			frame.size++
			p.pathCost++
		} else {
			frame.arr[slot.index] = value
		}
		// Keep parent's reference up to date if the slice reallocated while appending.
		if len(p.stack) >= 2 && frame.slot != nil {
			parent := p.stack[len(p.stack)-2]
			usjSetFrameSlot(parent, frame.slot, frame.arr)
		} else if frame.slot != nil && frame.slot.kind == usjSlotRoot {
			p.root = frame.arr
		}
		return
	}
	if !frame.obj.Has(slot.key) {
		frame.size++
		p.pathCost++
	}
	frame.obj.Set(slot.key, value)
}

func (p *StreamingJsonParser) copyOpenPath(pendingVal any, pendingSlot *usjSlot, hasPending bool) (any, bool) {
	if len(p.stack) == 0 {
		if hasPending && pendingSlot.kind == usjSlotRoot {
			return pendingVal, true
		}
		return p.root, p.hasRoot
	}
	var child any
	var childSlot *usjSlot
	for depth := len(p.stack) - 1; depth >= 0; depth-- {
		frame := p.stack[depth]
		if frame.isArray {
			arrCopy := slices.Clone(frame.arr)
			if arrCopy == nil {
				arrCopy = []any{}
			}
			if depth == len(p.stack)-1 && hasPending {
				arrCopy = usjSetArraySlot(arrCopy, pendingSlot, pendingVal)
			}
			if childSlot != nil {
				arrCopy = usjSetArraySlot(arrCopy, childSlot, child)
			}
			child = arrCopy
			childSlot = frame.slot
			continue
		}
		objCopy := frame.obj.Clone()
		if depth == len(p.stack)-1 && hasPending && pendingSlot.kind == usjSlotMember {
			objCopy.Set(pendingSlot.key, pendingVal)
		}
		if childSlot != nil && childSlot.kind == usjSlotMember {
			objCopy.Set(childSlot.key, child)
		}
		child = objCopy
		childSlot = frame.slot
	}
	if childSlot != nil && childSlot.kind == usjSlotRoot {
		return child, true
	}
	return p.root, p.hasRoot
}

func usjSetFrameSlot(frame *usjFrame, slot *usjSlot, value any) {
	if slot == nil {
		return
	}
	if slot.kind == usjSlotElement {
		frame.arr[slot.index] = value
	} else if slot.kind == usjSlotMember {
		frame.obj.Set(slot.key, value)
	}
}

func usjSetArraySlot(arr []any, slot *usjSlot, value any) []any {
	if slot == nil || slot.kind != usjSlotElement {
		return arr
	}
	if slot.index == len(arr) {
		return append(arr, value)
	}
	arr[slot.index] = value
	return arr
}

// ToolCallArgumentsStream accumulates raw argument text for one streaming tool call,
// providing incremental partial argument objects and a single full parse at finish.
type ToolCallArgumentsStream struct {
	text       string
	parser     *StreamingJsonParser
	hasLastSeq bool
	lastSeq    uint64
	arguments  *jsonx.Object
}

// NewToolCallArgumentsStream creates a ready-to-use ToolCallArgumentsStream.
func NewToolCallArgumentsStream() *ToolCallArgumentsStream {
	return &ToolCallArgumentsStream{
		parser:    NewStreamingJsonParser(),
		arguments: jsonx.NewObject(),
	}
}

func (s *ToolCallArgumentsStream) ensureInit() {
	if s.parser == nil {
		s.parser = NewStreamingJsonParser()
	}
	if s.arguments == nil {
		s.arguments = jsonx.NewObject()
	}
}

// Text returns the raw argument text received so far.
func (s *ToolCallArgumentsStream) Text() string {
	return s.text
}

// Append appends delta to the argument text and returns the current partial arguments object.
func (s *ToolCallArgumentsStream) Append(delta string) *jsonx.Object {
	s.ensureInit()
	s.text += delta
	s.parser.Append(delta)
	return s.partial()
}

// Replace accepts a provider's complete argument text. When text extends the text seen
// so far, it returns a pointer to the missing delta; otherwise it resets and returns nil delta.
func (s *ToolCallArgumentsStream) Replace(text string) (*string, *jsonx.Object) {
	s.ensureInit()
	if strings.HasPrefix(text, s.text) {
		delta := text[len(s.text):]
		if delta == "" {
			return &delta, s.partial()
		}
		return &delta, s.Append(delta)
	}
	s.text = ""
	s.parser = NewStreamingJsonParser()
	s.hasLastSeq = false
	return nil, s.Append(text)
}

// Finish parses the complete accumulated text as tool arguments.
func (s *ToolCallArgumentsStream) Finish() (*jsonx.Object, *string) {
	return ParseToolCallArguments(s.text)
}

func (s *ToolCallArgumentsStream) partial() *jsonx.Object {
	snap, ok := s.parser.Snapshot()
	if !ok {
		return s.arguments
	}
	if s.hasLastSeq && s.parser.snapshotSeq == s.lastSeq {
		return s.arguments
	}
	s.hasLastSeq = true
	s.lastSeq = s.parser.snapshotSeq
	if obj, isObj := snap.(*jsonx.Object); isObj && obj != nil {
		s.arguments = obj
	} else {
		s.arguments = jsonx.NewObject()
	}
	return s.arguments
}

func usjIsDelimiter(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', ',', ':', '{', '}', '[', ']', '"':
		return true
	default:
		return false
	}
}

func usjIsHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func usjScalarValue(token string) (any, bool) {
	for _, lit := range usjLiterals {
		if token == lit.name {
			return lit.value, true
		}
	}
	if usjNumberRE.Test(token) {
		return js.ToNumber(token), true
	}
	return nil, false
}

func usjPendingScalarValue(token string) (any, bool) {
	for _, lit := range usjLiterals {
		if strings.HasPrefix(lit.name, token) {
			return lit.value, true
		}
	}
	if usjNumberRE.Test(token) {
		return js.ToNumber(token), true
	}
	if m := usjNumberWithoutExponentDigitsRE.Exec(token, 0); m != nil {
		return js.ToNumber(m.Group(1)), true
	}
	return nil, false
}
