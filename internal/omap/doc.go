// Package omap provides insertion-ordered Map and Set types with JavaScript
// Map/Set iteration semantics (PORTING.md section 6.4).
//
// Iteration visits entries in insertion order. Deleting an entry during
// iteration skips it if it has not been visited yet, and entries added during
// iteration are visited, exactly like a JS for-of loop over a Map or Set.
//
// A Map with string (or integer) keys marshals to a JSON object in JS
// property order: array-index keys ("0", "1", ...) ascending first, then the
// remaining keys in insertion order, which is what JSON.stringify produces for
// the plain object a TS Record represents. Unmarshalling inserts keys in the
// order JSON.parse would expose them (Object.keys order).
//
// MarshalJSON does not HTML-escape '<', '>' or '&' (JSON.stringify parity),
// but encoding/json.Marshal re-escapes any Marshaler output; encode with a
// json.Encoder and SetEscapeHTML(false) to get byte-identical output.
//
// The types are not safe for concurrent use.
package omap
