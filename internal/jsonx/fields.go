package jsonx

import (
	"cmp"
	"encoding"
	"encoding/json"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// The struct field rules below follow encoding/json (Go 1.27) so that struct tags mean the
// same thing to jsonx and to encoding/json.

var (
	objectType          = reflect.TypeFor[Object]()
	objectPtrType       = reflect.TypeFor[*Object]()
	anySliceType        = reflect.TypeFor[[]any]()
	timeType            = reflect.TypeFor[time.Time]()
	numberType          = reflect.TypeFor[json.Number]()
	rawMessageType      = reflect.TypeFor[json.RawMessage]()
	bigIntType          = reflect.TypeFor[big.Int]()
	marshalerType       = reflect.TypeFor[json.Marshaler]()
	unmarshalerType     = reflect.TypeFor[json.Unmarshaler]()
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	optEncoderType      = reflect.TypeFor[optEncoder]()
	isZeroerType        = reflect.TypeFor[isZeroer]()
)

type isZeroer interface{ IsZero() bool }

// field is one serializable struct field.
type field struct {
	name      string
	nameBytes []byte
	tag       bool
	index     []int
	typ       reflect.Type
	omitEmpty bool
	omitZero  bool
	quoted    bool
	isZero    func(reflect.Value) bool
}

type structFields struct {
	list         []field
	byExactName  map[string]*field
	byFoldedName map[string]*field
}

var fieldCache sync.Map // map[reflect.Type]*structFields

func cachedTypeFields(t reflect.Type) *structFields {
	if f, ok := fieldCache.Load(t); ok {
		return f.(*structFields)
	}
	f, _ := fieldCache.LoadOrStore(t, typeFields(t))
	return f.(*structFields)
}

func parseTag(tag string) (string, string) {
	name, opts, _ := strings.Cut(tag, ",")
	return name, opts
}

func tagHas(opts, name string) bool {
	for opts != "" {
		var o string
		o, opts, _ = strings.Cut(opts, ",")
		if o == name {
			return true
		}
	}
	return false
}

func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

func typeFields(t reflect.Type) *structFields {
	current := []field{}
	next := []field{{typ: t}}
	var count, nextCount map[reflect.Type]int
	visited := map[reflect.Type]bool{}
	var fields []field

	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}

		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true
			for i := 0; i < f.typ.NumField(); i++ {
				sf := f.typ.Field(i)
				if sf.Anonymous {
					ft := sf.Type
					if ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if !sf.IsExported() && ft.Kind() != reflect.Struct {
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts := parseTag(tag)
				if !isValidTag(name) {
					name = ""
				}
				index := make([]int, len(f.index)+1)
				copy(index, f.index)
				index[len(f.index)] = i

				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				quoted := false
				if tagHas(opts, "string") {
					switch ft.Kind() {
					case reflect.Bool,
						reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
						reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
						reflect.Float32, reflect.Float64,
						reflect.String:
						quoted = true
					}
				}
				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = sf.Name
					}
					fl := field{
						name:      name,
						nameBytes: []byte(name),
						tag:       tagged,
						index:     index,
						typ:       ft,
						omitEmpty: tagHas(opts, "omitempty"),
						omitZero:  tagHas(opts, "omitzero"),
						quoted:    quoted,
					}
					if fl.omitZero {
						fl.isZero = zeroFunc(sf.Type)
					}
					fields = append(fields, fl)
					if count[f.typ] > 1 {
						fields = append(fields, fields[len(fields)-1])
					}
					continue
				}
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, field{name: ft.Name(), index: index, typ: ft})
				}
			}
		}
	}

	slices.SortFunc(fields, func(a, b field) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tag != b.tag {
			if a.tag {
				return -1
			}
			return +1
		}
		return slices.Compare(a.index, b.index)
	})

	out := fields[:0]
	for advance, i := 0, 0; i < len(fields); i += advance {
		fi := fields[i]
		for advance = 1; i+advance < len(fields); advance++ {
			if fields[i+advance].name != fi.name {
				break
			}
		}
		if advance == 1 {
			out = append(out, fi)
			continue
		}
		group := fields[i : i+advance]
		if len(group[0].index) == len(group[1].index) && group[0].tag == group[1].tag {
			continue
		}
		out = append(out, group[0])
	}
	fields = out
	slices.SortFunc(fields, func(a, b field) int { return slices.Compare(a.index, b.index) })

	sf := &structFields{
		list:         fields,
		byExactName:  make(map[string]*field, len(fields)),
		byFoldedName: make(map[string]*field, len(fields)),
	}
	for i := range fields {
		f := &sf.list[i]
		sf.byExactName[f.name] = f
		folded := string(foldName(f.nameBytes))
		if _, ok := sf.byFoldedName[folded]; !ok {
			sf.byFoldedName[folded] = f
		}
	}
	return sf
}

func zeroFunc(t reflect.Type) func(reflect.Value) bool {
	switch {
	case t.Kind() == reflect.Interface && t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.IsNil() ||
				(v.Elem().Kind() == reflect.Pointer && v.Elem().IsNil()) ||
				v.Interface().(isZeroer).IsZero()
		}
	case t.Kind() == reflect.Pointer && t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.IsNil() || v.Interface().(isZeroer).IsZero()
		}
	case t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.Interface().(isZeroer).IsZero()
		}
	case reflect.PointerTo(t).Implements(isZeroerType):
		return func(v reflect.Value) bool {
			if !v.CanAddr() {
				v2 := reflect.New(v.Type()).Elem()
				v2.Set(v)
				v = v2
			}
			return v.Addr().Interface().(isZeroer).IsZero()
		}
	}
	return func(v reflect.Value) bool { return v.IsZero() }
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

// foldName returns a folded form such that foldName(x) == foldName(y) iff
// bytes.EqualFold(x, y), like encoding/json.
func foldName(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		if c := in[i]; c < utf8.RuneSelf {
			if 'a' <= c && c <= 'z' {
				c -= 'a' - 'A'
			}
			out = append(out, c)
			i++
			continue
		}
		r, n := utf8.DecodeRune(in[i:])
		out = utf8.AppendRune(out, foldRune(r))
		i += n
	}
	return out
}

func foldRune(r rune) rune {
	for {
		r2 := unicode.SimpleFold(r)
		if r2 <= r {
			return r2
		}
		r = r2
	}
}

func (sf *structFields) lookup(key string) *field {
	if f, ok := sf.byExactName[key]; ok {
		return f
	}
	return sf.byFoldedName[string(foldName([]byte(key)))]
}
