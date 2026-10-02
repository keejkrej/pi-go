package jsdiff

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// JsonDiff is the jsdiff jsonDiff instance (class JsonDiff) for string inputs; DiffJson
// serializes non-string inputs first.
var JsonDiff = &Diff{
	Tokenize: tokenizeLines,
	Equals:   jsonEquals,
	// Discriminate between two lines of pretty-printed, serialized JSON where one of them
	// has a dangling comma and the other doesn't. Turns out including the dangling comma
	// yields the nicest output.
	UseLongestToken: true,
}

func jsonEquals(left, right string, options *Options) bool {
	return BaseEquals(stripCommaBeforeNewline(left), stripCommaBeforeNewline(right), options)
}

// stripCommaBeforeNewline is s.replace(/,([\r\n])/g, '$1').
func stripCommaBeforeNewline(s string) string {
	if !strings.Contains(s, ",") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == ',' && i+1 < len(s) && (s[i+1] == '\n' || s[i+1] == '\r') {
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// DiffJson diffs two JSON-serializable values. Strings are used as they are; other values
// are canonicalized (object keys sorted, see Canonicalize) and serialized with
// JSON.stringify(value, null, '  ') before a line diff that ignores trailing commas.
//
// Errors are the ones JSON.stringify or the line tokenizer would throw (for example a
// circular structure). It returns nil changes if MaxEditLength or Timeout was exceeded.
// TS options.undefinedReplacement has no Go counterpart: the jsonx value model has no
// undefined, so the default replacer is the identity.
func DiffJson(oldObj, newObj any, options *Options) ([]Change, error) {
	if options == nil {
		options = &Options{}
	}
	oldString, err := jsonCastInput(oldObj, options)
	if err != nil {
		return nil, err
	}
	newString, err := jsonCastInput(newObj, options)
	if err != nil {
		return nil, err
	}
	d := JsonDiff
	oldTokens := d.removeEmpty(d.tokenize(oldString, options))
	newTokens := d.removeEmpty(d.tokenize(newString, options))
	return d.diffWithOptionsObj(oldTokens, newTokens, options), nil
}

func jsonCastInput(value any, options *Options) (string, error) {
	if s, ok := value.(string); ok {
		return s, nil
	}
	canonical, keep, err := canonicalize(value, nil, nil, options.StringifyReplacer, "")
	if err != nil {
		return "", err
	}
	if !keep {
		// JSON.stringify(undefined) is undefined, which the line tokenizer then throws on.
		if options.StripTrailingCr {
			return "", errors.New("Cannot read properties of undefined (reading 'replace')")
		}
		return "", errors.New("Cannot read properties of undefined (reading 'split')")
	}
	return jsonx.StringifyIndent(canonical, "  ")
}

// Canonicalize returns a copy of obj with object keys in sorted order (JS default sort,
// then JS property order, which puts integer-like keys first). It handles circular
// references by reusing the canonical object already being built for an object that is
// on the current stack (so a circular input stays circular). The replacer is optional and
// is applied to every value like JSON.stringify's (key "" for the root); keep=false
// means undefined. Typed Go values are converted with jsonx.Encode (TS: toJSON).
//
// Go has no undefined: where TS stores undefined, an object property is left out and an
// array element is nil (both serialize as JSON.stringify writes them); an undefined
// result is (nil, nil).
func Canonicalize(obj any, replacer func(key string, value any) (replaced any, keep bool)) (any, error) {
	v, _, err := canonicalize(obj, nil, nil, replacer, "")
	return v, err
}

// canonicalize returns keep=false for undefined.
func canonicalize(obj any, stack, replacementStack []any, replacer func(string, any) (any, bool), key string) (any, bool, error) {
	if replacer != nil {
		v, keep := replacer(key, obj)
		if !keep {
			return nil, false, nil
		}
		obj = v
	}
	for i := range stack {
		if sameReference(stack[i], obj) {
			return replacementStack[i], true, nil
		}
	}
	switch obj.(type) {
	case nil, bool, float64, string, *jsonx.Object, []any:
	default:
		// toJSON: typed Go values become jsonx values.
		v, err := jsonx.Encode(obj)
		if err != nil {
			return nil, false, err
		}
		obj = v
	}
	if arr, ok := obj.([]any); ok && arr != nil {
		canonicalized := make([]any, len(arr))
		stack = append(stack, arr)
		replacementStack = append(replacementStack, canonicalized)
		for i, item := range arr {
			v, _, err := canonicalize(item, stack, replacementStack, replacer, strconv.Itoa(i))
			if err != nil {
				return nil, false, err
			}
			canonicalized[i] = v
		}
		return canonicalized, true, nil
	}
	if o, ok := obj.(*jsonx.Object); ok && o != nil {
		canonicalized := jsonx.NewObject()
		stack = append(stack, o)
		replacementStack = append(replacementStack, canonicalized)
		sortedKeys := o.Keys()
		slices.SortStableFunc(sortedKeys, compareUTF16)
		for _, k := range sortedKeys {
			item, _ := o.Get(k)
			v, keep, err := canonicalize(item, stack, replacementStack, replacer, k)
			if err != nil {
				return nil, false, err
			}
			if keep {
				canonicalized.Set(k, v)
			}
		}
		return canonicalized, true, nil
	}
	return obj, true, nil
}

// sameReference is JS === for the reference types of the jsonx value model.
func sameReference(a, b any) bool {
	switch x := a.(type) {
	case *jsonx.Object:
		y, ok := b.(*jsonx.Object)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		return ok && len(x) > 0 && len(x) == len(y) && &x[0] == &y[0]
	}
	return false
}

// compareUTF16 is the default Array.prototype.sort comparison (UTF-16 code units).
func compareUTF16(a, b string) int {
	ua, ub := toUTF16(a), toUTF16(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			if ua[i] < ub[i] {
				return -1
			}
			return 1
		}
	}
	return len(ua) - len(ub)
}
