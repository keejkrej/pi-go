package jsonx

import (
	"encoding"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Decode stores the untyped JSON value v into the Go value out points to. It follows
// encoding/json Unmarshal rules (struct tags, exact-then-case-insensitive field names,
// json.Unmarshaler, encoding.TextUnmarshaler, unknown keys ignored, first error reported
// as *json.UnmarshalTypeError after decoding as much as possible), with these jsonx
// rules on top:
//   - `any` targets receive jsonx values (*Object, not map[string]any);
//   - *Object and `any` targets alias the source (*Object, []any) like a JS reference, so
//     a typed view and the parsed document share nested objects;
//   - Opt[T] targets get the null state for JSON null;
//   - integral float64 values decode into integer fields (JS has only doubles).
//
// v may also hold typed Go values; they are converted with Encode first.
func Decode(v any, out any) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &json.InvalidUnmarshalError{Type: reflect.TypeOf(out)}
	}
	d := decoder{}
	d.value(v, rv)
	return d.err
}

// Unmarshal parses data with JSON.parse rules and decodes the result into out.
func Unmarshal(data []byte, out any) error {
	v, err := Parse(data)
	if err != nil {
		return err
	}
	return Decode(v, out)
}

// Encode converts a typed Go value to the untyped model; it equals
// Parse(Marshal(v)), so the result follows JSON.stringify rules (struct tags, omitted
// functions, NaN to null, time.Time as an ISO string). It returns nil for values JS
// would serialize as undefined.
func Encode(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, string:
		return v, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, nil
		}
		if x == 0 {
			return float64(0), nil
		}
		return x, nil
	}
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	return Parse(b)
}

type decoder struct {
	err        error
	errStruct  reflect.Type
	fieldStack []string
}

func (d *decoder) saveError(err error) {
	if d.err == nil {
		d.err = err
	}
}

func (d *decoder) typeError(value string, t reflect.Type) {
	err := &json.UnmarshalTypeError{Value: value, Type: t}
	if d.errStruct != nil || len(d.fieldStack) > 0 {
		if d.errStruct != nil {
			err.Struct = d.errStruct.Name()
		}
		err.Field = strings.Join(d.fieldStack, ".")
	}
	d.saveError(err)
}

func (d *decoder) value(src any, v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch s := src.(type) {
	case nil:
		d.null(v)
	case bool, float64, string:
		d.literal(src, v, false)
	case []any:
		if s == nil {
			d.null(v)
			return
		}
		d.array(s, v)
	case *Object:
		if s == nil {
			d.null(v)
			return
		}
		d.object(s, v)
	default:
		if isGoNumber(src) {
			d.literal(normalizeNumber(src), v, false)
			return
		}
		enc, err := Encode(src)
		if err != nil {
			d.saveError(err)
			return
		}
		d.value(enc, v)
	}
}

// indirect walks pointers like encoding/json's indirect, allocating as needed, and stops
// at decoders: Opt, json.Unmarshaler, encoding.TextUnmarshaler, or a *Object.
func indirect(v reflect.Value, decodingNull bool) (optDecoder, json.Unmarshaler, encoding.TextUnmarshaler, reflect.Value) {
	v0 := v
	haveAddr := false
	if v.Kind() != reflect.Pointer && v.Type().Name() != "" && v.CanAddr() {
		haveAddr = true
		v = v.Addr()
	}
	for {
		if v.Kind() == reflect.Interface && !v.IsNil() {
			e := v.Elem()
			if e.Kind() == reflect.Pointer && !e.IsNil() && (!decodingNull || e.Elem().Kind() == reflect.Pointer) {
				haveAddr = false
				v = e
				continue
			}
		}
		if v.Kind() != reflect.Pointer {
			break
		}
		if v.Type() == objectPtrType {
			return nil, nil, nil, v
		}
		if decodingNull && v.CanSet() {
			break
		}
		if v.Elem().Kind() == reflect.Interface && v.Elem().Elem().Equal(v) {
			v = v.Elem()
			break
		}
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		if v.Type().NumMethod() > 0 && v.CanInterface() {
			if od, ok := v.Interface().(optDecoder); ok {
				return od, nil, nil, reflect.Value{}
			}
			if u, ok := v.Interface().(json.Unmarshaler); ok {
				return nil, u, nil, reflect.Value{}
			}
			if !decodingNull {
				if u, ok := v.Interface().(encoding.TextUnmarshaler); ok {
					return nil, nil, u, reflect.Value{}
				}
			}
		}
		if haveAddr {
			v = v0
			haveAddr = false
		} else {
			v = v.Elem()
		}
	}
	return nil, nil, nil, v
}

func (d *decoder) callUnmarshaler(u json.Unmarshaler, src any) {
	b, err := marshalDepth(src, "", internalMaxDepth)
	if err != nil {
		d.saveError(err)
		return
	}
	if err := u.UnmarshalJSON(b); err != nil {
		d.saveError(err)
	}
}

func (d *decoder) null(v reflect.Value) {
	od, u, _, pv := indirect(v, true)
	if od != nil {
		od.jsonxDecodeOpt(d, nil)
		return
	}
	if u != nil {
		if err := u.UnmarshalJSON([]byte("null")); err != nil {
			d.saveError(err)
		}
		return
	}
	switch pv.Kind() {
	case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice:
		if pv.CanSet() {
			pv.SetZero()
		}
	}
}

func literalKind(src any) string {
	switch src.(type) {
	case bool:
		return "bool"
	case string:
		return "string"
	default:
		return "number"
	}
}

func (d *decoder) literal(src any, v reflect.Value, fromQuoted bool) {
	od, u, ut, pv := indirect(v, false)
	if od != nil {
		od.jsonxDecodeOpt(d, src)
		return
	}
	if u != nil {
		d.callUnmarshaler(u, src)
		return
	}
	if ut != nil {
		s, ok := src.(string)
		if !ok {
			if fromQuoted {
				d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", formatLiteral(src), v.Type()))
				return
			}
			d.typeError(literalKind(src), v.Type())
			return
		}
		if err := ut.UnmarshalText([]byte(s)); err != nil {
			d.saveError(err)
		}
		return
	}
	v = pv
	if v.Type() == objectPtrType {
		d.typeError(kindName(src), objectType)
		return
	}
	switch x := src.(type) {
	case bool:
		switch v.Kind() {
		case reflect.Bool:
			v.SetBool(x)
		case reflect.Interface:
			if v.NumMethod() == 0 {
				v.Set(reflect.ValueOf(x))
				return
			}
			d.typeError("bool", v.Type())
		default:
			if fromQuoted {
				d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", formatLiteral(src), v.Type()))
				return
			}
			d.typeError("bool", v.Type())
		}
	case string:
		switch v.Kind() {
		case reflect.Slice:
			if v.Type().Elem().Kind() != reflect.Uint8 {
				d.typeError("string", v.Type())
				return
			}
			b, err := base64.StdEncoding.DecodeString(x)
			if err != nil {
				d.saveError(err)
				return
			}
			v.SetBytes(b)
		case reflect.String:
			if v.Type() == numberType && !isValidNumber(x) {
				d.saveError(fmt.Errorf("json: invalid number literal, trying to unmarshal %q into Number", Quote(x)))
				return
			}
			v.SetString(x)
		case reflect.Interface:
			if v.NumMethod() == 0 {
				v.Set(reflect.ValueOf(x))
				return
			}
			d.typeError("string", v.Type())
		default:
			d.typeError("string", v.Type())
		}
	case float64:
		text := formatNumber(x)
		switch v.Kind() {
		case reflect.Interface:
			if v.NumMethod() == 0 {
				v.Set(reflect.ValueOf(x))
				return
			}
			d.typeError("number "+text, v.Type())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if x != math.Trunc(x) || math.IsInf(x, 0) || x < -9.223372036854775808e18 || x >= 9.223372036854775808e18 || v.OverflowInt(int64(x)) {
				d.typeError("number "+text, v.Type())
				return
			}
			v.SetInt(int64(x))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			if x != math.Trunc(x) || x < 0 || math.IsInf(x, 0) || x >= 1.8446744073709551616e19 || v.OverflowUint(uint64(x)) {
				d.typeError("number "+text, v.Type())
				return
			}
			v.SetUint(uint64(x))
		case reflect.Float32, reflect.Float64:
			if v.OverflowFloat(x) {
				d.typeError("number "+text, v.Type())
				return
			}
			v.SetFloat(x)
		case reflect.String:
			if v.Type() == numberType {
				v.SetString(text)
				return
			}
			if fromQuoted {
				d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", text, v.Type()))
				return
			}
			d.typeError("number "+text, v.Type())
		default:
			d.typeError("number "+text, v.Type())
		}
	}
}

func formatLiteral(v any) string {
	s, _ := Stringify(v)
	return s
}

func isValidNumber(s string) bool {
	if s == "" || (s[0] != '-' && !isDigit(s[0])) {
		return false
	}
	p := parser{data: []byte(s)}
	return p.scanNumber() && p.pos == len(s)
}

func (d *decoder) array(src []any, v reflect.Value) {
	od, u, ut, pv := indirect(v, false)
	if od != nil {
		od.jsonxDecodeOpt(d, src)
		return
	}
	if u != nil {
		d.callUnmarshaler(u, src)
		return
	}
	if ut != nil {
		d.typeError("array", v.Type())
		return
	}
	v = pv
	if v.Type() == objectPtrType {
		d.typeError("array", objectType)
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.NumMethod() == 0 {
			v.Set(reflect.ValueOf(src))
			return
		}
		d.typeError("array", v.Type())
		return
	case reflect.Array, reflect.Slice:
	default:
		d.typeError("array", v.Type())
		return
	}
	i := 0
	for _, el := range src {
		if v.Kind() == reflect.Slice {
			if i >= v.Cap() {
				v.Grow(1)
			}
			if i >= v.Len() {
				v.SetLen(i + 1)
			}
		}
		if i < v.Len() {
			d.value(el, v.Index(i))
		}
		i++
	}
	if i < v.Len() {
		if v.Kind() == reflect.Array {
			for ; i < v.Len(); i++ {
				v.Index(i).SetZero()
			}
		} else {
			v.SetLen(i)
		}
	}
	if i == 0 && v.Kind() == reflect.Slice {
		v.Set(reflect.MakeSlice(v.Type(), 0, 0))
	}
}

func (d *decoder) object(src *Object, v reflect.Value) {
	od, u, ut, pv := indirect(v, false)
	if od != nil {
		od.jsonxDecodeOpt(d, src)
		return
	}
	if u != nil {
		d.callUnmarshaler(u, src)
		return
	}
	if ut != nil {
		d.typeError("object", v.Type())
		return
	}
	v = pv
	t := v.Type()
	if t == objectPtrType {
		if v.CanSet() {
			v.Set(reflect.ValueOf(src))
		} else if !v.IsNil() {
			v.Elem().Set(reflect.ValueOf(*src.Clone()))
		}
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.NumMethod() == 0 {
			v.Set(reflect.ValueOf(src))
			return
		}
		d.typeError("object", t)
	case reflect.Map:
		d.mapValue(src, v)
	case reflect.Struct:
		d.structValue(src, v)
	default:
		d.typeError("object", t)
	}
}

func (d *decoder) mapValue(src *Object, v reflect.Value) {
	t := v.Type()
	kt := t.Key()
	switch kt.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
	default:
		if !reflect.PointerTo(kt).Implements(textUnmarshalerType) {
			d.typeError("object", t)
			return
		}
	}
	if v.IsNil() {
		v.Set(reflect.MakeMap(t))
	}
	elem := reflect.New(t.Elem()).Elem()
	for i := 0; i < len(src.entries); i++ {
		key := src.entries[i].key
		elem.SetZero()
		d.value(src.entries[i].val, elem)
		var kv reflect.Value
		switch {
		case reflect.PointerTo(kt).Implements(textUnmarshalerType):
			kv = reflect.New(kt)
			if err := kv.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(key)); err != nil {
				d.saveError(err)
				continue
			}
			kv = kv.Elem()
		case kt.Kind() == reflect.String:
			kv = reflect.New(kt).Elem()
			kv.SetString(key)
		default:
			switch kt.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				n, err := strconv.ParseInt(key, 10, 64)
				if err != nil || kt.OverflowInt(n) {
					d.typeError("number "+key, kt)
					continue
				}
				kv = reflect.New(kt).Elem()
				kv.SetInt(n)
			default:
				n, err := strconv.ParseUint(key, 10, 64)
				if err != nil || kt.OverflowUint(n) {
					d.typeError("number "+key, kt)
					continue
				}
				kv = reflect.New(kt).Elem()
				kv.SetUint(n)
			}
		}
		v.SetMapIndex(kv, elem)
	}
}

func (d *decoder) structValue(src *Object, v reflect.Value) {
	t := v.Type()
	fields := cachedTypeFields(t)
	savedStruct := d.errStruct
	savedDepth := len(d.fieldStack)
	for i := 0; i < len(src.entries); i++ {
		f := fields.lookup(src.entries[i].key)
		if f == nil {
			continue
		}
		subv := v
		for _, idx := range f.index {
			if subv.Kind() == reflect.Pointer {
				if subv.IsNil() {
					if !subv.CanSet() {
						d.saveError(fmt.Errorf("json: cannot set embedded pointer to unexported struct: %v", subv.Type().Elem()))
						subv = reflect.Value{}
						break
					}
					subv.Set(reflect.New(subv.Type().Elem()))
				}
				subv = subv.Elem()
			}
			subv = subv.Field(idx)
		}
		if !subv.IsValid() {
			continue
		}
		d.fieldStack = append(d.fieldStack, f.name)
		d.errStruct = t
		if f.quoted {
			d.quotedValue(src.entries[i].val, subv)
		} else {
			d.value(src.entries[i].val, subv)
		}
		d.fieldStack = d.fieldStack[:savedDepth]
		d.errStruct = savedStruct
	}
}

// quotedValue decodes a field tagged `,string`: the JSON value is a string holding a
// literal.
func (d *decoder) quotedValue(src any, v reflect.Value) {
	switch s := src.(type) {
	case nil:
		d.null(v)
	case string:
		inner, err := Parse([]byte(s))
		if err != nil {
			d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", Quote(s), v.Type()))
			return
		}
		switch inner.(type) {
		case nil:
			d.null(v)
		case bool, float64, string:
			d.literal(inner, v, true)
		default:
			d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", Quote(s), v.Type()))
		}
	default:
		d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal unquoted value into %v", v.Type()))
	}
}
