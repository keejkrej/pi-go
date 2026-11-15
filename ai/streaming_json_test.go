package ai_test

import (
	"encoding/base64"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/jsre"
)

type sjRand func() float64

func sjSeededRandom(seed int64) sjRand {
	state := seed
	return func() float64 {
		state = (state*1103515245 + 12345) & 0x7fffffff
		return float64(state) / 0x7fffffff
	}
}

func sjPick[T any](rand sjRand, items []T) T {
	return items[int(math.Floor(rand()*float64(len(items))))]
}

var sjStringPieces = []string{
	"a", "b", "x y", " ", "\n", "\t", "\\", `"`, "/", "é", "😀", "\u0001", "\u001f", "\u2028", "{", "]", ",",
}

func sjRandomString(rand sjRand) string {
	var b strings.Builder
	length := int(math.Floor(rand() * 6))
	for range length {
		b.WriteString(sjPick(rand, sjStringPieces))
	}
	return b.String()
}

func sjRandomScalar(rand sjRand) any {
	switch int(math.Floor(rand() * 10)) {
	case 0:
		return sjRandomString(rand)
	case 1:
		return math.Floor(rand()*2000) - 1000
	case 2:
		return rand() * 100
	case 3:
		return 1e21
	case 4:
		return -5e-8
	case 5:
		return math.Copysign(0, -1)
	case 6:
		return 0.0
	case 7:
		return true
	case 8:
		return false
	default:
		return nil
	}
}

func sjRandomValue(rand sjRand, depth int) any {
	roll := rand()
	if depth > 3 || roll < 0.3 {
		return sjRandomScalar(rand)
	}
	if roll < 0.65 {
		obj := jsonx.NewObject()
		count := int(math.Floor(rand() * 4))
		for range count {
			obj.Set(sjRandomString(rand), sjRandomValue(rand, depth+1))
		}
		return obj
	}
	n := int(math.Floor(rand() * 4))
	arr := make([]any, n)
	for i := range n {
		arr[i] = sjRandomValue(rand, depth+1)
	}
	return arr
}

func sjRender(value any, rand sjRand, spaced bool) string {
	space := func() string {
		if !spaced {
			return ""
		}
		return sjPick(rand, []string{"", "", " ", "\n  ", "\t", "\r\n"})
	}
	switch v := value.(type) {
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = sjRender(item, rand, spaced)
		}
		return "[" + space() + strings.Join(parts, space()+","+space()) + space() + "]"
	case *jsonx.Object:
		parts := make([]string, 0, v.Len())
		for k, item := range v.All() {
			ks, _ := jsonx.Stringify(k)
			parts = append(parts, ks+space()+":"+space()+sjRender(item, rand, spaced))
		}
		return "{" + space() + strings.Join(parts, space()+","+space()) + space() + "}"
	case float64:
		if v == 0 && math.Signbit(v) {
			return "-0"
		}
		s, _ := jsonx.Stringify(v)
		return s
	default:
		s, _ := jsonx.Stringify(v)
		return s
	}
}

func sjRandomDocuments(seed int64, count int, spaced bool) []string {
	rand := sjSeededRandom(seed)
	docs := make([]string, count)
	for i := range count {
		var val any
		if rand() < 0.8 {
			obj := jsonx.NewObject()
			obj.Set("path", sjRandomString(rand))
			obj.Set("args", sjRandomValue(rand, 0))
			val = obj
		} else {
			val = sjRandomValue(rand, 0)
		}
		docs[i] = sjRender(val, rand, spaced)
	}
	return docs
}

var (
	sjNumberRE         = jsre.MustCompile(`^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`, "")
	sjNumberExpPrefix  = jsre.MustCompile(`^(-?(?:0|[1-9]\d*)(?:\.\d+)?)[eE][+-]?$`, "")
	sjHexDigitRE       = jsre.MustCompile(`[0-9a-fA-F]`, "")
	sjSimpleEscapesMap = map[byte]string{'"': `"`, '\\': `\`, '/': "/", 'b': "\b", 'f': "\f", 'n': "\n", 'r': "\r", 't': "\t"}
)

type sjRead struct {
	value    any
	complete bool
	end      int
}

func sjSkipSpace(text string, start int) int {
	i := start
	for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r') {
		i++
	}
	return i
}

func sjReferenceSnapshot(text string, pendingScalars bool) (any, bool) {
	r, ok := sjReadValue(text, sjSkipSpace(text, 0), pendingScalars)
	if !ok {
		return nil, false
	}
	return r.value, true
}

func sjReadValue(text string, start int, pendingScalars bool) (sjRead, bool) {
	if start >= len(text) {
		return sjRead{}, false
	}
	switch text[start] {
	case '"':
		return sjReadString(text, start), true
	case '{':
		return sjReadObject(text, start, pendingScalars), true
	case '[':
		return sjReadArray(text, start, pendingScalars), true
	default:
		return sjReadScalar(text, start, pendingScalars)
	}
}

func sjTrailingHighSurrogate(s string) (uint16, bool) {
	if len(s) < 3 {
		return 0, false
	}
	b0, b1, b2 := s[len(s)-3], s[len(s)-2], s[len(s)-1]
	if b0 != 0xED || b1 < 0xA0 || b1 > 0xAF || b2 < 0x80 || b2 > 0xBF {
		return 0, false
	}
	return (uint16(b0&0x0F) << 12) | (uint16(b1&0x3F) << 6) | uint16(b2&0x3F), true
}

func sjEncodeWTF16(u uint16) string {
	if u >= 0xD800 && u <= 0xDFFF {
		return string([]byte{
			byte(0xE0 | (u >> 12)),
			byte(0x80 | ((u >> 6) & 0x3F)),
			byte(0x80 | (u & 0x3F)),
		})
	}
	return string(rune(u))
}

func sjReadString(text string, start int) sjRead {
	var b strings.Builder
	i := start + 1
	for i < len(text) {
		ch := text[i]
		if ch == '"' {
			return sjRead{value: b.String(), complete: true, end: i + 1}
		}
		if ch != '\\' {
			b.WriteByte(ch)
			i++
			continue
		}
		if i+1 >= len(text) {
			return sjRead{value: b.String(), complete: false, end: len(text)}
		}
		next := text[i+1]
		if rep, ok := sjSimpleEscapesMap[next]; ok {
			b.WriteString(rep)
			i += 2
			continue
		}
		if next == 'u' {
			end := i + 2
			for end < i+6 && end < len(text) && sjHexDigitRE.Test(text[end:end+1]) {
				end++
			}
			if end == i+6 {
				code, _ := strconv.ParseUint(text[i+2:end], 16, 16)
				unit := uint16(code)
				cur := b.String()
				if hi, ok := sjTrailingHighSurrogate(cur); ok && unit >= 0xDC00 && unit <= 0xDFFF {
					b.Reset()
					b.WriteString(cur[:len(cur)-3])
					b.WriteString(string(utf16.DecodeRune(rune(hi), rune(unit))))
				} else {
					b.WriteString(sjEncodeWTF16(unit))
				}
				i = end
				continue
			}
			if end == len(text) {
				return sjRead{value: b.String(), complete: false, end: end}
			}
			b.WriteString(text[i:end])
			i = end
			continue
		}
		b.WriteByte('\\')
		i++
	}
	return sjRead{value: b.String(), complete: false, end: len(text)}
}

func sjReadObject(text string, start int, pendingScalars bool) sjRead {
	members := jsonx.NewObject()
	partial := sjRead{value: members, complete: false, end: len(text)}
	i := sjSkipSpace(text, start+1)
	if i >= len(text) {
		return partial
	}
	if text[i] == '}' {
		return sjRead{value: members, complete: true, end: i + 1}
	}
	for {
		key := sjReadString(text, i)
		if !key.complete {
			return partial
		}
		i = sjSkipSpace(text, key.end)
		if i >= len(text) {
			return partial
		}
		i = sjSkipSpace(text, i+1)
		member, ok := sjReadValue(text, i, pendingScalars)
		if !ok {
			return partial
		}
		members.Set(key.value.(string), member.value)
		if !member.complete {
			return partial
		}
		i = sjSkipSpace(text, member.end)
		if i >= len(text) {
			return partial
		}
		if text[i] == '}' {
			return sjRead{value: members, complete: true, end: i + 1}
		}
		i = sjSkipSpace(text, i+1)
		if i >= len(text) {
			return partial
		}
	}
}

func sjReadArray(text string, start int, pendingScalars bool) sjRead {
	elements := []any{}
	partial := sjRead{value: elements, complete: false, end: len(text)}
	i := sjSkipSpace(text, start+1)
	if i >= len(text) {
		return partial
	}
	if text[i] == ']' {
		return sjRead{value: elements, complete: true, end: i + 1}
	}
	for {
		elem, ok := sjReadValue(text, i, pendingScalars)
		if !ok {
			return partial
		}
		elements = append(elements, elem.value)
		partial.value = elements
		if !elem.complete {
			return partial
		}
		i = sjSkipSpace(text, elem.end)
		if i >= len(text) {
			return partial
		}
		if text[i] == ']' {
			return sjRead{value: elements, complete: true, end: i + 1}
		}
		i = sjSkipSpace(text, i+1)
		if i >= len(text) {
			return partial
		}
	}
}

func sjReadScalar(text string, start int, pendingScalars bool) (sjRead, bool) {
	end := start
	for end < len(text) && !strings.ContainsRune(" \t\n\r,:{}[]\"", rune(text[end])) {
		end++
	}
	token := text[start:end]
	if end < len(text) {
		val, err := jsonx.ParseString(token)
		if err != nil {
			return sjRead{}, false
		}
		return sjRead{value: val, complete: true, end: end}, true
	}
	if !pendingScalars {
		return sjRead{}, false
	}
	for _, lit := range []struct {
		name string
		val  any
	}{{"true", true}, {"false", false}, {"null", nil}} {
		if strings.HasPrefix(lit.name, token) {
			return sjRead{value: lit.val, complete: false, end: end}, true
		}
	}
	if sjNumberRE.Test(token) {
		return sjRead{value: js.ToNumber(token), complete: false, end: end}, true
	}
	if m := sjNumberExpPrefix.Exec(token, 0); m != nil {
		return sjRead{value: js.ToNumber(m.Group(1)), complete: false, end: end}, true
	}
	return sjRead{}, false
}

func sjCloneValue(v any) any {
	switch x := v.(type) {
	case *jsonx.Object:
		if x == nil {
			return nil
		}
		cp := jsonx.NewObject()
		for k, val := range x.All() {
			cp.Set(k, sjCloneValue(val))
		}
		return cp
	case []any:
		cp := make([]any, len(x))
		for i, item := range x {
			cp[i] = sjCloneValue(item)
		}
		return cp
	default:
		return v
	}
}

func sjValueEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch x := a.(type) {
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && (x == y || (math.IsNaN(x) && math.IsNaN(y)))
	case *jsonx.Object:
		y, ok := b.(*jsonx.Object)
		if !ok || x == nil || y == nil || x.Len() != y.Len() {
			return false
		}
		for k, xv := range x.All() {
			yv, exists := y.Get(k)
			if !exists || !sjValueEqual(xv, yv) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sjValueEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

type sjStep struct {
	read        string
	snapshot    any
	hasSnapshot bool
	copy        any
	hasCopy     bool
}

func sjSplitChunk(text string, index int, size int) int {
	next := min(index+size, len(text))
	return next
}

func sjStream(text string, chunkSize int, rand sjRand) []sjStep {
	parser := ai.NewStreamingJsonParser()
	var steps []sjStep
	for i := 0; i < len(text); {
		size := chunkSize
		if rand != nil {
			size = 1 + int(math.Floor(rand()*12))
		}
		next := sjSplitChunk(text, i, size)
		parser.Append(text[i:next])
		i = next
		snap, ok := parser.Snapshot()
		steps = append(steps, sjStep{
			read:        text[:i],
			snapshot:    snap,
			hasSnapshot: ok,
			copy:        sjCloneValue(snap),
			hasCopy:     ok,
		})
	}
	return steps
}

func sjFinalSnapshot(text string, chunkSize int) (any, bool) {
	if chunkSize <= 0 {
		chunkSize = max(len(text), 1)
	}
	steps := sjStream(text, chunkSize, nil)
	if len(steps) == 0 {
		return nil, false
	}
	last := steps[len(steps)-1]
	return last.snapshot, last.hasSnapshot
}

func sjRead32(text string) int64 {
	var hash int32 = 7
	for i := 0; i < len(text); i++ {
		hash = hash*31 + int32(text[i])
	}
	if hash < 0 {
		hash = -hash
	}
	return int64(hash) + 1
}

func sjExpectPlainJson(t *testing.T, v any, ok bool, label string) {
	t.Helper()
	if !ok || v == nil {
		return
	}
	switch x := v.(type) {
	case bool, string:
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			t.Fatalf("%s: non-finite number %v", label, x)
		}
	case []any:
		for _, item := range x {
			sjExpectPlainJson(t, item, true, label)
		}
	case *jsonx.Object:
		if x != nil {
			for _, item := range x.All() {
				sjExpectPlainJson(t, item, true, label)
			}
		}
	default:
		t.Fatalf("%s: unexpected type %T", label, v)
	}
}

type sjMirror struct {
	value    any
	hasValue bool
}

func (m *sjMirror) apply(changes []ai.JsonChange) {
	for _, ch := range changes {
		path := ch.ChangePath()
		if len(path) == 0 {
			switch c := ch.(type) {
			case *ai.JsonChangeSet:
				m.value = sjCloneValue(c.Value)
				m.hasValue = true
			case *ai.JsonChangeAppend:
				prev, _ := m.value.(string)
				m.value = prev + c.Text
				m.hasValue = true
			}
			continue
		}
		parent := m.value
		for _, seg := range path[:len(path)-1] {
			switch p := parent.(type) {
			case *jsonx.Object:
				parent, _ = p.Get(seg.(string))
			case []any:
				parent = p[seg.(int)]
			}
		}
		key := path[len(path)-1]
		switch c := ch.(type) {
		case *ai.JsonChangeSet:
			val := sjCloneValue(c.Value)
			switch p := parent.(type) {
			case *jsonx.Object:
				p.Set(key.(string), val)
			case []any:
				idx := key.(int)
				if idx == len(p) {
					// Append to parent container slot when setting a new array element.
					sjSetArrayElem(&m.value, path, val)
				} else {
					p[idx] = val
				}
			}
		case *ai.JsonChangeAppend:
			switch p := parent.(type) {
			case *jsonx.Object:
				cur, _ := p.Get(key.(string))
				prev, _ := cur.(string)
				p.Set(key.(string), prev+c.Text)
			case []any:
				idx := key.(int)
				prev, _ := p[idx].(string)
				p[idx] = prev + c.Text
			}
		}
	}
}

func sjSetArrayElem(root *any, path ai.JsonPath, val any) {
	if len(path) == 1 {
		arr := (*root).([]any)
		arr = append(arr, val)
		*root = arr
		return
	}
	parent := *root
	for _, seg := range path[:len(path)-2] {
		switch p := parent.(type) {
		case *jsonx.Object:
			parent, _ = p.Get(seg.(string))
		case []any:
			parent = p[seg.(int)]
		}
	}
	arrKey := path[len(path)-2]
	switch p := parent.(type) {
	case *jsonx.Object:
		cur, _ := p.Get(arrKey.(string))
		arr := append(cur.([]any), val)
		p.Set(arrKey.(string), arr)
	case []any:
		idx := arrKey.(int)
		p[idx] = append(p[idx].([]any), val)
	}
}

func TestStreamingJsonParserDifferentialFuzzing(t *testing.T) {
	t.Run("matches reference after every piece at multiple chunk sizes with whitespace", func(t *testing.T) {
		for _, text := range sjRandomDocuments(1, 250, true) {
			for _, size := range []int{1, 2, 3, 7, 0} {
				var rand sjRand
				if size == 0 {
					rand = sjSeededRandom(sjRead32(text))
				}
				for _, step := range sjStream(text, size, rand) {
					want, wantOK := sjReferenceSnapshot(step.read, true)
					if step.hasSnapshot != wantOK || (wantOK && !sjValueEqual(step.snapshot, want)) {
						t.Fatalf("mismatch for %q (size %d): got (%v, %#v), want (%v, %#v)", step.read, size, step.hasSnapshot, step.snapshot, wantOK, want)
					}
				}
			}
		}
	})

	t.Run("matches ParseStreamingJson at object prefixes of compact documents", func(t *testing.T) {
		for _, text := range sjRandomDocuments(2, 400, false) {
			if !strings.HasPrefix(text, "{") {
				continue
			}
			p := ai.NewStreamingJsonParser()
			for i := 0; i < len(text); {
				r, w := utf8.DecodeRuneInString(text[i:])
				if w <= 0 {
					w = 1
				}
				i += w
				p.Append(text[i-w : i])
				read := text[:i]
				if unicode.IsSpace(r) {
					continue
				}
				expected := ai.ParseStreamingJson(read)
				snap, ok := p.Snapshot()
				if !ok {
					snap = jsonx.NewObject()
				}
				if !sjValueEqual(snap, expected) {
					t.Fatalf("ParseStreamingJson mismatch for %q: got %#v, want %#v", read, snap, expected)
				}
			}
		}
	})

	t.Run("reads complete documents identically to jsonx.ParseString", func(t *testing.T) {
		docs := append(sjRandomDocuments(3, 300, true), sjRandomDocuments(4, 300, false)...)
		for _, text := range docs {
			expected, err := jsonx.ParseString(text)
			if err != nil {
				t.Fatalf("parse %q: %v", text, err)
			}
			for _, size := range []int{1, 2, 3, 7, 5} {
				got, ok := sjFinalSnapshot(text, size)
				if !ok || !sjValueEqual(got, expected) {
					t.Fatalf("doc %q (size %d): got (%v, %#v), want %#v", text, size, ok, got, expected)
				}
			}
		}
	})

	t.Run("never mutates previously returned snapshots", func(t *testing.T) {
		for _, text := range sjRandomDocuments(5, 150, true) {
			for _, step := range sjStream(text, 0, sjSeededRandom(sjRead32(text))) {
				if step.hasSnapshot != step.hasCopy || (step.hasSnapshot && !sjValueEqual(step.snapshot, step.copy)) {
					t.Fatalf("snapshot mutated for %q", text)
				}
			}
		}
	})

	t.Run("repairs raw control chars and invalid escapes consistently", func(t *testing.T) {
		rand := sjSeededRandom(6)
		pieces := []string{"\n", "\t", "\u0000", "\\d", "\\x41", "\\u12", "\\uZZZZ", "\\u00e9", "\\\\", "\\q", "a", " ", "é"}
		for round := range 200 {
			var content strings.Builder
			for range 6 {
				content.WriteString(sjPick(rand, pieces))
			}
			text := fmt.Sprintf(`{"path":"p","content":"%s","n":%d}`, content.String(), round)
			gotFinal, ok := sjFinalSnapshot(text, 1)
			wantRepaired := ai.ParseStreamingJson(text)
			if !ok || !sjValueEqual(gotFinal, wantRepaired) {
				t.Fatalf("round %d %q: got %#v, want %#v", round, text, gotFinal, wantRepaired)
			}
			for _, step := range sjStream(text, 1, nil) {
				wantRef, wantOK := sjReferenceSnapshot(step.read, true)
				if step.hasSnapshot != wantOK || (wantOK && !sjValueEqual(step.snapshot, wantRef)) {
					t.Fatalf("round %d prefix %q: got (%v, %#v), want (%v, %#v)", round, step.read, step.hasSnapshot, step.snapshot, wantOK, wantRef)
				}
			}
		}
	})
}

func TestStreamingJsonParserStrings(t *testing.T) {
	t.Run("holds escapes back until complete at every byte split", func(t *testing.T) {
		cases := [][2]string{
			{`{"a":"x\u00e9y"}`, "xéy"},
			{`{"a":"x\ny"}`, "x\ny"},
			{`{"a":"x\\y"}`, `x\y`},
			{`{"a":"x\"y"}`, `x"y`},
			{`{"a":"x\/y"}`, "x/y"},
		}
		for _, tc := range cases {
			text, wantVal := tc[0], tc[1]
			for split := 0; split <= len(text); split++ {
				p := ai.NewStreamingJsonParser()
				p.Append(text[:split])
				got, ok := p.Snapshot()
				wantRef, wantOK := sjReferenceSnapshot(text[:split], true)
				if ok != wantOK || (wantOK && !sjValueEqual(got, wantRef)) {
					t.Fatalf("split %d of %q: got (%v, %#v), want (%v, %#v)", split, text, ok, got, wantOK, wantRef)
				}
				p.Append(text[split:])
				gotEnd, _ := p.Snapshot()
				wantObj := jsonx.NewObject()
				wantObj.Set("a", wantVal)
				if !sjValueEqual(gotEnd, wantObj) {
					t.Fatalf("end of %q: got %#v", text, gotEnd)
				}
			}
		}
	})

	t.Run("joins surrogate pairs split across pieces", func(t *testing.T) {
		for _, text := range []string{`{"a":"\ud83d\ude00!"}`, `{"a":"😀!"}`} {
			for split := 0; split <= len(text); split++ {
				p := ai.NewStreamingJsonParser()
				p.Append(text[:split])
				p.Snapshot()
				p.Append(text[split:])
				got, ok := p.Snapshot()
				want := jsonx.NewObject()
				want.Set("a", "😀!")
				if !ok || !sjValueEqual(got, want) {
					t.Fatalf("split %d of %q: got %#v, want %#v", split, text, got, want)
				}
			}
		}
		p := ai.NewStreamingJsonParser()
		p.Append(`{"a":"\ud83d`)
		got, _ := p.Snapshot()
		obj := got.(*jsonx.Object)
		val, _ := obj.Get("a")
		if s, ok := val.(string); !ok || len(s) != 3 {
			t.Fatalf("expected lonely high surrogate WTF-8 sequence, got %q", val)
		}
	})

	t.Run("keeps trailing whitespace, raw controls, and invalid u-escapes", func(t *testing.T) {
		p := ai.NewStreamingJsonParser()
		p.Append("{\"code\":\"x\t\t  ")
		got, _ := p.Snapshot()
		if v, _ := got.(*jsonx.Object).Get("code"); v != "x\t\t  " {
			t.Fatalf("got %q", v)
		}

		p2 := ai.NewStreamingJsonParser()
		p2.Append("{\"a\":\"x\ny \\d \\u12\"")
		got2, _ := p2.Snapshot()
		if v, _ := got2.(*jsonx.Object).Get("a"); v != "x\ny \\d \\u12" {
			t.Fatalf("got %q", v)
		}

		got3, _ := sjFinalSnapshot(`{"path":"a.tex","content":"\usepackage{x}","mode":"w"}`, 1)
		if v, _ := got3.(*jsonx.Object).Get("content"); v != `\usepackage{x}` {
			t.Fatalf("got %q", v)
		}

		got4, _ := sjFinalSnapshot(`{"text":"caf\u00e9 \url"}`, 3)
		if v, _ := got4.(*jsonx.Object).Get("text"); v != `café \url` {
			t.Fatalf("got %q", v)
		}

		p5 := ai.NewStreamingJsonParser()
		p5.Append(`{"text":"ab\u00`)
		got5, _ := p5.Snapshot()
		if v, _ := got5.(*jsonx.Object).Get("text"); v != "ab" {
			t.Fatalf("got %q", v)
		}
	})

	t.Run("keeps duplicate key position matching jsonx.ParseString", func(t *testing.T) {
		text := `{"a":1,"b":2,"a":"three"}`
		got, ok := sjFinalSnapshot(text, 1)
		want, err := jsonx.ParseString(text)
		if err != nil || !ok || !sjValueEqual(got, want) {
			t.Fatalf("got %#v, want %#v (err %v)", got, want, err)
		}
		if !slices.Equal(got.(*jsonx.Object).Keys(), want.(*jsonx.Object).Keys()) {
			t.Fatalf("keys = %v, want %v", got.(*jsonx.Object).Keys(), want.(*jsonx.Object).Keys())
		}
	})
}

func TestStreamingJsonParserPortedVectorsAndSuite(t *testing.T) {
	type vec struct {
		input string
		want  any
		has   bool
	}
	mkObj := func(kvs ...any) *jsonx.Object {
		o := jsonx.NewObject()
		for i := 0; i < len(kvs); i += 2 {
			o.Set(kvs[i].(string), kvs[i+1])
		}
		return o
	}
	vectors := []vec{
		{`"`, "", true},
		{`["`, []any{""}, true},
		{`{"": "`, mkObj("", ""), true},
		{`n`, nil, true},
		{`t`, true, true},
		{`f`, false, true},
		{`0`, 0.0, true},
		{`-1.25e+4`, -1.25e4, true},
		{`-1.25e+`, -1.25, true},
		{`-1.25e`, -1.25, true},
		{`" \x12`, ` \x12`, true},
		{`I`, nil, false},
		{`-I`, nil, false},
		{`N`, nil, false},
		{``, nil, false},
		{`nul`, nil, true},
		{`fals`, false, true},
		{`12.2`, 12.2, true},
		{`-12`, -12.0, true},
		{`-`, nil, false},
		{`2.5e`, 2.5, true},
		{`2.5e-`, 2.5, true},
		{`2.5e3`, 2.5e3, true},
		{`-2.5e3`, -2.5e3, true},
		{`12.`, nil, false},
		{`12.e`, nil, false},
		{`"abc`, "abc", true},
		{`"value with \"quoted\" text and \\ escape`, `value with "quoted" text and \ escape`, true},
		{`"value with \`, "value with ", true},
		{`"\u`, "", true},
		{`"\u12`, "", true},
		{`{"a":"\u12`, mkObj("a", ""), true},
		{"\"value with unicode \u003C\"", "value with unicode <", true},
		{`[`, []any{}, true},
		{`[[1], [2`, []any{[]any{1.0}, []any{2.0}}, true},
		{`[["1"], ["2`, []any{[]any{"1"}, []any{"2"}}, true},
		{`[[false], [nu`, []any{[]any{false}, []any{nil}}, true},
		{`[1, `, []any{1.0}, true},
		{`{"key":`, mkObj(), true},
		{`{"a": {"b": 1}, "c": {"d": 2`, mkObj("a", mkObj("b", 1.0), "c", mkObj("d", 2.0)), true},
		{`{"k1": 1, "k2":`, mkObj("k1", 1.0), true},
		{`{"key": "value"  `, mkObj("key", "value"), true},
		{`[1, [2, 3, [`, []any{1.0, []any{2.0, 3.0, []any{}}}, true},
		{`{"a": {"b": ["c", {"d": "e",`, mkObj("a", mkObj("b", []any{"c", mkObj("d", "e")})), true},
	}
	for _, tc := range vectors {
		for _, size := range []int{max(len(tc.input), 1), 1, 2} {
			got, ok := sjFinalSnapshot(tc.input, size)
			if ok != tc.has || (ok && !sjValueEqual(got, tc.want)) {
				t.Fatalf("input %q (size %d): got (%v, %#v), want (%v, %#v)", tc.input, size, ok, got, tc.has, tc.want)
			}
		}
	}

	t.Run("matches jsonx.ParseString across JSONTestSuite and never produces invalid JSON values", func(t *testing.T) {
		for name, b64 := range sjJsonTestSuiteFiles {
			rawBytes, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
			text := strings.ToValidUTF8(string(rawBytes), "\uFFFD")
			expected, parseErr := jsonx.ParseString(text)
			for _, size := range []int{max(len(text), 1), 1, 3} {
				snap, ok := sjFinalSnapshot(text, size)
				if parseErr == nil {
					if !ok || !sjValueEqual(snap, expected) {
						t.Fatalf("%s (size %d): got (%v, %#v), want %#v", name, size, ok, snap, expected)
					}
				} else {
					sjExpectPlainJson(t, snap, ok, name)
				}
			}
		}
	})
}

func TestStreamingJsonParserMalformedAndChanges(t *testing.T) {
	malformed := []string{
		`{"a":1 "b":2}`,
		`{"a" 1, "b": 2}`,
		`{a:1, 'b': 2}`,
		`{"a":1,,"b":2,}`,
		`[1 2, , 3,]`,
		`{"a":[1,2}, "b":3]`,
		`]]]}}}{`,
		`{"a":NaN,"b":Infinity,"c":-Infinity,"d":undefined,"e":1}`,
		`{"a":01,"b":1.,"c":.5,"d":+1,"e":0x10,"f":1e}`,
		`{"a":"x"} trailing garbage {"b":2}`,
		`{"a": tru, "b": nulll, "c": falsey}`,
		`{"a":{"b":{"c":`,
		`{"a\": 1}`,
		`"unterminated \`,
		"// comment\n{\"a\":1}",
		"\u0000{\"a\":\u00031}",
		`{"a":"x"}}}]]]`,
	}
	for _, text := range malformed {
		wantFinal, wantOK := sjFinalSnapshot(text, len(text))
		for _, size := range []int{1, 2, 3, 7, 0} {
			var rand sjRand
			if size == 0 {
				rand = sjSeededRandom(sjRead32(text))
			}
			steps := sjStream(text, size, rand)
			for _, st := range steps {
				sjExpectPlainJson(t, st.snapshot, st.hasSnapshot, text)
				if st.hasSnapshot != st.hasCopy || (st.hasSnapshot && !sjValueEqual(st.snapshot, st.copy)) {
					t.Fatalf("snapshot mutated for %q", text)
				}
			}
			last := steps[len(steps)-1]
			if last.hasSnapshot != wantOK || (wantOK && !sjValueEqual(last.snapshot, wantFinal)) {
				t.Fatalf("%q (size %d): final mismatch", text, size)
			}
		}
	}

	t.Run("tolerates missing commas/colons and drops values in invalid positions", func(t *testing.T) {
		got, _ := sjFinalSnapshot(`{"a":1 "b":2}`, 0)
		want := jsonx.NewObject()
		want.Set("a", 1.0)
		want.Set("b", 2.0)
		if !sjValueEqual(got, want) {
			t.Fatalf("got %#v", got)
		}
		got2, _ := sjFinalSnapshot(`[1 2, , 3,]`, 0)
		if !sjValueEqual(got2, []any{1.0, 2.0, 3.0}) {
			t.Fatalf("got %#v", got2)
		}
		got3, _ := sjFinalSnapshot(`{"a":"x"} trailing garbage {"b":2}`, 0)
		want3 := jsonx.NewObject()
		want3.Set("a", "x")
		if !sjValueEqual(got3, want3) {
			t.Fatalf("got %#v", got3)
		}
	})

	t.Run("mirror stays in sync with reference snapshot and final ParseString", func(t *testing.T) {
		for _, text := range sjRandomDocuments(9, 150, true) {
			for _, size := range []int{1, 2, 3, 7} {
				p := ai.NewStreamingJsonParser()
				m := &sjMirror{}
				for i := 0; i < len(text); {
					next := min(i+size, len(text))
					m.apply(p.Append(text[i:next]))
					i = next
					want, wantOK := sjReferenceSnapshot(text[:i], false)
					if m.hasValue != wantOK || (wantOK && !sjValueEqual(m.value, want)) {
						t.Fatalf("mirror prefix %q (size %d): got (%v, %#v), want (%v, %#v)", text[:i], size, m.hasValue, m.value, wantOK, want)
					}
				}
				m.apply(p.Append(" "))
				wantEnd, _ := jsonx.ParseString(text)
				if !sjValueEqual(m.value, wantEnd) {
					t.Fatalf("mirror end %q: got %#v, want %#v", text, m.value, wantEnd)
				}
			}
		}
	})

	t.Run("survives random corruption and keeps mirror equal to parser after trailing space", func(t *testing.T) {
		rand := sjSeededRandom(7)
		noise := []string{`"`, `\`, "{", "}", "[", "]", ",", ":", "x", "1", "-", "\n", "t"}
		for _, text := range sjRandomDocuments(8, 250, true) {
			runes := []rune(text)
			for range 3 {
				at := int(math.Floor(rand() * float64(len(runes)+1)))
				if rand() < 0.5 && len(runes) > 0 {
					if at >= len(runes) {
						at = len(runes) - 1
					}
					runes = append(runes[:at], runes[at+1:]...)
				} else {
					ins := []rune(sjPick(rand, noise))
					runes = append(runes[:at], append(ins, runes[at:]...)...)
				}
			}
			corrupted := string(runes)
			p := ai.NewStreamingJsonParser()
			m := &sjMirror{}
			for i := 0; i < len(corrupted); i += 4 {
				next := min(i+4, len(corrupted))
				m.apply(p.Append(corrupted[i:next]))
			}
			m.apply(p.Append(" "))
			snap, ok := p.Snapshot()
			if m.hasValue != ok || (ok && !sjValueEqual(m.value, snap)) {
				t.Fatalf("corrupted %q: mirror=(%v,%#v) snap=(%v,%#v)", corrupted, m.hasValue, m.value, ok, snap)
			}
		}
	})

	t.Run("reports deep > 32 containers once whole when closed", func(t *testing.T) {
		depth := 40
		text := strings.Repeat(`{"a":`, depth) + `"deep"` + strings.Repeat("}", depth)
		p := ai.NewStreamingJsonParser()
		m := &sjMirror{}
		for i := 0; i < len(text); i += 3 {
			next := min(i+3, len(text))
			m.apply(p.Append(text[i:next]))
		}
		want, _ := jsonx.ParseString(text)
		if !sjValueEqual(m.value, want) {
			t.Fatalf("deep mismatch")
		}
		changes := ai.NewStreamingJsonParser().Append(text)
		if len(changes) != 34 {
			t.Fatalf("changes len = %d, want 34", len(changes))
		}
		last := changes[len(changes)-1].(*ai.JsonChangeSet)
		if len(last.Path) != 32 {
			t.Fatalf("last path len = %d, want 32", len(last.Path))
		}
		sub, _ := jsonx.ParseString(text[32*5 : len(text)-32])
		if !sjValueEqual(last.Value, sub) {
			t.Fatalf("last value mismatch")
		}
	})

	t.Run("reads deep nesting non-recursively in linear time", func(t *testing.T) {
		depth := 50_000
		text := strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth)
		started := time.Now()
		p := ai.NewStreamingJsonParser()
		for i := 0; i < len(text); i += 7 {
			next := min(i+7, len(text))
			p.Append(text[i:next])
			p.Snapshot()
		}
		val, ok := p.Snapshot()
		if !ok {
			t.Fatal("expected snapshot")
		}
		for range depth {
			val = val.([]any)[0]
		}
		if val != 1.0 {
			t.Fatalf("got %v, want 1", val)
		}
		if time.Since(started) > 10*time.Second {
			t.Fatalf("took too long: %s", time.Since(started))
		}
	})
}

func TestToolCallArgumentsStream(t *testing.T) {
	t.Run("shows partial arguments as object and finishes with full parse", func(t *testing.T) {
		s := ai.NewToolCallArgumentsStream()
		if got := s.Append("  "); got.Len() != 0 {
			t.Fatalf("expected empty object, got %v", got)
		}
		got := s.Append(`{"path":"a.ts","content":"x`)
		want := jsonx.NewObject()
		want.Set("path", "a.ts")
		want.Set("content", "x")
		if !sjValueEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
		before := s.Append("y")
		if s.Append("") != before {
			t.Fatal("expected empty append to return same cached object pointer")
		}
		if s.Text() != `  {"path":"a.ts","content":"xy` {
			t.Fatalf("Text() = %q", s.Text())
		}
		args, errPtr := s.Finish()
		wantArgs, wantErr := ai.ParseToolCallArguments(`  {"path":"a.ts","content":"xy`)
		if !sjValueEqual(args, wantArgs) || (errPtr == nil) != (wantErr == nil) {
			t.Fatalf("Finish mismatch")
		}
		s.Append(`"}`)
		args2, errPtr2 := s.Finish()
		want2 := jsonx.NewObject()
		want2.Set("path", "a.ts")
		want2.Set("content", "xy")
		if errPtr2 != nil || !sjValueEqual(args2, want2) {
			t.Fatalf("got (%#v, %v)", args2, errPtr2)
		}
	})

	t.Run("shows non-object roots as empty partial arguments and finishes with error", func(t *testing.T) {
		s := ai.NewToolCallArgumentsStream()
		if got := s.Append("[1,2"); got.Len() != 0 {
			t.Fatalf("expected empty object, got %v", got)
		}
		s.Append("]")
		_, errPtr := s.Finish()
		if errPtr == nil || !strings.HasPrefix(*errPtr, "Tool arguments must be a JSON object") {
			t.Fatalf("expected object error, got %v", errPtr)
		}
	})

	t.Run("replaces text and returns delta only when extending prefix", func(t *testing.T) {
		s := ai.NewToolCallArgumentsStream()
		s.Append(`{"a":"x`)
		delta, args := s.Replace(`{"a":"xyz"}`)
		if delta == nil || *delta != `yz"}` {
			t.Fatalf("delta = %v, want yz\"}", delta)
		}
		if v, _ := args.Get("a"); v != "xyz" {
			t.Fatalf("args = %#v", args)
		}
		delta2, args2 := s.Replace(`{"b":2}`)
		if delta2 != nil {
			t.Fatalf("expected nil delta, got %q", *delta2)
		}
		if v, _ := args2.Get("b"); v != 2.0 {
			t.Fatalf("args2 = %#v", args2)
		}
		if s.Text() != `{"b":2}` {
			t.Fatalf("Text() = %q", s.Text())
		}
	})
}
