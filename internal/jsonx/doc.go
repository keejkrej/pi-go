// Package jsonx implements JSON with the exact semantics of V8 (Node 24) JSON.parse and
// JSON.stringify, so that every byte pi-go writes equals what TypeScript pi writes.
//
// Value model. Untyped JSON values are nil (null), bool, float64, string, []any and *Object.
// Object keeps JavaScript property order: array-index keys ("0" .. "4294967294") first in
// ascending numeric order, then all other keys in insertion order.
//
// Strings. Go strings stand in for JavaScript strings. A lone UTF-16 surrogate, which a JS
// string can hold but UTF-8 cannot, is represented by its generalized UTF-8 (WTF-8) three-byte
// encoding (ED A0..BF 80..BF). Parse produces that form for unpaired \uD800-\uDFFF escapes, and
// Stringify writes it back as a lowercase \udxxx escape, exactly like JSON.stringify. Other
// invalid UTF-8 is treated as U+FFFD, one replacement per maximal invalid subsequence (the
// WHATWG decoder rule Node uses when it decodes UTF-8 text).
//
// Typed values. Marshal, Stringify and Decode also handle Go structs, maps, slices and
// pointers with encoding/json struct tags (including omitzero, omitempty, string and "-"),
// json.Marshaler, json.Unmarshaler, encoding.TextMarshaler and encoding.TextUnmarshaler.
// Output always follows the JSON.stringify rules: no HTML escaping, raw U+2028/U+2029,
// ES Number.prototype.toString number formatting, NaN and ±Infinity as null, -0 as 0,
// functions treated as undefined, time.Time written as Date.prototype.toJSON does.
// Go integers are JS numbers: beyond ±2^53 they are written as the nearest double.
// Pointer-receiver MarshalJSON/MarshalText methods are used even on non-addressable values.
//
// Nesting. Like V8, Parse accepts any depth (it keeps an explicit stack). Stringify and
// Marshal fail with a *RangeError ("Maximum call stack size exceeded") beyond 6185 nested
// containers, the depth node v24 reaches from a shallow stack.
package jsonx
